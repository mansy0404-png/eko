package proxy

import (
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"eko/internal/bus"
	"eko/internal/ca"
	"eko/internal/config"
	"eko/internal/flow"
	"eko/internal/glob"
	"eko/internal/intercept"
	"eko/internal/project"
)

type Proxy struct {
	ca       *ca.CA
	projects *project.Manager
	bus      *bus.Bus
	ic       *intercept.Manager
	box      *config.Manager

	mu            sync.Mutex
	tr            *http.Transport
	tunnels       []glob.Matcher
	recordExclude []glob.Matcher
}

func New(c *ca.CA, projects *project.Manager, b *bus.Bus, ic *intercept.Manager, box *config.Manager) *Proxy {
	p := &Proxy{ca: c, projects: projects, bus: b, ic: ic, box: box}
	p.Sync(box.Get())
	return p
}

func (p *Proxy) store() *flow.Store { return p.projects.Store() }

// Sync applies live settings: rule matchers and the upstream transport.
func (p *Proxy) Sync(s config.Settings) {
	tunnels := glob.Compile(s.TunnelHosts)
	excl := glob.Compile(s.RecordExclude)

	tr := &http.Transport{
		Proxy:                 nil,
		DialContext:           (&net.Dialer{Timeout: 15 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		ForceAttemptHTTP2:     false, // keep upstream HTTP/1.1 so raw editing stays sane
		MaxIdleConns:          256,
		MaxIdleConnsPerHost:   16,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   15 * time.Second,
		ExpectContinueTimeout: time.Second,
		ResponseHeaderTimeout: 180 * time.Second,
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: s.UpstreamInsecure,
			MinVersion:         tls.VersionTLS12,
		},
	}

	p.mu.Lock()
	old := p.tr
	p.tr = tr
	p.tunnels = tunnels
	p.recordExclude = excl
	p.mu.Unlock()
	if old != nil {
		old.CloseIdleConnections()
	}
}

func (p *Proxy) transport() *http.Transport {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.tr
}

func (p *Proxy) matchers() ([]glob.Matcher, []glob.Matcher) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.tunnels, p.recordExclude
}

func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodConnect {
		p.handleConnect(w, r)
		return
	}
	p.serve(w, r, "http")
}

func (p *Proxy) handleConnect(w http.ResponseWriter, r *http.Request) {
	hj, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "eko: connection hijacking unsupported", http.StatusInternalServerError)
		return
	}
	conn, _, err := hj.Hijack()
	if err != nil {
		return
	}
	if _, err := conn.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n")); err != nil {
		conn.Close()
		return
	}

	tunnels, _ := p.matchers()
	if glob.Match(tunnels, r.Host) {
		p.tunnel(conn, r.Host)
		return
	}

	tlsConn := tls.Server(conn, p.ca.TLSConfig(r.Host))
	_ = tlsConn.SetDeadline(time.Now().Add(20 * time.Second))
	if err := tlsConn.Handshake(); err != nil {
		conn.Close()
		return
	}
	_ = tlsConn.SetDeadline(time.Time{})

	srv := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, rr *http.Request) {
			p.serve(w, rr, "https")
		}),
		ReadHeaderTimeout: 30 * time.Second,
		IdleTimeout:       120 * time.Second,
		ErrorLog:          log.New(io.Discard, "", 0),
	}
	l := &connListener{closed: make(chan struct{})}
	tc := &trackedConn{Conn: tlsConn, onClose: l.closeOnce}
	l.conn = tc
	_ = srv.Serve(l)
	conn.Close()
}

func (p *Proxy) tunnel(client net.Conn, host string) {
	if _, _, err := net.SplitHostPort(host); err != nil {
		host = net.JoinHostPort(host, "443")
	}
	up, err := net.DialTimeout("tcp", host, 15*time.Second)
	if err != nil {
		client.Close()
		return
	}
	done := make(chan struct{}, 2)
	go func() { io.Copy(up, client); done <- struct{}{} }()
	go func() { io.Copy(client, up); done <- struct{}{} }()
	<-done
	client.Close()
	up.Close()
}

