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
)

// TestHookWindowID is a global hook used by automated tests to locate the selection window.
var TestHookWindowID uint32

type regionState struct {
	xu                 *xgbutil.XUtil
	winID              xproto.Window
	gcID               xproto.Gcontext
	bgPixmapID         xproto.Pixmap
	bufPixmapID        xproto.Pixmap
	cyanGCID           xproto.Gcontext
	pinkGCID           xproto.Gcontext
	overlayGCID        xproto.Gcontext
	screenWidth        int
	screenHeight       int
	screen             *xproto.ScreenInfo
	rgbaImg            *image.RGBA
	dragStart          bool
	startX             int
	startY             int
	currX              int
	currY              int
	selected           bool
	aborted            bool
	doodling           bool
	lastDoodleX        int
	lastDoodleY        int
	annoTool           string
	textInputActive    bool
	textInputX         int
	textInputY         int
	textInputBuffer    string
	lastRightClickTime time.Time
	fontScale          int
	brushThickness     uint32
	history            []*image.RGBA
	clipboardAction    string // "image", "path", "ocr", "translate"
}

// InteractiveSelectRegion is a backward-compatible wrapper around InteractiveSelectRegionExt.
func InteractiveSelectRegion(fullImg image.Image) (image.Image, error) {
	return InteractiveSelectRegionExt(fullImg, nil)
}

// InteractiveSelectRegionExt captures the fullscreen, displays it in an override-redirect
// window, lets the user drag-and-drop to select a region, and returns the cropped image bounds.
// It also allows the user to draw annotations/doodles using the Right Mouse Button before selecting.
// It populates outClipboardAction if a dynamic shortcut was pressed.
func InteractiveSelectRegionExt(fullImg image.Image, outClipboardAction *string) (image.Image, error) {
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
		xu:             xu,
		winID:          winID,
		gcID:           gcID,
		bgPixmapID:     bgPixmapID,
		bufPixmapID:    bufPixmapID,
		cyanGCID:       cyanGCID,
		pinkGCID:       pinkGCID,
		overlayGCID:    overlayGCID,
		screenWidth:    screenWidth,
		screenHeight:   screenHeight,
		screen:         screen,
		rgbaImg:        rgbaImg,
		annoTool:       "doodle",
		fontScale:      4,
		brushThickness: brushThickness,
		history:        []*image.RGBA{copyImage(rgbaImg)},
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

	// Crop the annotated fullscreen screenshot image
	cropped := state.rgbaImg.SubImage(image.Rect(x1, y1, x1+w, y1+h))
	if outClipboardAction != nil {
		*outClipboardAction = state.clipboardAction
	}
	return cropped, nil
}

