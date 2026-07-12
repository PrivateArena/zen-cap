package capture

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"log"
	"math"
	"time"

	"github.com/jezek/xgb/xproto"
	"github.com/jezek/xgbutil"
	"github.com/jezek/xgbutil/keybind"
	"github.com/jezek/xgbutil/xevent"

	"zen-cap/pkg/annotation"
)

// TestHookWindowID is a global hook used by automated tests to locate the selection window.
var TestHookWindowID uint32

type regionState struct {
	xu              *xgbutil.XUtil
	winID           xproto.Window
	gcID            xproto.Gcontext
	bgPixmapID      xproto.Pixmap
	bufPixmapID     xproto.Pixmap
	cyanGCID        xproto.Gcontext
	pinkGCID        xproto.Gcontext
	overlayGCID     xproto.Gcontext
	screenWidth     int
	screenHeight    int
	screen          *xproto.ScreenInfo
	dragStart       bool
	startX          int
	startY          int
	currX           int
	currY           int
	selected        bool
	aborted         bool
	clipboardAction string // "image", "path", "ocr", "translate"

	ann          *annotation.Annotator
	magnifier    *Magnifier
	displayCache *image.RGBA // cached GetComposite() to avoid double allocation per redraw
}

// InteractiveSelectRegion is a backward-compatible wrapper around InteractiveSelectRegionExt.
func InteractiveSelectRegion(fullImg image.Image) (image.Image, error) {
	return InteractiveSelectRegionExt(fullImg, nil, nil, nil, nil, nil)
}