func (p *Proxy) serve(w http.ResponseWriter, r *http.Request, scheme string) {
	s := p.box.Get()
	start := time.Now()

	hostHeader := r.Host
	if hostHeader == "" {
		hostHeader = r.URL.Host
	}
	host, port := splitHostPort(hostHeader, scheme)
	path := r.URL.Path
	if path == "" {
		path = "/"
	}
	target := scheme + "://" + hostHeader + r.URL.RequestURI()

	f := &flow.Flow{
		ID:        flow.NewID(),
		StartedAt: start,
		Scheme:    scheme,
		Host:      host,
		Port:      port,
		Method:    r.Method,
		Path:      path,
		Query:     r.URL.RawQuery,
		URL:       target,
		Proto:     r.Proto,
		Client:    r.RemoteAddr,
	}

	body, rest, truncated, err := flow.CaptureBody(r.Body, s.MaxBodyCapture)
	if err != nil {
		p.fail(w, f, false, http.StatusBadGateway, "reading request body: "+err.Error())
		return
	}
	f.Req = flow.Message{
		Headers:   flow.HeaderList(r.Header),
		BodyLen:   flow.BodyLen(len(body), r.ContentLength, truncated),
		Truncated: truncated,
	}
	f.ReqBody = body

	method := r.Method
	urlStr := target
	hdr := r.Header.Clone()
	hostOverride := ""
	var outBody io.Reader = bytes.NewReader(body)
	contentLength := int64(len(body))
	if truncated {
		outBody = io.MultiReader(bytes.NewReader(body), rest)
		contentLength = r.ContentLength
	}
	if len(body) == 0 && !truncated {
		outBody = nil
	}

	if !truncated && p.ic.Match("request", urlStr) {
		dec, held := p.ic.Hold("request", f.ID, method, urlStr, 0, flow.HeaderList(hdr), body)
		if held {
			f.Intercepted = true
			if dec.Action == "drop" {
				saved := p.save(f, s)
				p.fail(w, f, saved, http.StatusForbidden, "request dropped by eko")
				return
			}
			if dec.Method != "" {
				method = dec.Method
			}
			if dec.URL != "" {
				urlStr = dec.URL
			}
			if dec.Headers != nil {
				hdr = flow.HeaderMap(dec.Headers)
			}
			if dec.Host != "" {
				hostOverride = dec.Host
			}
			if dec.BodySet {
				body = dec.Body
				truncated = false
				outBody = bytes.NewReader(body)
				contentLength = int64(len(body))
			}
			target = urlStr
			f.Method = method
			f.URL = urlStr
			f.Req.Headers = flow.HeaderList(hdr)
			f.Req.BodyLen = int64(len(body))
			f.Req.Truncated = truncated
			f.ReqBody = body
			if u, perr := url.Parse(urlStr); perr == nil {
				f.Scheme = u.Scheme
				f.Host = u.Hostname()
				f.Port = u.Port()
				f.Path = u.Path
				f.Query = u.RawQuery
			}
		}
	}

	outReq, err := http.NewRequestWithContext(r.Context(), method, target, outBody)
	if err != nil {
		p.fail(w, f, false, http.StatusBadGateway, "building request: "+err.Error())
		return
	}
	outReq.Header = hdr
	outReq.ContentLength = contentLength
	outReq.Host = r.Host
	if u, perr := url.Parse(target); perr == nil && u.Host != hostHeader {
		outReq.Host = u.Host
	}
	if hostOverride != "" {
		outReq.Host = hostOverride
	}
	if hv := outReq.Header.Get("Host"); hv != "" {
		outReq.Host = hv
		outReq.Header.Del("Host")
	}
	upgrade := outReq.Header.Get("Upgrade") != ""
	flow.RemoveHop(outReq.Header, upgrade)
	outReq.Header.Del("Content-Length")
	if s.StripZstd {
		if ae := outReq.Header.Get("Accept-Encoding"); ae != "" {
			if stripped := stripToken(ae, "zstd"); stripped != ae {
				if stripped == "" {
					outReq.Header.Del("Accept-Encoding")
				} else {
					outReq.Header.Set("Accept-Encoding", stripped)
				}
			}
		}
	}
	outReq.RequestURI = ""

	saved := p.save(f, s)

	resp, err := p.transport().RoundTrip(outReq)
	if err != nil {
		p.fail(w, f, saved, http.StatusBadGateway, "upstream: "+err.Error())
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusSwitchingProtocols {
		p.handleUpgrade(w, f, saved, resp)
		return
	}

	f.Status = resp.StatusCode
	f.Proto = resp.Proto
	f.Resp = &flow.Message{Headers: flow.HeaderList(resp.Header)}

	bodyReader := io.Reader(resp.Body)
	bodyLen := resp.ContentLength

	if p.ic.Match("response", target) && (resp.ContentLength < 0 || resp.ContentLength <= s.MaxBodyCapture) {
		pre, leftover, trunc, _ := flow.CaptureBody(resp.Body, s.MaxBodyCapture)
		if trunc {
			bodyReader = io.MultiReader(bytes.NewReader(pre), leftover)
		} else {
			dec, held := p.ic.Hold("response", f.ID, method, target, resp.StatusCode, flow.HeaderList(resp.Header), pre)
			if held {
				f.Intercepted = true
				if dec.Action == "drop" {
					p.fail(w, f, saved, http.StatusBadGateway, "response dropped by eko")
					return
				}
				if dec.Status != 0 {
					resp.StatusCode = dec.Status
				}
				if dec.Headers != nil {
					resp.Header = flow.HeaderMap(dec.Headers)
				}
				if dec.BodySet {
					pre = dec.Body
				}
				bodyReader = bytes.NewReader(pre)
				bodyLen = int64(len(pre))
				f.Status = resp.StatusCode
				f.Resp.Headers = flow.HeaderList(resp.Header)
			} else {
				bodyReader = io.MultiReader(bytes.NewReader(pre), leftover)
			}
		}
	}

	rec := &cappedWriter{limit: s.MaxBodyCapture}
	n, werr := p.copyResponse(w, resp, bodyReader, bodyLen, rec)

	f.Resp.BodyLen = flow.BodyLen(int(n), resp.ContentLength, rec.truncated)
	f.Resp.Truncated = rec.truncated
	f.RespBody = rec.Bytes()
	f.DurationMs = time.Since(start).Milliseconds()
	if werr != nil {
		f.Err = "client: " + werr.Error()
	}
	if saved {
		if st := p.store(); st != nil {
			if err := st.Finish(f); err != nil {
				log.Printf("eko: store finish: %v", err)
			}
			p.bus.Publish("flow", f.Summary())
		}
	}
}

func (p *Proxy) copyResponse(w http.ResponseWriter, resp *http.Response, body io.Reader, bodyLen int64, rec *cappedWriter) (int64, error) {
	dst := w.Header()
	for k, vv := range resp.Header {
		if flow.IsHop(k) {
			continue
		}
		for _, v := range vv {
			dst.Add(k, v)
		}
	}
	if bodyLen >= 0 && resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusNotModified {
		dst.Set("Content-Length", strconv.FormatInt(bodyLen, 10))
	} else {
		dst.Del("Content-Length")
	}
	w.WriteHeader(resp.StatusCode)
	return copyFlush(w, body, rec)
}

func copyFlush(w http.ResponseWriter, r io.Reader, rec *cappedWriter) (int64, error) {
	flusher, _ := w.(http.Flusher)
	buf := make([]byte, 32*1024)
	var total int64
	for {
		n, err := r.Read(buf)
		if n > 0 {
			if rec != nil {
				_, _ = rec.Write(buf[:n])
			}
			if _, werr := w.Write(buf[:n]); werr != nil {
				return total, werr
			}
			total += int64(n)
			if flusher != nil {
				flusher.Flush()
			}
		}
		if err == io.EOF {
			return total, nil
		}
		if err != nil {
			return total, err
		}
	}
}

func (p *Proxy) handleUpgrade(w http.ResponseWriter, f *flow.Flow, saved bool, resp *http.Response) {
	hj, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "eko: upgrade unsupported", http.StatusInternalServerError)
		return
	}
	conn, brw, err := hj.Hijack()
	if err != nil {
		return
	}
	defer conn.Close()

	if resp.Header.Get("Upgrade") == "" {
		resp.Header.Set("Upgrade", "websocket")
	}
	if resp.Header.Get("Connection") == "" {
		resp.Header.Set("Connection", "Upgrade")
	}
	fmt.Fprintf(brw, "HTTP/1.1 101 Switching Protocols\r\n")
	_ = resp.Header.Write(brw)
	_, _ = brw.WriteString("\r\n")
	_ = brw.Flush()

	f.Status = http.StatusSwitchingProtocols
	f.Proto = resp.Proto
	f.Resp = &flow.Message{Headers: flow.HeaderList(resp.Header)}
	f.DurationMs = time.Since(f.StartedAt).Milliseconds()
	if saved {
		if st := p.store(); st != nil {
			_ = st.Finish(f)
			p.bus.Publish("flow", f.Summary())
		}
	}

	rwc, ok := resp.Body.(io.ReadWriteCloser)
	if !ok {
		return
	}
	go func() {
		_, _ = io.Copy(rwc, brw.Reader)
		_ = rwc.Close()
	}()
	_, _ = io.Copy(conn, rwc)
}

