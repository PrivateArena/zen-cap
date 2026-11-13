package automation

import (
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jezek/xgb/xproto"
	"github.com/jezek/xgbutil"
	"github.com/jezek/xgbutil/xwindow"

	"zen-cap/pkg/target"
)

// Live coverage for window_ops.go. Headless runs skip. As in pkg/target, a
// scratch display keeps the real desktop out of it:
//
//	Xvfb :99 -screen 0 1280x800x24 &
//	DISPLAY=:99 go test ./pkg/automation/ -run Live -v
//
// Under Xvfb there is no window manager, so the EWMH paths (managed-window move,
// minimize) self-skip — they need a WM that can refuse, which is the whole point
// of F3/F4.

func liveConn(t *testing.T) *xgbutil.XUtil {
	t.Helper()
	d := os.Getenv("DISPLAY")
	if d == "" {
		t.Skip("no X display")
	}
	var lastErr error
	for attempt := 0; attempt < 6; attempt++ {
		xu, err := xgbutil.NewConnDisplay(d)
		if err == nil {
			t.Cleanup(func() { xu.Conn().Close() })
			return xu
		}
		lastErr = err
		time.Sleep(50 * time.Millisecond)
	}
	t.Skipf("cannot open display %q: %v", d, lastErr)
	return nil
}

// newLiveWindow creates and maps a window. overrideRedirect=false hands it to
// the window manager, which is what the EWMH code paths need.
func newLiveWindow(t *testing.T, xu *xgbutil.XUtil, x, y, w, h int, overrideRedirect bool) uint32 {
	t.Helper()
	win, err := xwindow.Generate(xu)
	if err != nil {
		t.Fatalf("generate id: %v", err)
	}
	mask := uint32(xproto.CwBackPixel)
	values := []uint32{0xff00ff00}
	if overrideRedirect {
		mask |= xproto.CwOverrideRedirect
		values = append(values, 1)
	}
	if err := xproto.CreateWindowChecked(xu.Conn(), xu.Screen().RootDepth, win.Id,
		xproto.Window(xu.RootWin()), int16(x), int16(y), uint16(w), uint16(h), 0,
		xproto.WindowClassInputOutput, xu.Screen().RootVisual, mask, values).Check(); err != nil {
		t.Fatalf("create window: %v", err)
	}
	t.Cleanup(func() { _ = xproto.DestroyWindowChecked(xu.Conn(), win.Id).Check() })
	if err := xproto.MapWindowChecked(xu.Conn(), win.Id).Check(); err != nil {
		t.Fatalf("map window: %v", err)
	}
	return uint32(win.Id)
}

