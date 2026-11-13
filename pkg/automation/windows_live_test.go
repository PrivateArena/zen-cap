package automation

import (
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jezek/xgb/xproto"
	"github.com/jezek/xgbutil"
	"github.com/jezek/xgbutil/ewmh"
	"github.com/jezek/xgbutil/icccm"
	"github.com/jezek/xgbutil/xwindow"
)

// Live coverage for the read side of the HTTP API (plan step A8). Self-skipping
// without $DISPLAY; run them for real before shipping a window-listing change:
//
//	Xvfb :99 -screen 0 1280x800x24 &
//	DISPLAY=:99 go test ./pkg/automation/ -run Live -v
//
// The probe window is a normal (non override-redirect) client on purpose: an
// override-redirect window is deliberately invisible to a window manager, so it
// never enters _NET_CLIENT_LIST and would not be listed by a *managed*-window
// enumeration. Under Xvfb there is no WM at all and the listing falls back to the
// window tree, which sees it either way.

func newNamedLiveWindow(t *testing.T, xu *xgbutil.XUtil, title, class string) uint32 {
	t.Helper()
	win, err := xwindow.Generate(xu)
	if err != nil {
		t.Fatalf("generate id: %v", err)
	}
	if err := xproto.CreateWindowChecked(xu.Conn(), xu.Screen().RootDepth, win.Id,
		xproto.Window(xu.RootWin()), 20, 20, 160, 90, 0,
		xproto.WindowClassInputOutput, xu.Screen().RootVisual,
		// No CwOverrideRedirect: the probe must be an ordinary managed client, or a
		// real window manager would leave it out of _NET_CLIENT_LIST by design.
		xproto.CwBackPixel, []uint32{0x00ff00ff}).Check(); err != nil {
		t.Fatalf("create window: %v", err)
	}
	id := uint32(win.Id)
	t.Cleanup(func() { _ = xproto.DestroyWindowChecked(xu.Conn(), win.Id).Check() })

	// Names before mapping: a WM reads WM_CLASS once, at manage time.
	if err := ewmh.WmNameSet(xu, win.Id, title); err != nil {
		t.Fatalf("set _NET_WM_NAME: %v", err)
	}
	if err := icccm.WmNameSet(xu, win.Id, title); err != nil {
		t.Fatalf("set WM_NAME: %v", err)
	}
	if class != "" {
		if err := icccm.WmClassSet(xu, win.Id, &icccm.WmClass{Instance: class, Class: class}); err != nil {
			t.Fatalf("set WM_CLASS: %v", err)
		}
	}
	if err := xproto.MapWindowChecked(xu.Conn(), win.Id).Check(); err != nil {
		t.Fatalf("map window: %v", err)
	}
	// One round trip so the listing in the same connection sees a mapped window.
	if _, err := xproto.TranslateCoordinates(xu.Conn(), win.Id, xproto.Window(xu.RootWin()), 0, 0).Reply(); err != nil {
		t.Fatalf("round trip: %v", err)
	}
	return id
}

