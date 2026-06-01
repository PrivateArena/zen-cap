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

	filePath := filepath.Join(tempDir, "automations.yaml")

	// 1. Initial creation (should seed example script)
	mgr, err := NewManager(filePath)
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
	scripts[0].Name = "Custom Reward Script"
	data, err := yaml.Marshal(scripts)
	if err != nil {
		t.Fatalf("failed to marshal scripts: %v", err)
	}

	err = os.WriteFile(filePath, data, 0644)
	if err != nil {
		t.Fatalf("failed to simulate manual edit: %v", err)
	}

	// 3. Reload manager / check hot reload
	scripts2 := mgr.GetAll() // Should hot-reload from file
	if len(scripts2) != 1 {
		t.Fatalf("expected 1 script after reload, got %d", len(scripts2))
	}

	if scripts2[0].Name != "Custom Reward Script" {
		t.Errorf("expected hot-reloaded name 'Custom Reward Script', got %q", scripts2[0].Name)
	}
}
