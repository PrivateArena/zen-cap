package automation

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"gopkg.in/yaml.v3"
)

type Manager struct {
	scripts  []Script
	filePath string
	mu       sync.RWMutex
}

// NewManager creates a new automation manager and loads existing scripts from file.
func NewManager(filePath string) (*Manager, error) {
	mgr := &Manager{
		filePath: filePath,
		scripts:  make([]Script, 0),
	}

	if err := mgr.loadScripts(); err != nil {
		if !os.IsNotExist(err) {
			return nil, fmt.Errorf("failed to load automations: %w", err)
		}
	}

	// Seed an example script if the loaded list is empty
	if len(mgr.GetAll()) == 0 {
		example := Script{
			Name: "Example Reward Clicker",
			Window: &WindowTarget{
				Title: "MyGame",
			},
			Steps: []Step{
				{Action: "wait", Duration: "1s"},
				{Action: "click", X: 400, Y: 300, Button: "left"},
				{Action: "wait", Duration: "500ms"},
				{Action: "type", Text: "Hello, Zen-Cap!"},
				{Action: "key", Keys: "Return"},
			},
		}
		if err := mgr.Add(example); err != nil {
			return nil, fmt.Errorf("failed to seed example automation: %w", err)
		}
	}

	return mgr, nil
}

func (m *Manager) loadScripts() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, err := os.Stat(m.filePath); os.IsNotExist(err) {
		return nil
	}

	data, err := os.ReadFile(m.filePath)
	if err != nil {
		return err
	}

	var loaded []Script
	if err := yaml.Unmarshal(data, &loaded); err != nil {
		return err
	}

	m.scripts = loaded
	return nil
}

func (m *Manager) Save() error {
	m.mu.RLock()
	data, err := yaml.Marshal(m.scripts)
	m.mu.RUnlock()
	if err != nil {
		return err
	}

	dir := filepath.Dir(m.filePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	return os.WriteFile(m.filePath, data, 0644)
}

// GetAll returns a copy of all loaded scripts, hot-reloading from disk first.
func (m *Manager) GetAll() []Script {
	_ = m.loadScripts()

	m.mu.RLock()
	defer m.mu.RUnlock()

	res := make([]Script, len(m.scripts))
	copy(res, m.scripts)
	return res
}

// Add appends and saves a new script.
func (m *Manager) Add(script Script) error {
	m.mu.Lock()
	m.scripts = append(m.scripts, script)
	m.mu.Unlock()

	return m.Save()
}
