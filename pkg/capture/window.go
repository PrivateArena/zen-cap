// [VERIFIED]
package capture

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"

	"github.com/jezek/xgb/xproto"
	"github.com/jezek/xgbutil"
	"github.com/jezek/xgbutil/keybind"
	"github.com/jezek/xgbutil/xevent"
	"zen-cap/pkg/display"
)

type windowState struct {
	xu              *xgbutil.XUtil
	winID           xproto.Window
	gcID            xproto.Gcontext
	bgPixmapID      xproto.Pixmap
	bufPixmapID     xproto.Pixmap
	cyanGCID        xproto.Gcontext
	overlayGCID     xproto.Gcontext
	screenWidth     int
	screenHeight    int
	screen          *xproto.ScreenInfo
	rgbaImg         *image.RGBA
	windows         []display.Window
	hoveredIdx      int
	selected        bool
	aborted         bool
	clipboardAction string
}

// InteractiveSelectWindowExt captures the fullscreen, displays it,
// and highlights windows under the cursor for selection.
func InteractiveSelectWindowExt(fullImg image.Image, outClipboardAction *string) (image.Image, error) {
	dm, err := display.NewX11DisplayManager()
	if err != nil {
		return nil, fmt.Errorf("failed to initialize display manager: %w", err)
	}
	defer dm.Close()

	windows, err := dm.GetWindows()
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve windows: %w", err)
	}

	xu, err := xgbutil.NewConn()
	if err != nil {
		return nil, fmt.Errorf("failed to connect to X server: %w", err)
	}
	defer xu.Conn().Close()

	screen := xu.Screen()
	screenWidth := int(screen.WidthInPixels)
	screenHeight := int(screen.HeightInPixels)

	bounds := fullImg.Bounds()
	rect := image.Rect(0, 0, screenWidth, screenHeight)
	rgbaImg := image.NewRGBA(rect)
	draw.Draw(rgbaImg, rect, fullImg, bounds.Min, draw.Src)

	winID, err := xproto.NewWindowId(xu.Conn())
	if err != nil {
		return nil, fmt.Errorf("failed to create window ID: %w", err)
	}

	var overrideRedirect uint32 = 1
	var eventMask uint32 = xproto.EventMaskButtonPress |
		xproto.EventMaskButtonRelease |
		xproto.EventMaskButtonMotion |
		xproto.EventMaskKeyPress |
		xproto.EventMaskExposure

	err = xproto.CreateWindowChecked(
		xu.Conn(),
		screen.RootDepth,
		winID,
		screen.Root,
		0, 0, uint16(screenWidth), uint16(screenHeight),
		0,
		xproto.WindowClassInputOutput,
		screen.RootVisual,
		xproto.CwOverrideRedirect|xproto.CwEventMask,
		[]uint32{overrideRedirect, eventMask},
	).Check()
	if err != nil {
		return nil, fmt.Errorf("failed to create fullscreen window: %w", err)
	}

	TestHookWindowID = uint32(winID)

	windowNeedsDestroy := true
	defer func() {
		if windowNeedsDestroy {
			xproto.DestroyWindow(xu.Conn(), winID)
		}
	}()

	gcID, err := xproto.NewGcontextId(xu.Conn())
	if err != nil {
		return nil, fmt.Errorf("failed to create GC ID: %w", err)
	}
	err = xproto.CreateGCChecked(xu.Conn(), gcID, xproto.Drawable(winID), 0, nil).Check()
	if err != nil {
		return nil, fmt.Errorf("failed to create GC: %w", err)
	}

	bgPixmapID, err := xproto.NewPixmapId(xu.Conn())
	if err != nil {
		return nil, fmt.Errorf("failed to create background pixmap ID: %w", err)
	}
	err = xproto.CreatePixmapChecked(xu.Conn(), screen.RootDepth, bgPixmapID, xproto.Drawable(winID), uint16(screenWidth), uint16(screenHeight)).Check()
	if err != nil {
		return nil, fmt.Errorf("failed to create background pixmap: %w", err)
	}
	defer xproto.FreePixmap(xu.Conn(), bgPixmapID)

	bufPixmapID, err := xproto.NewPixmapId(xu.Conn())
	if err != nil {
		return nil, fmt.Errorf("failed to create buffer pixmap ID: %w", err)
	}
	err = xproto.CreatePixmapChecked(xu.Conn(), screen.RootDepth, bufPixmapID, xproto.Drawable(winID), uint16(screenWidth), uint16(screenHeight)).Check()
	if err != nil {
		return nil, fmt.Errorf("failed to create buffer pixmap: %w", err)
	}
	defer xproto.FreePixmap(xu.Conn(), bufPixmapID)

	bgraData := imageToBGRA(rgbaImg)
	err = uploadImageChunked(xu, xproto.Drawable(bgPixmapID), gcID, screen.RootDepth, screenWidth, screenHeight, bgraData)
	if err != nil {
		return nil, fmt.Errorf("failed to upload background image: %w", err)
	}

	cyanGCID, err := xproto.NewGcontextId(xu.Conn())
	if err != nil {
		return nil, fmt.Errorf("failed to create cyan GC ID: %w", err)
	}
	var fgColor uint32 = 0x00f0ff
	var lineWidth uint32 = 3
	err = xproto.CreateGCChecked(xu.Conn(), cyanGCID, xproto.Drawable(winID), xproto.GcForeground|xproto.GcLineWidth, []uint32{fgColor, lineWidth}).Check()
	if err != nil {
		return nil, fmt.Errorf("failed to create cyan GC: %w", err)
	}
	defer xproto.FreeGC(xu.Conn(), cyanGCID)

	overlayGCID, err := xproto.NewGcontextId(xu.Conn())
	if err != nil {
		return nil, fmt.Errorf("failed to create overlay GC ID: %w", err)
	}
	stipplePixmapID, err := xproto.NewPixmapId(xu.Conn())
	if err != nil {
		return nil, fmt.Errorf("failed to create stipple pixmap ID: %w", err)
	}
	err = xproto.CreatePixmapChecked(xu.Conn(), 1, stipplePixmapID, xproto.Drawable(winID), 2, 2).Check()
	if err != nil {
		return nil, fmt.Errorf("failed to create stipple pixmap: %w", err)
	}
	defer xproto.FreePixmap(xu.Conn(), stipplePixmapID)

	bitmapGCID, err := xproto.NewGcontextId(xu.Conn())
	if err != nil {
		return nil, fmt.Errorf("failed to create bitmap GC ID: %w", err)
	}
	err = xproto.CreateGCChecked(xu.Conn(), bitmapGCID, xproto.Drawable(stipplePixmapID), 0, nil).Check()
	if err != nil {
		return nil, fmt.Errorf("failed to create bitmap GC: %w", err)
	}
	defer xproto.FreeGC(xu.Conn(), bitmapGCID)

	xproto.ChangeGC(xu.Conn(), bitmapGCID, xproto.GcForeground, []uint32{1})
	xproto.PolyPoint(xu.Conn(), xproto.CoordModeOrigin, xproto.Drawable(stipplePixmapID), bitmapGCID, []xproto.Point{{X: 0, Y: 0}, {X: 1, Y: 1}})
	xproto.ChangeGC(xu.Conn(), bitmapGCID, xproto.GcForeground, []uint32{0})
	xproto.PolyPoint(xu.Conn(), xproto.CoordModeOrigin, xproto.Drawable(stipplePixmapID), bitmapGCID, []xproto.Point{{X: 1, Y: 0}, {X: 0, Y: 1}})

	err = xproto.CreateGCChecked(xu.Conn(), overlayGCID, xproto.Drawable(winID), xproto.GcForeground|xproto.GcFillStyle|xproto.GcStipple, []uint32{0x000000, uint32(xproto.FillStyleStippled), uint32(stipplePixmapID)}).Check()
	if err != nil {
		return nil, fmt.Errorf("failed to create overlay GC: %w", err)
	}
	defer xproto.FreeGC(xu.Conn(), overlayGCID)

	err = xproto.MapWindowChecked(xu.Conn(), winID).Check()
	if err != nil {
		return nil, fmt.Errorf("failed to map window: %w", err)
	}

	pointerGrab, err := xproto.GrabPointer(
		xu.Conn(),
		false,
		winID,
		uint16(xproto.EventMaskButtonPress|xproto.EventMaskButtonRelease|xproto.EventMaskButtonMotion),
		xproto.GrabModeAsync,
		xproto.GrabModeAsync,
		xproto.WindowNone,
		xproto.CursorNone,
		xproto.TimeCurrentTime,
	).Reply()
	if err == nil && pointerGrab.Status == xproto.GrabStatusSuccess {
		defer xproto.UngrabPointer(xu.Conn(), xproto.TimeCurrentTime)
	}

	keyboardGrab, err := xproto.GrabKeyboard(xu.Conn(), false, winID, xproto.TimeCurrentTime, xproto.GrabModeAsync, xproto.GrabModeAsync).Reply()
	if err == nil && keyboardGrab.Status == xproto.GrabStatusSuccess {
		defer xproto.UngrabKeyboard(xu.Conn(), xproto.TimeCurrentTime)
	}

	keybind.Initialize(xu)

	state := &windowState{
		xu:           xu,
		winID:        winID,
		gcID:         gcID,
		bgPixmapID:   bgPixmapID,
		bufPixmapID:  bufPixmapID,
		cyanGCID:     cyanGCID,
		overlayGCID:  overlayGCID,
		screenWidth:  screenWidth,
		screenHeight: screenHeight,
		screen:       screen,
		rgbaImg:      rgbaImg,
		windows:      windows,
		hoveredIdx:   -1,
	}

	xevent.ButtonPressFun(state.handleButtonPress).Connect(xu, winID)
	xevent.MotionNotifyFun(state.handleMotionNotify).Connect(xu, winID)
	xevent.KeyPressFun(state.handleKeyPress).Connect(xu, winID)
	xevent.ExposeFun(func(X *xgbutil.XUtil, ev xevent.ExposeEvent) {
		state.redraw()
	}).Connect(xu, winID)

	// Extra safety abort binding
	keybind.KeyPressFun(func(X *xgbutil.XUtil, ev xevent.KeyPressEvent) {
		state.aborted = true
		xevent.Quit(xu)
	}).Connect(xu, winID, "Control-Shift-x", true)

	keybind.KeyPressFun(func(X *xgbutil.XUtil, ev xevent.KeyPressEvent) {
		state.aborted = true
		xevent.Quit(xu)
	}).Connect(xu, winID, "Control-Shift-X", true)

	state.redraw()
	xevent.Main(xu)

	windowNeedsDestroy = false
	xproto.DestroyWindow(xu.Conn(), winID)

	if state.aborted {
		return nil, fmt.Errorf("window capture aborted by user")
	}

	if !state.selected {
		return nil, fmt.Errorf("no window was selected")
	}

	if outClipboardAction != nil {
		*outClipboardAction = state.clipboardAction
	}

	if state.hoveredIdx == -1 {
		// Fallback: capture entire screen
		return rgbaImg, nil
	}

	w := state.windows[state.hoveredIdx]
	x1 := w.Geometry.X
	y1 := w.Geometry.Y
	width := w.Geometry.Width
	height := w.Geometry.Height
	sub := rgbaImg.SubImage(clampRect(image.Rect(x1, y1, x1+width, y1+height), rgbaImg.Bounds()))
	cropped := image.NewRGBA(image.Rect(0, 0, width, height))
	draw.Draw(cropped, cropped.Bounds(), sub, sub.Bounds().Min, draw.Src)
	return cropped, nil
}

