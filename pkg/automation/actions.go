package automation

import (
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/jezek/xgb/xproto"
	"github.com/jezek/xgbutil"
	"github.com/jezek/xgbutil/ewmh"

	"zen-cap/pkg/capture"
	"zen-cap/pkg/config"
	"zen-cap/pkg/target"
)

// ExecContext is the state of one run. It is created by RunSteps and is not
// reusable: the variable table, the last match coordinates and the step trace
// all belong to that single request or script.
type ExecContext struct {
	Target     target.Target
	AbortChan  chan struct{}
	Logger     func(format string, args ...interface{})
	ScriptDir  string
	Config     *config.Config
	LastFoundX int
	LastFoundY int

	// hasFound distinguishes "LastFound is (0,0) because a match landed there"
	// from "nothing has matched yet" — the difference between using the last
	// match and using the centre of the screen for an unpositioned scroll.
	hasFound bool

	// LastResult is the `data` payload of the step that just ran, verbatim JSON
	// (what find_text matched, what window_state read back, where a screenshot
	// was written). It is copied into StepResult.Data and then cleared.
	LastResult json.RawMessage

	Variables map[string]interface{}
	Functions map[string][]Step

	// tracker is non-nil only under RunSteps; direct ExecuteStep callers (unit
	// tests, the picker's internals) leave it nil and nothing is recorded.
	tracker     *stepTracker
	stopOnError bool

	// resultX/resultY are the coordinates the running step wants reported, which
	// is not always LastFoundX/Y (an explicit click has no LastFound side effect).
	resultX, resultY int
	hasResult        bool

	// lastIndex is the index of the most recently recorded step, so a failure can
	// be attributed to it.
	lastIndex int

	// activateClicks opts this run into raise+activate before the first click. It
	// is a *run* property, not a step one: an HTTP agent wants "click focuses the
	// field" (it cannot do the focusing itself), while a YAML script written to be
	// focusless must stay focusless — see RunOptions.ActivateClicks.
	activateClicks bool

	// clickActivated latches the raise+activate of a run, so a batch of clicks
	// into the same window does not re-activate (and re-settle) per click.
	clickActivated bool
}

// logf is the only logging path in the engine. Logger is optional — a caller
// that does not care what a script printed must not be the reason it panics
// half-way through clicking on a live desktop.
func (ctx *ExecContext) logf(format string, args ...interface{}) {
	if ctx.Logger != nil {
		ctx.Logger(format, args...)
	}
}

// hasLogger reports whether anything is listening (the `log` action falls back
// to stdout when nothing is).
func (ctx *ExecContext) hasLogger() bool { return ctx.Logger != nil }

// beginResult clears the per-step result slot; called before each dispatch.
func (ctx *ExecContext) beginResult() {
	ctx.LastResult = nil
	ctx.resultX, ctx.resultY, ctx.hasResult = 0, 0, false
}

// runNested executes a control-flow body without polluting the enclosing step's
// result slot: without the save/restore, the trace entry for a `loop` would carry
// the coordinates and data of whatever its last child matched.
func runNested(steps []Step, ctx *ExecContext) error {
	savedResult, savedX, savedY, savedHas := ctx.LastResult, ctx.resultX, ctx.resultY, ctx.hasResult
	ctx.LastResult, ctx.resultX, ctx.resultY, ctx.hasResult = nil, 0, 0, false
	err := executeStepList(steps, ctx)
	ctx.LastResult, ctx.resultX, ctx.resultY, ctx.hasResult = savedResult, savedX, savedY, savedHas
	return err
}

// publish attaches coordinates to the step currently executing.
func (ctx *ExecContext) publish(x, y int) {
	ctx.resultX, ctx.resultY, ctx.hasResult = x, y, true
}

// setData attaches a JSON payload to the step currently executing. A value that
// cannot be marshalled is a programming error in the action, not a run failure,
// so it is reported in the log and otherwise ignored.
func (ctx *ExecContext) setData(v interface{}) {
	b, err := json.Marshal(v)
	if err != nil {
		ctx.logf("[Automation] warning: could not encode step data: %v", err)
		return
	}
	ctx.LastResult = b
}

// recordResult appends the completed step to the run trace (if there is one).
func (ctx *ExecContext) recordResult(action string, start time.Time, err error) {
	if ctx.tracker == nil {
		return
	}
	res := StepResult{
		Index:     ctx.tracker.Seq,
		Action:    action,
		OK:        err == nil,
		ElapsedMS: time.Since(start).Milliseconds(),
		Data:      ctx.LastResult,
	}
	if err != nil {
		res.Error = err.Error()
	}
	if ctx.hasResult {
		res.X, res.Y = ctx.resultX, ctx.resultY
	}
	ctx.tracker.Seq = res.Index + 1
	ctx.lastIndex = res.Index
	ctx.tracker.appendResult(res)
}

// x11ctx returns the underlying X11Target if the current target is X11-based,
// otherwise returns nil. Used for X11-specific operations (window management).
func (ctx *ExecContext) x11ctx() *target.X11Target {
	switch t := ctx.Target.(type) {
	case *target.X11Target:
		return t
	case *target.VFBTarget:
		return t.X11()
	}
	return nil
}

// leafActions is the dispatch table of every action that does something rather
// than control flow. It is the single source of the action vocabulary: the HTTP
// action catalog (catalog.go) is built from these keys, so a new action is
// documented the moment it exists and cannot drift (catalog_test.go enforces it).
var leafActions = map[string]func(Step, *ExecContext) error{
	"click":        runClick,
	"move":         runMove,
	"hold":         runHold,
	"release":      runRelease,
	"drag":         runDrag,
	"type":         runType,
	"key":          runKey,
	"wait":         runWait,
	"notify":       runNotify,
	"log":          runLog,
	"run":          runCommand,
	"clipboard":    runClipboard,
	"file":         runFile,
	"window":       runWindow,
	"find_image":   runFindImage,
	"find_text":    runFindText,
	"ocr":          runOCR,
	"screenshot":   runScreenshot,
	"scroll":       runScroll,
	"wait_until":   runWaitUntil,
	"window_state": runWindowState,
}

