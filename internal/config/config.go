package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

// Settings is the user-editable configuration. It is persisted as JSON in
// ~/.config/eko/settings.json and is safe to hand to the UI verbatim.
type Settings struct {
	ProxyAddr           string   `json:"proxyAddr"`
	APIAddr             string   `json:"apiAddr"`
	RecordEnabled       bool     `json:"recordEnabled"`
	RecordExclude       []string `json:"recordExclude"`
	TunnelHosts         []string `json:"tunnelHosts"`
	InterceptRequest    bool     `json:"interceptRequest"`
	InterceptResponse   bool     `json:"interceptResponse"`
	InterceptInclude    []string `json:"interceptInclude"`
	InterceptExclude    []string `json:"interceptExclude"`
	InterceptTimeoutSec int      `json:"interceptTimeoutSec"`
	UpstreamInsecure    bool     `json:"upstreamInsecure"`
	MaxBodyCapture      int64    `json:"maxBodyCapture"`
	InlineBodyLimit     int64    `json:"inlineBodyLimit"`
	MaxFlows            int      `json:"maxFlows"`
	StripZstd           bool     `json:"stripZstd"`
}

func Default() Settings {
	return Settings{
		ProxyAddr:           "127.0.0.1:8080",
		APIAddr:             "127.0.0.1:8081",
		RecordEnabled:       true,
		InterceptRequest:    false,
		InterceptResponse:   false,
		InterceptTimeoutSec: 120,
		MaxBodyCapture:      4 << 20,
		InlineBodyLimit:     256 << 10,
		MaxFlows:            10000,
		StripZstd:           true,
	}
}

func dir(env, fallback string) string {
	if v := os.Getenv(env); v != "" {
		return filepath.Join(v, "eko")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".", fallback, "eko")
	}
	return filepath.Join(home, fallback, "eko")
}

func ConfigDir() string { return dir("XDG_CONFIG_HOME", ".config") }
func DataDir() string   { return dir("XDG_DATA_HOME", ".local/share") }

// Manager owns the live settings and writes them atomically on change.
type Manager struct {
	mu       sync.RWMutex
	path     string
	s        Settings
	onChange func(Settings)
}

func Load() (*Manager, error) {
	s := Default()
	m := &Manager{path: filepath.Join(ConfigDir(), "settings.json")}
	if err := os.MkdirAll(ConfigDir(), 0o755); err != nil {
		return nil, err
	}
	if b, err := os.ReadFile(m.path); err == nil {
		// Unmarshal into the defaults so absent fields keep their default value.
		if err := json.Unmarshal(b, &s); err != nil {
			s = Default()
		}
	}
	sanitize(&s)
	m.s = s
	return m, nil
}

func (m *Manager) OnChange(fn func(Settings)) {
	m.mu.Lock()
	m.onChange = fn
	m.mu.Unlock()
}

func (m *Manager) Get() Settings {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.s
}

func (m *Manager) Path() string { return m.path }

func (m *Manager) Set(s Settings) error {
	sanitize(&s)
	m.mu.Lock()
	m.s = s
	fn := m.onChange
	m.mu.Unlock()

	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp := m.path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, m.path); err != nil {
		return err
	}
	if fn != nil {
		fn(s)
	}
	return nil
}

func clamp(v, lo, hi int64) int64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func sanitize(s *Settings) {
	d := Default()
	if s.ProxyAddr == "" {
		s.ProxyAddr = d.ProxyAddr
	}
	if s.APIAddr == "" {
		s.APIAddr = d.APIAddr
	}
	s.MaxBodyCapture = clamp(s.MaxBodyCapture, 64<<10, 64<<20)
	s.InlineBodyLimit = clamp(s.InlineBodyLimit, 4<<10, 8<<20)
	s.InterceptTimeoutSec = int(clamp(int64(s.InterceptTimeoutSec), 5, 3600))
	s.MaxFlows = int(clamp(int64(s.MaxFlows), 100, 200000))
}
