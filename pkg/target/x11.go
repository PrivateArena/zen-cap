package target

import (
	"fmt"
	"image"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/jezek/xgb"
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
//
// Coordinate space: for a window-scoped target, (0,0) is the top-left of the
// CLIENT area — the same origin Screenshot() renders, because FFmpeg's xcbgrab
// grabs the client drawable (see pkg/av/device.go window_id). Input is
// therefore offset by the client origin too, never by the WM frame. See
// clientOrigin.
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
	t := &X11Target{
		xu:       xu,
		windowID: windowID,
		display:  display,
		w:        int(screen.WidthInPixels),
		h:        int(screen.HeightInPixels),
	}

	// A window-scoped target reports the CLIENT size, not the monitor size:
	// Screenshot() returns the client image, so ScreenSize() has to describe
	// that image or region shorthands (TL/BR/…) resolve against the wrong
	// box (F1b).
	if windowID != 0 {
		geom, err := xwindow.New(xu, xproto.Window(windowID)).Geometry()
		if err != nil {
			xu.Conn().Close()
			return nil, fmt.Errorf("x11: cannot get geometry of window 0x%x: %w", windowID, err)
		}
		t.w, t.h = geom.Width(), geom.Height()
	}
	return t, nil
}

// IsWindowTarget reports whether this target is scoped to a single client
// window rather than the whole screen.
func (t *X11Target) IsWindowTarget() bool { return t.windowID != 0 }

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

	rx, ry, err := t.rootPoint(x, y)
	if err != nil {
		return err
	}
	if err := fakeMotion(c, rx, ry); err != nil {
		return err
	}

	btn := resolveButton(button)
	if err := fakeButton(c, xproto.ButtonPress, btn, "button press"); err != nil {
		return err
	}
	return fakeButton(c, xproto.ButtonRelease, btn, "button release")
}

func (t *X11Target) ButtonPress(x, y int, button string) error {
	c := t.xu.Conn()
	if err := xtest.Init(c); err != nil {
		return fmt.Errorf("x11: xtest init: %w", err)
	}

	rx, ry, err := t.rootPoint(x, y)
	if err != nil {
		return err
	}
	if err := fakeMotion(c, rx, ry); err != nil {
		return err
	}
	return fakeButton(c, xproto.ButtonPress, resolveButton(button), "button press")
}

func (t *X11Target) ButtonRelease(x, y int, button string) error {
	c := t.xu.Conn()
	if err := xtest.Init(c); err != nil {
		return fmt.Errorf("x11: xtest init: %w", err)
	}

	rx, ry, err := t.rootPoint(x, y)
	if err != nil {
		return err
	}
	if err := fakeMotion(c, rx, ry); err != nil {
		return err
	}
	return fakeButton(c, xproto.ButtonRelease, resolveButton(button), "button release")
}

// resolveButton maps user-facing button names to X11 button numbers.
// Unknown names fall back to the left button (1); raw numbers pass through.
func resolveButton(button string) byte {
	switch strings.ToLower(button) {
	case "left", "":
		return 1
	case "middle":
		return 2
	case "right":
		return 3
	default:
		if v, err := strconv.Atoi(button); err == nil {
			return byte(v)
		}
	}
	return 1
}

func (t *X11Target) Move(x, y int) error {
	c := t.xu.Conn()
	if err := xtest.Init(c); err != nil {
		return fmt.Errorf("x11: xtest init: %w", err)
	}

	rx, ry, err := t.rootPoint(x, y)
	if err != nil {
		return err
	}
	return fakeMotion(c, rx, ry)
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
			if err := fakeKey(c, xproto.KeyPress, shiftKC, "key press (shift)"); err != nil {
				return err
			}
		}
		// On any failure, lift the modifier we are holding before reporting: an
		// error here usually means XTEST stopped working, and leaving Shift down
		// would corrupt every keystroke that follows.
		if err := fakeKey(c, xproto.KeyPress, targetKC, "key press"); err != nil {
			unwindShift(c, needShift, shiftKC)
			return err
		}
		if err := fakeKey(c, xproto.KeyRelease, targetKC, "key release"); err != nil {
			unwindShift(c, needShift, shiftKC)
			return err
		}
		if needShift {
			if err := fakeKey(c, xproto.KeyRelease, shiftKC, "key release (shift)"); err != nil {
				return err
			}
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
				if err := fakeKey(c, xproto.KeyPress, kc, "modifier press"); err != nil {
					releaseMods(c, activeMods)
					return err
				}
			}
		}
	}

	kc := byte(keycodes[0])
	if err := fakeKey(c, xproto.KeyPress, kc, "key press"); err != nil {
		releaseMods(c, activeMods)
		return err
	}
	if err := fakeKey(c, xproto.KeyRelease, kc, "key release"); err != nil {
		releaseMods(c, activeMods)
		return err
	}
	// Always unwind every modifier we grabbed, reporting the first failure.
	return releaseMods(c, activeMods)
}

