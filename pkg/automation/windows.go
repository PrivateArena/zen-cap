package automation

import (
	"fmt"
	"os"
	"strings"

	"github.com/jezek/xgb/xproto"
	"github.com/jezek/xgbutil"
	"github.com/jezek/xgbutil/ewmh"
	"github.com/jezek/xgbutil/keybind"

	"zen-cap/pkg/display"
)

// Window introspection for the read side of the HTTP API.
//
// Coordinate space: WindowInfo.Geometry is the CLIENT rectangle in root
// coordinates — the same (0,0) a window-scoped screenshot and every click of a
// window-scoped run use (Phase 0 F1). pkg/display keeps reporting the decorated
// FRAME rectangle for the picker UI, which must show what the user sees; do not
// treat the two as the same number.

// WindowInfo is one window as the API describes it: enough to pick a target, and
// enough to verify afterwards what the window manager actually did.
type WindowInfo struct {
	ID       uint32      `json:"id"`
	IDHex    string      `json:"id_hex"`
	PID      int         `json:"pid"`
	Class    string      `json:"class"`
	Instance string      `json:"instance"`
	Title    string      `json:"title"`
	Geometry WindowGeom  `json:"geometry"`
	State    WindowState `json:"state"`
	Active   bool        `json:"active"`
	MapState string      `json:"map_state"` // viewable | unviewable | unmapped
}

// WindowState is the derived, agent-friendly reading of _NET_WM_STATE plus the
// raw atom list, because an agent will want to check for a state this struct has
// no field for.
type WindowState struct {
	Fullscreen       bool     `json:"fullscreen"`
	Maximized        bool     `json:"maximized"`
	Minimized        bool     `json:"minimized"`
	DemandsAttention bool     `json:"demands_attention"`
	States           []string `json:"states"`
}

// DisplayState is the whole-screen half of GET /automation/state.
type DisplayState struct {
	Display string       `json:"display"`
	Screens []ScreenInfo `json:"screens"`
	Active  *WindowInfo  `json:"active_window"`
	Pointer Point        `json:"pointer"`
}

// ScreenInfo mirrors display.Screen with wire-format tags, so the API's shape does
// not change when an internal struct gets renamed.
type ScreenInfo struct {
	Index    int        `json:"index"`
	Name     string     `json:"name"`
	Geometry WindowGeom `json:"geometry"`
}

type Point struct {
	X int `json:"x"`
	Y int `json:"y"`
}

// Connect opens an X connection, honouring an explicit display ("" = $DISPLAY).
// It initialises the keybinding atom cache, which every key-sending path needs.
func Connect(display string) (*xgbutil.XUtil, error) {
	var xu *xgbutil.XUtil
	var err error
	if display == "" {
		xu, err = xgbutil.NewConn()
	} else {
		xu, err = xgbutil.NewConnDisplay(display)
	}
	if err != nil {
		return nil, fmt.Errorf("cannot connect to X display %q: %w", display, err)
	}
	keybind.Initialize(xu)
	return xu, nil
}

// ListWindows enumerates every viewable client window, titled or not — unlike
// the picker listing, which drops untitled windows.
//
// Enumeration itself lives in pkg/display (one ClientList walk for the whole
// program); this converts to the API shape and adds the client-space geometry.
func ListWindows(xu *xgbutil.XUtil) ([]WindowInfo, error) {
	if xu == nil {
		return nil, fmt.Errorf("X connection is nil")
	}
	mgr := display.NewX11DisplayManagerWith(xu)
	details, err := mgr.GetWindowsDetailed()
	if err != nil {
		return nil, err
	}

	out := make([]WindowInfo, 0, len(details))
	for _, d := range details {
		out = append(out, windowInfoFrom(xu, d))
	}
	return out, nil
}

// DescribeWindow reports one window by id, whatever its map state — so an agent
// can read back a window it just minimized.
func DescribeWindow(xu *xgbutil.XUtil, id uint32) (WindowInfo, error) {
	if xu == nil {
		return WindowInfo{}, fmt.Errorf("X connection is nil")
	}
	mgr := display.NewX11DisplayManagerWith(xu)
	d, err := mgr.GetWindowDetailed(id)
	if err != nil {
		return WindowInfo{}, err
	}
	return windowInfoFrom(xu, d), nil
}

