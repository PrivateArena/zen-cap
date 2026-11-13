package automation

import (
	"fmt"
	"strings"
	"time"

	"github.com/jezek/xgb/xproto"
	"github.com/jezek/xgbutil"
	"github.com/jezek/xgbutil/ewmh"
	"github.com/jezek/xgbutil/icccm"
	"github.com/jezek/xgbutil/xwindow"
)

// This file is the single home for X11 window mutations. It used to be
// scattered through runWindow in actions.go, where every request was fire-and-
// forget: errors were discarded, and "the WM ignored my request" was
// indistinguishable from success. Everything here follows one rule — ask, then
// read the answer back before claiming it happened.
//
// Two coordinate spaces exist on X and they are not interchangeable:
//
//	client  — the drawable that screenshots are captured from and that clicks
//	          are addressed in (see X11Target.clientOrigin). Default here.
//	frame   — the decorated outer rectangle a reparenting WM adds. Only used by
//	          window-picker UIs (pkg/display), never for input.

// geomTolerance absorbs the rounding of a WM that honoured a request but nudged
// it by a pixel (border alignment, fractional scaling).
const geomTolerance = 2

// pollStep is how often the WaitFor* helpers re-read the server.
const pollStep = 50 * time.Millisecond

// WindowGeom is a rectangle in either the client or the frame space; which one
// is decided by the caller of CurrentGeometry, and by nothing else.
// The JSON tags are the wire format of the /automation API (a geometry renders
// as {"x":…,"y":…,"w":…,"h":…}); in-process callers ignore them.
type WindowGeom struct {
	X int `json:"x"`
	Y int `json:"y"`
	W int `json:"w"`
	H int `json:"h"`
}

func (g WindowGeom) String() string {
	return fmt.Sprintf("%dx%d%+d%+d", g.W, g.H, g.X, g.Y)
}

// sameAs compares two geometries, tolerating geomTolerance pixels per component.
//
// A non-positive W or H on the receiver means "not specified" and is skipped —
// X has no window of zero or negative size, so it cannot be a requirement. The
// origin is always compared, because (0,0) is a legal position.
func (g WindowGeom) sameAs(o WindowGeom) bool {
	if !near(g.X, o.X) || !near(g.Y, o.Y) {
		return false
	}
	if g.W > 0 && !near(g.W, o.W) {
		return false
	}
	if g.H > 0 && !near(g.H, o.H) {
		return false
	}
	return true
}

func near(a, b int) bool {
	d := a - b
	if d < 0 {
		d = -d
	}
	return d <= geomTolerance
}

// SupportedByWM reports whether the running window manager advertises the EWMH
// feature `_NET_<name>` in _NET_SUPPORTED. An absent property (no WM at all, or
// a stacking WM like a bare i3 config) reports false, which is how callers pick
// their non-EWMH fallback. xgbutil's older ewmh.Supported helper is not part of
// this revision, hence the local implementation.
func SupportedByWM(xu *xgbutil.XUtil, name string) bool {
	names, err := ewmh.SupportedGet(xu)
	if err != nil {
		return false
	}
	for _, n := range names {
		n = strings.ToUpper(n)
		if n == "_NET_"+name || n == name {
			return true
		}
	}
	return false
}

// rootOrigin returns the top-left of w in root coordinates. For a reparented
// client this is the client origin, not the frame origin.
func rootOrigin(xu *xgbutil.XUtil, w xproto.Window) (int, int, error) {
	reply, err := xproto.TranslateCoordinates(xu.Conn(), w,
		xproto.Window(xu.RootWin()), 0, 0).Reply()
	if err != nil {
		return 0, 0, fmt.Errorf("x11: cannot resolve origin of window 0x%x: %w", uint32(w), err)
	}
	return int(reply.DstX), int(reply.DstY), nil
}

