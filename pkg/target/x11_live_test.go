package target

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jezek/xgb/xproto"
	"github.com/jezek/xgb/xtest"
	"github.com/jezek/xgbutil"
	"github.com/jezek/xgbutil/xwindow"
)

// These tests need a reachable X server, so they self-skip headless. They are
// the first coverage pkg/target ever had: everything they assert (client-area
// addressing, int16/range validation, button recovery) is invisible to a
// pure-Go test.
//
// Run them against a scratch display to keep your real pointer and desktop
// out of it:
//
//	Xvfb :99 -screen 0 1280x800x24 &
//	DISPLAY=:99 go test ./pkg/target/ -run Live -v

// dialDisplay opens an X connection, retrying a few times. A freshly accepted
// AF_UNIX connection is intermittently reset (reproduced with 20 sequential
// open/close cycles against a scratch Xvfb: 2/20 failures), which would skip
// live tests at random and hide real regressions.
func dialDisplay(display string) (*xgbutil.XUtil, error) {
	var lastErr error
	for attempt := 0; attempt < 6; attempt++ {
		xu, err := xgbutil.NewConnDisplay(display)
		if err == nil {
			return xu, nil
		}
		lastErr = err
		time.Sleep(50 * time.Millisecond)
	}
	return nil, fmt.Errorf("cannot open display %q after retries: %w", display, lastErr)
}

