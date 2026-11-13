package automation

import (
	"errors"
	"fmt"
	"strings"

	"github.com/jezek/xgb/xproto"
	"github.com/jezek/xgbutil"
	"github.com/jezek/xgbutil/keybind"

	"zen-cap/pkg/config"
	"zen-cap/pkg/target"
)

// TargetSpec says what a run should address, decoupled from where that request
// came from: a YAML script fills it from `target:`/`window:`, an HTTP request
// fills it from its already-resolved window id.
//
// Extracted from the body of RunScript, which used to inline the whole mapping.
type TargetSpec struct {
	// Display is the X display string ("" = $DISPLAY). Only meaningful for the
	// x11/vfb backends.
	Display string
	// Backend selects the target implementation: ""|"x11"|"vnc"|"adb"|"wda"|"vfb".
	Backend string
	// WindowID addresses a client window directly (0 = whole screen).
	WindowID uint32
	// Window is a by-title/by-class selector, resolved on the X server. It is
	// ignored when WindowID is already non-zero.
	Window *WindowTarget
	// Scale resizes captures (see target.Config).
	Scale float64

	// VNC / ADB / WDA / Xvfb plumbing, mirrors of Script.Target.
	Host       string
	Port       int
	Password   string
	Serial     string
	WDAHost    string
	VFBDisplay string
	VFBSerial  string
	VFBWidth   int
	VFBHeight  int

	// AllowUnresolved keeps a by-title/class miss non-fatal: the run proceeds
	// in absolute screen space, which is what YAML scripts have always done.
	// The HTTP API leaves it false, so a stale selector is a real error rather
	// than clicks on the wrong window.
	AllowUnresolved bool

	// Logf receives the resolution chatter. RunScript passes the script logger;
	// the HTTP passes a collector that ends up in the response's `log`.
	Logf func(format string, args ...interface{})

	Config *config.Config
}

func (s TargetSpec) logf(format string, args ...interface{}) {
	if s.Logf != nil {
		s.Logf(format, args...)
	}
}

// TargetSpecFromScript maps a Script onto a TargetSpec.
func TargetSpecFromScript(script Script, cfg *config.Config) TargetSpec {
	spec := TargetSpec{Window: script.Window, Config: cfg, AllowUnresolved: true}

	if script.Target != nil {
		spec.Backend = script.Target.Type
		spec.Display = script.Target.Display
		spec.Scale = script.Target.Scale
		spec.Host = script.Target.Host
		spec.Port = script.Target.Port
		spec.Password = script.Target.Password
		spec.Serial = script.Target.Serial
		spec.WDAHost = script.Target.WDAHost
		spec.VFBDisplay = script.Target.VFBDisplay
		spec.VFBSerial = script.Target.VFBSerial
		spec.VFBWidth = script.Target.VFBWidth
		spec.VFBHeight = script.Target.VFBHeight
	}
	return spec
}

// OpenTarget connects to the requested backend and returns the live target plus
// the window id it ended up scoped to. The caller owns Close().
func OpenTarget(spec TargetSpec) (target.Target, uint32, error) {
	// Resolve a by-title/class window selector before connecting: the id has to
	// go into target.New, not be looked up again later.
	if spec.WindowID == 0 && spec.Window != nil && (spec.Backend == "" || spec.Backend == "x11") {
		xu, connErr := connectDisplay(spec.Display)
		switch {
		case connErr != nil && !spec.AllowUnresolved:
			return nil, 0, newRunError(CodeXError, "cannot connect to display to resolve window: %v", connErr)
		case connErr != nil:
			// Historical behaviour: an unreachable X server does not stop a YAML
			// script, it just loses its window scoping.
			spec.logf("[Automation] Warning: cannot resolve window: %v. Running in absolute screen space.", connErr)
		default:
			id, err := ResolveWindow(xu, spec.Window)
			xu.Conn().Close()
			if err != nil {
				if !spec.AllowUnresolved {
					return nil, 0, newRunError(CodeWindowMissing, "no window matching title=%q class=%q", spec.Window.Title, spec.Window.Class)
				}
				spec.logf("[Automation] Warning: %v. Running in absolute screen space.", err)
			} else {
				spec.WindowID = id
				spec.logf("[Automation] Target window resolved: WID=%d", id)
			}
		}
	}

	tgt, err := target.New(spec.targetConfig(), spec.WindowID)
	if err != nil {
		// A stale XID and a dead one are the same thing to X: the id names no
		// drawable. Reporting that as window_gone (HTTP 409) is what lets an agent
		// re-enumerate instead of retrying a request that can never succeed.
		if isDeadWindowError(err) {
			return nil, 0, newRunError(CodeWindowGone, "window 0x%x is gone: %v", spec.WindowID, err)
		}
		return nil, 0, newRunError(CodeXError, "failed to create automation target: %v", err)
	}

	// Validate the id *after* construction: XIDs are recycled, so a window that
	// closed between requests can silently name a different application. The
	// caller reports the title/class it found (RunResult.WindowTitle) so an
	// agent can notice the swap; the liveness probe catches the plain case.
	if err := CheckWindowAlive(tgt); err != nil {
		_ = tgt.Close()
		if re, ok := err.(*RunError); ok {
			return nil, 0, re
		}
		return nil, 0, newRunError(CodeXError, "%v", err)
	}
	return tgt, spec.WindowID, nil
}

