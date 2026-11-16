package api

import (
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"eko/internal/bus"
	"eko/internal/ca"
	"eko/internal/config"
	"eko/internal/flow"
	"eko/internal/intercept"
	"eko/internal/project"
	"eko/internal/proxy"
)

type Server struct {
	token      string
	version    string
	started    time.Time
	box        *config.Manager
	projects   *project.Manager
	bus        *bus.Bus
	ic         *intercept.Manager
	px         *proxy.Proxy
	ca         *ca.CA
	onShutdown func()
}

func New(token, version string, box *config.Manager, projects *project.Manager, b *bus.Bus, ic *intercept.Manager, px *proxy.Proxy, c *ca.CA, onShutdown func()) *Server {
	return &Server{token: token, version: version, started: time.Now(), box: box, projects: projects, bus: b, ic: ic, px: px, ca: c, onShutdown: onShutdown}
}

func (a *Server) store() *flow.Store { return a.projects.Store() }

func (a *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/healthz", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("GET /api/stats", a.stats)
	mux.HandleFunc("GET /api/settings", a.getSettings)
	mux.HandleFunc("PUT /api/settings", a.putSettings)
	mux.HandleFunc("GET /api/flows", a.listFlows)
	mux.HandleFunc("DELETE /api/flows", a.clearFlows)
	mux.HandleFunc("GET /api/flows/{id}", a.getFlow)
	mux.HandleFunc("DELETE /api/flows/{id}", a.deleteFlow)
	mux.HandleFunc("PUT /api/flows/{id}/comment", a.commentFlow)
	mux.HandleFunc("GET /api/flows/{id}/body/{kind}", a.getBody)
	mux.HandleFunc("POST /api/repeater", a.repeater)
	mux.HandleFunc("GET /api/intercept", a.listIntercept)
	mux.HandleFunc("POST /api/intercept", a.resolveInterceptByBody)
	mux.HandleFunc("POST /api/intercept/config", a.interceptConfig)
	mux.HandleFunc("POST /api/intercept/{id}", a.resolveIntercept)
	mux.HandleFunc("GET /api/projects", a.listProjects)
	mux.HandleFunc("POST /api/projects", a.createProject)
	mux.HandleFunc("POST /api/projects/select", a.selectProject)
	mux.HandleFunc("PUT /api/projects/{id}", a.updateProject)
	mux.HandleFunc("DELETE /api/projects/{id}", a.deleteProject)
	mux.HandleFunc("GET /api/events", a.events)
	mux.HandleFunc("POST /api/shutdown", a.shutdown)
	mux.HandleFunc("GET /api/ca.pem", a.caPEM)
	mux.HandleFunc("POST /api/ca/import", a.caImport)
	mux.Handle("/", a.ui())
	return a.auth(mux)
}

func (a *Server) auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/healthz" {
			next.ServeHTTP(w, r)
			return
		}
		ok := false
		if c, err := r.Cookie("eko_token"); err == nil {
			ok = subtle.ConstantTimeCompare([]byte(c.Value), []byte(a.token)) == 1
		}
		if !ok && r.Header.Get("X-Eko-Token") == a.token {
			ok = true
		}
		if !ok && r.URL.Query().Get("token") == a.token {
			http.SetCookie(w, &http.Cookie{
				Name:     "eko_token",
				Value:    a.token,
				Path:     "/",
				HttpOnly: true,
				SameSite: http.SameSiteStrictMode,
			})
			q := r.URL.Query()
			q.Del("token")
			u := *r.URL
			u.RawQuery = q.Encode()
			http.Redirect(w, r, u.String(), http.StatusSeeOther)
			return
		}
		if !ok {
			if strings.HasPrefix(r.URL.Path, "/api/") {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte(`<!doctype html><meta charset="utf-8"><title>Eko</title>
<body style="background:#0f1115;color:#d7dce5;font:14px system-ui;padding:40px">
<h1 style="font-weight:600">eko</h1>
<p>This control UI is bound to loopback and needs a session token.</p>
<p>Start it with the <code>eko</code> launcher, or open the URL printed by
<code>eko url</code>.</p></body>`))
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (a *Server) ui() http.Handler {
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		panic(err)
	}
	fileServer := http.FileServerFS(sub)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, "/")
		if name == "" {
			name = "index.html"
		}
		if _, err := fs.Stat(sub, name); err != nil {
			rr := r.Clone(r.Context())
			rr.URL.Path = "/"
			w.Header().Set("Cache-Control", "no-cache")
			fileServer.ServeHTTP(w, rr)
			return
		}
		if name == "index.html" {
			w.Header().Set("Cache-Control", "no-cache")
		} else {
			w.Header().Set("Cache-Control", "public, max-age=3600")
		}
		fileServer.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func (a *Server) stats(w http.ResponseWriter, r *http.Request) {
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	s := a.box.Get()
	st := a.store()
	var storeStats any
	if st != nil {
		storeStats = st.Stats()
	}
	writeJSON(w, 200, map[string]any{
		"version":    a.version,
		"uptimeSec":  int64(time.Since(a.started).Seconds()),
		"goroutines": runtime.NumGoroutine(),
		"heapMB":     float64(ms.HeapAlloc) / (1 << 20),
		"sysMB":      float64(ms.Sys) / (1 << 20),
		"proxyAddr":  s.ProxyAddr,
		"apiAddr":    s.APIAddr,
		"caPath":     filepath.Join(a.ca.Dir(), "ca.pem"),
		"dataDir":    config.DataDir(),
		"store":      storeStats,
		"project":    a.projects.Active(),
	})
}

