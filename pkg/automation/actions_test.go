package automation

import (
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"zen-cap/pkg/capture"
	"zen-cap/pkg/config"
)

// mockTarget satisfies target.Target for unit tests.
// Screenshot returns the image set in screenshotImg (defaults to 100x100 black).
type mockTarget struct {
	screenshotImg image.Image
	clicks        []struct {
		x, y int
		btn  string
	}
	types []string
	keys  []string
	moves []struct {
		x, y int
	}
	presses []struct {
		x, y int
		btn  string
	}
	releases []struct {
		x, y int
		btn  string
	}
}

func newMockTarget(img image.Image) *mockTarget {
	if img == nil {
		img = image.NewRGBA(image.Rect(0, 0, 100, 100))
	}
	return &mockTarget{screenshotImg: img}
}

func (m *mockTarget) Screenshot() (image.Image, error) { return m.screenshotImg, nil }
func (m *mockTarget) ScreenSize() (int, int)           { b := m.screenshotImg.Bounds(); return b.Dx(), b.Dy() }
func (m *mockTarget) Click(x, y int, btn string) error {
	m.clicks = append(m.clicks, struct {
		x, y int
		btn  string
	}{x, y, btn})
	return nil
}
func (m *mockTarget) Move(x, y int) error {
	m.moves = append(m.moves, struct {
		x, y int
	}{x, y})
	return nil
}
func (m *mockTarget) ButtonPress(x, y int, btn string) error {
	m.presses = append(m.presses, struct {
		x, y int
		btn  string
	}{x, y, btn})
	return nil
}
func (m *mockTarget) ButtonRelease(x, y int, btn string) error {
	m.releases = append(m.releases, struct {
		x, y int
		btn  string
	}{x, y, btn})
	return nil
}
func (m *mockTarget) Type(text string, delay int64) error {
	m.types = append(m.types, text)
	return nil
}
func (m *mockTarget) Key(keys string) error         { m.keys = append(m.keys, keys); return nil }
func (m *mockTarget) Scroll(x, y, dx, dy int) error { return nil }
func (m *mockTarget) Close() error                  { return nil }

func TestRunLog(t *testing.T) {
	var loggedMessage string
	logger := func(format string, args ...interface{}) {
		loggedMessage = fmt.Sprintf(format, args...)
	}

	ctx := &ExecContext{
		Logger: logger,
	}

	step := Step{
		Action:  "log",
		Message: "Hello, testing log action!",
	}

	err := ExecuteStep(step, ctx)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	expected := "[Automation] Log: Hello, testing log action!"
	if loggedMessage != expected {
		t.Errorf("expected logged message %q, got %q", expected, loggedMessage)
	}

	// Test fallback to Text
	loggedMessage = ""
	stepFallback := Step{
		Action: "log",
		Text:   "Hello, text fallback!",
	}

	err = ExecuteStep(stepFallback, ctx)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	expectedFallback := "[Automation] Log: Hello, text fallback!"
	if loggedMessage != expectedFallback {
		t.Errorf("expected logged message %q, got %q", expectedFallback, loggedMessage)
	}
}

func TestRunOCR(t *testing.T) {
	// Create a mock OCR server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Mock response containing "match-me" with bounds (10, 20) -> (50, 40)
		resp := `{
			"results": [
				{
					"Text": "match-me",
					"Confidence": 0.95,
					"Bounds": {
						"Min": {"X": 10, "Y": 20},
						"Max": {"X": 50, "Y": 40}
					}
				}
			]
		}`
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(resp))
	}))
	defer server.Close()

	// Screenshot mocked via ctx.Target (newMockTarget)
	dummyImg := image.NewRGBA(image.Rect(0, 0, 100, 100))
	draw.Draw(dummyImg, dummyImg.Bounds(), &image.Uniform{color.RGBA{255, 0, 0, 255}}, image.Point{}, draw.Src)

	// Create temp output file path
	tempDir, err := os.MkdirTemp("", "ocr-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)
	outputPath := filepath.Join(tempDir, "cropped_textbox.png")

	// Set up execution context
	var loggedMessage string
	logger := func(format string, args ...interface{}) {
		loggedMessage += fmt.Sprintf(format, args...) + "\n"
	}

	cfg := &config.Config{
		OCRAddress:  server.URL,
		OCRLanguage: "en",
	}

	ctx := &ExecContext{
		Target:    newMockTarget(dummyImg),
		Logger:    logger,
		Config:    cfg,
		ScriptDir: tempDir,
	}

	step := Step{
		Action:  "ocr",
		Text:    "match-me",
		Output:  outputPath,
		Timeout: "100ms",
	}

	err = ExecuteStep(step, ctx)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Verify coordinates were updated
	if ctx.LastFoundX != 30 || ctx.LastFoundY != 30 {
		t.Errorf("expected LastFound coordinates to be (30, 30), got (%d, %d)", ctx.LastFoundX, ctx.LastFoundY)
	}

	// Verify cropped image exists and has correct dimensions
	if _, err := os.Stat(outputPath); os.IsNotExist(err) {
		t.Fatalf("expected output file to exist, but it doesn't")
	}

	outF, err := os.Open(outputPath)
	if err != nil {
		t.Fatalf("failed to open output file: %v", err)
	}
	defer outF.Close()

	savedImg, err := png.Decode(outF)
	if err != nil {
		t.Fatalf("failed to decode saved PNG: %v", err)
	}

	bounds := savedImg.Bounds()
	if bounds.Dx() != 40 || bounds.Dy() != 20 {
		t.Errorf("expected cropped image dimensions 40x20, got %dx%d", bounds.Dx(), bounds.Dy())
	}
}