// topLevelAncestor returns the direct child of the root containing w — the WM
// frame for a managed window, w itself when nothing reparented it.
func topLevelAncestor(xu *xgbutil.XUtil, w xproto.Window) (xproto.Window, error) {
	cur := w
	// Bounded walk: a sane WM nests one level (KWin sometimes two, via a
	// wrapper), nothing legitimately nests ten.
	for i := 0; i < 10; i++ {
		tree, err := xproto.QueryTree(xu.Conn(), cur).Reply()
		if err != nil {
			return 0, fmt.Errorf("x11: cannot query tree of window 0x%x: %w", uint32(cur), err)
		}
		if tree.Parent == 0 || tree.Parent == xproto.Window(xu.RootWin()) {
			return cur, nil
		}
		cur = tree.Parent
	}
	return cur, nil
}

// CurrentGeometry reads a window's rectangle. When decor is false the result is
// in client space — the space screenshots and clicks live in — which is what
// every automation caller wants. decor == true yields the WM frame, the space a
// window picker lists (mirrors pkg/display/x11.go's DecorGeometry-then-Geometry
// fallback).
func CurrentGeometry(xu *xgbutil.XUtil, id uint32, decor bool) (WindowGeom, error) {
	if decor {
		geom, err := xwindow.New(xu, xproto.Window(id)).DecorGeometry()
		if err != nil {
			geom, err = xwindow.New(xu, xproto.Window(id)).Geometry()
			if err != nil {
				return WindowGeom{}, fmt.Errorf("x11: frame geometry of window 0x%x: %w", id, err)
			}
		}
		return WindowGeom{X: geom.X(), Y: geom.Y(), W: geom.Width(), H: geom.Height()}, nil
	}

	x, y, err := rootOrigin(xu, xproto.Window(id))
	if err != nil {
		return WindowGeom{}, err
	}
	geom, err := xwindow.New(xu, xproto.Window(id)).Geometry()
	if err != nil {
		return WindowGeom{}, fmt.Errorf("x11: client geometry of window 0x%x: %w", id, err)
	}
	return WindowGeom{X: x, Y: y, W: geom.Width(), H: geom.Height()}, nil
}

// decorationDelta returns (client − frame) in root coordinates: the border plus
// title bar a reparenting WM added. Zero for unmanaged windows.
func decorationDelta(xu *xgbutil.XUtil, id uint32) (int, int, error) {
	frame, err := topLevelAncestor(xu, xproto.Window(id))
	if err != nil {
		return 0, 0, err
	}
	cx, cy, err := rootOrigin(xu, xproto.Window(id))
	if err != nil {
		return 0, 0, err
	}
	if frame == xproto.Window(id) {
		return 0, 0, nil
	}
	fx, fy, err := rootOrigin(xu, frame)
	if err != nil {
		return 0, 0, err
	}
	return cx - fx, cy - fy, nil
}

// isManaged reports whether a window manager took ownership of the window, which
// ICCCM signals by the WM_STATE property. Unmanaged windows (override-redirect:
// menus, popups, this project's test windows) must be configured directly —
// sending them EWMH requests means talking to a WM that has deliberately said no
// to them, and nothing happens.
func isManaged(xu *xgbutil.XUtil, id uint32) bool {
	_, err := icccm.WmStateGet(xu, xproto.Window(id))
	return err == nil
}

