package automation

import (
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"zen-cap/pkg/config"
	"zen-cap/pkg/target"
)

// Unit tests for the actions added by the /automation API plan (steps A5/A6).
// None of them needs an X server: everything runs against mockTarget, and the
// X-dependent side (window conditions) is asserted to fail fast instead.

func newActionCtx(t *testing.T, tgt target.Target, outDir string) *ExecContext {
	t.Helper()
	return &ExecContext{
		Target:    tgt,
		AbortChan: make(chan struct{}),
		Logger:    func(string, ...interface{}) {},
		Config:    &config.Config{OutputDir: outDir, OCRAddress: "http://localhost:8765", OCRLanguage: "en"},
		ScriptDir: outDir,
		Variables: map[string]interface{}{},
	}
}

// --- scroll ---------------------------------------------------------------

func TestScrollDirections(t *testing.T) {
	// The sign convention belongs to pkg/target: vertical uses buttons 4 (up) and
	// 5 (down), horizontal 6 (left) / 7 (right), and the loop count is |delta|.
	// pkg/automation must not re-derive it, so the test pins the mapping once.
	cases := []struct {
		direction string
		dx, dy    int
	}{
		{"down", 0, 3},
		{"up", 0, -3},
		{"right", 3, 0},
		{"left", -3, 0},
	}
	for _, c := range cases {
		mt := newMockTarget(nil)
		ctx := newActionCtx(t, mt, t.TempDir())
		step := Step{Action: "scroll", Direction: c.direction, Amount: 3, X: 10, Y: 20}
		if err := ExecuteStep(step, ctx); err != nil {
			t.Fatalf("scroll %s: %v", c.direction, err)
		}
		if len(mt.scrolls) != 1 {
			t.Fatalf("scroll %s: want 1 Scroll call, got %d", c.direction, len(mt.scrolls))
		}
		got := mt.scrolls[0]
		if got.dx != c.dx || got.dy != c.dy {
			t.Errorf("scroll %s: want (dx,dy)=(%d,%d), got (%d,%d)", c.direction, c.dx, c.dy, got.dx, got.dy)
		}
		if got.x != 10 || got.y != 20 {
			t.Errorf("scroll %s: want the pointer at (10,20), got (%d,%d)", c.direction, got.x, got.y)
		}
	}
}

func TestScrollAmountDefaultsAndClamps(t *testing.T) {
	mt := newMockTarget(nil)
	ctx := newActionCtx(t, mt, t.TempDir())
	if err := ExecuteStep(Step{Action: "scroll", Direction: "down", X: 1, Y: 1}, ctx); err != nil {
		t.Fatalf("default amount: %v", err)
	}
	if mt.scrolls[0].dy != 3 {
		t.Errorf("default amount: want 3 ticks, got %d", mt.scrolls[0].dy)
	}
	if err := ExecuteStep(Step{Action: "scroll", Direction: "down", Amount: 5000, X: 1, Y: 1}, ctx); err != nil {
		t.Fatalf("clamped amount: %v", err)
	}
	if mt.scrolls[1].dy != maxScrollTicks {
		t.Errorf("clamp: want %d ticks, got %d", maxScrollTicks, mt.scrolls[1].dy)
	}
	if err := ExecuteStep(Step{Action: "scroll", Direction: "sideways", X: 1, Y: 1}, ctx); err == nil {
		t.Error("unknown direction must be an error, not a silent no-op")
	}
	if err := ExecuteStep(Step{Action: "scroll", Direction: "down", Amount: -2, X: 1, Y: 1}, ctx); err == nil {
		t.Error("a negative amount must be an error")
	}
}

func TestScrollFallsBackToLastFoundThenCentre(t *testing.T) {
	mt := newMockTarget(image.NewRGBA(image.Rect(0, 0, 400, 200)))
	ctx := newActionCtx(t, mt, t.TempDir())

	// Nothing matched yet: aim at the centre of the captured area.
	if err := ExecuteStep(Step{Action: "scroll", Direction: "down"}, ctx); err != nil {
		t.Fatalf("centre fallback: %v", err)
	}
	if got := mt.scrolls[0]; got.x != 200 || got.y != 100 {
		t.Errorf("centre fallback: want (200,100), got (%d,%d)", got.x, got.y)
	}

	// A recorded match wins over the centre.
	ctx.setFound(7, 9)
	if err := ExecuteStep(Step{Action: "scroll", Direction: "down"}, ctx); err != nil {
		t.Fatalf("last-found fallback: %v", err)
	}
	if got := mt.scrolls[1]; got.x != 7 || got.y != 9 {
		t.Errorf("last-found fallback: want (7,9), got (%d,%d)", got.x, got.y)
	}
}

