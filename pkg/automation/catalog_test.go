package automation

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// The catalog is only worth anything if it cannot drift. These tests are the
// mechanism the plan chose over a go/parser drift test: the catalog is *derived*
// from the dispatch table, so the tests compare structures, not prose.

func TestCatalogMatchesDispatchTables(t *testing.T) {
	want := map[string]bool{}
	for name := range leafActions {
		want[name] = true
	}
	for _, name := range controlFlowActions {
		want[name] = true
	}

	got := map[string]bool{}
	for _, spec := range Catalog() {
		if got[spec.Action] {
			t.Errorf("action %q is listed twice", spec.Action)
		}
		got[spec.Action] = true
	}

	for name := range want {
		if !got[name] {
			t.Errorf("action %q is dispatchable but missing from the catalog", name)
		}
	}
	for name := range got {
		if !want[name] {
			t.Errorf("catalog advertises %q, which the engine cannot run", name)
		}
	}

	// Every dispatchable action must also be *documented*: an entry with the
	// placeholder summary means a key was added without writing its FieldSpecs.
	for _, spec := range Catalog() {
		if spec.Summary == "" || spec.Summary == "undocumented" {
			t.Errorf("action %q has no summary", spec.Action)
		}
	}
}

// TestCatalogFieldsExistOnStep catches the subtler drift: a documented field name
// that the JSON decoder would ignore, i.e. instructions to agents that silently
// do nothing.
func TestCatalogFieldsExistOnStep(t *testing.T) {
	wire := map[string]bool{}
	stepType := reflect.TypeOf(Step{})
	for i := 0; i < stepType.NumField(); i++ {
		name := strings.Split(stepType.Field(i).Tag.Get("json"), ",")[0]
		if name != "" && name != "-" {
			wire[name] = true
		}
	}

	for _, spec := range Catalog() {
		for _, f := range spec.Fields {
			if !wire[f.Name] {
				t.Errorf("action %q documents field %q, which is not a wire name of automation.Step", spec.Action, f.Name)
			}
			if f.Description == "" {
				t.Errorf("action %q field %q has no description", spec.Action, f.Name)
			}
			if f.Type == "" {
				t.Errorf("action %q field %q has no type", spec.Action, f.Name)
			}
		}
	}

	for _, f := range StepSchema() {
		if !wire[f.Name] {
			t.Errorf("step_schema lists %q, which is not a wire name", f.Name)
		}
		if f.Description == "" {
			t.Errorf("step_schema field %q has no description", f.Name)
		}
	}
	if len(StepSchema()) != len(wire) {
		t.Errorf("step_schema has %d fields, Step has %d wire names", len(StepSchema()), len(wire))
	}
}

// TestInputFlagIsExactUnderpins deny_api_input: the set of refused actions must be
// exactly the set that moves the pointer or keyboard, no wider (a too-wide flag
// makes the switch useless) and no narrower (a too-narrow one is the security bug).
func TestInputFlagIsExact(t *testing.T) {
	want := map[string]bool{
		"click": true, "move": true, "hold": true, "release": true, "drag": true,
		"type": true, "key": true, "scroll": true,
	}
	for _, spec := range Catalog() {
		if spec.InjectsInput != want[spec.Action] {
			t.Errorf("action %q injects_input = %v, want %v", spec.Action, spec.InjectsInput, want[spec.Action])
		}
	}
	// The mutating-but-not-input actions are the documented residual risk; if one
	// ever starts injecting input, this test is the reminder to update the docs.
	for _, name := range []string{"run", "file", "clipboard", "window", "screenshot"} {
		spec, ok := LookupAction(name)
		if !ok {
			t.Fatalf("%q missing from the catalog", name)
		}
		if spec.InjectsInput {
			t.Errorf("%q is now marked injects_input; ZEN_CAP_SERVER_API.md's deny_api_input section must say so too", name)
		}
	}
}