// MoveWindow places the *client* origin at root (x, y) — the same space a
// screenshot of this window uses.
//
// EWMH is the only protocol-correct way to move a managed window: a raw
// ConfigureWindow is overridden by reparenting WMs, and its x/y mean different
// things depending on whether the client sits in a frame (F4). Note that
// _NET_MOVERESIZE_WINDOW positions the *frame*, so the decoration delta is
// subtracted before sending.
func MoveWindow(xu *xgbutil.XUtil, id uint32, x, y int) error {
	// Positions are read back through TranslateCoordinates, whose reply fields
	// are INT16: beyond that, a "successful" move cannot be verified at all.
	if x < -32768 || x > 32767 || y < -32768 || y > 32767 {
		return fmt.Errorf("x11: refusing to move window 0x%x to (%d,%d): outside the 16-bit geometry range [-32768, 32767]", id, x, y)
	}

	if SupportedByWM(xu, "MOVERESIZE_WINDOW") && isManaged(xu, id) {
		dx, dy, err := decorationDelta(xu, id)
		if err != nil {
			return err
		}
		return ewmh.MoveWindow(xu, xproto.Window(id), x-dx, y-dy)
	}

	// No EWMH (bare Xvfb, override-redirect window, non-EWMH WM): configure the
	// client directly. Its x/y are parent-relative, so convert the requested
	// root position into that space — which keeps working for a window that does
	// live in a frame, as long as the frame itself does not move.
	parent, err := windowParent(xu, id)
	if err != nil {
		return err
	}
	px, py := 0, 0
	if parent != 0 && parent != xproto.Window(xu.RootWin()) {
		px, py, err = rootOrigin(xu, parent)
		if err != nil {
			return err
		}
	}
	return xproto.ConfigureWindowChecked(xu.Conn(), xproto.Window(id),
		xproto.ConfigWindowX|xproto.ConfigWindowY,
		[]uint32{uint32(int32(x - px)), uint32(int32(y - py))}).Check()
}

// ResizeWindow sets the *client* size, which is the size Screenshot() returns.
// A maximized window is restored first: a WM pins a maximized client to the work
// area and would silently veto the new size.
func ResizeWindow(xu *xgbutil.XUtil, id uint32, w, h int) error {
	// ConfigureWindow's width/height fields are CARD16; a bigger value wraps
	// around into a tiny window rather than failing.
	if w < 1 || w > 65535 || h < 1 || h > 65535 {
		return fmt.Errorf("x11: refusing to resize window 0x%x to %dx%d: outside the 16-bit geometry range [1, 65535]", id, w, h)
	}
	if err := ClearMaximized(xu, id); err != nil {
		return err
	}

	xwindow.New(xu, xproto.Window(id)).Resize(w, h)
	// ConfigureWindow is an unchecked request in xwindow; this round trip turns
	// a dead window into an error instead of a silent no-op.
	if _, err := xproto.GetGeometry(xu.Conn(), xproto.Drawable(id)).Reply(); err != nil {
		return fmt.Errorf("x11: resize window 0x%x: %w", id, err)
	}
	return nil
}

// MinimizeWindow asks the WM to iconify the window.
//
// The previous implementation set the WM-proprietary _NET_WM_STATE_HIDDEN atom,
// which WMs are entitled to ignore — and did, while the action reported success
// (F3). ICCCM's WM_CHANGE_STATE(ClientMessage) with IconicState is the
// protocol-correct request; xproto has no IconifyWindow (that is an Xlib
// convenience wrapper around this very message), so it is sent directly.
// Verification is the caller's job — see WaitForIconic.
func MinimizeWindow(xu *xgbutil.XUtil, id uint32) error {
	const iconicStateValue = 3 // ICCCM IconicState
	if err := ewmh.ClientEvent(xu, xproto.Window(id), "WM_CHANGE_STATE", iconicStateValue); err != nil {
		return fmt.Errorf("x11: iconify window 0x%x: %w", id, err)
	}
	return nil
}

// ClearMaximized drops both maximized atoms, if present.
func ClearMaximized(xu *xgbutil.XUtil, id uint32) error {
	states, err := ewmh.WmStateGet(xu, xproto.Window(id))
	if err != nil {
		// No property yet: nothing can be maximized.
		return nil
	}
	for _, s := range states {
		switch s {
		case "_NET_WM_STATE_MAXIMIZED_HORZ", "_NET_WM_STATE_MAXIMIZED_VERT":
			if err := ewmh.WmStateReq(xu, xproto.Window(id), ewmh.StateRemove, s); err != nil {
				return fmt.Errorf("x11: clear %s on window 0x%x: %w", s, id, err)
			}
		}
	}
	return nil
}

