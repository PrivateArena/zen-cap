package snippet

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"log"
	"os"
	"strconv"
	"time"

	"github.com/jezek/xgb/xproto"
	"github.com/jezek/xgbutil"
	"github.com/jezek/xgbutil/keybind"
	"github.com/jezek/xgbutil/xevent"

	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"

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
	faceNormal    font.Face
	faceSmall     font.Face
	// prevFocusWin is the X11 window that held focus before the picker opened.
	// Passed through to Paste() so it can poll/restore focus deterministically.
	prevFocusWin  xproto.Window
}

// ShowPicker opens a native X11 popup window at the center of the screen to select a snippet.
func ShowPicker(mgr *Manager, cfg *config.Config) error {
	snippets := mgr.GetAll()

	// Read the previous focus window saved by the service before spawning us.
	// This lets Paste() deterministically restore and verify focus.
	var prevFocusWin xproto.Window
	if envVal := os.Getenv("ZENCAP_PREV_FOCUS"); envVal != "" {
		if n, err := strconv.ParseUint(envVal, 10, 32); err == nil {
			prevFocusWin = xproto.Window(n)
		}
	}

	// Connect to X server
	xu, err := xgbutil.NewConn()
	if err != nil {
		return fmt.Errorf("failed to connect to X server: %w", err)
	}
	defer xu.Conn().Close()

	screen := xu.Screen()
	screenWidth := int(screen.WidthInPixels)
	screenHeight := int(screen.HeightInPixels)

	// Premium Dropdown Window dimensions from configuration
	width := cfg.SnippetPicker.Width
	height := cfg.SnippetPicker.Height

	faceNormal, faceSmall := loadPickerFonts(cfg)

	// Create window ID
	winID, err := xproto.NewWindowId(xu.Conn())
	if err != nil {
		return fmt.Errorf("failed to create window ID: %w", err)
	}

	// Set window attributes: override_redirect to float on top of everything without borders
	var overrideRedirect uint32 = 1
	// Include StructureNotify so we receive MapNotify before attempting GrabKeyboard.
	var eventMask uint32 = xproto.EventMaskKeyPress | xproto.EventMaskExposure | xproto.EventMaskButtonPress | xproto.EventMaskStructureNotify

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

	// Wait for the server to confirm the window is mapped (MapNotify) before
	// grabbing the keyboard.  Grabbing before this event arrives is a race that
	// causes GrabKeyboard to target an unmapped window and fail or behave
	// unpredictably.
	mapDeadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(mapDeadline) {
		ev, evErr := xu.Conn().PollForEvent()
		if evErr != nil {
			break
		}
		if ev == nil {
			time.Sleep(5 * time.Millisecond)
			continue
		}
		if mn, ok := ev.(xproto.MapNotifyEvent); ok && mn.Window == winID {
			break
		}
	}

	// Obtain a real server-side timestamp via a round-trip PropertyNotify.
	// TimeCurrentTime (0) is treated as timestamp 0 by the server which is
	// older than any real event — ICCCM-compliant WMs silently ignore such
	// SetInputFocus calls.
	realTimestamp := pickerRealTimestamp(xu, winID)

	// Set focus using the real timestamp so compositing WMs honour it.
	_ = xproto.SetInputFocusChecked(xu.Conn(), xproto.InputFocusParent, winID, realTimestamp).Check()

	// Grab Keyboard exclusively — now safe because MapNotify was received.
	grabSuccess := false
	for i := 0; i < 20; i++ {
		keyboardGrab, err := xproto.GrabKeyboard(
			xu.Conn(),
			false,
			winID,
			realTimestamp,
			xproto.GrabModeAsync,
			xproto.GrabModeAsync,
		).Reply()
		if err == nil && keyboardGrab.Status == xproto.GrabStatusSuccess {
			grabSuccess = true
			break
		}
		time.Sleep(15 * time.Millisecond)
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
		faceNormal:    faceNormal,
		faceSmall:     faceSmall,
		prevFocusWin:  prevFocusWin,
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
			if selectedSnip.Smart == SmartTypeIP && state.smartState.ipLoading {
				// Block for a short duration waiting for background resolution to finish
				for i := 0; i < 15 && state.smartState.ipLoading; i++ {
					time.Sleep(100 * time.Millisecond)
				}
			}
			content = state.smartState.Content(selectedSnip.Format)
		}
		return state.mgr.Paste(content, state.cfg.SnippetMode, prevFocusWin)
	}

	return nil
}

