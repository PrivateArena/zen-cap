package pipeline

import (
	"context"

	"zen-cap/pkg/capture"
	"zen-cap/pkg/config"
)

type DisplayLiveTask struct{}

func (DisplayLiveTask) Name() string { return "display_live" }

func (DisplayLiveTask) Enabled(cfg *config.Config, r *Result) bool {
	return r.Image != nil && len(r.OCRBoxes) > 0
}

func (DisplayLiveTask) Requires() []string { return []string{"ocr"} }

func (DisplayLiveTask) Terminal() bool { return true }

// Run renders the OCR boxes and pushes the frame into the persistent overlay
// sink. It owns no window lifecycle; the loop owns the sink (F8).
func (DisplayLiveTask) Run(ctx context.Context, r *Result, cfg *config.Config, opts *Options) error {
	if opts == nil || opts.DisplaySink == nil {
		return nil
	}
	rgba := capture.RenderOCRBoxes(r.Image, r.OCRBoxes)
	return opts.DisplaySink.Update(rgba)
}
