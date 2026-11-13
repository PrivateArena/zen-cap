package display

import (
	"fmt"

	"github.com/jezek/xgb/xinerama"
	"github.com/jezek/xgb/xproto"
	"github.com/jezek/xgbutil"
	"github.com/jezek/xgbutil/ewmh"
	"github.com/jezek/xgbutil/icccm"
	"github.com/jezek/xgbutil/xwindow"
)

type X11DisplayManager struct {
	xu *xgbutil.XUtil
	// ownsConn is false for a manager built over a caller's connection, and
	// Close must then leave that connection alone.
	ownsConn bool
}

// NewX11DisplayManagerWith wraps a connection the caller already owns, so an
// existing X client (the automation target's connection) can use this package's
// enumeration instead of opening a second connection — or, worse, copying the
// ClientList walk. Close on this manager is a no-op; the caller decides when the
// connection dies.
func NewX11DisplayManagerWith(xu *xgbutil.XUtil) *X11DisplayManager {
	return &X11DisplayManager{xu: xu}
}

// NewX11DisplayManager connects to $DISPLAY. It stays the constructor every
// existing caller (magnifier, capture, the CLI listing) uses.
func NewX11DisplayManager() (*X11DisplayManager, error) {
	return NewX11DisplayManagerOn("")
}

// NewX11DisplayManagerOn connects to an explicit display string ("" = $DISPLAY),
// which is how the HTTP API answers a request that names its own display
// without disturbing the daemon's own connection.
func NewX11DisplayManagerOn(display string) (*X11DisplayManager, error) {
	var xu *xgbutil.XUtil
	var err error
	if display == "" {
		xu, err = xgbutil.NewConn()
	} else {
		xu, err = xgbutil.NewConnDisplay(display)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to connect to X server: %w", err)
	}
	return &X11DisplayManager{xu: xu, ownsConn: true}, nil
}

// XUtil exposes the connection so callers that need a raw X round trip (the
// automation window introspection) can share this manager's connection instead
// of opening a second one.
func (m *X11DisplayManager) XUtil() *xgbutil.XUtil { return m.xu }

func (m *X11DisplayManager) Close() {
	if m.xu != nil && m.ownsConn {
		m.xu.Conn().Close()
	}
}

func (m *X11DisplayManager) GetScreens() ([]Screen, error) {
	c := m.xu.Conn()

	// Initialize Xinerama extension if available
	err := xinerama.Init(c)
	if err == nil {
		active, err := xinerama.IsActive(c).Reply()
		if err == nil && active.State > 0 {
			screensReply, err := xinerama.QueryScreens(c).Reply()
			if err == nil {
				var screens []Screen
				for i, s := range screensReply.ScreenInfo {
					screens = append(screens, Screen{
						Index: i,
						Name:  fmt.Sprintf("Screen %d", i),
						Geometry: Geometry{
							X:      int(s.XOrg),
							Y:      int(s.YOrg),
							Width:  int(s.Width),
							Height: int(s.Height),
						},
					})
				}
				if len(screens) > 0 {
					return screens, nil
				}
			}
		}
	}

	// Fallback to the default screen provided by XUtil
	screen := m.xu.Screen()
	if screen == nil {
		return nil, fmt.Errorf("failed to get default screen info")
	}
	return []Screen{
		{
			Index: 0,
			Name:  "Primary",
			Geometry: Geometry{
				X:      0,
				Y:      0,
				Width:  int(screen.WidthInPixels),
				Height: int(screen.HeightInPixels),
			},
		},
	}, nil
}

// GetWindows lists managed windows for the window-picker UIs.
//
// The geometry reported here is deliberately FRAME-relative (DecorGeometry): a
// picker must show the rectangle the user actually sees, title bar included.
// That is not the space automation input lives in — pkg/target.X11Target offsets
// clicks by the CLIENT origin (TranslateCoordinates), which is also the origin
// Screenshot() captures from. Do not feed these coordinates to Click/Move.
//
// It is GetWindowsDetailed with the untitled windows filtered out, because that
// is exactly what a picker wants: the two views cannot drift apart.
func (m *X11DisplayManager) GetWindows() ([]Window, error) {
	detailed, err := m.GetWindowsDetailed()
	if err != nil {
		return nil, err
	}
	var windows []Window
	for _, d := range detailed {
		if d.Title == "" {
			continue // Skip windows without titles to avoid cluttering the list
		}
		windows = append(windows, d.Window)
	}
	return windows, nil
}