func (a *Server) getSettings(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, a.box.Get())
}

func (a *Server) putSettings(w http.ResponseWriter, r *http.Request) {
	var s config.Settings
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	if err := json.NewDecoder(r.Body).Decode(&s); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	if err := a.box.Set(s); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	writeJSON(w, 200, a.box.Get())
}

func (a *Server) listFlows(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	f := flow.Filter{
		Query:    q.Get("q"),
		Host:     q.Get("host"),
		Method:   q.Get("method"),
		Status:   q.Get("status"),
		Repeater: q.Get("repeater") == "1",
		Limit:    limit,
	}
	st := a.store()
	if st == nil { http.Error(w, "no active project", 503); return }
	flows, err := st.List(f)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	writeJSON(w, 200, map[string]any{"flows": flows})
}

func (a *Server) clearFlows(w http.ResponseWriter, r *http.Request) {
	st := a.store()
	if st == nil { http.Error(w, "no active project", 503); return }
	if err := st.Clear(); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (a *Server) getFlow(w http.ResponseWriter, r *http.Request) {
	st := a.store()
	if st == nil { http.Error(w, "no active project", 503); return }
	f, err := st.Get(r.PathValue("id"))
	if err != nil {
		http.Error(w, "not found", 404)
		return
	}
	writeJSON(w, 200, f)
}

func (a *Server) deleteFlow(w http.ResponseWriter, r *http.Request) {
	st := a.store()
	if st == nil { http.Error(w, "no active project", 503); return }
	if err := st.Delete(r.PathValue("id")); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (a *Server) commentFlow(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Comment string `json:"comment"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	st := a.store()
	if st == nil { http.Error(w, "no active project", 503); return }
	if err := st.UpdateComment(r.PathValue("id"), body.Comment); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

func headerValue(h flow.Headers, key string) string {
	for _, kv := range h {
		if strings.EqualFold(kv[0], key) {
			return kv[1]
		}
	}
	return ""
}

func (a *Server) getBody(w http.ResponseWriter, r *http.Request) {
	st := a.store()
	if st == nil { http.Error(w, "no active project", 503); return }
	f, err := st.Get(r.PathValue("id"))
	if err != nil {
		http.Error(w, "not found", 404)
		return
	}
	kind := r.PathValue("kind")
	var data []byte
	var msg *flow.Message
	if kind == "req" {
		data = f.ReqBody
		msg = &f.Req
	} else if kind == "resp" {
		data = f.RespBody
		msg = f.Resp
	} else {
		http.Error(w, "bad kind", 400)
		return
	}
	enc := ""
	truncated := false
	if msg != nil {
		enc = headerValue(msg.Headers, "Content-Encoding")
		truncated = msg.Truncated
	}
	if r.URL.Query().Get("raw") != "1" {
		if decoded, ok := flow.DecodeBody(data, enc); ok {
			data = decoded
			w.Header().Set("X-Eko-Decoded", "1")
		}
	}
	if enc != "" {
		w.Header().Set("X-Eko-Encoding", enc)
	}
	if truncated {
		w.Header().Set("X-Eko-Truncated", "1")
	}
	w.Header().Set("X-Eko-Body-Len", strconv.Itoa(len(data)))
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	if flow.LooksText(data) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	} else {
		w.Header().Set("Content-Type", "application/octet-stream")
	}
	_, _ = w.Write(data)
}

func (a *Server) repeater(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Raw string `json:"raw"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<20)
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	f, err := a.px.Repeat(body.Raw)
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	writeJSON(w, 200, map[string]any{"flow": f.Summary()})
}

func (a *Server) listIntercept(w http.ResponseWriter, r *http.Request) {
	s := a.box.Get()
	writeJSON(w, 200, map[string]any{
		"pending":  a.ic.List(),
		"request":  s.InterceptRequest,
		"response": s.InterceptResponse,
	})
}

func (a *Server) resolveIntercept(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	a.resolve(w, r, id)
}

func (a *Server) resolveInterceptByBody(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.ID == "" {
		http.Error(w, "id required", 400)
		return
	}
	a.resolve(w, r, body.ID)
}

func (a *Server) resolve(w http.ResponseWriter, r *http.Request, id string) {
	pending, ok := a.ic.Get(id)
	if !ok {
		http.Error(w, "no such pending item", 409)
		return
	}
	var body struct {
		Action string `json:"action"`
		Raw    string `json:"raw"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	if body.Action != "forward" && body.Action != "drop" {
		http.Error(w, "action must be forward or drop", 400)
		return
	}
	dec := intercept.Decision{Action: body.Action}
	if body.Action == "forward" && strings.TrimSpace(body.Raw) != "" {
		if pending.Kind == "request" {
			method, rawURL, host, headers, data, err := flow.ParseRawRequest(body.Raw)
			if err != nil {
				http.Error(w, "parse request: "+err.Error(), 400)
				return
			}
			dec.Method = method
			dec.URL = rawURL
			dec.Host = host
			dec.Headers = headers
			dec.Body = data
			dec.BodySet = true
		} else {
			status, headers, data, err := flow.ParseRawResponse(body.Raw)
			if err != nil {
				http.Error(w, "parse response: "+err.Error(), 400)
				return
			}
			dec.Status = status
			dec.Headers = headers
			dec.Body = data
			dec.BodySet = true
		}
	}
	if !a.ic.Resolve(id, dec) {
		http.Error(w, "already resolved", 409)
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (a *Server) interceptConfig(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Request  *bool `json:"request"`
		Response *bool `json:"response"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	s := a.box.Get()
	if body.Request != nil {
		s.InterceptRequest = *body.Request
	}
	if body.Response != nil {
		s.InterceptResponse = *body.Response
	}
	if err := a.box.Set(s); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	writeJSON(w, 200, a.box.Get())
}

func (a *Server) events(w http.ResponseWriter, r *http.Request) {
	fl, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", 500)
		return
	}
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("Connection", "keep-alive")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(200)

	id, ch := a.bus.Subscribe()
	defer a.bus.Unsubscribe(id)

	fmt.Fprint(w, ": connected\n\n")
	fl.Flush()

	ticker := time.NewTicker(20 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				return
			}
			b, err := json.Marshal(ev)
			if err != nil {
				continue
			}
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.Type, b)
			fl.Flush()
		case <-ticker.C:
			fmt.Fprint(w, ": ping\n\n")
			fl.Flush()
		case <-r.Context().Done():
			return
		}
	}
}