// controlFlowActions are handled by executeStepWithControl / executeStepList;
// they are listed here so the catalog covers them too.
var controlFlowActions = []string{
	"loop", "if_found", "if_pixel", "if_window", "var", "call", "goto", "stop",
}

// ExecuteStep runs a single leaf action. Control flow and {{variable}}
// interpolation live one level up, in executeStepWithControl — a caller that has
// a whole script should go through RunSteps, not call this.
func ExecuteStep(step Step, ctx *ExecContext) error {
	select {
	case <-ctx.AbortChan:
		return ErrAborted
	default:
	}

	switch strings.ToLower(step.Action) {
	case "goto":
		return GotoError{Target: step.Target}
	case "stop":
		return StopError{Message: step.Message}
	}

	fn, ok := leafActions[strings.ToLower(step.Action)]
	if !ok {
		return fmt.Errorf("unknown action: %s", step.Action)
	}
	return fn(step, ctx)
}

const defaultClickGap = 40 * time.Millisecond

// clickSettleDelay is how long the pointer waits after an activate, for the WM
// to reparent/focus the window before the press lands.
const clickSettleDelay = 150 * time.Millisecond

func runClick(step Step, ctx *ExecContext) error {
	x := getIntField(step.X, ctx.Variables, -1)
	y := getIntField(step.Y, ctx.Variables, -1)
	if x == -1 && y == -1 {
		x = ctx.LastFoundX
		y = ctx.LastFoundY
	}

	clicks := step.Clicks
	if clicks < 1 {
		clicks = 1
	}
	gap := defaultClickGap
	if step.ClickGap != "" {
		d, err := time.ParseDuration(step.ClickGap)
		if err != nil {
			return fmt.Errorf("invalid click_gap %q: %w", step.ClickGap, err)
		}
		gap = d
	}

	// Activation happens only when the caller asked for it (ActivateClicks) and the
	// step did not opt out (no_activate). Focus cannot be verified generically, so
	// this is the affordance that makes screenshot -> click -> type work for an
	// agent, without changing what an existing focusless script does.
	if shouldActivateBeforeClick(ctx, step) {
		if err := activateForClick(ctx); err != nil {
			ctx.logf("[Automation] activate before click: %v", err)
		}
	}

	ctx.logf("[Automation] Click: x=%d, y=%d, button=%s, clicks=%d", x, y, step.Button, clicks)
	ctx.publish(x, y)

	for i := 0; i < clicks; i++ {
		if err := ctx.Target.Click(x, y, step.Button); err != nil {
			return err
		}
		if i < clicks-1 {
			// One pointer warp, N press/release pairs: re-warping between presses
			// is what makes a fast app see two single clicks instead of one double.
			if err := sleepAbortable(gap, ctx.AbortChan); err != nil {
				return err
			}
		}
	}
	return nil
}

// shouldActivateBeforeClick is the whole activation policy, as a predicate so it
// can be pinned without a window manager: HTTP runs ask for activation (an agent
// has no other way to focus the field it is about to type into), YAML scripts never
// get it unless a caller asks, and `no_activate: true` always wins.
func shouldActivateBeforeClick(ctx *ExecContext, step Step) bool {
	return ctx.activateClicks && !step.NoActivate
}

// activateForClick raises the window a run is scoped to and asks the WM to make
// it active, then waits briefly for the WM to catch up. A no-op for
// screen-scoped and non-X11 runs.
func activateForClick(ctx *ExecContext) error {
	if ctx.clickActivated {
		return nil
	}
	x11 := ctx.x11ctx()
	if x11 == nil || !x11.IsWindowTarget() {
		return nil
	}
	ctx.clickActivated = true
	winID := x11.WinID()
	if err := StackWindow(x11.XUtil(), winID, xproto.StackModeAbove); err != nil {
		return err
	}
	if err := ewmh.ActiveWindowReq(x11.XUtil(), xproto.Window(winID)); err != nil {
		return err
	}
	// Let the WM repaint/focus before the pointer lands.
	return sleepAbortable(clickSettleDelay, ctx.AbortChan)
}

// sleepAbortable waits d, waking early with ErrAborted when the run is cancelled.
func sleepAbortable(d time.Duration, abortChan chan struct{}) error {
	if d <= 0 {
		return nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-abortChan:
		return ErrAborted
	case <-t.C:
		return nil
	}
}

func runMove(step Step, ctx *ExecContext) error {
	x := getIntField(step.X, ctx.Variables, -1)
	y := getIntField(step.Y, ctx.Variables, -1)
	if x == -1 && y == -1 {
		x = ctx.LastFoundX
		y = ctx.LastFoundY
	}
	ctx.logf("[Automation] Move: x=%d, y=%d", x, y)
	ctx.publish(x, y)
	return ctx.Target.Move(x, y)
}

// runHold presses and keeps the pointer button down at (x, y). Pair with
// runMove / runRelease to build drag and slide gestures.
func runHold(step Step, ctx *ExecContext) error {
	x := getIntField(step.X, ctx.Variables, -1)
	y := getIntField(step.Y, ctx.Variables, -1)
	if x == -1 && y == -1 {
		x = ctx.LastFoundX
		y = ctx.LastFoundY
	}
	ctx.logf("[Automation] Hold: x=%d, y=%d, button=%s", x, y, step.Button)
	ctx.publish(x, y)
	return ctx.Target.ButtonPress(x, y, step.Button)
}

