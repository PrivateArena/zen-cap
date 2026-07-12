package overlay

import (
	"fmt"
	"image"
	"sync"

	"github.com/jezek/xgb/xproto"
	"github.com/jezek/xgbutil"
	"github.com/jezek/xgbutil/keybind"
	"github.com/jezek/xgbutil/xevent"

	"zen-cap/pkg/annotation"
)

type OverlayConfig struct {
	X, Y, Width, Height int
	TargetFPS           int
	Display             string // X11 display (e.g. ":0.0"); empty = $DISPLAY
}

type X11Overlay struct {
	xu     *xgbutil.XUtil
	ann    *annotation.Annotator
	win    xproto.Window
	gc     xproto.Gcontext
	bufPix xproto.Pixmap
	cfg    OverlayConfig
	depth  byte
	base   *image.RGBA

	stop    chan struct{}
	done    chan error
	wg      sync.WaitGroup
	started bool
	mu      sync.Mutex
}

func NewX11Overlay(ann *annotation.Annotator, cfg OverlayConfig) *X11Overlay {
	if cfg.TargetFPS <= 0 {
		cfg.TargetFPS = 30
	}
	return &X11Overlay{
		ann:  ann,
		cfg:  cfg,
		stop: make(chan struct{}),
		done: make(chan error, 1),
	}
}

func (ov *X11Overlay) Start() error {
	ov.mu.Lock()
	defer ov.mu.Unlock()
	if ov.started {
		return fmt.Errorf("overlay already started")
	}

	display := ov.cfg.Display
	if display == "" {
		display = ":0.0"
	}
	xu, err := xgbutil.NewConnDisplay(display)
	if err != nil {
		return fmt.Errorf("overlay: NewConn: %w", err)
	}
	ov.xu = xu
	screen := xu.Screen()

	// Use root visual/depth — no ARGB transparency needed (frozen-frame
	// annotation composites the image in software). This avoids BadMatch
	// on WMs without a compositing manager, where depth-32 child windows
	// are not allowed under a depth-24 root.
	ov.depth = screen.RootDepth

	winID, err := xproto.NewWindowId(xu.Conn())
	if err != nil {
		xu.Conn().Close()
		return fmt.Errorf("overlay: NewWindowId: %w", err)
	}
	ov.win = winID

	var overrideRedirect uint32 = 1
	var eventMask uint32 = xproto.EventMaskButtonPress |
		xproto.EventMaskButtonRelease |
		xproto.EventMaskButtonMotion |
		xproto.EventMaskKeyPress |
		xproto.EventMaskExposure |
		xproto.EventMaskStructureNotify

	// Value-list must follow LSB-first mask-bit order:
	//   CwBackPixel (bit1), CwBorderPixel (bit3),
	//   CwOverrideRedirect (bit9), CwEventMask (bit11)
	err = xproto.CreateWindowChecked(
		xu.Conn(), screen.RootDepth, winID, screen.Root,
		int16(ov.cfg.X), int16(ov.cfg.Y),
		uint16(ov.cfg.Width), uint16(ov.cfg.Height),
		0, xproto.WindowClassInputOutput, screen.RootVisual,
		xproto.CwOverrideRedirect|xproto.CwEventMask|xproto.CwBackPixel|xproto.CwBorderPixel,
		[]uint32{0x00000000, 0x00000000, overrideRedirect, eventMask},
	).Check()
	if err != nil {
		xu.Conn().Close()
		return fmt.Errorf("overlay: CreateWindow: %w", err)
	}

	setAtom(xu, winID, "_NET_WM_WINDOW_TYPE", "_NET_WM_WINDOW_TYPE_UTILITY")
	setAtom(xu, winID, "_NET_WM_STATE", "_NET_WM_STATE_ABOVE")

	gcID, err := xproto.NewGcontextId(xu.Conn())
	if err != nil {
		xproto.DestroyWindow(xu.Conn(), winID)
		xu.Conn().Close()
		return fmt.Errorf("overlay: NewGcontextId: %w", err)
	}
	if err := xproto.CreateGCChecked(xu.Conn(), gcID, xproto.Drawable(winID), 0, nil).Check(); err != nil {
		xproto.DestroyWindow(xu.Conn(), winID)
		xu.Conn().Close()
		return fmt.Errorf("overlay: CreateGC: %w", err)
	}
	ov.gc = gcID

	bufPix, err := xproto.NewPixmapId(xu.Conn())
	if err != nil {
		xproto.FreeGC(xu.Conn(), gcID)
		xproto.DestroyWindow(xu.Conn(), winID)
		xu.Conn().Close()
		return fmt.Errorf("overlay: NewPixmapId: %w", err)
	}
	if err := xproto.CreatePixmapChecked(xu.Conn(), ov.depth, bufPix, xproto.Drawable(winID),
		uint16(ov.cfg.Width), uint16(ov.cfg.Height)).Check(); err != nil {
		xproto.FreePixmap(xu.Conn(), bufPix)
		xproto.FreeGC(xu.Conn(), gcID)
		xproto.DestroyWindow(xu.Conn(), winID)
		xu.Conn().Close()
		return fmt.Errorf("overlay: CreatePixmap: %w", err)
	}
	ov.bufPix = bufPix

	if ov.cfg.Width <= 0 || ov.cfg.Height <= 0 {
		xu.Conn().Close()
		return fmt.Errorf("overlay: invalid dimensions %dx%d", ov.cfg.Width, ov.cfg.Height)
	}

	xproto.MapWindow(xu.Conn(), winID)
	xproto.ConfigureWindow(xu.Conn(), winID, xproto.ConfigWindowStackMode,
		[]uint32{uint32(xproto.StackModeAbove)})

	// Give the overlay keyboard focus so it can receive text input for
	// annotations. Without this, an override-redirect window never gets
	// keyboard events.
	xproto.SetInputFocus(xu.Conn(), xproto.InputFocusParent, winID, xproto.TimeCurrentTime)

	keybind.Initialize(xu)

	xevent.ButtonPressFun(func(X *xgbutil.XUtil, ev xevent.ButtonPressEvent) {
		ov.ann.HandleEvent(annotation.InputEvent{
			Kind:   annotation.Press,
			X:      int(ev.EventX),
			Y:      int(ev.EventY),
			Button: int(ev.Detail),
			Mods:   ev.State,
		})
	}).Connect(xu, winID)

	xevent.ButtonReleaseFun(func(X *xgbutil.XUtil, ev xevent.ButtonReleaseEvent) {
		ov.ann.HandleEvent(annotation.InputEvent{
			Kind:   annotation.Release,
			X:      int(ev.EventX),
			Y:      int(ev.EventY),
			Button: int(ev.Detail),
		})
	}).Connect(xu, winID)

	xevent.MotionNotifyFun(func(X *xgbutil.XUtil, ev xevent.MotionNotifyEvent) {
		ov.ann.HandleEvent(annotation.InputEvent{
			Kind: annotation.Motion,
			X:    int(ev.EventX),
			Y:    int(ev.EventY),
		})
	}).Connect(xu, winID)

	xevent.KeyPressFun(func(X *xgbutil.XUtil, ev xevent.KeyPressEvent) {
		mods := ev.State
		keycode := ev.Detail
		keyStr := keybind.LookupString(xu, mods, keycode)

		if keyStr == "Return" || keyStr == "Enter" {
			select {
			case ov.done <- nil:
			default:
			}
			return
		}
		if keyStr == "Escape" || keyStr == "q" || keyStr == "Q" {
			select {
			case ov.done <- fmt.Errorf("annotation cancelled"):
			default:
			}
			return
		}

		ov.ann.HandleEvent(annotation.InputEvent{
			Kind:    annotation.Key,
			KeyStr:  keyStr,
			Keycode: uint8(keycode),
			Mods:    mods,
		})
	}).Connect(xu, winID)

	xevent.ExposeFun(func(X *xgbutil.XUtil, ev xevent.ExposeEvent) {
		ov.render()
	}).Connect(xu, winID)

	ov.started = true
	ov.wg.Add(2)
	go func() {
		defer ov.wg.Done()
		xevent.Main(xu)
	}()
	go ov.runRenderLoop()

	return nil
}

