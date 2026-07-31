package pipeline

import (
	"context"
	"strings"

	"zen-cap/pkg/capture"
	"zen-cap/pkg/config"
)

type TranslateTask struct{}

func (TranslateTask) Name() string { return "translate" }

func (TranslateTask) Enabled(cfg *config.Config, r *Result) bool { return len(r.OCRBoxes) > 0 }

func (TranslateTask) Requires() []string { return []string{"ocr"} }

func (TranslateTask) Terminal() bool { return false }

func (TranslateTask) Run(ctx context.Context, r *Result, cfg *config.Config, opts *Options) error {
	translated := make([]capture.OCRResult, 0, len(r.OCRBoxes))
	var lines []string
	for _, res := range r.OCRBoxes {
		if res.Text == "" {
			translated = append(translated, res)
			continue
		}
		t, err := capture.TranslateTextFn(cfg.TranslationEngine, cfg.OCRAddress, res.Text, cfg.TranslationTarget)
		if err != nil || t == "" {
			t = res.Text // keep original text for that box (F2)
		}
		res.Text = t
		translated = append(translated, res)
		lines = append(lines, t)
	}
	r.OCRBoxes = translated
	r.Text = strings.Join(lines, "\n")
	return nil
}
