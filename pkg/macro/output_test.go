package macro

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"zen-cap/pkg/automation"
	"zen-cap/pkg/capture"
	"zen-cap/pkg/config"
)

func scriptFixture() automation.Script {
	return automation.Script{
		Name: "My Macro Test",
		Steps: []automation.Step{
			{Action: "hold", X: 10, Y: 20, Button: "left"},
			{Action: "release", X: 10, Y: 20, Button: "left"},
		},
	}
}

func TestOutputChainDefault(t *testing.T) {
	if got := OutputChain(nil); !equalStrings(got, []string{TaskSaveScript}) {
		t.Fatalf("nil cfg: expected [save_script], got %v", got)
	}

	cfg := config.DefaultConfig()
	cfg.CurrentTaskProfile = "Copy Image" // capture-only profile
	if got := OutputChain(cfg); !equalStrings(got, []string{TaskSaveScript}) {
		t.Fatalf("capture profile: expected [save_script], got %v", got)
	}
}

func TestOutputChainUnknownProfile(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.CurrentTaskProfile = "Does Not Exist"
	if got := OutputChain(cfg); !equalStrings(got, []string{TaskSaveScript}) {
		t.Fatalf("unknown profile: expected [save_script], got %v", got)
	}
}

func TestOutputChainMacroProfiles(t *testing.T) {
	cfg := config.DefaultConfig()
	if cfg.CurrentTaskProfile == "Macro File" || cfg.CurrentTaskProfile == "Macro Clipboard" {
		t.Skip("default current profile collides with macro profile")
	}

	cfg.CurrentTaskProfile = "Macro Clipboard"
	if got := OutputChain(cfg); !equalStrings(got, []string{TaskCopyScript}) {
		t.Fatalf("macro clipboard profile: expected [copy_script], got %v", got)
	}

	cfg.CurrentTaskProfile = "Macro Both"
	if got := OutputChain(cfg); !equalStrings(got, []string{TaskSaveScript, TaskCopyScript}) {
		t.Fatalf("macro both profile: expected [save_script, copy_script], got %v", got)
	}
}

func TestOutputChainMacroProfileEmptyTasksFallsBack(t *testing.T) {
	cfg := &config.Config{
		CurrentTaskProfile: "Empty Macro",
		TaskProfiles: []config.TaskProfile{
			{Name: "Empty Macro", Tasks: nil, AppliesTo: []string{"macro"}},
		},
	}
	if got := OutputChain(cfg); !equalStrings(got, []string{TaskSaveScript}) {
		t.Fatalf("empty macro tasks: expected [save_script], got %v", got)
	}
}

func TestSaveScript(t *testing.T) {
	dir := t.TempDir()
	path, err := SaveScript(scriptFixture(), dir)
	if err != nil {
		t.Fatalf("SaveScript failed: %v", err)
	}

	wantPath := filepath.Join(dir, "My_Macro_Test.yaml")
	if path != wantPath {
		t.Errorf("expected path %q, got %q", wantPath, path)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read saved script: %v", err)
	}
	var got automation.Script
	if err := yaml.Unmarshal(data, &got); err != nil {
		t.Fatalf("saved script is not valid YAML: %v", err)
	}
	if got.Name != "My Macro Test" {
		t.Errorf("expected name %q, got %q", "My Macro Test", got.Name)
	}
	if len(got.Steps) != 2 || got.Steps[0].Action != "hold" || got.Steps[1].Action != "release" {
		t.Errorf("expected hold+release steps, got %+v", got.Steps)
	}
}

func TestSaveScriptCreatesDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "dir")
	path, err := SaveScript(scriptFixture(), dir)
	if err != nil {
		t.Fatalf("SaveScript into missing dir failed: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected file at %q, got error: %v", path, err)
	}
}

func TestCopyScript(t *testing.T) {
	old := capture.CopyTextToClipboard
	defer func() { capture.CopyTextToClipboard = old }()

	var copied string
	capture.CopyTextToClipboard = func(text string) error {
		copied = text
		return nil
	}

	if err := CopyScript(scriptFixture()); err != nil {
		t.Fatalf("CopyScript failed: %v", err)
	}
	if !strings.Contains(copied, "action: hold") {
		t.Errorf("clipboard should contain hold action, got:\n%s", copied)
	}
	if !strings.Contains(copied, "My Macro Test") {
		t.Errorf("clipboard should contain script name, got:\n%s", copied)
	}
}

func TestRenderChain(t *testing.T) {
	old := capture.CopyTextToClipboard
	defer func() { capture.CopyTextToClipboard = old }()

	var copied string
	capture.CopyTextToClipboard = func(text string) error {
		copied = text
		return nil
	}

	dir := t.TempDir()
	results, err := Render([]string{TaskSaveScript, TaskCopyScript}, scriptFixture(), dir)
	if err != nil {
		t.Fatalf("Render failed: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d: %v", len(results), results)
	}
	if _, err := os.Stat(filepath.Join(dir, "My_Macro_Test.yaml")); err != nil {
		t.Errorf("expected script file to be written: %v", err)
	}
	if copied == "" {
		t.Errorf("expected clipboard to receive YAML")
	}
}

func TestRenderUnknownTaskFailsLoudly(t *testing.T) {
	_, err := Render([]string{"bogus_task"}, scriptFixture(), t.TempDir())
	if err == nil {
		t.Fatal("expected unknown task to fail loudly")
	}
	if !strings.Contains(err.Error(), "unknown output task") {
		t.Fatalf("expected 'unknown output task' error, got %v", err)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