// --- click repeat ---------------------------------------------------------

func TestClickTwiceSendsTwoPresses(t *testing.T) {
	mt := newMockTarget(nil)
	ctx := newActionCtx(t, mt, t.TempDir())
	if err := ExecuteStep(Step{Action: "click", X: 42, Y: 43, Clicks: 2, ClickGap: "1ms"}, ctx); err != nil {
		t.Fatalf("double click: %v", err)
	}
	if len(mt.clicks) != 2 {
		t.Fatalf("want 2 clicks, got %d", len(mt.clicks))
	}
	for i, c := range mt.clicks {
		if c.x != 42 || c.y != 43 {
			t.Errorf("click %d at (%d,%d), want (42,43) — both presses must land on one warp", i, c.x, c.y)
		}
	}
}

func TestClickRejectsBadClickGap(t *testing.T) {
	mt := newMockTarget(nil)
	ctx := newActionCtx(t, mt, t.TempDir())
	err := ExecuteStep(Step{Action: "click", X: 1, Y: 1, Clicks: 2, ClickGap: "soon"}, ctx)
	if err == nil || !strings.Contains(err.Error(), "click_gap") {
		t.Fatalf("want a click_gap error, got %v", err)
	}
	if len(mt.clicks) != 0 {
		t.Errorf("a malformed gap must be refused before any input, got %d clicks", len(mt.clicks))
	}
}

// --- wait_until -----------------------------------------------------------

func TestWaitUntilUnknownCondition(t *testing.T) {
	mt := newMockTarget(nil)
	ctx := newActionCtx(t, mt, t.TempDir())
	if err := ExecuteStep(Step{Action: "wait_until"}, ctx); err == nil {
		t.Error("wait_until without wait_for must fail")
	}
	err := ExecuteStep(Step{Action: "wait_until", WaitFor: "vibes", Timeout: "50ms"}, ctx)
	if err == nil || !strings.Contains(err.Error(), "unknown wait_for") {
		t.Errorf("want an unknown-condition error, got %v", err)
	}
}

// TestWaitUntilWindowNeedsX11 pins the fast failure: a window condition on a
// backend that cannot answer one must not spin until the deadline.
func TestWaitUntilWindowNeedsX11(t *testing.T) {
	mt := newMockTarget(nil)
	ctx := newActionCtx(t, mt, t.TempDir())
	start := time.Now()
	err := ExecuteStep(Step{Action: "wait_until", WaitFor: "window", Window: &WindowTarget{Title: "x"}, Timeout: "30s"}, ctx)
	if err == nil {
		t.Fatal("want an error on a non-x11 target")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("took %v; a non-x11 target must fail immediately, not wait out the timeout", elapsed)
	}
}

// TestWaitUntilImageIsBoundedOnCaptureError is the HTTP-facing half of Phase 0
// F2: a dead window must produce an error inside the deadline, never an infinite
// recapture loop.
func TestWaitUntilImageIsBoundedOnCaptureError(t *testing.T) {
	dir := t.TempDir()
	writeTestTemplate(t, filepath.Join(dir, "needle.png"))

	mt := newMockTarget(nil)
	mt.screenshotErr = errors.New("cannot open capture device")
	ctx := newActionCtx(t, mt, dir)

	start := time.Now()
	err := ExecuteStep(Step{Action: "wait_until", WaitFor: "image", Image: "needle.png", Timeout: "300ms"}, ctx)
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("wait_until spun for %v on a failing capture", elapsed)
	}
	if err == nil {
		t.Fatal("a timed out wait_until must be an error")
	}
	if !strings.Contains(err.Error(), "capture") {
		t.Errorf("the error should say capture was failing, got %q", err)
	}
}