func TestFindTextWithBounds(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := `{
			"results": [
				{
					"Text": "target-text",
					"Confidence": 0.99,
					"Bounds": {
						"Min": {"X": 5, "Y": 5},
						"Max": {"X": 25, "Y": 15}
					}
				}
			]
		}`
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(resp))
	}))
	defer server.Close()

	img := image.NewRGBA(image.Rect(0, 0, 50, 50))
	x, y, bounds, conf, err := FindTextWithBounds(img, server.URL, "en", "ja", "target-text")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if x != 15 || y != 10 {
		t.Errorf("expected center (15, 10), got (%d, %d)", x, y)
	}

	if bounds.Min.X != 5 || bounds.Min.Y != 5 || bounds.Max.X != 25 || bounds.Max.Y != 15 {
		t.Errorf("bounds mismatch: %+v", bounds)
	}

	if float32(conf) != 0.99 {
		t.Errorf("expected confidence 0.99, got %f", conf)
	}
}

func TestIfFoundOCR(t *testing.T) {
	// Create a mock OCR server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := `{
			"results": [
				{
					"Text": "target-text",
					"Confidence": 0.95,
					"Bounds": {
						"Min": {"X": 20, "Y": 30},
						"Max": {"X": 60, "Y": 50}
					}
				}
			]
		}`
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(resp))
	}))
	defer server.Close()

	// Screenshot mocked via ctx.Target (newMockTarget)
	dummyImg := image.NewRGBA(image.Rect(0, 0, 100, 100))
	draw.Draw(dummyImg, dummyImg.Bounds(), &image.Uniform{color.RGBA{255, 0, 0, 255}}, image.Point{}, draw.Src)

	tempDir, err := os.MkdirTemp("", "iffound-ocr-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)
	outputPath := filepath.Join(tempDir, "if_found_textbox.png")

	var loggedMessage string
	logger := func(format string, args ...interface{}) {
		loggedMessage += fmt.Sprintf(format, args...) + "\n"
	}

	cfg := &config.Config{
		OCRAddress:  server.URL,
		OCRLanguage: "en",
	}

	ctx := &ExecContext{
		Target:    newMockTarget(dummyImg),
		Logger:    logger,
		Config:    cfg,
		ScriptDir: tempDir,
	}

	// Create step: if_found with type: ocr
	substep := Step{
		Action: "log",
		Text:   "Substep Executed!",
	}

	step := Step{
		Action:      "if_found",
		Type:        "ocr",
		Text:        "target-text",
		Output:      outputPath,
		WaitTimeout: "100ms",
		Steps:       []Step{substep},
	}

	err = executeStepWithControl(step, ctx)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Verify LastFound coordinates (center of 20,30 -> 60,50 is 40,40)
	if ctx.LastFoundX != 40 || ctx.LastFoundY != 40 {
		t.Errorf("expected LastFound coordinates (40, 40), got (%d, %d)", ctx.LastFoundX, ctx.LastFoundY)
	}

	// Verify cropped image exists and has correct dimensions (40x20)
	if _, err := os.Stat(outputPath); os.IsNotExist(err) {
		t.Fatalf("expected output file to exist, but it doesn't")
	}

	outF, err := os.Open(outputPath)
	if err != nil {
		t.Fatalf("failed to open output file: %v", err)
	}
	defer outF.Close()

	savedImg, err := png.Decode(outF)
	if err != nil {
		t.Fatalf("failed to decode saved PNG: %v", err)
	}

	bounds := savedImg.Bounds()
	if bounds.Dx() != 40 || bounds.Dy() != 20 {
		t.Errorf("expected cropped image dimensions 40x20, got %dx%d", bounds.Dx(), bounds.Dy())
	}
}

func TestVariablesAndExpressions(t *testing.T) {
	ctx := &ExecContext{
		Logger:    func(string, ...interface{}) {},
		Variables: make(map[string]interface{}),
	}

	// 1. Assign variable
	step1 := Step{
		Action: "var",
		Name:   "counter",
		Value:  10,
	}
	if err := executeStepWithControl(step1, ctx); err != nil {
		t.Fatalf("var set failed: %v", err)
	}
	if ctx.Variables["counter"] != 10 {
		t.Errorf("expected counter=10, got %v", ctx.Variables["counter"])
	}

	// 2. Math assignment
	step2 := Step{
		Action: "var",
		Name:   "counter",
		Value:  "${counter} + 5",
	}
	if err := executeStepWithControl(step2, ctx); err != nil {
		t.Fatalf("var math addition failed: %v", err)
	}
	if ctx.Variables["counter"] != 15.0 { // math returns float64
		t.Errorf("expected counter=15.0, got %v", ctx.Variables["counter"])
	}

	// 3. String interpolation
	ctx.Variables["name"] = "World"
	step3 := Step{
		Action: "var",
		Name:   "greeting",
		Value:  "Hello ${name}",
	}
	if err := executeStepWithControl(step3, ctx); err != nil {
		t.Fatalf("var interpolation failed: %v", err)
	}
	if ctx.Variables["greeting"] != "Hello World" {
		t.Errorf("expected greeting='Hello World', got %q", ctx.Variables["greeting"])
	}

	// 4. Nested dotted path lookup
	ctx.Variables["data"] = map[string]interface{}{
		"user": map[string]interface{}{
			"age": 30.0,
		},
	}
	step4 := Step{
		Action: "var",
		Name:   "age",
		Value:  "${data.user.age}",
	}
	if err := executeStepWithControl(step4, ctx); err != nil {
		t.Fatalf("var nested path failed: %v", err)
	}
	if ctx.Variables["age"] != 30.0 {
		t.Errorf("expected age=30.0, got %v", ctx.Variables["age"])
	}
}

func TestConditionals(t *testing.T) {
	ctx := &ExecContext{
		Logger:    func(string, ...interface{}) {},
		Variables: make(map[string]interface{}),
	}
	ctx.Variables["counter"] = 5.0
	ctx.Variables["status"] = "active"

	// Step that should execute
	var executed1 bool
	step1 := Step{
		Action: "var", // we'll use var to record execution
		Name:   "executed1",
		Value:  true,
		When:   "${counter} < 10",
	}
	if err := executeStepWithControl(step1, ctx); err != nil {
		t.Fatalf("execution failed: %v", err)
	}
	executed1 = ctx.Variables["executed1"] == true
	if !executed1 {
		t.Errorf("expected step1 to execute when condition is met")
	}

	// Step that should be skipped
	ctx.Variables["executed2"] = false
	step2 := Step{
		Action: "var",
		Name:   "executed2",
		Value:  true,
		When:   "${counter} == 10",
	}
	if err := executeStepWithControl(step2, ctx); err != nil {
		t.Fatalf("execution failed: %v", err)
	}
	if ctx.Variables["executed2"] == true {
		t.Errorf("expected step2 to be skipped when condition is not met")
	}

	// String comparison condition
	ctx.Variables["executed3"] = false
	step3 := Step{
		Action: "var",
		Name:   "executed3",
		Value:  true,
		When:   `"${status}" == "active"`,
	}
	if err := executeStepWithControl(step3, ctx); err != nil {
		t.Fatalf("execution failed: %v", err)
	}
	if ctx.Variables["executed3"] != true {
		t.Errorf("expected step3 to execute when string condition is met")
	}
}

func TestJumpsAndGotos(t *testing.T) {
	ctx := &ExecContext{
		Logger:    func(string, ...interface{}) {},
		Variables: make(map[string]interface{}),
	}
	ctx.Variables["counter"] = 0.0

	// We construct a list of steps representing a loop that runs 3 times:
	// 1. var: counter = counter + 1
	// 2. goto: end (when counter == 3)
	// 3. goto: start
	// 4. (label: end) var: done = true
	steps := []Step{
		{
			Label:  "start",
			Action: "var",
			Name:   "counter",
			Value:  "${counter} + 1",
		},
		{
			Action: "goto",
			Target: "end",
			When:   "${counter} == 3",
		},
		{
			Action: "goto",
			Target: "start",
		},
		{
			Label:  "end",
			Action: "var",
			Name:   "done",
			Value:  true,
		},
	}

	err := executeStepList(steps, ctx)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if ctx.Variables["counter"] != 3.0 {
		t.Errorf("expected counter=3, got %v", ctx.Variables["counter"])
	}
	if ctx.Variables["done"] != true {
		t.Errorf("expected done=true, got %v", ctx.Variables["done"])
	}
}

func TestProceduresAndFunctions(t *testing.T) {
	ctx := &ExecContext{
		Logger:    func(string, ...interface{}) {},
		Variables: make(map[string]interface{}),
		Functions: map[string][]Step{
			"double_value": {
				{
					Action: "var",
					Name:   "val",
					Value:  "${val} * 2",
				},
				// Mutates global variable status
				{
					Action: "var",
					Name:   "status",
					Value:  "modified",
				},
			},
		},
	}

	ctx.Variables["val"] = 5.0
	ctx.Variables["status"] = "initial"

	// Call function passing argument val
	step := Step{
		Action: "call",
		Target: "double_value",
		Args: map[string]interface{}{
			"val": "${val}",
		},
	}

	if err := executeStepWithControl(step, ctx); err != nil {
		t.Fatalf("function call failed: %v", err)
	}

	// 1. Verify global variable was mutated
	if ctx.Variables["status"] != "modified" {
		t.Errorf("expected status to be mutated to 'modified', got %v", ctx.Variables["status"])
	}

	// 2. Verify local/shadowed parameter 'val' restored to original caller's value (5.0)
	if ctx.Variables["val"] != 5.0 {
		t.Errorf("expected val to be restored to 5.0, got %v", ctx.Variables["val"])
	}
}

func TestStopAction(t *testing.T) {
	ctx := &ExecContext{
		Logger: func(string, ...interface{}) {},
	}
	step := Step{
		Action:  "stop",
		Message: "target reached",
	}
	err := ExecuteStep(step, ctx)
	if err == nil {
		t.Fatalf("expected StopError, got nil")
	}
	stopErr, ok := err.(StopError)
	if !ok {
		t.Fatalf("expected StopError, got %T: %v", err, err)
	}
	if stopErr.Message != "target reached" {
		t.Errorf("expected message 'target reached', got %q", stopErr.Message)
	}

	script := Script{
		Name: "test stop",
		Steps: []Step{
			step,
		},
	}
	err = executeStepList(script.Steps, ctx)
	if err == nil {
		t.Fatalf("expected executeStepList to propagate StopError")
	}
	if _, ok := err.(StopError); !ok {
		t.Errorf("expected StopError, got %v", err)
	}
}

func TestClipboardReadBack(t *testing.T) {
	oldRead := capture.ReadTextFromClipboard
	oldCopy := capture.CopyTextToClipboard
	defer func() {
		capture.ReadTextFromClipboard = oldRead
		capture.CopyTextToClipboard = oldCopy
	}()

	capture.ReadTextFromClipboard = func() (string, error) {
		return "hello-clipboard", nil
	}
	capture.CopyTextToClipboard = func(text string) error {
		return nil
	}

	ctx := &ExecContext{
		Logger:    func(string, ...interface{}) {},
		Variables: make(map[string]interface{}),
	}

	stepRead := Step{
		Action: "clipboard",
		Mode:   "read",
		Name:   "clip_val",
	}
	err := ExecuteStep(stepRead, ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ctx.Variables["clip_val"] != "hello-clipboard" {
		t.Errorf("expected variables['clip_val'] to be 'hello-clipboard', got %v", ctx.Variables["clip_val"])
	}
}

func TestFileOperations(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "file-action-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	filePath := filepath.Join(tempDir, "test.txt")
	ctx := &ExecContext{
		Logger:    func(string, ...interface{}) {},
		ScriptDir: tempDir,
		Variables: make(map[string]interface{}),
	}

	// 1. Write
	stepWrite := Step{
		Action: "file",
		Mode:   "write",
		Target: filePath,
		Text:   "line 1\n",
	}
	if err := ExecuteStep(stepWrite, ctx); err != nil {
		t.Fatalf("write failed: %v", err)
	}

	data, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("failed to read file: %v", err)
	}
	if string(data) != "line 1\n" {
		t.Errorf("expected 'line 1\n', got %q", string(data))
	}

	// 2. Append
	stepAppend := Step{
		Action: "file",
		Mode:   "append",
		Target: filePath,
		Text:   "line 2\n",
	}
	if err := ExecuteStep(stepAppend, ctx); err != nil {
		t.Fatalf("append failed: %v", err)
	}

	data, err = os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("failed to read file: %v", err)
	}
	if string(data) != "line 1\nline 2\n" {
		t.Errorf("expected 'line 1\nline 2\n', got %q", string(data))
	}

	// 3. Read
	stepRead := Step{
		Action: "file",
		Mode:   "read",
		Target: filePath,
		Name:   "file_data",
	}
	if err := ExecuteStep(stepRead, ctx); err != nil {
		t.Fatalf("read failed: %v", err)
	}
	if ctx.Variables["file_data"] != "line 1\nline 2\n" {
		t.Errorf("expected variable to be 'line 1\nline 2\n', got %q", ctx.Variables["file_data"])
	}
}

func TestIfPixel(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 10, 10))
	img.Set(5, 5, color.RGBA{255, 0, 0, 255})

	var executed bool
	ctx := &ExecContext{
		Target:    newMockTarget(img),
		Logger:    func(string, ...interface{}) {},
		Variables: make(map[string]interface{}),
	}

	stepMatch := Step{
		Action:    "if_pixel",
		X:         5,
		Y:         5,
		Color:     "#FF0000",
		Tolerance: 5,
		Steps: []Step{
			{
				Action: "var",
				Name:   "executed",
				Value:  true,
			},
		},
	}
	if err := executeStepWithControl(stepMatch, ctx); err != nil {
		t.Fatalf("if_pixel failed: %v", err)
	}
	executed = ctx.Variables["executed"] == true
	if !executed {
		t.Errorf("expected steps to execute because color matches")
	}

	ctx.Variables["executed"] = false
	stepMismatch := Step{
		Action:    "if_pixel",
		X:         5,
		Y:         5,
		Color:     "#00FF00",
		Tolerance: 5,
		Steps: []Step{
			{
				Action: "var",
				Name:   "executed",
				Value:  true,
			},
		},
		Else: []Step{
			{
				Action: "var",
				Name:   "else_executed",
				Value:  true,
			},
		},
	}
	if err := executeStepWithControl(stepMismatch, ctx); err != nil {
		t.Fatalf("if_pixel failed: %v", err)
	}
	if ctx.Variables["executed"] == true {
		t.Errorf("expected steps not to execute because color mismatches")
	}
	if ctx.Variables["else_executed"] != true {
		t.Errorf("expected else steps to execute")
	}
}

func TestWindowActionErrors(t *testing.T) {
	ctx := &ExecContext{
		Logger: func(string, ...interface{}) {},
	}
	step := Step{
		Action: "window",
		Mode:   "activate",
	}
	err := ExecuteStep(step, ctx)
	if err == nil {
		t.Fatalf("expected error due to missing/zero WindowID, got nil")
	}
}

func TestIfWindow(t *testing.T) {
	// With a non-X11 target, x11ctx() returns nil and exists is always false.
	// "absent" mode should fire its steps, "present" mode fires else steps.
	ctx := &ExecContext{
		Target:    newMockTarget(nil),
		Logger:    func(string, ...interface{}) {},
		Variables: make(map[string]interface{}),
	}

	// No window present (non-x11 target → always absent)
	stepNoWait := Step{
		Action: "if_window",
		Window: &WindowTarget{Title: "Firefox"},
		Steps: []Step{
			{Action: "var", Name: "res", Value: "found"},
		},
		Else: []Step{
			{Action: "var", Name: "res", Value: "else"},
		},
	}
	if err := executeStepWithControl(stepNoWait, ctx); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ctx.Variables["res"] != "else" {
		t.Errorf("expected res to be 'else' (non-x11 target has no windows), got %v", ctx.Variables["res"])
	}

	// Absent mode: non-x11 target → always absent → steps fire
	stepAbsent := Step{
		Action: "if_window",
		Mode:   "absent",
		Window: &WindowTarget{Title: "Firefox"},
		Steps: []Step{
			{Action: "var", Name: "res", Value: "absent_met"},
		},
		Else: []Step{
			{Action: "var", Name: "res", Value: "absent_unmet"},
		},
	}
	if err := executeStepWithControl(stepAbsent, ctx); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ctx.Variables["res"] != "absent_met" {
		t.Errorf("expected res to be 'absent_met', got %v", ctx.Variables["res"])
	}
}

func TestWindowActionsMocked(t *testing.T) {
	ctx := &ExecContext{
		Target: newMockTarget(nil),
		Logger: func(string, ...interface{}) {},
	}
	step := Step{
		Action: "window",
		Mode:   "fullscreen",
	}
	err := ExecuteStep(step, ctx)
	if err == nil {
		t.Fatalf("expected error due to non-x11 target, got nil")
	}
	if !strings.Contains(err.Error(), "x11 or vfb target") {
		t.Errorf("expected error about x11 target, got %q", err.Error())
	}
}

// failMoveTarget errors on Move so we can assert the stuck-press protection.
type failMoveTarget struct {
	*mockTarget
}

func (m *failMoveTarget) Move(x, y int) error { return errors.New("move failed") }

func TestHoldMoveRelease(t *testing.T) {
	mt := newMockTarget(nil)
	ctx := &ExecContext{
		Target:    mt,
		Logger:    func(string, ...interface{}) {},
		Variables: make(map[string]interface{}),
	}

	steps := []Step{
		{Action: "hold", X: 10, Y: 20, Button: "left"},
		{Action: "move", X: 50, Y: 60},
		{Action: "release", X: 50, Y: 60, Button: "left"},
	}
	for _, s := range steps {
		if err := ExecuteStep(s, ctx); err != nil {
			t.Fatalf("step %q failed: %v", s.Action, err)
		}
	}

	if len(mt.presses) != 1 || mt.presses[0].x != 10 || mt.presses[0].y != 20 || mt.presses[0].btn != "left" {
		t.Errorf("expected one press at (10, 20) left, got %+v", mt.presses)
	}
	if len(mt.moves) != 1 || mt.moves[0].x != 50 || mt.moves[0].y != 60 {
		t.Errorf("expected one move to (50, 60), got %+v", mt.moves)
	}
	if len(mt.releases) != 1 || mt.releases[0].x != 50 || mt.releases[0].y != 60 || mt.releases[0].btn != "left" {
		t.Errorf("expected one release at (50, 60) left, got %+v", mt.releases)
	}
	if len(mt.clicks) != 0 {
		t.Errorf("expected no atomic clicks for a hold/move/release sequence, got %+v", mt.clicks)
	}
}

func TestDragGesture(t *testing.T) {
	mt := newMockTarget(nil)
	ctx := &ExecContext{
		Target:    mt,
		Logger:    func(string, ...interface{}) {},
		Variables: make(map[string]interface{}),
	}

	step := Step{
		Action:   "drag",
		X:        0,
		Y:        0,
		X2:       100,
		Y2:       0,
		Button:   "left",
		Duration: "160ms",
	}
	if err := ExecuteStep(step, ctx); err != nil {
		t.Fatalf("drag failed: %v", err)
	}

	if len(mt.presses) != 1 || mt.presses[0].x != 0 || mt.presses[0].y != 0 || mt.presses[0].btn != "left" {
		t.Errorf("expected press at (0, 0) left, got %+v", mt.presses)
	}
	if len(mt.releases) != 1 || mt.releases[0].x != 100 || mt.releases[0].y != 0 || mt.releases[0].btn != "left" {
		t.Errorf("expected release at (100, 0) left, got %+v", mt.releases)
	}

	// 160ms at 16ms/step = 10 interpolated moves: (10,0)...(100,0).
	if len(mt.moves) != 10 {
		t.Fatalf("expected 10 interpolated moves, got %d: %+v", len(mt.moves), mt.moves)
	}
	if mt.moves[0].x != 10 || mt.moves[0].y != 0 {
		t.Errorf("expected first move at (10, 0), got %+v", mt.moves[0])
	}
	if mt.moves[len(mt.moves)-1].x != 100 || mt.moves[len(mt.moves)-1].y != 0 {
		t.Errorf("expected last move at (100, 0), got %+v", mt.moves[len(mt.moves)-1])
	}
	for i, mv := range mt.moves {
		if mv.x != (i+1)*10 || mv.y != 0 {
			t.Errorf("move %d: expected x=%d y=0, got %+v", i, (i+1)*10, mv)
		}
	}
}

func TestDragLongPress(t *testing.T) {
	mt := newMockTarget(nil)
	ctx := &ExecContext{
		Target:    mt,
		Logger:    func(string, ...interface{}) {},
		Variables: make(map[string]interface{}),
	}

	// Same start and end coordinates degrade to a long press: no motion.
	step := Step{
		Action:   "drag",
		X:        5,
		Y:        5,
		X2:       5,
		Y2:       5,
		Button:   "left",
		Duration: "5ms",
	}
	if err := ExecuteStep(step, ctx); err != nil {
		t.Fatalf("long press drag failed: %v", err)
	}

	if len(mt.presses) != 1 || mt.presses[0].x != 5 || mt.presses[0].y != 5 {
		t.Errorf("expected press at (5, 5), got %+v", mt.presses)
	}
	if len(mt.releases) != 1 || mt.releases[0].x != 5 || mt.releases[0].y != 5 {
		t.Errorf("expected release at (5, 5), got %+v", mt.releases)
	}
	if len(mt.moves) != 0 {
		t.Errorf("expected no moves for a long press, got %+v", mt.moves)
	}
}

func TestDragDefaultsToLastFound(t *testing.T) {
	mt := newMockTarget(nil)
	ctx := &ExecContext{
		Target:     mt,
		Logger:     func(string, ...interface{}) {},
		Variables:  make(map[string]interface{}),
		LastFoundX: 20,
		LastFoundY: 30,
	}

	// No coordinates: both endpoints resolve to LastFound -> long press.
	step := Step{Action: "drag", Button: "left", Duration: "1ms"}
	if err := ExecuteStep(step, ctx); err != nil {
		t.Fatalf("drag failed: %v", err)
	}

	if len(mt.presses) != 1 || mt.presses[0].x != 20 || mt.presses[0].y != 30 {
		t.Errorf("expected press at LastFound (20, 30), got %+v", mt.presses)
	}
	if len(mt.releases) != 1 || mt.releases[0].x != 20 || mt.releases[0].y != 30 {
		t.Errorf("expected release at LastFound (20, 30), got %+v", mt.releases)
	}
}

func TestReleaseWithoutHold(t *testing.T) {
	mt := newMockTarget(nil)
	ctx := &ExecContext{
		Target:    mt,
		Logger:    func(string, ...interface{}) {},
		Variables: make(map[string]interface{}),
	}

	// Releasing an unpressed button is tolerated, not an error.
	step := Step{Action: "release", X: 7, Y: 8, Button: "left"}
	if err := ExecuteStep(step, ctx); err != nil {
		t.Fatalf("release without hold should be tolerated, got %v", err)
	}
	if len(mt.releases) != 1 || mt.releases[0].x != 7 || mt.releases[0].y != 8 {
		t.Errorf("expected release at (7, 8), got %+v", mt.releases)
	}
}

func TestDragReleasesOnMoveError(t *testing.T) {
	failing := &failMoveTarget{newMockTarget(nil)}
	ctx := &ExecContext{
		Target:    failing,
		Logger:    func(string, ...interface{}) {},
		Variables: make(map[string]interface{}),
	}

	step := Step{
		Action:   "drag",
		X:        0,
		Y:        0,
		X2:       100,
		Y2:       0,
		Duration: "160ms",
	}
	err := ExecuteStep(step, ctx)
	if err == nil {
		t.Fatalf("expected drag to propagate Move error")
	}

	// The button must be released so the pointer does not stay stuck down.
	if len(failing.presses) != 1 || len(failing.releases) != 1 {
		t.Fatalf("expected 1 press and 1 cleanup release, got presses=%+v releases=%+v", failing.presses, failing.releases)
	}
	if failing.releases[0].x != 10 || failing.releases[0].y != 0 {
		t.Errorf("expected cleanup release at first failed move (10, 0), got %+v", failing.releases[0])
	}
}

// --- F2 regression: a capture failure must not outlive the step's timeout ---

// writeTemplatePNG writes a small PNG usable as a find_image template.
func writeTemplatePNG(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "tpl.png")
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	draw.Draw(img, img.Bounds(), &image.Uniform{color.RGBA{0, 255, 0, 255}}, image.Point{}, draw.Src)
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create template: %v", err)
	}
	if err := png.Encode(f, img); err != nil {
		t.Fatalf("encode template: %v", err)
	}
	f.Close()
	return path
}

func captureFailCtx() *ExecContext {
	return &ExecContext{
		Target:    newCaptureFailTarget(),
		Logger:    func(string, ...interface{}) {},
		Variables: make(map[string]interface{}),
	}
}

func TestFindImageCaptureErrorTimesOut(t *testing.T) {
	ctx := captureFailCtx()
	step := Step{Action: "find_image", Image: writeTemplatePNG(t), Timeout: "300ms"}

	start := time.Now()
	err := ExecuteStep(step, ctx)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected an error when every capture fails, got nil")
	}
	if !strings.Contains(err.Error(), "not found within timeout") {
		t.Errorf("expected a timeout error, got %q", err)
	}
	if !strings.Contains(err.Error(), "capture device gone") {
		t.Errorf("expected the capture error to be reported, got %q", err)
	}
	if elapsed >= 1500*time.Millisecond {
		t.Errorf("poll loop outlived its 300ms timeout: %v (F2 infinite recapture regression)", elapsed)
	}
}

func TestFindTextCaptureErrorTimesOut(t *testing.T) {
	ctx := captureFailCtx()
	ctx.Config = &config.Config{OCRAddress: "http://127.0.0.1:1", OCRLanguage: "en"}
	step := Step{Action: "find_text", Text: "hello", Timeout: "300ms"}

	start := time.Now()
	err := ExecuteStep(step, ctx)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected an error when every capture fails, got nil")
	}
	if !strings.Contains(err.Error(), "not found within timeout") {
		t.Errorf("expected a timeout error, got %q", err)
	}
	if elapsed >= 1500*time.Millisecond {
		t.Errorf("poll loop outlived its 300ms timeout: %v", elapsed)
	}
}

func TestOCRCaptureErrorTimesOut(t *testing.T) {
	ctx := captureFailCtx()
	ctx.Config = &config.Config{OCRAddress: "http://127.0.0.1:1", OCRLanguage: "en"}
	step := Step{Action: "ocr", Text: "hello", Timeout: "300ms"}

	start := time.Now()
	err := ExecuteStep(step, ctx)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected an error when every capture fails, got nil")
	}
	if !strings.Contains(err.Error(), "not found via OCR within timeout") {
		t.Errorf("expected an OCR timeout error, got %q", err)
	}
	if elapsed >= 1500*time.Millisecond {
		t.Errorf("poll loop outlived its 300ms timeout: %v", elapsed)
	}
}

// TestFindImageAbortsDuringSleep proves an abort unblocks a poll immediately
// instead of after the remaining interval.
func TestFindImageAbortsDuringSleep(t *testing.T) {
	ctx := &ExecContext{
		Target:    newMockTarget(nil),
		Logger:    func(string, ...interface{}) {},
		Variables: make(map[string]interface{}),
		AbortChan: make(chan struct{}),
	}
	go func() {
		time.Sleep(50 * time.Millisecond)
		close(ctx.AbortChan)
	}()

	step := Step{Action: "find_image", Image: writeTemplatePNG(t), Timeout: "30s"}
	start := time.Now()
	err := ExecuteStep(step, ctx)
	if err == nil {
		t.Fatal("expected abort error")
	}
	if !strings.Contains(err.Error(), "aborted") {
		t.Errorf("expected abort error, got %q", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("abort did not interrupt a 30s poll quickly: %v", elapsed)
	}
}

// --- if_found must stay bounded and keep its hard errors (step 3) ---

func TestIfFoundCaptureErrorsTakeElseBranch(t *testing.T) {
	ctx := captureFailCtx()
	ctx.AbortChan = make(chan struct{})
	step := Step{
		Action:      "if_found",
		Type:        "image",
		Image:       writeTemplatePNG(t),
		WaitTimeout: "300ms",
		Steps:       []Step{{Action: "var", Name: "res", Value: "found"}},
		Else:        []Step{{Action: "var", Name: "res", Value: "else"}},
	}
	start := time.Now()
	if err := executeStepWithControl(step, ctx); err != nil {
		t.Fatalf("if_found must not error on a miss, got %v", err)
	}
	if elapsed := time.Since(start); elapsed >= 1500*time.Millisecond {
		t.Errorf("if_found outlived its 300ms wait_timeout: %v", elapsed)
	}
	if ctx.Variables["res"] != "else" {
		t.Errorf("expected else branch, got %v", ctx.Variables["res"])
	}
}

func TestIfFoundBrokenTemplateStillErrors(t *testing.T) {
	ctx := captureFailCtx()
	ctx.AbortChan = make(chan struct{})
	step := Step{
		Action:      "if_found",
		Type:        "image",
		Image:       "/definitely/not/here.png",
		WaitTimeout: "3s",
		Steps:       []Step{{Action: "var", Name: "res", Value: "found"}},
	}
	err := executeStepWithControl(step, ctx)
	if err == nil || !strings.Contains(err.Error(), "failed to open template image") {
		t.Fatalf("expected template open error to propagate, got %v", err)
	}
}

// --- stuck-press escalation to target.PanicReleaser (F6, step 7) ---

// stuckReleaseTarget fails ButtonRelease the way a half-dead X connection does,
// and records whether the caller fell back to a panic release.
type stuckReleaseTarget struct {
	*mockTarget
	releaseErr error
	panicCalls int
	panicErr   error
}

func (m *stuckReleaseTarget) ButtonRelease(x, y int, btn string) error { return m.releaseErr }

func (m *stuckReleaseTarget) ReleaseAllButtons() error {
	m.panicCalls++
	return m.panicErr
}

func TestDragEscalatesToPanicRelease(t *testing.T) {
	sentinel := errors.New("x11: fake button release: BadWindow")
	stuck := &stuckReleaseTarget{mockTarget: newMockTarget(nil), releaseErr: sentinel}
	ctx := &ExecContext{
		Target:    stuck,
		Logger:    func(string, ...interface{}) {},
		Variables: make(map[string]interface{}),
	}

	step := Step{Action: "drag", X: 10, Y: 10, X2: 40, Y2: 40, Duration: "32ms"}
	err := ExecuteStep(step, ctx)
	if err == nil || !errors.Is(err, sentinel) {
		t.Fatalf("expected the release error to surface, got %v", err)
	}
	if stuck.panicCalls == 0 {
		t.Fatal("a failed release on a PanicReleaser backend must escalate to ReleaseAllButtons")
	}

	// A backend without the capability is left alone.
	plain := &noPanicTarget{mockTarget: newMockTarget(nil)}
	ctx.Target = plain
	if err := ExecuteStep(step, ctx); err == nil {
		t.Fatal("expected release error")
	}
	if len(plain.releases) == 0 {
		t.Error("plain release should still have been attempted")
	}
}

// noPanicTarget deliberately does not implement PanicReleaser.
type noPanicTarget struct{ *mockTarget }

func (m *noPanicTarget) ButtonRelease(x, y int, btn string) error {
	m.releases = append(m.releases, struct {
		x, y int
		btn  string
	}{x, y, btn})
	return errors.New("release failed")
}