// InteractiveSelectRegionExt captures the fullscreen, displays it in an override-redirect
// window, lets the user drag-and-drop to select a region, and returns the cropped image bounds.
// It also allows the user to draw annotations/doodles using the Right Mouse Button before selecting.
// It populates outClipboardAction if a dynamic shortcut was pressed.
func InteractiveSelectRegionExt(fullImg image.Image, outClipboardAction *string, outX, outY, outW, outH *int) (image.Image, error) {
	// Connect to X server
	xu, err := xgbutil.NewConn()
	if err != nil {
		return nil, fmt.Errorf("failed to connect to X server: %w", err)
	}
	defer xu.Conn().Close()

	screen := xu.Screen()
	screenWidth := int(screen.WidthInPixels)
	screenHeight := int(screen.HeightInPixels)

	// Ensure we have a mutable RGBA image matching screen dimensions
	bounds := fullImg.Bounds()
	rect := image.Rect(0, 0, screenWidth, screenHeight)
	rgbaImg := image.NewRGBA(rect)
	draw.Draw(rgbaImg, rect, fullImg, bounds.Min, draw.Src)

	// Create window ID
	winID, err := xproto.NewWindowId(xu.Conn())
	if err != nil {
		return nil, fmt.Errorf("failed to create window ID: %w", err)
	}
	TestHookWindowID = uint32(winID)

	// Set window attributes: override_redirect to bypass WM, capture all necessary events
	// EventMaskButtonMotion allows tracking motion during left/right click drags
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
		0, // border width
		xproto.WindowClassInputOutput,
		screen.RootVisual,
		xproto.CwOverrideRedirect|xproto.CwEventMask,
		[]uint32{overrideRedirect, eventMask},
	).Check()
	if err != nil {
		return nil, fmt.Errorf("failed to create fullscreen window: %w", err)
	}

	// Make sure window is destroyed on function exit if we error out
	windowNeedsDestroy := true
	defer func() {
		if windowNeedsDestroy {
			xproto.DestroyWindow(xu.Conn(), winID)
		}
	}()

	// Setup Graphics Context (GC) for drawing
	gcID, err := xproto.NewGcontextId(xu.Conn())
	if err != nil {
		return nil, fmt.Errorf("failed to create GC ID: %w", err)
	}
	err = xproto.CreateGCChecked(
		xu.Conn(),
		gcID,
		xproto.Drawable(winID),
		0,
		nil,
	).Check()
	if err != nil {
		return nil, fmt.Errorf("failed to create GC: %w", err)
	}

	// Create a background Pixmap to store the full screen screenshot
	bgPixmapID, err := xproto.NewPixmapId(xu.Conn())
	if err != nil {
		return nil, fmt.Errorf("failed to create background pixmap ID: %w", err)
	}
	err = xproto.CreatePixmapChecked(
		xu.Conn(),
		screen.RootDepth,
		bgPixmapID,
		xproto.Drawable(winID),
		uint16(screenWidth),
		uint16(screenHeight),
	).Check()
	if err != nil {
		return nil, fmt.Errorf("failed to create background pixmap: %w", err)
	}
	defer xproto.FreePixmap(xu.Conn(), bgPixmapID)

	// Create a buffer Pixmap for double-buffering (flicker-free drawing)
	bufPixmapID, err := xproto.NewPixmapId(xu.Conn())
	if err != nil {
		return nil, fmt.Errorf("failed to create buffer pixmap ID: %w", err)
	}
	err = xproto.CreatePixmapChecked(
		xu.Conn(),
		screen.RootDepth,
		bufPixmapID,
		xproto.Drawable(winID),
		uint16(screenWidth),
		uint16(screenHeight),
	).Check()
	if err != nil {
		return nil, fmt.Errorf("failed to create buffer pixmap: %w", err)
	}
	defer xproto.FreePixmap(xu.Conn(), bufPixmapID)

	// Convert Go Image to BGRA bytes for X11 ZPixmap format
	bgraData := imageToBGRA(rgbaImg)

	// Upload background image to bgPixmap in chunks to avoid MaxRequestBytes limits
	err = uploadImageChunked(xu, xproto.Drawable(bgPixmapID), gcID, screen.RootDepth, screenWidth, screenHeight, bgraData)
	if err != nil {
		return nil, fmt.Errorf("failed to upload screenshot to X server: %w", err)
	}

	// Create a premium neon-cyan GC for drawing selection borders
	cyanGCID, err := xproto.NewGcontextId(xu.Conn())
	if err != nil {
		return nil, fmt.Errorf("failed to create cyan GC ID: %w", err)
	}
	var fgColor uint32 = 0x00f0ff // Neon cyan
	var lineWidth uint32 = 2
	err = xproto.CreateGCChecked(
		xu.Conn(),
		cyanGCID,
		xproto.Drawable(winID),
		xproto.GcForeground|xproto.GcLineWidth,
		[]uint32{fgColor, lineWidth},
	).Check()
	if err != nil {
		return nil, fmt.Errorf("failed to create cyan GC: %w", err)
	}
	defer xproto.FreeGC(xu.Conn(), cyanGCID)

	// Create a premium neon-pink GC for drawing doodles/annotations
	pinkGCID, err := xproto.NewGcontextId(xu.Conn())
	if err != nil {
		return nil, fmt.Errorf("failed to create pink GC ID: %w", err)
	}
	var pinkColor uint32 = 0xff007f // Neon pink
	var brushThickness uint32 = 4
	err = xproto.CreateGCChecked(
		xu.Conn(),
		pinkGCID,
		xproto.Drawable(winID),
		xproto.GcForeground|xproto.GcLineWidth|xproto.GcLineStyle|xproto.GcCapStyle|xproto.GcJoinStyle,
		[]uint32{pinkColor, brushThickness, uint32(xproto.LineStyleSolid), uint32(xproto.CapStyleRound), uint32(xproto.JoinStyleRound)},
	).Check()
	if err != nil {
		return nil, fmt.Errorf("failed to create pink GC: %w", err)
	}
	defer xproto.FreeGC(xu.Conn(), pinkGCID)

	// Create a gray translucent overlay GC for unselected areas
	overlayGCID, err := xproto.NewGcontextId(xu.Conn())
	if err != nil {
		return nil, fmt.Errorf("failed to create overlay GC ID: %w", err)
	}
	// We use black with a stipple/dither pattern for 100% fallback compatibility on any depth.
	// We'll create a 50% stipple pixmap (alternating pixels) to darken the unselected parts.
	stipplePixmapID, err := xproto.NewPixmapId(xu.Conn())
	if err != nil {
		return nil, fmt.Errorf("failed to create stipple pixmap ID: %w", err)
	}
	// Stipple needs to be depth 1 (bitmap)
	err = xproto.CreatePixmapChecked(xu.Conn(), 1, stipplePixmapID, xproto.Drawable(winID), 2, 2).Check()
	if err != nil {
		return nil, fmt.Errorf("failed to create stipple pixmap: %w", err)
	}
	defer xproto.FreePixmap(xu.Conn(), stipplePixmapID)

	// Create a depth-1 GC to draw on the stipple pixmap
	bitmapGCID, err := xproto.NewGcontextId(xu.Conn())
	if err != nil {
		return nil, fmt.Errorf("failed to create bitmap GC ID: %w", err)
	}
	err = xproto.CreateGCChecked(xu.Conn(), bitmapGCID, xproto.Drawable(stipplePixmapID), 0, nil).Check()
	if err != nil {
		return nil, fmt.Errorf("failed to create bitmap GC: %w", err)
	}
	defer xproto.FreeGC(xu.Conn(), bitmapGCID)

	// Alternating pattern: pixel (0,0) and (1,1) are black (value 1), (0,1) and (1,0) are white (value 0)
	xproto.ChangeGC(xu.Conn(), bitmapGCID, xproto.GcForeground, []uint32{1})
	xproto.PolyPoint(xu.Conn(), xproto.CoordModeOrigin, xproto.Drawable(stipplePixmapID), bitmapGCID, []xproto.Point{{X: 0, Y: 0}, {X: 1, Y: 1}})
	xproto.ChangeGC(xu.Conn(), bitmapGCID, xproto.GcForeground, []uint32{0})
	xproto.PolyPoint(xu.Conn(), xproto.CoordModeOrigin, xproto.Drawable(stipplePixmapID), bitmapGCID, []xproto.Point{{X: 1, Y: 0}, {X: 0, Y: 1}})

	// Initialize the translucent overlay GC with stippling
	err = xproto.CreateGCChecked(
		xu.Conn(),
		overlayGCID,
		xproto.Drawable(winID),
		xproto.GcForeground|xproto.GcFillStyle|xproto.GcStipple,
		[]uint32{0x000000, uint32(xproto.FillStyleStippled), uint32(stipplePixmapID)},
	).Check()
	if err != nil {
		return nil, fmt.Errorf("failed to create overlay GC: %w", err)
	}
	defer xproto.FreeGC(xu.Conn(), overlayGCID)

	// Map the fullscreen window
	err = xproto.MapWindowChecked(xu.Conn(), winID).Check()
	if err != nil {
		return nil, fmt.Errorf("failed to map window: %w", err)
	}

	// Grab Pointer (mouse) and Keyboard to capture all events exclusively
	// Pointer Grab:
	pointerGrab, err := xproto.GrabPointer(
		xu.Conn(),
		false, // ownerEvents
		winID, // grabWindow
		uint16(xproto.EventMaskButtonPress|xproto.EventMaskButtonRelease|xproto.EventMaskButtonMotion),
		xproto.GrabModeAsync, // pointerMode
		xproto.GrabModeAsync, // keyboardMode
		xproto.WindowNone,    // confineTo
		xproto.CursorNone,    // cursor
		xproto.TimeCurrentTime,
	).Reply()
	if err != nil || pointerGrab.Status != xproto.GrabStatusSuccess {
		log.Printf("Warning: GrabPointer status: %v, err: %v", pointerGrab.Status, err)
	} else {
		defer xproto.UngrabPointer(xu.Conn(), xproto.TimeCurrentTime)
	}

	// Keyboard Grab:
	keyboardGrab, err := xproto.GrabKeyboard(
		xu.Conn(),
		false,
		winID,
		xproto.TimeCurrentTime,
		xproto.GrabModeAsync,
		xproto.GrabModeAsync,
	).Reply()
	if err != nil || keyboardGrab.Status != xproto.GrabStatusSuccess {
		log.Printf("Warning: GrabKeyboard status: %v, err: %v", keyboardGrab.Status, err)
	} else {
		defer xproto.UngrabKeyboard(xu.Conn(), xproto.TimeCurrentTime)
	}

	// Initialize keybinds for Escape, Q, and Ctrl+Shift+X abort keys
	keybind.Initialize(xu)

	// Instantiate state
	state := &regionState{
		xu:              xu,
		winID:           winID,
		gcID:            gcID,
		bgPixmapID:      bgPixmapID,
		bufPixmapID:     bufPixmapID,
		cyanGCID:        cyanGCID,
		pinkGCID:        pinkGCID,
		overlayGCID:     overlayGCID,
		screenWidth:     screenWidth,
		screenHeight:    screenHeight,
		screen:          screen,
		ann: annotation.NewAnnotator(rgbaImg, annotation.Config{
			BrushThickness: brushThickness,
			FontScale:      4,
			Color:          color.RGBA{R: 255, G: 0, B: 127, A: 255},
		}),
		magnifier:       NewMagnifier(),
	}

	// Register event handlers
	xevent.ButtonPressFun(state.handleButtonPress).Connect(xu, winID)
	xevent.ButtonReleaseFun(state.handleButtonRelease).Connect(xu, winID)
	xevent.MotionNotifyFun(state.handleMotionNotify).Connect(xu, winID)
	xevent.ExposeFun(func(X *xgbutil.XUtil, ev xevent.ExposeEvent) {
		state.redraw()
	}).Connect(xu, winID)
	xevent.KeyPressFun(state.handleKeyPress).Connect(xu, winID)

	// Extra safety abort binding specifically for Ctrl+Shift+X
	keybind.KeyPressFun(func(X *xgbutil.XUtil, ev xevent.KeyPressEvent) {
		state.aborted = true
		xevent.Quit(xu)
	}).Connect(xu, winID, "Control-Shift-x", true)

	keybind.KeyPressFun(func(X *xgbutil.XUtil, ev xevent.KeyPressEvent) {
		state.aborted = true
		xevent.Quit(xu)
	}).Connect(xu, winID, "Control-Shift-X", true)

	// Initial redraw
	state.redraw()

	// Run X11 event loop until Quit is called
	xevent.Main(xu)

	// Destroy window immediately
	windowNeedsDestroy = false
	xproto.DestroyWindow(xu.Conn(), winID)

	if state.aborted {
		return nil, fmt.Errorf("region capture aborted by user safety key")
	}

	if !state.selected {
		return nil, fmt.Errorf("no region was selected")
	}

	// Calculate final selection coordinates
	x1 := int(math.Min(float64(state.startX), float64(state.currX)))
	y1 := int(math.Min(float64(state.startY), float64(state.currY)))
	w := int(math.Abs(float64(state.currX - state.startX)))
	h := int(math.Abs(float64(state.currY - state.startY)))

	// If region is too small (e.g. less than 5x5 pixels), assume it was an accidental click and abort
	if w < 5 || h < 5 {
		return nil, fmt.Errorf("selected region too small (%dx%d)", w, h)
	}

	fmt.Printf("[InteractiveSelectRegion] Selected bounds: x=%d, y=%d, width=%d, height=%d\n", x1, y1, w, h)

	if outX != nil {
		*outX = x1
	}
	if outY != nil {
		*outY = y1
	}
	if outW != nil {
		*outW = w
	}
	if outH != nil {
		*outH = h
	}

	// Crop the annotated fullscreen screenshot image
	composite := state.ann.GetComposite()
	cropped := composite.SubImage(image.Rect(x1, y1, x1+w, y1+h))
	if outClipboardAction != nil {
		*outClipboardAction = state.clipboardAction
	}
	return cropped, nil
}