// targetConfig maps the spec onto the backend's own config struct.
func (s TargetSpec) targetConfig() target.Config {
	return target.Config{
		Type:       s.Backend,
		Display:    s.Display,
		Host:       s.Host,
		Port:       s.Port,
		Password:   s.Password,
		Serial:     s.Serial,
		WDAHost:    s.WDAHost,
		VFBDisplay: s.VFBDisplay,
		VFBSerial:  s.VFBSerial,
		VFBWidth:   s.VFBWidth,
		VFBHeight:  s.VFBHeight,
		Scale:      s.Scale,
	}
}

// isDeadWindowError recognises the X protocol's "that id is not a window (any
// more)" answers, wherever in the chain they were wrapped.
func isDeadWindowError(err error) bool {
	if err == nil {
		return false
	}
	var winErr xproto.WindowError
	var drawErr xproto.DrawableError
	if errors.As(err, &winErr) || errors.As(err, &drawErr) {
		return true
	}
	// Last resort for an error that lost its type on the way here.
	msg := err.Error()
	return strings.Contains(msg, "BadWindow") || strings.Contains(msg, "BadDrawable")
}

// connectDisplay opens an X connection, honouring an explicit display string.
func connectDisplay(display string) (*xgbutil.XUtil, error) {
	if display == "" {
		xu, err := xgbutil.NewConn()
		if err != nil {
			return nil, err
		}
		keybind.Initialize(xu)
		return xu, nil
	}
	xu, err := xgbutil.NewConnDisplay(display)
	if err != nil {
		return nil, err
	}
	keybind.Initialize(xu)
	return xu, nil
}

// CheckWindowAlive reports whether the window a target is scoped to still
// exists, as an *RunError with CodeWindowGone. It is a single round trip
// (TranslateCoordinates is the same request the input path needs, so the probe
// cannot lie about what a click would hit).
//
// Non-X11 backends have no notion of a client window id, so nil means "cannot
// say, and nothing to invalidate".
func CheckWindowAlive(t target.Target) error {
	switch tt := t.(type) {
	case *target.X11Target:
		return checkX11WindowAlive(tt)
	case *target.VFBTarget:
		if x11 := tt.X11(); x11 != nil {
			return checkX11WindowAlive(x11)
		}
	}
	return nil
}

func checkX11WindowAlive(t *target.X11Target) error {
	if !t.IsWindowTarget() {
		return nil
	}
	xu := t.XUtil()
	// Translating the client origin is exactly what input does before warping
	// the pointer, so this probe fails precisely when the next click would.
	_, err := xproto.TranslateCoordinates(xu.Conn(), xproto.Window(t.WinID()), xproto.Window(t.WinID()), 0, 0).Reply()
	if err != nil {
		// Only a "that id is no longer a window" answer is a gone window. An X I/O
		// error means the *connection* died; telling the agent to re-enumerate would
		// send it retrying against a dead daemon.
		if isDeadWindowError(err) {
			return newRunError(CodeWindowGone, "window 0x%x is no longer usable: %v", t.WinID(), err)
		}
		return newRunError(CodeXError, "cannot check window 0x%x: %v", t.WinID(), err)
	}
	return nil
}

// DescribeWindowFromTarget reports the window a live target is scoped to. It is
// how a response states which window it really addressed — the check that catches
// a recycled XID pointing at a different application.
func DescribeWindowFromTarget(t target.Target, id uint32) (WindowInfo, error) {
	switch tt := t.(type) {
	case *target.X11Target:
		if tt.WinID() != 0 {
			id = tt.WinID()
		}
		return DescribeWindow(tt.XUtil(), id)
	case *target.VFBTarget:
		if x11 := tt.X11(); x11 != nil {
			if x11.WinID() != 0 {
				id = x11.WinID()
			}
			return DescribeWindow(x11.XUtil(), id)
		}
	}
	return WindowInfo{}, fmt.Errorf("this target backend has no windows to describe")
}

// ReleasePointerButtons opens a throwaway connection purely to force-release the
// pointer buttons, for the abort route: a run that died mid-drag must not leave
// the user's desktop acting as if the mouse were held, and the aborting request
// has no target of its own.
func ReleasePointerButtons(display string) (bool, error) {
	tgt, err := target.New(target.Config{Display: display}, 0)
	if err != nil {
		return false, err
	}
	defer func() { _ = tgt.Close() }()
	return ReleaseButtons(tgt), nil
}

// ReleaseButtons force-releases every pointer button the backend might be
// holding, and reports whether a release actually ran.
//
// This is deliberately not a Target wrapper: wrapping would break the
// ctx.x11ctx() type switch that `window`/`fullscreen` depend on.
func ReleaseButtons(t target.Target) bool {
	pr, ok := t.(target.PanicReleaser)
	if !ok {
		return false
	}
	if err := pr.ReleaseAllButtons(); err != nil {
		return false
	}
	return true
}