func (s *windowState) redraw() {
	xproto.CopyArea(
		s.xu.Conn(),
		xproto.Drawable(s.bgPixmapID),
		xproto.Drawable(s.bufPixmapID),
		s.gcID,
		0, 0,
		0, 0,
		uint16(s.screenWidth), uint16(s.screenHeight),
	)

	if s.hoveredIdx != -1 {
		w := s.windows[s.hoveredIdx]
		x1 := w.Geometry.X
		y1 := w.Geometry.Y
		width := w.Geometry.Width
		height := w.Geometry.Height

		// Top band
		if y1 > 0 {
			rect := xproto.Rectangle{X: 0, Y: 0, Width: uint16(s.screenWidth), Height: uint16(y1)}
			xproto.PolyFillRectangle(s.xu.Conn(), xproto.Drawable(s.bufPixmapID), s.overlayGCID, []xproto.Rectangle{rect})
		}
		// Bottom band
		if y1+height < s.screenHeight {
			rect := xproto.Rectangle{X: 0, Y: int16(y1 + height), Width: uint16(s.screenWidth), Height: uint16(s.screenHeight - (y1 + height))}
			xproto.PolyFillRectangle(s.xu.Conn(), xproto.Drawable(s.bufPixmapID), s.overlayGCID, []xproto.Rectangle{rect})
		}
		// Left band
		if x1 > 0 && height > 0 {
			rect := xproto.Rectangle{X: 0, Y: int16(y1), Width: uint16(x1), Height: uint16(height)}
			xproto.PolyFillRectangle(s.xu.Conn(), xproto.Drawable(s.bufPixmapID), s.overlayGCID, []xproto.Rectangle{rect})
		}
		// Right band
		if x1+width < s.screenWidth && height > 0 {
			rect := xproto.Rectangle{X: int16(x1 + width), Y: int16(y1), Width: uint16(s.screenWidth - (x1 + width)), Height: uint16(height)}
			xproto.PolyFillRectangle(s.xu.Conn(), xproto.Drawable(s.bufPixmapID), s.overlayGCID, []xproto.Rectangle{rect})
		}

		// Border outline
		if width > 0 && height > 0 {
			rect := xproto.Rectangle{X: int16(x1), Y: int16(y1), Width: uint16(width), Height: uint16(height)}
			xproto.PolyRectangle(s.xu.Conn(), xproto.Drawable(s.bufPixmapID), s.cyanGCID, []xproto.Rectangle{rect})
		}

		// Tooltip HUD
		hudText := fmt.Sprintf("[%s] %s", w.Class, w.Title)
		if len(hudText) > 60 {
			hudText = hudText[:57] + "..."
		}

		tx := x1
		ty := y1 - 20
		if ty < 5 {
			ty = y1 + 5
		}
		if tx < 5 {
			tx = 5
		}

		textW := len(hudText)*6 + 6
		textH := 11
		textImg := image.NewRGBA(image.Rect(0, 0, textW, textH))
		draw.Draw(textImg, textImg.Bounds(), &image.Uniform{color.RGBA{R: 26, G: 26, B: 36, A: 220}}, image.Point{}, draw.Src)
		drawString(textImg, hudText, 3, 3, color.RGBA{R: 0, G: 240, B: 255, A: 255})

		textBGRA := imageToBGRA(textImg)
		_ = xproto.PutImage(
			s.xu.Conn(),
			xproto.ImageFormatZPixmap,
			xproto.Drawable(s.bufPixmapID),
			s.gcID,
			uint16(textW),
			uint16(textH),
			int16(tx),
			int16(ty),
			0,
			s.screen.RootDepth,
			textBGRA,
		)
	} else {
		rect := xproto.Rectangle{X: 0, Y: 0, Width: uint16(s.screenWidth), Height: uint16(s.screenHeight)}
		xproto.PolyFillRectangle(s.xu.Conn(), xproto.Drawable(s.bufPixmapID), s.overlayGCID, []xproto.Rectangle{rect})
	}

	xproto.CopyArea(
		s.xu.Conn(),
		xproto.Drawable(s.bufPixmapID),
		xproto.Drawable(s.winID),
		s.gcID,
		0, 0,
		0, 0,
		uint16(s.screenWidth), uint16(s.screenHeight),
	)
}