func liveTarget(t *testing.T, winID uint32) *target.X11Target {
	t.Helper()
	var lastErr error
	for attempt := 0; attempt < 6; attempt++ {
		tgt, err := target.NewX11Target(target.Config{Display: os.Getenv("DISPLAY")}, winID)
		if err == nil {
			t.Cleanup(func() { _ = tgt.Close() })
			return tgt
		}
		lastErr = err
		if !strings.Contains(err.Error(), "cannot open display") {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("new x11 target: %v", lastErr)
	return nil
}

// requireWM skips unless a window manager is running that both advertises EWMH
// and is willing to manage our test window.
func requireWM(t *testing.T, xu *xgbutil.XUtil, winID uint32) {
	t.Helper()
	if !SupportedByWM(xu, "MOVERESIZE_WINDOW") {
		t.Skip("no EWMH window manager on this display (_NET_SUPPORTED missing)")
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if isManaged(xu, winID) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Skip("window manager did not take ownership of the test window")
}

// TestLiveMoveAndResizeWindow is the F4 happy path: an unmanaged window is moved
// and resized through the same helpers a scripted `window: geometry` uses, and
// the result is read back rather than assumed.
func TestLiveMoveAndResizeWindow(t *testing.T) {
	xu := liveConn(t)
	id := newLiveWindow(t, xu, 300, 300, 320, 240, true)

	if err := MoveWindow(xu, id, 500, 400); err != nil {
		t.Fatalf("MoveWindow: %v", err)
	}
	got, err := WaitForGeometry(xu, id, WindowGeom{X: 500, Y: 400, W: 320, H: 240}, 2*time.Second)
	if err != nil {
		t.Fatalf("after MoveWindow: %v", err)
	}
	if got.X != 500 || got.Y != 400 {
		t.Fatalf("geometry after move is %s, want origin (500,400)", got)
	}

	if err := ResizeWindow(xu, id, 410, 260); err != nil {
		t.Fatalf("ResizeWindow: %v", err)
	}
	got, err = WaitForGeometry(xu, id, WindowGeom{X: 500, Y: 400, W: 410, H: 260}, 2*time.Second)
	if err != nil {
		t.Fatalf("after ResizeWindow: %v", err)
	}
	if got.W != 410 || got.H != 260 {
		t.Fatalf("geometry after resize is %s, want size 410x260", got)
	}
}

// TestLiveWaitForGeometryReportsMismatch pins the F4 contract: when the request
// is not honoured, the error names both what was asked for and what is really
// there.
func TestLiveWaitForGeometryReportsMismatch(t *testing.T) {
	xu := liveConn(t)
	id := newLiveWindow(t, xu, 300, 300, 320, 240, true)

	// Nothing moved: the window is still at (300,300)+320x240.
	want := WindowGeom{X: 640, Y: 480, W: 320, H: 240}
	got, err := WaitForGeometry(xu, id, want, 120*time.Millisecond)
	if err == nil {
		t.Fatalf("WaitForGeometry accepted %s without moving anything", got)
	}
	for _, needle := range []string{"320x240+640+480", "320x240+300+300"} {
		if !strings.Contains(err.Error(), needle) {
			t.Errorf("error %q should mention %s (requested and actual)", err.Error(), needle)
		}
	}
}

// TestLiveRunWindowGeometry runs the YAML-facing action end to end, through
// ExecuteStep, on a window-scoped target.
func TestLiveRunWindowGeometry(t *testing.T) {
	xu := liveConn(t)
	id := newLiveWindow(t, xu, 200, 200, 300, 200, true)

	ctx := &ExecContext{
		Target:    liveTarget(t, id),
		Logger:    func(string, ...interface{}) {},
		Variables: map[string]interface{}{},
		AbortChan: make(chan struct{}),
	}
	step := Step{
		Action:  "window",
		Mode:    "geometry",
		X:       520,
		Y:       360,
		OffsetX: 360, // w
		OffsetY: 220, // h
	}
	if err := ExecuteStep(step, ctx); err != nil {
		t.Fatalf("window geometry: %v", err)
	}

	got, err := CurrentGeometry(xu, id, false)
	if err != nil {
		t.Fatalf("read geometry: %v", err)
	}
	if got.X != 520 || got.Y != 360 || got.W != 360 || got.H != 220 {
		t.Errorf("window is %s, want 360x220+520+360", got)
	}
}

// TestLiveRunWindowGeometryRangeGuard proves an absurd size is refused instead of
// wrapping through the CARD16 ConfigureWindow fields into a tiny window.
func TestLiveRunWindowGeometryRangeGuard(t *testing.T) {
	xu := liveConn(t)
	id := newLiveWindow(t, xu, 200, 200, 300, 200, true)

	ctx := &ExecContext{
		Target:    liveTarget(t, id),
		Logger:    func(string, ...interface{}) {},
		Variables: map[string]interface{}{},
		AbortChan: make(chan struct{}),
	}

	w := Step{Action: "window", Mode: "geometry", OffsetX: 70000}
	if err := ExecuteStep(w, ctx); err == nil {
		t.Error("70000px wide accepted; 16-bit size guard is missing")
	} else if !strings.Contains(err.Error(), "70000") {
		t.Errorf("error should name the offending size, got %q", err.Error())
	}

	m := Step{Action: "window", Mode: "geometry", X: 99999}
	if err := ExecuteStep(m, ctx); err == nil {
		t.Error("x=99999 accepted; 16-bit position guard is missing")
	} else if !strings.Contains(err.Error(), "99999") {
		t.Errorf("error should name the offending coordinate, got %q", err.Error())
	}

	// The window must be untouched by the refused requests.
	got, err := CurrentGeometry(xu, id, false)
	if err != nil {
		t.Fatalf("read geometry: %v", err)
	}
	if got.W != 300 || got.H != 200 || got.X != 200 || got.Y != 200 {
		t.Errorf("refused requests changed the geometry to %s", got)
	}
}

// TestLiveManagedWindowMinimize covers F3 and the EWMH move path. Both need a
// real window manager, so this self-skips on a bare Xvfb — and must FAIL on a WM
// that ignores the request, which is exactly the silent-success bug it exists to
// catch.
func TestLiveManagedWindowMinimize(t *testing.T) {
	xu := liveConn(t)
	id := newLiveWindow(t, xu, 250, 250, 340, 220, false)
	requireWM(t, xu, id)

	// EWMH move of a managed window, verified through the client space.
	if err := MoveWindow(xu, id, 60, 70); err != nil {
		t.Fatalf("MoveWindow: %v", err)
	}
	if _, err := WaitForGeometry(xu, id, WindowGeom{X: 60, Y: 70}, 3*time.Second); err != nil {
		t.Errorf("EWMH move not honoured: %v", err)
	}

	if err := MinimizeWindow(xu, id); err != nil {
		t.Fatalf("MinimizeWindow: %v", err)
	}
	if err := WaitForIconic(xu, id, 3*time.Second); err != nil {
		t.Errorf("minimize did not happen (F3 regression): %v", err)
	}

	// And back again, so the desktop is left as we found it.
	if err := RestoreWindow(xu, id); err != nil {
		t.Fatalf("RestoreWindow: %v", err)
	}
	if err := WaitForViewable(xu, id, 3*time.Second); err != nil {
		t.Errorf("restore did not make the window viewable: %v", err)
	}
}

// TestLiveFullscreenEntersAndLeaves covers F3's other half: fullscreen used to
// leave a maximized window's work-area constraint in place and to report success
// either way. Both the request and its removal are verified against the WM.
// (runWindow's own `restore` clears the state before mapping; this checks the
// atom round trip directly so a WM that ignores it is caught.)
func TestLiveFullscreenEntersAndLeaves(t *testing.T) {
	xu := liveConn(t)
	id := newLiveWindow(t, xu, 300, 260, 360, 240, false)
	requireWM(t, xu, id)

	if err := RequestFullscreen(xu, id); err != nil {
		t.Fatalf("RequestFullscreen: %v", err)
	}
	if err := WaitForState(xu, id, "_NET_WM_STATE_FULLSCREEN", true, 3*time.Second); err != nil {
		t.Errorf("fullscreen did not take effect: %v", err)
	}

	if err := RequestState(xu, id, false, "_NET_WM_STATE_FULLSCREEN"); err != nil {
		t.Fatalf("leave fullscreen: %v", err)
	}
	if err := WaitForState(xu, id, "_NET_WM_STATE_FULLSCREEN", false, 3*time.Second); err != nil {
		t.Errorf("fullscreen was not cleared: %v", err)
	}
	if err := WaitForViewable(xu, id, 3*time.Second); err != nil {
		t.Errorf("window left the screen while leaving fullscreen: %v", err)
	}
}

// TestLiveWindowGeometryUsesClientSpace is the read-back half of the F4 fix:
// CurrentGeometry(decor=false) must report the CLIENT rectangle, which for a
// reparented window is neither what DecorGeometry reports (the frame) nor what
// GetGeometry reports for the client (coordinates relative to the frame).
func TestLiveWindowGeometryUsesClientSpace(t *testing.T) {
	xu := liveConn(t)

	frame := newLiveWindow(t, xu, 260, 240, 400, 320, true)
	client := newLiveWindow(t, xu, 0, 0, 300, 200, true)
	if err := xproto.ReparentWindowChecked(xu.Conn(), xproto.Window(client),
		xproto.Window(frame), 12, 24).Check(); err != nil {
		t.Fatalf("reparent: %v", err)
	}

	got, err := CurrentGeometry(xu, client, false)
	if err != nil {
		t.Fatalf("client geometry: %v", err)
	}
	if got.X != 272 || got.Y != 264 || got.W != 300 || got.H != 200 {
		t.Errorf("CurrentGeometry(client) = %s, want 300x200+272+264", got)
	}

	frameGeom, err := CurrentGeometry(xu, client, true)
	if err != nil {
		t.Fatalf("frame geometry: %v", err)
	}
	if frameGeom.X == got.X && frameGeom.Y == got.Y {
		t.Skip("no frame/client distinction on this server: nothing to discriminate")
	}
	if frameGeom.X != 260 || frameGeom.Y != 240 {
		t.Errorf("CurrentGeometry(frame) = %s, want origin (260,240)", frameGeom)
	}
}

// TestLiveRunWindowGeometryReportsMismatch drives the whole `window: geometry`
// action into a window manager that will not grant the request, and asserts the
// error names both what was asked for and what the WM actually did — the
// behaviour the API plan's error responses depend on.
//
// Skips if the WM really does honour an absurd request (then there is nothing to
// report a mismatch about).
func TestLiveRunWindowGeometryReportsMismatch(t *testing.T) {
	xu := liveConn(t)
	id := newLiveWindow(t, xu, 240, 240, 320, 220, false)
	requireWM(t, xu, id)

	ctx := &ExecContext{
		Target:    liveTarget(t, id),
		Logger:    func(string, ...interface{}) {},
		Variables: map[string]interface{}{},
		AbortChan: make(chan struct{}),
	}
	step := Step{Action: "window", Mode: "geometry", OffsetX: 40000, OffsetY: 30000}
	err := ExecuteStep(step, ctx)
	if err == nil {
		got, gerr := CurrentGeometry(xu, id, false)
		if gerr != nil {
			t.Fatalf("reading geometry after a successful action: %v", gerr)
		}
		t.Skipf("window manager accepted %s; nothing to report a mismatch about", got)
	}
	msg := err.Error()
	if !strings.Contains(msg, "40000") {
		t.Errorf("error should name the requested size, got %q", msg)
	}
	actual, gerr := CurrentGeometry(xu, id, false)
	if gerr != nil {
		t.Fatalf("reading geometry: %v", gerr)
	}
	if !strings.Contains(msg, fmt.Sprint(actual.W)) || !strings.Contains(msg, fmt.Sprint(actual.H)) {
		t.Errorf("error %q should report the actual size %s", msg, actual)
	}
}

// paintOn paints solid rectangles on a window using its own GC. Used to give a
// test window content that capture can actually find.
// restoreLivePointer puts the pointer back where the test found it.
func restoreLivePointer(t *testing.T, xu *xgbutil.XUtil) {
	r, err := xproto.QueryPointer(xu.Conn(), xproto.Window(xu.RootWin())).Reply()
	if err != nil {
		return
	}
	sx, sy := int(r.RootX), int(r.RootY)
	t.Cleanup(func() {
		_ = xproto.WarpPointerChecked(xu.Conn(), xproto.Window(xu.RootWin()),
			xproto.Window(xu.RootWin()), 0, 0, 0, 0, int16(sx), int16(sy)).Check()
	})
}

func paintOn(t *testing.T, xu *xgbutil.XUtil, win uint32, bg uint32, rects []xproto.Rectangle, fg uint32) {
	t.Helper()
	g, err := xwindow.Generate(xu)
	if err != nil {
		t.Fatalf("generate gc id: %v", err)
	}
	gc := xproto.Gcontext(g.Id)
	if err := xproto.CreateGCChecked(xu.Conn(), gc, xproto.Drawable(win), xproto.GcForeground, []uint32{bg}).Check(); err != nil {
		t.Fatalf("create gc: %v", err)
	}
	if err := xproto.PolyFillRectangleChecked(xu.Conn(), xproto.Drawable(win), gc,
		[]xproto.Rectangle{{X: 0, Y: 0, Width: 400, Height: 400}}).Check(); err != nil {
		t.Fatalf("paint background: %v", err)
	}
	if err := xproto.ChangeGCChecked(xu.Conn(), gc, xproto.GcForeground, []uint32{fg}).Check(); err != nil {
		t.Fatalf("change gc: %v", err)
	}
	if err := xproto.PolyFillRectangleChecked(xu.Conn(), xproto.Drawable(win), gc, rects).Check(); err != nil {
		t.Fatalf("paint: %v", err)
	}
	// A round trip so the pixels are certainly on the window before capturing.
	if _, err := xproto.GetGeometry(xu.Conn(), xproto.Drawable(win)).Reply(); err != nil {
		t.Fatalf("sync paint: %v", err)
	}
}

// magentaBounds returns the bounding box of the painted mark inside a captured
// image, and false when the mark is not in the image at all.
func magentaBounds(img image.Image) (image.Rectangle, bool) {
	minX, minY := 1<<30, 1<<30
	maxX, maxY := -1, -1
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			r, g, bl, _ := img.At(x, y).RGBA()
			if r>>8 > 200 && g>>8 < 60 && bl>>8 > 200 {
				if x < minX {
					minX = x
				}
				if y < minY {
					minY = y
				}
				if x > maxX {
					maxX = x
				}
				if y > maxY {
					maxY = y
				}
			}
		}
	}
	if maxX < 0 {
		return image.Rectangle{}, false
	}
	return image.Rect(minX, minY, maxX+1, maxY+1), true
}

// TestLiveCaptureAndClickShareClientSpace is the F1 acceptance test end to end,
// through the real FFmpeg capture path and the real XTEST injection path:
//
//	screenshot a window → cut a template out of it → find_image → click
//
// The window is reparented inside a frame on purpose, because that is the only
// situation where the frame origin and the client origin differ: capture grabs
// the client drawable, so a match at client (80,55) must be injected at
// root clientOrigin+(80,55). The old DecorGeometry offset put the click at the
// frame origin instead — 10px left and 20px above the mark it had just seen.
//
// The capture half is skipped on a display whose compositor will not hand
// xcbgrab the pixels of an unmanaged window; the injection half still runs,
// because it does not depend on seeing anything.
func TestLiveCaptureAndClickShareClientSpace(t *testing.T) {
	xu := liveConn(t)
	restoreLivePointer(t, xu)
	const frameX, frameY = 150, 120
	const decorX, decorY = 10, 20
	const clientX, clientY = frameX + decorX, frameY + decorY
	const markX, markY, markW, markH = 60, 40, 40, 30
	wantX, wantY := markX+markW/2, markY+markH/2

	// Keep the pointer where the user left it once the click test is done.
	frame := newLiveWindow(t, xu, frameX, frameY, 400, 300, true)
	client := newLiveWindow(t, xu, 0, 0, 300, 200, true)
	if err := xproto.ReparentWindowChecked(xu.Conn(), xproto.Window(client),
		xproto.Window(frame), decorX, decorY).Check(); err != nil {
		t.Fatalf("reparent: %v", err)
	}
	paintOn(t, xu, client, 0x00202020,
		[]xproto.Rectangle{{X: markX, Y: markY, Width: markW, Height: markH}}, 0x00ff00ff)
	xwindow.New(xu, xproto.Window(frame)).Stack(xproto.StackModeAbove)
	xwindow.New(xu, xproto.Window(client)).Stack(xproto.StackModeAbove)

	tgt := liveTarget(t, client)

	// Keep the drawn cursor out of the grabbed rectangle (xcbgrab draws the
	// pointer, and it would pollute the template).
	if err := tgt.Move(2, 2); err != nil {
		t.Fatalf("park pointer: %v", err)
	}

	ctx := &ExecContext{
		Target:    tgt,
		Logger:    func(string, ...interface{}) {},
		Variables: map[string]interface{}{},
		AbortChan: make(chan struct{}),
		ScriptDir: t.TempDir(),
	}

	if shot, err := tgt.Screenshot(); err != nil {
		t.Errorf("window capture: %v", err)
	} else {
		if b := shot.Bounds(); b.Dx() != 300 || b.Dy() != 200 {
			t.Errorf("captured %v, want the 300x200 client area", b)
		} else if w, h := tgt.ScreenSize(); w != b.Dx() || h != b.Dy() {
			// F1b: ScreenSize must describe the image find_image searches in, or
			// region shorthands resolve against the monitor instead of the window.
			t.Errorf("ScreenSize() = %dx%d but Screenshot() is %v", w, h, b)
		} else if box, ok := magentaBounds(shot); !ok {
			t.Logf("SKIP capture half: the display returned no pixels for this window "+
				"(compositor and xcbgrab disagree about window 0x%x)", client)
		} else {
			// The capture must place the mark in client coordinates.
			if gotX, gotY := box.Min.X+box.Dx()/2, box.Min.Y+box.Dy()/2; gotX != wantX || gotY != wantY {
				t.Errorf("capture put the %v mark centre at (%d,%d), want (%d,%d)", box, gotX, gotY, wantX, wantY)
			}
			tmpl := filepath.Join(t.TempDir(), "mark.png")
			cropped, _, _ := CropImage(shot, box.Min.X, box.Min.Y, box.Dx(), box.Dy())
			f, err := os.Create(tmpl)
			if err != nil {
				t.Fatalf("create template: %v", err)
			}
			if err := png.Encode(f, cropped); err != nil {
				f.Close()
				t.Fatalf("encode template: %v", err)
			}
			f.Close()

			// screenshot → template → find_image, all inside one window-scoped run.
			if err := ExecuteStep(Step{Action: "find_image", Image: tmpl, Confidence: 0.95, Timeout: "5s"}, ctx); err != nil {
				t.Fatalf("find_image on a window it just captured: %v", err)
			}
			if ctx.LastFoundX != wantX || ctx.LastFoundY != wantY {
				t.Errorf("match reported at (%d,%d), want the mark centre (%d,%d) in client space",
					ctx.LastFoundX, ctx.LastFoundY, wantX, wantY)
			}
		}
	}

	// And the click must land on the mark itself, in root coordinates.
	if err := ExecuteStep(Step{Action: "click", X: wantX, Y: wantY, Button: "left"}, ctx); err != nil {
		t.Fatalf("click at the mark: %v", err)
	}
	reply, err := xproto.QueryPointer(xu.Conn(), xproto.Window(xu.RootWin())).Reply()
	if err != nil {
		t.Fatalf("query pointer: %v", err)
	}
	if int(reply.RootX) != clientX+wantX || int(reply.RootY) != clientY+wantY {
		t.Errorf("click landed at root (%d,%d), want (%d,%d) — capture and input disagree",
			reply.RootX, reply.RootY, clientX+wantX, clientY+wantY)
	}
}

// TestGeometryMismatchNamesBothRectangles is the always-on (no X, no window
// manager) half of the F4 contract: the error text a caller gets when a geometry
// request was refused must carry what was asked for *and* what the window
// manager actually produced.
func TestGeometryMismatchNamesBothRectangles(t *testing.T) {
	want := WindowGeom{X: 100, Y: 150, W: 800, H: 600}
	actual := WindowGeom{X: 64, Y: 64, W: 1707, H: 960}
	err := geometryMismatchErr(0x1234, want, actual, time.Second)
	msg := err.Error()
	for _, needle := range []string{"800x600+100+150", "1707x960+64+64", "0x1234", "1s"} {
		if !strings.Contains(msg, needle) {
			t.Errorf("error %q is missing %q", msg, needle)
		}
	}
}

// TestRegionResolvesAgainstClientSize pins the F1b contract end of the stick
// that needs no display: with a window-scoped target ScreenSize() reports the
// client rectangle (see TestLiveCaptureAndClickShareClientSpace for the other
// end), and the region shorthands are computed from that size.
func TestRegionResolvesAgainstClientSize(t *testing.T) {
	// 400x300 client, as a window target would report it.
	x, y, w, h, err := ParseRegion("BR", 400, 300)
	if err != nil {
		t.Fatalf("ParseRegion: %v", err)
	}
	if x != 200 || y != 150 || w != 200 || h != 150 {
		t.Errorf("BR of a 400x300 client = (%d,%d,%d,%d), want (200,150,200,150)", x, y, w, h)
	}
	// Same shorthand against the *monitor* would be a different box entirely —
	// which is the bug: the image being searched is only 400x300.
	mx, my, _, _ := mustRegion(t, "BR", 1707, 960)
	if mx == x && my == y {
		t.Error("test is not discriminating: monitor and client give the same region")
	}
	if cw, ch := mustCropBounds(t, 400, 300, "BR"); cw != 200 || ch != 150 {
		t.Errorf("crop of BR = %dx%d, want 200x150", cw, ch)
	}
}

func mustRegion(t *testing.T, region string, w, h int) (int, int, int, int) {
	t.Helper()
	x, y, rw, rh, err := ParseRegion(region, w, h)
	if err != nil {
		t.Fatalf("ParseRegion(%q): %v", region, err)
	}
	return x, y, rw, rh
}

func mustCropBounds(t *testing.T, w, h int, region string) (int, int) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	x, y, rw, rh, err := ParseRegion(region, img.Bounds().Dx(), img.Bounds().Dy())
	if err != nil {
		t.Fatalf("ParseRegion: %v", err)
	}
	crop, offX, offY := CropImage(img, x, y, rw, rh)
	if offX != x || offY != y {
		t.Errorf("CropImage offset (%d,%d) != region origin (%d,%d)", offX, offY, x, y)
	}
	b := crop.Bounds()
	return b.Dx(), b.Dy()
}

