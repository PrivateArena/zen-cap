package magnifier

import (
	"fmt"

	"github.com/jezek/xgb/xproto"
	"github.com/jezek/xgbutil"
)

// overlayWindow wraps an X11 override-redirect window used for rendering
// the magnified content. It holds a double-buffer pixmap for flicker-free
// blitting.
type overlayWindow struct {
	xu        *xgbutil.XUtil
	win       xproto.Window
	gcID      xproto.Gcontext
	bufPixmap xproto.Pixmap
	width     int
	height    int
	depth     byte
}

// createFullscreenOverlay creates an override-redirect window that exactly
// covers the given monitor geometry. No XShape mask is applied.
func createFullscreenOverlay(xu *xgbutil.XUtil, mon MonitorGeometry) (*overlayWindow, error) {
	return createOverlay(xu, mon.X, mon.Y, mon.W, mon.H, ModeFullscreen, ShapeSquare)
}

// createLensOverlay creates a small overlay window centred off-screen initially.
// The caller is responsible for positioning it before mapping.
func createLensOverlay(xu *xgbutil.XUtil, size int, ls LensShape) (*overlayWindow, error) {
	return createOverlay(xu, -size, -size, size, size, ModeLens, ls)
}

func createOverlay(xu *xgbutil.XUtil, x, y, w, h int, m Mode, ls LensShape) (*overlayWindow, error) {
	conn := xu.Conn()
	screen := xu.Screen()
	depth := screen.RootDepth

	winID, err := xproto.NewWindowId(conn)
	if err != nil {
		return nil, fmt.Errorf("overlay: NewWindowId: %w", err)
	}

	// override-redirect bypasses the window manager entirely.
	var overrideRedirect uint32 = 1
	// We want no input events on the overlay — the magnifier grabs events
	// on the root window independently.
	var eventMask uint32 = xproto.EventMaskStructureNotify

	if err := xproto.CreateWindowChecked(
		conn, depth, winID, screen.Root,
		int16(x), int16(y), uint16(w), uint16(h),
		0, // border width
		xproto.WindowClassInputOutput,
		screen.RootVisual,
		xproto.CwOverrideRedirect|xproto.CwEventMask,
		[]uint32{overrideRedirect, eventMask},
	).Check(); err != nil {
		return nil, fmt.Errorf("overlay: CreateWindow: %w", err)
	}

	// _NET_WM_WINDOW_TYPE = _NET_WM_WINDOW_TYPE_SPLASH
	// This hints to any compositor to ignore the window for stacking purposes.
	setAtomProperty(xu, winID, "_NET_WM_WINDOW_TYPE", "_NET_WM_WINDOW_TYPE_SPLASH")

	// _NET_WM_STATE_ABOVE to keep overlay on top.
	setAtomProperty(xu, winID, "_NET_WM_STATE", "_NET_WM_STATE_ABOVE")

	// GC for blitting.
	gcID, err := xproto.NewGcontextId(conn)
	if err != nil {
		xproto.DestroyWindow(conn, winID)
		return nil, fmt.Errorf("overlay: NewGcontextId: %w", err)
	}
	if err := xproto.CreateGCChecked(conn, gcID,
		xproto.Drawable(winID), 0, nil).Check(); err != nil {
		xproto.DestroyWindow(conn, winID)
		return nil, fmt.Errorf("overlay: CreateGC: %w", err)
	}

	// Double-buffer pixmap.
	bufPix, err := xproto.NewPixmapId(conn)
	if err != nil {
		xproto.FreeGC(conn, gcID)
		xproto.DestroyWindow(conn, winID)
		return nil, fmt.Errorf("overlay: NewPixmapId (buf): %w", err)
	}
	if err := xproto.CreatePixmapChecked(conn, depth, bufPix,
		xproto.Drawable(winID), uint16(w), uint16(h)).Check(); err != nil {
		xproto.FreeGC(conn, gcID)
		xproto.DestroyWindow(conn, winID)
		return nil, fmt.Errorf("overlay: CreatePixmap (buf): %w", err)
	}

	// Apply lens shape mask if needed (for lens mode only).
	if m == ModeLens && ls != ShapeSquare {
		if err := applyWindowShape(xu, winID, w, ls); err != nil {
			// Non-fatal; window remains rectangular.
			fmt.Printf("[Magnifier] Warning: could not apply shape mask: %v\n", err)
		}
	}

	// Map (show) the window.
	xproto.MapWindow(conn, winID)
	// Raise it above everything.
	xproto.ConfigureWindow(conn, winID, xproto.ConfigWindowStackMode,
		[]uint32{uint32(xproto.StackModeAbove)})

	return &overlayWindow{
		xu:        xu,
		win:       winID,
		gcID:      gcID,
		bufPixmap: bufPix,
		width:     w,
		height:    h,
		depth:     depth,
	}, nil
}

