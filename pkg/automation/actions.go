package automation

import (
	"context"
	"fmt"
	"image"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/jezek/xgb/xproto"
	"github.com/jezek/xgb/xtest"
	"github.com/jezek/xgbutil"
	"github.com/jezek/xgbutil/ewmh"
	"github.com/jezek/xgbutil/keybind"
	"github.com/jezek/xgbutil/xwindow"

	"zen-cap/pkg/capture"
	"zen-cap/pkg/config"
)

type ExecContext struct {
	X          *xgbutil.XUtil
	WindowID   uint32
	AbortChan  chan struct{}
	Logger     func(string, ...interface{})
	ScriptDir  string
	Config     *config.Config
	LastFoundX int
	LastFoundY int
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
	case "find_image":
		return runFindImage(step, ctx)
	case "find_text":
		return runFindText(step, ctx)
	case "ocr":
		return runOCR(step, ctx)
	default:
		return fmt.Errorf("unknown action: %s", step.Action)
	}
}

func runClick(step Step, ctx *ExecContext) error {
	x := step.X
	y := step.Y
	if x == -1 && y == -1 {
		x = ctx.LastFoundX
		y = ctx.LastFoundY
	}

	ctx.Logger("[Automation] Click: x=%d, y=%d, button=%s (window=%d)", x, y, step.Button, ctx.WindowID)

	xu := ctx.X
	if xu == nil {
		var err error
		xu, err = xgbutil.NewConn()
		if err != nil {
			return fmt.Errorf("failed to open X connection: %w", err)
		}
		defer xu.Conn().Close()
		keybind.Initialize(xu)
	}

	return nativeClick(xu, ctx.WindowID, step.Button, x, y)
}

func runMove(step Step, ctx *ExecContext) error {
	x := step.X
	y := step.Y
	if x == -1 && y == -1 {
		x = ctx.LastFoundX
		y = ctx.LastFoundY
	}

	ctx.Logger("[Automation] Move: x=%d, y=%d, relative=%v (window=%d)", x, y, step.Relative, ctx.WindowID)

	xu := ctx.X
	if xu == nil {
		var err error
		xu, err = xgbutil.NewConn()
		if err != nil {
			return fmt.Errorf("failed to open X connection: %w", err)
		}
		defer xu.Conn().Close()
		keybind.Initialize(xu)
	}

	return nativeMove(xu, ctx.WindowID, x, y, step.Relative)
}

func runType(step Step, ctx *ExecContext) error {
	var delayMs int64 = 12
	if step.Delay != "" {
		if d, err := time.ParseDuration(step.Delay); err == nil {
			delayMs = d.Milliseconds()
		}
	}

	ctx.Logger("[Automation] Type: %q (window=%d, delay=%dms)", step.Text, ctx.WindowID, delayMs)

	xu := ctx.X
	if xu == nil {
		var err error
		xu, err = xgbutil.NewConn()
		if err != nil {
			return fmt.Errorf("failed to open X connection: %w", err)
		}
		defer xu.Conn().Close()
		keybind.Initialize(xu)
	}

	return nativeType(xu, ctx.WindowID, step.Text, delayMs)
}

func runKey(step Step, ctx *ExecContext) error {
	ctx.Logger("[Automation] Key: %q (window=%d)", step.Keys, ctx.WindowID)

	xu := ctx.X
	if xu == nil {
		var err error
		xu, err = xgbutil.NewConn()
		if err != nil {
			return fmt.Errorf("failed to open X connection: %w", err)
		}
		defer xu.Conn().Close()
		keybind.Initialize(xu)
	}

	return nativeKey(xu, ctx.WindowID, step.Keys)
}

func nativeClick(xu *xgbutil.XUtil, windowID uint32, button string, x, y int) error {
	c := xu.Conn()
	if err := xtest.Init(c); err != nil {
		return fmt.Errorf("xtest init failed: %w", err)
	}

	targetX, targetY := x, y
	if windowID != 0 {
		geom, err := xwindow.New(xu, xproto.Window(windowID)).Geometry()
		if err == nil {
			targetX = geom.X() + x
			targetY = geom.Y() + y
		}
	}

	// Move mouse
	xtest.FakeInput(c, xproto.MotionNotify, 0, 0, 0, int16(targetX), int16(targetY), 0)

	var btnDetail byte = 1
	switch strings.ToLower(button) {
	case "left":
		btnDetail = 1
	case "middle":
		btnDetail = 2
	case "right":
		btnDetail = 3
	default:
		if val, err := strconv.Atoi(button); err == nil {
			btnDetail = byte(val)
		}
	}

	xtest.FakeInput(c, xproto.ButtonPress, btnDetail, 0, 0, 0, 0, 0)
	xtest.FakeInput(c, xproto.ButtonRelease, btnDetail, 0, 0, 0, 0, 0)
	return nil
}

func nativeMove(xu *xgbutil.XUtil, windowID uint32, x, y int, relative bool) error {
	c := xu.Conn()
	if err := xtest.Init(c); err != nil {
		return fmt.Errorf("xtest init failed: %w", err)
	}

	if windowID != 0 {
		geom, err := xwindow.New(xu, xproto.Window(windowID)).Geometry()
		if err == nil {
			x = geom.X() + x
			y = geom.Y() + y
		}
		relative = false
	}

	var detail byte = 0
	if relative {
		detail = 1
	}

	xtest.FakeInput(c, xproto.MotionNotify, detail, 0, 0, int16(x), int16(y), 0)
	return nil
}