// shutdown lets the UI turn the proxy off without a terminal.
func (a *Server) shutdown(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{"ok": true})
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
	if a.onShutdown != nil {
		// Give the response a moment to reach the browser before the server dies.
		time.AfterFunc(150*time.Millisecond, a.onShutdown)
	}
}

func (a *Server) caPEM(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/x-pem-file")
	w.Header().Set("Content-Disposition", `attachment; filename="eko-ca.pem"`)
	_, _ = w.Write(a.ca.PEM())
}

func (a *Server) caImport(w http.ResponseWriter, r *http.Request) {
	dirs := nssDirs()
	type result struct {
		DB     string `json:"db"`
		OK     bool   `json:"ok"`
		Output string `json:"output"`
	}
	var out []result
	if _, err := exec.LookPath("certutil"); err != nil {
		writeJSON(w, 200, map[string]any{"results": out, "error": "certutil not found (install nss)"})
		return
	}
	caPath := filepath.Join(a.ca.Dir(), "ca.pem")
	for _, dir := range dirs {
		output, err := importInto(dir, caPath)
		out = append(out, result{DB: dir, OK: err == nil, Output: strings.TrimSpace(output)})
	}
	writeJSON(w, 200, map[string]any{"results": out})
}

func nssDirs() []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	globs := []string{
		filepath.Join(home, ".mozilla/firefox/*"),
		filepath.Join(home, "snap/firefox/common/.mozilla/firefox/*"),
		filepath.Join(home, ".var/app/org.mozilla.firefox/.mozilla/firefox/*"),
	}
	var out []string
	for _, g := range globs {
		matches, _ := filepath.Glob(g)
		for _, m := range matches {
			if _, err := os.Stat(filepath.Join(m, "cert9.db")); err == nil {
				out = append(out, m)
			}
		}
	}
	pki := filepath.Join(home, ".pki/nssdb")
	if _, err := os.Stat(filepath.Join(pki, "cert9.db")); err == nil {
		out = append(out, pki)
	} else if err := os.MkdirAll(pki, 0o700); err == nil {
		if _, lerr := exec.LookPath("certutil"); lerr == nil {
			out2, err := exec.Command("certutil", "-N", "--empty-password", "-d", "sql:"+pki).CombinedOutput()
			if err == nil {
				out = append(out, pki)
			} else {
				_ = out2
			}
		}
	}
	return out
}