func (s *windowState) handleButtonPress(X *xgbutil.XUtil, ev xevent.ButtonPressEvent) {
	if ev.Detail == 1 {
		if s.hoveredIdx != -1 {
			s.selected = true
			xevent.Quit(s.xu)
		}
	} else if ev.Detail == 3 {
		s.aborted = true
		xevent.Quit(s.xu)
	}
}

func (s *windowState) handleMotionNotify(X *xgbutil.XUtil, ev xevent.MotionNotifyEvent) {
	mx := int(ev.EventX)
	my := int(ev.EventY)
	newHovered := s.findHoveredWindow(mx, my)
	if newHovered != s.hoveredIdx {
		s.hoveredIdx = newHovered
		s.redraw()
	}
}

func (s *windowState) handleKeyPress(X *xgbutil.XUtil, ev xevent.KeyPressEvent) {
	mods := ev.State
	keycode := ev.Detail
	keyStr := keybind.LookupString(s.xu, mods, keycode)

	if keyStr == "Escape" || keyStr == "q" || keyStr == "Q" {
		s.aborted = true
		xevent.Quit(s.xu)
		return
	}

	if keyStr == "i" || keyStr == "I" || keyStr == "p" || keyStr == "P" || keyStr == "o" || keyStr == "O" || keyStr == "t" || keyStr == "T" {
		action := "image"
		if keyStr == "p" || keyStr == "P" {
			action = "path"
		} else if keyStr == "o" || keyStr == "O" {
			action = "ocr"
		} else if keyStr == "t" || keyStr == "T" {
			action = "translate"
		}
		s.clipboardAction = action
		s.selected = true
		xevent.Quit(s.xu)
		return
	}
}

func (s *windowState) findHoveredWindow(mx, my int) int {
	for i := len(s.windows) - 1; i >= 0; i-- {
		w := s.windows[i]
		if w.ID == uint32(s.winID) {
			continue
		}
		if w.Geometry.Width >= s.screenWidth && w.Geometry.Height >= s.screenHeight && w.Geometry.X == 0 && w.Geometry.Y == 0 {
			continue
		}
		if mx >= w.Geometry.X && mx < w.Geometry.X+w.Geometry.Width &&
			my >= w.Geometry.Y && my < w.Geometry.Y+w.Geometry.Height {
			return i
		}
	}
	return -1
}

func clampRect(rect image.Rectangle, max image.Rectangle) image.Rectangle {
	r := rect.Intersect(max)
	if r.Empty() {
		return image.Rect(0, 0, 1, 1)
	}
	return r
}
