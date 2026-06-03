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
	"github.com/jezek/xgbutil/ewmh"
	"github.com/jezek/xgbutil/xwindow"

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

	Variables  map[string]interface{}
	Functions  map[string][]Step
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

func runWindow(step Step, ctx *ExecContext) error {
	x11 := ctx.x11ctx()
	if x11 == nil {
		return fmt.Errorf("window action requires an x11 or vfb target")
	}
	xu := x11.XUtil()
	var winID uint32
	var err error

	if step.Window != nil {
		winID, err = ResolveWindow(xu, step.Window)
		if err != nil {
			return err
		}
	} else {
		winID = x11.WinID()
	}
	if winID == 0 {
		return fmt.Errorf("no target window specified or resolved")
	}

	mode := strings.ToLower(step.Mode)
	switch mode {
	case "activate":
		return ewmh.ActiveWindowReq(xu, xproto.Window(winID))
	case "close":
		return ewmh.CloseWindow(xu, xproto.Window(winID))
	case "minimize":
		return ewmh.WmStateReq(xu, xproto.Window(winID), 1, "_NET_WM_STATE_HIDDEN")
	case "maximize":
		return ewmh.WmStateReqExtra(xu, xproto.Window(winID), 1, "_NET_WM_STATE_MAXIMIZED_HORZ", "_NET_WM_STATE_MAXIMIZED_VERT", 1)
	case "fullscreen":
		return ewmh.WmStateReq(xu, xproto.Window(winID), 1, "_NET_WM_STATE_FULLSCREEN")
	case "restore":
		_ = ewmh.WmStateReq(xu, xproto.Window(winID), 0, "_NET_WM_STATE_FULLSCREEN")
		_ = ewmh.WmStateReqExtra(xu, xproto.Window(winID), 0, "_NET_WM_STATE_MAXIMIZED_HORZ", "_NET_WM_STATE_MAXIMIZED_VERT", 1)
		_ = ewmh.WmStateReq(xu, xproto.Window(winID), 0, "_NET_WM_STATE_HIDDEN")
		xwindow.New(xu, xproto.Window(winID)).Map()
		return nil
	case "raise":
		xwindow.New(xu, xproto.Window(winID)).Stack(xproto.StackModeAbove)
		return nil
	case "lower":
		xwindow.New(xu, xproto.Window(winID)).Stack(xproto.StackModeBelow)
		return nil
	case "geometry":
		x := getIntField(step.X, ctx.Variables, -1)
		y := getIntField(step.Y, ctx.Variables, -1)
		w := step.OffsetX
		h := step.OffsetY

		win := xwindow.New(xu, xproto.Window(winID))
		geom, err := win.Geometry()
		if err != nil {
			return fmt.Errorf("failed to get current window geometry: %w", err)
		}

		targetX := x
		if x == -1 {
			targetX = geom.X()
		}
		targetY := y
		if y == -1 {
			targetY = geom.Y()
		}
		targetW := w
		if w <= 0 {
			targetW = geom.Width()
		}
		targetH := h
		if h <= 0 {
			targetH = geom.Height()
		}

		win.MoveResize(targetX, targetY, targetW, targetH)
		return nil
	default:
		return fmt.Errorf("unsupported window mode: %q", step.Mode)
	}
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

	timeout := 10 * time.Second
	if step.Timeout != "" {
		if d, err := time.ParseDuration(step.Timeout); err == nil {
			timeout = d
		}
	}

	ctx.Logger("[Automation] Find Image: %q (timeout=%v, confidence=%.2f)", step.Image, timeout, confidence)

	deadline := time.Now().Add(timeout)
	var bestConf float64
	for {
		select {
		case <-ctx.AbortChan:
			return fmt.Errorf("execution aborted by user")
		default:
		}

		haystack, err := ctx.Target.Screenshot()
		if err != nil {
			ctx.Logger("[Automation] Error capturing screen for search: %v", err)
			time.Sleep(500 * time.Millisecond)
			continue
		}

		offsetX, offsetY := 0, 0
		if step.Region != "" {
			rx, ry, rw, rh, err := ParseRegion(step.Region, haystack.Bounds().Dx(), haystack.Bounds().Dy())
			if err == nil {
				haystack, offsetX, offsetY = CropImage(haystack, rx, ry, rw, rh)
			} else {
				ctx.Logger("[Automation] Warning parsing region: %v", err)
			}
		}

		x, y, conf, err := FindImage(haystack, needle, confidence)
		if conf > bestConf {
			bestConf = conf
		}

		if err == nil {
			// Restore coordinates to absolute screen/window bounds
			x += offsetX
			y += offsetY
			ctx.Logger("[Automation] Match found at (%d, %d) with confidence %.4f", x, y, conf)
			ctx.LastFoundX = x
			ctx.LastFoundY = y
			targetX := x + step.OffsetX
			targetY := y + step.OffsetY

			if strings.ToLower(step.Then) == "click" {
				clickStep := Step{
					Action: "click",
					X:      targetX,
					Y:      targetY,
					Button: step.Button,
				}
				return runClick(clickStep, ctx)
			} else if strings.ToLower(step.Then) == "move" {
				moveStep := Step{
					Action: "move",
					X:      targetX,
					Y:      targetY,
				}
				return runMove(moveStep, ctx)
			}
			return nil
		}

		if time.Now().After(deadline) {
			return fmt.Errorf("template image %q not found within timeout (best confidence %.4f)", step.Image, bestConf)
		}

		time.Sleep(500 * time.Millisecond)
	}
}