// RequestFullscreen enters fullscreen, first clearing maximized — a maximized
// window keeps its work-area constraint and the WM ends up ignoring the
// fullscreen atom (F3). Verification is the caller's job: see WaitForState.
func RequestFullscreen(xu *xgbutil.XUtil, id uint32) error {
	if err := ClearMaximized(xu, id); err != nil {
		return err
	}
	if err := ewmh.WmStateReq(xu, xproto.Window(id), ewmh.StateAdd, "_NET_WM_STATE_FULLSCREEN"); err != nil {
		return fmt.Errorf("x11: fullscreen window 0x%x: %w", id, err)
	}
	return nil
}

// RequestState sets or clears an arbitrary _NET_WM_STATE atom.
func RequestState(xu *xgbutil.XUtil, id uint32, add bool, atom string) error {
	action := ewmh.StateRemove
	if add {
		action = ewmh.StateAdd
	}
	if err := ewmh.WmStateReq(xu, xproto.Window(id), action, atom); err != nil {
		return fmt.Errorf("x11: set %s on window 0x%x: %w", atom, id, err)
	}
	return nil
}

// MapWindow maps the window, reporting the X error (a dead window) instead of
// discarding it. Only meaningful for windows no WM manages — see RestoreWindow.
func MapWindow(xu *xgbutil.XUtil, id uint32) error {
	if err := xproto.MapWindowChecked(xu.Conn(), xproto.Window(id)).Check(); err != nil {
		return fmt.Errorf("x11: map window 0x%x: %w", id, err)
	}
	return nil
}

// RestoreWindow brings a window back from iconified, which X has no single
// request for: a WM iconifies by unmapping a frame the *WM* owns, so only the WM
// can undo it. _NET_ACTIVE_WINDOW (activating a window de-iconifies it, and is
// what a task-bar click does) is therefore the primary request; ICCCM's
// WM_CHANGE_STATE(NormalState) and a direct MapWindow follow, because the first
// is what some WMs listen for and the second is the only thing that works for a
// window no WM manages. Whether the window really came back is the caller's
// problem to verify — see WaitForViewable.
func RestoreWindow(xu *xgbutil.XUtil, id uint32) error {
	const normalState = 1 // ICCCM NormalState
	var firstErr error
	if err := ewmh.ActiveWindowReq(xu, xproto.Window(id)); err != nil && firstErr == nil {
		firstErr = fmt.Errorf("x11: restore window 0x%x by activating it: %w", id, err)
	}
	if err := ewmh.ClientEvent(xu, xproto.Window(id), "WM_CHANGE_STATE", normalState); err != nil && firstErr == nil {
		firstErr = fmt.Errorf("x11: restore window 0x%x: %w", id, err)
	}
	if err := MapWindow(xu, id); err != nil && firstErr == nil {
		firstErr = err
	}
	return firstErr
}

// windowParent returns w's parent window (0 when unknown).
func windowParent(xu *xgbutil.XUtil, id uint32) (xproto.Window, error) {
	tree, err := xproto.QueryTree(xu.Conn(), xproto.Window(id)).Reply()
	if err != nil {
		return 0, fmt.Errorf("x11: cannot query tree of window 0x%x: %w", id, err)
	}
	return tree.Parent, nil
}

// onScreen reports whether the window is really visible on the root, plus a
// human-readable description of why not.
//
// Two things hide a window: an unmapped top-level ancestor — a WM iconifies by
// unmapping the *frame*, leaving the client itself mapped — and the EWMH
// _NET_WM_STATE_HIDDEN flag. X has no MapState for "iconified", so this derived
// predicate is the only portable reading of it, and it is why WaitForIconic
// asserts "gone from the screen" rather than a state code.
func onScreen(xu *xgbutil.XUtil, id uint32) (bool, string, error) {
	top, err := topLevelAncestor(xu, xproto.Window(id))
	if err != nil {
		return false, "", err
	}
	attrs, err := xproto.GetWindowAttributes(xu.Conn(), top).Reply()
	if err != nil {
		return false, "", fmt.Errorf("x11: cannot read map state of window 0x%x: %w", uint32(top), err)
	}
	name := mapStateName(attrs.MapState)
	if attrs.MapState != xproto.MapStateViewable {
		return false, name, nil
	}
	hidden, err := hasHiddenState(xu, id)
	if err != nil {
		// A WM that does not maintain _NET_WM_STATE simply has no HIDDEN flag.
		return true, name, nil
	}
	if hidden {
		return false, name + " but _NET_WM_STATE_HIDDEN", nil
	}
	return true, name, nil
}

