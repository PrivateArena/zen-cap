package capture

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"log"
	"math"
	"strings"
	"time"

	"github.com/jezek/xgb/xproto"
	"github.com/jezek/xgbutil"
	"github.com/jezek/xgbutil/keybind"
	"github.com/jezek/xgbutil/xevent"
)

// TestHookWindowID is a global hook used by automated tests to locate the selection window.
var TestHookWindowID uint32

// Character bitmaps for custom 3x5 pixel font
var charBitmaps = map[rune][5]byte{
	'0': {0x7, 0x5, 0x5, 0x5, 0x7}, // 111, 101, 101, 101, 111
	'1': {0x2, 0x6, 0x2, 0x2, 0x7},
	'2': {0x7, 0x1, 0x7, 0x4, 0x7},
	'3': {0x7, 0x1, 0x7, 0x1, 0x7},
	'4': {0x5, 0x5, 0x7, 0x1, 0x1},
	'5': {0x7, 0x4, 0x7, 0x1, 0x7},
	'6': {0x7, 0x4, 0x7, 0x5, 0x7},
	'7': {0x7, 0x1, 0x2, 0x4, 0x4},
	'8': {0x7, 0x5, 0x7, 0x5, 0x7},
	'9': {0x7, 0x5, 0x7, 0x1, 0x7},
	' ': {0x0, 0x0, 0x0, 0x0, 0x0},
	',': {0x0, 0x0, 0x0, 0x2, 0x4},
	'X': {0x5, 0x5, 0x2, 0x5, 0x5},
	'#': {0x5, 0x7, 0x5, 0x7, 0x5},
}

// drawChar draws a single character in our 3x5 font
func drawChar(img draw.Image, char rune, x, y int, col color.Color) {
	bitmap, ok := charBitmaps[char]
	if !ok {
		bitmap = charBitmaps[' ']
	}
	for row := 0; row < 5; row++ {
		val := bitmap[row]
		for colIdx := 0; colIdx < 3; colIdx++ {
			if (val >> (2 - colIdx)) & 1 == 1 {
				img.Set(x+colIdx, y+row, col)
			}
		}
	}
}

// drawString draws a string in our 3x5 font (centered at y, starts at x)
func drawString(img draw.Image, s string, x, y int, col color.Color) {
	currX := x
	for _, char := range strings.ToUpper(s) {
		drawChar(img, char, currX, y, col)
		currX += 4 // 3 pixels width + 1 pixel spacing
	}
}