func runFindText(step Step, ctx *ExecContext) error {
	if step.Text == "" {
		return fmt.Errorf("missing target text in find_text step")
	}

	timeout := 10 * time.Second
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

	deadline := time.Now().Add(timeout)
	for {
		select {
		case <-ctx.AbortChan:
			return fmt.Errorf("execution aborted by user")
		default:
		}

		haystack, err := ctx.Target.Screenshot()
		if err != nil {
			ctx.Logger("[Automation] Error capturing screen for search: %v", err)
			time.Sleep(500 * time.Millisecond)
			continue
		}

		offsetX, offsetY := 0, 0
		if step.Region != "" {
			rx, ry, rw, rh, err := ParseRegion(step.Region, haystack.Bounds().Dx(), haystack.Bounds().Dy())
			if err == nil {
				haystack, offsetX, offsetY = CropImage(haystack, rx, ry, rw, rh)
			} else {
				ctx.Logger("[Automation] Warning parsing region: %v", err)
			}
		}

		x, y, conf, err := FindText(haystack, ocrAddr, ocrLang, step.Text)
		if err == nil {
			// Restore coordinates to absolute screen/window bounds
			x += offsetX
			y += offsetY
			ctx.Logger("[Automation] Text %q found at (%d, %d) with confidence %.4f", step.Text, x, y, conf)
			ctx.LastFoundX = x
			ctx.LastFoundY = y
			targetX := x + step.OffsetX
			targetY := y + step.OffsetY

			if strings.ToLower(step.Then) == "click" {
				clickStep := Step{
					Action: "click",
					X:      targetX,
					Y:      targetY,
					Button: step.Button,
				}
				return runClick(clickStep, ctx)
			} else if strings.ToLower(step.Then) == "move" {
				moveStep := Step{
					Action: "move",
					X:      targetX,
					Y:      targetY,
				}
				return runMove(moveStep, ctx)
			}
			return nil
		}

		if time.Now().After(deadline) {
			return fmt.Errorf("text %q not found within timeout: %w", step.Text, err)
		}

		time.Sleep(500 * time.Millisecond)
	}
}

func runOCR(step Step, ctx *ExecContext) error {
	targetText := step.Text
	if targetText == "" {
		targetText = step.Target
	}
	if targetText == "" {
		return fmt.Errorf("missing target text in ocr step")
	}

	timeout := 10 * time.Second
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

	deadline := time.Now().Add(timeout)
	for {
		select {
		case <-ctx.AbortChan:
			return fmt.Errorf("execution aborted by user")
		default:
		}

		haystack, err := ctx.Target.Screenshot()
		if err != nil {
			ctx.Logger("[Automation] Error capturing screen for OCR: %v", err)
			time.Sleep(500 * time.Millisecond)
			continue
		}

		offsetX, offsetY := 0, 0
		if step.Region != "" {
			rx, ry, rw, rh, err := ParseRegion(step.Region, haystack.Bounds().Dx(), haystack.Bounds().Dy())
			if err == nil {
				haystack, offsetX, offsetY = CropImage(haystack, rx, ry, rw, rh)
			} else {
				ctx.Logger("[Automation] Warning parsing region: %v", err)
			}
		}

		x, y, bounds, conf, err := FindTextWithBounds(haystack, ocrAddr, ocrLang, ocrModel, targetText)
		if err == nil {
			if step.Output != "" {
				minX, minY := bounds.Min.X, bounds.Min.Y
				maxX, maxY := bounds.Max.X, bounds.Max.Y
				rw := maxX - minX
				rh := maxY - minY

				textboxImg, _, _ := CropImage(haystack, minX, minY, rw, rh)

				outputPath := step.Output
				if !filepath.IsAbs(outputPath) && ctx.ScriptDir != "" {
					outputPath = filepath.Join(ctx.ScriptDir, outputPath)
				}
				if err := os.MkdirAll(filepath.Dir(outputPath), 0755); err != nil {
					return fmt.Errorf("failed to create directory for OCR output: %w", err)
				}
				outF, err := os.Create(outputPath)
				if err != nil {
					return fmt.Errorf("failed to create OCR output file: %w", err)
				}
				if err := png.Encode(outF, textboxImg); err != nil {
					outF.Close()
					return fmt.Errorf("failed to encode/write OCR textbox image: %w", err)
				}
				outF.Close()
				ctx.Logger("[Automation] OCR textbox image saved to %s", outputPath)
			}

			x += offsetX
			y += offsetY
			ctx.Logger("[Automation] OCR: Text %q found at (%d, %d) with confidence %.4f", targetText, x, y, conf)
			ctx.LastFoundX = x
			ctx.LastFoundY = y
			targetX := x + step.OffsetX
			targetY := y + step.OffsetY

			if strings.ToLower(step.Then) == "click" {
				clickStep := Step{
					Action: "click",
					X:      targetX,
					Y:      targetY,
					Button: step.Button,
				}
				return runClick(clickStep, ctx)
			} else if strings.ToLower(step.Then) == "move" {
				moveStep := Step{
					Action: "move",
					X:      targetX,
					Y:      targetY,
				}
				return runMove(moveStep, ctx)
			}
			return nil
		}

		if time.Now().After(deadline) {
			return fmt.Errorf("text %q not found via OCR within timeout: %w", targetText, err)
		}

		time.Sleep(500 * time.Millisecond)
	}
}