// runRelease lets the pointer button go at (x, y). Tolerates releasing a
// button that was never pressed.
func runRelease(step Step, ctx *ExecContext) error {
	x := getIntField(step.X, ctx.Variables, -1)
	y := getIntField(step.Y, ctx.Variables, -1)
	if x == -1 && y == -1 {
		x = ctx.LastFoundX
		y = ctx.LastFoundY
	}
	ctx.logf("[Automation] Release: x=%d, y=%d, button=%s", x, y, step.Button)
	ctx.publish(x, y)
	return ctx.Target.ButtonRelease(x, y, step.Button)
}

// runDrag performs a full hold-move-release gesture from (x, y) to (x2, y2).
// The path is interpolated into small motion steps spread over `duration`, so
// targets that track movement (scrcpy sliding, drag-and-drop) see motion
// rather than a jump. When start equals end it degrades to a long press.
// If any step fails or the run is aborted the button is released to avoid
// leaving a stuck press, escalating to target.PanicReleaser when that release
// itself fails.
func runDrag(step Step, ctx *ExecContext) error {
	x := getIntField(step.X, ctx.Variables, -1)
	y := getIntField(step.Y, ctx.Variables, -1)
	x2 := getIntField(step.X2, ctx.Variables, -1)
	y2 := getIntField(step.Y2, ctx.Variables, -1)
	if x == -1 && y == -1 {
		x = ctx.LastFoundX
		y = ctx.LastFoundY
	}
	if x2 == -1 {
		x2 = x
	}
	if y2 == -1 {
		y2 = y
	}

	duration := 300 * time.Millisecond
	if step.Duration != "" {
		if d, err := time.ParseDuration(step.Duration); err == nil {
			duration = d
		}
	}

	ctx.logf("[Automation] Drag: (%d, %d) -> (%d, %d), button=%s, duration=%v", x, y, x2, y2, step.Button, duration)
	ctx.publish(x2, y2)

	if err := ctx.Target.ButtonPress(x, y, step.Button); err != nil {
		return err
	}

	if x2 == x && y2 == y {
		// Long press: hold in place for the duration, then release.
		select {
		case <-ctx.AbortChan:
			_ = releasePointer(ctx.Target, x, y, step.Button)
			return ErrAborted
		case <-time.After(duration):
		}
		return releasePointer(ctx.Target, x, y, step.Button)
	}

	steps := int(duration / (16 * time.Millisecond))
	if steps < 1 {
		steps = 1
	}
	stepDelay := duration / time.Duration(steps)
	for i := 1; i <= steps; i++ {
		mx := x + (x2-x)*i/steps
		my := y + (y2-y)*i/steps
		if err := ctx.Target.Move(mx, my); err != nil {
			_ = releasePointer(ctx.Target, mx, my, step.Button)
			return err
		}
		if i < steps {
			select {
			case <-ctx.AbortChan:
				_ = releasePointer(ctx.Target, mx, my, step.Button)
				return ErrAborted
			case <-time.After(stepDelay):
			}
		}
	}

	return releasePointer(ctx.Target, x2, y2, step.Button)
}

// releasePointer lets go of a button, and escalates: if the plain release fails
// on a backend that can force-release (target.PanicReleaser — X11 and VFB), a
// press left down would leave the whole desktop acting as if the mouse were
// held, so the panic release is worth trying before giving up.
func releasePointer(tgt target.Target, x, y int, button string) error {
	err := tgt.ButtonRelease(x, y, button)
	if err == nil {
		return nil
	}
	pr, ok := tgt.(target.PanicReleaser)
	if !ok {
		return err
	}
	if panicErr := pr.ReleaseAllButtons(); panicErr != nil {
		return fmt.Errorf("%w (panic release also failed: %v)", err, panicErr)
	}
	return err
}

func runType(step Step, ctx *ExecContext) error {
	var delayMs int64 = 12
	if step.Delay != "" {
		if d, err := time.ParseDuration(step.Delay); err == nil {
			delayMs = d.Milliseconds()
		}
	}

	chunk := step.ChunkRunes
	if chunk <= 0 {
		chunk = defaultTypeChunkRunes
	}

	ctx.logf("[Automation] Type: %q (delay=%dms)", step.Text, delayMs)

	// XTEST has no abort hook inside a single Type call: one 5000-character paste
	// would hold the input gate until it finished, however long the client waited.
	// Typing in rune-sized pieces with an abort check between them makes the
	// longest uncancellable stretch one chunk instead of the whole string.
	runes := []rune(step.Text)
	for len(runes) > 0 {
		piece := runes
		if len(piece) > chunk {
			piece = piece[:chunk]
		}
		if err := ctx.Target.Type(string(piece), delayMs); err != nil {
			return err
		}
		runes = runes[len(piece):]
		if len(runes) > 0 {
			select {
			case <-ctx.AbortChan:
				return ErrAborted
			default:
			}
		}
	}
	return nil
}

func runKey(step Step, ctx *ExecContext) error {
	ctx.logf("[Automation] Key: %q", step.Keys)
	return ctx.Target.Key(step.Keys)
}

func runWait(step Step, ctx *ExecContext) error {
	durStr := step.Duration
	if durStr == "" {
		durStr = "1s"
	}
	d, err := time.ParseDuration(durStr)
	if err != nil {
		return fmt.Errorf("invalid wait duration %q: %w", durStr, err)
	}

	ctx.logf("[Automation] Wait: %v", d)
	select {
	case <-ctx.AbortChan:
		return ErrAborted
	case <-time.After(d):
	}
	return nil
}

