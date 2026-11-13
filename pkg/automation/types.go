package automation

type WindowTarget struct {
	Title string `json:"title,omitempty" yaml:"title,omitempty"`
	Class string `json:"class,omitempty" yaml:"class,omitempty"`
}

type Step struct {
	Action      string        `json:"action" yaml:"action"`
	X           interface{}   `json:"x,omitempty" yaml:"x,omitempty"`
	Y           interface{}   `json:"y,omitempty" yaml:"y,omitempty"`
	X2          interface{}   `json:"x2,omitempty" yaml:"x2,omitempty"`
	Y2          interface{}   `json:"y2,omitempty" yaml:"y2,omitempty"`
	Button      string        `json:"button,omitempty" yaml:"button,omitempty"` // left, right, middle
	Text        string        `json:"text,omitempty" yaml:"text,omitempty"`
	Keys        string        `json:"keys,omitempty" yaml:"keys,omitempty"`
	Duration    string        `json:"duration,omitempty" yaml:"duration,omitempty"` // e.g. "2s", "500ms"
	Image       string        `json:"image,omitempty" yaml:"image,omitempty"`
	Timeout     string        `json:"timeout,omitempty" yaml:"timeout,omitempty"`
	Confidence  float64       `json:"confidence,omitempty" yaml:"confidence,omitempty"` // 0.0 - 1.0
	Then        string        `json:"then,omitempty" yaml:"then,omitempty"`             // click, move, none
	OffsetX     int           `json:"offset_x,omitempty" yaml:"offset_x,omitempty"`
	OffsetY     int           `json:"offset_y,omitempty" yaml:"offset_y,omitempty"`
	Relative    bool          `json:"relative,omitempty" yaml:"relative,omitempty"`
	Output      string        `json:"output,omitempty" yaml:"output,omitempty"`
	Region      string        `json:"region,omitempty" yaml:"region,omitempty"`
	Command     string        `json:"command,omitempty" yaml:"command,omitempty"`
	Mode        string        `json:"mode,omitempty" yaml:"mode,omitempty"` // write, read
	Title       string        `json:"title,omitempty" yaml:"title,omitempty"`
	Message     string        `json:"message,omitempty" yaml:"message,omitempty"`
	Count       int           `json:"count,omitempty" yaml:"count,omitempty"`
	Steps       []Step        `json:"steps,omitempty" yaml:"steps,omitempty"`   // for loop, if_found
	Find        string        `json:"find,omitempty" yaml:"find,omitempty"`     // image, text
	Type        string        `json:"type,omitempty" yaml:"type,omitempty"`     // alias for find
	Target      string        `json:"target,omitempty" yaml:"target,omitempty"` // alias for image/text/find_target/goto/call
	Else        []Step        `json:"else,omitempty" yaml:"else,omitempty"`
	Delay       string        `json:"delay,omitempty" yaml:"delay,omitempty"`
	WaitTimeout string        `json:"wait_timeout,omitempty" yaml:"wait_timeout,omitempty"`
	Language    string        `json:"language,omitempty" yaml:"language,omitempty"`
	Model       string        `json:"model,omitempty" yaml:"model,omitempty"`
	Window      *WindowTarget `json:"window,omitempty" yaml:"window,omitempty"`
	Color       string        `json:"color,omitempty" yaml:"color,omitempty"`
	Tolerance   int           `json:"tolerance,omitempty" yaml:"tolerance,omitempty"`

	// Scripting extensions:
	Label string                 `json:"label,omitempty" yaml:"label,omitempty"`
	When  string                 `json:"when,omitempty" yaml:"when,omitempty"`
	Name  string                 `json:"name,omitempty" yaml:"name,omitempty"`
	Value interface{}            `json:"value,omitempty" yaml:"value,omitempty"`
	Args  map[string]interface{} `json:"args,omitempty" yaml:"args,omitempty"`

	// Pointer gestures and waits (see AUTOMATION_API.md).
	//
	// Every field here is omitempty on both tags, so an existing YAML script
	// marshals byte-identically after this change — asserted by
	// TestExampleScriptRoundTripsUnchanged in manager_test.go.
	Direction  string `json:"direction,omitempty" yaml:"direction,omitempty"`     // scroll: up|down|left|right
	Amount     int    `json:"amount,omitempty" yaml:"amount,omitempty"`           // scroll: tick count (default 3, clamped to 50)
	Clicks     int    `json:"clicks,omitempty" yaml:"clicks,omitempty"`           // click: repeat count (1 = single, 2 = double, 3 = triple)
	ClickGap   string `json:"click_gap,omitempty" yaml:"click_gap,omitempty"`     // click: gap between repeats, default "40ms"
	WaitFor    string `json:"wait_for,omitempty" yaml:"wait_for,omitempty"`       // wait_until: window|window_gone|image|text|pixel
	NoActivate bool   `json:"no_activate,omitempty" yaml:"no_activate,omitempty"` // click: inverse polarity, false (absent) = raise+activate first

	// ChunkRunes bounds how much text a single `type` step pushes through XTEST
	// before checking the abort channel; 0 = default (200).
	ChunkRunes int `json:"chunk_runes,omitempty" yaml:"chunk_runes,omitempty"`
}

type Script struct {
	Name string `json:"name" yaml:"name"`
	// Target selects the backend (x11/vnc/adb/wda/vfb). Omit for default X11.
	Target    *TargetConfig     `json:"target,omitempty" yaml:"target,omitempty"`
	Window    *WindowTarget     `json:"window,omitempty" yaml:"window,omitempty"`
	Steps     []Step            `json:"steps" yaml:"steps"`
	Functions map[string][]Step `json:"functions,omitempty" yaml:"functions,omitempty"`
}

// TargetConfig is the YAML-level mirror of target.Config.
// Defined here to avoid an import cycle: automation → target → capture.
type TargetConfig struct {
	Type       string  `yaml:"type,omitempty"        json:"type,omitempty"`
	Display    string  `yaml:"display,omitempty"     json:"display,omitempty"`
	Host       string  `yaml:"host,omitempty"        json:"host,omitempty"`
	Port       int     `yaml:"port,omitempty"        json:"port,omitempty"`
	Password   string  `yaml:"password,omitempty"    json:"password,omitempty"`
	Serial     string  `yaml:"serial,omitempty"      json:"serial,omitempty"`
	WDAHost    string  `yaml:"wda_host,omitempty"    json:"wda_host,omitempty"`
	VFBDisplay string  `yaml:"vfb_display,omitempty" json:"vfb_display,omitempty"`
	VFBSerial  string  `yaml:"vfb_serial,omitempty"  json:"vfb_serial,omitempty"`
	VFBWidth   int     `yaml:"vfb_width,omitempty"   json:"vfb_width,omitempty"`
	VFBHeight  int     `yaml:"vfb_height,omitempty"  json:"vfb_height,omitempty"`
	Scale      float64 `yaml:"scale,omitempty"       json:"scale,omitempty"`
}