func (ov *X11Overlay) Stop() {
	ov.mu.Lock()
	if !ov.started {
		ov.mu.Unlock()
		return
	}
	ov.started = false
	ov.mu.Unlock()

	close(ov.stop)
	xevent.Quit(ov.xu)
	ov.wg.Wait()

	xproto.FreePixmap(ov.xu.Conn(), ov.bufPix)
	xproto.FreeGC(ov.xu.Conn(), ov.gc)
	xproto.DestroyWindow(ov.xu.Conn(), ov.win)
	ov.xu.Conn().Close()
}

func (ov *X11Overlay) WaitDone() error {
	return <-ov.done
}

func (ov *X11Overlay) render() {
	ov.ann.ClearDirty()
	composite := ov.ann.GetComposite()

	bpp, pad := pixmapFormatFor(ov.xu, ov.depth)
	stride := rowStrideBytes(ov.cfg.Width, bpp, pad)
	pixels := imageToPixelData(composite, bpp/8, stride)

	uploadImageChunked(ov.xu, xproto.Drawable(ov.bufPix), ov.gc, ov.depth,
		ov.cfg.Width, ov.cfg.Height, pixels)
	xproto.CopyArea(ov.xu.Conn(),
		xproto.Drawable(ov.bufPix),
		xproto.Drawable(ov.win),
		ov.gc, 0, 0, 0, 0,
		uint16(ov.cfg.Width), uint16(ov.cfg.Height),
	)
}
