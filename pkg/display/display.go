package display

type Geometry struct {
	X      int
	Y      int
	Width  int
	Height int
}

type Screen struct {
	Index    int
	Name     string
	Geometry Geometry
}

type Window struct {
	ID       uint32
	Title    string
	Class    string
	Geometry Geometry
}

// WindowDetail is everything this package can learn about one client window.
// It exists so callers that need more than a picker label (the /automation HTTP
// API, anything verifying what a window manager actually did) can get it without
// re-implementing the ClientList walk — see X11DisplayManager.GetWindowsDetailed.
type WindowDetail struct {
	Window
	PID      int      // _NET_WM_PID, 0 when the client does not set it
	Instance string   // WM_CLASS instance (the "firefox" next to the "Firefox" class)
	MapState string   // viewable | unviewable | unmapped
	Active   bool     // the window named by _NET_ACTIVE_WINDOW
	States   []string // raw _NET_WM_STATE atom names
}

type DisplayManager interface {
	GetScreens() ([]Screen, error)
	GetWindows() ([]Window, error)
	GetActiveWindow() (*Window, error)
	Close()
}
