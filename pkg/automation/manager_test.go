package automation

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
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

// TestNewStepFieldsStayUnwritten proves the wire-format promise of step A5: the
// fields added for the HTTP API are omitempty on BOTH tags, so a script written
// before they existed still round-trips to exactly the same bytes. Without this,
// saving a script through the picker would silently rewrite every YAML file in
// the user's automations directory.
func TestNewStepFieldsStayUnwritten(t *testing.T) {
	mgr, err := NewManager(t.TempDir())
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	scripts := mgr.GetAll()
	if len(scripts) == 0 {
		t.Fatal("no seeded example script to test against")
	}

	for _, script := range scripts {
		before, err := os.ReadFile(filepath.Join(mgr.dirPath, fileNameOfScript(t, mgr, script.Name)))
		if err != nil {
			t.Fatalf("read source of %q: %v", script.Name, err)
		}

		out, err := yaml.Marshal(script)
		if err != nil {
			t.Fatalf("marshal %q: %v", script.Name, err)
		}
		for _, key := range []string{"direction", "amount", "clicks", "click_gap", "wait_for", "no_activate", "chunk_runes"} {
			if strings.Contains(string(out), key+":") && !strings.Contains(string(before), key+":") {
				t.Errorf("marshalling %q emits %q, which the source file never had:\n%s", script.Name, key, out)
			}
		}

		// And the round trip must not lose what the file did have.
		var again Script
		if err := yaml.Unmarshal(out, &again); err != nil {
			t.Fatalf("re-parse of marshalled %q: %v", script.Name, err)
		}
		if len(again.Steps) != len(script.Steps) || again.Name != script.Name {
			t.Errorf("round trip changed %q: %d steps -> %d steps", script.Name, len(script.Steps), len(again.Steps))
		}
	}
}

// TestNewStepFieldsParseFromYAML is the other half: the same names must arrive
// when a script does use them, over YAML as well as JSON.
func TestNewStepFieldsParseFromYAML(t *testing.T) {
	src := `
name: gesture probe
steps:
  - action: click
    x: 10
    y: 20
    clicks: 2
    click_gap: 30ms
    no_activate: true
  - action: scroll
    direction: down
    amount: 5
  - action: type
    text: hello
    chunk_runes: 40
  - action: wait_until
    wait_for: pixel
    x: 1
    y: 2
    color: "#ffffff"
    timeout: 2s
`
	var s Script
	if err := yaml.Unmarshal([]byte(src), &s); err != nil {
		t.Fatalf("the documented YAML form does not parse: %v", err)
	}
	if len(s.Steps) != 4 {
		t.Fatalf("parsed %d steps, want 4", len(s.Steps))
	}
	c := s.Steps[0]
	if c.Clicks != 2 || c.ClickGap != "30ms" || !c.NoActivate {
		t.Errorf("click fields lost: %+v", c)
	}
	if s.Steps[1].Direction != "down" || s.Steps[1].Amount != 5 {
		t.Errorf("scroll fields lost: %+v", s.Steps[1])
	}
	if s.Steps[2].ChunkRunes != 40 {
		t.Errorf("chunk_runes lost: %+v", s.Steps[2])
	}
	if s.Steps[3].WaitFor != "pixel" || s.Steps[3].Timeout != "2s" {
		t.Errorf("wait_until fields lost: %+v", s.Steps[3])
	}

	var j Step
	if err := json.Unmarshal([]byte(`{"action":"screenshot","output":"x.png"}`), &j); err != nil {
		t.Fatalf("json decode: %v", err)
	}
	if j.Output != "x.png" {
		t.Errorf("json decode lost output: %+v", j)
	}
}

func fileNameOfScript(t *testing.T, mgr *Manager, name string) string {
	t.Helper()
	for _, s := range mgr.GetAll() {
		if s.Name == name {
			// The manager keeps no filename on Script, so find it by content.
			files, err := os.ReadDir(mgr.dirPath)
			if err != nil {
				t.Fatalf("readdir: %v", err)
			}
			for _, f := range files {
				data, err := os.ReadFile(filepath.Join(mgr.dirPath, f.Name()))
				if err == nil && strings.Contains(string(data), "name: "+name) {
					return f.Name()
				}
			}
		}
	}
	t.Fatalf("could not find the file for script %q", name)
	return ""
}
