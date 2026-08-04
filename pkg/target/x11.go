package target

import (
	"fmt"
	"image"
	"os"
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
)

// X11Target drives a local X11 display using XTEST for input and FFmpeg
// x11grab for capture. It is the default when no target type is configured.
type X11Target struct {
	xu       *xgbutil.XUtil
	windowID uint32
	display  string
	w, h     int
}

// NewX11Target opens an X connection to cfg.Display, falling back to the
// $DISPLAY environment variable (C5) so remote/forwarded/multi-seat sessions
// work. Returns an error if no display can be determined. windowID scopes
// capture and click-offset to that window.
func NewX11Target(cfg Config, windowID uint32) (*X11Target, error) {
	display := cfg.Display
	if display == "" {
		display = os.Getenv("DISPLAY")
	}
	if display == "" {
		return nil, fmt.Errorf("x11: no display configured and DISPLAY is not set")
	}
	xu, err := xgbutil.NewConnDisplay(display)
	if err != nil {
		return nil, fmt.Errorf("x11: cannot open display %q: %w", display, err)
	}
	keybind.Initialize(xu)

	screen := xu.Screen()
	return &X11Target{
		xu:       xu,
		windowID: windowID,
		display:  display,
		w:        int(screen.WidthInPixels),
		h:        int(screen.HeightInPixels),
	}, nil
}

// XUtil exposes the underlying connection for X11-specific operations (window
// management, ResolveWindow, etc.).
func (t *X11Target) XUtil() *xgbutil.XUtil { return t.xu }

// WinID returns the scoped window ID (0 = whole screen).
func (t *X11Target) WinID() uint32 { return t.windowID }

// Display returns the display string, e.g. ":0.0".
func (t *X11Target) Display() string { return t.display }

func (t *X11Target) Screenshot() (image.Image, error) {
	return capture.CaptureScreen(capture.CaptureConfig{
		Display:  t.display,
		WindowID: t.windowID,
	})
}

func (t *X11Target) ScreenSize() (int, int) { return t.w, t.h }

func (t *X11Target) Click(x, y int, button string) error {
	c := t.xu.Conn()
	if err := xtest.Init(c); err != nil {
		return fmt.Errorf("x11: xtest init: %w", err)
	}

	tx, ty := x, y
	if ox, oy := t.windowFrame(); ox != 0 || oy != 0 {
		tx = ox + x
		ty = oy + y
	}

	xtest.FakeInput(c, xproto.MotionNotify, 0, 0, 0, int16(tx), int16(ty), 0)

	var btn byte = 1
	switch strings.ToLower(button) {
	case "left", "":
		btn = 1
	case "middle":
		btn = 2
	case "right":
		btn = 3
	default:
		if v, err := strconv.Atoi(button); err == nil {
			btn = byte(v)
		}
	}
	xtest.FakeInput(c, xproto.ButtonPress, btn, 0, 0, 0, 0, 0)
	xtest.FakeInput(c, xproto.ButtonRelease, btn, 0, 0, 0, 0, 0)
	return nil
}

func (t *X11Target) Move(x, y int) error {
	c := t.xu.Conn()
	if err := xtest.Init(c); err != nil {
		return fmt.Errorf("x11: xtest init: %w", err)
	}

	tx, ty := x, y
	if ox, oy := t.windowFrame(); ox != 0 || oy != 0 {
		tx = ox + x
		ty = oy + y
	}
	xtest.FakeInput(c, xproto.MotionNotify, 0, 0, 0, int16(tx), int16(ty), 0)
	return nil
}