// unwindShift lifts Shift after a failed keystroke, ignoring a second failure.
func unwindShift(c *xgb.Conn, needed bool, shiftKC byte) {
	if needed {
		_ = fakeKey(c, xproto.KeyRelease, shiftKC, "key release (shift)")
	}
}

// releaseMods lifts every modifier keycode in reverse order, never stopping
// half-way (a stuck modifier would corrupt all later input), and returns the
// first error seen.
func releaseMods(c *xgb.Conn, mods []byte) error {
	var firstErr error
	for i := len(mods) - 1; i >= 0; i-- {
		if err := fakeKey(c, xproto.KeyRelease, mods[i], "modifier release"); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (t *X11Target) Scroll(x, y, dx, dy int) error {
	c := t.xu.Conn()
	if err := xtest.Init(c); err != nil {
		return fmt.Errorf("x11: xtest init: %w", err)
	}
	rx, ry, err := t.rootPoint(x, y)
	if err != nil {
		return err
	}
	if err := fakeMotion(c, rx, ry); err != nil {
		return err
	}

	// M5: vertical scroll uses buttons 4 (up) / 5 (down); horizontal scroll
	// uses buttons 6 (left) / 7 (right). Previously dx was folded into the
	// vertical tick count, producing extra vertical scrolls instead.
	for i := 0; i < abs(dy); i++ {
		btn := byte(4)
		if dy > 0 {
			btn = 5
		}
		if err := fakeButton(c, xproto.ButtonPress, btn, "scroll press"); err != nil {
			return err
		}
		if err := fakeButton(c, xproto.ButtonRelease, btn, "scroll release"); err != nil {
			return err
		}
	}
	for i := 0; i < abs(dx); i++ {
		btn := byte(6)
		if dx > 0 {
			btn = 7
		}
		if err := fakeButton(c, xproto.ButtonPress, btn, "scroll press"); err != nil {
			return err
		}
		if err := fakeButton(c, xproto.ButtonRelease, btn, "scroll release"); err != nil {
			return err
		}
	}
	return nil
}

// clientOrigin returns the top-left corner of the scoped window's CLIENT area
// in root-window coordinates, which is the origin Screenshot() renders from.
//
// Capture and input must share that one space. The previous implementation
// offset input by the *decorated frame* origin (DecorGeometry), i.e. the outer
// WM chrome, while FFmpeg's xcbgrab grabs the client drawable — so every
// find_image → click in a window-scoped run landed one title-bar + border too
// far down/right (F1). TranslateCoordinates(client → root) is the X-protocol
// answer to "where does this window's (0,0) actually live", and it is correct
// whether or not a reparenting WM is running.
//
// pkg/display keeps reporting frame-relative geometry; that is right for a
// window picker, which must show what the user sees on screen.
func (t *X11Target) clientOrigin() (int, int, error) {
	if t.windowID == 0 {
		return 0, 0, nil
	}
	reply, err := xproto.TranslateCoordinates(t.xu.Conn(),
		xproto.Window(t.windowID), xproto.Window(t.xu.RootWin()), 0, 0).Reply()
	if err != nil {
		return 0, 0, fmt.Errorf("x11: cannot resolve client origin of window 0x%x: %w", t.windowID, err)
	}
	return int(reply.DstX), int(reply.DstY), nil
}

// checkPoint validates a caller-supplied coordinate before it is truncated to
// int16 and before any X request is issued (F5). Silent truncation of e.g.
// 40000 wraps around to a coordinate on the opposite edge of the screen.
func (t *X11Target) checkPoint(x, y int) error {
	if x < -32768 || x > 32767 || y < -32768 || y > 32767 {
		return fmt.Errorf("x11: coordinate (%d,%d) outside int16 range [-32768, 32767]", x, y)
	}
	if t.windowID == 0 && (x < 0 || y < 0 || x >= t.w || y >= t.h) {
		return fmt.Errorf("x11: coordinate (%d,%d) outside screen bounds %dx%d", x, y, t.w, t.h)
	}
	return nil
}

// rootPoint converts a target-space coordinate (client area for a window
// target, root otherwise) into root-window space, rejecting anything that would
// not survive int16 encoding after the offset is applied.
func (t *X11Target) rootPoint(x, y int) (int, int, error) {
	if err := t.checkPoint(x, y); err != nil {
		return 0, 0, err
	}
	ox, oy, err := t.clientOrigin()
	if err != nil {
		return 0, 0, err
	}
	rx, ry := ox+x, oy+y
	if rx < -32768 || rx > 32767 || ry < -32768 || ry > 32767 {
		return 0, 0, fmt.Errorf("x11: root coordinate (%d,%d) outside int16 range [-32768, 32767]", rx, ry)
	}
	return rx, ry, nil
}

// fakeMotion warps the pointer to absolute root coordinates.
func fakeMotion(c *xgb.Conn, x, y int) error {
	if err := xtest.FakeInputChecked(c, xproto.MotionNotify, 0, 0, 0, int16(x), int16(y), 0).Check(); err != nil {
		return fmt.Errorf("x11: fake motion to (%d,%d): %w", x, y, err)
	}
	return nil
}

// fakeButton injects one pointer button press or release.
func fakeButton(c *xgb.Conn, kind byte, button byte, what string) error {
	if err := xtest.FakeInputChecked(c, kind, button, 0, 0, 0, 0, 0).Check(); err != nil {
		return fmt.Errorf("x11: fake %s (button %d): %w", what, button, err)
	}
	return nil
}

// fakeKey injects one keyboard press or release.
func fakeKey(c *xgb.Conn, kind byte, keycode byte, what string) error {
	if err := xtest.FakeInputChecked(c, kind, keycode, 0, 0, 0, 0, 0).Check(); err != nil {
		return fmt.Errorf("x11: fake %s (keycode %d): %w", what, keycode, err)
	}
	return nil
}

// ReleaseAllButtons force-releases buttons 1-3 at the pointer's current
// position. It exists to undo a press that never got its release — an aborted
// or failed gesture would otherwise leave the whole desktop behaving as if the
// mouse were held down forever.
//
// Releasing a button that is not pressed is a no-op, so the synthetic events are
// deliberately fired blind; only a total failure to inject anything is reported.
func (t *X11Target) ReleaseAllButtons() error {
	c := t.xu.Conn()
	if err := xtest.Init(c); err != nil {
		return fmt.Errorf("x11: xtest init: %w", err)
	}

	x, y := 0, 0
	if reply, err := xproto.QueryPointer(c, xproto.Window(t.xu.RootWin())).Reply(); err == nil {
		x, y = int(reply.RootX), int(reply.RootY)
	}

	var lastErr error
	injected := 0
	for _, btn := range []byte{1, 2, 3} {
		if err := fakeButton(c, xproto.ButtonRelease, btn, "panic button release"); err != nil {
			lastErr = err
			continue
		}
		injected++
	}

	// Flush and collect any X error from the synthetic releases. xgb.Conn.Sync()
	// reports nothing, so a checked round trip is the way to learn whether the
	// server actually took the events.
	if _, err := xproto.QueryPointer(c, xproto.Window(t.xu.RootWin())).Reply(); err != nil {
		return fmt.Errorf("x11: release all buttons at (%d,%d): flush failed: %w", x, y, err)
	}
	if injected == 0 && lastErr != nil {
		return fmt.Errorf("x11: could not release any pointer button (at %d,%d): %w", x, y, lastErr)
	}
	return nil
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