// InteractiveSelectRegion captures the fullscreen, displays it in an override-redirect
// window, lets the user drag-and-drop to select a region, and returns the cropped image bounds.
// It also allows the user to draw annotations/doodles using the Right Mouse Button before selecting.
func InteractiveSelectRegion(fullImg image.Image) (image.Image, error) {
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

	// State variables
	var (
		dragStart = false
		startX    = 0
		startY    = 0
		currX     = 0
		currY     = 0
		selected  = false
		aborted   = false

		// Doodle variables
		doodling    = false
		lastDoodleX = 0
		lastDoodleY = 0
		annoTool    = "doodle" // "doodle", "rect", "circle"

		// Text input variables
		textInputActive    = false
		textInputX         = 0
		textInputY         = 0
		textInputBuffer    = ""
		lastRightClickTime time.Time

		// Font scale and undo history variables
		fontScale = 2 // Premium larger default text size
		history   = []*image.RGBA{copyImage(rgbaImg)}
	)

	// Redraw function using double buffering
	redraw := func() {
		// Copy base screenshot (with doodles drawn directly to it) from bgPixmap to buffer pixmap
		xproto.CopyArea(
			xu.Conn(),
			xproto.Drawable(bgPixmapID),
			xproto.Drawable(bufPixmapID),
			gcID,
			0, 0, // srcX, srcY
			0, 0, // dstX, dstY
			uint16(screenWidth), uint16(screenHeight),
		)

		if dragStart {
			x1 := int(math.Min(float64(startX), float64(currX)))
			y1 := int(math.Min(float64(startY), float64(currY)))
			w := int(math.Abs(float64(currX - startX)))
			h := int(math.Abs(float64(currY - startY)))

			// Draw 50% gray overlay on the four outer areas surrounding the selection
			if y1 > 0 {
				// Top band
				rect := xproto.Rectangle{X: 0, Y: 0, Width: uint16(screenWidth), Height: uint16(y1)}
				xproto.PolyFillRectangle(xu.Conn(), xproto.Drawable(bufPixmapID), overlayGCID, []xproto.Rectangle{rect})
			}
			if y1+h < screenHeight {
				// Bottom band
				rect := xproto.Rectangle{X: 0, Y: int16(y1 + h), Width: uint16(screenWidth), Height: uint16(screenHeight - (y1 + h))}
				xproto.PolyFillRectangle(xu.Conn(), xproto.Drawable(bufPixmapID), overlayGCID, []xproto.Rectangle{rect})
			}
			if x1 > 0 && h > 0 {
				// Left band
				rect := xproto.Rectangle{X: 0, Y: int16(y1), Width: uint16(x1), Height: uint16(h)}
				xproto.PolyFillRectangle(xu.Conn(), xproto.Drawable(bufPixmapID), overlayGCID, []xproto.Rectangle{rect})
			}
			if x1+w < screenWidth && h > 0 {
				// Right band
				rect := xproto.Rectangle{X: int16(x1 + w), Y: int16(y1), Width: uint16(screenWidth - (x1 + w)), Height: uint16(h)}
				xproto.PolyFillRectangle(xu.Conn(), xproto.Drawable(bufPixmapID), overlayGCID, []xproto.Rectangle{rect})
			}

			// Draw neon-cyan selection border rectangle
			if w > 0 && h > 0 {
				rect := xproto.Rectangle{X: int16(x1), Y: int16(y1), Width: uint16(w), Height: uint16(h)}
				xproto.PolyRectangle(xu.Conn(), xproto.Drawable(bufPixmapID), cyanGCID, []xproto.Rectangle{rect})
			}
		} else {
			// No active crop drag: dark overlay over entire screen
			rect := xproto.Rectangle{X: 0, Y: 0, Width: uint16(screenWidth), Height: uint16(screenHeight)}
			xproto.PolyFillRectangle(xu.Conn(), xproto.Drawable(bufPixmapID), overlayGCID, []xproto.Rectangle{rect})
		}

		// Draw preview of active shape drawing
		if doodling && (annoTool == "rect" || annoTool == "circle") {
			x1 := int(math.Min(float64(lastDoodleX), float64(currX)))
			y1 := int(math.Min(float64(lastDoodleY), float64(currY)))
			w := int(math.Abs(float64(currX - lastDoodleX)))
			h := int(math.Abs(float64(currY - lastDoodleY)))

			if w > 0 && h > 0 {
				if annoTool == "rect" {
					rect := xproto.Rectangle{X: int16(x1), Y: int16(y1), Width: uint16(w), Height: uint16(h)}
					xproto.PolyRectangle(xu.Conn(), xproto.Drawable(bufPixmapID), pinkGCID, []xproto.Rectangle{rect})
				} else if annoTool == "circle" {
					dx := currX - lastDoodleX
					dy := currY - lastDoodleY
					r := int(math.Sqrt(float64(dx*dx + dy*dy)))
					if r > 0 {
						arc := xproto.Arc{
							X:      int16(lastDoodleX - r),
							Y:      int16(lastDoodleY - r),
							Width:  uint16(r * 2),
							Height: uint16(r * 2),
							Angle1: 0,
							Angle2: 360 * 64,
						}
						xproto.PolyArc(xu.Conn(), xproto.Drawable(bufPixmapID), pinkGCID, []xproto.Arc{arc})
					}
				}
			}
		}

		// Draw real-time blinking text input box with scale adjustment
		if textInputActive {
			cursor := "_"
			if time.Now().UnixNano()/500000000%2 == 0 {
				cursor = " "
			}
			textToShow := textInputBuffer + cursor
			textW := len(textToShow)*4*fontScale + 6
			textH := 9 * fontScale
			textImg := image.NewRGBA(image.Rect(0, 0, textW, textH))
			pinkColorRGBA := color.RGBA{R: 255, G: 0, B: 127, A: 255}

			for dy := 0; dy < textH; dy++ {
				for dx := 0; dx < textW; dx++ {
					textImg.Set(dx, dy, color.Black)
				}
			}
			drawStringScaled(textImg, textToShow, 3, 2, pinkColorRGBA, fontScale)

			textBGRA := imageToBGRA(textImg)
			xproto.PutImage(
				xu.Conn(),
				xproto.ImageFormatZPixmap,
				xproto.Drawable(bufPixmapID),
				gcID,
				uint16(textW),
				uint16(textH),
				int16(textInputX),
				int16(textInputY),
				0,
				screen.RootDepth,
				textBGRA,
			)
		}

		// Draw circular magnifier loupe floating above/below cursor
		lx := currX + 20
		ly := currY - 140
		if ly < 10 {
			ly = currY + 20 // Flip below cursor
		}
		if lx+120 > screenWidth {
			lx = currX - 140 // Flip to left of cursor
		}
		if lx < 10 {
			lx = 10
		}
		if ly+120 > screenHeight {
			ly = screenHeight - 130
		}
		if ly < 10 {
			ly = 10
		}

		// Generate circular magnifier loupe image
		magImg := getMagnifierImage(rgbaImg, currX, currY, lx, ly, screenWidth, screenHeight, dragStart, startX, startY)
		magBGRA := imageToBGRA(magImg)

		// Upload magnifier to the buffer pixmap
		xproto.PutImage(
			xu.Conn(),
			xproto.ImageFormatZPixmap,
			xproto.Drawable(bufPixmapID),
			gcID,
			120, // width
			120, // height
			int16(lx),
			int16(ly),
			0, // leftPad
			screen.RootDepth,
			magBGRA,
		)

		// Copy complete buffer pixmap to the fullscreen window
		xproto.CopyArea(
			xu.Conn(),
			xproto.Drawable(bufPixmapID),
			xproto.Drawable(winID),
			gcID,
			0, 0,
			0, 0,
			uint16(screenWidth), uint16(screenHeight),
		)
	}

	// Register event handlers
	xevent.ButtonPressFun(func(X *xgbutil.XUtil, ev xevent.ButtonPressEvent) {
		if ev.Detail == 1 { // Left Mouse Button -> Crop Selection
			if !doodling && !textInputActive {
				dragStart = true
				startX = int(ev.EventX)
				startY = int(ev.EventY)
				currX = startX
				currY = startY
				redraw()
			}
		} else if ev.Detail == 3 { // Right Mouse Button -> Annotate/Doodle
			if !dragStart && !textInputActive {
				now := time.Now()
				if now.Sub(lastRightClickTime) < 300*time.Millisecond {
					// Double Right Click -> Text input mode!
					textInputActive = true
					textInputX = int(ev.EventX)
					textInputY = int(ev.EventY)
					textInputBuffer = ""
					redraw()
					return
				}
				lastRightClickTime = now

				// Normal right-click annotation setup
				doodling = true
				lastDoodleX = int(ev.EventX)
				lastDoodleY = int(ev.EventY)
				currX = lastDoodleX
				currY = lastDoodleY

				// Select shape tool based on modifiers
				if ev.State&xproto.ModMaskShift != 0 {
					annoTool = "rect"
				} else if ev.State&xproto.ModMaskControl != 0 {
					annoTool = "circle"
				} else {
					annoTool = "doodle"
				}
			}
		}
	}).Connect(xu, winID)

	xevent.ButtonReleaseFun(func(X *xgbutil.XUtil, ev xevent.ButtonReleaseEvent) {
		if ev.Detail == 1 && dragStart { // Left Mouse Button release
			dragStart = false
			currX = int(ev.EventX)
			currY = int(ev.EventY)
			selected = true
			xevent.Quit(xu)
		} else if ev.Detail == 3 && doodling { // Right Mouse Button release
			doodling = false
			cx := int(ev.EventX)
			cy := int(ev.EventY)
			pinkColorRGBA := color.RGBA{R: 255, G: 0, B: 127, A: 255}

			if annoTool == "rect" {
				x1 := int(math.Min(float64(lastDoodleX), float64(cx)))
				y1 := int(math.Min(float64(lastDoodleY), float64(cy)))
				w := int(math.Abs(float64(cx - lastDoodleX)))
				h := int(math.Abs(float64(cy - lastDoodleY)))
				if w > 0 && h > 0 {
					// 1. Burn into Go image permanently
					drawRect(rgbaImg, lastDoodleX, lastDoodleY, cx, cy, pinkColorRGBA, int(brushThickness))
					// 2. Burn into X11 background pixmap
					rect := xproto.Rectangle{X: int16(x1), Y: int16(y1), Width: uint16(w), Height: uint16(h)}
					xproto.PolyRectangle(xu.Conn(), xproto.Drawable(bgPixmapID), pinkGCID, []xproto.Rectangle{rect})
				}
			} else if annoTool == "circle" {
				dx := cx - lastDoodleX
				dy := cy - lastDoodleY
				r := int(math.Sqrt(float64(dx*dx + dy*dy)))
				if r > 0 {
					// 1. Burn into Go image permanently
					drawCircle(rgbaImg, lastDoodleX, lastDoodleY, r, pinkColorRGBA, int(brushThickness))
					// 2. Burn into X11 background pixmap
					arc := xproto.Arc{
						X:      int16(lastDoodleX - r),
						Y:      int16(lastDoodleY - r),
						Width:  uint16(r * 2),
						Height: uint16(r * 2),
						Angle1: 0,
						Angle2: 360 * 64,
					}
					xproto.PolyArc(xu.Conn(), xproto.Drawable(bgPixmapID), pinkGCID, []xproto.Arc{arc})
				}
			}

			// Push state to history after completing doodle, rectangle, or circle annotation!
			history = append(history, copyImage(rgbaImg))
			redraw()
		}
	}).Connect(xu, winID)

	xevent.MotionNotifyFun(func(X *xgbutil.XUtil, ev xevent.MotionNotifyEvent) {
		currX = int(ev.EventX)
		currY = int(ev.EventY)

		if dragStart {
			redraw()
		} else if doodling {
			cx := int(ev.EventX)
			cy := int(ev.EventY)

			if annoTool == "doodle" {
				// 1. Draw line in background pixmap using X11 Neon Pink GC
				xproto.PolyLine(
					xu.Conn(),
					xproto.CoordModeOrigin,
					xproto.Drawable(bgPixmapID),
					pinkGCID,
					[]xproto.Point{
						{X: int16(lastDoodleX), Y: int16(lastDoodleY)},
						{X: int16(cx), Y: int16(cy)},
					},
				)

				// 2. Draw line on Go Image to burn the annotation into the final capture
				pinkColorRGBA := color.RGBA{R: 255, G: 0, B: 127, A: 255}
				drawLine(rgbaImg, lastDoodleX, lastDoodleY, cx, cy, pinkColorRGBA, int(brushThickness))

				lastDoodleX = cx
				lastDoodleY = cy
				redraw()
			} else {
				// Shape tools (rect / circle): just update preview coordinates
				redraw()
			}
		} else {
			// Just moving: redraw to update magnifier position
			redraw()
		}
	}).Connect(xu, winID)

	xevent.ExposeFun(func(X *xgbutil.XUtil, ev xevent.ExposeEvent) {
		redraw()
	}).Connect(xu, winID)

	// Safe keys handling (Escape, q, Q, Ctrl+Shift+X) and text input capturing
	xevent.KeyPressFun(func(X *xgbutil.XUtil, ev xevent.KeyPressEvent) {
		mods := ev.State
		keycode := ev.Detail
		keyStr := keybind.LookupString(xu, mods, keycode)

		if textInputActive {
			// Check for Enter / Return
			if keyStr == "\r" || keyStr == "\n" || keyStr == "Return" || keyStr == "Enter" {
				// Commit text!
				if len(textInputBuffer) > 0 {
					pinkColorRGBA := color.RGBA{R: 255, G: 0, B: 127, A: 255}
					// Burn into Go image permanently with fontScale
					drawHUDTextScaled(rgbaImg, textInputBuffer, textInputX, textInputY, pinkColorRGBA, color.Black, fontScale)
					// Push to history before uploading!
					history = append(history, copyImage(rgbaImg))
					// Redraw background pixmap to include the committed text
					bgra := imageToBGRA(rgbaImg)
					uploadImageChunked(xu, xproto.Drawable(bgPixmapID), gcID, screen.RootDepth, screenWidth, screenHeight, bgra)
				}
				textInputActive = false
				textInputBuffer = ""
				redraw()
				return
			}

			// Check for Escape (Cancel typing)
			if keyStr == "Escape" {
				textInputActive = false
				textInputBuffer = ""
				redraw()
				return
			}

			// Check for Backspace
			if keyStr == "BackSpace" || keycode == 22 {
				if len(textInputBuffer) > 0 {
					textInputBuffer = textInputBuffer[:len(textInputBuffer)-1]
					redraw()
				}
				return
			}

			// Capture printable characters
			if len(keyStr) == 1 && keyStr[0] >= 32 && keyStr[0] <= 126 {
				textInputBuffer += keyStr
				redraw()
			}
			return
		}

		// --- Normal Mode keys ---

		// Undo: Ctrl+Z
		if (keyStr == "z" || keyStr == "Z") && (mods&xproto.ModMaskControl != 0) {
			if len(history) > 1 {
				history = history[:len(history)-1]
				rgbaImg = copyImage(history[len(history)-1])
				// Sync to background X11 pixmap
				bgra := imageToBGRA(rgbaImg)
				uploadImageChunked(xu, xproto.Drawable(bgPixmapID), gcID, screen.RootDepth, screenWidth, screenHeight, bgra)
				redraw()
			}
			return
		}

		// Adjust brush thickness and font scale: Ctrl+Plus or Ctrl+Minus
		if (keyStr == "equal" || keyStr == "plus" || keyStr == "+") && (mods&xproto.ModMaskControl != 0) {
			fontScale++
			brushThickness += 2
			// Update X11 GC line width
			xproto.ChangeGC(xu.Conn(), pinkGCID, xproto.GcLineWidth, []uint32{brushThickness})
			redraw()
			return
		}
		if (keyStr == "minus" || keyStr == "hyphen" || keyStr == "-") && (mods&xproto.ModMaskControl != 0) {
			if fontScale > 1 {
				fontScale--
			}
			if brushThickness > 2 {
				brushThickness -= 2
			}
			// Update X11 GC line width
			xproto.ChangeGC(xu.Conn(), pinkGCID, xproto.GcLineWidth, []uint32{brushThickness})
			redraw()
			return
		}

		// Normal mode keys (Escape, q, Ctrl+Shift+X)
		if keyStr == "Escape" || keyStr == "q" || keyStr == "Q" {
			aborted = true
			xevent.Quit(xu)
		}
	}).Connect(xu, winID)

	// Extra safety abort binding specifically for Ctrl+Shift+X
	keybind.KeyPressFun(func(X *xgbutil.XUtil, ev xevent.KeyPressEvent) {
		aborted = true
		xevent.Quit(xu)
	}).Connect(xu, winID, "Control-Shift-x", true)

	keybind.KeyPressFun(func(X *xgbutil.XUtil, ev xevent.KeyPressEvent) {
		aborted = true
		xevent.Quit(xu)
	}).Connect(xu, winID, "Control-Shift-X", true)

	// Run X11 event loop until Quit is called
	xevent.Main(xu)

	// Destroy window immediately
	windowNeedsDestroy = false
	xproto.DestroyWindow(xu.Conn(), winID)

	if aborted {
		return nil, fmt.Errorf("region capture aborted by user safety key")
	}

	if !selected {
		return nil, fmt.Errorf("no region was selected")
	}

	// Calculate final selection coordinates
	x1 := int(math.Min(float64(startX), float64(currX)))
	y1 := int(math.Min(float64(startY), float64(currY)))
	w := int(math.Abs(float64(currX - startX)))
	h := int(math.Abs(float64(currY - startY)))

	// If region is too small (e.g. less than 5x5 pixels), assume it was an accidental click and abort
	if w < 5 || h < 5 {
		return nil, fmt.Errorf("selected region too small (%dx%d)", w, h)
	}

	// Crop the annotated fullscreen screenshot image
	cropped := rgbaImg.SubImage(image.Rect(x1, y1, x1+w, y1+h))
	return cropped, nil
}