func runNotify(step Step, ctx *ExecContext) error {
	title := step.Title
	if title == "" {
		title = "Zen-Cap"
	}
	message := step.Message
	if message == "" {
		message = "Action complete."
	}
	ctx.logf("[Automation] Notify: %s - %s", title, message)
	cmd := exec.Command("notify-send", "-a", "Zen-Cap", title, message)
	return cmd.Run()
}

func runLog(step Step, ctx *ExecContext) error {
	message := step.Message
	if message == "" {
		message = step.Text
	}
	if ctx.hasLogger() {
		ctx.logf("[Automation] Log: %s", message)
	} else {
		fmt.Printf("[Automation] Log: %s\n", message)
	}
	return nil
}

func runCommand(step Step, ctx *ExecContext) error {
	timeout := 10 * time.Second
	if step.Timeout != "" {
		if d, err := time.ParseDuration(step.Timeout); err == nil {
			timeout = d
		}
	}

	ctx.logf("[Automation] Run command: %q (timeout=%v)", step.Command, timeout)
	c, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := exec.CommandContext(c, "bash", "-c", step.Command)
	return cmd.Run()
}

func runClipboard(step Step, ctx *ExecContext) error {
	mode := strings.ToLower(step.Mode)
	if mode == "" {
		mode = "write"
	}

	if mode == "write" {
		ctx.logf("[Automation] Clipboard: write %q", step.Text)
		return capture.CopyTextToClipboard(step.Text)
	} else if mode == "read" {
		text, err := capture.ReadTextFromClipboard()
		if err != nil {
			return err
		}
		ctx.logf("[Automation] Clipboard: read %q", text)
		if step.Name != "" {
			ctx.Variables[step.Name] = text
		}
	} else {
		return fmt.Errorf("unknown clipboard mode: %q", step.Mode)
	}

	return nil
}

func runFile(step Step, ctx *ExecContext) error {
	if step.Target == "" {
		return fmt.Errorf("missing target path for file action")
	}
	path := step.Target
	if !filepath.IsAbs(path) && ctx.ScriptDir != "" {
		path = filepath.Join(ctx.ScriptDir, path)
	}

	mode := strings.ToLower(step.Mode)
	switch mode {
	case "read":
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if step.Name != "" {
			ctx.Variables[step.Name] = string(data)
		}
	case "write", "append":
		flags := os.O_CREATE | os.O_WRONLY
		if mode == "append" {
			flags |= os.O_APPEND
		} else {
			flags |= os.O_TRUNC
		}
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			return err
		}
		f, err := os.OpenFile(path, flags, 0644)
		if err != nil {
			return err
		}
		defer f.Close()
		if _, err := f.WriteString(step.Text); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unknown file mode: %q", step.Mode)
	}
	return nil
}

// runWindow dispatches the `window` action. Every mode goes through
// window_ops.go: no X request is issued here, and no request is left unverified
// when verification is possible (F3, F4).
func runWindow(step Step, ctx *ExecContext) error {
	x11 := ctx.x11ctx()
	if x11 == nil {
		return fmt.Errorf("window action requires an x11 or vfb target")
	}
	xu := x11.XUtil()

	var winID uint32
	var err error
	if step.Window != nil {
		if winID, err = ResolveWindow(xu, step.Window); err != nil {
			return err
		}
	} else {
		winID = x11.WinID()
	}
	if winID == 0 {
		return fmt.Errorf("no target window specified or resolved")
	}

	mode := strings.ToLower(step.Mode)
	ctx.logf("[Automation] Window action %q on window 0x%x", mode, winID)

	switch mode {
	case "activate":
		return ewmh.ActiveWindowReq(xu, xproto.Window(winID))
	case "close":
		return ewmh.CloseWindow(xu, xproto.Window(winID))
	case "minimize":
		if err := MinimizeWindow(xu, winID); err != nil {
			return err
		}
		return WaitForIconic(xu, winID, windowStateTimeout)
	case "maximize":
		if err := ewmh.WmStateReqExtra(xu, xproto.Window(winID), ewmh.StateAdd,
			"_NET_WM_STATE_MAXIMIZED_HORZ", "_NET_WM_STATE_MAXIMIZED_VERT", 2); err != nil {
			return fmt.Errorf("x11: maximize window 0x%x: %w", winID, err)
		}
		// Maximized is verified by state atom *or* by the window visibly covering
		// the screen: tiling WMs are maximal already and answer the request by
		// doing nothing, which is not a failure.
		return WaitForStateOrCoverage(xu, winID, "_NET_WM_STATE_MAXIMIZED_HORZ", maximizeCoverage, windowStateTimeout)
	case "fullscreen":
		if err := RequestFullscreen(xu, winID); err != nil {
			return err
		}
		return WaitForStateOrCoverage(xu, winID, "_NET_WM_STATE_FULLSCREEN", fullscreenCoverage, windowStateTimeout)
	case "restore":
		if err := RequestState(xu, winID, false, "_NET_WM_STATE_FULLSCREEN"); err != nil {
			return err
		}
		if err := ClearMaximized(xu, winID); err != nil {
			return err
		}
		if err := RequestState(xu, winID, false, "_NET_WM_STATE_HIDDEN"); err != nil {
			return err
		}
		if err := RestoreWindow(xu, winID); err != nil {
			return err
		}
		return WaitForViewable(xu, winID, windowStateTimeout)
	case "raise":
		return StackWindow(xu, winID, xproto.StackModeAbove)
	case "lower":
		return StackWindow(xu, winID, xproto.StackModeBelow)
	case "geometry":
		return windowGeometry(step, ctx, xu, winID)
	default:
		return fmt.Errorf("unsupported window mode: %q", step.Mode)
	}
}

