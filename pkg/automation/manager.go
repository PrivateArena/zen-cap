package automation

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

type Manager struct {
	scripts []Script
	dirPath string
	mu      sync.RWMutex
}

// NewManager creates a new automation manager and loads existing scripts from the directory.
func NewManager(dirPath string) (*Manager, error) {
	mgr := &Manager{
		dirPath: dirPath,
		scripts: make([]Script, 0),
	}

	if err := mgr.loadScripts(); err != nil {
		if !os.IsNotExist(err) {
			return nil, fmt.Errorf("failed to load automations directory: %w", err)
		}
	}

	// Seed an example script if the directory is empty or has no yaml files
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

	if _, err := os.Stat(m.dirPath); os.IsNotExist(err) {
		return nil
	}

	files, err := os.ReadDir(m.dirPath)
	if err != nil {
		return err
	}

	var loaded []Script
	for _, file := range files {
		if file.IsDir() {
			continue
		}
		ext := filepath.Ext(file.Name())
		if ext != ".yaml" && ext != ".yml" {
			continue
		}

		path := filepath.Join(m.dirPath, file.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}

		var script Script
		if err := yaml.Unmarshal(data, &script); err != nil {
			continue
		}
		
		// Ensure name is populated
		if script.Name == "" {
			script.Name = strings.TrimSuffix(file.Name(), ext)
		}

		loaded = append(loaded, script)
	}

	m.scripts = loaded
	return nil
}

// Save saves all in-memory scripts into separate YAML files inside the directory.
func (m *Manager) Save() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := os.MkdirAll(m.dirPath, 0755); err != nil {
		return err
	}

	for _, script := range m.scripts {
		fileName := sanitizeFileName(script.Name) + ".yaml"
		path := filepath.Join(m.dirPath, fileName)
		data, err := yaml.Marshal(script)
		if err != nil {
			return err
		}
		if err := os.WriteFile(path, data, 0644); err != nil {
			return err
		}
	}
	return nil
}

// GetAll returns a copy of all loaded scripts, hot-reloading from the folder first.
func (m *Manager) GetAll() []Script {
	_ = m.loadScripts()

	m.mu.RLock()
	defer m.mu.RUnlock()

	res := make([]Script, len(m.scripts))
	copy(res, m.scripts)
	return res
}

// Add appends and saves a new script in its own file.
func (m *Manager) Add(script Script) error {
	m.mu.Lock()
	m.scripts = append(m.scripts, script)
	m.mu.Unlock()

	return m.Save()
}

func sanitizeFileName(name string) string {
	var res []rune
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
			res = append(res, r)
		} else if r == ' ' {
			res = append(res, '_')
		}
	}
	if len(res) == 0 {
		return "script"
	}
	return string(res)
}