// pickerRealTimestamp obtains a real X server timestamp by triggering a
// PropertyNotify event via a zero-length ChangeProperty and waiting for it.
// This is required because TimeCurrentTime (0) is rejected by ICCCM/EWMH
// compliant compositors when used with SetInputFocus or GrabKeyboard.
func pickerRealTimestamp(xu *xgbutil.XUtil, win xproto.Window) xproto.Timestamp {
	// Subscribe to PropertyChange events on the window
	xproto.ChangeWindowAttributes(xu.Conn(), win, xproto.CwEventMask,
		[]uint32{xproto.EventMaskPropertyChange | xproto.EventMaskStructureNotify |
			xproto.EventMaskKeyPress | xproto.EventMaskExposure | xproto.EventMaskButtonPress})

	// Touch a zero-length WM_NAME property to provoke PropertyNotify
	wmNameAtom, err := xproto.InternAtom(xu.Conn(), false, uint16(len("WM_NAME")), "WM_NAME").Reply()
	if err != nil {
		return xproto.TimeCurrentTime
	}
	xproto.ChangeProperty(xu.Conn(), xproto.PropModeAppend, win,
		wmNameAtom.Atom, xproto.AtomString, 8, 0, nil)
	xu.Conn().Flush()

	deadline := time.Now().Add(200 * time.Millisecond)
	for time.Now().Before(deadline) {
		ev, evErr := xu.Conn().PollForEvent()
		if evErr != nil {
			break
		}
		if ev == nil {
			time.Sleep(2 * time.Millisecond)
			continue
		}
		if pn, ok := ev.(xproto.PropertyNotifyEvent); ok {
			return pn.Time
		}
	}
	return xproto.TimeCurrentTime // fallback only
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

	startY, itemHeight, maxVisible, heightNormal, heightSmall := s.layoutMetrics()
	sepY := 20 + heightNormal + 14

	s.drawText(img, "SELECT SNIPPET", 30, 20, headerColor, 2)

	modeText := "MODE: PASTE"
	modeColor := mutedColor
	if s.cfg != nil && s.cfg.SnippetMode == "type" {
		modeText = "MODE: TYPING"
		modeColor = borderColor
	}
	
	var modeWidth int
	if s.faceNormal != nil {
		dMeasure := &font.Drawer{Face: s.faceNormal}
		modeWidth = dMeasure.MeasureString(modeText).Round()
	} else {
		modeWidth = len(modeText) * 12
	}
	s.drawText(img, modeText, s.width-30-modeWidth, 20, modeColor, 2)

	for x := 20; x < s.width-20; x++ {
		img.Set(x, sepY, mutedColor)
	}

	if len(s.snippets) == 0 {
		s.drawText(img, "NO SNIPPETS FOUND.", 40, startY+60, mutedColor, 2)
		s.drawText(img, "Add to snippets.yaml directly", 40, startY+100, mutedColor, 2)
	} else {
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
			availableWidth := s.width - 60 // 30px padding on each side

			if s.faceNormal != nil {
				dMeasure := &font.Drawer{Face: s.faceNormal}
				if dMeasure.MeasureString(displayName).Round() > availableWidth {
					runes := []rune(displayName)
					for len(runes) > 3 {
						runes = runes[:len(runes)-1]
						testStr := string(runes) + "..."
						if dMeasure.MeasureString(testStr).Round() <= availableWidth {
							displayName = testStr
							break
						}
					}
				}
			} else {
				maxChars := availableWidth / 12
				if len([]rune(displayName)) > maxChars {
					if maxChars > 3 {
						displayName = string([]rune(displayName)[:maxChars-3]) + "..."
					} else {
						displayName = "..."
					}
				}
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
			textYPadding := (itemHeight - 4 - heightNormal) / 2
			s.drawText(img, displayName, 30, yPos+textYPadding, nameColor, 2)
		}

		// ── Smart Snippet Info Panel ─────────────────────────────────────
		if s.smartState != nil {
			panelY := startY + maxVisible*itemHeight + 4

			// Separator
			for x := 20; x < s.width-20; x++ {
				img.Set(x, panelY, mutedColor)
			}
			panelY += 6

			// Live value (either time or IP)
			selectedSnip := s.snippets[s.selectedIndex]
			valStr := s.smartState.Content(selectedSnip.Format)
			s.drawText(img, valStr, 30, panelY, smartColor, 2)

			if s.smartState.kind == SmartTypeTime {
				// Location label
				panelY += heightNormal + 8
				locStr := "@ " + s.smartState.LocationLabel()
				s.drawText(img, locStr, 30, panelY, mutedColor, 1)

				// Query input line
				panelY += heightSmall + 9
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
				s.drawText(img, qLabel, 20, panelY, qColor, 1)
			} else if s.smartState.kind == SmartTypeIP {
				panelY += heightNormal + 8
				statusStr := "Online Lookup (httpbin.org/ip)"
				if s.smartState.ipLoading {
					statusStr = "Fetching IP address..."
				} else if s.smartState.ipErr != nil {
					statusStr = fmt.Sprintf("Error: %v", s.smartState.ipErr)
				}
				s.drawText(img, statusStr, 30, panelY, mutedColor, 1)
			} else if s.smartState.kind == SmartTypeEmoji {
				panelY += heightNormal + 8
				emojiName := "No match"
				matchCount := 0
				if len(s.smartState.emojiMatches) > 0 && s.smartState.emojiIdx >= 0 && s.smartState.emojiIdx < len(s.smartState.emojiMatches) {
					emojiName = s.smartState.emojiMatches[s.smartState.emojiIdx].Name
					matchCount = len(s.smartState.emojiMatches)
				}
				statusStr := fmt.Sprintf("%s (%d matches)", emojiName, matchCount)
				s.drawText(img, statusStr, 30, panelY, mutedColor, 1)

				// Query input line
				panelY += heightSmall + 9
				qLabel := "  > "
				if s.smartState.query != "" {
					qLabel += s.smartState.query + "_"
				} else {
					qLabel += "type emoji name/tags or \u2190\u2192 to cycle"
				}
				qColor := mutedColor
				if s.smartState.query != "" {
					qColor = color.RGBA{R: 180, G: 230, B: 180, A: 255}
				}
				s.drawText(img, qLabel, 20, panelY, qColor, 1)
			} else if s.smartState.kind == SmartTypePrompt {
				panelY += heightNormal + 8
				roleName := "No match"
				matchCount := 0
				if len(s.smartState.promptMatches) > 0 && s.smartState.promptIdx >= 0 && s.smartState.promptIdx < len(s.smartState.promptMatches) {
					roleName = s.smartState.promptMatches[s.smartState.promptIdx].Name
					matchCount = len(s.smartState.promptMatches)
				}
				statusStr := fmt.Sprintf("%s (%d matches)", roleName, matchCount)
				s.drawText(img, statusStr, 30, panelY, mutedColor, 1)

				// Query input line
				panelY += heightSmall + 9
				qLabel := "  > "
				if s.smartState.query != "" {
					qLabel += s.smartState.query + "_"
				} else {
					qLabel += "type prompt role/tags or \u2190\u2192 to cycle"
				}
				qColor := mutedColor
				if s.smartState.query != "" {
					qColor = color.RGBA{R: 180, G: 230, B: 180, A: 255}
				}
				s.drawText(img, qLabel, 20, panelY, qColor, 1)
			}
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
			if s.smartState == nil || s.smartState.kind != SmartTypeTime {
				s.smartState = newSmartState()
			}
			return
		} else if snip.Smart == SmartTypeIP {
			if s.smartState == nil || s.smartState.kind != SmartTypeIP {
				s.smartState = &SmartState{
					kind:      SmartTypeIP,
					ipAddress: cachedIPAddress,
					ipErr:     cachedIPErr,
					ipFetched: cachedIPFetched,
				}
			}
			s.smartState.TriggerIPFetch(func() {
				s.redraw()
			})
			return
		} else if snip.Smart == SmartTypeEmoji {
			if s.smartState == nil || s.smartState.kind != SmartTypeEmoji {
				s.smartState = &SmartState{
					kind: SmartTypeEmoji,
				}
				s.smartState.resolveEmojiQuery()
			}
			return
		} else if snip.Smart == SmartTypePrompt {
			if s.smartState == nil || s.smartState.kind != SmartTypePrompt {
				s.smartState = &SmartState{
					kind: SmartTypePrompt,
				}
				s.smartState.resolvePromptQuery()
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

		// ── Smart snippet: Left/Right cycle presets / predictions ─────────
		if s.smartState != nil && (s.smartState.kind == SmartTypeTime || s.smartState.kind == SmartTypeEmoji || s.smartState.kind == SmartTypePrompt) {
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

func (s *pickerState) layoutMetrics() (startY, itemHeight, maxVisible, heightNormal, heightSmall int) {
	if s.faceNormal != nil {
		heightNormal = s.faceNormal.Metrics().Height.Round()
		heightSmall = s.faceSmall.Metrics().Height.Round()
	} else {
		heightNormal = 14
		heightSmall = 7
	}

	sepY := 20 + heightNormal + 14
	startY = sepY + 12

	itemHeight = 38
	if s.faceNormal != nil {
		if heightNormal+16 > itemHeight {
			itemHeight = heightNormal + 16
		}
	}

	maxVisible = (s.height - startY - 140) / itemHeight
	if maxVisible < 1 {
		maxVisible = 1
	}

	return startY, itemHeight, maxVisible, heightNormal, heightSmall
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

		startY, itemHeight, maxVisible, _, _ := s.layoutMetrics()

		if x >= 15 && x < s.width-15 && y >= startY && y < startY+maxVisible*itemHeight {
			clickedIdx := (y - startY) / itemHeight

			scrollOffset := 0
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

func loadPickerFonts(cfg *config.Config) (font.Face, font.Face) {
	fontSize := float64(cfg.SnippetPicker.FontSize)
	if fontSize <= 0 {
		fontSize = 14.0
	}

	var fontBytes []byte
	var err error

	if cfg.SnippetPicker.FontFace != "" {
		fontBytes, err = os.ReadFile(cfg.SnippetPicker.FontFace)
		if err != nil {
			log.Printf("[SnippetPicker] Failed to load configured font %q: %v. Falling back to system fonts.", cfg.SnippetPicker.FontFace, err)
		}
	}

	if len(fontBytes) == 0 {
		// Try fallback system fonts
		fontPaths := []string{
			"fonts/NotoSans-Regular.ttf",
			"../fonts/NotoSans-Regular.ttf",
			"/media/jang/home/Deve/zen-cap/fonts/NotoSans-Regular.ttf",
			"/usr/share/fonts/truetype/liberation/LiberationSans-Regular.ttf",
			"/usr/share/fonts/truetype/dejavu/DejaVuSans.ttf",
			"/usr/share/fonts/truetype/droid/DroidSansFallbackFull.ttf",
			"/usr/share/fonts/truetype/droid/DroidSansFallback.ttf",
		}
		for _, path := range fontPaths {
			fontBytes, err = os.ReadFile(path)
			if err == nil {
				break
			}
		}
	}

	if len(fontBytes) == 0 {
		// No font file found at all, we will return nil and use bitmap fallback
		return nil, nil
	}

	f, err := opentype.Parse(fontBytes)
	if err != nil {
		log.Printf("[SnippetPicker] Failed to parse font: %v", err)
		return nil, nil
	}

	faceNormal, err := opentype.NewFace(f, &opentype.FaceOptions{
		Size:    fontSize,
		DPI:     72,
		Hinting: font.HintingNone,
	})
	if err != nil {
		log.Printf("[SnippetPicker] Failed to create normal face: %v", err)
		return nil, nil
	}

	faceSmall, err := opentype.NewFace(f, &opentype.FaceOptions{
		Size:    fontSize * 0.75,
		DPI:     72,
		Hinting: font.HintingNone,
	})
	if err != nil {
		log.Printf("[SnippetPicker] Failed to create small face: %v", err)
		return faceNormal, faceNormal
	}

	return faceNormal, faceSmall
}

func (s *pickerState) drawText(img draw.Image, text string, x, y int, col color.Color, scale int) {
	var face font.Face
	if scale == 1 {
		face = s.faceSmall
	} else {
		face = s.faceNormal
	}

	if face == nil {
		capture.DrawStringScaled(img, text, x, y, col, scale)
		return
	}

	metrics := face.Metrics()
	ascent := metrics.Ascent.Round()

	d := &font.Drawer{
		Dst:  img,
		Src:  image.NewUniform(col),
		Face: face,
		Dot:  fixed.P(x, y+ascent),
	}
	d.DrawString(text)
}