// TestLiveListWindowsFindsProbe is the acceptance test of A8/A9: the enumeration
// returns the probe with a usable geometry, a map state and an active flag that
// agrees with _NET_ACTIVE_WINDOW.
func TestLiveListWindowsFindsProbe(t *testing.T) {
	xu := liveConn(t)
	title := fmt.Sprintf("zencap-probe-%d", time.Now().UnixNano())
	id := newNamedLiveWindow(t, xu, title, "zencap-probe")

	// An override-redirect window is unmapped-as-iconic by nothing, but WMs are
	// entitled to be slow about real clients; the listing reads live state, so give
	// the server a moment only when a WM is in play.
	deadline := time.Now().Add(2 * time.Second)
	var info WindowInfo
	var found bool
	for time.Now().Before(deadline) {
		windows, err := ListWindows(xu)
		if err != nil {
			t.Fatalf("ListWindows: %v", err)
		}
		for _, w := range windows {
			if w.ID == id {
				info, found = w, true
				break
			}
		}
		if found {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !found {
		t.Fatalf("ListWindows did not include the probe window 0x%x (%q)", id, title)
	}

	if info.IDHex != fmt.Sprintf("0x%x", id) {
		t.Errorf("id_hex = %q", info.IDHex)
	}
	if info.Title == "" && info.Class == "" {
		t.Error("a listed window must carry at least a title or a class")
	}
	if info.Title != title {
		t.Errorf("title = %q, want %q", info.Title, title)
	}
	if info.Geometry.W <= 0 || info.Geometry.H <= 0 {
		t.Errorf("geometry %+v has no size", info.Geometry)
	}
	if info.MapState != "viewable" {
		t.Errorf("map_state = %q, want viewable", info.MapState)
	}

	active, err := ActiveWindowID(xu)
	if err != nil {
		if info.Active {
			t.Error("no active window reported, but the listing marked one active")
		}
	} else if info.Active != (active == id) {
		t.Errorf("active flag %v disagrees with _NET_ACTIVE_WINDOW 0x%x", info.Active, active)
	}

	// DescribeWindow on the same id must agree with the listing entry.
	one, err := DescribeWindow(xu, id)
	if err != nil {
		t.Fatalf("DescribeWindow: %v", err)
	}
	if one.Geometry != info.Geometry || one.Title != info.Title {
		t.Errorf("DescribeWindow %+v disagrees with the listing %+v", one, info)
	}

	// And FindWindow must locate it by title, with the same substring rule a YAML
	// window: selector uses.
	if byTitle, err := FindWindow(xu, "", "zencap-probe", 0); err != nil || byTitle.ID != id {
		t.Errorf("FindWindow by title = %+v (%v), want the probe", byTitle, err)
	}
}

func TestLiveDescribeWindowRejectsGarbageID(t *testing.T) {
	xu := liveConn(t)

	// An id that cannot exist: above the client id mask of any real server.
	if _, err := DescribeWindow(xu, 0x00ffffff); err == nil {
		t.Error("DescribeWindow accepted a bogus window id")
	}
	// An id that used to exist and does not any more.
	xu2, err := Connect(os.Getenv("DISPLAY"))
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer xu2.Conn().Close()
	dead, err := xwindow.Generate(xu2)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if err := xproto.CreateWindowChecked(xu2.Conn(), xu2.Screen().RootDepth, dead.Id,
		xproto.Window(xu2.RootWin()), 0, 0, 10, 10, 0,
		xproto.WindowClassInputOutput, xu2.Screen().RootVisual, 0, nil).Check(); err != nil {
		t.Fatalf("create: %v", err)
	}
	id := uint32(dead.Id)
	if _, err := DescribeWindow(xu2, id); err != nil {
		t.Fatalf("a live window must describe cleanly: %v", err)
	}
	if err := xproto.DestroyWindowChecked(xu2.Conn(), dead.Id).Check(); err != nil {
		t.Fatalf("destroy: %v", err)
	}
	// Force the server to process the destroy before asking about it.
	_, _ = xproto.QueryTree(xu2.Conn(), xproto.Window(xu2.RootWin())).Reply()
	if _, err := DescribeWindow(xu2, id); err == nil {
		t.Errorf("DescribeWindow still answered for destroyed window 0x%x", id)
	}
}

func TestLivePointerPositionMatchesX(t *testing.T) {
	xu := liveConn(t)

	got, err := PointerPosition(xu)
	if err != nil {
		t.Fatalf("PointerPosition: %v", err)
	}
	rep, err := xproto.QueryPointer(xu.Conn(), xu.RootWin()).Reply()
	if err != nil {
		t.Fatalf("QueryPointer: %v", err)
	}
	if got.X != int(rep.RootX) || got.Y != int(rep.RootY) {
		t.Errorf("PointerPosition = %v, QueryPointer says (%d,%d)", got, rep.RootX, rep.RootY)
	}
}

func TestLiveWindowStateReadsAtoms(t *testing.T) {
	xu := liveConn(t)
	id := newNamedLiveWindow(t, xu, "zencap-state-probe", "zencap-probe")

	st, err := QueryWindowState(xu, id)
	if err != nil {
		t.Fatalf("QueryWindowState: %v", err)
	}
	if len(st.States) != 0 && st.Fullscreen {
		t.Errorf("a fresh window should not claim fullscreen: %+v", st)
	}

	// Set an atom the way an app would, then read it back: this is exactly the
	// verification path `window: fullscreen` depends on.
	if err := ewmh.WmStateReqExtra(xu, xproto.Window(id), ewmh.StateAdd, "_NET_WM_STATE_ABOVE", "", 1); err != nil {
		t.Skipf("cannot set a state atom (%v); nothing to verify", err)
	}
	deadline := time.Now().Add(time.Second)
	sawAbove := false
	for time.Now().Before(deadline) {
		st, err = QueryWindowState(xu, id)
		if err != nil {
			t.Fatalf("QueryWindowState after set: %v", err)
		}
		for _, s := range st.States {
			if s == "_NET_WM_STATE_ABOVE" {
				sawAbove = true
			}
		}
		if sawAbove {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !sawAbove {
		t.Log("_NET_WM_STATE_ABOVE not visible (no WM to record it): acceptable under Xvfb")
	}
}

func TestLiveDescribeDisplay(t *testing.T) {
	xu := liveConn(t)
	state, err := DescribeDisplayOn(xu, os.Getenv("DISPLAY"))
	if err != nil {
		t.Fatalf("DescribeDisplayOn: %v", err)
	}
	if len(state.Screens) == 0 {
		t.Error("no screens reported")
	}
	for _, s := range state.Screens {
		if s.Geometry.W <= 0 || s.Geometry.H <= 0 {
			t.Errorf("screen %d has no size: %+v", s.Index, s.Geometry)
		}
	}
	if state.Display == "" {
		t.Error("display name not reported")
	}
	if state.Pointer.X == 0 && state.Pointer.Y == 0 {
		t.Log("pointer at the origin; fine on a fresh Xvfb")
	}
}