// Redraw function using double buffering
func (s *regionState) redraw() {
	// Copy base screenshot (with doodles drawn directly to it) from bgPixmap to buffer pixmap
	xproto.CopyArea(
		s.xu.Conn(),
		xproto.Drawable(s.bgPixmapID),
		xproto.Drawable(s.bufPixmapID),
		s.gcID,
		0, 0, // srcX, srcY
		0, 0, // dstX, dstY
		uint16(s.screenWidth), uint16(s.screenHeight),
	)

	if s.dragStart {
		x1 := int(math.Min(float64(s.startX), float64(s.currX)))
		y1 := int(math.Min(float64(s.startY), float64(s.currY)))
		w := int(math.Abs(float64(s.currX - s.startX)))
		h := int(math.Abs(float64(s.currY - s.startY)))

		// Draw 50% gray overlay on the four outer areas surrounding the selection
		if y1 > 0 {
			// Top band
			rect := xproto.Rectangle{X: 0, Y: 0, Width: uint16(s.screenWidth), Height: uint16(y1)}
			xproto.PolyFillRectangle(s.xu.Conn(), xproto.Drawable(s.bufPixmapID), s.overlayGCID, []xproto.Rectangle{rect})
		}
		if y1+h < s.screenHeight {
			// Bottom band
			rect := xproto.Rectangle{X: 0, Y: int16(y1 + h), Width: uint16(s.screenWidth), Height: uint16(s.screenHeight - (y1 + h))}
			xproto.PolyFillRectangle(s.xu.Conn(), xproto.Drawable(s.bufPixmapID), s.overlayGCID, []xproto.Rectangle{rect})
		}
		if x1 > 0 && h > 0 {
			// Left band
			rect := xproto.Rectangle{X: 0, Y: int16(y1), Width: uint16(x1), Height: uint16(h)}
			xproto.PolyFillRectangle(s.xu.Conn(), xproto.Drawable(s.bufPixmapID), s.overlayGCID, []xproto.Rectangle{rect})
		}
		if x1+w < s.screenWidth && h > 0 {
			// Right band
			rect := xproto.Rectangle{X: int16(x1 + w), Y: int16(y1), Width: uint16(s.screenWidth - (x1 + w)), Height: uint16(h)}
			xproto.PolyFillRectangle(s.xu.Conn(), xproto.Drawable(s.bufPixmapID), s.overlayGCID, []xproto.Rectangle{rect})
		}

		// Draw neon-cyan selection border rectangle
		if w > 0 && h > 0 {
			rect := xproto.Rectangle{X: int16(x1), Y: int16(y1), Width: uint16(w), Height: uint16(h)}
			xproto.PolyRectangle(s.xu.Conn(), xproto.Drawable(s.bufPixmapID), s.cyanGCID, []xproto.Rectangle{rect})
		}
	} else {
		// No active crop drag: dark overlay over entire screen
		rect := xproto.Rectangle{X: 0, Y: 0, Width: uint16(s.screenWidth), Height: uint16(s.screenHeight)}
		xproto.PolyFillRectangle(s.xu.Conn(), xproto.Drawable(s.bufPixmapID), s.overlayGCID, []xproto.Rectangle{rect})
	}

	// Draw preview of active shape drawing
	if s.doodling && (s.annoTool == "rect" || s.annoTool == "circle") {
		x1 := int(math.Min(float64(s.lastDoodleX), float64(s.currX)))
		y1 := int(math.Min(float64(s.lastDoodleY), float64(s.currY)))
		w := int(math.Abs(float64(s.currX - s.lastDoodleX)))
		h := int(math.Abs(float64(s.currY - s.lastDoodleY)))

		if w > 0 && h > 0 {
			if s.annoTool == "rect" {
				rect := xproto.Rectangle{X: int16(x1), Y: int16(y1), Width: uint16(w), Height: uint16(h)}
				xproto.PolyRectangle(s.xu.Conn(), xproto.Drawable(s.bufPixmapID), s.pinkGCID, []xproto.Rectangle{rect})
			} else if s.annoTool == "circle" {
				dx := s.currX - s.lastDoodleX
				dy := s.currY - s.lastDoodleY
				r := int(math.Sqrt(float64(dx*dx + dy*dy)))
				if r > 0 {
					arc := xproto.Arc{
						X:      int16(s.lastDoodleX - r),
						Y:      int16(s.lastDoodleY - r),
						Width:  uint16(r * 2),
						Height: uint16(r * 2),
						Angle1: 0,
						Angle2: 360 * 64,
					}
					xproto.PolyArc(s.xu.Conn(), xproto.Drawable(s.bufPixmapID), s.pinkGCID, []xproto.Arc{arc})
				}
			}
		}
	}

	// Draw real-time blinking text input box with scale adjustment
	if s.textInputActive {
		cursor := "_"
		if time.Now().UnixNano()/500000000%2 == 0 {
			cursor = " "
		}
		textToShow := s.textInputBuffer + cursor
		textW := len(textToShow)*6*s.fontScale + 6
		textH := 7*s.fontScale + 4
		textImg := image.NewRGBA(image.Rect(0, 0, textW, textH))
		pinkColorRGBA := color.RGBA{R: 255, G: 0, B: 127, A: 255}

		for dy := 0; dy < textH; dy++ {
			for dx := 0; dx < textW; dx++ {
				textImg.Set(dx, dy, color.Black)
			}
		}
		drawStringScaled(textImg, textToShow, 3, 2, pinkColorRGBA, s.fontScale)

		textBGRA := imageToBGRA(textImg)
		xproto.PutImage(
			s.xu.Conn(),
			xproto.ImageFormatZPixmap,
			xproto.Drawable(s.bufPixmapID),
			s.gcID,
			uint16(textW),
			uint16(textH),
			int16(s.textInputX),
			int16(s.textInputY),
			0,
			s.screen.RootDepth,
			textBGRA,
		)
	}

	// Draw circular magnifier loupe floating above/below cursor
	lx := s.currX + 20
	ly := s.currY - 140
	if ly < 10 {
		ly = s.currY + 20 // Flip below cursor
	}
	if lx+120 > s.screenWidth {
		lx = s.currX - 140 // Flip to left of cursor
	}
	if lx < 10 {
		lx = 10
	}
	if ly+120 > s.screenHeight {
		ly = s.screenHeight - 130
	}
	if ly < 10 {
		ly = 10
	}

	// Generate circular magnifier loupe image
	magImg := getMagnifierImage(s.rgbaImg, s.currX, s.currY, lx, ly, s.screenWidth, s.screenHeight, s.dragStart, s.startX, s.startY)
	magBGRA := imageToBGRA(magImg)

	// Upload magnifier to the buffer pixmap
	xproto.PutImage(
		s.xu.Conn(),
		xproto.ImageFormatZPixmap,
		xproto.Drawable(s.bufPixmapID),
		s.gcID,
		120, // width
		120, // height
		int16(lx),
		int16(ly),
		0, // leftPad
		s.screen.RootDepth,
		magBGRA,
	)

	// Copy complete buffer pixmap to the fullscreen window
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

func (s *regionState) handleButtonPress(X *xgbutil.XUtil, ev xevent.ButtonPressEvent) {
	if ev.Detail == 1 { // Left Mouse Button -> Crop Selection
		if !s.doodling && !s.textInputActive {
			s.dragStart = true
			s.startX = int(ev.EventX)
			s.startY = int(ev.EventY)
			s.currX = s.startX
			s.currY = s.startY
			s.redraw()
		}
	} else if ev.Detail == 3 { // Right Mouse Button -> Annotate/Doodle
		if !s.dragStart && !s.textInputActive {
			now := time.Now()
			if now.Sub(s.lastRightClickTime) < 300*time.Millisecond {
				// Double Right Click -> Text input mode!
				s.textInputActive = true
				s.textInputX = int(ev.EventX)
				s.textInputY = int(ev.EventY)
				s.textInputBuffer = ""
				s.redraw()
				return
			}
			s.lastRightClickTime = now

			// Normal right-click annotation setup
			s.doodling = true
			s.lastDoodleX = int(ev.EventX)
			s.lastDoodleY = int(ev.EventY)
			s.currX = s.lastDoodleX
			s.currY = s.lastDoodleY

			// Select shape tool based on modifiers
			if ev.State&xproto.ModMaskShift != 0 {
				s.annoTool = "rect"
			} else if ev.State&xproto.ModMaskControl != 0 {
				s.annoTool = "circle"
			} else {
				s.annoTool = "doodle"
			}
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
	} else if ev.Detail == 3 && s.doodling { // Right Mouse Button release
		s.doodling = false
		cx := int(ev.EventX)
		cy := int(ev.EventY)
		pinkColorRGBA := color.RGBA{R: 255, G: 0, B: 127, A: 255}

		if s.annoTool == "rect" {
			x1 := int(math.Min(float64(s.lastDoodleX), float64(cx)))
			y1 := int(math.Min(float64(s.lastDoodleY), float64(cy)))
			w := int(math.Abs(float64(cx - s.lastDoodleX)))
			h := int(math.Abs(float64(cy - s.lastDoodleY)))
			if w > 0 && h > 0 {
				// 1. Burn into Go image permanently
				drawRect(s.rgbaImg, s.lastDoodleX, s.lastDoodleY, cx, cy, pinkColorRGBA, int(s.brushThickness))
				// 2. Burn into X11 background pixmap
				rect := xproto.Rectangle{X: int16(x1), Y: int16(y1), Width: uint16(w), Height: uint16(h)}
				xproto.PolyRectangle(s.xu.Conn(), xproto.Drawable(s.bgPixmapID), s.pinkGCID, []xproto.Rectangle{rect})
			}
		} else if s.annoTool == "circle" {
			dx := cx - s.lastDoodleX
			dy := cy - s.lastDoodleY
			r := int(math.Sqrt(float64(dx*dx + dy*dy)))
			if r > 0 {
				// 1. Burn into Go image permanently
				drawCircle(s.rgbaImg, s.lastDoodleX, s.lastDoodleY, r, pinkColorRGBA, int(s.brushThickness))
				// 2. Burn into X11 background pixmap
				arc := xproto.Arc{
					X:      int16(s.lastDoodleX - r),
					Y:      int16(s.lastDoodleY - r),
					Width:  uint16(r * 2),
					Height: uint16(r * 2),
					Angle1: 0,
					Angle2: 360 * 64,
				}
				xproto.PolyArc(s.xu.Conn(), xproto.Drawable(s.bgPixmapID), s.pinkGCID, []xproto.Arc{arc})
			}
		}

		// Push state to history after completing doodle, rectangle, or circle annotation!
		s.history = append(s.history, copyImage(s.rgbaImg))
		s.redraw()
	}
}

func (s *regionState) handleMotionNotify(X *xgbutil.XUtil, ev xevent.MotionNotifyEvent) {
	s.currX = int(ev.EventX)
	s.currY = int(ev.EventY)

	if s.dragStart {
		s.redraw()
	} else if s.doodling {
		cx := int(ev.EventX)
		cy := int(ev.EventY)

		if s.annoTool == "doodle" {
			// 1. Draw line in background pixmap using X11 Neon Pink GC
			xproto.PolyLine(
				s.xu.Conn(),
				xproto.CoordModeOrigin,
				xproto.Drawable(s.bgPixmapID),
				s.pinkGCID,
				[]xproto.Point{
					{X: int16(s.lastDoodleX), Y: int16(s.lastDoodleY)},
					{X: int16(cx), Y: int16(cy)},
				},
			)

			// 2. Draw line on Go Image to burn the annotation into the final capture
			pinkColorRGBA := color.RGBA{R: 255, G: 0, B: 127, A: 255}
			drawLine(s.rgbaImg, s.lastDoodleX, s.lastDoodleY, cx, cy, pinkColorRGBA, int(s.brushThickness))

			s.lastDoodleX = cx
			s.lastDoodleY = cy
			s.redraw()
		} else {
			// Shape tools (rect / circle): just update preview coordinates
			s.redraw()
		}
	} else {
		// Just moving: redraw to update magnifier position
		s.redraw()
	}
}

func (s *regionState) handleKeyPress(X *xgbutil.XUtil, ev xevent.KeyPressEvent) {
	mods := ev.State
	keycode := ev.Detail
	keyStr := keybind.LookupString(s.xu, mods, keycode)

	if s.textInputActive {
		// Check for Enter / Return
		if keyStr == "\r" || keyStr == "\n" || keyStr == "Return" || keyStr == "Enter" {
			// Commit text!
			if len(s.textInputBuffer) > 0 {
				pinkColorRGBA := color.RGBA{R: 255, G: 0, B: 127, A: 255}
				// Burn into Go image permanently with fontScale
				drawHUDTextScaled(s.rgbaImg, s.textInputBuffer, s.textInputX, s.textInputY, pinkColorRGBA, color.Black, s.fontScale)
				// Push to history before uploading!
				s.history = append(s.history, copyImage(s.rgbaImg))
				// Redraw background pixmap to include the committed text
				bgra := imageToBGRA(s.rgbaImg)
				uploadImageChunked(s.xu, xproto.Drawable(s.bgPixmapID), s.gcID, s.screen.RootDepth, s.screenWidth, s.screenHeight, bgra)
			}
			s.textInputActive = false
			s.textInputBuffer = ""
			s.redraw()
			return
		}

		// Check for Escape (Cancel typing)
		if keyStr == "Escape" {
			s.textInputActive = false
			s.textInputBuffer = ""
			s.redraw()
			return
		}

		// Check for Backspace
		if keyStr == "BackSpace" || keycode == 22 {
			if len(s.textInputBuffer) > 0 {
				s.textInputBuffer = s.textInputBuffer[:len(s.textInputBuffer)-1]
				s.redraw()
			}
			return
		}

		// Capture printable characters
		if len(keyStr) == 1 && keyStr[0] >= 32 && keyStr[0] <= 126 {
			s.textInputBuffer += keyStr
			s.redraw()
		}
		return
	}

	// --- Normal Mode keys ---

	// Undo: Ctrl+Z
	if (keyStr == "z" || keyStr == "Z") && (mods&xproto.ModMaskControl != 0) {
		if len(s.history) > 1 {
			s.history = s.history[:len(s.history)-1]
			s.rgbaImg = copyImage(s.history[len(s.history)-1])
			// Sync to background X11 pixmap
			bgra := imageToBGRA(s.rgbaImg)
			uploadImageChunked(s.xu, xproto.Drawable(s.bgPixmapID), s.gcID, s.screen.RootDepth, s.screenWidth, s.screenHeight, bgra)
			s.redraw()
		}
		return
	}

	// Adjust brush thickness and font scale: Ctrl+Plus or Ctrl+Minus
	if (keyStr == "equal" || keyStr == "plus" || keyStr == "+") && (mods&xproto.ModMaskControl != 0) {
		s.fontScale++
		s.brushThickness += 2
		// Update X11 GC line width
		xproto.ChangeGC(s.xu.Conn(), s.pinkGCID, xproto.GcLineWidth, []uint32{s.brushThickness})
		s.redraw()
		return
	}
	if (keyStr == "minus" || keyStr == "hyphen" || keyStr == "-") && (mods&xproto.ModMaskControl != 0) {
		if s.fontScale > 1 {
			s.fontScale--
		}
		if s.brushThickness > 2 {
			s.brushThickness -= 2
		}
		// Update X11 GC line width
		xproto.ChangeGC(s.xu.Conn(), s.pinkGCID, xproto.GcLineWidth, []uint32{s.brushThickness})
		s.redraw()
		return
	}

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
