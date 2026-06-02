package automation

import (
	"context"
	"fmt"
	"image"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"zen-cap/pkg/capture"
	"zen-cap/pkg/config"
)

type ExecContext struct {
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
	default:
		return fmt.Errorf("unknown action: %s", step.Action)
	}
}

func runClick(step Step, ctx *ExecContext) error {
	btnMap := map[string]string{
		"left":   "1",
		"middle": "2",
		"right":  "3",
	}
	btn := "1"
	if b, ok := btnMap[strings.ToLower(step.Button)]; ok {
		btn = b
	}

	x := step.X
	y := step.Y
	if x == -1 && y == -1 {
		x = ctx.LastFoundX
		y = ctx.LastFoundY
	}

	var args []string
	if ctx.WindowID != 0 {
		args = []string{"mousemove", "--window", strconv.FormatUint(uint64(ctx.WindowID), 10), strconv.Itoa(x), strconv.Itoa(y), "click", "--window", strconv.FormatUint(uint64(ctx.WindowID), 10), btn}
	} else {
		args = []string{"mousemove", strconv.Itoa(x), strconv.Itoa(y), "click", btn}
	}

	ctx.Logger("[Automation] Click: x=%d, y=%d, button=%s (window=%d)", x, y, step.Button, ctx.WindowID)
	cmd := exec.Command("xdotool", args...)
	return cmd.Run()
}

func runMove(step Step, ctx *ExecContext) error {
	x := step.X
	y := step.Y
	if x == -1 && y == -1 {
		x = ctx.LastFoundX
		y = ctx.LastFoundY
	}

	var args []string
	if ctx.WindowID != 0 {
		args = []string{"mousemove", "--window", strconv.FormatUint(uint64(ctx.WindowID), 10), strconv.Itoa(x), strconv.Itoa(y)}
	} else {
		if step.Relative {
			args = []string{"mousemove_relative", strconv.Itoa(x), strconv.Itoa(y)}
		} else {
			args = []string{"mousemove", strconv.Itoa(x), strconv.Itoa(y)}
		}
	}

	ctx.Logger("[Automation] Move: x=%d, y=%d, relative=%v (window=%d)", x, y, step.Relative, ctx.WindowID)
	cmd := exec.Command("xdotool", args...)
	return cmd.Run()
}

func runType(step Step, ctx *ExecContext) error {
	delayMs := "12"
	if step.Delay != "" {
		if d, err := time.ParseDuration(step.Delay); err == nil {
			delayMs = strconv.FormatInt(d.Milliseconds(), 10)
		}
	}

	var args []string
	if ctx.WindowID != 0 {
		args = []string{"type", "--window", strconv.FormatUint(uint64(ctx.WindowID), 10), "--delay", delayMs, step.Text}
	} else {
		args = []string{"type", "--delay", delayMs, step.Text}
	}

	ctx.Logger("[Automation] Type: %q (window=%d, delay=%sms)", step.Text, ctx.WindowID, delayMs)
	cmd := exec.Command("xdotool", args...)
	return cmd.Run()
}

func runKey(step Step, ctx *ExecContext) error {
	var args []string
	if ctx.WindowID != 0 {
		args = []string{"key", "--window", strconv.FormatUint(uint64(ctx.WindowID), 10), "--clearmodifiers", step.Keys}
	} else {
		args = []string{"key", "--clearmodifiers", step.Keys}
	}

	ctx.Logger("[Automation] Key: %q (window=%d)", step.Keys, ctx.WindowID)
	cmd := exec.Command("xdotool", args...)
	return cmd.Run()
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