// FindWindow picks the first window matching class/title/pid, using the same
// substring, case-insensitive rule as a YAML `window:` selector, so a script and
// an HTTP request with the same selector name the same window.
func FindWindow(xu *xgbutil.XUtil, class, title string, pid int) (WindowInfo, error) {
	windows, err := ListWindows(xu)
	if err != nil {
		return WindowInfo{}, err
	}
	sel := &WindowTarget{Title: title, Class: class}
	for _, w := range windows {
		if pid > 0 && w.PID != pid {
			continue
		}
		if pid > 0 && title == "" && class == "" {
			return w, nil
		}
		if MatchWindowTitleClass(w, sel) {
			return w, nil
		}
	}
	return WindowInfo{}, fmt.Errorf("no window matching pid=%d class=%q title=%q", pid, class, title)
}

// MatchWindowTitleClass is the predicate behind both ResolveWindow and the HTTP
// ?class=/?title= filters: case-insensitive substring, in ClientList order.
// Several windows often match; the caller is expected to disambiguate by id,
// which is why GET /automation/windows returns all of them.
func MatchWindowTitleClass(w WindowInfo, t *WindowTarget) bool {
	if t == nil {
		return false
	}
	// Class and title are alternatives, not a conjunction: a request that names one
	// string for both ({"window":"firefox"}) must match either. ResolveWindow, which
	// takes a YAML selector with one field set, keeps its title-then-class priority.
	if t.Class != "" {
		cl := strings.ToLower(w.Class)
		in := strings.ToLower(w.Instance)
		want := strings.ToLower(t.Class)
		if strings.Contains(cl, want) || strings.Contains(in, want) {
			return true
		}
	}
	if t.Title != "" {
		return strings.Contains(strings.ToLower(w.Title), strings.ToLower(t.Title))
	}
	return false
}

// QueryWindowState reads _NET_WM_STATE and interprets the atoms that matter.
func QueryWindowState(xu *xgbutil.XUtil, id uint32) (WindowState, error) {
	states, err := ewmh.WmStateGet(xu, xproto.Window(id))
	if err != nil {
		// No property at all is a legal answer (unmanaged or brand new window),
		// not a failure: report an empty state so callers can still render it.
		if strings.Contains(err.Error(), "WINDOW_MANAGER") || strings.Contains(err.Error(), "does not exist") {
			return WindowState{}, nil
		}
		// xgbutil returns the raw xproto error for an absent property.
		if _, perr := xproto.GetWindowAttributes(xu.Conn(), xproto.Window(id)).Reply(); perr != nil {
			return WindowState{}, fmt.Errorf("window 0x%x does not exist: %w", id, perr)
		}
		return WindowState{}, nil
	}

	st := WindowState{States: states}
	for _, name := range states {
		switch name {
		case "_NET_WM_STATE_FULLSCREEN":
			st.Fullscreen = true
		case "_NET_WM_STATE_HIDDEN":
			st.Minimized = true
		case "_NET_WM_STATE_MAXIMIZED_HORZ", "_NET_WM_STATE_MAXIMIZED_VERT":
			st.Maximized = true
		case "_NET_WM_STATE-demands-attention", "_NET_WM_STATE_DEMANDS_ATTENTION":
			st.DemandsAttention = true
		}
	}
	// A maximized window is only really maximized if both halves are set; report
	// the useful answer rather than making every caller count atoms.
	if st.Maximized {
		h, v := false, false
		for _, name := range states {
			switch name {
			case "_NET_WM_STATE_MAXIMIZED_HORZ":
				h = true
			case "_NET_WM_STATE_MAXIMIZED_VERT":
				v = true
			}
		}
		st.Maximized = h && v
	}
	return st, nil
}