func TestValidateSteps(t *testing.T) {
	ok := []Step{
		{Action: "click", X: 10, Y: 20},
		{Action: "click", X: 10, Y: 20, Clicks: 2, NoActivate: true},
		{Action: "scroll", Direction: "down", Amount: 3},
		{Action: "wait_until", WaitFor: "window", Window: &WindowTarget{Title: "x"}},
		{Action: "wait_until", WaitFor: "pixel", X: 1, Y: 2, Color: "#000000"},
		{Action: "window", Mode: "fullscreen"},
		{Action: "type", Text: "hello"},
		{Action: "key", Keys: "ctrl-c"},
		{Action: "screenshot", Output: "x.png"},
		{Action: "loop", Count: 2, Steps: []Step{{Action: "wait", Duration: "1ms"}}},
		{Action: "if_found", Find: "image", Image: "n.png", Steps: []Step{{Action: "click"}}},
		{Action: "if_pixel", X: 1, Y: 2, Color: "#ffffff"},
		{Action: "if_window", Window: &WindowTarget{Class: "firefox"}},
		{Action: "var", Name: "v", Value: 1},
		{Action: "goto", Target: "lbl"},
		{Action: "stop"},
		{Action: "file", Target: "x.txt", Mode: "read"},
		{Action: "ocr", Text: "hi"},
	}
	if err := ValidateSteps(ok); err != nil {
		t.Errorf("valid steps were rejected: %v", err)
	}

	bad := []struct {
		name     string
		steps    []Step
		contains string
	}{
		{"unknown action", []Step{{Action: "nope"}}, "unknown action"},
		{"missing action", []Step{{}}, "missing action"},
		{"wait_until without wait_for", []Step{{Action: "wait_until"}}, "wait_for"},
		{"type without text", []Step{{Action: "type"}}, "text"},
		{"key without keys", []Step{{Action: "key"}}, "keys"},
		{"scroll without direction", []Step{{Action: "scroll"}}, "direction"},
		{"bad enum value", []Step{{Action: "scroll", Direction: "sideways"}}, "must be one of"},
		{"window without mode", []Step{{Action: "window"}}, "mode"},
		{"bad window mode", []Step{{Action: "window", Mode: "teleport"}}, "teleport"},
		{"find_image without image", []Step{{Action: "find_image"}}, "image"},
		{"file without target", []Step{{Action: "file", Mode: "read"}}, "target"},
		{"ocr without text or target", []Step{{Action: "ocr"}}, "at least one of"},
		{"if_pixel without colour", []Step{{Action: "if_pixel", X: 1, Y: 1}}, "color"},
		{"nested bad step", []Step{{Action: "loop", Count: 1, Steps: []Step{{Action: "nope"}}}}, "unknown action"},
		{"else branch bad", []Step{{Action: "if_found", Find: "image", Image: "x", Else: []Step{{Action: "type"}}}}, "text"},
	}
	for _, c := range bad {
		err := ValidateSteps(c.steps)
		if err == nil {
			t.Errorf("%s: accepted, want a rejection", c.name)
			continue
		}
		if !strings.Contains(err.Error(), c.contains) {
			t.Errorf("%s: error %q does not mention %q", c.name, err, c.contains)
		}
		re, isTyped := err.(*RunError)
		if !isTyped || re.Code != CodeBadRequest {
			t.Errorf("%s: want a %q RunError, got %#v", c.name, CodeBadRequest, err)
		}
	}
}

func TestInjectsInputSeesNestedSteps(t *testing.T) {
	if !InjectsInput([]Step{{Action: "log"}, {Action: "loop", Steps: []Step{{Action: "click"}}}}) {
		t.Error("a click inside a loop must count as input")
	}
	if !InjectsInput([]Step{{Action: "if_found", Find: "image", Image: "x", Else: []Step{{Action: "type", Text: "x"}}}}) {
		t.Error("a type in an else branch must count as input")
	}
	if InjectsInput([]Step{{Action: "log"}, {Action: "screenshot"}}) {
		t.Error("a read-only batch must not be treated as input")
	}
	if InjectsInput(nil) {
		t.Error("no steps inject nothing")
	}
}

func TestCatalogJSONIsWellFormed(t *testing.T) {
	raw, err := CatalogJSON()
	if err != nil {
		t.Fatalf("CatalogJSON: %v", err)
	}
	var doc struct {
		Actions    []ActionSpec `json:"actions"`
		StepSchema []FieldSpec  `json:"step_schema"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("the catalog body is not valid JSON: %v", err)
	}
	if len(doc.Actions) != len(leafActions)+len(controlFlowActions) {
		t.Errorf("catalog has %d actions, want %d", len(doc.Actions), len(leafActions)+len(controlFlowActions))
	}
	if len(doc.StepSchema) == 0 {
		t.Error("step_schema is empty; an agent has no way to learn the step shape")
	}

	// The catalog must answer "what does this return?" for the actions whose whole
	// purpose is producing data an agent will use.
	for _, name := range []string{"find_text", "window_state", "screenshot"} {
		spec, ok := LookupAction(name)
		if !ok {
			t.Fatalf("%q missing", name)
		}
		if len(spec.Returns) == 0 {
			t.Errorf("%q publishes no example payload", name)
		}
		var probe map[string]interface{}
		if err := json.Unmarshal(spec.Returns, &probe); err != nil {
			t.Errorf("%q returns is not a JSON object: %v", name, err)
		}
	}

	// Required fields sort first, so a client rendering the catalog sees them.
	spec, _ := LookupAction("wait_until")
	seenOptional := false
	for _, f := range spec.Fields {
		if !f.Required {
			seenOptional = true
			continue
		}
		if seenOptional {
			t.Error("required fields are not listed before optional ones")
		}
	}
}

func TestActionInjectsInputHelper(t *testing.T) {
	if !actionInjectsInput("click") || actionInjectsInput("screenshot") || actionInjectsInput("nope") {
		t.Error("actionInjectsInput disagrees with the catalog")
	}
}
