// Package intercept implements Burp-style "hold the request/response until the
// user says forward, edit or drop" queues.
package intercept

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"eko/internal/bus"
	"eko/internal/config"
	"eko/internal/flow"
	"eko/internal/glob"
)

type Decision struct {
	Action  string
	Method  string
	URL     string
	Host    string
	Status  int
	Headers flow.Headers
	Body    []byte
	BodySet bool
}

type Pending struct {
	ID        string       `json:"id"`
	Kind      string       `json:"kind"`
	FlowID    string       `json:"flowId"`
	Method    string       `json:"method"`
	URL       string       `json:"url"`
	Status    int          `json:"status"`
	Headers   flow.Headers `json:"headers"`
	Body      string       `json:"body"`
	BodyLen   int          `json:"bodyLen"`
	Truncated bool         `json:"truncated"`
	CreatedAt time.Time    `json:"createdAt"`
}

type entry struct {
	p  Pending
	ch chan Decision
}

type Manager struct {
	bus         *bus.Bus
	mu          sync.Mutex
	enabledReq  bool
	enabledResp bool
	include     []glob.Matcher
	exclude     []glob.Matcher
	timeout     time.Duration
	pending     map[string]*entry
	order       []string
	seq         atomic.Int64
}

func New(b *bus.Bus) *Manager {
	return &Manager{bus: b, pending: map[string]*entry{}, timeout: 120 * time.Second}
}

func (m *Manager) Sync(s config.Settings) {
	m.mu.Lock()
	m.enabledReq = s.InterceptRequest
	m.enabledResp = s.InterceptResponse
	m.include = glob.Compile(s.InterceptInclude)
	m.exclude = glob.Compile(s.InterceptExclude)
	m.timeout = time.Duration(s.InterceptTimeoutSec) * time.Second
	m.mu.Unlock()
}

func (m *Manager) Match(kind, url string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if kind == "request" && !m.enabledReq {
		return false
	}
	if kind == "response" && !m.enabledResp {
		return false
	}
	if glob.Match(m.exclude, url) {
		return false
	}
	if len(m.include) > 0 && !glob.Match(m.include, url) {
		return false
	}
	return true
}

func (m *Manager) Hold(kind, flowID, method, url string, status int, headers flow.Headers, body []byte) (Decision, bool) {
	if !m.Match(kind, url) {
		return Decision{}, false
	}
	p := Pending{
		Kind:      kind,
		FlowID:    flowID,
		Method:    method,
		URL:       url,
		Status:    status,
		Headers:   headers,
		Body:      string(body),
		BodyLen:   len(body),
		CreatedAt: time.Now(),
	}
	p.ID = fmt.Sprintf("%s-%d", kind, m.seq.Add(1))
	e := &entry{p: p, ch: make(chan Decision, 1)}

	m.mu.Lock()
	m.pending[p.ID] = e
	m.order = append(m.order, p.ID)
	timeout := m.timeout
	m.mu.Unlock()

	m.bus.Publish("intercept", p)

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case d := <-e.ch:
		m.remove(p.ID)
		m.bus.Publish("intercept-gone", p.ID)
		return d, true
	case <-timer.C:
		m.remove(p.ID)
		m.bus.Publish("intercept-gone", p.ID)
		return Decision{Action: "forward"}, true
	}
}

func (m *Manager) remove(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.pending, id)
	for i, v := range m.order {
		if v == id {
			m.order = append(m.order[:i], m.order[i+1:]...)
			break
		}
	}
}

func (m *Manager) List() []Pending {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Pending, 0, len(m.order))
	for _, id := range m.order {
		if e, ok := m.pending[id]; ok {
			out = append(out, e.p)
		}
	}
	return out
}

func (m *Manager) Get(id string) (Pending, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.pending[id]
	if !ok {
		return Pending{}, false
	}
	return e.p, true
}

func (m *Manager) Resolve(id string, d Decision) bool {
	m.mu.Lock()
	e, ok := m.pending[id]
	m.mu.Unlock()
	if !ok {
		return false
	}
	select {
	case e.ch <- d:
		return true
	default:
		return false
	}
}
