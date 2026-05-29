// [VERIFIED]
package capture

import (
	"fmt"
	"image"
	"image/png"
	"os"

	"github.com/asticode/go-astiav"
	"zen-cap/pkg/av"
)

type CaptureConfig struct {
	Display  string
	X        int
	Y        int
	Width    int
	Height   int
	WindowID uint32
}

// CaptureScreen opens the display capture device, grabs one frame,
// converts it to RGBA, and returns it as a Go image.Image.
func CaptureScreen(cfg CaptureConfig) (image.Image, error) {
	// Initialize and open capture device
	devCfg := av.DeviceConfig{
		Display:  cfg.Display,
		X:        cfg.X,
		Y:        cfg.Y,
		Width:    cfg.Width,
		Height:   cfg.Height,
		FPS:      5, // Low fps is fine for a single screenshot
		WindowID: cfg.WindowID,
	}

	device, err := av.OpenDevice(devCfg)
	if err != nil {
		return nil, fmt.Errorf("failed to open capture device: %w", err)
	}
	defer device.Close()

	// Read one frame
	srcFrame, err := device.ReadFrame()
	if err != nil {
		return nil, fmt.Errorf("failed to read frame: %w", err)
	}

	w := device.Width()
	h := device.Height()

	// Prepare destination frame in RGBA format
	dstFrame := astiav.AllocFrame()
	defer dstFrame.Free()

	dstFrame.SetWidth(w)
	dstFrame.SetHeight(h)
	dstFrame.SetPixelFormat(astiav.PixelFormatRgba)

	if err := dstFrame.AllocBuffer(1); err != nil {
		return nil, fmt.Errorf("failed to allocate destination frame buffer: %w", err)
	}

	// Create scaler to convert from source pixel format (e.g. BGR0/BGRA) to RGBA
	scaler, err := av.NewScaler(w, h, device.PixelFormat(), w, h, astiav.PixelFormatRgba)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize scaler: %w", err)
	}
	defer scaler.Close()

	// Scale and convert format
	if err := scaler.Scale(srcFrame, dstFrame); err != nil {
		return nil, fmt.Errorf("failed to scale frame: %w", err)
	}

	// Populate Go image
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
