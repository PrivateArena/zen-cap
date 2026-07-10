package pipeline

import (
	"context"
	"fmt"
	"image"
	"image/png"
	"os"
	"os/exec"
	"strings"

	"zen-cap/pkg/capture"
	"zen-cap/pkg/config"
)

type EditTask struct{}

func (EditTask) Name() string { return "edit" }

func (EditTask) Enabled(cfg *config.Config) bool { return cfg.Edit.Enabled }

func (EditTask) Run(ctx context.Context, r *Result, cfg *config.Config) error {
	if cfg.Edit.Mode == "external" {
		return runExternalEditor(r, cfg)
	}
	return runBuiltinAnnotator(r, cfg)
}

// runExternalEditor launches a configured editor command, blocks until it
// exits, then reloads the (possibly modified) file back into the pipeline.
func runExternalEditor(r *Result, cfg *config.Config) error {
	if cfg.Edit.ExternalCmd == "" {
		return fmt.Errorf("edit.mode is 'external' but edit.external_cmd is empty")
	}
	cmdline := strings.ReplaceAll(cfg.Edit.ExternalCmd, "{file}", r.OutputPath)
	parts := strings.Fields(cmdline)
	if len(parts) == 0 {
		return fmt.Errorf("edit.external_cmd resolved to an empty command")
	}
	cmd := exec.Command(parts[0], parts[1:]...)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("external editor failed: %w", err)
	}
	f, err := os.Open(r.OutputPath)
	if err != nil {
		return fmt.Errorf("failed to reopen edited file: %w", err)
	}
	defer f.Close()
	img, err := png.Decode(f)
	if err != nil {
		return fmt.Errorf("failed to decode edited PNG: %w", err)
	}
	r.Image = img
	return nil
}

// runBuiltinAnnotator opens the internal notation overlay on the already-
// captured image and re-saves it once the user confirms.
func runBuiltinAnnotator(r *Result, cfg *config.Config) error {
	rgba, ok := r.Image.(*image.RGBA)
	if !ok {
		b := r.Image.Bounds()
		tmp := image.NewRGBA(b)
		for y := b.Min.Y; y < b.Max.Y; y++ {
			for x := b.Min.X; x < b.Max.X; x++ {
				tmp.Set(x, y, r.Image.At(x, y))
			}
		}
		rgba = tmp
	}

	edited, err := capture.InteractiveAnnotate(rgba, cfg.Edit.BrushThickness, cfg.Edit.FontScale)
	if err != nil {
		return fmt.Errorf("interactive annotation failed: %w", err)
	}
	r.Image = edited

	f, err := os.Create(r.OutputPath)
	if err != nil {
		return fmt.Errorf("failed to reopen output file for re-save: %w", err)
	}
	defer f.Close()
	return png.Encode(f, edited)
}
