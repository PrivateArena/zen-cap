package capture

import (
	"fmt"
	"image"
	"image/png"
	"os"

	"zen-cap/pkg/av"

	"github.com/asticode/go-astiav"
)

type CaptureConfig struct {
	Display         string
	X               int
	Y               int
	Width           int
	Height          int
	WindowID        uint32
	Interactive     bool
	WindowSelect    bool
	ClipboardAction *string // Dynamic clipboard action output pointer
}

// CaptureScreen opens the display capture device, grabs one frame,
// converts it to RGBA, and returns it as a Go image.Image.
var CaptureScreen func(cfg CaptureConfig) (image.Image, error)

func init() {
	CaptureScreen = captureScreenImpl
}

func captureScreenImpl(cfg CaptureConfig) (image.Image, error) {
	if cfg.Interactive {
		// First capture the entire screen
		fullCfg := cfg
		fullCfg.Interactive = false
		fullCfg.X = -1
		fullCfg.Y = -1
		fullCfg.Width = 0
		fullCfg.Height = 0
		fullCfg.WindowID = 0

		fullImg, err := CaptureScreen(fullCfg)
		if err != nil {
			return nil, fmt.Errorf("failed to capture base screen for interactive selection: %w", err)
		}

		if cfg.WindowSelect {
			return InteractiveSelectWindowExt(fullImg, cfg.ClipboardAction)
		}
		return InteractiveSelectRegionExt(fullImg, cfg.ClipboardAction)
	}

	devCfg := av.DeviceConfig{
		Display:  cfg.Display,
		X:        cfg.X,
		Y:        cfg.Y,
		Width:    cfg.Width,
		Height:   cfg.Height,
		FPS:      5,
		WindowID: cfg.WindowID,
	}

	device, err := av.OpenDevice(devCfg)
	if err != nil {
		return nil, fmt.Errorf("failed to open capture device: %w", err)
	}
	defer device.Close()

	// ReadFrame also latches the real pixel format inside the device.
	srcFrame, err := device.ReadFrame()
	if err != nil {
		return nil, fmt.Errorf("failed to read frame: %w", err)
	}

	w := device.Width()
	h := device.Height()

	// FIX: Use device.PixelFormat() AFTER ReadFrame() so we get the format
	// actually present in the decoded frame, not the codec-context guess.
	srcPixFmt := device.PixelFormat()

	dstFrame := astiav.AllocFrame()
	defer dstFrame.Free()
	dstFrame.SetWidth(w)
	dstFrame.SetHeight(h)
	dstFrame.SetPixelFormat(astiav.PixelFormatRgba)
	if err := dstFrame.AllocBuffer(1); err != nil {
		return nil, fmt.Errorf("failed to allocate destination frame buffer: %w", err)
	}

	scaler, err := av.NewScaler(w, h, srcPixFmt, w, h, astiav.PixelFormatRgba)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize scaler: %w", err)
	}
	defer scaler.Close()

	if err := scaler.Scale(srcFrame, dstFrame); err != nil {
		return nil, fmt.Errorf("failed to scale frame: %w", err)
	}

	img := image.NewRGBA(image.Rect(0, 0, w, h))
	if err := dstFrame.Data().ToImage(img); err != nil {
		return nil, fmt.Errorf("failed to convert frame to Go image: %w", err)
	}

	return img, nil
}

// SavePNG saves an image.Image to a PNG file.
func SavePNG(img image.Image, path string) error {
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("failed to create output file: %w", err)
	}
	defer f.Close()

	if err := png.Encode(f, img); err != nil {
		return fmt.Errorf("failed to encode PNG: %w", err)
	}
	return nil
}
