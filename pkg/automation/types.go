package automation

type WindowTarget struct {
	Title string `json:"title,omitempty" yaml:"title,omitempty"`
	Class string `json:"class,omitempty" yaml:"class,omitempty"`
}

type Step struct {
	Action      string                 `json:"action" yaml:"action"`
	X           interface{}            `json:"x,omitempty" yaml:"x,omitempty"`
	Y           interface{}            `json:"y,omitempty" yaml:"y,omitempty"`
	Button      string                 `json:"button,omitempty" yaml:"button,omitempty"` // left, right, middle
	Text        string                 `json:"text,omitempty" yaml:"text,omitempty"`
	Keys        string                 `json:"keys,omitempty" yaml:"keys,omitempty"`
	Duration    string                 `json:"duration,omitempty" yaml:"duration,omitempty"` // e.g. "2s", "500ms"
	Image       string                 `json:"image,omitempty" yaml:"image,omitempty"`
	Timeout     string                 `json:"timeout,omitempty" yaml:"timeout,omitempty"`
	Confidence  float64                `json:"confidence,omitempty" yaml:"confidence,omitempty"` // 0.0 - 1.0
	Then        string                 `json:"then,omitempty" yaml:"then,omitempty"`             // click, move, none
	OffsetX     int                    `json:"offset_x,omitempty" yaml:"offset_x,omitempty"`
	OffsetY     int                    `json:"offset_y,omitempty" yaml:"offset_y,omitempty"`
	Relative    bool                   `json:"relative,omitempty" yaml:"relative,omitempty"`
	Output      string                 `json:"output,omitempty" yaml:"output,omitempty"`
	Region      string                 `json:"region,omitempty" yaml:"region,omitempty"`
	Command     string                 `json:"command,omitempty" yaml:"command,omitempty"`
	Mode        string                 `json:"mode,omitempty" yaml:"mode,omitempty"` // write, read
	Title       string                 `json:"title,omitempty" yaml:"title,omitempty"`
	Message     string                 `json:"message,omitempty" yaml:"message,omitempty"`
	Count       int                    `json:"count,omitempty" yaml:"count,omitempty"`
	Steps       []Step                 `json:"steps,omitempty" yaml:"steps,omitempty"` // for loop, if_found
	Find        string                 `json:"find,omitempty" yaml:"find,omitempty"`   // image, text
	Type        string                 `json:"type,omitempty" yaml:"type,omitempty"`   // alias for find
	Target      string                 `json:"target,omitempty" yaml:"target,omitempty"` // alias for image/text/find_target/goto/call
	Else        []Step                 `json:"else,omitempty" yaml:"else,omitempty"`
	Delay       string                 `json:"delay,omitempty" yaml:"delay,omitempty"`
	WaitTimeout string                 `json:"wait_timeout,omitempty" yaml:"wait_timeout,omitempty"`
	Language    string                 `json:"language,omitempty" yaml:"language,omitempty"`
	Model       string                 `json:"model,omitempty" yaml:"model,omitempty"`

	// Scripting extensions:
	Label       string                 `json:"label,omitempty" yaml:"label,omitempty"`
	When        string                 `json:"when,omitempty" yaml:"when,omitempty"`
	Name        string                 `json:"name,omitempty" yaml:"name,omitempty"`
	Value       interface{}            `json:"value,omitempty" yaml:"value,omitempty"`
	Args        map[string]interface{} `json:"args,omitempty" yaml:"args,omitempty"`
}

type Script struct {
	Name      string            `json:"name" yaml:"name"`
	Window    *WindowTarget     `json:"window,omitempty" yaml:"window,omitempty"`
	Steps     []Step            `json:"steps" yaml:"steps"`
	Functions map[string][]Step `json:"functions,omitempty" yaml:"functions,omitempty"`
}
