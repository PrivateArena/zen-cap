package magnifier

import (
	"fmt"

	"github.com/jezek/xgb/randr"
	"github.com/jezek/xgbutil"
)

// MonitorGeometry describes a single connected output's position and size.
type MonitorGeometry struct {
	X, Y, W, H int
	Name        string
}

// detectMonitors uses XRandR to enumerate all active outputs and their screen
// geometries. Falls back to a single monitor covering the root window if the
// extension is unavailable or returns no outputs.
func detectMonitors(xu *xgbutil.XUtil) ([]MonitorGeometry, error) {
	conn := xu.Conn()

	if err := randr.Init(conn); err != nil {
		// RandR not available: return root window bounds as a single monitor.
		screen := xu.Screen()
		return []MonitorGeometry{{
			X: 0, Y: 0,
			W:    int(screen.WidthInPixels),
			H:    int(screen.HeightInPixels),
			Name: "default",
		}}, nil
	}

	root := xu.RootWin()
	res, err := randr.GetScreenResources(conn, root).Reply()
	if err != nil {
		screen := xu.Screen()
		return []MonitorGeometry{{
			X: 0, Y: 0,
			W:    int(screen.WidthInPixels),
			H:    int(screen.HeightInPixels),
			Name: "default",
		}}, nil
	}

	var monitors []MonitorGeometry
	for _, crtcID := range res.Crtcs {
		info, err := randr.GetCrtcInfo(conn, crtcID, res.ConfigTimestamp).Reply()
		if err != nil || info.Width == 0 || info.Height == 0 {
			continue // inactive CRTC
		}
		name := fmt.Sprintf("crtc-%d", crtcID)
		monitors = append(monitors, MonitorGeometry{
			X:    int(info.X),
			Y:    int(info.Y),
			W:    int(info.Width),
			H:    int(info.Height),
			Name: name,
		})
	}

	if len(monitors) == 0 {
		// No active CRTCs found: fall back to root window.
		screen := xu.Screen()
		monitors = []MonitorGeometry{{
			X: 0, Y: 0,
			W:    int(screen.WidthInPixels),
			H:    int(screen.HeightInPixels),
			Name: "default",
		}}
	}
	return monitors, nil
}

// monitorForPoint returns the monitor that contains the given point.
// Falls back to the first monitor if no match is found.
func monitorForPoint(monitors []MonitorGeometry, x, y int) MonitorGeometry {
	for _, m := range monitors {
		if x >= m.X && x < m.X+m.W && y >= m.Y && y < m.Y+m.H {
			return m
		}
	}
	if len(monitors) > 0 {
		return monitors[0]
	}
	return MonitorGeometry{X: 0, Y: 0, W: 1920, H: 1080, Name: "fallback"}
}
