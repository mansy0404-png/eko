package project

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"eko/internal/flow"
)

type Meta struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	CreatedAt string   `json:"createdAt"`
	ScopeIn   []string `json:"scopeInclude,omitempty"`
	ScopeEx   []string `json:"scopeExclude,omitempty"`
}

type state struct {
	Active   string `json:"active"`
	Projects []Meta `json:"projects"`
}

type Manager struct {
	mu      sync.RWMutex
	root    string
	state   state
	store   *flow.Store
	maxFlows int
	inline   int64
	onSwitch func()
}

func newID() string {
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}

func Open(root string, maxFlows int, inline int64) (*Manager, error) {
	m := &Manager{root: root, maxFlows: maxFlows, inline: inline}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, err
	}
	if err := m.loadOrInit(); err != nil {
		return nil, err
	}
	if err := m.openActive(); err != nil {
		return nil, err
	}
	return m, nil
}

func (m *Manager) OnSwitch(fn func()) {
	m.mu.Lock()
	m.onSwitch = fn
	m.mu.Unlock()
}

func (m *Manager) statePath() string    { return filepath.Join(m.root, "projects.json") }
func (m *Manager) projectsDir() string  { return filepath.Join(m.root, "projects") }
func (m *Manager) projectDir(id string) string { return filepath.Join(m.projectsDir(), id) }

func (m *Manager) loadOrInit() error {
	// migrate legacy layout: root eko.db/blobs -> default project
	legacyDB := filepath.Join(m.root, "eko.db")
	projectsJSON := m.statePath()
	if _, err := os.Stat(projectsJSON); os.IsNotExist(err) {
		// create default project
		def := Meta{ID: "default", Name: "Default", CreatedAt: time.Now().Format(time.RFC3339)}
		m.state = state{Active: "default", Projects: []Meta{def}}
		if err := os.MkdirAll(m.projectDir("default"), 0o755); err != nil {
			return err
		}
		if _, err := os.Stat(legacyDB); err == nil {
			// move legacy files into default project dir
			for _, name := range []string{"eko.db", "eko.db-wal", "eko.db-shm"} {
				src := filepath.Join(m.root, name)
				if _, e := os.Stat(src); e == nil {
					_ = os.Rename(src, filepath.Join(m.projectDir("default"), name))
				}
			}
			if _, e := os.Stat(filepath.Join(m.root, "blobs")); e == nil {
				_ = os.Rename(filepath.Join(m.root, "blobs"), filepath.Join(m.projectDir("default"), "blobs"))
			}
		}
		return m.saveStateLocked()
	}
	b, err := os.ReadFile(projectsJSON)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(b, &m.state); err != nil {
		return err
	}
	if m.state.Active == "" || len(m.state.Projects) == 0 {
		def := Meta{ID: "default", Name: "Default", CreatedAt: time.Now().Format(time.RFC3339)}
		m.state = state{Active: "default", Projects: []Meta{def}}
		_ = os.MkdirAll(m.projectDir("default"), 0o755)
		return m.saveStateLocked()
	}
	return nil
}

func (m *Manager) saveStateLocked() error {
	b, err := json.MarshalIndent(m.state, "", "  ")
	if err != nil {
		return err
	}
	tmp := m.statePath() + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, m.statePath())
}

func (m *Manager) openActive() error {
	id := m.state.Active
	dir := m.projectDir(id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	s, err := flow.Open(dir, m.maxFlows, m.inline)
	if err != nil {
		return err
	}
	m.store = s
	return nil
}

func (m *Manager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.store != nil {
		err := m.store.Close()
		m.store = nil
		return err
	}
	return nil
}

func (m *Manager) Store() *flow.Store {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.store
}

func (m *Manager) Active() Meta {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, p := range m.state.Projects {
		if p.ID == m.state.Active {
			return p
		}
	}
	if len(m.state.Projects) > 0 {
		return m.state.Projects[0]
	}
	return Meta{ID: "default", Name: "Default"}
}

func (m *Manager) List() (active string, projects []Meta) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]Meta, len(m.state.Projects))
	copy(out, m.state.Projects)
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt < out[j].CreatedAt })
	return m.state.Active, out
}

func (m *Manager) Create(name string) (Meta, error) {
	if name == "" {
		name = "New project"
	}
	p := Meta{ID: newID(), Name: name, CreatedAt: time.Now().Format(time.RFC3339)}
	dir := m.projectDir(p.ID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return Meta{}, err
	}
	m.mu.Lock()
	m.state.Projects = append(m.state.Projects, p)
	err := m.saveStateLocked()
	m.mu.Unlock()
	if err != nil {
		return Meta{}, err
	}
	return p, nil
}

func (m *Manager) Switch(id string) error {
	m.mu.Lock()
	// validate
	found := false
	for _, p := range m.state.Projects {
		if p.ID == id {
			found = true
			break
		}
	}
	if !found {
		m.mu.Unlock()
		return fmt.Errorf("unknown project")
	}
	if id == m.state.Active && m.store != nil {
		m.mu.Unlock()
		return nil
	}
	old := m.store
	m.store = nil
	m.state.Active = id
	if err := m.saveStateLocked(); err != nil {
		m.store = old
		m.mu.Unlock()
		return err
	}
	dir := m.projectDir(id)
	m.mu.Unlock()
	if old != nil {
		_ = old.Close()
	}
	s, err := flow.Open(dir, m.maxFlows, m.inline)
	if err != nil {
		return err
	}
	m.mu.Lock()
	m.store = s
	cb := m.onSwitch
	m.mu.Unlock()
	if cb != nil {
		cb()
	}
	return nil
}

func (m *Manager) Rename(id, name string) error {
	if name == "" {
		return fmt.Errorf("name required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.state.Projects {
		if m.state.Projects[i].ID == id {
			m.state.Projects[i].Name = name
			return m.saveStateLocked()
		}
	}
	return fmt.Errorf("unknown project")
}

func (m *Manager) Delete(id string) error {
	m.mu.Lock()
	if id == m.state.Active {
		m.mu.Unlock()
		return fmt.Errorf("cannot delete the active project")
	}
	idx := -1
	for i, p := range m.state.Projects {
		if p.ID == id {
			idx = i
			break
		}
	}
	if idx < 0 {
		m.mu.Unlock()
		return fmt.Errorf("unknown project")
	}
	m.state.Projects = append(m.state.Projects[:idx], m.state.Projects[idx+1:]...)
	if err := m.saveStateLocked(); err != nil {
		m.mu.Unlock()
		return err
	}
	dir := m.projectDir(id)
	m.mu.Unlock()
	return os.RemoveAll(dir)
}

func (m *Manager) UpdateScope(id string, include, exclude []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.state.Projects {
		if m.state.Projects[i].ID == id {
			m.state.Projects[i].ScopeIn = include
			m.state.Projects[i].ScopeEx = exclude
			return m.saveStateLocked()
		}
	}
	return fmt.Errorf("unknown project")
}

func (m *Manager) SetLimits(maxFlows int, inline int64) {
	m.mu.Lock()
	m.maxFlows = maxFlows
	m.inline = inline
	s := m.store
	m.mu.Unlock()
	if s != nil {
		s.SetMaxFlows(maxFlows)
		s.SetInlineLimit(inline)
	}
}

func (m *Manager) All() []Meta {
	_, p := m.List()
	return p
}
