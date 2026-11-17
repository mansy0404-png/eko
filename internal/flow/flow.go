// Package flow holds the captured request/response model and its SQLite store.
package flow

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
)

type Headers [][2]string

type Message struct {
	Headers   Headers `json:"headers"`
	BodyLen   int64   `json:"bodyLen"`
	Truncated bool    `json:"truncated"`
}

type Flow struct {
	ID          string    `json:"id"`
	Seq         int64     `json:"seq"`
	StartedAt   time.Time `json:"startedAt"`
	DurationMs  int64     `json:"durationMs"`
	Scheme      string    `json:"scheme"`
	Host        string    `json:"host"`
	Port        string    `json:"port"`
	Method      string    `json:"method"`
	Path        string    `json:"path"`
	Query       string    `json:"query"`
	URL         string    `json:"url"`
	Proto       string    `json:"proto"`
	Status      int       `json:"status"`
	Req         Message   `json:"req"`
	Resp        *Message  `json:"resp"`
	ReqBody     []byte    `json:"-"`
	RespBody    []byte    `json:"-"`
	Err         string    `json:"err,omitempty"`
	Tags        []string  `json:"tags,omitempty"`
	Comment     string    `json:"comment,omitempty"`
	Repeater    bool      `json:"repeater"`
	Intercepted bool      `json:"intercepted"`
	Client      string    `json:"client,omitempty"`
}

// Summary strips bodies for SSE and list responses.
func (f *Flow) Summary() *Flow {
	c := *f
	c.ReqBody = nil
	c.RespBody = nil
	return &c
}

func NewID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return time.Now().Format("150405.000000")
	}
	return hex.EncodeToString(b[:])
}

func HeaderList(h http.Header) Headers {
	keys := make([]string, 0, len(h))
	for k := range h {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make(Headers, 0, len(keys))
	for _, k := range keys {
		for _, v := range h[k] {
			out = append(out, [2]string{k, v})
		}
	}
	return out
}

func HeaderMap(h Headers) http.Header {
	m := make(http.Header, len(h))
	for _, kv := range h {
		m.Add(kv[0], kv[1])
	}
	return m
}

// WithHost adds the request's Host as a normal header entry when it is not
// already present. Go keeps Host outside http.Request.Header, so without this
// the UI cannot show (or seed) the full request headers.
func WithHost(h Headers, hostPort string) Headers {
	if hostPort == "" {
		return h
	}
	for _, kv := range h {
		if http.CanonicalHeaderKey(kv[0]) == "Host" {
			return h
		}
	}
	out := make(Headers, 0, len(h)+1)
	out = append(out, [2]string{"Host", hostPort})
	return append(out, h...)
}

func JoinHeader(h Headers) string {
	var b strings.Builder
	for _, kv := range h {
		b.WriteString(kv[0])
		b.WriteString(": ")
		b.WriteString(kv[1])
		b.WriteString("\n")
	}
	return b.String()
}

var hopHeaders = map[string]bool{
	"Connection":          true,
	"Proxy-Connection":    true,
	"Keep-Alive":          true,
	"Proxy-Authenticate":  true,
	"Proxy-Authorization": true,
	"Te":                  true,
	"Trailer":             true,
	"Transfer-Encoding":   true,
	"Upgrade":             true,
}

func IsHop(k string) bool { return hopHeaders[http.CanonicalHeaderKey(k)] }

// RemoveHop drops hop-by-hop headers. keepUpgrade leaves the WebSocket
// upgrade pair intact.
func RemoveHop(h http.Header, keepUpgrade bool) {
	if c := h.Get("Connection"); c != "" && !keepUpgrade {
		for _, f := range strings.Split(c, ",") {
			if f = strings.TrimSpace(f); f != "" {
				h.Del(f)
			}
		}
	}
	for k := range hopHeaders {
		if keepUpgrade && (k == "Upgrade" || k == "Connection") {
			continue
		}
		h.Del(k)
	}
}

// CaptureBody reads up to limit bytes and returns a reader for the leftover so
// the caller can still forward the full body without buffering it.
func CaptureBody(r io.Reader, limit int64) (captured []byte, rest io.Reader, truncated bool, err error) {
	if r == nil {
		return nil, nil, false, nil
	}
	if limit < 0 {
		limit = 0
	}
	buf := new(bytes.Buffer)
	n, err := io.CopyN(buf, r, limit)
	if err == io.EOF {
		return buf.Bytes(), nil, false, nil
	}
	if err != nil {
		return buf.Bytes(), nil, true, err
	}
	_ = n
	one := make([]byte, 1)
	m, e2 := r.Read(one)
	if m > 0 {
		return buf.Bytes(), io.MultiReader(bytes.NewReader(one[:m]), r), true, nil
	}
	if e2 == io.EOF {
		return buf.Bytes(), nil, false, nil
	}
	if e2 != nil {
		return buf.Bytes(), nil, true, nil
	}
	return buf.Bytes(), r, false, nil
}

// ParseRawRequest parses the UI raw editor format. The request line must use an
// absolute URL; a Host header is optional.
func ParseRawRequest(raw string) (method, rawURL, host string, headers Headers, body []byte, err error) {
	br := bufio.NewReader(strings.NewReader(raw))
	req, err := http.ReadRequest(br)
	if err != nil {
		return "", "", "", nil, nil, err
	}
	defer req.Body.Close()
	if req.URL.IsAbs() {
		rawURL = req.URL.String()
	} else {
		scheme := "http"
		if req.TLS != nil {
			scheme = "https"
		}
		rawURL = scheme + "://" + req.Host + req.URL.RequestURI()
	}
	body, err = io.ReadAll(req.Body)
	if err != nil {
		return "", "", "", nil, nil, err
	}
	// The editor does not require Content-Length; anything after the header
	// block that the parser left in the buffer is the intended body.
	if rest, rerr := io.ReadAll(br); rerr == nil && len(bytes.TrimSpace(rest)) > 0 {
		body = append(body, rest...)
	}
	return req.Method, rawURL, req.Host, HeaderList(req.Header), body, nil
}

// ParseRawResponse parses a raw HTTP response from the UI.
func ParseRawResponse(raw string) (status int, headers Headers, body []byte, err error) {
	resp, err := http.ReadResponse(bufio.NewReader(strings.NewReader(raw)), nil)
	if err != nil {
		return 0, nil, nil, err
	}
	defer resp.Body.Close()
	body, err = io.ReadAll(resp.Body)
	if err != nil {
		return 0, nil, nil, err
	}
	return resp.StatusCode, HeaderList(resp.Header), body, nil
}

// BodyLen reports the recorded body length, using the framing length when the
// capture was truncated.
func BodyLen(captured int, contentLength int64, truncated bool) int64 {
	if truncated && contentLength >= 0 {
		return contentLength
	}
	return int64(captured)
}