func (p *Proxy) save(f *flow.Flow, s config.Settings) bool {
	if !s.RecordEnabled {
		return false
	}
	_, excl := p.matchers()
	if glob.Match(excl, f.URL) {
		return false
	}
	st := p.store()
	if st == nil {
		return false
	}
	if err := st.Insert(f); err != nil {
		log.Printf("eko: store insert: %v", err)
		return false
	}
	p.bus.Publish("flow", f.Summary())
	return true
}

func (p *Proxy) fail(w http.ResponseWriter, f *flow.Flow, saved bool, code int, msg string) {
	f.Err = msg
	f.DurationMs = time.Since(f.StartedAt).Milliseconds()
	if saved {
		if st := p.store(); st != nil {
			if err := st.Finish(f); err != nil {
				log.Printf("eko: store finish: %v", err)
			}
			p.bus.Publish("flow", f.Summary())
		}
	}
	http.Error(w, "eko: "+msg, code)
}

// Repeat executes a Repeater request and records it as a flow.
func (p *Proxy) Repeat(raw string) (*flow.Flow, error) {
	s := p.box.Get()
	method, target, host, headers, body, err := flow.ParseRawRequest(raw)
	if err != nil {
		return nil, err
	}
	u, err := url.Parse(target)
	if err != nil {
		return nil, err
	}
	if !u.IsAbs() || u.Host == "" {
		return nil, fmt.Errorf("absolute URL required in request line")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
	defer cancel()

	outReq, err := http.NewRequestWithContext(ctx, method, target, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	outReq.Header = flow.HeaderMap(headers)
	if host != "" {
		outReq.Host = host
	}
	if hv := outReq.Header.Get("Host"); hv != "" {
		outReq.Host = hv
		outReq.Header.Del("Host")
	}
	flow.RemoveHop(outReq.Header, outReq.Header.Get("Upgrade") != "")
	outReq.Header.Del("Content-Length")
	outReq.RequestURI = ""

	path := u.Path
	if path == "" {
		path = "/"
	}
	port := u.Port()
	if port == "" {
		if u.Scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	}
	f := &flow.Flow{
		ID:        flow.NewID(),
		StartedAt: time.Now(),
		Scheme:    u.Scheme,
		Host:      u.Hostname(),
		Port:      port,
		Method:    method,
		Path:      path,
		Query:     u.RawQuery,
		URL:       target,
		Proto:     "HTTP/1.1",
		Repeater:  true,
		Req: flow.Message{
			Headers: flow.HeaderList(outReq.Header),
			BodyLen: int64(len(body)),
		},
		ReqBody: body,
		Client:  "repeater",
	}
	st := p.store()
	if st == nil {
		return nil, fmt.Errorf("no active project")
	}
	if err := st.Insert(f); err != nil {
		return nil, err
	}
	p.bus.Publish("flow", f.Summary())

	resp, err := p.transport().RoundTrip(outReq)
	if err != nil {
		f.Err = err.Error()
		f.DurationMs = time.Since(f.StartedAt).Milliseconds()
		_ = st.Finish(f)
		p.bus.Publish("flow", f.Summary())
		return f, nil
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusSwitchingProtocols {
		f.Err = "websocket upgrades are not supported in repeater"
		f.Status = 101
		f.DurationMs = time.Since(f.StartedAt).Milliseconds()
		_ = st.Finish(f)
		p.bus.Publish("flow", f.Summary())
		return f, nil
	}

	limit := s.MaxBodyCapture
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	truncated := false
	if int64(len(data)) > limit {
		data = data[:limit]
		truncated = true
	}
	if err != nil && len(data) == 0 {
		f.Err = err.Error()
	}

	f.Status = resp.StatusCode
	f.Proto = resp.Proto
	f.Resp = &flow.Message{
		Headers:   flow.HeaderList(resp.Header),
		BodyLen:   flow.BodyLen(len(data), resp.ContentLength, truncated),
		Truncated: truncated,
	}
	f.RespBody = data
	f.DurationMs = time.Since(f.StartedAt).Milliseconds()
	if err := st.Finish(f); err != nil {
		return f, err
	}
	p.bus.Publish("flow", f.Summary())
	return f, nil
}

func splitHostPort(h, scheme string) (string, string) {
	if host, port, err := net.SplitHostPort(h); err == nil {
		return host, port
	}
	port := "80"
	if scheme == "https" {
		port = "443"
	}
	return h, port
}

func stripToken(value, token string) string {
	parts := strings.Split(value, ",")
	out := parts[:0]
	for _, p := range parts {
		name := strings.TrimSpace(strings.SplitN(p, ";", 2)[0])
		if strings.EqualFold(name, token) {
			continue
		}
		if strings.TrimSpace(p) != "" {
			out = append(out, strings.TrimSpace(p))
		}
	}
	return strings.Join(out, ", ")
}

type cappedWriter struct {
	limit     int64
	buf       bytes.Buffer
	truncated bool
}

func (c *cappedWriter) Write(b []byte) (int, error) {
	remain := c.limit - int64(c.buf.Len())
	if remain > 0 {
		if int64(len(b)) > remain {
			c.buf.Write(b[:remain])
			c.truncated = true
		} else {
			c.buf.Write(b)
		}
	} else if len(b) > 0 {
		c.truncated = true
	}
	return len(b), nil
}

func (c *cappedWriter) Bytes() []byte { return c.buf.Bytes() }

type connListener struct {
	mu     sync.Mutex
	conn   net.Conn
	closed chan struct{}
	once   sync.Once
}

func (l *connListener) Accept() (net.Conn, error) {
	l.mu.Lock()
	if l.conn != nil {
		c := l.conn
		l.conn = nil
		l.mu.Unlock()
		return c, nil
	}
	l.mu.Unlock()
	<-l.closed
	return nil, net.ErrClosed
}

func (l *connListener) closeOnce()     { l.once.Do(func() { close(l.closed) }) }
func (l *connListener) Close() error   { l.closeOnce(); return nil }
func (l *connListener) Addr() net.Addr { return oneAddr{} }

type oneAddr struct{}

func (oneAddr) Network() string { return "tcp" }
func (oneAddr) String() string  { return "eko-mitm" }

type trackedConn struct {
	net.Conn
	onClose func()
}

func (c *trackedConn) Close() error {
	c.onClose()
	return c.Conn.Close()
}