// moveTo repositions the lens overlay so it is centred on (cx, cy).
// This is called every render frame and is very cheap (one X request).
func (o *overlayWindow) moveTo(cx, cy int) {
	x := cx - o.width/2
	y := cy - o.height/2
	xproto.ConfigureWindow(o.xu.Conn(), o.win,
		xproto.ConfigWindowX|xproto.ConfigWindowY,
		[]uint32{uint32(int32(x)), uint32(int32(y))},
	)
}

// blit uploads bgraData into the double buffer and copies it to the window.
func (o *overlayWindow) blit(bgraData []byte) {
	conn := o.xu.Conn()
	uploadImageChunked(o.xu, xproto.Drawable(o.bufPixmap), o.gcID,
		o.depth, o.width, o.height, bgraData)
	xproto.CopyArea(conn,
		xproto.Drawable(o.bufPixmap),
		xproto.Drawable(o.win),
		o.gcID,
		0, 0, 0, 0,
		uint16(o.width), uint16(o.height),
	)
}

// destroy unmaps and frees all X resources associated with the overlay.
func (o *overlayWindow) destroy() {
	conn := o.xu.Conn()
	xproto.FreePixmap(conn, o.bufPixmap)
	xproto.FreeGC(conn, o.gcID)
	xproto.DestroyWindow(conn, o.win)
}

// ---------- helpers ----------

// setAtomProperty is a convenience wrapper for setting a _NET_WM_ atom property.
func setAtomProperty(xu *xgbutil.XUtil, win xproto.Window, propName, valueName string) {
	conn := xu.Conn()
	propAtom, err := xproto.InternAtom(conn, false, uint16(len(propName)),
		propName).Reply()
	if err != nil {
		return
	}
	valueAtom, err := xproto.InternAtom(conn, false, uint16(len(valueName)),
		valueName).Reply()
	if err != nil {
		return
	}
	atomType, err := xproto.InternAtom(conn, false, 4, "ATOM").Reply()
	if err != nil {
		return
	}
	data := []byte{
		byte(valueAtom.Atom),
		byte(valueAtom.Atom >> 8),
		byte(valueAtom.Atom >> 16),
		byte(valueAtom.Atom >> 24),
	}
	xproto.ChangeProperty(conn, xproto.PropModeReplace, win,
		propAtom.Atom, atomType.Atom, 32, 1, data)
}

// uploadImageChunked splits a large BGRA buffer into chunks that fit within
// the X server's MaximumRequestLength, then uploads each chunk via PutImage.
func uploadImageChunked(xu *xgbutil.XUtil, drawable xproto.Drawable,
	gc xproto.Gcontext, depth byte, w, h int, bgraData []byte) {

	rowBytes := w * 4
	setup := xproto.Setup(xu.Conn())
	maxReq := 0
	if setup != nil {
		maxReq = int(setup.MaximumRequestLength) * 4
	}
	if maxReq <= 0 {
		maxReq = 65536 * 4
	}
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
		xproto.PutImage(
			xu.Conn(),
			xproto.ImageFormatZPixmap,
			drawable, gc,
			uint16(w), uint16(rows),
			0, int16(y),
			0, depth,
			bgraData[offset:offset+length],
		)
	}
}

