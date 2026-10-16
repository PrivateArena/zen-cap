package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultConfigRecordAutomationHotkey(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.Hotkeys.RecordAutomation != "Control-Shift-m" {
		t.Errorf("expected default record_automation 'Control-Shift-m', got %q", cfg.Hotkeys.RecordAutomation)
	}
}

func TestDefaultPortableConfigRecordAutomationHotkey(t *testing.T) {
	cfg := DefaultPortableConfig(t.TempDir())
	if cfg.Hotkeys.RecordAutomation != "Control-Shift-m" {
		t.Errorf("expected portable default record_automation 'Control-Shift-m', got %q", cfg.Hotkeys.RecordAutomation)
	}
}

func TestDefaultConfigMacroTaskProfiles(t *testing.T) {
	cfg := DefaultConfig()
	byName := make(map[string]TaskProfile)
	for _, p := range cfg.TaskProfiles {
		byName[p.Name] = p
	}

	want := []struct {
		name  string
		tasks []string
	}{
		{"Macro File", []string{"save_script"}},
		{"Macro Clipboard", []string{"copy_script"}},
		{"Macro Both", []string{"save_script", "copy_script"}},
	}
	for _, w := range want {
		p, ok := byName[w.name]
		if !ok {
			t.Errorf("expected task profile %q in defaults", w.name)
			continue
		}
		if !contains(p.AppliesTo, "macro") {
			t.Errorf("profile %q: expected AppliesTo to include 'macro', got %v", w.name, p.AppliesTo)
		}
		if len(p.Tasks) != len(w.tasks) {
			t.Errorf("profile %q: expected tasks %v, got %v", w.name, w.tasks, p.Tasks)
			continue
		}
		for i := range w.tasks {
			if p.Tasks[i] != w.tasks[i] {
				t.Errorf("profile %q: tasks[%d] = %q, want %q", w.name, i, p.Tasks[i], w.tasks[i])
			}
		}
	}
}

func TestDefaultPortableConfigMacroTaskProfiles(t *testing.T) {
	cfg := DefaultPortableConfig(t.TempDir())
	found := false
	for _, p := range cfg.TaskProfiles {
		if p.Name == "Macro File" && contains(p.AppliesTo, "macro") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected portable defaults to include a macro task profile, got %+v", cfg.TaskProfiles)
	}
}

// TestReadConfigFillsRecordAutomationFromDefaults is the backward-compatibility
// guard: an existing config.json without the new field must still yield a
// usable hotkey after loading.
func TestReadConfigFillsRecordAutomationFromDefaults(t *testing.T) {
	dir := t.TempDir()
	binDir := filepath.Join(dir, "bin")
	configPath := filepath.Join(dir, "config.json")

	legacy := `{
		"output_dir": "out",
		"hotkeys": {
			"screenshot": "Control-Shift-9"
		}
	}`
	if err := os.WriteFile(configPath, []byte(legacy), 0644); err != nil {
		t.Fatalf("failed to write legacy config: %v", err)
	}

	cfg, err := readConfig(configPath, binDir, false)
	if err != nil {
		t.Fatalf("readConfig failed: %v", err)
	}

	// Existing value is preserved, absent new value is backfilled.
	if cfg.Hotkeys.Screenshot != "Control-Shift-9" {
		t.Errorf("expected existing screenshot hotkey preserved, got %q", cfg.Hotkeys.Screenshot)
	}
	if cfg.Hotkeys.RecordAutomation != "Control-Shift-m" {
		t.Errorf("expected record_automation backfilled from defaults, got %q", cfg.Hotkeys.RecordAutomation)
	}
}

// TestReadConfigKeepsExplicitRecordAutomation: a user-configured value must not
// be clobbered by defaults.
func TestReadConfigKeepsExplicitRecordAutomation(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")

	cfgJSON := `{
		"hotkeys": {
			"record_automation": "Control-Shift-z"
		}
	}`
	if err := os.WriteFile(configPath, []byte(cfgJSON), 0644); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	cfg, err := readConfig(configPath, filepath.Join(dir, "bin"), false)
	if err != nil {
		t.Fatalf("readConfig failed: %v", err)
	}
	if cfg.Hotkeys.RecordAutomation != "Control-Shift-z" {
		t.Errorf("expected explicit record_automation preserved, got %q", cfg.Hotkeys.RecordAutomation)
	}
}

func contains(s []string, v string) bool {
	for _, e := range s {
		if e == v {
			return true
		}
	}
	return false
}