// windowGeometry implements `window: geometry`. x/y address the CLIENT origin —
// the same (0,0) a screenshot of this window uses — and the result is read back
// from the server rather than assumed. Omitted fields keep their current value.
func windowGeometry(step Step, ctx *ExecContext, xu *xgbutil.XUtil, winID uint32) error {
	x := getIntField(step.X, ctx.Variables, -1)
	y := getIntField(step.Y, ctx.Variables, -1)

	current, err := CurrentGeometry(xu, winID, false)
	if err != nil {
		return err
	}

	want := current
	if x >= 0 {
		want.X = x
	}
	if y >= 0 {
		want.Y = y
	}
	if step.OffsetX > 0 {
		want.W = step.OffsetX
	}
	if step.OffsetY > 0 {
		want.H = step.OffsetY
	}

	// Resize before moving: clearing the maximized state (which resizing a
	// managed window requires) can shift the position, and the WM has then had
	// every say it wants before we place the window for good.
	if want.W != current.W || want.H != current.H {
		if err := ResizeWindow(xu, winID, want.W, want.H); err != nil {
			return err
		}
	}
	if want.X != current.X || want.Y != current.Y {
		if err := MoveWindow(xu, winID, want.X, want.Y); err != nil {
			return err
		}
	}

	actual, err := WaitForGeometry(xu, winID, want, windowGeometryTimeout)
	if err != nil {
		return err
	}
	ctx.logf("[Automation] Window 0x%x geometry is now %s (requested %s)", winID, actual, want)
	return nil
}

func runFindImage(step Step, ctx *ExecContext) error {
	if step.Image == "" {
		return fmt.Errorf("missing template image path in find_image step")
	}

	// Resolve path
	needle, err := decodeTemplate(step.Image, ctx.ScriptDir)
	if err != nil {
		return err
	}

	confidence := step.Confidence
	if confidence <= 0 {
		confidence = 0.90
	}

	timeout := defaultFindTimeout
	if step.Timeout != "" {
		if d, err := time.ParseDuration(step.Timeout); err == nil {
			timeout = d
		}
	}

	ctx.logf("[Automation] Find Image: %q (timeout=%v, confidence=%.2f)", step.Image, timeout, confidence)

	poller := newScreenPoller(ctx, timeout, capturePollInterval)
	var bestConf float64
	for {
		res, err := poller.step(step.Region, func(haystack image.Image, offX, offY int) (bool, error) {
			x, y, conf, err := FindImage(haystack, needle, confidence)
			if conf > bestConf {
				bestConf = conf
			}
			if err != nil {
				return false, nil
			}

			// Restore coordinates to absolute screen/window bounds
			x += offX
			y += offY
			ctx.logf("[Automation] Match found at (%d, %d) with confidence %.4f", x, y, conf)
			ctx.setFound(x, y)
			ctx.setData(map[string]interface{}{"x": x, "y": y, "confidence": conf})

			switch strings.ToLower(step.Then) {
			case "click":
				return true, runClick(Step{Action: "click", X: x + step.OffsetX, Y: y + step.OffsetY, Button: step.Button}, ctx)
			case "move":
				return true, runMove(Step{Action: "move", X: x + step.OffsetX, Y: y + step.OffsetY}, ctx)
			}
			return true, nil
		})
		if err != nil {
			return err
		}
		if res == outDone {
			return nil
		}
		if res == outTimeout || res == outCaptureFailed {
			return fmt.Errorf("template image %q not found within timeout (best confidence %.4f)%s", step.Image, bestConf, poller.captureNote())
		}
	}
}

func runFindText(step Step, ctx *ExecContext) error {
	if step.Text == "" {
		return fmt.Errorf("missing target text in find_text step")
	}

	timeout := defaultFindTimeout
	if step.Timeout != "" {
		if d, err := time.ParseDuration(step.Timeout); err == nil {
			timeout = d
		}
	}

	ocrAddr := "http://localhost:8765"
	ocrLang := "ch"
	if ctx.Config != nil {
		ocrAddr = ctx.Config.OCRAddress
		ocrLang = ctx.Config.OCRLanguage
	}

	ctx.logf("[Automation] Find Text: %q (timeout=%v, lang=%s)", step.Text, timeout, ocrLang)

	poller := newScreenPoller(ctx, timeout, capturePollInterval)
	var lastErr error
	for {
		res, err := poller.step(step.Region, func(haystack image.Image, offX, offY int) (bool, error) {
			x, y, conf, err := FindText(haystack, ocrAddr, ocrLang, step.Text)
			if err != nil {
				// Keep the last OCR failure so the timeout message can quote it.
				lastErr = err
				return false, nil
			}

			// Restore coordinates to absolute screen/window bounds
			x += offX
			y += offY
			ctx.logf("[Automation] Text %q found at (%d, %d) with confidence %.4f", step.Text, x, y, conf)
			ctx.setFound(x, y)
			ctx.setData(map[string]interface{}{"x": x, "y": y, "confidence": conf})

			switch strings.ToLower(step.Then) {
			case "click":
				return true, runClick(Step{Action: "click", X: x + step.OffsetX, Y: y + step.OffsetY, Button: step.Button}, ctx)
			case "move":
				return true, runMove(Step{Action: "move", X: x + step.OffsetX, Y: y + step.OffsetY}, ctx)
			}
			return true, nil
		})
		if err != nil {
			return err
		}
		if res == outDone {
			return nil
		}
		if res == outTimeout || res == outCaptureFailed {
			return fmt.Errorf("text %q not found within timeout%s: %w", step.Text, poller.captureNote(), joinErr(lastErr, ErrNotFound))
		}
	}
}

// joinErr returns err, or fallback when err is nil, so a timeout message always
// has something to quote even if every attempt failed before the matcher ran.
func joinErr(err, fallback error) error {
	if err != nil {
		return err
	}
	return fallback
}

