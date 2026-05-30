package capture

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"log"
	"math"

	"github.com/jezek/xgb/xproto"
	"github.com/jezek/xgbutil"
	"github.com/jezek/xgbutil/keybind"
	"github.com/jezek/xgbutil/xevent"
)

// TestHookWindowID is a global hook used by automated tests to locate the selection window.
var TestHookWindowID uint32

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
			if !doodling {
				dragStart = true
				startX = int(ev.EventX)
				startY = int(ev.EventY)
				currX = startX
				currY = startY
				redraw()
			}
		} else if ev.Detail == 3 { // Right Mouse Button -> Annotate/Doodle
			if !dragStart {
				doodling = true
				lastDoodleX = int(ev.EventX)
				lastDoodleY = int(ev.EventY)
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
		}
	}).Connect(xu, winID)

	xevent.MotionNotifyFun(func(X *xgbutil.XUtil, ev xevent.MotionNotifyEvent) {
		if dragStart {
			currX = int(ev.EventX)
			currY = int(ev.EventY)
			redraw()
		} else if doodling {
			cx := int(ev.EventX)
			cy := int(ev.EventY)

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
		}
	}).Connect(xu, winID)

	xevent.ExposeFun(func(X *xgbutil.XUtil, ev xevent.ExposeEvent) {
		redraw()
	}).Connect(xu, winID)

	// Safe keys handling (Escape, q, Q, Ctrl+Shift+X)
	abortHandler := func(X *xgbutil.XUtil, ev xevent.KeyPressEvent) {
		aborted = true
		xevent.Quit(xu)
	}

	// Bind Escape key
	keybind.KeyPressFun(abortHandler).Connect(xu, winID, "Escape", true)
	// Bind Q key
	keybind.KeyPressFun(abortHandler).Connect(xu, winID, "q", true)
	keybind.KeyPressFun(abortHandler).Connect(xu, winID, "Q", true)
	// Bind Ctrl+Shift+X key
	keybind.KeyPressFun(abortHandler).Connect(xu, winID, "Control-Shift-x", true)
	keybind.KeyPressFun(abortHandler).Connect(xu, winID, "Control-Shift-X", true)

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
