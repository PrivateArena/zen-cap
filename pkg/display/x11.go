// [VERIFIED]
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
}

func NewX11DisplayManager() (*X11DisplayManager, error) {
	xu, err := xgbutil.NewConn()
	if err != nil {
		return nil, fmt.Errorf("failed to connect to X server: %w", err)
	}
	return &X11DisplayManager{xu: xu}, nil
}

func (m *X11DisplayManager) Close() {
	if m.xu != nil {
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

func (m *X11DisplayManager) GetWindows() ([]Window, error) {
	clientIDs, err := ewmh.ClientListGet(m.xu)
	if err != nil {
		// Fallback: query tree of the root window
		tree, err := xproto.QueryTree(m.xu.Conn(), m.xu.RootWin()).Reply()
		if err != nil {
			return nil, fmt.Errorf("failed to query window tree: %w", err)
		}
		clientIDs = tree.Children
	}

	var windows []Window
	for _, winID := range clientIDs {
		// Only list visible, mapable windows or windows with titles
		attrs, err := xproto.GetWindowAttributes(m.xu.Conn(), winID).Reply()
		if err != nil || attrs.MapState != xproto.MapStateViewable {
			continue
		}

		title, err := ewmh.WmNameGet(m.xu, winID)
		if err != nil || title == "" {
			title, err = icccm.WmNameGet(m.xu, winID)
			if err != nil || title == "" {
				continue // Skip windows without titles to avoid cluttering list
			}
		}

		classInfo, err := icccm.WmClassGet(m.xu, winID)
		class := ""
		if err == nil && classInfo != nil {
			class = classInfo.Class
		}

		geom, err := xwindow.New(m.xu, winID).DecorGeometry()
		if err != nil {
			// Try normal geometry if decor geometry fails
			geomNormal, err := xwindow.New(m.xu, winID).Geometry()
			if err != nil {
				continue
			}
			geom = geomNormal
		}

		windows = append(windows, Window{
			ID:    uint32(winID),
			Title: title,
			Class: class,
			Geometry: Geometry{
				X:      geom.X(),
				Y:      geom.Y(),
				Width:  geom.Width(),
				Height: geom.Height(),
			},
		})
	}

	return windows, nil
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
