package pipeline

import (
	"context"
	"fmt"

	"zen-cap/pkg/capture"
	"zen-cap/pkg/config"
)

type CopyTextTask struct{}

func (CopyTextTask) Name() string { return "copy_text" }

func (CopyTextTask) Enabled(cfg *config.Config, r *Result) bool { return r.Text != "" }

func (CopyTextTask) Requires() []string { return nil }

func (CopyTextTask) Terminal() bool { return false }

func (CopyTextTask) Run(ctx context.Context, r *Result, cfg *config.Config, opts *Options) error {
	if err := capture.SpawnClipboardDaemon("--text", r.Text); err != nil {
		return fmt.Errorf("clipboard text copy failed: %w", err)
	}
	fmt.Printf("[Clipboard] Copied text to clipboard (%d chars).\n", len(r.Text))
	if !r.Quiet {
		sendNotification("Zen-Cap", "Copied text to clipboard!")
	}
	return nil
}

type CopyPathTask struct{}

func (CopyPathTask) Name() string { return "copy_path" }

func (CopyPathTask) Enabled(cfg *config.Config, r *Result) bool { return r.FilePath != "" }

func (CopyPathTask) Requires() []string { return nil }

func (CopyPathTask) Terminal() bool { return false }

func (CopyPathTask) Run(ctx context.Context, r *Result, cfg *config.Config, opts *Options) error {
	if err := capture.SpawnClipboardDaemon("--text", r.FilePath); err != nil {
		return fmt.Errorf("clipboard path copy failed: %w", err)
	}
	fmt.Printf("[Clipboard] Copied path to clipboard: %s\n", r.FilePath)
	if !r.Quiet {
		sendNotification("Zen-Cap", "Copied image file path to clipboard!")
	}
	return nil
}

type CopyImageTask struct{}

func (CopyImageTask) Name() string { return "copy_image" }

func (CopyImageTask) Enabled(cfg *config.Config, r *Result) bool {
	return r.Image != nil && r.FilePath != ""
}

func (CopyImageTask) Requires() []string { return nil }

func (CopyImageTask) Terminal() bool { return false }

func (CopyImageTask) Run(ctx context.Context, r *Result, cfg *config.Config, opts *Options) error {
	if err := capture.SpawnClipboardDaemon("--image", r.FilePath); err != nil {
		return fmt.Errorf("clipboard image copy failed: %w", err)
	}
	fmt.Println("[Clipboard] Copied image to clipboard.")
	if !r.Quiet {
		sendNotification("Zen-Cap", "Copied captured image to clipboard!")
	}
	return nil
}

type CopyURLTask struct{}

func (CopyURLTask) Name() string { return "copy_url" }

func (CopyURLTask) Enabled(cfg *config.Config, r *Result) bool { return r.UploadURL != "" }

func (CopyURLTask) Requires() []string { return []string{"upload"} }

func (CopyURLTask) Terminal() bool { return false }

func (CopyURLTask) Run(ctx context.Context, r *Result, cfg *config.Config, opts *Options) error {
	if err := capture.SpawnClipboardDaemon("--text", r.UploadURL); err != nil {
		return fmt.Errorf("clipboard URL copy failed: %w", err)
	}
	fmt.Printf("[Clipboard] Copied upload URL to clipboard: %s\n", r.UploadURL)
	if !r.Quiet {
		sendNotification("Zen-Cap", "Copied upload URL to clipboard!")
	}
	return nil
}

type CopyLLMTask struct{}

func (CopyLLMTask) Name() string { return "copy_llm" }

func (CopyLLMTask) Enabled(cfg *config.Config, r *Result) bool { return r.LLMText != "" }

func (CopyLLMTask) Requires() []string { return []string{"vision"} }

func (CopyLLMTask) Terminal() bool { return false }

func (CopyLLMTask) Run(ctx context.Context, r *Result, cfg *config.Config, opts *Options) error {
	if err := capture.SpawnClipboardDaemon("--text", r.LLMText); err != nil {
		return fmt.Errorf("clipboard LLM text copy failed: %w", err)
	}
	fmt.Printf("[Clipboard] Copied LLM explanation to clipboard (%d chars).\n", len(r.LLMText))
	if !r.Quiet {
		sendNotification("Zen-Cap", "Copied LLM explanation to clipboard!")
	}
	return nil
}
