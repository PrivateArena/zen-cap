// Package target provides a universal abstraction over screen capture and input
// injection for any device or display (local X11, VNC, Android ADB, iOS WDA,
// or a managed Xvfb virtual display).
package target

import (
	"fmt"
	"image"
)

// Target is the single interface every backend must satisfy.
// Coordinates are always in "template space" — the same pixel space as the
// screenshots returned by Screenshot(). Each backend handles the translation to
// device-native coordinates internally (see Config.Scale).
type Target interface {
	// Screenshot captures the current screen state.
	Screenshot() (image.Image, error)
	// ScreenSize returns the pixel dimensions in template space (after scaling).
	ScreenSize() (w, h int)
	// Click injects a pointer press+release at (x, y). button: "left","right","middle".
	Click(x, y int, button string) error
	// Move moves the pointer to (x, y) without clicking.
	Move(x, y int) error
	// Type injects text character by character. delayMs controls inter-key delay.
	Type(text string, delayMs int64) error
	// Key injects a key combo, e.g. "ctrl-c", "Return", "F5".
	Key(keys string) error
	// Scroll injects a scroll event at (x, y) with delta (dx, dy).
	Scroll(x, y, dx, dy int) error
	// Close releases all resources.
	Close() error
}

// Config is the YAML-serialisable configuration for any target type.
// Only the fields relevant to the chosen Type need to be set.
type Config struct {
	// Type selects the backend. One of: "x11", "vnc", "adb", "wda", "vfb".
	// Defaults to "x11" when empty.
	Type string `yaml:"type" json:"type"`

	// --- X11 ---
	// Display is the X11 display string, e.g. ":0.0". Defaults to ":0.0".
	Display string `yaml:"display,omitempty" json:"display,omitempty"`

	// --- VNC ---
	Host     string `yaml:"host,omitempty" json:"host,omitempty"`
	Port     int    `yaml:"port,omitempty" json:"port,omitempty"`
	Password string `yaml:"password,omitempty" json:"password,omitempty"`

	// --- ADB (Android) ---
	// Serial is the ADB device serial, e.g. "192.168.1.5:5555" or "emulator-5554".
	// Leave empty to use the single connected device.
	Serial string `yaml:"serial,omitempty" json:"serial,omitempty"`

	// --- WDA (iOS WebDriverAgent) ---
	// WDAHost is the host:port of a running WebDriverAgent server, e.g. "localhost:8100".
	WDAHost string `yaml:"wda_host,omitempty" json:"wda_host,omitempty"`

	// --- VFB (managed Xvfb + scrcpy) ---
	// VFBDisplay is the X display to create, e.g. ":99". Auto-selected if empty.
	VFBDisplay string `yaml:"vfb_display,omitempty" json:"vfb_display,omitempty"`
	// VFBSerial is the ADB serial passed to scrcpy. Required for vfb type.
	VFBSerial string `yaml:"vfb_serial,omitempty" json:"vfb_serial,omitempty"`
	// VFBWidth / VFBHeight are the Xvfb virtual screen dimensions.
	VFBWidth  int `yaml:"vfb_width,omitempty"  json:"vfb_width,omitempty"`
	VFBHeight int `yaml:"vfb_height,omitempty" json:"vfb_height,omitempty"`

	// --- Common ---
	// Scale resizes screenshots before returning them, e.g. 0.5 = half resolution.
	// Click coordinates are automatically multiplied by (1/Scale) before being
	// sent to the device, so the automation layer always works in template space.
	// 0 or 1.0 means no scaling (native resolution).
	Scale float64 `yaml:"scale,omitempty" json:"scale,omitempty"`
}

// New constructs a Target from cfg. windowID is X11-specific: it constrains
// capture and click offsets to that window. Ignored for non-x11 types.
func New(cfg Config, windowID uint32) (Target, error) {
	switch cfg.Type {
	case "", "x11":
		return NewX11Target(cfg, windowID)
	case "vnc":
		return NewVNCTarget(cfg)
	case "adb":
		return NewADBTarget(cfg)
	case "wda":
		return NewWDATarget(cfg)
	case "vfb":
		return NewVFBTarget(cfg)
	default:
		return nil, fmt.Errorf("target: unknown type %q", cfg.Type)
	}
}