func TestWaitUntilPixelTimeoutReportsWhatItSaw(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 10, 10))
	img.Set(1, 1, color.RGBA{R: 200, A: 255})

	mt := newMockTarget(img)
	ctx := newActionCtx(t, mt, t.TempDir())
	err := ExecuteStep(Step{Action: "wait_until", WaitFor: "pixel", X: 1, Y: 1, Color: "#00ff00", Tolerance: 5, Timeout: "120ms"}, ctx)
	if err == nil {
		t.Fatal("want a timeout error")
	}
	for _, want := range []string{"#c80000", "#00ff00", "120ms"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("timeout error should mention %s, got %q", want, err)
		}
	}
}

func TestWaitUntilPixelMatches(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 10, 10))
	img.Set(2, 3, color.RGBA{G: 255, A: 255})

	mt := newMockTarget(img)
	ctx := newActionCtx(t, mt, t.TempDir())
	if err := ExecuteStep(Step{Action: "wait_until", WaitFor: "pixel", X: 2, Y: 3, Color: "#00ff00", Timeout: "1s"}, ctx); err != nil {
		t.Fatalf("expected an immediate match, got %v", err)
	}
}

// --- type chunking --------------------------------------------------------

func TestTypeChunksAbort(t *testing.T) {
	mt := newMockTarget(nil)
	ctx := newActionCtx(t, mt, t.TempDir())

	// 10 runes in chunks of 4 → 3 pieces. Cancel right after the first piece is
	// injected: pieces 2 and 3 must never reach the target.
	mt.afterType = func(calls int) {
		if calls == 1 {
			close(ctx.AbortChan)
		}
	}

	long := strings.Repeat("x", 10)
	err := ExecuteStep(Step{Action: "type", Text: long, ChunkRunes: 4}, ctx)
	if err == nil {
		t.Fatal("an aborted type must report an error")
	}
	if len(mt.types) == 3 {
		t.Errorf("all 3 chunks were injected after the abort; chunking is not honouring AbortChan")
	}
	if len(mt.types) == 0 {
		t.Error("the first chunk should have gone through before the abort")
	}
	for _, piece := range mt.types {
		if len([]rune(piece)) > 4 {
			t.Errorf("chunk of %d runes exceeds chunk_runes=4", len([]rune(piece)))
		}
	}
}

func TestTypeWithoutAbortSendsEveryChunk(t *testing.T) {
	mt := newMockTarget(nil)
	ctx := newActionCtx(t, mt, t.TempDir())
	long := strings.Repeat("y", 10)
	if err := ExecuteStep(Step{Action: "type", Text: long, ChunkRunes: 4}, ctx); err != nil {
		t.Fatalf("type: %v", err)
	}
	if strings.Join(mt.types, "") != long {
		t.Errorf("chunks %q do not reassemble to the input", mt.types)
	}
}

// --- screenshot action ----------------------------------------------------

func TestRunScreenshotWritesFile(t *testing.T) {
	dir := t.TempDir()
	mt := newMockTarget(gradientImage(60, 40))
	ctx := newActionCtx(t, mt, dir)

	var res struct {
		Path   string `json:"path"`
		Width  int    `json:"width"`
		Height int    `json:"height"`
		Bytes  int    `json:"bytes"`
	}
	if err := ExecuteStep(Step{Action: "screenshot", Output: "sub/cap.png"}, ctx); err != nil {
		t.Fatalf("screenshot: %v", err)
	}
	if err := json.Unmarshal(ctx.LastResult, &res); err != nil {
		t.Fatalf("screenshot did not publish a JSON payload: %s", ctx.LastResult)
	}
	if res.Width != 60 || res.Height != 40 {
		t.Errorf("payload says %dx%d, want 60x40", res.Width, res.Height)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "sub", "cap.png"))
	if err != nil {
		t.Fatalf("file not written where reported (%s): %v", res.Path, err)
	}
	if len(raw) != res.Bytes {
		t.Errorf("payload bytes=%d, file is %d", res.Bytes, len(raw))
	}
}

func TestRunScreenshotReportsCaptureError(t *testing.T) {
	mt := newMockTarget(nil)
	mt.screenshotErr = errors.New("gone")
	ctx := newActionCtx(t, mt, t.TempDir())
	if err := ExecuteStep(Step{Action: "screenshot"}, ctx); err == nil {
		t.Error("a failed capture must fail the step")
	}
}

// --- window_state ---------------------------------------------------------