func hasHiddenState(xu *xgbutil.XUtil, id uint32) (bool, error) {
	states, err := ewmh.WmStateGet(xu, xproto.Window(id))
	if err != nil {
		return false, err
	}
	for _, s := range states {
		if s == "_NET_WM_STATE_HIDDEN" {
			return true, nil
		}
	}
	return false, nil
}

// WaitForGeometry polls until the client rectangle matches want (±2px) or the
// budget runs out, and always returns the geometry the WM really applied so the
// caller can report it. A non-positive W/H in want means "any size" (see sameAs);
// windowGeometry below always passes the full rectangle.
func WaitForGeometry(xu *xgbutil.XUtil, id uint32, want WindowGeom, within time.Duration) (WindowGeom, error) {
	var actual WindowGeom
	deadline := time.Now().Add(within)
	for {
		g, err := CurrentGeometry(xu, id, false)
		if err != nil {
			return actual, err
		}
		actual = g
		if want.sameAs(g) {
			return g, nil
		}
		if time.Now().After(deadline) {
			return g, fmt.Errorf("x11: window 0x%x did not reach geometry %s within %v (still %s) — the window manager refused or adjusted the request",
				id, want, within, g)
		}
		time.Sleep(pollStep)
	}
}

// geometryMismatchErr names both rectangles: "the WM refused" is only useful to a
// caller that can see what the WM did instead, and to the HTTP API that has to
// report it.
func geometryMismatchErr(id uint32, want, actual WindowGeom, within time.Duration) error {
	return fmt.Errorf("x11: window 0x%x did not reach geometry %s within %v (still %s) \u2014 the window manager refused or adjusted the request",
		id, want, within, actual)
}

// WaitForState waits until an atom appears in (or disappears from)
// _NET_WM_STATE.
func WaitForState(xu *xgbutil.XUtil, id uint32, atom string, present bool, within time.Duration) error {
	deadline := time.Now().Add(within)
	var states []string
	var lastErr error
	for {
		states, lastErr = ewmh.WmStateGet(xu, xproto.Window(id))
		if lastErr == nil {
			var have bool
			for _, s := range states {
				if s == atom {
					have = true
					break
				}
			}
			if have == present {
				return nil
			}
		}
		if time.Now().After(deadline) {
			if lastErr != nil {
				return fmt.Errorf("x11: cannot read _NET_WM_STATE of window 0x%x: %w", id, lastErr)
			}
			return fmt.Errorf("x11: window 0x%x did not %s %s within %v (state is now: %s)",
				id, verb(present), atom, within, strings.Join(states, " "))
		}
		time.Sleep(pollStep)
	}
}

func verb(present bool) string {
	if present {
		return "enter"
	}
	return "leave"
}

// coverage reports the fraction of the screen the client rectangle occupies.
func coverage(xu *xgbutil.XUtil, id uint32) (float64, WindowGeom, error) {
	g, err := CurrentGeometry(xu, id, false)
	if err != nil {
		return 0, g, err
	}
	sw, sh := float64(xu.Screen().WidthInPixels), float64(xu.Screen().HeightInPixels)
	if sw <= 0 || sh <= 0 {
		return 0, g, fmt.Errorf("x11: screen has no extent")
	}
	return (float64(g.W) * float64(g.H)) / (sw * sh), g, nil
}

