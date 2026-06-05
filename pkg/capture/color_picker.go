// [VERIFIED]
package capture

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"log"
	"math"
	"os/exec"
	"strings"

	"github.com/jezek/xgb/xproto"
	"github.com/jezek/xgbutil"
	"github.com/jezek/xgbutil/keybind"
	"github.com/jezek/xgbutil/xevent"
)

type pickerState struct {
	xu           *xgbutil.XUtil
	winID        xproto.Window
	gcID         xproto.Gcontext
	bgPixmapID   xproto.Pixmap
	bufPixmapID  xproto.Pixmap
	cyanGCID     xproto.Gcontext
	screenWidth  int
	screenHeight int
	screen       *xproto.ScreenInfo
	currX        int
	currY        int
	pickerSize   int
	selected     bool
	aborted      bool
	magnifier    *Magnifier
	rgbaImg      *image.RGBA
	pickedColors []string
	formatStr    string
}

// InteractiveColorPicker captures the fullscreen, displays it, lets the user Hover,
// scroll wheel to change picker size, and Left Click to copy pixel HEX colors to clipboard.
func InteractiveColorPicker(fullImg image.Image, formatStr string) (string, error) {
	// Connect to X server
	xu, err := xgbutil.NewConn()
	if err != nil {
		return "", fmt.Errorf("failed to connect to X server: %w", err)
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
		return "", fmt.Errorf("failed to create window ID: %w", err)
	}

	// Set window attributes: override_redirect to bypass WM, capture mouse/keyboard events
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
		return "", fmt.Errorf("failed to create fullscreen window: %w", err)
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
		return "", fmt.Errorf("failed to create GC ID: %w", err)
	}
	err = xproto.CreateGCChecked(
		xu.Conn(),
		gcID,
		xproto.Drawable(winID),
		0,
		nil,
	).Check()
	if err != nil {
		return "", fmt.Errorf("failed to create GC: %w", err)
	}

	// Create a background Pixmap to store the full screen screenshot
	bgPixmapID, err := xproto.NewPixmapId(xu.Conn())
	if err != nil {
		return "", fmt.Errorf("failed to create background pixmap ID: %w", err)
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
		return "", fmt.Errorf("failed to create background pixmap: %w", err)
	}
	defer xproto.FreePixmap(xu.Conn(), bgPixmapID)

	// Create a buffer Pixmap for double-buffering (flicker-free drawing)
	bufPixmapID, err := xproto.NewPixmapId(xu.Conn())
	if err != nil {
		return "", fmt.Errorf("failed to create buffer pixmap ID: %w", err)
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
		return "", fmt.Errorf("failed to create buffer pixmap: %w", err)
	}
	defer xproto.FreePixmap(xu.Conn(), bufPixmapID)

	// Convert Go Image to BGRA bytes for X11 ZPixmap format
	bgraData := imageToBGRA(rgbaImg)

	// Upload background image to bgPixmap in chunks
	err = uploadImageChunked(xu, xproto.Drawable(bgPixmapID), gcID, screen.RootDepth, screenWidth, screenHeight, bgraData)
	if err != nil {
		return "", fmt.Errorf("failed to upload screenshot to X server: %w", err)
	}

	// Create a premium neon-cyan GC for drawing picker area boundaries
	cyanGCID, err := xproto.NewGcontextId(xu.Conn())
	if err != nil {
		return "", fmt.Errorf("failed to create cyan GC ID: %w", err)
	}
	var fgColor uint32 = 0x00f0ff // Neon cyan
	var lineWidth uint32 = 1      // 1px border for precise pixel boundary
	err = xproto.CreateGCChecked(
		xu.Conn(),
		cyanGCID,
		xproto.Drawable(winID),
		xproto.GcForeground|xproto.GcLineWidth,
		[]uint32{fgColor, lineWidth},
	).Check()
	if err != nil {
		return "", fmt.Errorf("failed to create cyan GC: %w", err)
	}
	defer xproto.FreeGC(xu.Conn(), cyanGCID)

	// Map the fullscreen window
	err = xproto.MapWindowChecked(xu.Conn(), winID).Check()
	if err != nil {
		return "", fmt.Errorf("failed to map window: %w", err)
	}

	// Grab Pointer (mouse) and Keyboard exclusively
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
	if err != nil || pointerGrab.Status != xproto.GrabStatusSuccess {
		log.Printf("Warning: GrabPointer status: %v, err: %v", pointerGrab.Status, err)
	} else {
		defer xproto.UngrabPointer(xu.Conn(), xproto.TimeCurrentTime)
	}

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

	keybind.Initialize(xu)

	// Instantiate state: start with 1x1 picker size
	mag := NewMagnifier()
	mag.PickerSize = 1

	// Retrieve initial cursor position
	qp, err := xproto.QueryPointer(xu.Conn(), screen.Root).Reply()
	initialX := screenWidth / 2
	initialY := screenHeight / 2
	if err == nil {
		initialX = int(qp.RootX)
		initialY = int(qp.RootY)
	}

	state := &pickerState{
		xu:           xu,
		winID:        winID,
		gcID:         gcID,
		bgPixmapID:   bgPixmapID,
		bufPixmapID:  bufPixmapID,
		cyanGCID:     cyanGCID,
		screenWidth:  screenWidth,
		screenHeight: screenHeight,
		screen:       screen,
		currX:        initialX,
		currY:        initialY,
		pickerSize:   1,
		magnifier:    mag,
		rgbaImg:      rgbaImg,
		pickedColors: []string{},
		formatStr:    formatStr,
	}

	// Register event handlers
	xevent.ButtonPressFun(state.handleButtonPress).Connect(xu, winID)
	xevent.MotionNotifyFun(state.handleMotionNotify).Connect(xu, winID)
	xevent.ExposeFun(func(X *xgbutil.XUtil, ev xevent.ExposeEvent) {
		state.redraw()
	}).Connect(xu, winID)
	xevent.KeyPressFun(state.handleKeyPress).Connect(xu, winID)

	// Initial redraw
	state.redraw()

	// Run X11 event loop until Quit is called
	xevent.Main(xu)

	// Destroy window immediately
	windowNeedsDestroy = false
	xproto.DestroyWindow(xu.Conn(), winID)

	if state.aborted && len(state.pickedColors) == 0 {
		return "", fmt.Errorf("color picking aborted by user")
	}

	// If the user exited but didn't left-click anything, fallback to picking the current point
	if len(state.pickedColors) == 0 {
		x1 := state.currX - state.pickerSize/2
		y1 := state.currY - state.pickerSize/2
		for y := y1; y < y1+state.pickerSize; y++ {
			for x := x1; x < x1+state.pickerSize; x++ {
				tx := x
				ty := y
				if tx < 0 {
					tx = 0
				}
				if tx >= screenWidth {
					tx = screenWidth - 1
				}
				if ty < 0 {
					ty = 0
				}
				if ty >= screenHeight {
					ty = screenHeight - 1
				}

				c := rgbaImg.At(tx, ty)
				state.pickedColors = append(state.pickedColors, formatColor(c, formatStr))
			}
		}
	}

	clipboardText := strings.Join(state.pickedColors, "\n")

	// Copy text to clipboard natively
	if err := CopyTextToClipboard(clipboardText); err != nil {
		return "", fmt.Errorf("failed to copy color to clipboard: %w", err)
	}

	return clipboardText, nil
}