func TestWindowStateNeedsAWindow(t *testing.T) {
	mt := newMockTarget(nil)
	ctx := newActionCtx(t, mt, t.TempDir())
	err := ExecuteStep(Step{Action: "window_state"}, ctx)
	if err == nil || !strings.Contains(err.Error(), "x11") {
		t.Errorf("window_state on a non-x11 target should say so, got %v", err)
	}
}

// TestActivationPolicyIsCallerScoped is the invariant the plan states as "existing
// focusless YAML scripts keep their behaviour": activation before a click is a
// property of the *caller*, never of an absent step field, so a script written
// before this existed cannot start stealing focus.
func TestActivationPolicyIsCallerScoped(t *testing.T) {
	yamlRun := &ExecContext{}                     // RunScript leaves ActivateClicks false
	httpRun := &ExecContext{activateClicks: true} // handleExec sets it

	if shouldActivateBeforeClick(yamlRun, Step{Action: "click"}) {
		t.Error("a YAML run would now activate: the default of an absent no_activate must not change focus behaviour")
	}
	if !shouldActivateBeforeClick(httpRun, Step{Action: "click"}) {
		t.Error("an HTTP run must activate by default, or click-then-type cannot work")
	}
	if shouldActivateBeforeClick(httpRun, Step{Action: "click", NoActivate: true}) {
		t.Error("no_activate: true must always win")
	}
}

// TestNoActivateFlagRoundTrips checks the wire field itself.
func TestNoActivateFlagRoundTrips(t *testing.T) {
	var s Step
	if err := json.Unmarshal([]byte(`{"action":"click","no_activate":true}`), &s); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !s.NoActivate {
		t.Error("no_activate did not decode")
	}
}

// --- logging --------------------------------------------------------------

// TestLogfNilSafe is A4(c): a direct caller that does not care what the script
// printed must not be the reason a click panics.
func TestLogfNilSafe(t *testing.T) {
	mt := newMockTarget(nil)
	ctx := &ExecContext{
		Target:    mt,
		AbortChan: make(chan struct{}),
		Variables: map[string]interface{}{},
		// Logger deliberately nil.
	}
	if err := ExecuteStep(Step{Action: "click", X: 3, Y: 4}, ctx); err != nil {
		t.Fatalf("click with no logger: %v", err)
	}
	if err := ExecuteStep(Step{Action: "scroll", Direction: "up", X: 3, Y: 4}, ctx); err != nil {
		t.Fatalf("scroll with no logger: %v", err)
	}
	if len(mt.clicks) != 1 || len(mt.scrolls) != 1 {
		t.Errorf("the actions did not run: clicks=%d scrolls=%d", len(mt.clicks), len(mt.scrolls))
	}
}

// TestExecuteStepUnknownAction also pins the wording the HTTP layer turns into a
// 400 unknown_action.
func TestExecuteStepUnknownAction(t *testing.T) {
	mt := newMockTarget(nil)
	ctx := newActionCtx(t, mt, t.TempDir())
	err := ExecuteStep(Step{Action: "bogus"}, ctx)
	if err == nil || err.Error() != "unknown action: bogus" {
		t.Fatalf("want %q, got %v", "unknown action: bogus", err)
	}
}

// TestNoActivateSkipsActivation checks the inverse-polarity flag from the one
// angle that is testable without a WM: a screen-scoped run never activates.
func TestStepDefaultsRoundTripJSON(t *testing.T) {
	raw := []byte(`{"action":"click","x":5,"y":6,"clicks":3,"click_gap":"20ms","no_activate":true,"direction":"down","amount":2,"wait_for":"pixel","chunk_runes":50}`)
	var s Step
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatalf("the documented wire form does not decode: %v", err)
	}
	if s.Clicks != 3 || s.ClickGap != "20ms" || !s.NoActivate || s.Direction != "down" || s.Amount != 2 || s.WaitFor != "pixel" || s.ChunkRunes != 50 {
		t.Errorf("decoded step lost fields: %+v", s)
	}
}

// --- fixtures -------------------------------------------------------------

func writeTestTemplate(t *testing.T, path string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create template: %v", err)
	}
	defer f.Close()
	if err := png.Encode(f, gradientImage(8, 8)); err != nil {
		t.Fatalf("encode template: %v", err)
	}
}

func gradientImage(w, h int) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x), G: uint8(y), B: 128, A: 255})
		}
	}
	return img
}
