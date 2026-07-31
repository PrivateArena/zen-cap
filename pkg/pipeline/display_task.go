package pipeline

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"zen-cap/pkg/capture"
	"zen-cap/pkg/config"
)

type DisplayTask struct{}

func (DisplayTask) Name() string { return "display" }

func (DisplayTask) Enabled(cfg *config.Config, r *Result) bool {
	return r.Image != nil && len(r.OCRBoxes) > 0
}

func (DisplayTask) Requires() []string { return []string{"ocr"} }

func (DisplayTask) Terminal() bool { return true }

// Run renders the OCR boxes onto the image, saves an overlay PNG and blocks in
// the modal overlay window until dismissed (one-shot OCR display).
func (DisplayTask) Run(ctx context.Context, r *Result, cfg *config.Config, opts *Options) error {
	rgba := capture.RenderOCRBoxes(r.Image, r.OCRBoxes)

	timestamp := time.Now().Format("20060102_150405")
	filename := filepath.Join(cfg.OutputDir, fmt.Sprintf("ocr_overlay_%s.png", timestamp))
	_ = os.MkdirAll(cfg.OutputDir, 0755)
	if err := capture.SavePNG(rgba, filename); err != nil {
		return fmt.Errorf("failed to save OCR overlay image: %w", err)
	}
	fmt.Printf("[OCR Overlay] Saved overlay image to %s\n", filename)

	return capture.ShowOCROverlayWindow(rgba, r.OffsetX, r.OffsetY)
}