func runOCR(step Step, ctx *ExecContext) error {
	targetText := step.Text
	if targetText == "" {
		targetText = step.Target
	}
	if targetText == "" {
		return fmt.Errorf("missing target text in ocr step")
	}

	timeout := defaultFindTimeout
	if step.Timeout != "" {
		if d, err := time.ParseDuration(step.Timeout); err == nil {
			timeout = d
		}
	}

	ocrAddr := "http://localhost:8765"
	ocrLang := "ch"
	if ctx.Config != nil {
		ocrAddr = ctx.Config.OCRAddress
		ocrLang = ctx.Config.OCRLanguage
	}
	if step.Language != "" {
		ocrLang = step.Language
	}
	ocrModel := step.Model

	ctx.logf("[Automation] OCR search: %q (timeout=%v, lang=%s, model=%s)", targetText, timeout, ocrLang, ocrModel)

	poller := newScreenPoller(ctx, timeout, capturePollInterval)
	var lastErr error
	for {
		res, err := poller.step(step.Region, func(haystack image.Image, offX, offY int) (bool, error) {
			x, y, bounds, conf, err := FindTextWithBounds(haystack, ocrAddr, ocrLang, ocrModel, targetText)
			if err != nil {
				lastErr = err
				return false, nil
			}

			if step.Output != "" {
				minX, minY := bounds.Min.X, bounds.Min.Y
				rw := bounds.Max.X - minX
				rh := bounds.Max.Y - minY

				textboxImg, _, _ := CropImage(haystack, minX, minY, rw, rh)

				outputPath := step.Output
				if !filepath.IsAbs(outputPath) && ctx.ScriptDir != "" {
					outputPath = filepath.Join(ctx.ScriptDir, outputPath)
				}
				if err := os.MkdirAll(filepath.Dir(outputPath), 0755); err != nil {
					return true, fmt.Errorf("failed to create directory for OCR output: %w", err)
				}
				outF, err := os.Create(outputPath)
				if err != nil {
					return true, fmt.Errorf("failed to create OCR output file: %w", err)
				}
				if err := png.Encode(outF, textboxImg); err != nil {
					outF.Close()
					return true, fmt.Errorf("failed to encode/write OCR textbox image: %w", err)
				}
				outF.Close()
				ctx.logf("[Automation] OCR textbox image saved to %s", outputPath)
			}

			// Restore coordinates to absolute screen/window bounds
			x += offX
			y += offY
			ctx.logf("[Automation] OCR: Text %q found at (%d, %d) with confidence %.4f", targetText, x, y, conf)
			ctx.setFound(x, y)
			ctx.setData(map[string]interface{}{"x": x, "y": y, "confidence": conf, "bounds": map[string]int{
				"x": bounds.Min.X, "y": bounds.Min.Y,
				"w": bounds.Max.X - bounds.Min.X, "h": bounds.Max.Y - bounds.Min.Y,
			}})
			targetX := x + step.OffsetX
			targetY := y + step.OffsetY

			switch strings.ToLower(step.Then) {
			case "click":
				return true, runClick(Step{Action: "click", X: targetX, Y: targetY, Button: step.Button}, ctx)
			case "move":
				return true, runMove(Step{Action: "move", X: targetX, Y: targetY}, ctx)
			}
			return true, nil
		})
		if err != nil {
			return err
		}
		if res == outDone {
			return nil
		}
		if res == outTimeout || res == outCaptureFailed {
			return fmt.Errorf("text %q not found via OCR within timeout%s: %w", targetText, poller.captureNote(), joinErr(lastErr, ErrNotFound))
		}
	}
}

// ---------------------------------------------------------------------------
// Actions added for the /automation HTTP API (plan steps A5/A6).
//
// They are ordinary engine actions — a YAML script can use them too — but they
// exist because an HTTP agent has nothing else to lean on: it cannot see the
// screen, only what a step reports back.
// ---------------------------------------------------------------------------

const defaultTypeChunkRunes = 200
const maxScrollTicks = 50

// runScroll sends wheel/button clicks at one point. Vertical scroll is X buttons
// 4 (up) / 5 (down), horizontal is 6 (left) / 7 (right) — see
// pkg/target.X11Target.Scroll, which owns the sign convention.
func runScroll(step Step, ctx *ExecContext) error {
	n := step.Amount
	if n == 0 {
		n = 3
	}
	if n < 0 {
		return fmt.Errorf("scroll amount must be positive, got %d", n)
	}
	if n > maxScrollTicks {
		ctx.logf("[Automation] scroll amount %d clamped to %d", n, maxScrollTicks)
		n = maxScrollTicks
	}

	dx, dy := 0, 0
	switch strings.ToLower(step.Direction) {
	case "down":
		dy = n
	case "up":
		dy = -n
	case "right":
		dx = n
	case "left":
		dx = -n
	default:
		return fmt.Errorf("unknown scroll direction %q (want up, down, left or right)", step.Direction)
	}

	x, y, ok := coordinateOrDefault(step, ctx, true)
	if !ok {
		return fmt.Errorf("scroll needs x/y, a previous match or a sized target to aim at")
	}

	ctx.logf("[Automation] Scroll: (%d, %d) %s x%d", x, y, step.Direction, n)
	ctx.publish(x, y)
	return ctx.Target.Scroll(x, y, dx, dy)
}

// coordinateOrDefault resolves a step's x/y: explicit values win, then the last
// successful match, then the centre of the captured area. The third fallback is
// what makes "scroll the middle of this window" a one-line request.
func coordinateOrDefault(step Step, ctx *ExecContext, preferLastFound bool) (int, int, bool) {
	x := getIntField(step.X, ctx.Variables, -1)
	y := getIntField(step.Y, ctx.Variables, -1)
	if x >= 0 && y >= 0 {
		return x, y, true
	}
	if preferLastFound && ctx.hasFound {
		return ctx.LastFoundX, ctx.LastFoundY, true
	}
	w, h := ctx.Target.ScreenSize()
	if w <= 0 || h <= 0 {
		return 0, 0, false
	}
	cx, cy := w/2, h/2
	if x >= 0 {
		return x, cy, true
	}
	if y >= 0 {
		return cx, y, true
	}
	return cx, cy, true
}

