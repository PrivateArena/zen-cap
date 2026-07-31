package pipeline

import (
	"context"
	"fmt"
	"strings"

	"zen-cap/pkg/capture"
	"zen-cap/pkg/config"
)

type OCRTask struct{}

func (OCRTask) Name() string { return "ocr" }

func (OCRTask) Enabled(cfg *config.Config, r *Result) bool { return r.Image != nil }

func (OCRTask) Requires() []string { return nil }

func (OCRTask) Terminal() bool { return false }

func (OCRTask) Run(ctx context.Context, r *Result, cfg *config.Config, opts *Options) error {
	results, err := capture.PerformOCRWithDetails(r.Image, cfg.OCRAddress, cfg.OCRLanguage)
	if err != nil {
		return fmt.Errorf("OCR failed: %w", err)
	}
	var lines []string
	for _, res := range results {
		if res.Text != "" {
			lines = append(lines, res.Text)
		}
	}
	r.OCRBoxes = results
	r.Text = strings.Join(lines, "\n")
	return nil
}
