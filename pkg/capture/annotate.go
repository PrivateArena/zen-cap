package capture

import (
	"fmt"
	"image"
	"image/color"

	"zen-cap/pkg/annotation"
	"zen-cap/pkg/annotation/overlay"
)

func InteractiveAnnotate(img *image.RGBA, brushThickness uint32, fontScale int, display ...string) (*image.RGBA, error) {
	ann := annotation.NewAnnotator(img, annotation.Config{
		BrushThickness: brushThickness,
		FontScale:      fontScale,
		Color:          color.RGBA{R: 255, G: 0, B: 127, A: 255},
	})

	b := img.Bounds()
	var dpy string
	if len(display) > 0 {
		dpy = display[0]
	}
	ov := overlay.NewX11Overlay(ann, overlay.OverlayConfig{
		X:         0,
		Y:         0,
		Width:     b.Dx(),
		Height:    b.Dy(),
		TargetFPS: 30,
		Display:   dpy,
	})

	if err := ov.Start(); err != nil {
		return nil, fmt.Errorf("InteractiveAnnotate: overlay start: %w", err)
	}

	err := ov.WaitDone()
	ov.Stop()

	if err != nil {
		return img, nil
	}

	return ann.GetComposite(), nil
}