// TestLiveMaximizeAndRestore covers the two window modes whose verification is
// only observable through a window manager: maximize (state atom, or the window
// visibly covering the screen) and restore (which has no single X request, so it
// is verified by the window being on screen again).
func TestLiveMaximizeAndRestore(t *testing.T) {
	xu := liveConn(t)
	id := newLiveWindow(t, xu, 200, 200, 320, 200, false)
	requireWM(t, xu, id)

	ctx := &ExecContext{
		Target:    liveTarget(t, id),
		Logger:    func(string, ...interface{}) {},
		Variables: map[string]interface{}{},
		AbortChan: make(chan struct{}),
	}

	if err := ExecuteStep(Step{Action: "window", Mode: "maximize"}, ctx); err != nil {
		t.Errorf("maximize: %v", err)
	}

	if err := ExecuteStep(Step{Action: "window", Mode: "minimize"}, ctx); err != nil {
		t.Fatalf("minimize: %v", err)
	}
	// Restore is the de-iconify path: activating the window is what a task-bar
	// click does, and is the only request every EWMH WM implements.
	if err := ExecuteStep(Step{Action: "window", Mode: "restore"}, ctx); err != nil {
		t.Errorf("restore: %v", err)
	}
	if on, why, err := onScreen(xu, id); err != nil {
		t.Fatalf("onScreen: %v", err)
	} else if !on {
		t.Errorf("window is still %s after restore", why)
	}
}
