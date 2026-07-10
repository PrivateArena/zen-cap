package pipeline

import (
	"context"
	"image"
	"testing"

	"zen-cap/pkg/config"
)

func TestPipelineNew(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.AfterCaptureTasks = []string{"edit", "vision", "invalid-task", "clipboard"}

	p := New(cfg)
	if len(p.tasks) != 3 {
		t.Fatalf("expected 3 tasks, got %d", len(p.tasks))
	}

	if p.tasks[0].Name() != "edit" {
		t.Errorf("expected task 0 to be edit, got %s", p.tasks[0].Name())
	}
	if p.tasks[1].Name() != "vision" {
		t.Errorf("expected task 1 to be vision, got %s", p.tasks[1].Name())
	}
	if p.tasks[2].Name() != "clipboard" {
		t.Errorf("expected task 2 to be clipboard, got %s", p.tasks[2].Name())
	}
}

func TestPipelineRunNoop(t *testing.T) {
	cfg := config.DefaultConfig()
	// Disable all tasks to ensure Run behaves as a safe no-op
	cfg.Edit.Enabled = false
	cfg.Uploader.Enabled = false
	cfg.Vision.Enabled = false
	cfg.ClipboardMode = "none"

	img := image.NewRGBA(image.Rect(0, 0, 10, 10))
	res := Run(context.Background(), cfg, img, "test.png", "")

	if res == nil {
		t.Fatal("expected non-nil result")
	}
	if res.OutputPath != "test.png" {
		t.Errorf("expected OutputPath 'test.png', got %s", res.OutputPath)
	}
	if res.UploadURL != "" {
		t.Errorf("expected empty UploadURL, got %s", res.UploadURL)
	}
	if res.LLMText != "" {
		t.Errorf("expected empty LLMText, got %s", res.LLMText)
	}
}