func nativeType(xu *xgbutil.XUtil, windowID uint32, text string, delayMs int64) error {
	if windowID != 0 {
		_ = ewmh.ActiveWindowReq(xu, xproto.Window(windowID))
		time.Sleep(100 * time.Millisecond)
	}

	c := xu.Conn()
	if err := xtest.Init(c); err != nil {
		return fmt.Errorf("xtest init failed: %w", err)
	}

	keyMap := keybind.KeyMapGet(xu)
	if keyMap == nil {
		return fmt.Errorf("failed to get keyboard mapping")
	}

	setup := xproto.Setup(c)
	minKC := setup.MinKeycode
	maxKC := setup.MaxKeycode
	per := keyMap.KeysymsPerKeycode

	_, shiftKCs, _ := keybind.ParseString(xu, "Shift_L")
	var shiftKC byte
	if len(shiftKCs) > 0 {
		shiftKC = byte(shiftKCs[0])
	} else {
		shiftKC = 50
	}

	for _, r := range text {
		sym := xproto.Keysym(r)

		var targetKC byte
		var col byte
		found := false
		for kc := int(minKC); kc <= int(maxKC); kc++ {
			offset := (kc - int(minKC)) * int(per)
			for c := 0; c < int(per); c++ {
				if keyMap.Keysyms[offset+c] == sym {
					targetKC = byte(kc)
					col = byte(c)
					found = true
					break
				}
			}
			if found {
				break
			}
		}

		if !found {
			continue
		}

		needShift := col%2 == 1
		if needShift {
			xtest.FakeInput(c, xproto.KeyPress, shiftKC, 0, 0, 0, 0, 0)
		}

		xtest.FakeInput(c, xproto.KeyPress, targetKC, 0, 0, 0, 0, 0)
		xtest.FakeInput(c, xproto.KeyRelease, targetKC, 0, 0, 0, 0, 0)

		if needShift {
			xtest.FakeInput(c, xproto.KeyRelease, shiftKC, 0, 0, 0, 0, 0)
		}

		if delayMs > 0 {
			time.Sleep(time.Duration(delayMs) * time.Millisecond)
		}
	}
	return nil
}

func nativeKey(xu *xgbutil.XUtil, windowID uint32, keysStr string) error {
	if windowID != 0 {
		_ = ewmh.ActiveWindowReq(xu, xproto.Window(windowID))
		time.Sleep(100 * time.Millisecond)
	}

	c := xu.Conn()
	if err := xtest.Init(c); err != nil {
		return fmt.Errorf("xtest init failed: %w", err)
	}

	norm := normalizeKeyString(keysStr)
	mods, keycodes, err := keybind.ParseString(xu, norm)
	if err != nil {
		return fmt.Errorf("failed to parse key sequence %q: %w", keysStr, err)
	}
	if len(keycodes) == 0 {
		return fmt.Errorf("no keycode found for key sequence %q", keysStr)
	}

	modMap := []struct {
		mask uint16
		name string
	}{
		{xproto.ModMaskControl, "Control_L"},
		{xproto.ModMaskShift, "Shift_L"},
		{xproto.ModMask1, "Alt_L"},
		{xproto.ModMask4, "Super_L"},
	}

	var activeModKeycodes []byte
	for _, m := range modMap {
		if mods&m.mask != 0 {
			_, kcs, err := keybind.ParseString(xu, m.name)
			if err == nil && len(kcs) > 0 {
				kc := byte(kcs[0])
				activeModKeycodes = append(activeModKeycodes, kc)
				xtest.FakeInput(c, xproto.KeyPress, kc, 0, 0, 0, 0, 0)
			}
		}
	}

	targetKC := byte(keycodes[0])
	xtest.FakeInput(c, xproto.KeyPress, targetKC, 0, 0, 0, 0, 0)
	xtest.FakeInput(c, xproto.KeyRelease, targetKC, 0, 0, 0, 0, 0)

	for i := len(activeModKeycodes) - 1; i >= 0; i-- {
		xtest.FakeInput(c, xproto.KeyRelease, activeModKeycodes[i], 0, 0, 0, 0, 0)
	}

	return nil
}

func normalizeKeyString(s string) string {
	s = strings.ToLower(s)
	s = strings.ReplaceAll(s, "+", "-")
	parts := strings.Split(s, "-")
	for i, part := range parts {
		switch part {
		case "ctrl", "control":
			parts[i] = "control"
		case "alt":
			parts[i] = "mod1"
		case "win", "super":
			parts[i] = "mod4"
		case "shift":
			parts[i] = "shift"
		}
	}
	return strings.Join(parts, "-")
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
	} else {
		return fmt.Errorf("unknown clipboard mode: %q", step.Mode)
	}

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

		capCfg := capture.CaptureConfig{
			Display:  ":0.0",
			WindowID: ctx.WindowID,
		}
		haystack, err := capture.CaptureScreen(capCfg)
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

		capCfg := capture.CaptureConfig{
			Display:  ":0.0",
			WindowID: ctx.WindowID,
		}
		haystack, err := capture.CaptureScreen(capCfg)
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

		capCfg := capture.CaptureConfig{
			Display:  ":0.0",
			WindowID: ctx.WindowID,
		}
		haystack, err := capture.CaptureScreen(capCfg)
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
