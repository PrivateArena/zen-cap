package target

// VFBTarget manages the full Xvfb + scrcpy lifecycle from Go: starts a
// virtual X display, launches scrcpy into it, and exposes an X11Target
// pointed at that display. On Close() both child processes are terminated.
// This replaces the bash script's Xvfb/scrcpy/trap boilerplate with clean
// Go context management.

import (
	"fmt"
	"image"
	"net"
	"os/exec"
	"strconv"
	"time"
)

// VFBTarget wraps an X11Target on a privately managed Xvfb display.
type VFBTarget struct {
	x11    *X11Target
	xvfb   *exec.Cmd
	scrcpy *exec.Cmd
}

// NewVFBTarget auto-selects a free display number (or uses cfg.VFBDisplay),
// starts Xvfb, launches scrcpy into it, waits for the window to appear, then
// returns a fully usable Target. No bash, no traps, no race-prone sleeps.
func NewVFBTarget(cfg Config) (*VFBTarget, error) {
	display := cfg.VFBDisplay
	if display == "" {
		var err error
		display, err = findFreeDisplay()
		if err != nil {
			return nil, fmt.Errorf("vfb: find free display: %w", err)
		}
	}

	w, h := cfg.VFBWidth, cfg.VFBHeight
	if w <= 0 {
		w = 1024
	}
	if h <= 0 {
		h = 768
	}

	// Start Xvfb
	xvfb := exec.Command("Xvfb", display,
		"-screen", "0", fmt.Sprintf("%dx%dx24", w, h),
		"-nolisten", "tcp",
	)
	if err := xvfb.Start(); err != nil {
		return nil, fmt.Errorf("vfb: start Xvfb: %w", err)
	}

	// Wait until the X socket appears (up to 5 s)
	if err := waitForDisplay(display, 5*time.Second); err != nil {
		xvfb.Process.Kill()
		return nil, fmt.Errorf("vfb: Xvfb did not start: %w", err)
	}

	t := &VFBTarget{xvfb: xvfb}

	// Launch scrcpy if a serial is provided
	if cfg.VFBSerial != "" {
		args := []string{
			"-s", cfg.VFBSerial,
			"--max-size", strconv.Itoa(w),
			"--video-bit-rate", "512K",
			"--max-fps", "5",
			"--no-audio",
			"--window-x", "0", "--window-y", "0",
			"--window-width", strconv.Itoa(w),
			"--window-height", strconv.Itoa(h),
			"--force-adb-forward",
		}
		scrcpy := exec.Command("scrcpy", args...)
		scrcpy.Env = append(scrcpy.Environ(), "DISPLAY="+display)
		if err := scrcpy.Start(); err != nil {
			xvfb.Process.Kill()
			return nil, fmt.Errorf("vfb: start scrcpy: %w", err)
		}
		t.scrcpy = scrcpy
		// Give scrcpy time to open its window (poll instead of fixed sleep)
		time.Sleep(3 * time.Second)
	}

	// Attach X11Target to the virtual display
	x11, err := NewX11Target(Config{Display: display, Scale: cfg.Scale}, 0)
	if err != nil {
		t.Close()
		return nil, fmt.Errorf("vfb: attach x11: %w", err)
	}
	t.x11 = x11
	return t, nil
}

func (t *VFBTarget) Screenshot() (image.Image, error)          { return t.x11.Screenshot() }
func (t *VFBTarget) ScreenSize() (int, int)                    { return t.x11.ScreenSize() }
func (t *VFBTarget) Click(x, y int, b string) error            { return t.x11.Click(x, y, b) }
func (t *VFBTarget) Move(x, y int) error                       { return t.x11.Move(x, y) }
func (t *VFBTarget) Type(text string, delay int64) error       { return t.x11.Type(text, delay) }
func (t *VFBTarget) Key(keys string) error                     { return t.x11.Key(keys) }
func (t *VFBTarget) Scroll(x, y, dx, dy int) error            { return t.x11.Scroll(x, y, dx, dy) }

// X11 exposes the embedded X11Target for window management operations.
func (t *VFBTarget) X11() *X11Target { return t.x11 }

// Close shuts down scrcpy and Xvfb in order, then closes the X11 connection.
func (t *VFBTarget) Close() error {
	if t.x11 != nil {
		t.x11.Close()
	}
	if t.scrcpy != nil && t.scrcpy.Process != nil {
		t.scrcpy.Process.Kill()
		t.scrcpy.Wait()
	}
	if t.xvfb != nil && t.xvfb.Process != nil {
		t.xvfb.Process.Kill()
		t.xvfb.Wait()
	}
	return nil
}

// findFreeDisplay probes display numbers starting from :99 and returns the
// first whose X socket does not yet exist.
func findFreeDisplay() (string, error) {
	for n := 99; n < 200; n++ {
		socket := fmt.Sprintf("/tmp/.X11-unix/X%d", n)
		if _, err := net.Dial("unix", socket); err != nil {
			// Socket not in use — this display is free
			return fmt.Sprintf(":%d", n), nil
		}
	}
	return "", fmt.Errorf("all displays :99–:199 are in use")
}

// waitForDisplay polls until the X unix socket at display appears or timeout.
func waitForDisplay(display string, timeout time.Duration) error {
	n := display[1:] // strip leading ':'
	socket := "/tmp/.X11-unix/X" + n
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if c, err := net.Dial("unix", socket); err == nil {
			c.Close()
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("socket %s not ready after %s", socket, timeout)
}
