package automation

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
)

// The action catalog is the machine-readable contract of the /automation API.
//
// It is *derived*, not written prose: the action names come from the dispatch
// table (leafActions) plus the control-flow list, so a new action appears in
// GET /automation/actions the moment it exists, and catalog_test.go fails if a
// dispatch key has no documentation entry. A hand-written catalog plus a
// go/parser drift test would break on any refactor of the dispatcher; this one
// cannot drift in silence.

// FieldSpec documents one JSON field of a step.
type FieldSpec struct {
	Name        string   `json:"name"`
	Type        string   `json:"type"`
	Description string   `json:"description"`
	Required    bool     `json:"required,omitempty"`
	Enum        []string `json:"enum,omitempty"`
	Default     string   `json:"default,omitempty"`
}

// ActionSpec documents one action.
type ActionSpec struct {
	Action  string `json:"action"`
	Summary string `json:"summary"`
	// InjectsInput marks the actions that move the real pointer or keyboard, and
	// are therefore refused when deny_api_input is true. It is also the flag that
	// decides whether a failed step needs a panic button release.
	InjectsInput bool        `json:"injects_input"`
	Fields       []FieldSpec `json:"fields"`
	// Returns is an example of the `data` payload, for actions that produce one.
	Returns json.RawMessage `json:"returns,omitempty"`
}

// catalogEntry is the documentation half of an action; the name half comes from
// the dispatch tables. AnyOf lists alternative required fields ("at least one of").
type catalogEntry struct {
	summary      string
	injectsInput bool
	fields       []FieldSpec
	anyOf        [][]string
	returns      interface{}
}

// commonFields are valid on every step.
var commonFields = []FieldSpec{
	{Name: "label", Type: "string", Description: "Jump target for a `goto` in the same step list."},
	{Name: "when", Type: "string", Description: "Condition expression; the step is skipped when it is false."},
}

func field(name, typ, desc string, extra ...string) FieldSpec {
	fs := FieldSpec{Name: name, Type: typ, Description: desc}
	for _, e := range extra {
		switch {
		case e == "required":
			fs.Required = true
		case strings.HasPrefix(e, "default="):
			fs.Default = strings.TrimPrefix(e, "default=")
		default:
			fs.Enum = strings.Split(e, "|")
		}
	}
	return fs
}

// coordField is `field` with the integer type pre-filled; a helper keeps the
// coordinate-heavy entries below readable.
func coordField(name, desc string, extra ...string) FieldSpec {
	return field(name, "integer", desc, extra...)
}

// buttonField has no Enum on purpose: pkg/target's resolveButton accepts any raw X
// button number too ("8" for browser Back), and an enum that only listed the three
// named buttons would turn a legal request into a 400.
var buttonField = FieldSpec{
	Name:        "button",
	Type:        "string",
	Description: "Which button: left, middle, right, or a raw X button number such as \"8\" (browser Back).",
	Default:     "left",
}

