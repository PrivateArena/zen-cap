package overlay

import (
	"fmt"
	"image"
	"image/color"
	"math"
	"os"
	"sync"
	"time"

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

	previewGC xproto.Gcontext // bright-color GC for transient shape outlines

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
		display = os.Getenv("DISPLAY")
	}
	if display == "" {
		return fmt.Errorf("overlay: no X11 display configured and DISPLAY is not set")
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

	// Create preview GC for transient shape outlines and text cursor
	pGC, err := xproto.NewGcontextId(xu.Conn())
	if err != nil {
		xproto.FreeGC(xu.Conn(), gcID)
		xproto.DestroyWindow(xu.Conn(), winID)
		xu.Conn().Close()
		return fmt.Errorf("overlay: NewGcontextId (preview): %w", err)
	}
	if err := xproto.CreateGCChecked(xu.Conn(), pGC, xproto.Drawable(winID),
		xproto.GcForeground, []uint32{0x00FF69B4}, // neon pink
	).Check(); err != nil {
		xproto.FreeGC(xu.Conn(), pGC)
		xproto.FreeGC(xu.Conn(), gcID)
		xproto.DestroyWindow(xu.Conn(), winID)
		xu.Conn().Close()
		return fmt.Errorf("overlay: CreateGC (preview): %w", err)
	}
	ov.previewGC = pGC

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
		evX, evY := int(ev.EventX), int(ev.EventY)

		if ov.ann.IsDoodling() && ov.ann.Tool() == annotation.Doodle {
			// Doodle fast path: snapshot last position, let Annotator
			// draw on Go layer, then draw segment directly on bufPix
			// via X11 PolyLine (avoids full composite upload per event).
			last := ov.ann.DoodleLast()
			ov.ann.HandleEvent(annotation.InputEvent{
				Kind: annotation.Motion,
				X:    evX,
				Y:    evY,
			})
			xproto.PolyLine(ov.xu.Conn(),
				xproto.CoordModeOrigin,
				xproto.Drawable(ov.bufPix), ov.previewGC,
				[]xproto.Point{
					{X: int16(last.X), Y: int16(last.Y)},
					{X: int16(evX), Y: int16(evY)},
				},
			)
			xproto.CopyArea(ov.xu.Conn(),
				xproto.Drawable(ov.bufPix),
				xproto.Drawable(ov.win),
				ov.gc, 0, 0, 0, 0,
				uint16(ov.cfg.Width), uint16(ov.cfg.Height),
			)
			return
		}

		_, needsRedraw := ov.ann.HandleEvent(annotation.InputEvent{
			Kind: annotation.Motion,
			X:    evX,
			Y:    evY,
		})
		if needsRedraw {
			ov.render()
		}
	}).Connect(xu, winID)

	xevent.KeyPressFun(func(X *xgbutil.XUtil, ev xevent.KeyPressEvent) {
		mods := ev.State
		keycode := ev.Detail
		keyStr := keybind.LookupString(xu, mods, keycode)

		// Forward to Annotator first (handles text commit/cancel,
		// undo, tool switching, etc.)
		handled, needsRedraw := ov.ann.HandleEvent(annotation.InputEvent{
			Kind:    annotation.Key,
			KeyStr:  keyStr,
			Keycode: uint8(keycode),
			Mods:    mods,
		})
		if handled {
			if needsRedraw {
				ov.render()
			}
			return
		}

		// Only handle Enter/Escape for overlay dismissal if the
		// Annotator didn't consume them (i.e., not in text mode).
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
	xproto.FreeGC(ov.xu.Conn(), ov.previewGC)
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

	ov.renderTransient()

	xproto.CopyArea(ov.xu.Conn(),
		xproto.Drawable(ov.bufPix),
		xproto.Drawable(ov.win),
		ov.gc, 0, 0, 0, 0,
		uint16(ov.cfg.Width), uint16(ov.cfg.Height),
	)
}

// renderTransient draws shape outlines and text cursor preview on bufPix
// on top of the committed composite. Called from render() before CopyArea.
func (ov *X11Overlay) renderTransient() {
	bpp, _ := pixmapFormatFor(ov.xu, ov.depth)
	pixelBytes := bpp / 8
	if pixelBytes < 4 {
		pixelBytes = 4
	}

	// Shape outline preview during drag
	if ov.ann.IsDoodling() {
		tool := ov.ann.Tool()
		if tool == annotation.Rect {
			start := ov.ann.DoodleStart()
			last := ov.ann.DoodleLast()
			x1 := int(math.Min(float64(start.X), float64(last.X)))
			y1 := int(math.Min(float64(start.Y), float64(last.Y)))
			w := int(math.Abs(float64(last.X - start.X)))
			h := int(math.Abs(float64(last.Y - start.Y)))
			if w > 0 && h > 0 {
				rect := xproto.Rectangle{
					X: int16(x1), Y: int16(y1),
					Width: uint16(w), Height: uint16(h),
				}
				xproto.PolyRectangle(ov.xu.Conn(),
					xproto.Drawable(ov.bufPix), ov.previewGC,
					[]xproto.Rectangle{rect})
			}
		} else if tool == annotation.Circle {
			start := ov.ann.DoodleStart()
			last := ov.ann.DoodleLast()
			dx := last.X - start.X
			dy := last.Y - start.Y
			r := int(math.Sqrt(float64(dx*dx + dy*dy)))
			if r > 0 {
				arc := xproto.Arc{
					X: int16(start.X - r), Y: int16(start.Y - r),
					Width: uint16(r * 2), Height: uint16(r * 2),
					Angle1: 0, Angle2: 360 * 64,
				}
				xproto.PolyArc(ov.xu.Conn(),
					xproto.Drawable(ov.bufPix), ov.previewGC,
					[]xproto.Arc{arc})
			}
		}
	}

	// Text cursor preview
	if ov.ann.IsTextActive() {
		cfg := ov.ann.Config()
		text := ov.ann.TextBuffer()
		cursor := " "
		if time.Now().UnixNano()/500000000%2 == 0 {
			cursor = "_"
		}
		textToShow := text + cursor
		scale := cfg.FontScale
		textW := len(textToShow)*6*scale + 6
		textH := 7*scale + 4
		if textW <= 0 || textH <= 0 {
			return
		}
		textImg := image.NewRGBA(image.Rect(0, 0, textW, textH))
		pink := color.RGBA{R: 255, G: 0, B: 127, A: 255}
		for dy := 0; dy < textH; dy++ {
			for dx := 0; dx < textW; dx++ {
				textImg.Set(dx, dy, color.Black)
			}
		}
		annotation.DrawStringScaled(textImg, textToShow, 3, 2, pink, scale)

		stride := rowStrideBytes(textW, bpp, 32) // scanlinePad=32 always for depth≥24
		textPixels := imageToPixelData(textImg, pixelBytes, stride)
		xproto.PutImage(ov.xu.Conn(),
			xproto.ImageFormatZPixmap,
			xproto.Drawable(ov.bufPix), ov.gc,
			uint16(textW), uint16(textH),
			int16(ov.ann.TextAnchor().X), int16(ov.ann.TextAnchor().Y),
			0, ov.depth,
			textPixels,
		)
	}
}
