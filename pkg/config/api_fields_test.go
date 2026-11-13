package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// chdirWithConfig copies the repo's own config.json (which predates the API
// fields and therefore declares none of them) into a scratch dir and makes it
// the working directory, so LoadConfig picks it up as "config.json" relative to
// CWD — the same file the daemon loads when started from the repo root.
//
// Absent keys must come out as the documented defaults, not as Go zero values:
// a zero APIMaxRunDurationSeconds would mean "run for 0 seconds".
func chdirWithConfig(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	src, err := filepath.Abs(filepath.Join("..", "..", "config.json"))
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	data, err := os.ReadFile(src)
	if err != nil {
		t.Skipf("repo config.json not readable from %s: %v", src, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.json"), data, 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	old, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(old) })
}

func TestLoadConfigAPIDefaultsForLegacyFile(t *testing.T) {
	chdirWithConfig(t)

	cfg, path, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if path == "" {
		t.Fatalf("expected the scratch config.json to be loaded, got empty path")
	}

	if cfg.DenyAPIInput {
		t.Errorf("DenyAPIInput = true, want false (absent key must not block input)")
	}
	if cfg.APIMaxRunDurationSeconds != 60 {
		t.Errorf("APIMaxRunDurationSeconds = %d, want 60", cfg.APIMaxRunDurationSeconds)
	}
	if cfg.APIMaxInlineBytes != 8388608 {
		t.Errorf("APIMaxInlineBytes = %d, want 8388608", cfg.APIMaxInlineBytes)
	}
}

// TestAPIFieldsRoundTrip guards the wire names: the HTTP docs promise
// deny_api_input / api_max_run_duration_seconds / api_max_inline_bytes.
func TestAPIFieldsRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	cfg := DefaultConfig()
	cfg.OutputDir = dir
	cfg.DenyAPIInput = true
	cfg.APIMaxRunDurationSeconds = 5
	cfg.APIMaxInlineBytes = 1024
	if err := SaveConfig(cfg, path); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	for _, key := range []string{"deny_api_input", "api_max_run_duration_seconds", "api_max_inline_bytes"} {
		if !strings.Contains(string(raw), `"`+key+`"`) {
			t.Errorf("saved config does not contain key %q", key)
		}
	}

	loaded, err := readConfig(path, dir, false)
	if err != nil {
		t.Fatalf("readConfig: %v", err)
	}
	if !loaded.DenyAPIInput {
		t.Errorf("DenyAPIInput = false, want true")
	}
	if loaded.APIMaxRunDurationSeconds != 5 {
		t.Errorf("APIMaxRunDurationSeconds = %d, want 5 (an explicit value must survive the <=0 fixup)", loaded.APIMaxRunDurationSeconds)
	}
	if loaded.APIMaxInlineBytes != 1024 {
		t.Errorf("APIMaxInlineBytes = %d, want 1024", loaded.APIMaxInlineBytes)
	}
}

// TestDefaultConstructorsAgree: the repo has two default constructors and the
// fixups live in a third place (readConfig). All three must agree, otherwise a
// daemon started without a config file gets different limits than one started
// with a legacy file.
func TestDefaultConstructorsAgree(t *testing.T) {
	a := DefaultConfig()
	b := DefaultPortableConfig(t.TempDir())
	for _, c := range []*Config{a, b} {
		if c.APIMaxRunDurationSeconds != 60 || c.APIMaxInlineBytes != 8<<20 || c.DenyAPIInput {
			t.Errorf("API defaults mismatch: deny=%v run=%d inline=%d", c.DenyAPIInput, c.APIMaxRunDurationSeconds, c.APIMaxInlineBytes)
		}
	}
}