// runScreenshot writes one capture of the run's target to disk. Unlike
// POST /screenshot it deliberately runs no pipeline: no clipboard copy, no
// upload, no LLM call, and above all no overlay — an overlay grabs the pointer
// and keyboard, which would deadlock the agent waiting for the response.
func runScreenshot(step Step, ctx *ExecContext) error {
	res, err := CaptureTo(ctx.Target, ShotSpec{
		Output: step.Output,
		Region: step.Region,
		Config: ctx.Config,
	})
	if err != nil {
		return err
	}
	ctx.logf("[Automation] Screenshot: %s (%dx%d, %d bytes)", res.Path, res.Width, res.Height, res.Bytes)
	ctx.setData(res)
	return nil
}

// runWaitUntil blocks on a condition instead of making the agent poll over HTTP.
//
// Bounded twice over: the poller's own deadline, and the run's watchdog. The
// error text always says what was wanted and what was seen, because that string
// is the only diagnostic an agent gets.
func runWaitUntil(step Step, ctx *ExecContext) error {
	timeout := defaultFindTimeout
	if step.Timeout != "" {
		d, err := time.ParseDuration(step.Timeout)
		if err != nil {
			return fmt.Errorf("invalid timeout %q for wait_until: %w", step.Timeout, err)
		}
		timeout = d
	}

	cond := strings.ToLower(step.WaitFor)
	if cond == "" {
		return fmt.Errorf("wait_until requires wait_for (window, window_gone, image, text or pixel)")
	}

	ctx.logf("[Automation] Wait until %s (timeout=%v)", cond, timeout)

	switch cond {
	case "window", "window_gone":
		return waitUntilWindow(step, ctx, cond, timeout)
	case "image", "text", "pixel":
		return waitUntilScreen(step, ctx, cond, timeout)
	default:
		return fmt.Errorf("unknown wait_for %q (want window, window_gone, image, text or pixel)", cond)
	}
}

func waitUntilWindow(step Step, ctx *ExecContext, cond string, timeout time.Duration) error {
	if step.Window == nil {
		return fmt.Errorf("wait_until %q requires a window selector", cond)
	}
	x11 := ctx.x11ctx()
	if x11 == nil {
		return fmt.Errorf("wait_until %q requires an x11 or vfb target", cond)
	}
	xu := x11.XUtil()

	want := cond == "window"
	poller := newScreenPoller(ctx, timeout, statePollInterval)
	for {
		res, err := poller.tick(func() (bool, error) {
			_, err := ResolveWindow(xu, step.Window)
			if err != nil {
				return !want, nil
			}
			return want, nil
		})
		if err != nil {
			return err
		}
		if res == outDone {
			// Re-resolve for the report: the id the condition matched is the id the
			// agent wants to address next.
			id, _ := ResolveWindow(xu, step.Window)
			info, infoErr := DescribeWindow(xu, id)
			if infoErr != nil {
				info = WindowInfo{ID: id, IDHex: fmt.Sprintf("0x%x", id)}
			}
			ctx.setData(info)
			ctx.logf("[Automation] wait_until %s satisfied: window 0x%x %q", cond, id, info.Title)
			return nil
		}
		if res == outTimeout {
			return fmt.Errorf("timed out after %v waiting for %s: no window matching title=%q class=%q%s",
				timeout, cond, step.Window.Title, step.Window.Class, poller.captureNote())
		}
	}
}

func waitUntilScreen(step Step, ctx *ExecContext, cond string, timeout time.Duration) error {
	var describe string
	var lastSeen string // for pixel: what the screen actually showed, in the error text
	var match func(haystack image.Image, offX, offY int) (bool, error)

	switch cond {
	case "image":
		path := step.Image
		if path == "" {
			path = step.Target
		}
		if path == "" {
			return fmt.Errorf("wait_until image requires an image path")
		}
		needle, err := decodeTemplate(path, ctx.ScriptDir)
		if err != nil {
			return err
		}
		confidence := step.Confidence
		if confidence <= 0 {
			confidence = 0.90
		}
		describe = fmt.Sprintf("image %q at confidence %.2f", path, confidence)
		match = func(haystack image.Image, offX, offY int) (bool, error) {
			x, y, conf, err := FindImage(haystack, needle, confidence)
			if err != nil {
				return false, nil
			}
			ctx.setFound(x+offX, y+offY)
			ctx.setData(map[string]interface{}{"x": x + offX, "y": y + offY, "confidence": conf})
			return true, nil
		}
	case "text":
		needle := step.Text
		if needle == "" {
			needle = step.Target
		}
		if needle == "" {
			return fmt.Errorf("wait_until text requires text to wait for")
		}
		ocrAddr, ocrLang, ocrModel := ocrSettings(step, ctx)
		describe = fmt.Sprintf("text %q", needle)
		match = func(haystack image.Image, offX, offY int) (bool, error) {
			x, y, _, conf, err := FindTextWithBounds(haystack, ocrAddr, ocrLang, ocrModel, needle)
			if err != nil {
				return false, nil
			}
			ctx.setFound(x+offX, y+offY)
			ctx.setData(map[string]interface{}{"x": x + offX, "y": y + offY, "confidence": conf})
			return true, nil
		}
	case "pixel":
		x := getIntField(step.X, ctx.Variables, -1)
		y := getIntField(step.Y, ctx.Variables, -1)
		if x < 0 || y < 0 {
			return fmt.Errorf("wait_until pixel requires x and y")
		}
		want, err := parseHexColor(step.Color)
		if err != nil {
			return err
		}
		tol := step.Tolerance
		if tol < 0 {
			return fmt.Errorf("tolerance must be >= 0, got %d", tol)
		}
		describe = fmt.Sprintf("pixel (%d, %d) == %s (+/- %d)", x, y, step.Color, tol)
		lastSeen = "nothing"
		match = func(haystack image.Image, _, _ int) (bool, error) {
			got, ok := pixelAt(haystack, x, y)
			if !ok {
				b := haystack.Bounds()
				return false, fmt.Errorf("pixel (%d, %d) is outside the captured %dx%d area", x, y, b.Dx(), b.Dy())
			}
			lastSeen = got.Hex()
			ctx.setData(map[string]interface{}{"x": x, "y": y, "color": got.Hex(), "want": step.Color})
			if colorNear(got, want, tol) {
				return true, nil
			}
			return false, nil
		}
	}

	poller := newScreenPoller(ctx, timeout, capturePollInterval)
	for {
		res, err := poller.step(step.Region, match)
		if err != nil {
			return err
		}
		if res == outDone {
			return nil
		}
		if res == outTimeout || res == outCaptureFailed {
			seen := ""
			if lastSeen != "" {
				seen = fmt.Sprintf(", last seen %s", lastSeen)
			}
			return fmt.Errorf("timed out after %v waiting for %s%s%s", timeout, describe, seen, poller.captureNote())
		}
	}
}