// ActiveWindowID returns _NET_ACTIVE_WINDOW (0 when no WM is running).
func ActiveWindowID(xu *xgbutil.XUtil) (uint32, error) {
	id, err := ewmh.ActiveWindowGet(xu)
	if err != nil {
		return 0, err
	}
	return uint32(id), nil
}

// PointerPosition returns the pointer's root coordinates. It is the cheapest way
// for an agent to see where the synthetic warps actually left the cursor.
func PointerPosition(xu *xgbutil.XUtil) (Point, error) {
	p, _, err := PointerState(xu)
	return p, err
}

// PointerState also reports which buttons are currently down, read from the
// pointer's key/modifier mask — the honest answer to "did a run leave a button
// stuck?", which is otherwise unknowable from the outside.
func PointerState(xu *xgbutil.XUtil) (Point, []string, error) {
	rep, err := xproto.QueryPointer(xu.Conn(), xu.RootWin()).Reply()
	if err != nil {
		return Point{}, nil, fmt.Errorf("query pointer: %w", err)
	}
	// The button bits start at 1<<8: the low bits of a pointer mask are the
	// modifier locks (Shift, CapsLock, Control, Mod1, Mod2), and reading those as
	// "buttons held down" would tell an agent the mouse is stuck whenever the user
	// has NumLock on.
	const button1Mask = 1 << 8
	buttons := []string{}
	for i, name := range []string{"1", "2", "3", "4", "5", "6", "7", "8", "9"} {
		if rep.Mask&(button1Mask<<uint(i)) != 0 {
			buttons = append(buttons, name)
		}
	}
	return Point{X: int(rep.RootX), Y: int(rep.RootY)}, buttons, nil
}

// DescribeDisplay is the snapshot behind GET /automation/state.
func DescribeDisplay(displayName string) (DisplayState, error) {
	xu, err := Connect(displayName)
	if err != nil {
		return DisplayState{}, err
	}
	defer xu.Conn().Close()
	return DescribeDisplayOn(xu, displayName)
}

// DescribeDisplayOn is DescribeDisplay over a connection the caller owns, so a
// per-request state read can ride the target's existing connection (and so a
// test can retry a flaky socket without retrying the whole snapshot).
func DescribeDisplayOn(xu *xgbutil.XUtil, displayName string) (DisplayState, error) {
	mgr := display.NewX11DisplayManagerWith(xu)
	screens, err := mgr.GetScreens()
	if err != nil {
		return DisplayState{}, err
	}
	if displayName == "" {
		displayName = os.Getenv("DISPLAY")
	}
	out := DisplayState{Display: displayName}
	for _, s := range screens {
		out.Screens = append(out.Screens, ScreenInfo{
			Index:    s.Index,
			Name:     s.Name,
			Geometry: WindowGeom{X: s.Geometry.X, Y: s.Geometry.Y, W: s.Geometry.Width, H: s.Geometry.Height},
		})
	}
	if id, err := ActiveWindowID(xu); err == nil && id != 0 {
		info, err := DescribeWindow(xu, id)
		if err == nil {
			out.Active = &info
		}
	}
	if p, err := PointerPosition(xu); err == nil {
		out.Pointer = p
	}
	return out, nil
}

// windowInfoFrom turns the pkg/display enumeration into the API shape, replacing
// the frame geometry with the client geometry the automation space uses.
func windowInfoFrom(xu *xgbutil.XUtil, d display.WindowDetail) WindowInfo {
	info := WindowInfo{
		ID:       d.ID,
		IDHex:    fmt.Sprintf("0x%x", d.ID),
		PID:      d.PID,
		Class:    d.Class,
		Instance: d.Instance,
		Title:    d.Title,
		Active:   d.Active,
		MapState: d.MapState,
		Geometry: WindowGeom{X: d.Geometry.X, Y: d.Geometry.Y, W: d.Geometry.Width, H: d.Geometry.Height},
	}
	if g, err := CurrentGeometry(xu, d.ID, false); err == nil {
		info.Geometry = g
	}
	if st, err := QueryWindowState(xu, d.ID); err == nil {
		info.State = st
	}
	return info
}
