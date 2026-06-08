package magnifier

import "strings"

// Mode represents the magnifier operating mode.
type Mode int

const (
	ModeOff        Mode = iota // magnifier disabled
	ModeFullscreen             // entire monitor zoomed
	ModeLens                   // loupe window following cursor
)

// LensShape controls the shape of the lens overlay window.
type LensShape int

const (
	ShapeCircle          LensShape = iota // circular loupe
	ShapeSquare                           // square (no mask)
	ShapeVerticalStrip                    // tall narrow strip
	ShapeHorizontalStrip                  // wide short strip
)

// ParseLensShape converts a string (from config.json) to LensShape.
func ParseLensShape(s string) LensShape {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "square":
		return ShapeSquare
	case "vertical", "vstrip", "vertical_strip":
		return ShapeVerticalStrip
	case "horizontal", "hstrip", "horizontal_strip":
		return ShapeHorizontalStrip
	default:
		return ShapeCircle
	}
}

// Config holds all magnifier configuration values loaded from config.json.
type Config struct {
	// Display is the X11 display string, e.g. ":0.0". Empty = default.
	Display string `json:"display"`

	// FullscreenHotkey activates/deactivates fullscreen zoom mode.
	// Format matches xgbutil keybind strings: e.g. "super-f", "mod4-f".
	FullscreenHotkey string `json:"fullscreen_hotkey"`

	// LensHotkey activates/deactivates lens/loupe mode.
	LensHotkey string `json:"lens_hotkey"`

	// ScrollModifier is the modifier key that must be held while scrolling
	// to zoom in/out. Values: "super"(mod4), "alt"(mod1), "ctrl".
	ScrollModifier string `json:"scroll_modifier"`

	// ZoomMin is the minimum zoom factor (must be >= 1.0).
	ZoomMin float64 `json:"zoom_min"`

	// ZoomMax is the maximum zoom factor.
	ZoomMax float64 `json:"zoom_max"`

	// ZoomStep is the zoom increment per scroll tick.
	ZoomStep float64 `json:"zoom_step"`

	// InitialZoom is the starting zoom level when a mode is first activated.
	InitialZoom float64 `json:"initial_zoom"`

	// LensSize is the side length of the lens overlay window in pixels.
	LensSize int `json:"lens_size"`

	// LensShapeStr is the JSON-parsed shape name; parsed into LensShape.
	LensShapeStr string `json:"lens_shape"`

	// SmoothScaling uses bilinear interpolation when true; nearest-neighbor when false.
	SmoothScaling bool `json:"smooth_scaling"`

	// Enabled controls whether the magnifier service starts at all.
	Enabled bool `json:"enabled"`

	// LensShape is the parsed enum; populated after JSON decode.
	LensShape LensShape `json:"-"`
}

// DefaultConfig returns sensible defaults for a first-run magnifier.
func DefaultConfig() Config {
	return Config{
		Display:          ":0.0",
		FullscreenHotkey: "super-f",
		LensHotkey:       "super-l",
		ScrollModifier:   "super",
		ZoomMin:          1.5,
		ZoomMax:          10.0,
		ZoomStep:         0.5,
		InitialZoom:      2.0,
		LensSize:         420,
		LensShapeStr:     "circle",
		SmoothScaling:    true,
		Enabled:          false, // opt-in: user must enable in config.json
		LensShape:        ShapeCircle,
	}
}

// Normalize fills in zero-value fields and parses derived values.
func (c *Config) Normalize() {
	if c.Display == "" {
		c.Display = ":0.0"
	}
	if c.FullscreenHotkey == "" {
		c.FullscreenHotkey = "super-f"
	}
	if c.LensHotkey == "" {
		c.LensHotkey = "super-l"
	}
	if c.ScrollModifier == "" {
		c.ScrollModifier = "super"
	}
	if c.ZoomMin < 1.0 {
		c.ZoomMin = 1.5
	}
	if c.ZoomMax < c.ZoomMin {
		c.ZoomMax = 10.0
	}
	if c.ZoomStep <= 0 {
		c.ZoomStep = 0.5
	}
	if c.InitialZoom < c.ZoomMin {
		c.InitialZoom = c.ZoomMin
	}
	if c.LensSize <= 0 {
		c.LensSize = 420
	}
	c.LensShape = ParseLensShape(c.LensShapeStr)
}