// liveTarget is NewX11Target with the same connection retry.
func liveTarget(t *testing.T, windowID uint32) *X11Target {
	t.Helper()
	display := os.Getenv("DISPLAY")
	var lastErr error
	for attempt := 0; attempt < 6; attempt++ {
		tgt, err := NewX11Target(Config{Display: display}, windowID)
		if err == nil {
			return tgt
		}
		lastErr = err
		if !strings.Contains(err.Error(), "cannot open display") {
			break // a real error (bad window, no XTEST), not a dropped socket
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("new target: %v", lastErr)
	return nil
}

func requireX(t *testing.T) *xgbutil.XUtil {
	t.Helper()
	if os.Getenv("DISPLAY") == "" {
		t.Skip("no X display")
	}
	xu, err := dialDisplay(os.Getenv("DISPLAY"))
	if err != nil {
		t.Skipf("cannot open display: %v", err)
	}
	if err := xtest.Init(xu.Conn()); err != nil {
		xu.Conn().Close()
		t.Skipf("XTEST unavailable: %v", err)
	}
	t.Cleanup(func() { xu.Conn().Close() })
	return xu
}

// newTestWindow creates a mapped, override-redirect (i.e. unmanaged) window,
// registered for destruction at cleanup.
func newTestWindow(t *testing.T, xu *xgbutil.XUtil, parent xproto.Window, x, y, w, h int) xproto.Window {
	t.Helper()
	win, err := xwindow.Generate(xu)
	if err != nil {
		t.Fatalf("generate id: %v", err)
	}
	if err := xproto.CreateWindowChecked(xu.Conn(),
		xu.Screen().RootDepth, win.Id, parent,
		int16(x), int16(y), uint16(w), uint16(h), 0,
		xproto.WindowClassInputOutput, xu.Screen().RootVisual,
		xproto.CwBackPixel|xproto.CwOverrideRedirect,
		[]uint32{0xffc0c0c0, 1}).Check(); err != nil {
		t.Fatalf("create window: %v", err)
	}
	t.Cleanup(func() { _ = xproto.DestroyWindowChecked(xu.Conn(), win.Id).Check() })
	return win.Id
}

// mapWindow maps win and waits for the server to acknowledge it.
func mapWindow(t *testing.T, xu *xgbutil.XUtil, win xproto.Window) {
	t.Helper()
	if err := xproto.MapWindowChecked(xu.Conn(), win).Check(); err != nil {
		t.Fatalf("map window 0x%x: %v", win, err)
	}
	if _, err := xproto.GetWindowAttributes(xu.Conn(), win).Reply(); err != nil {
		t.Fatalf("sync after map: %v", err)
	}
}

// pointerPos reads the pointer position in root coordinates.
func pointerPos(t *testing.T, xu *xgbutil.XUtil) (int, int) {
	t.Helper()
	reply, err := xproto.QueryPointer(xu.Conn(), xproto.Window(xu.RootWin())).Reply()
	if err != nil {
		t.Fatalf("query pointer: %v", err)
	}
	return int(reply.RootX), int(reply.RootY)
}

// restorePointer puts the pointer back where the test found it.
func restorePointer(t *testing.T, xu *xgbutil.XUtil) {
	x, y := pointerPos(t, xu)
	t.Cleanup(func() {
		_ = xproto.WarpPointerChecked(xu.Conn(), xproto.Window(xu.RootWin()),
			xproto.Window(xu.RootWin()), 0, 0, 0, 0, int16(x), int16(y)).Check()
	})
}

// buttonHeld reports whether pointer button n is logically down, according to
// the state mask returned by QueryPointer.
//
// The plan suggested xproto.QueryKeymap for this. It does not work: on both
// Xvfb's virtual pointer and a real desktop session the QueryKeymap payload
// stays all-zero for pointer buttons, while the QueryPointer state mask tracks
// them faithfully (Button1 = 0x100).
func buttonHeld(t *testing.T, xu *xgbutil.XUtil, n byte) bool {
	t.Helper()
	var mask uint16
	switch n {
	case 1:
		mask = xproto.KeyButMaskButton1
	case 2:
		mask = xproto.KeyButMaskButton2
	case 3:
		mask = xproto.KeyButMaskButton3
	default:
		t.Fatalf("no state mask for button %d", n)
	}
	reply, err := xproto.QueryPointer(xu.Conn(), xproto.Window(xu.RootWin())).Reply()
	if err != nil {
		t.Fatalf("query pointer: %v", err)
	}
	return reply.Mask&mask != 0
}

// TestLiveInputOffsetIsClientOrigin is the F1 regression test.
//
// A "fake WM" is built by hand — an override_redirect frame window with the
// client reparented inside it at (+10,+20) — because that is the only reliable
// way to make the frame origin and the client origin differ. Capture (xcbgrab
// window_id=client) renders the client; input must therefore be offset by the
// client origin (210,170), not by the decorated frame origin (200,150) that the
// previous DecorGeometry-based frame offset produced.
func TestLiveInputOffsetIsClientOrigin(t *testing.T) {
	xu := requireX(t)
	restorePointer(t, xu)

	const frameX, frameY = 200, 150
	const inFrameX, inFrameY = 10, 20
	const wantX, wantY = frameX + inFrameX, frameY + inFrameY // client origin in root space

	frame := newTestWindow(t, xu, xproto.Window(xu.RootWin()), frameX, frameY, 400, 300)
	client := newTestWindow(t, xu, xproto.Window(xu.RootWin()), 0, 0, 300, 200)
	mapWindow(t, xu, frame)
	if err := xproto.ReparentWindowChecked(xu.Conn(), client, frame, inFrameX, inFrameY).Check(); err != nil {
		t.Fatalf("reparent: %v", err)
	}
	mapWindow(t, xu, client)

	// Guard the premise of the test: the two coordinate spaces really differ.
	reply, err := xproto.TranslateCoordinates(xu.Conn(), client, xproto.Window(xu.RootWin()), 0, 0).Reply()
	if err != nil {
		t.Fatalf("translate coordinates: %v", err)
	}
	if int(reply.DstX) != wantX || int(reply.DstY) != wantY {
		t.Fatalf("test setup: client origin is (%d,%d), want (%d,%d)", reply.DstX, reply.DstY, wantX, wantY)
	}
	if geom, err := xwindow.New(xu, client).DecorGeometry(); err == nil {
		if geom.X() == wantX && geom.Y() == wantY {
			t.Skip("window manager did not reparent: frame and client origins coincide, nothing to discriminate")
		}
		t.Logf("decor(frame) origin = (%d,%d), client origin = (%d,%d) — input must use the latter",
			geom.X(), geom.Y(), wantX, wantY)
	}

	tgt := liveTarget(t, uint32(client))
	defer tgt.Close()

	if err := tgt.Move(10, 20); err != nil {
		t.Fatalf("move: %v", err)
	}
	gx, gy := pointerPos(t, xu)
	if gx != wantX+10 || gy != wantY+20 {
		t.Errorf("pointer landed at (%d,%d), want (%d,%d) — input is not client-area relative",
			gx, gy, wantX+10, wantY+20)
	}
}

// TestLiveScreenSizeIsClientSize is the F1b regression test: a window-scoped
// target must describe the image Screenshot() returns, not the monitor.
func TestLiveScreenSizeIsClientSize(t *testing.T) {
	xu := requireX(t)

	const w, h = 400, 300
	client := newTestWindow(t, xu, xproto.Window(xu.RootWin()), 60, 70, w, h)
	mapWindow(t, xu, client)

	tgt := liveTarget(t, uint32(client))
	defer tgt.Close()

	if !tgt.IsWindowTarget() {
		t.Error("IsWindowTarget() = false for a window-scoped target")
	}
	if gw, gh := tgt.ScreenSize(); gw != w || gh != h {
		t.Errorf("ScreenSize() = (%d,%d), want (%d,%d) (client size)", gw, gh, w, h)
	}

	// A full-screen target keeps the monitor dimensions.
	screen := liveTarget(t, 0)
	defer screen.Close()
	if screen.IsWindowTarget() {
		t.Error("IsWindowTarget() = true for a screen target")
	}
	if sw, sh := screen.ScreenSize(); sw < w || sh < h {
		t.Errorf("screen target reports (%d,%d), smaller than the test window", sw, sh)
	}
}

// TestLiveClickRejectsOutOfRange is the F5 regression test: coordinates are no
// longer silently wrapped through int16, and off-screen points are refused
// before any request reaches the server.
func TestLiveClickRejectsOutOfRange(t *testing.T) {
	xu := requireX(t)
	restorePointer(t, xu)

	tgt := liveTarget(t, 0)
	defer tgt.Close()

	before := func() (int, int) { return pointerPos(t, xu) }

	if err := tgt.Click(40000, 10, "left"); err == nil {
		t.Error("Click(40000,10) accepted: int16 truncation is back")
	} else if got := err.Error(); !strings.Contains(got, "int16") {
		t.Errorf("Click(40000,10) error = %q, want a mention of int16", got)
	}

	for _, pt := range [][2]int{{-5, 10}, {1 << 20, 1}} {
		bx, by := before()
		if err := tgt.Move(pt[0], pt[1]); err == nil {
			t.Errorf("Move(%d,%d) accepted, want out-of-range rejection", pt[0], pt[1])
		}
		if ax, ay := pointerPos(t, xu); ax != bx || ay != by {
			t.Errorf("rejected Move(%d,%d) still warped the pointer to (%d,%d)", pt[0], pt[1], ax, ay)
		}
	}
}

// TestLiveReleaseAllButtonsRecoversStuckPress covers the PanicReleaser primitive
// the automation API needs for its abort path. The stuck state is created by the
// test itself — no xdotool required.
//
// The press lands on a window the test owns. That matters on a live desktop: a
// press over someone else's window can leave *them* holding an active pointer
// grab, whose shadow button state no synthetic release can clear, and the test
// would then be measuring the wrong thing.
func TestLiveReleaseAllButtonsRecoversStuckPress(t *testing.T) {
	xu := requireX(t)
	restorePointer(t, xu)

	tgt := liveTarget(t, 0)
	defer tgt.Close()

	w, h := tgt.ScreenSize()
	cx, cy := w/2, h/2
	surface := newTestWindow(t, xu, xproto.Window(xu.RootWin()), cx-110, cy-80, 220, 160)
	mapWindow(t, xu, surface)
	xwindow.New(xu, surface).Stack(xproto.StackModeAbove)

	if err := tgt.ButtonPress(cx, cy, "left"); err != nil {
		t.Fatalf("button press: %v", err)
	}
	if !buttonHeld(t, xu, 1) {
		t.Fatal("synthetic ButtonPress did not leave button 1 down — nothing to recover, test is not measuring")
	}
	if gx, gy := pointerPos(t, xu); gx != cx || gy != cy {
		t.Errorf("pointer at (%d,%d), want (%d,%d)", gx, gy, cx, cy)
	}

	if err := tgt.ReleaseAllButtons(); err != nil {
		t.Fatalf("ReleaseAllButtons: %v", err)
	}
	if buttonHeld(t, xu, 1) {
		t.Error("button 1 still logically down after ReleaseAllButtons")
	}

	var _ PanicReleaser = tgt // X11Target keeps the capability
}

// TestLiveTypingIsAccepted exercises the keyboard injection path end to end.
// Every synthetic event is now a checked request, so a mistake in that plumbing
// (a wrong request shape, an uninitialised extension) surfaces here instead of
// silently dropping keystrokes in front of a user.
//
// The keystrokes are aimed at a window this test owns, so running it on a live
// desktop cannot type into the user's editor.
func TestLiveTypingIsAccepted(t *testing.T) {
	xu := requireX(t)

	own := newTestWindow(t, xu, xproto.Window(xu.RootWin()), 40, 40, 120, 90)
	mapWindow(t, xu, own)

	saved, err := xproto.GetInputFocus(xu.Conn()).Reply()
	if err != nil {
		t.Fatalf("get input focus: %v", err)
	}
	t.Cleanup(func() {
		_ = xproto.SetInputFocusChecked(xu.Conn(), xproto.InputFocusPointerRoot,
			saved.Focus, xproto.TimeCurrentTime).Check()
	})
	if err := xproto.SetInputFocusChecked(xu.Conn(), xproto.InputFocusNone,
		own, xproto.TimeCurrentTime).Check(); err != nil {
		t.Fatalf("set input focus: %v", err)
	}

	tgt := liveTarget(t, 0)
	defer tgt.Close()

	// ASCII needs no Shift and exists on every layout.
	if err := tgt.Type("abc", 0); err != nil {
		t.Errorf("Type(\"abc\"): %v", err)
	}
	// Upper-case and digits take the Shift column.
	if err := tgt.Type("A1", 0); err != nil {
		t.Errorf("Type(\"A1\"): %v", err)
	}
	// A chord: the modifier path must press and release symmetrically.
	if err := tgt.Key("ctrl-n"); err != nil {
		t.Errorf("Key(\"ctrl-n\"): %v", err)
	}
	// U+E000 is Unicode private use: no keysym in any layout can produce it, so
	// the report must say so instead of silently typing nothing.
	if err := tgt.Type("\ue000", 0); err == nil {
		t.Error("an untypeable character was accepted silently (M4 regression)")
	} else if !strings.Contains(err.Error(), "not typeable") {
		t.Errorf("unexpected error for an untypeable character: %v", err)
	}
}