// GetWindowsDetailed is the one window enumeration in this package: every
// viewable client, titled or not, with the pid, the ICCCM instance name, the
// map state, the raw _NET_WM_STATE atoms and the active-window flag.
//
// A minimized window is unmapped as far as X is concerned, so it is absent here;
// use GetWindowDetailed (or the id-based lookup) to inspect one.
func (m *X11DisplayManager) GetWindowsDetailed() ([]WindowDetail, error) {
	clientIDs, err := ewmh.ClientListGet(m.xu)
	if err != nil {
		// Fallback: query tree of the root window
		tree, err := xproto.QueryTree(m.xu.Conn(), m.xu.RootWin()).Reply()
		if err != nil {
			return nil, fmt.Errorf("failed to query window tree: %w", err)
		}
		clientIDs = tree.Children
	}

	activeID, activeErr := ewmh.ActiveWindowGet(m.xu)

	var windows []WindowDetail
	for _, winID := range clientIDs {
		attrs, err := xproto.GetWindowAttributes(m.xu.Conn(), winID).Reply()
		if err != nil || attrs.MapState != xproto.MapStateViewable {
			continue
		}

		detail, err := m.describe(winID, activeErr == nil && winID == activeID)
		if err != nil {
			continue
		}
		windows = append(windows, detail)
	}

	return windows, nil
}

// describe gathers everything knowable about one client window. It is the single
// place that talks to X for window facts, so GetWindowDetailed and the listing
// cannot disagree.
func (m *X11DisplayManager) describe(winID xproto.Window, active bool) (WindowDetail, error) {
	attrs, err := xproto.GetWindowAttributes(m.xu.Conn(), winID).Reply()
	if err != nil {
		return WindowDetail{}, fmt.Errorf("failed to get attributes of window 0x%x: %w", uint32(winID), err)
	}

	geom, err := xwindow.New(m.xu, winID).DecorGeometry()
	if err != nil {
		// Try normal geometry if decor geometry fails
		geom, err = xwindow.New(m.xu, winID).Geometry()
		if err != nil {
			return WindowDetail{}, fmt.Errorf("failed to get geometry of window 0x%x: %w", uint32(winID), err)
		}
	}

	title, terr := ewmh.WmNameGet(m.xu, winID)
	if terr != nil || title == "" {
		title, terr = icccm.WmNameGet(m.xu, winID)
		if terr != nil {
			title = ""
		}
	}

	d := WindowDetail{
		Window: Window{
			ID:    uint32(winID),
			Title: title,
			Geometry: Geometry{
				X:      geom.X(),
				Y:      geom.Y(),
				Width:  geom.Width(),
				Height: geom.Height(),
			},
		},
		Active:   active,
		MapState: mapStateName(attrs.MapState),
	}

	if classInfo, err := icccm.WmClassGet(m.xu, winID); err == nil && classInfo != nil {
		d.Class = classInfo.Class
		d.Instance = classInfo.Instance
	}
	if pid, err := ewmh.WmPidGet(m.xu, winID); err == nil {
		d.PID = int(pid)
	}
	if states, err := ewmh.WmStateGet(m.xu, winID); err == nil {
		d.States = states
	}
	return d, nil
}

// GetWindowDetailed describes a single window by id, whatever its map state —
// the read-back an agent uses after `window: minimize` or `window: geometry`.
func (m *X11DisplayManager) GetWindowDetailed(id uint32) (WindowDetail, error) {
	activeID, activeErr := ewmh.ActiveWindowGet(m.xu)
	return m.describe(xproto.Window(id), activeErr == nil && xproto.Window(id) == activeID)
}

// GetActiveWindowDetailed is GetWindowDetailed for the window the WM says is
// active (nil when no window manager is running or none is focused).
func (m *X11DisplayManager) GetActiveWindowDetailed() (*WindowDetail, error) {
	id, err := ewmh.ActiveWindowGet(m.xu)
	if err != nil {
		return nil, fmt.Errorf("failed to get active window: %w", err)
	}
	d, err := m.describe(id, true)
	if err != nil {
		return nil, err
	}
	return &d, nil
}

func mapStateName(state byte) string {
	switch state {
	case xproto.MapStateUnmapped:
		return "unmapped"
	case xproto.MapStateUnviewable:
		return "unviewable"
	case xproto.MapStateViewable:
		return "viewable"
	default:
		return fmt.Sprintf("unknown(%d)", state)
	}
}

func (m *X11DisplayManager) GetActiveWindow() (*Window, error) {
	activeWinID, err := ewmh.ActiveWindowGet(m.xu)
	if err != nil {
		return nil, fmt.Errorf("failed to get active window: %w", err)
	}

	title, err := ewmh.WmNameGet(m.xu, activeWinID)
	if err != nil || title == "" {
		title, err = icccm.WmNameGet(m.xu, activeWinID)
		if err != nil {
			title = "Unknown"
		}
	}

	classInfo, err := icccm.WmClassGet(m.xu, activeWinID)
	class := ""
	if err == nil && classInfo != nil {
		class = classInfo.Class
	}

	geom, err := xwindow.New(m.xu, activeWinID).DecorGeometry()
	if err != nil {
		geomNormal, err := xwindow.New(m.xu, activeWinID).Geometry()
		if err != nil {
			return nil, fmt.Errorf("failed to get geometry for active window: %w", err)
		}
		geom = geomNormal
	}

	return &Window{
		ID:    uint32(activeWinID),
		Title: title,
		Class: class,
		Geometry: Geometry{
			X:      geom.X(),
			Y:      geom.Y(),
			Width:  geom.Width(),
			Height: geom.Height(),
		},
	}, nil
}
