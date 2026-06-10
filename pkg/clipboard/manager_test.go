package clipboard

import (
	"os"
	"path/filepath"
	"testing"

	"zen-cap/pkg/config"
)

func TestClipboardManagerMode(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "zencap-clip-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cfg := &config.Config{
		ClipboardSessionFile: filepath.Join(tmpDir, "session.json"),
		SnippetMode:          "type",
	}

	mgr, err := NewManager(cfg)
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}

	if mgr.cfg.SnippetMode != "type" {
		t.Errorf("expected SnippetMode 'type', got %s", mgr.cfg.SnippetMode)
	}

	// Test paste from empty slot should not crash or fail unexpectedly
	mgr.PasteFromSlot(0)
}