// Convert image.Image to BGRA bytes for X11
func imageToBGRA(img image.Image) []byte {
	bounds := img.Bounds()
	w, h := bounds.Dx(), bounds.Dy()
	data := make([]byte, w*h*4)
	idx := 0
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			r, g, b, a := img.At(x, y).RGBA()
			// Go's RGBA() returns alpha-premultiplied values in [0, 65535]
			data[idx] = byte(b >> 8)
			data[idx+1] = byte(g >> 8)
			data[idx+2] = byte(r >> 8)
			data[idx+3] = byte(a >> 8)
			idx += 4
		}
	}
	return data
}

// Upload image data in chunks to prevent X11 packet size limits
func uploadImageChunked(xu *xgbutil.XUtil, drawable xproto.Drawable, gc xproto.Gcontext, depth byte, w, h int, bgraData []byte) error {
	rowBytes := w * 4

	// Determine safe chunkRows based on X connection's MaximumRequestLength (which is in 4-byte units)
	maxReq := 0
	setup := xproto.Setup(xu.Conn())
	if setup != nil {
		maxReq = int(setup.MaximumRequestLength) * 4
	}
	if maxReq <= 0 {
		maxReq = 65536 * 4 // Core X11 fallback (256KB)
	}
	// Subtract 1024 bytes buffer for X11 packet headers (PutImage is a few dozen bytes)
	maxDataBytes := maxReq - 1024
	chunkRows := maxDataBytes / rowBytes
	if chunkRows < 1 {
		chunkRows = 1
	}

	for y := 0; y < h; y += chunkRows {
		rows := chunkRows
		if y+rows > h {
			rows = h - y
		}
		offset := y * rowBytes
		length := rows * rowBytes

		err := xproto.PutImageChecked(
			xu.Conn(),
			xproto.ImageFormatZPixmap,
			drawable,
			gc,
			uint16(w),
			uint16(rows),
			0,         // dstX
			int16(y),  // dstY
			0,         // leftPad
			depth,
			bgraData[offset:offset+length],
		).Check()
		if err != nil {
			return fmt.Errorf("PutImage failed at row %d (chunk size %d rows): %w", y, rows, err)
		}
	}
	return nil
}