func (t *X11Target) Type(text string, delayMs int64) error {
	if t.windowID != 0 {
		_ = ewmh.ActiveWindowReq(t.xu, xproto.Window(t.windowID))
		time.Sleep(100 * time.Millisecond)
	}
	c := t.xu.Conn()
	if err := xtest.Init(c); err != nil {
		return fmt.Errorf("x11: xtest init: %w", err)
	}

	keyMap := keybind.KeyMapGet(t.xu)
	if keyMap == nil {
		return fmt.Errorf("x11: failed to get keyboard mapping")
	}
	setup := xproto.Setup(c)
	minKC := setup.MinKeycode
	maxKC := setup.MaxKeycode
	per := keyMap.KeysymsPerKeycode

	_, shiftKCs, _ := keybind.ParseString(t.xu, "Shift_L")
	var shiftKC byte = 50
	if len(shiftKCs) > 0 {
		shiftKC = byte(shiftKCs[0])
	}

	// M4: characters that need Level3/AltGr or a layout-group switch have no
	// keycode+column reachable via Shift alone. Count them instead of silently
	// dropping them so the caller knows typing was incomplete.
	dropped := 0
	for _, r := range text {
		sym := xproto.Keysym(r)
		var targetKC, col byte
		found := false
		for kc := int(minKC); kc <= int(maxKC); kc++ {
			offset := (kc - int(minKC)) * int(per)
			for ci := 0; ci < int(per); ci++ {
				if keyMap.Keysyms[offset+ci] == sym {
					targetKC = byte(kc)
					col = byte(ci)
					found = true
					break
				}
			}
			if found {
				break
			}
		}
		if !found {
			dropped++
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
	if dropped > 0 {
		return fmt.Errorf("x11: %d character(s) not typeable on this keyboard layout (need AltGr/Level3 or group switch)", dropped)
	}
	return nil
}

func (t *X11Target) Key(keys string) error {
	if t.windowID != 0 {
		_ = ewmh.ActiveWindowReq(t.xu, xproto.Window(t.windowID))
		time.Sleep(100 * time.Millisecond)
	}
	c := t.xu.Conn()
	if err := xtest.Init(c); err != nil {
		return fmt.Errorf("x11: xtest init: %w", err)
	}

	norm := normalizeX11Keys(keys)
	mods, keycodes, err := keybind.ParseString(t.xu, norm)
	if err != nil {
		return fmt.Errorf("x11: parse key %q: %w", keys, err)
	}
	if len(keycodes) == 0 {
		return fmt.Errorf("x11: no keycode for %q", keys)
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
	var activeMods []byte
	for _, m := range modMap {
		if mods&m.mask != 0 {
			_, kcs, err := keybind.ParseString(t.xu, m.name)
			if err == nil && len(kcs) > 0 {
				kc := byte(kcs[0])
				activeMods = append(activeMods, kc)
				xtest.FakeInput(c, xproto.KeyPress, kc, 0, 0, 0, 0, 0)
			}
		}
	}

	kc := byte(keycodes[0])
	xtest.FakeInput(c, xproto.KeyPress, kc, 0, 0, 0, 0, 0)
	xtest.FakeInput(c, xproto.KeyRelease, kc, 0, 0, 0, 0, 0)

	for i := len(activeMods) - 1; i >= 0; i-- {
		xtest.FakeInput(c, xproto.KeyRelease, activeMods[i], 0, 0, 0, 0, 0)
	}
	return nil
}

func (t *X11Target) Scroll(x, y, dx, dy int) error {
	c := t.xu.Conn()
	if err := xtest.Init(c); err != nil {
		return fmt.Errorf("x11: xtest init: %w", err)
	}
	tx, ty := x, y
	if ox, oy := t.windowFrame(); ox != 0 || oy != 0 {
		tx = ox + x
		ty = oy + y
	}
	xtest.FakeInput(c, xproto.MotionNotify, 0, 0, 0, int16(tx), int16(ty), 0)

	// M5: vertical scroll uses buttons 4 (up) / 5 (down); horizontal scroll
	// uses buttons 6 (left) / 7 (right). Previously dx was folded into the
	// vertical tick count, producing extra vertical scrolls instead.
	for i := 0; i < abs(dy); i++ {
		btn := byte(4)
		if dy > 0 {
			btn = 5
		}
		xtest.FakeInput(c, xproto.ButtonPress, btn, 0, 0, 0, 0, 0)
		xtest.FakeInput(c, xproto.ButtonRelease, btn, 0, 0, 0, 0, 0)
	}
	for i := 0; i < abs(dx); i++ {
		btn := byte(6)
		if dx > 0 {
			btn = 7
		}
		xtest.FakeInput(c, xproto.ButtonPress, btn, 0, 0, 0, 0, 0)
		xtest.FakeInput(c, xproto.ButtonRelease, btn, 0, 0, 0, 0, 0)
	}
	return nil
}

// windowFrame returns the decorated (outer, WM-chrome-inclusive) corner of the
// scoped window. display/x11.go reports window positions via DecorGeometry, so
// input injection must offset by the same frame (M3); otherwise clicks on
// stacking WMs with title bars land offset by the decoration thickness. Falls
// back to content Geometry if decor info is unavailable.
func (t *X11Target) windowFrame() (int, int) {
	if t.windowID == 0 {
		return 0, 0
	}
	if geom, err := xwindow.New(t.xu, xproto.Window(t.windowID)).DecorGeometry(); err == nil {
		return geom.X(), geom.Y()
	}
	if geom, err := xwindow.New(t.xu, xproto.Window(t.windowID)).Geometry(); err == nil {
		return geom.X(), geom.Y()
	}
	return 0, 0
}

func (t *X11Target) Close() error {
	t.xu.Conn().Close()
	return nil
}

// normalizeX11Keys converts user-friendly key strings to xgbutil format.
func normalizeX11Keys(s string) string {
	s = strings.ToLower(s)
	s = strings.ReplaceAll(s, "+", "-")
	parts := strings.Split(s, "-")
	for i, p := range parts {
		switch p {
		case "ctrl", "control":
			parts[i] = "control"
		case "alt":
			parts[i] = "mod1"
		case "win", "super":
			parts[i] = "mod4"
		}
	}
	return strings.Join(parts, "-")
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