func (s *pickerState) redraw() {
	// Copy background screenshot to buffer pixmap
	xproto.CopyArea(
		s.xu.Conn(),
		xproto.Drawable(s.bgPixmapID),
		xproto.Drawable(s.bufPixmapID),
		s.gcID,
		0, 0,
		0, 0,
		uint16(s.screenWidth), uint16(s.screenHeight),
	)

	// Draw selection box centered on cursor
	px := s.currX - s.pickerSize/2
	py := s.currY - s.pickerSize/2
	rect := xproto.Rectangle{
		X:      int16(px),
		Y:      int16(py),
		Width:  uint16(s.pickerSize),
		Height: uint16(s.pickerSize),
	}
	xproto.PolyRectangle(s.xu.Conn(), xproto.Drawable(s.bufPixmapID), s.cyanGCID, []xproto.Rectangle{rect})

	// Render magnifier next to cursor
	s.magnifier.Render(
		s.xu,
		s.bufPixmapID,
		s.gcID,
		s.screen.RootDepth,
		s.rgbaImg,
		s.currX,
		s.currY,
		s.screenWidth,
		s.screenHeight,
		false,
		0,
		0,
	)

	// Copy complete double buffer to fullscreen window
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

func (s *pickerState) handleButtonPress(X *xgbutil.XUtil, ev xevent.ButtonPressEvent) {
	if ev.Detail == 1 { // Left click: pick color
		x1 := s.currX - s.pickerSize/2
		y1 := s.currY - s.pickerSize/2

		var colors []string
		for y := y1; y < y1+s.pickerSize; y++ {
			for x := x1; x < x1+s.pickerSize; x++ {
				tx := x
				ty := y
				if tx < 0 {
					tx = 0
				}
				if tx >= s.screenWidth {
					tx = s.screenWidth - 1
				}
				if ty < 0 {
					ty = 0
				}
				if ty >= s.screenHeight {
					ty = s.screenHeight - 1
				}

				c := s.rgbaImg.At(tx, ty)
				colors = append(colors, formatColor(c, s.formatStr))
			}
		}

		s.pickedColors = append(s.pickedColors, colors...)
		s.selected = true

		// Quick user notification
		notifyMsg := fmt.Sprintf("Added color %s to palette (Total: %d)", colors[0], len(s.pickedColors))
		if len(colors) > 1 {
			notifyMsg = fmt.Sprintf("Added %d colors to palette (Total: %d)", len(colors), len(s.pickedColors))
		}
		_ = exec.Command("notify-send", "-a", "Zen-Cap", "Color Picker", notifyMsg).Run()

	} else if ev.Detail == 3 { // Right click: move picking point to cursor
		s.currX = int(ev.EventX)
		s.currY = int(ev.EventY)
		s.redraw()
	} else if ev.Detail == 4 { // Scroll Up: extend picker size
		s.pickerSize += 2
		if s.pickerSize > 29 {
			s.pickerSize = 29
		}
		s.magnifier.PickerSize = s.pickerSize
		s.redraw()
	} else if ev.Detail == 5 { // Scroll Down: reduce picker size
		s.pickerSize -= 2
		if s.pickerSize < 1 {
			s.pickerSize = 1
		}
		s.magnifier.PickerSize = s.pickerSize
		s.redraw()
	}
}

func (s *pickerState) handleMotionNotify(X *xgbutil.XUtil, ev xevent.MotionNotifyEvent) {
	// Motion notify does not change target picking point, it only updates
	// when right-clicked to set picking point.
}

func (s *pickerState) handleKeyPress(X *xgbutil.XUtil, ev xevent.KeyPressEvent) {
	mods := ev.State
	keycode := ev.Detail
	keyStr := keybind.LookupString(s.xu, mods, keycode)

	if keyStr == "Escape" || keyStr == "q" || keyStr == "Q" {
		s.aborted = true
		xevent.Quit(s.xu)
	} else if keyStr == "Return" || keyStr == "space" {
		xevent.Quit(s.xu)
	}
}

func formatColor(c color.Color, fmtStr string) string {
	nrgba := color.NRGBAModel.Convert(c).(color.NRGBA)
	r8 := nrgba.R
	g8 := nrgba.G
	b8 := nrgba.B
	a8 := nrgba.A

	switch strings.ToLower(fmtStr) {
	case "rgb":
		return fmt.Sprintf("rgb(%d, %d, %d)", r8, g8, b8)
	case "rgba":
		return fmt.Sprintf("rgba(%d, %d, %d, %.2f)", r8, g8, b8, float64(a8)/255.0)
	case "hsl":
		h, s, l := rgbToHSL(r8, g8, b8)
		return fmt.Sprintf("hsl(%d, %d%%, %d%%)", int(math.Round(h)), int(math.Round(s*100)), int(math.Round(l*100)))
	case "hex":
		fallthrough
	default:
		return fmt.Sprintf("#%02x%02x%02x", r8, g8, b8)
	}
}

func rgbToHSL(r, g, b uint8) (h, s, l float64) {
	rf := float64(r) / 255.0
	gf := float64(g) / 255.0
	bf := float64(b) / 255.0

	minVal := math.Min(rf, math.Min(gf, bf))
	maxVal := math.Max(rf, math.Max(gf, bf))
	delta := maxVal - minVal

	l = (maxVal + minVal) / 2.0

	if delta == 0 {
		h = 0
		s = 0
	} else {
		if l < 0.5 {
			s = delta / (maxVal + minVal)
		} else {
			s = delta / (2.0 - maxVal - minVal)
		}

		switch maxVal {
		case rf:
			h = (gf - bf) / delta
			if gf < bf {
				h += 6
			}
		case gf:
			h = (bf-rf)/delta + 2
		case bf:
			h = (rf-gf)/delta + 4
		}
		h *= 60
	}
	return
}
