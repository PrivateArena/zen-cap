package snippet

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"log"
	"time"

	"github.com/jezek/xgb/xproto"
	"github.com/jezek/xgbutil"
	"github.com/jezek/xgbutil/keybind"
	"github.com/jezek/xgbutil/xevent"

	"zen-cap/pkg/capture"
	"zen-cap/pkg/config"
)

type pickerState struct {
	xu            *xgbutil.XUtil
	winID         xproto.Window
	gcID          xproto.Gcontext
	bufPixmapID   xproto.Pixmap
	screenWidth   int
	screenHeight  int
	screen        *xproto.ScreenInfo
	width         int
	height        int
	snippets      []Snippet
	selectedIndex int
	aborted       bool
	selected      bool
	mgr           *Manager
	cfg           *config.Config
	// smartState is non-nil when the currently selected snippet is a smart one.
	smartState    *SmartState
}

// ShowPicker opens a native X11 popup window at the center of the screen to select a snippet.
func ShowPicker(mgr *Manager, cfg *config.Config) error {
	snippets := mgr.GetAll()

	// Connect to X server
	xu, err := xgbutil.NewConn()
	if err != nil {
		return fmt.Errorf("failed to connect to X server: %w", err)
	}
	defer xu.Conn().Close()

	screen := xu.Screen()
	screenWidth := int(screen.WidthInPixels)
	screenHeight := int(screen.HeightInPixels)

	// Premium Dropdown Window dimensions
	// Extra 90px reserved for the smart snippet info panel at the bottom.
	width := 550
	height := 390

	// Create window ID
	winID, err := xproto.NewWindowId(xu.Conn())
	if err != nil {
		return fmt.Errorf("failed to create window ID: %w", err)
	}

	// Set window attributes: override_redirect to float on top of everything without borders
	var overrideRedirect uint32 = 1
	var eventMask uint32 = xproto.EventMaskKeyPress | xproto.EventMaskExposure | xproto.EventMaskButtonPress

	x := (screenWidth - width) / 2
	y := (screenHeight - height) / 2

	err = xproto.CreateWindowChecked(
		xu.Conn(),
		screen.RootDepth,
		winID,
		screen.Root,
		int16(x), int16(y), uint16(width), uint16(height),
		0, // border width
		xproto.WindowClassInputOutput,
		screen.RootVisual,
		xproto.CwOverrideRedirect|xproto.CwEventMask,
		[]uint32{overrideRedirect, eventMask},
	).Check()
	if err != nil {
		return fmt.Errorf("failed to create picker window: %w", err)
	}

	windowNeedsDestroy := true
	defer func() {
		if windowNeedsDestroy {
			xproto.DestroyWindow(xu.Conn(), winID)
		}
	}()

	// Setup Graphics Context (GC) for drawing
	gcID, err := xproto.NewGcontextId(xu.Conn())
	if err != nil {
		return fmt.Errorf("failed to create GC ID: %w", err)
	}
	err = xproto.CreateGCChecked(
		xu.Conn(),
		gcID,
		xproto.Drawable(winID),
		0,
		nil,
	).Check()
	if err != nil {
		return fmt.Errorf("failed to create GC: %w", err)
	}
	defer xproto.FreeGC(xu.Conn(), gcID)

	// Create buffer Pixmap for double-buffering
	bufPixmapID, err := xproto.NewPixmapId(xu.Conn())
	if err != nil {
		return fmt.Errorf("failed to create buffer pixmap ID: %w", err)
	}
	err = xproto.CreatePixmapChecked(
		xu.Conn(),
		screen.RootDepth,
		bufPixmapID,
		xproto.Drawable(winID),
		uint16(width),
		uint16(height),
	).Check()
	if err != nil {
		return fmt.Errorf("failed to create buffer pixmap: %w", err)
	}
	defer xproto.FreePixmap(xu.Conn(), bufPixmapID)

	// Map the window
	err = xproto.MapWindowChecked(xu.Conn(), winID).Check()
	if err != nil {
		return fmt.Errorf("failed to map window: %w", err)
	}

	// Actively request input focus for the override_redirect window
	_ = xproto.SetInputFocus(xu.Conn(), xproto.InputFocusParent, winID, xproto.TimeCurrentTime).Check()

	// Grab Keyboard for exclusive hotkey focus, retrying in case the service's passive grab is still active
	grabSuccess := false
	for i := 0; i < 20; i++ {
		keyboardGrab, err := xproto.GrabKeyboard(
			xu.Conn(),
			false,
			winID,
			xproto.TimeCurrentTime,
			xproto.GrabModeAsync,
			xproto.GrabModeAsync,
		).Reply()
		if err == nil && keyboardGrab.Status == xproto.GrabStatusSuccess {
			grabSuccess = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	if !grabSuccess {
		log.Printf("[SnippetPicker] Warning: GrabKeyboard failed after retries. Input focus might be degraded.")
	} else {
		defer xproto.UngrabKeyboard(xu.Conn(), xproto.TimeCurrentTime)
	}

	keybind.Initialize(xu)

	state := &pickerState{
		xu:            xu,
		winID:         winID,
		gcID:          gcID,
		bufPixmapID:   bufPixmapID,
		screenWidth:   screenWidth,
		screenHeight:  screenHeight,
		screen:        screen,
		width:         width,
		height:        height,
		snippets:      snippets,
		selectedIndex: 0,
		mgr:           mgr,
		cfg:           cfg,
	}
	// Initialise smartState if the first item is a smart snippet.
	state.syncSmartState()

	// Connect event handlers
	xevent.KeyPressFun(state.handleKeyPress).Connect(xu, winID)
	xevent.ButtonPressFun(state.handleButtonPress).Connect(xu, winID)
	xevent.ExposeFun(func(X *xgbutil.XUtil, ev xevent.ExposeEvent) {
		state.redraw()
	}).Connect(xu, winID)

	// Draw first frame
	state.redraw()

	// Event loop
	xevent.Main(xu)

	windowNeedsDestroy = false
	xproto.DestroyWindow(xu.Conn(), winID)

	if state.selected && len(snippets) > 0 && state.selectedIndex >= 0 && state.selectedIndex < len(snippets) {
		selectedSnip := snippets[state.selectedIndex]
		fmt.Printf("[SnippetPicker] Selected snippet: %s. Pasting (%s mode)...\n", selectedSnip.Name, state.cfg.SnippetMode)
		// For smart snippets, resolve content now (captures current time/state).
		content := selectedSnip.Content
		if selectedSnip.Smart != "" && state.smartState != nil {
			content = state.smartState.Content()
		}
		return state.mgr.Paste(content, state.cfg.SnippetMode)
	}

	return nil
}

func (s *pickerState) redraw() {
	img := image.NewRGBA(image.Rect(0, 0, s.width, s.height))

	// Retro Premium Theme
	bgColor    := color.RGBA{R: 26, G: 26, B: 36, A: 255}
	borderColor := color.RGBA{R: 0, G: 240, B: 255, A: 255}
	headerColor := color.RGBA{R: 255, G: 0, B: 127, A: 255}
	textColor   := color.RGBA{R: 230, G: 230, B: 240, A: 255}
	mutedColor  := color.RGBA{R: 120, G: 120, B: 150, A: 255}
	selectedBg  := color.RGBA{R: 45, G: 55, B: 85, A: 255}
	smartColor  := color.RGBA{R: 255, G: 215, B: 0, A: 255} // Gold for smart snippets

	draw.Draw(img, img.Bounds(), &image.Uniform{bgColor}, image.Point{}, draw.Src)

	// Double border
	drawRect(img, 0, 0, s.width-1, s.height-1, headerColor)
	drawRect(img, 1, 1, s.width-2, s.height-2, headerColor)
	drawRect(img, 2, 2, s.width-3, s.height-3, borderColor)
	drawRect(img, 3, 3, s.width-4, s.height-4, borderColor)

	capture.DrawStringScaled(img, "SELECT SNIPPET", 30, 20, headerColor, 2)

	modeText := "MODE: PASTE"
	modeColor := mutedColor
	if s.cfg != nil && s.cfg.SnippetMode == "type" {
		modeText = "MODE: TYPING"
		modeColor = borderColor
	}
	capture.DrawStringScaled(img, modeText, s.width-180, 20, modeColor, 2)

	for x := 20; x < s.width-20; x++ {
		img.Set(x, 48, mutedColor)
	}

	if len(s.snippets) == 0 {
		capture.DrawStringScaled(img, "NO SNIPPETS FOUND.", 40, 120, mutedColor, 2)
		capture.DrawStringScaled(img, "Add to snippets.yaml directly", 40, 160, mutedColor, 2)
	} else {
		startY := 60
		itemHeight := 38
		maxVisible := 5

		scrollOffset := 0
		if s.selectedIndex >= maxVisible {
			scrollOffset = s.selectedIndex - maxVisible + 1
		}

		for idx := 0; idx < maxVisible && idx+scrollOffset < len(s.snippets); idx++ {
			snipIdx := idx + scrollOffset
			snip := s.snippets[snipIdx]
			yPos := startY + idx*itemHeight
			isActive := snipIdx == s.selectedIndex

			if isActive {
				cardRect := image.Rect(15, yPos, s.width-15, yPos+itemHeight-4)
				draw.Draw(img, cardRect, &image.Uniform{selectedBg}, image.Point{}, draw.Src)
				for lx := 15; lx < 22; lx++ {
					for ly := yPos; ly < yPos+itemHeight-4; ly++ {
						img.Set(lx, ly, borderColor)
					}
				}
				for lx := s.width - 22; lx < s.width-15; lx++ {
					for ly := yPos; ly < yPos+itemHeight-4; ly++ {
						img.Set(lx, ly, borderColor)
					}
				}
			}

			displayName := fmt.Sprintf("%d. %s", snipIdx+1, snip.Name)
			if len([]rune(displayName)) > 28 {
				displayName = string([]rune(displayName)[:25]) + "..."
			}
			nameColor := textColor
			if isActive {
				if snip.Smart != "" {
					nameColor = smartColor
				} else {
					nameColor = borderColor
				}
			} else if snip.Smart != "" {
				nameColor = color.RGBA{R: 200, G: 170, B: 50, A: 255}
			}
			capture.DrawStringScaled(img, displayName, 30, yPos+4, nameColor, 2)
		}

		// ── Smart Snippet Info Panel ─────────────────────────────────────
		if s.smartState != nil {
			panelY := startY + maxVisible*itemHeight + 4

			// Separator
			for x := 20; x < s.width-20; x++ {
				img.Set(x, panelY, mutedColor)
			}
			panelY += 6

			// Live time value
			timeStr := s.smartState.Content()
			capture.DrawStringScaled(img, timeStr, 30, panelY, smartColor, 2)

			// Location label
			panelY += 22
			locStr := "@ " + s.smartState.LocationLabel()
			capture.DrawStringScaled(img, locStr, 30, panelY, mutedColor, 1)

			// Query input line
			panelY += 16
			qLabel := "  > "
			if s.smartState.query != "" {
				qLabel += s.smartState.query + "_"
			} else {
				qLabel += "type location or \u2190\u2192 to cycle"
			}
			qColor := mutedColor
			if s.smartState.query != "" {
				qColor = color.RGBA{R: 180, G: 230, B: 180, A: 255}
			}
			capture.DrawStringScaled(img, qLabel, 20, panelY, qColor, 1)
		}
	}

	// 2. Upload and display using X11
	bgra := capture.ImageToBGRA(img)
	_ = capture.UploadImageChunked(s.xu, xproto.Drawable(s.bufPixmapID), s.gcID, s.screen.RootDepth, s.width, s.height, bgra)

	xproto.CopyArea(
		s.xu.Conn(),
		xproto.Drawable(s.bufPixmapID),
		xproto.Drawable(s.winID),
		s.gcID,
		0, 0,
		0, 0,
		uint16(s.width), uint16(s.height),
	)
}

func (s *pickerState) syncSmartState() {
	if s.selectedIndex >= 0 && s.selectedIndex < len(s.snippets) {
		snip := s.snippets[s.selectedIndex]
		if snip.Smart == SmartTypeTime {
			if s.smartState == nil {
				s.smartState = newSmartState()
			}
			return
		}
	}
	s.smartState = nil
}

func (s *pickerState) handleKeyPress(X *xgbutil.XUtil, ev xevent.KeyPressEvent) {
	mods := ev.State
	keycode := ev.Detail
	keyStr := keybind.LookupString(s.xu, mods, keycode)

	if keyStr == "Escape" {
		// If there's an active query, clear it first before aborting.
		if s.smartState != nil && s.smartState.query != "" {
			s.smartState.ClearQuery()
			s.redraw()
			return
		}
		s.aborted = true
		xevent.Quit(s.xu)
		return
	}

	if keyStr == "q" || keyStr == "Q" {
		// Only quit if not currently typing a location query.
		if s.smartState == nil || s.smartState.query == "" {
			s.aborted = true
			xevent.Quit(s.xu)
			return
		}
	}

	if len(s.snippets) > 0 {
		// ── Navigation (Up/Down) ──────────────────────────────────────────
		if keyStr == "Up" || (keyStr == "k" && (s.smartState == nil || s.smartState.query == "")) {
			s.selectedIndex--
			if s.selectedIndex < 0 {
				s.selectedIndex = len(s.snippets) - 1
			}
			s.syncSmartState()
			s.redraw()
			return
		}
		if keyStr == "Down" || (keyStr == "j" && (s.smartState == nil || s.smartState.query == "")) {
			s.selectedIndex++
			if s.selectedIndex >= len(s.snippets) {
				s.selectedIndex = 0
			}
			s.syncSmartState()
			s.redraw()
			return
		}

		// ── Smart snippet: Left/Right cycle presets ───────────────────────
		if s.smartState != nil {
			if keyStr == "Left" {
				s.smartState.CyclePrev()
				s.redraw()
				return
			}
			if keyStr == "Right" {
				s.smartState.CycleNext()
				s.redraw()
				return
			}
			// Backspace on the query buffer
			if keyStr == "BackSpace" {
				s.smartState.BackspaceQuery()
				s.redraw()
				return
			}
			// Printable chars go into the location query buffer
			if len([]rune(keyStr)) == 1 {
				r := []rune(keyStr)[0]
				// Accept letters, digits, spaces, dots and slashes (for IANA paths)
				if r >= 32 && r < 127 {
					s.smartState.AppendQuery(r)
					s.redraw()
					return
				}
			}
		}

		// ── Confirm ───────────────────────────────────────────────────────
		if keyStr == "Return" || keyStr == "Enter" {
			s.selected = true
			xevent.Quit(s.xu)
			return
		}
	}
}

func (s *pickerState) handleButtonPress(X *xgbutil.XUtil, ev xevent.ButtonPressEvent) {
	// Right click -> abort
	if ev.Detail == 3 {
		s.aborted = true
		xevent.Quit(s.xu)
		return
	}

	// Left click -> select
	if ev.Detail == 1 {
		x := int(ev.EventX)
		y := int(ev.EventY)

		if x >= 15 && x < s.width-15 && y >= 60 && y < 60+5*38 {
			clickedIdx := (y - 60) / 38

			scrollOffset := 0
			maxVisible := 5
			if s.selectedIndex >= maxVisible {
				scrollOffset = s.selectedIndex - maxVisible + 1
			}

			actualIdx := clickedIdx + scrollOffset
			if actualIdx >= 0 && actualIdx < len(s.snippets) {
				s.selectedIndex = actualIdx
				s.selected = true
				xevent.Quit(s.xu)
			}
		}
	}
}

func drawRect(img draw.Image, x0, y0, x1, y1 int, col color.Color) {
	for x := x0; x <= x1; x++ {
		img.Set(x, y0, col)
		img.Set(x, y1, col)
	}
	for y := y0; y <= y1; y++ {
		img.Set(x0, y, col)
		img.Set(x1, y, col)
	}
}