// runWindowState reads back what the window manager actually did — the difference
// between "the request was sent" and "the window is fullscreen now".
func runWindowState(step Step, ctx *ExecContext) error {
	x11 := ctx.x11ctx()
	if x11 == nil {
		return fmt.Errorf("window_state requires an x11 or vfb target")
	}
	xu := x11.XUtil()

	var winID uint32
	var err error
	if step.Window != nil {
		if winID, err = ResolveWindow(xu, step.Window); err != nil {
			return err
		}
	} else {
		winID = x11.WinID()
	}
	if winID == 0 {
		return fmt.Errorf("window_state needs a window: the run is screen-scoped, pass a window selector")
	}

	info, err := DescribeWindow(xu, winID)
	if err != nil {
		return err
	}
	ctx.setData(info)
	ctx.logf("[Automation] window 0x%x: %s %q state=%s", winID, info.Geometry, info.Title, strings.Join(info.State.States, ","))
	return nil
}

// ---------------------------------------------------------------------------
// Small shared helpers for the actions above (and for engine.go's if_pixel).
// ---------------------------------------------------------------------------

// setFound records a match: it feeds both the {{LastFound}} style coordinate
// chaining of subsequent steps and the x/y reported in the step result.
func (ctx *ExecContext) setFound(x, y int) {
	ctx.LastFoundX, ctx.LastFoundY = x, y
	ctx.hasFound = true
	ctx.publish(x, y)
}

// ocrSettings resolves the OCR endpoint/language/model from config and step.
func ocrSettings(step Step, ctx *ExecContext) (addr, lang, model string) {
	addr, lang = "http://localhost:8765", "ch"
	if ctx.Config != nil {
		addr, lang = ctx.Config.OCRAddress, ctx.Config.OCRLanguage
	}
	if step.Language != "" {
		lang = step.Language
	}
	return addr, lang, step.Model
}

// decodeTemplate loads a match template, resolving relative paths against the
// script directory (for HTTP runs that is cfg.AutomationDir).
func decodeTemplate(path, scriptDir string) (image.Image, error) {
	imgPath := path
	if !filepath.IsAbs(imgPath) && scriptDir != "" {
		imgPath = filepath.Join(scriptDir, imgPath)
	}
	f, err := os.Open(imgPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open template image: %w", err)
	}
	defer f.Close()
	needle, _, err := image.Decode(f)
	if err != nil {
		return nil, fmt.Errorf("failed to decode template image: %w", err)
	}
	return needle, nil
}

// rgb is a plain 8-bit colour; the engine needs nothing richer, and using its own
// type keeps the tolerance rules in one place instead of spread over two actions.
type rgb struct{ r, g, b uint8 }

func (c rgb) Hex() string { return fmt.Sprintf("#%02x%02x%02x", c.r, c.g, c.b) }

func parseHexColor(s string) (rgb, error) {
	var c rgb
	hexStr := strings.TrimPrefix(strings.TrimSpace(s), "#")
	n, err := fmt.Sscanf(hexStr, "%02x%02x%02x", &c.r, &c.g, &c.b)
	if err != nil || n != 3 {
		return c, fmt.Errorf("invalid hex color string %q (want #rrggbb)", s)
	}
	return c, nil
}

// pixelAt reads one pixel, reporting false when the point is outside the image.
func pixelAt(img image.Image, x, y int) (rgb, bool) {
	b := img.Bounds()
	if x < b.Min.X || x >= b.Max.X || y < b.Min.Y || y >= b.Max.Y {
		return rgb{}, false
	}
	r, g, bl, _ := img.At(x, y).RGBA()
	return rgb{r: uint8(r >> 8), g: uint8(g >> 8), b: uint8(bl >> 8)}, true
}

// colorNear compares channel-wise with a per-channel tolerance, which is what
// if_pixel has always meant by "tolerance".
func colorNear(got, want rgb, tolerance int) bool {
	diff := func(a, b uint8) int {
		if a > b {
			return int(a - b)
		}
		return int(b - a)
	}
	return diff(got.r, want.r) <= tolerance &&
		diff(got.g, want.g) <= tolerance &&
		diff(got.b, want.b) <= tolerance
}
