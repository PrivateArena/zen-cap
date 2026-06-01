package automation

import (
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestAutomationManagerSeedingAndLoading(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "zen-cap-auto-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// 1. Initial creation (should seed example script in tempDir)
	mgr, err := NewManager(tempDir)
	if err != nil {
		t.Fatalf("failed to create NewManager: %v", err)
	}

	scripts := mgr.GetAll()
	if len(scripts) == 0 {
		t.Fatalf("expected seeded script, found none")
	}

	if scripts[0].Name != "Example Reward Clicker" {
		t.Errorf("expected seeded name 'Example Reward Clicker', got %q", scripts[0].Name)
	}

	// Verify step parsing
	if len(scripts[0].Steps) < 2 {
		t.Fatalf("expected at least 2 steps, got %d", len(scripts[0].Steps))
	}

	// 2. Simulate manual edit (Hot-Reload)
	filePath := filepath.Join(tempDir, "Example_Reward_Clicker.yaml")
	scripts[0].Name = "Custom Reward Script"
	data, err := yaml.Marshal(scripts[0])
	if err != nil {
		t.Fatalf("failed to marshal script: %v", err)
	}

	err = os.WriteFile(filePath, data, 0644)
	if err != nil {
		t.Fatalf("failed to simulate manual edit: %v", err)
	}

	// 3. Reload manager / check hot reload
	scripts2 := mgr.GetAll() // Should hot-reload from folder files
	if len(scripts2) != 1 {
		t.Fatalf("expected 1 script after reload, got %d", len(scripts2))
	}

	if scripts2[0].Name != "Custom Reward Script" {
		t.Errorf("expected hot-reloaded name 'Custom Reward Script', got %q", scripts2[0].Name)
	}
}

func TestLoadRealScripts(t *testing.T) {
	// Look at the real automations directory
	dir := "../../automations"
	files, err := os.ReadDir(dir)
	if err != nil {
		t.Logf("no real automations dir found at %s: %v", dir, err)
		return
	}

	for _, f := range files {
		if f.IsDir() {
			continue
		}
		ext := filepath.Ext(f.Name())
		if ext != ".yaml" && ext != ".yml" {
			continue
		}

		path := filepath.Join(dir, f.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			t.Errorf("failed to read %s: %v", f.Name(), err)
			continue
		}

		var s Script
		if err := yaml.Unmarshal(data, &s); err != nil {
			t.Errorf("failed to parse %s: %v", f.Name(), err)
			continue
		}

		t.Logf("SUCCESSFULLY LOADED REAL SCRIPT: %q with %d steps", s.Name, len(s.Steps))
	}
}