var catalogDocs = map[string]catalogEntry{
	// --- pointer and keyboard: the actions deny_api_input refuses ---
	"click": {
		summary: "Click at a point (defaults to the previous match, else the centre of the target).", injectsInput: true,
		fields: []FieldSpec{
			coordField("x", "Client-area x; omit to use the last match."),
			coordField("y", "Client-area y; omit to use the last match."),
			buttonField,
			field("clicks", "integer", "Press/release pairs at one warp: 2 is a real double-click.", "default=1"),
			field("click_gap", "string", "Gap between repeats; a duration like \"40ms\".", "default=40ms"),
			field("no_activate", "bool", "true = do not raise+activate the window first. Over HTTP the default raises and focuses, which is what a later `type` needs; YAML runs never activate."),
		},
	},
	"move": {
		summary: "Warp the pointer without clicking.", injectsInput: true,
		fields: []FieldSpec{coordField("x", "Client-area x."), coordField("y", "Client-area y.")},
	},
	"hold": {
		summary: "Press and keep a button down at a point (start of a manual drag).", injectsInput: true,
		fields: []FieldSpec{coordField("x", "Client-area x."), coordField("y", "Client-area y."), buttonField},
	},
	"release": {
		summary: "Release a button at a point (end of a manual drag). Releasing an unheld button is a no-op.", injectsInput: true,
		fields: []FieldSpec{coordField("x", "Client-area x."), coordField("y", "Client-area y."), buttonField},
	},
	"drag": {
		summary: "Press, move through interpolated points, release. Start equals end means a long press.", injectsInput: true,
		fields: []FieldSpec{
			coordField("x", "Start x."), coordField("y", "Start y."),
			coordField("x2", "End x; defaults to x."), coordField("y2", "End y; defaults to y."),
			field("duration", "string", "Total gesture time, so targets that track movement see motion.", "default=300ms"),
			buttonField,
		},
	},
	"type": {
		summary: "Type text through XTEST. Chunked so a long paste stays cancellable.", injectsInput: true,
		fields: []FieldSpec{
			field("text", "string", "The literal text.", "required"),
			field("delay", "string", "Per-key delay.", "default=12ms"),
			field("chunk_runes", "integer", "Runes per XTEST batch; the abort channel is checked between batches.", "default=200"),
		},
	},
	"key": {
		summary: "Press a key chord, e.g. \"ctrl-c\", \"Return\", \"alt+F4\".", injectsInput: true,
		fields: []FieldSpec{field("keys", "string", "The chord.", "required")},
	},
	"scroll": {
		summary: "Scroll wheel clicks at a point (X buttons 4-7).", injectsInput: true,
		fields: []FieldSpec{
			field("direction", "string", "Scroll direction.", "up|down|left|right", "required"),
			field("amount", "integer", "Ticks, clamped to 50.", "default=3"),
			coordField("x", "Client-area x; defaults to the last match, then the centre."),
			coordField("y", "Client-area y; defaults to the last match, then the centre."),
		},
	},

	// --- waiting ---
	"wait": {
		summary: "Sleep. Cancellable.",
		fields:  []FieldSpec{field("duration", "string", "How long.", "default=1s")},
	},
	"wait_until": {
		summary: "Block until a condition holds, instead of the agent polling over HTTP. Bounded by `timeout` and by the run's hard ceiling.",
		fields: []FieldSpec{
			field("wait_for", "string", "What to wait for.", "window|window_gone|image|text|pixel", "required"),
			field("timeout", "string", "Give up after this long.", "default=10s"),
			field("window", "object", "{title,class} selector, for wait_for window/window_gone."),
			field("image", "string", "Template path, for wait_for image."),
			field("text", "string", "Text to look for, for wait_for text."),
			field("confidence", "float", "Match threshold for wait_for image.", "default=0.9"),
			coordField("x", "Pixel x, for wait_for pixel."), coordField("y", "Pixel y, for wait_for pixel."),
			field("color", "string", "#rrggbb, for wait_for pixel."),
			field("tolerance", "integer", "Per-channel colour distance.", "default=0"),
			field("region", "string", "Restrict the search area (see `screenshot`)."),
		},
		anyOf: [][]string{{"window", "image", "text", "color"}},
	},

	// --- capture and reading ---
	"screenshot": {
		summary: "Capture the target to a PNG. Runs no pipeline: no clipboard, no upload, no overlay.",
		fields: []FieldSpec{
			field("output", "string", "Path under the output dir, or an absolute path. Empty = dated file in <output_dir>/automations/."),
			field("region", "string", "\"x,y,w,h\" or a shorthand (TL/BR/full…) resolved against the captured area."),
		},
		returns: map[string]interface{}{"path": "/abs/shot.png", "width": 1920, "height": 1080, "bytes": 41233},
	},
	"window_state": {
		summary: "Read back what the window manager actually did: geometry, mapped state, _NET_WM_STATE atoms.",
		fields:  []FieldSpec{field("window", "object", "{title,class} selector; defaults to the run's window.")},
		returns: map[string]interface{}{"id": 4194305, "id_hex": "0x400001", "title": "Preview", "geometry": map[string]int{"x": 100, "y": 100, "w": 800, "h": 600}, "state": map[string]interface{}{"fullscreen": true}},
	},
	"find_image": {
		summary: "Poll the screen for a template image; sets the coordinate used by later unpositioned steps.",
		fields: []FieldSpec{
			field("image", "string", "Template PNG path (relative to the script or automation dir).", "required"),
			field("confidence", "float", "Match threshold.", "default=0.9"),
			field("timeout", "string", "Search budget.", "default=10s"),
			field("region", "string", "Restrict the search area."),
			field("then", "string", "Act on the match immediately.", "click|move|none", "default=none"),
			field("offset_x", "integer", "x offset applied to a `then` action."),
			field("offset_y", "integer", "y offset applied to a `then` action."),
			field("button", "string", "Button for `then: click`."),
		},
		returns: map[string]interface{}{"x": 812, "y": 233, "confidence": 0.97},
	},
	"find_text": {
		summary: "Poll the screen with OCR for a string.",
		fields: []FieldSpec{
			field("text", "string", "String to find.", "required"),
			field("timeout", "string", "Search budget.", "default=10s"),
			field("language", "string", "OCR language override."),
			field("region", "string", "Restrict the search area."),
			field("then", "string", "Act on the match immediately.", "click|move|none", "default=none"),
			field("offset_x", "integer", "x offset applied to a `then` action."),
			field("offset_y", "integer", "y offset applied to a `then` action."),
		},
		returns: map[string]interface{}{"x": 812, "y": 233, "confidence": 0.91},
	},
	"ocr": {
		summary: "Like find_text but with the model selection exposed, and an optional crop of the matched text box.",
		fields: []FieldSpec{
			field("text", "string", "String to find (or `target`)."),
			field("target", "string", "Alias for text."),
			field("model", "string", "OCR model override."),
			field("language", "string", "OCR language override."),
			field("output", "string", "Write the matched text box image here."),
			field("timeout", "string", "Search budget.", "default=10s"),
			field("region", "string", "Restrict the search area."),
			field("then", "string", "Act on the match immediately.", "click|move|none", "default=none"),
		},
		anyOf:   [][]string{{"text", "target"}},
		returns: map[string]interface{}{"x": 812, "y": 233, "confidence": 0.91, "bounds": map[string]int{"x": 800, "y": 220, "w": 60, "h": 20}},
	},

	// --- window management ---
	"window": {
		summary: "Mutate a window (needs an x11 target). Every mode is read-back verified, so a WM that ignored the request is an error, not a silent success.",
		fields: []FieldSpec{
			field("mode", "string", "What to do.", "activate|close|minimize|maximize|fullscreen|restore|raise|lower|geometry", "required"),
			field("window", "object", "{title,class} selector; defaults to the run's window."),
			coordField("x", "Client x, for mode geometry."), coordField("y", "Client y, for mode geometry."),
			field("offset_x", "integer", "Width, for mode geometry."),
			field("offset_y", "integer", "Height, for mode geometry."),
		},
	},

	// --- everything else ---
	"notify": {
		summary: "Desktop notification (notify-send).",
		fields:  []FieldSpec{field("title", "string", "Notification title.", "default=Zen-Cap"), field("message", "string", "Body.", "default=Action complete.")},
	},
	"log": {
		summary: "Append a line to the run log (returned as `log`).",
		fields:  []FieldSpec{field("message", "string", "The text (or `text`).")},
	},
	"run": {
		summary: "Run a shell command with bash -c. NOT covered by deny_api_input: it injects no X input.",
		fields: []FieldSpec{
			field("command", "string", "The command line.", "required"),
			field("timeout", "string", "Kill the command after this long.", "default=10s"),
		},
	},
	"clipboard": {
		summary: "Write or read the clipboard. NOT covered by deny_api_input.",
		fields: []FieldSpec{
			field("mode", "string", "Read or write.", "write|read", "default=write"),
			field("text", "string", "Payload for mode write."),
			field("name", "string", "Variable to store the clipboard contents in, for mode read."),
		},
	},
	"file": {
		summary: "Read or write a file relative to the script directory. NOT covered by deny_api_input.",
		fields: []FieldSpec{
			field("target", "string", "File path.", "required"),
			field("mode", "string", "File operation.", "write|read|append", "default=write"),
			field("text", "string", "Payload for write/append."),
			field("name", "string", "Variable to store the file contents in, for mode read."),
		},
	},

	// --- control flow (executeStepWithControl) ---
	"loop": {
		summary: "Repeat `steps` count times.",
		fields: []FieldSpec{
			field("count", "integer", "Iterations.", "default=1"),
			field("steps", "array", "The body."),
		},
	},
	"if_found": {
		summary: "Poll for a match and run `steps` on a hit, `else` otherwise. Never fails on a miss.",
		fields: []FieldSpec{
			field("find", "string", "What to look for.", "image|text|ocr"),
			field("type", "string", "Alias of find."),
			field("image", "string", "Template path, for find image."),
			field("text", "string", "String, for find text."),
			field("wait_timeout", "string", "Search budget."),
			field("steps", "array", "Run when found."),
			field("else", "array", "Run when not found."),
		},
		anyOf: [][]string{{"find", "type"}, {"image", "text", "target"}},
	},
	"if_pixel": {
		summary: "Compare one pixel and run `steps` or `else`.",
		fields: []FieldSpec{
			coordField("x", "Pixel x.", "required"), coordField("y", "Pixel y.", "required"),
			field("color", "string", "#rrggbb expected colour.", "required"),
			field("tolerance", "integer", "Per-channel distance.", "default=0"),
			field("steps", "array", "Run on a match."),
			field("else", "array", "Run otherwise."),
		},
	},
	"if_window": {
		summary: "Wait for a window to appear (or, with mode absent, disappear) and branch.",
		fields: []FieldSpec{
			field("window", "object", "{title,class} selector.", "required"),
			field("mode", "string", "absent inverts the condition.", "absent|not_exists"),
			field("wait_timeout", "string", "Search budget."),
			field("steps", "array", "Run when the condition holds."),
			field("else", "array", "Run otherwise."),
		},
	},
	"var": {
		summary: "Set a variable, readable as {{name}} by later steps in the same request.",
		fields: []FieldSpec{
			field("name", "string", "Variable name.", "required"),
			field("value", "any", "Value; strings may reference other variables."),
		},
	},
	"call": {
		summary: "Invoke a function from the script's `functions` map (YAML only: an HTTP request has no function table).",
		fields: []FieldSpec{
			field("target", "string", "Function name.", "required"),
			field("args", "object", "Argument variables, shadowed for the call."),
		},
	},
	"goto": {
		summary: "Jump forward or back to a `label` in the same step list.",
		fields:  []FieldSpec{field("target", "string", "Label to jump to.", "required")},
	},
	"stop": {
		summary: "End the run here. Reported as success with `stopped: true`.",
		fields:  []FieldSpec{field("message", "string", "Why.")},
	},
}