// drawLine draws a thick line on a Go draw.Image using Bresenham's line algorithm with brush thickness.
func drawLine(img draw.Image, x0, y0, x1, y1 int, col color.Color, thickness int) {
	dx := abs(x1 - x0)
	dy := abs(y1 - y0)
	sx, sy := 1, 1
	if x0 > x1 {
		sx = -1
	}
	if y0 > y1 {
		sy = -1
	}
	err := dx - dy

	for {
		// Draw brush of specified thickness
		for ty := -thickness / 2; ty <= thickness/2; ty++ {
			for tx := -thickness / 2; tx <= thickness/2; tx++ {
				// Circle brush constraint
				if tx*tx+ty*ty <= (thickness*thickness)/4 {
					img.Set(x0+tx, y0+ty, col)
				}
			}
		}

		if x0 == x1 && y0 == y1 {
			break
		}
		e2 := 2 * err
		if e2 > -dy {
			err -= dy
			x0 += sx
		}
		if e2 < dx {
			err += dx
			y0 += sy
		}
	}
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// getMagnifierImage generates a 120x120 circle magnifier image with a neon-cyan bezel, neon-pink crosshairs,
// and coordinates or current crop dimensions rendered in a custom 3x5 font at the bottom.
func getMagnifierImage(rgbaImg *image.RGBA, mx, my, lx, ly, screenWidth, screenHeight int, dragging bool, startX, startY int) *image.RGBA {
	mag := image.NewRGBA(image.Rect(0, 0, 120, 120))
	pinkColor := color.RGBA{R: 255, G: 0, B: 127, A: 255}
	cyanColor := color.RGBA{R: 0, G: 240, B: 255, A: 255}

	for dy := 0; dy < 120; dy++ {
		for dx := 0; dx < 120; dx++ {
			rx := dx - 60
			ry := dy - 60
			distSq := rx*rx + ry*ry

			var col color.Color
			if distSq > 60*60 {
				// Outside the circular magnifier glass: draw background screen pixel
				sx := lx + dx
				sy := ly + dy
				if sx >= 0 && sx < screenWidth && sy >= 0 && sy < screenHeight {
					col = rgbaImg.At(sx, sy)
				} else {
					col = color.Black
				}
			} else if distSq >= 58*58 && distSq <= 60*60 {
				// Outer bezel: Neon cyan
				col = cyanColor
			} else {
				// Inside the circular magnifier glass: 4x nearest-neighbor magnification
				// Map dx, dy [0..120] to source offsets [-15..15]
				sx := mx - 15 + dx/4
				sy := my - 15 + dy/4
				if sx >= 0 && sx < screenWidth && sy >= 0 && sy < screenHeight {
					col = rgbaImg.At(sx, sy)
				} else {
					col = color.Black
				}

				// Draw circular central crosshairs (Neon pink, length 10px in 4 directions)
				if (rx == 0 && ry >= -10 && ry <= 10) || (ry == 0 && rx >= -10 && rx <= 10) {
					col = pinkColor
				}
			}
			mag.Set(dx, dy, col)
		}
	}

	// Determine info string to display in the bottom HUD bar
	var infoStr string
	if dragging {
		w := int(math.Abs(float64(mx - startX)))
		h := int(math.Abs(float64(my - startY)))
		infoStr = fmt.Sprintf("%dX%d", w, h)
	} else {
		infoStr = fmt.Sprintf("%d,%d", mx, my)
	}

	// Render HUD background bar inside the bottom of the magnifier (transparent black)
	hudWidth := len(infoStr)*4 + 6
	hudStartX := (120 - hudWidth) / 2
	for ty := 95; ty < 103; ty++ {
		for tx := hudStartX; tx < hudStartX+hudWidth; tx++ {
			rx := tx - 60
			ry := ty - 60
			if rx*rx+ry*ry < 56*56 {
				mag.Set(tx, ty, color.RGBA{R: 0, G: 0, B: 0, A: 220})
			}
		}
	}

	// Draw custom 3x5 pixel font coordinates/dims centered
	drawString(mag, infoStr, hudStartX+3, 97, color.White)

	return mag
}

// drawRect draws a rectangle with thickness on a draw.Image
func drawRect(img draw.Image, x0, y0, x1, y1 int, col color.Color, thickness int) {
	// Draw the 4 edges using drawLine
	drawLine(img, x0, y0, x1, y0, col, thickness)
	drawLine(img, x1, y0, x1, y1, col, thickness)
	drawLine(img, x1, y1, x0, y1, col, thickness)
	drawLine(img, x0, y1, x0, y0, col, thickness)
}

// drawCircle draws a circle with thickness on a draw.Image
func drawCircle(img draw.Image, cx, cy, r int, col color.Color, thickness int) {
	for y := cy - r - thickness; y <= cy + r + thickness; y++ {
		for x := cx - r - thickness; x <= cx + r + thickness; x++ {
			dx := x - cx
			dy := y - cy
			dist := math.Sqrt(float64(dx*dx + dy*dy))
			if math.Abs(dist-float64(r)) <= float64(thickness)/2.0 {
				img.Set(x, y, col)
			}
		}
	}
}

// drawHUDTextScaled draws a string scaled onto a draw.Image inside a solid background rectangle
func drawHUDTextScaled(img draw.Image, text string, x, y int, fg, bg color.Color, scale int) {
	w := len(text)*4*scale + 6
	h := 9 * scale
	for dy := 0; dy < h; dy++ {
		for dx := 0; dx < w; dx++ {
			img.Set(x+dx, y+dy, bg)
		}
	}
	drawStringScaled(img, text, x+3, y+2, fg, scale)
}

// drawCharScaled draws a single character scaled by a factor
func drawCharScaled(img draw.Image, char rune, x, y int, col color.Color, scale int) {
	bitmap, ok := charBitmaps[char]
	if !ok {
		bitmap = charBitmaps[' ']
	}
	for row := 0; row < 5; row++ {
		val := bitmap[row]
		for colIdx := 0; colIdx < 3; colIdx++ {
			if (val >> (2 - colIdx)) & 1 == 1 {
				// Draw a scale x scale block
				for sy := 0; sy < scale; sy++ {
					for sx := 0; sx < scale; sx++ {
						img.Set(x+colIdx*scale+sx, y+row*scale+sy, col)
					}
				}
			}
		}
	}
}

// drawStringScaled draws a string scaled by a factor
func drawStringScaled(img draw.Image, s string, x, y int, col color.Color, scale int) {
	currX := x
	for _, char := range strings.ToUpper(s) {
		drawCharScaled(img, char, currX, y, col, scale)
		currX += 4 * scale // 3 pixels width + 1 pixel spacing * scale
	}
}

// copyImage creates a deep copy of an *image.RGBA
func copyImage(src *image.RGBA) *image.RGBA {
	bounds := src.Bounds()
	dst := image.NewRGBA(bounds)
	copy(dst.Pix, src.Pix)
	return dst
}