func (a *Server) listProjects(w http.ResponseWriter, r *http.Request) {
	active, projects := a.projects.List()
	writeJSON(w, 200, map[string]any{"active": active, "projects": projects})
}

func (a *Server) createProject(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name   string `json:"name"`
		Switch bool   `json:"switch"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	pj, err := a.projects.Create(body.Name)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	if body.Switch {
		if err := a.projects.Switch(pj.ID); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		a.bus.Publish("project", a.projects.Active())
	}
	writeJSON(w, 200, pj)
}

func (a *Server) selectProject(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.ID == "" {
		http.Error(w, "id required", 400)
		return
	}
	if err := a.projects.Switch(body.ID); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	a.bus.Publish("project", a.projects.Active())
	writeJSON(w, 200, a.projects.Active())
}

func (a *Server) updateProject(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var body struct {
		Name         *string  `json:"name"`
		ScopeInclude *[]string `json:"scopeInclude"`
		ScopeExclude *[]string `json:"scopeExclude"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	if body.Name != nil {
		if err := a.projects.Rename(id, *body.Name); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
	}
	if body.ScopeInclude != nil || body.ScopeExclude != nil {
		_, ps := a.projects.List()
		var curIn, curEx []string
		for _, p := range ps {
			if p.ID == id {
				curIn, curEx = p.ScopeIn, p.ScopeEx
			}
		}
		in, ex := curIn, curEx
		if body.ScopeInclude != nil {
			in = *body.ScopeInclude
		}
		if body.ScopeExclude != nil {
			ex = *body.ScopeExclude
		}
		if err := a.projects.UpdateScope(id, in, ex); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
	}
	a.bus.Publish("project", a.projects.Active())
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (a *Server) deleteProject(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := a.projects.Delete(id); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

func importInto(dir, caPath string) (string, error) {
	args := []string{"-d", "sql:" + dir, "-A", "-t", "C,,", "-n", "Eko Root CA", "-i", caPath}
	out, err := exec.Command("certutil", args...).CombinedOutput()
	if err != nil && strings.Contains(strings.ToLower(string(out)), "already exists") {
		_, _ = exec.Command("certutil", "-d", "sql:"+dir, "-D", "-n", "Eko Root CA").CombinedOutput()
		out, err = exec.Command("certutil", args...).CombinedOutput()
	}
	return string(out), err
}