// WaitForStateOrCoverage verifies a size-increasing request the way a WM
// actually satisfies it: either it records the state atom it was asked for, or
// the window visibly ended up covering the screen. The second branch matters for
// tiling WMs, which are always maximal and answer such requests by doing nothing
// at all — refusing to believe them would turn a working desktop into an error.
//
// minCoverage is the fraction of the screen the window must occupy for the
// geometric answer to count.
func WaitForStateOrCoverage(xu *xgbutil.XUtil, id uint32, atom string, minCoverage float64, within time.Duration) error {
	deadline := time.Now().Add(within)
	var states []string
	var g WindowGeom
	var ratio float64
	var geomErr, stateErr error
	for {
		states, stateErr = ewmh.WmStateGet(xu, xproto.Window(id))
		if stateErr == nil {
			for _, st := range states {
				if st == atom {
					return nil
				}
			}
		}
		// A WM that does not publish _NET_WM_STATE at all is not an error here;
		// the geometric answer below is what decides.
		ratio, g, geomErr = coverage(xu, id)
		if geomErr == nil && ratio >= minCoverage {
			return nil
		}
		if time.Now().After(deadline) {
			break
		}
		time.Sleep(pollStep)
	}
	if geomErr != nil {
		return fmt.Errorf("x11: cannot measure window 0x%x while waiting for %s: %w", id, atom, geomErr)
	}
	note := ""
	if stateErr != nil {
		note = fmt.Sprintf(", and _NET_WM_STATE could not be read (%v)", stateErr)
	}
	return fmt.Errorf("x11: window 0x%x neither entered %s nor covers %.0f%% of the screen within %v (now %s, states: %q%s) \u2014 the window manager ignored the request",
		id, atom, minCoverage*100, within, g, strings.Join(states, " "), note)
}

// WaitForViewable waits until the window is really on screen.
func WaitForViewable(xu *xgbutil.XUtil, id uint32, within time.Duration) error {
	return waitForOnScreen(xu, id, true, within)
}

// WaitForIconic waits until the window has left the screen. "Iconic" is a
// request, not a state X can confirm: what a cooperative WM does about it is
// unmap the frame and/or set _NET_WM_STATE_HIDDEN, and that is what is verified.
// A WM that ignores the request therefore makes this fail loudly, which is
// exactly the point (F3).
func WaitForIconic(xu *xgbutil.XUtil, id uint32, within time.Duration) error {
	return waitForOnScreen(xu, id, false, within)
}

func waitForOnScreen(xu *xgbutil.XUtil, id uint32, want bool, within time.Duration) error {
	deadline := time.Now().Add(within)
	var state string
	var lastErr error
	for {
		on, desc, err := onScreen(xu, id)
		lastErr = err
		if err == nil {
			state = desc
			if on == want {
				return nil
			}
		}
		if time.Now().After(deadline) {
			break
		}
		time.Sleep(pollStep)
	}
	if lastErr != nil {
		return lastErr
	}
	wantText := "off screen (iconified or unmapped)"
	if want {
		wantText = "viewable"
	}
	return fmt.Errorf("x11: window 0x%x did not become %s within %v (still %s) — the window manager ignored the request",
		id, wantText, within, state)
}

func mapStateName(b byte) string {
	switch b {
	case xproto.MapStateViewable:
		return "viewable"
	case xproto.MapStateUnmapped:
		return "unmapped"
	case xproto.MapStateUnviewable:
		return "unviewable"
	}
	return fmt.Sprintf("map state %d", b)
}

// StackWindow raises or lowers a window, reporting the X error rather than
// discarding it (xwindow.Stack is unchecked and silently succeeds on a dead
// window).
func StackWindow(xu *xgbutil.XUtil, id uint32, mode byte) error {
	return xproto.ConfigureWindowChecked(xu.Conn(), xproto.Window(id),
		xproto.ConfigWindowStackMode, []uint32{uint32(mode)}).Check()
}