// Redraw function using double buffering
func (s *regionState) redraw() {
	// 1. Always start from clean background
	xproto.CopyArea(
		s.xu.Conn(),
		xproto.Drawable(s.bgPixmapID),
		xproto.Drawable(s.bufPixmapID),
		s.gcID,
		0, 0,
		0, 0,
		uint16(s.screenWidth), uint16(s.screenHeight),
	)

	// 2. Fetch composite once, cache for upload + magnifier; avoid double allocation
	if s.ann.IsDirty() {
		s.displayCache = s.ann.GetComposite()
		s.ann.ClearDirty()
	}
	if s.displayCache != nil {
		bgra := imageToBGRA(s.displayCache)
		_ = uploadImageChunked(s.xu, xproto.Drawable(s.bufPixmapID), s.gcID, s.screen.RootDepth, s.screenWidth, s.screenHeight, bgra)
	}

	// 3. Selection overlay
	if s.dragStart {
		x1 := int(math.Min(float64(s.startX), float64(s.currX)))
		y1 := int(math.Min(float64(s.startY), float64(s.currY)))
		w := int(math.Abs(float64(s.currX - s.startX)))
		h := int(math.Abs(float64(s.currY - s.startY)))

		if y1 > 0 {
			rect := xproto.Rectangle{X: 0, Y: 0, Width: uint16(s.screenWidth), Height: uint16(y1)}
			xproto.PolyFillRectangle(s.xu.Conn(), xproto.Drawable(s.bufPixmapID), s.overlayGCID, []xproto.Rectangle{rect})
		}
		if y1+h < s.screenHeight {
			rect := xproto.Rectangle{X: 0, Y: int16(y1 + h), Width: uint16(s.screenWidth), Height: uint16(s.screenHeight - (y1 + h))}
			xproto.PolyFillRectangle(s.xu.Conn(), xproto.Drawable(s.bufPixmapID), s.overlayGCID, []xproto.Rectangle{rect})
		}
		if x1 > 0 && h > 0 {
			rect := xproto.Rectangle{X: 0, Y: int16(y1), Width: uint16(x1), Height: uint16(h)}
			xproto.PolyFillRectangle(s.xu.Conn(), xproto.Drawable(s.bufPixmapID), s.overlayGCID, []xproto.Rectangle{rect})
		}
		if x1+w < s.screenWidth && h > 0 {
			rect := xproto.Rectangle{X: int16(x1 + w), Y: int16(y1), Width: uint16(s.screenWidth - (x1 + w)), Height: uint16(h)}
			xproto.PolyFillRectangle(s.xu.Conn(), xproto.Drawable(s.bufPixmapID), s.overlayGCID, []xproto.Rectangle{rect})
		}

		if w > 0 && h > 0 {
			rect := xproto.Rectangle{X: int16(x1), Y: int16(y1), Width: uint16(w), Height: uint16(h)}
			xproto.PolyRectangle(s.xu.Conn(), xproto.Drawable(s.bufPixmapID), s.cyanGCID, []xproto.Rectangle{rect})
		}
	} else {
		rect := xproto.Rectangle{X: 0, Y: 0, Width: uint16(s.screenWidth), Height: uint16(s.screenHeight)}
		xproto.PolyFillRectangle(s.xu.Conn(), xproto.Drawable(s.bufPixmapID), s.overlayGCID, []xproto.Rectangle{rect})
	}

	// 4. Transient annotation preview (text cursor, shape outlines)
	s.drawAnnotationPreview()

	// 5. Magnifier (use cached composite if available to avoid allocation)
	displayImg := s.displayCache
	if displayImg == nil {
		displayImg = s.ann.GetComposite()
	}
	s.magnifier.Render(
		s.xu,
		s.bufPixmapID,
		s.gcID,
		s.screen.RootDepth,
		displayImg,
		s.currX,
		s.currY,
		s.screenWidth,
		s.screenHeight,
		s.dragStart,
		s.startX,
		s.startY,
	)

	// 6. Copy to window
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

// drawAnnotationPreview draws transient shape outlines and text cursor on the buffer pixmap.
func (s *regionState) drawAnnotationPreview() {
	if !s.ann.IsDoodling() && !s.ann.IsTextActive() {
		return
	}

	// Draw rect/circle preview outline during drag
	if s.ann.IsDoodling() {
		tool := s.ann.Tool()
		if tool == annotation.Rect || tool == annotation.Circle {
			start := s.ann.DoodleStart()
			if tool == annotation.Rect {
				x1 := int(math.Min(float64(start.X), float64(s.currX)))
				y1 := int(math.Min(float64(start.Y), float64(s.currY)))
				w := int(math.Abs(float64(s.currX - start.X)))
				h := int(math.Abs(float64(s.currY - start.Y)))
				if w > 0 && h > 0 {
					rect := xproto.Rectangle{X: int16(x1), Y: int16(y1), Width: uint16(w), Height: uint16(h)}
					xproto.PolyRectangle(s.xu.Conn(), xproto.Drawable(s.bufPixmapID), s.pinkGCID, []xproto.Rectangle{rect})
				}
			} else if tool == annotation.Circle {
				dx := s.currX - start.X
				dy := s.currY - start.Y
				r := int(math.Sqrt(float64(dx*dx + dy*dy)))
				if r > 0 {
					arc := xproto.Arc{
						X: int16(start.X - r), Y: int16(start.Y - r),
						Width: uint16(r * 2), Height: uint16(r * 2),
						Angle1: 0, Angle2: 360 * 64,
					}
					xproto.PolyArc(s.xu.Conn(), xproto.Drawable(s.bufPixmapID), s.pinkGCID, []xproto.Arc{arc})
				}
			}
		}
	}

	// Draw text input cursor indicator
	if s.ann.IsTextActive() {
		cfg := s.ann.Config()
		text := s.ann.TextBuffer()
		cursor := " "
		if time.Now().UnixNano()/500000000%2 == 0 {
			cursor = "_"
		}
		textToShow := text + cursor
		scale := cfg.FontScale
		textW := len(textToShow)*6*scale + 6
		textH := 7*scale + 4
		textImg := image.NewRGBA(image.Rect(0, 0, textW, textH))
		pinkColorRGBA := color.RGBA{R: 255, G: 0, B: 127, A: 255}

		for dy := 0; dy < textH; dy++ {
			for dx := 0; dx < textW; dx++ {
				textImg.Set(dx, dy, color.Black)
			}
		}
		annotation.DrawStringScaled(textImg, textToShow, 3, 2, pinkColorRGBA, scale)

		anchor := s.ann.TextAnchor()
		textBGRA := imageToBGRA(textImg)
		xproto.PutImage(
			s.xu.Conn(),
			xproto.ImageFormatZPixmap,
			xproto.Drawable(s.bufPixmapID),
			s.gcID,
			uint16(textW),
			uint16(textH),
			int16(anchor.X),
			int16(anchor.Y),
			0,
			s.screen.RootDepth,
			textBGRA,
		)
	}
}

func (s *regionState) handleButtonPress(X *xgbutil.XUtil, ev xevent.ButtonPressEvent) {
	if ev.Detail == 1 { // Left Mouse Button -> Crop Selection
		if !s.ann.IsDoodling() && !s.ann.IsTextActive() {
			s.dragStart = true
			s.startX = int(ev.EventX)
			s.startY = int(ev.EventY)
			s.currX = s.startX
			s.currY = s.startY
			s.redraw()
		}
	} else if ev.Detail == 3 { // Right Mouse Button -> Annotate/Doodle
		consumed, _ := s.ann.HandleEvent(annotation.InputEvent{
			Kind:   annotation.Press,
			X:      int(ev.EventX),
			Y:      int(ev.EventY),
			Button: 3,
			Mods:   ev.State,
		})
		if consumed {
			s.currX = int(ev.EventX)
			s.currY = int(ev.EventY)
			s.redraw()
		}
	}
}

func (s *regionState) handleButtonRelease(X *xgbutil.XUtil, ev xevent.ButtonReleaseEvent) {
	if ev.Detail == 1 && s.dragStart { // Left Mouse Button release
		s.dragStart = false
		s.currX = int(ev.EventX)
		s.currY = int(ev.EventY)
		s.selected = true
		xevent.Quit(s.xu)
	} else if ev.Detail == 3 && s.ann.IsDoodling() { // Right Mouse Button release
		consumed, _ := s.ann.HandleEvent(annotation.InputEvent{
			Kind:   annotation.Release,
			X:      int(ev.EventX),
			Y:      int(ev.EventY),
			Button: 3,
		})
		if consumed {
			s.redraw()
		}
	}
}

func (s *regionState) handleMotionNotify(X *xgbutil.XUtil, ev xevent.MotionNotifyEvent) {
	s.currX = int(ev.EventX)
	s.currY = int(ev.EventY)

	if s.dragStart {
		s.redraw()
	} else if s.ann.IsDoodling() {
		// Snapshot last position before HandleEvent updates it
		last := s.ann.DoodleLast()
		consumed, _ := s.ann.HandleEvent(annotation.InputEvent{
			Kind: annotation.Motion,
			X:    int(ev.EventX),
			Y:    int(ev.EventY),
		})
		if consumed {
			if s.ann.Tool() == annotation.Doodle {
				// Doodle: fast path — draw segment directly with X11 PolyLine.
				// Avoids full composite upload + Go allocation per motion event.
				xproto.PolyLine(
					s.xu.Conn(),
					xproto.CoordModeOrigin,
					xproto.Drawable(s.bufPixmapID),
					s.pinkGCID,
					[]xproto.Point{
						{X: int16(last.X), Y: int16(last.Y)},
						{X: int16(s.currX), Y: int16(s.currY)},
					},
				)
				xproto.CopyArea(
					s.xu.Conn(),
					xproto.Drawable(s.bufPixmapID),
					xproto.Drawable(s.winID),
					s.gcID,
					0, 0, 0, 0,
					uint16(s.screenWidth), uint16(s.screenHeight),
				)
			} else {
				// Rect/Circle: use full redraw (reset + preview outline)
				s.redraw()
			}
		}
	} else {
		s.redraw()
	}
}

func (s *regionState) handleKeyPress(X *xgbutil.XUtil, ev xevent.KeyPressEvent) {
	mods := ev.State
	keycode := ev.Detail
	keyStr := keybind.LookupString(s.xu, mods, keycode)

	handled, needsRedraw := s.ann.HandleEvent(annotation.InputEvent{
		Kind:    annotation.Key,
		KeyStr:  keyStr,
		Keycode: uint8(keycode),
		Mods:    mods,
	})
	if handled {
		if needsRedraw {
			s.redraw()
		}
		return
	}

	// --- Normal Mode keys ---

	// Dynamic clipboard actions
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
		if !s.dragStart {
			// Entire screen selection if no active drag crop
			s.startX = 0
			s.startY = 0
			s.currX = s.screenWidth
			s.currY = s.screenHeight
		}
		s.dragStart = false
		xevent.Quit(s.xu)
		return
	}

	// Normal mode keys (Escape, q, Q)
	if keyStr == "Escape" || keyStr == "q" || keyStr == "Q" {
		s.aborted = true
		xevent.Quit(s.xu)
	}
}