// Catalog returns every action the engine can run, sorted by name: the dispatch
// table's leaf actions plus the control-flow keywords.
func Catalog() []ActionSpec {
	names := make([]string, 0, len(leafActions)+len(controlFlowActions))
	for name := range leafActions {
		names = append(names, name)
	}
	names = append(names, controlFlowActions...)
	sort.Strings(names)

	out := make([]ActionSpec, 0, len(names))
	for _, name := range names {
		doc, ok := catalogDocs[name]
		if !ok {
			// Should never happen: catalog_test.go fails on a missing entry.
			// Emitting the action anyway keeps the endpoint honest about what the
			// engine accepts while a doc entry is being written.
			out = append(out, ActionSpec{Action: name, Summary: "undocumented"})
			continue
		}
		fields := append([]FieldSpec{}, doc.fields...)
		fields = append(fields, commonFields...)
		sort.SliceStable(fields, func(i, j int) bool {
			if fields[i].Required != fields[j].Required {
				return fields[i].Required
			}
			return fields[i].Name < fields[j].Name
		})
		spec := ActionSpec{
			Action:       name,
			Summary:      doc.summary,
			InjectsInput: doc.injectsInput,
			Fields:       fields,
		}
		if doc.returns != nil {
			if b, err := json.Marshal(doc.returns); err == nil {
				spec.Returns = b
			}
		}
		out = append(out, spec)
	}
	return out
}

