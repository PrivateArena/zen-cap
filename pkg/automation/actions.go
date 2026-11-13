package automation

import (
	"context"
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

type ExecContext struct {
	Target     target.Target
	AbortChan  chan struct{}
	Logger     func(string, ...interface{})
	ScriptDir  string
	Config     *config.Config
	LastFoundX int
	LastFoundY int

	Variables map[string]interface{}
	Functions map[string][]Step
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

func ExecuteStep(step Step, ctx *ExecContext) error {
	select {
	case <-ctx.AbortChan:
		return fmt.Errorf("execution aborted by user")
	default:
	}

	switch strings.ToLower(step.Action) {
	case "click":
		return runClick(step, ctx)
	case "move":
		return runMove(step, ctx)
	case "hold":
		return runHold(step, ctx)
	case "release":
		return runRelease(step, ctx)
	case "drag":
		return runDrag(step, ctx)
	case "type":
		return runType(step, ctx)
	case "key":
		return runKey(step, ctx)
	case "wait":
		return runWait(step, ctx)
	case "notify":
		return runNotify(step, ctx)
	case "log":
		return runLog(step, ctx)
	case "run":
		return runCommand(step, ctx)
	case "clipboard":
		return runClipboard(step, ctx)
	case "file":
		return runFile(step, ctx)
	case "window":
		return runWindow(step, ctx)
	case "find_image":
		return runFindImage(step, ctx)
	case "find_text":
		return runFindText(step, ctx)
	case "ocr":
		return runOCR(step, ctx)
	case "goto":
		return GotoError{Target: step.Target}
	case "stop":
		return StopError{Message: step.Message}
	default:
		return fmt.Errorf("unknown action: %s", step.Action)
	}
}

func runClick(step Step, ctx *ExecContext) error {
	x := getIntField(step.X, ctx.Variables, -1)
	y := getIntField(step.Y, ctx.Variables, -1)
	if x == -1 && y == -1 {
		x = ctx.LastFoundX
		y = ctx.LastFoundY
	}
	ctx.Logger("[Automation] Click: x=%d, y=%d, button=%s", x, y, step.Button)
	return ctx.Target.Click(x, y, step.Button)
}

func runMove(step Step, ctx *ExecContext) error {
	x := getIntField(step.X, ctx.Variables, -1)
	y := getIntField(step.Y, ctx.Variables, -1)
	if x == -1 && y == -1 {
		x = ctx.LastFoundX
		y = ctx.LastFoundY
	}
	ctx.Logger("[Automation] Move: x=%d, y=%d", x, y)
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
	ctx.Logger("[Automation] Hold: x=%d, y=%d, button=%s", x, y, step.Button)
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
	ctx.Logger("[Automation] Release: x=%d, y=%d, button=%s", x, y, step.Button)
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

	ctx.Logger("[Automation] Drag: (%d, %d) -> (%d, %d), button=%s, duration=%v", x, y, x2, y2, step.Button, duration)

	if err := ctx.Target.ButtonPress(x, y, step.Button); err != nil {
		return err
	}

	if x2 == x && y2 == y {
		// Long press: hold in place for the duration, then release.
		select {
		case <-ctx.AbortChan:
			_ = releasePointer(ctx.Target, x, y, step.Button)
			return fmt.Errorf("execution aborted by user")
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
				return fmt.Errorf("execution aborted by user")
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
	ctx.Logger("[Automation] Type: %q (delay=%dms)", step.Text, delayMs)
	return ctx.Target.Type(step.Text, delayMs)
}

func runKey(step Step, ctx *ExecContext) error {
	ctx.Logger("[Automation] Key: %q", step.Keys)
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

	ctx.Logger("[Automation] Wait: %v", d)
	select {
	case <-ctx.AbortChan:
		return fmt.Errorf("execution aborted by user")
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
	ctx.Logger("[Automation] Notify: %s - %s", title, message)
	cmd := exec.Command("notify-send", "-a", "Zen-Cap", title, message)
	return cmd.Run()
}

func runLog(step Step, ctx *ExecContext) error {
	message := step.Message
	if message == "" {
		message = step.Text
	}
	if ctx.Logger != nil {
		ctx.Logger("[Automation] Log: %s", message)
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

	ctx.Logger("[Automation] Run command: %q (timeout=%v)", step.Command, timeout)
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
		ctx.Logger("[Automation] Clipboard: write %q", step.Text)
		return capture.CopyTextToClipboard(step.Text)
	} else if mode == "read" {
		text, err := capture.ReadTextFromClipboard()
		if err != nil {
			return err
		}
		ctx.Logger("[Automation] Clipboard: read %q", text)
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
	ctx.Logger("[Automation] Window action %q on window 0x%x", mode, winID)

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
	ctx.Logger("[Automation] Window 0x%x geometry is now %s (requested %s)", winID, actual, want)
	return nil
}

func runFindImage(step Step, ctx *ExecContext) error {
	if step.Image == "" {
		return fmt.Errorf("missing template image path in find_image step")
	}

	// Resolve path
	imgPath := step.Image
	if !filepath.IsAbs(imgPath) && ctx.ScriptDir != "" {
		imgPath = filepath.Join(ctx.ScriptDir, imgPath)
	}

	f, err := os.Open(imgPath)
	if err != nil {
		return fmt.Errorf("failed to open template image: %w", err)
	}
	defer f.Close()

	needle, _, err := image.Decode(f)
	if err != nil {
		return fmt.Errorf("failed to decode template image: %w", err)
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

	ctx.Logger("[Automation] Find Image: %q (timeout=%v, confidence=%.2f)", step.Image, timeout, confidence)

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
			ctx.Logger("[Automation] Match found at (%d, %d) with confidence %.4f", x, y, conf)
			ctx.LastFoundX = x
			ctx.LastFoundY = y

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

	ctx.Logger("[Automation] Find Text: %q (timeout=%v, lang=%s)", step.Text, timeout, ocrLang)

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
			ctx.Logger("[Automation] Text %q found at (%d, %d) with confidence %.4f", step.Text, x, y, conf)
			ctx.LastFoundX = x
			ctx.LastFoundY = y

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

	ctx.Logger("[Automation] OCR search: %q (timeout=%v, lang=%s, model=%s)", targetText, timeout, ocrLang, ocrModel)

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
				ctx.Logger("[Automation] OCR textbox image saved to %s", outputPath)
			}

			// Restore coordinates to absolute screen/window bounds
			x += offX
			y += offY
			ctx.Logger("[Automation] OCR: Text %q found at (%d, %d) with confidence %.4f", targetText, x, y, conf)
			ctx.LastFoundX = x
			ctx.LastFoundY = y
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
