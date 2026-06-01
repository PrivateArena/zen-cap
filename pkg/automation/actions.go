package automation

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"zen-cap/pkg/capture"
)

type ExecContext struct {
	WindowID  uint32
	AbortChan chan struct{}
	Logger    func(string, ...interface{})
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
	case "run":
		return runCommand(step, ctx)
	case "clipboard":
		return runClipboard(step, ctx)
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

	var args []string
	if ctx.WindowID != 0 {
		args = []string{"mousemove", "--window", strconv.FormatUint(uint64(ctx.WindowID), 10), strconv.Itoa(step.X), strconv.Itoa(step.Y), "click", "--window", strconv.FormatUint(uint64(ctx.WindowID), 10), btn}
	} else {
		args = []string{"mousemove", strconv.Itoa(step.X), strconv.Itoa(step.Y), "click", btn}
	}

	ctx.Logger("[Automation] Click: x=%d, y=%d, button=%s (window=%d)", step.X, step.Y, step.Button, ctx.WindowID)
	cmd := exec.Command("xdotool", args...)
	return cmd.Run()
}

func runMove(step Step, ctx *ExecContext) error {
	var args []string
	if ctx.WindowID != 0 {
		args = []string{"mousemove", "--window", strconv.FormatUint(uint64(ctx.WindowID), 10), strconv.Itoa(step.X), strconv.Itoa(step.Y)}
	} else {
		if step.Relative {
			args = []string{"mousemove_relative", strconv.Itoa(step.X), strconv.Itoa(step.Y)}
		} else {
			args = []string{"mousemove", strconv.Itoa(step.X), strconv.Itoa(step.Y)}
		}
	}

	ctx.Logger("[Automation] Move: x=%d, y=%d, relative=%v (window=%d)", step.X, step.Y, step.Relative, ctx.WindowID)
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