// LookupAction finds one catalog entry by action name.
func LookupAction(name string) (ActionSpec, bool) {
	for _, spec := range Catalog() {
		if spec.Action == strings.ToLower(name) {
			return spec, true
		}
	}
	return ActionSpec{}, false
}

// CatalogJSON is the body of GET /automation/actions.
func CatalogJSON() ([]byte, error) {
	return json.Marshal(map[string]interface{}{
		"actions":     Catalog(),
		"step_schema": StepSchema(),
	})
}

// StepSchema describes the wire fields of a Step, derived from the struct tags —
// so the schema cannot claim a field the decoder would ignore.
func StepSchema() []FieldSpec {
	t := reflect.TypeOf(Step{})
	out := make([]FieldSpec, 0, t.NumField())
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		name := strings.Split(f.Tag.Get("json"), ",")[0]
		if name == "" || name == "-" {
			continue
		}
		out = append(out, FieldSpec{Name: name, Type: jsonTypeOf(f.Type), Description: stepFieldDocs[name]})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func StepSchemaJSON() ([]byte, error) { return json.Marshal(StepSchema()) }

// stepFieldDocs fills in the one-line meaning of a Step field for the schema
// dump; an empty string is tolerable, an unknown field name is not (the test).
var stepFieldDocs = map[string]string{
	"action": "What to do; see `actions`.", "x": "Primary x coordinate (target space).",
	"y": "Primary y coordinate (target space).", "x2": "Secondary x (drag end).",
	"y2": "Secondary y (drag end).", "button": "Pointer button.", "text": "Literal text payload.",
	"keys": "Key chord.", "duration": "Duration string, e.g. 500ms, 2s.",
	"image": "Template image path.", "timeout": "Deadline for this step.",
	"confidence": "Template match threshold, 0..1.", "then": "Act on a match: click or move.",
	"offset_x": "Offset applied to a `then` action; width for `window: geometry`.",
	"offset_y": "Offset applied to a `then` action; height for `window: geometry`.",
	"relative": "Interpret coordinates relative to the last match.",
	"output":   "Output file path.", "region": "Capture/search region.",
	"command": "Shell command for `run`.", "mode": "Sub-operation selector.",
	"title": "Notification title.", "message": "Notification or log text.",
	"count": "Loop iterations.", "steps": "Nested step list.", "find": "What if_found looks for.",
	"type": "Alias of find.", "target": "Alias of image/text; also goto/call/file target.",
	"else": "Nested steps for the false branch.", "delay": "Per-key delay for `type`.",
	"wait_timeout": "Poll budget for the if_* actions.", "language": "OCR language.",
	"model": "OCR model.", "window": "{title,class} window selector.",
	"color": "Expected #rrggbb colour.", "tolerance": "Per-channel colour distance.",
	"label": "Jump target for goto.", "when": "Condition guarding the step.",
	"name": "Variable name to store a result in.", "value": "Variable value.",
	"args": "Arguments for `call`.", "direction": "Scroll direction.",
	"amount": "Scroll tick count.", "clicks": "Click repeat count.",
	"click_gap": "Gap between repeated clicks.", "wait_for": "Condition for wait_until.",
	"no_activate": "Skip the raise+activate before a click.",
	"chunk_runes": "Runes per `type` batch.",
}

func jsonTypeOf(t reflect.Type) string {
	switch t.Kind() {
	case reflect.String:
		return "string"
	case reflect.Bool:
		return "bool"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return "integer"
	case reflect.Float32, reflect.Float64:
		return "float"
	case reflect.Slice, reflect.Array:
		return "array"
	case reflect.Map:
		return "object"
	case reflect.Struct:
		return "object"
	case reflect.Ptr:
		return jsonTypeOf(t.Elem())
	default:
		return "any"
	}
}

// InjectsInput reports whether any step moves the real pointer or keyboard, i.e.
// whether the request must be refused while deny_api_input is set.
//
// It is also the single place to broaden that predicate: `run`, `file` and
// `clipboard` deliberately do NOT count here, because they mutate nothing on the
// display — the trade-off documented in ZEN_CAP_SERVER_API.md.
func InjectsInput(steps []Step) bool {
	for _, s := range steps {
		if actionInjectsInput(strings.ToLower(s.Action)) {
			return true
		}
		if InjectsInput(s.Steps) || InjectsInput(s.Else) {
			return true
		}
	}
	return false
}

func actionInjectsInput(name string) bool {
	doc, ok := catalogDocs[name]
	return ok && doc.injectsInput
}

// ValidateSteps checks a request against the catalog before anything touches X:
// unknown action, missing required field or a value outside an enum is a 400, not
// a half-executed run.
func ValidateSteps(steps []Step) error {
	for i, step := range steps {
		if err := validateStep(step); err != nil {
			return &RunError{Code: CodeBadRequest, Message: fmt.Sprintf("step %d: %s", i, err), StepIndex: i}
		}
	}
	return nil
}

func validateStep(step Step) error {
	name := strings.ToLower(step.Action)
	if name == "" {
		return fmt.Errorf("missing action")
	}
	if _, ok := leafActions[name]; !ok {
		known := false
		for _, cf := range controlFlowActions {
			if cf == name {
				known = true
				break
			}
		}
		if !known {
			return fmt.Errorf("unknown action: %s (see GET /automation/actions)", step.Action)
		}
	}
	doc, ok := catalogDocs[name]
	if !ok {
		return nil
	}

	for _, f := range doc.fields {
		if f.Required && !stepFieldSet(step, f.Name) {
			return fmt.Errorf("action %q requires %q (%s)", name, f.Name, f.Description)
		}
	}
	for _, group := range doc.anyOf {
		set := false
		for _, name := range group {
			if stepFieldSet(step, name) {
				set = true
				break
			}
		}
		if !set {
			return fmt.Errorf("action %q needs at least one of %s", name, strings.Join(group, " or "))
		}
	}
	for _, f := range doc.fields {
		if len(f.Enum) == 0 || !stepFieldSet(step, f.Name) {
			continue
		}
		val, ok := stepStringField(step, f.Name)
		if !ok {
			continue
		}
		matched := false
		for _, e := range f.Enum {
			if strings.EqualFold(val, e) {
				matched = true
				break
			}
		}
		if !matched {
			return fmt.Errorf("action %q: %q must be one of %s, got %q", name, f.Name, strings.Join(f.Enum, ", "), val)
		}
	}

	if err := ValidateSteps(step.Steps); err != nil {
		return fmt.Errorf("in %s steps: %w", name, err)
	}
	if err := ValidateSteps(step.Else); err != nil {
		return fmt.Errorf("in %s else: %w", name, err)
	}
	return nil
}

// stepFieldSet reports whether a wire field carries information, using the same
// JSON names the request uses (so a typo in the catalog is a validation hole the
// tests catch, not a silent pass).
func stepFieldSet(step Step, wireName string) bool {
	v := stepFieldValue(step, wireName)
	if !v.IsValid() {
		return false
	}
	switch v.Kind() {
	case reflect.Interface, reflect.Ptr:
		return !v.IsNil()
	case reflect.String, reflect.Slice, reflect.Map:
		return v.Len() > 0
	default:
		return !v.IsZero()
	}
}

func stepStringField(step Step, wireName string) (string, bool) {
	v := stepFieldValue(step, wireName)
	if !v.IsValid() || v.Kind() != reflect.String {
		return "", false
	}
	return v.String(), true
}

func stepFieldValue(step Step, wireName string) reflect.Value {
	t := reflect.TypeOf(step)
	val := reflect.ValueOf(step)
	for i := 0; i < t.NumField(); i++ {
		name := strings.Split(t.Field(i).Tag.Get("json"), ",")[0]
		if name == wireName {
			return val.Field(i)
		}
	}
	return reflect.Value{}
}
