// [VERIFIED]
package main

import (
	"context"
	"fmt"
	"image"
	"image/draw"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/jezek/xgb/xproto"

	"zen-cap/pkg/capture"
	"zen-cap/pkg/config"
	"zen-cap/pkg/pipeline"
	"zen-cap/pkg/recorder"
)

func (s *serviceState) runSignalHandler(sigChan chan os.Signal, ch *serviceChannels) {
	for sig := range sigChan {
		switch sig {
		case os.Interrupt, syscall.SIGTERM:
			fmt.Println("\nShutting down service...")
			s.recMu.Lock()
			if s.activeRec != nil {
				fmt.Println("Stopping active recording before exit...")
				s.activeRec.Stop()
			}
			s.recMu.Unlock()
			os.Exit(0)
		case syscall.SIGUSR1:
			fmt.Println("Received SIGUSR1: Triggering screenshot...")
			select {
			case ch.Screenshot <- struct{}{}:
			default:
			}
		case syscall.SIGUSR2:
			fmt.Println("Received SIGUSR2: Triggering recording toggle...")
			select {
			case ch.Record <- struct{}{}:
			default:
			}
		}
	}
}

func (s *serviceState) runScreenshotLoop(ch <-chan struct{}) {
	for range ch {
		go func() {
			if freshCfg, _, err := config.LoadConfig(); err == nil {
				s.cfg = freshCfg
			}
			timestamp := time.Now().Format("20060102_150405")
			filename := filepath.Join(s.cfg.OutputDir, fmt.Sprintf("screenshot_%s.png", timestamp))
			fmt.Printf("[%s] Capturing fullscreen to %s...\n", time.Now().Format("15:04:05"), filename)

			_ = os.MkdirAll(s.cfg.OutputDir, 0755)

			capCfg := capture.CaptureConfig{
				Display: ":0.0",
				X:       -1,
				Y:       -1,
			}
			img, err := capture.CaptureScreen(capCfg)
			if err != nil {
				fmt.Printf("Error capturing screenshot: %v\n", err)
				return
			}
			if err := capture.SavePNG(img, filename); err != nil {
				fmt.Printf("Error saving screenshot: %v\n", err)
				return
			}
			fmt.Printf("Screenshot saved successfully to %s\n", filename)

			absPath, err := filepath.Abs(filename)
			if err != nil {
				absPath = filename
			}
			pipeline.Run(context.Background(), s.cfg, img, absPath, "")
		}()
	}
}

func (s *serviceState) runRegionScreenshotLoop(ch <-chan struct{}) {
	for range ch {
		go func() {
			if freshCfg, _, err := config.LoadConfig(); err == nil {
				s.cfg = freshCfg
			}
			timestamp := time.Now().Format("20060102_150405")
			filename := filepath.Join(s.cfg.OutputDir, fmt.Sprintf("screenshot_region_%s.png", timestamp))
			fmt.Printf("[%s] Launching interactive region screenshot to %s...\n", time.Now().Format("15:04:05"), filename)

			_ = os.MkdirAll(s.cfg.OutputDir, 0755)

			var chosenAction string
			capCfg := capture.CaptureConfig{
				Display:         ":0.0",
				X:               -1,
				Y:               -1,
				Interactive:     true,
				ClipboardAction: &chosenAction,
			}
			img, err := capture.CaptureScreen(capCfg)
			if err != nil {
				fmt.Printf("Error capturing region screenshot: %v\n", err)
				return
			}
			if err := capture.SavePNG(img, filename); err != nil {
				fmt.Printf("Error saving region screenshot: %v\n", err)
				return
			}
			fmt.Printf("Region screenshot saved successfully to %s\n", filename)

			absPath, err := filepath.Abs(filename)
			if err != nil {
				absPath = filename
			}
			pipeline.Run(context.Background(), s.cfg, img, absPath, chosenAction)
		}()
	}
}

func (s *serviceState) runWindowScreenshotLoop(ch <-chan struct{}) {
	for range ch {
		go func() {
			if freshCfg, _, err := config.LoadConfig(); err == nil {
				s.cfg = freshCfg
			}
			timestamp := time.Now().Format("20060102_150405")
			filename := filepath.Join(s.cfg.OutputDir, fmt.Sprintf("screenshot_window_%s.png", timestamp))
			fmt.Printf("[%s] Launching interactive window screenshot to %s...\n", time.Now().Format("15:04:05"), filename)

			_ = os.MkdirAll(s.cfg.OutputDir, 0755)

			var chosenAction string
			capCfg := capture.CaptureConfig{
				Display:         ":0.0",
				X:               -1,
				Y:               -1,
				Interactive:     true,
				WindowSelect:    true,
				ClipboardAction: &chosenAction,
			}
			img, err := capture.CaptureScreen(capCfg)
			if err != nil {
				fmt.Printf("Error capturing window screenshot: %v\n", err)
				return
			}
			if err := capture.SavePNG(img, filename); err != nil {
				fmt.Printf("Error saving window screenshot: %v\n", err)
				return
			}
			fmt.Printf("Window screenshot saved successfully to %s\n", filename)

			absPath, err := filepath.Abs(filename)
			if err != nil {
				absPath = filename
			}
			pipeline.Run(context.Background(), s.cfg, img, absPath, chosenAction)
		}()
	}
}

func (s *serviceState) runOCRScreenshotLoop(ch <-chan struct{}) {
	for range ch {
		go func() {
			if freshCfg, _, err := config.LoadConfig(); err == nil {
				s.cfg = freshCfg
			}
			fmt.Println("Launching fullscreen OCR/Translation...")
			capCfg := capture.CaptureConfig{
				Display: ":0.0",
				X:       -1,
				Y:       -1,
			}
			img, err := capture.CaptureScreen(capCfg)
			if err != nil {
				fmt.Printf("Error capturing fullscreen for OCR: %v\n", err)
				return
			}
			if err := capture.PerformOCROverlay(img, s.cfg.OCRAddress, s.cfg.OCRLanguage, s.cfg.TranslationTarget, s.cfg.TranslationEngine, s.cfg.AutoTranslate, s.cfg.OutputDir); err != nil {
				fmt.Printf("OCR Overlay error: %v\n", err)
			}
		}()
	}
}

func (s *serviceState) runOCRRegionScreenshotLoop(ch <-chan struct{}) {
	for range ch {
		go func() {
			if freshCfg, _, err := config.LoadConfig(); err == nil {
				s.cfg = freshCfg
			}
			fmt.Println("Launching region OCR/Translation...")
			var chosenAction string
			capCfg := capture.CaptureConfig{
				Display:         ":0.0",
				X:               -1,
				Y:               -1,
				Interactive:     true,
				ClipboardAction: &chosenAction,
			}
			img, err := capture.CaptureScreen(capCfg)
			if err != nil {
				fmt.Printf("Error capturing region for OCR: %v\n", err)
				return
			}
			if err := capture.PerformOCROverlay(img, s.cfg.OCRAddress, s.cfg.OCRLanguage, s.cfg.TranslationTarget, s.cfg.TranslationEngine, s.cfg.AutoTranslate, s.cfg.OutputDir); err != nil {
				fmt.Printf("OCR Overlay error: %v\n", err)
			}
		}()
	}
}

func (s *serviceState) runOCRWindowScreenshotLoop(ch <-chan struct{}) {
	for range ch {
		go func() {
			if freshCfg, _, err := config.LoadConfig(); err == nil {
				s.cfg = freshCfg
			}
			fmt.Println("Launching window OCR/Translation...")
			var chosenAction string
			capCfg := capture.CaptureConfig{
				Display:         ":0.0",
				X:               -1,
				Y:               -1,
				Interactive:     true,
				WindowSelect:    true,
				ClipboardAction: &chosenAction,
			}
			img, err := capture.CaptureScreen(capCfg)
			if err != nil {
				fmt.Printf("Error capturing window for OCR: %v\n", err)
				return
			}
			if err := capture.PerformOCROverlay(img, s.cfg.OCRAddress, s.cfg.OCRLanguage, s.cfg.TranslationTarget, s.cfg.TranslationEngine, s.cfg.AutoTranslate, s.cfg.OutputDir); err != nil {
				fmt.Printf("OCR Overlay error: %v\n", err)
			}
		}()
	}
}

func (s *serviceState) runOCRCycleModelLoop(ch <-chan struct{}) {
	for range ch {
		go func() {
			freshCfg, cfgPath, err := config.LoadConfig()
			if err != nil {
				fmt.Printf("[OCR Cycle] Error loading config: %v\n", err)
				return
			}
			s.cfg = freshCfg

			if len(s.cfg.OCRLanguages) == 0 {
				fmt.Println("[OCR Cycle] No OCR models/languages defined in config")
				return
			}

			currentIndex := -1
			for i, lang := range s.cfg.OCRLanguages {
				if lang == s.cfg.OCRLanguage {
					currentIndex = i
					break
				}
			}

			nextIndex := (currentIndex + 1) % len(s.cfg.OCRLanguages)
			nextLang := s.cfg.OCRLanguages[nextIndex]
			s.cfg.OCRLanguage = nextLang

			if cfgPath != "" {
				if err := config.SaveConfig(s.cfg, cfgPath); err != nil {
					fmt.Printf("[OCR Cycle] Error saving config: %v\n", err)
				} else {
					fmt.Printf("[OCR Cycle] Updated config.json: ocr_language = %s\n", nextLang)
				}
			}

			resolvedAddress, err := capture.EnsureOCRServer(s.cfg.OCRAddress)
			if err == nil {
				updateURL := fmt.Sprintf("%s/ocr?model=%s", strings.TrimSuffix(resolvedAddress, "/"), url.QueryEscape(nextLang))
				resp, err := http.Post(updateURL, "application/json", nil)
				if err == nil {
					resp.Body.Close()
					fmt.Printf("[OCR Cycle] Successfully notified OCR server to switch default model to: %s\n", nextLang)
				} else {
					fmt.Printf("[OCR Cycle] Failed to notify OCR server: %v\n", err)
				}
			} else {
				fmt.Printf("[OCR Cycle] OCR server is down or not found, updated local setting only: %v\n", err)
			}

			sendNotification("Zen-Cap OCR", fmt.Sprintf("Cycled OCR model to: %s", nextLang))
		}()
	}
}

func (s *serviceState) runSnippetCycleModeLoop(ch <-chan struct{}) {
	for range ch {
		go func() {
			freshCfg, cfgPath, err := config.LoadConfig()
			if err != nil {
				fmt.Printf("[Snippet Mode Cycle] Error loading config: %v\n", err)
				return
			}
			s.cfg = freshCfg

			newMode := "type"
			if s.cfg.SnippetMode == "type" {
				newMode = "paste"
			}
			s.cfg.SnippetMode = newMode

			if cfgPath != "" {
				if err := config.SaveConfig(s.cfg, cfgPath); err != nil {
					fmt.Printf("[Snippet Mode Cycle] Error saving config: %v\n", err)
				} else {
					fmt.Printf("[Snippet Mode Cycle] Updated config.json: snippet_mode = %s\n", newMode)
				}
			}

			modeLabel := "Normal Paste"
			if newMode == "type" {
				modeLabel = "Human Typing"
			}
			sendNotification("Zen-Cap Snippets", fmt.Sprintf("Cycled snippet mode to: %s", modeLabel))
		}()
	}
}

func (s *serviceState) runTaskProfileCycleLoop(ch <-chan struct{}) {
	for range ch {
		go func() {
			freshCfg, cfgPath, err := config.LoadConfig()
			if err != nil {
				fmt.Printf("[Profile Cycle] Error loading config: %v\n", err)
				return
			}
			s.cfg = freshCfg

			if len(s.cfg.TaskProfiles) == 0 {
				fmt.Println("[Profile Cycle] No task profiles defined in config")
				return
			}

			currentIndex := -1
			for i, p := range s.cfg.TaskProfiles {
				if p.Name == s.cfg.CurrentTaskProfile {
					currentIndex = i
					break
				}
			}

			nextIndex := (currentIndex + 1) % len(s.cfg.TaskProfiles)
			nextProfile := s.cfg.TaskProfiles[nextIndex]
			s.cfg.CurrentTaskProfile = nextProfile.Name

			if cfgPath != "" {
				if err := config.SaveConfig(s.cfg, cfgPath); err != nil {
					fmt.Printf("[Profile Cycle] Error saving config: %v\n", err)
				} else {
					fmt.Printf("[Profile Cycle] Updated config.json: current_task_profile = %s\n", nextProfile.Name)
				}
			}

			sendNotification("Zen-Cap Profile", fmt.Sprintf("Cycled task profile to: %s", nextProfile.Name))
		}()
	}
}

func (s *serviceState) runWindowClassGrabLoop(ch <-chan struct{}) {
	for range ch {
		s.windowClassGrabMu.Lock()
		if s.windowClassGrabRunning {
			s.windowClassGrabMu.Unlock()
			continue
		}
		s.windowClassGrabRunning = true
		s.windowClassGrabMu.Unlock()

		go func() {
			defer func() {
				s.windowClassGrabMu.Lock()
				s.windowClassGrabRunning = false
				s.windowClassGrabMu.Unlock()
			}()

			if freshCfg, _, err := config.LoadConfig(); err == nil {
				s.cfg = freshCfg
			}
			fmt.Println("Launching interactive window class grab...")

			wClass, err := capture.InteractiveSelectWindowClass(":0.0")
			if err != nil {
				fmt.Printf("Error grabbing window class: %v\n", err)
				return
			}

			if wClass == "" {
				return
			}

			if err := capture.SpawnClipboardDaemon("--text", wClass); err != nil {
				fmt.Printf("Error spawning clipboard daemon for window class: %v\n", err)
			} else {
				fmt.Printf("[Clipboard] Copied window class to clipboard: %s\n", wClass)
				sendNotification("Zen-Cap", fmt.Sprintf("Copied window class %q to clipboard!", wClass))
			}
		}()
	}
}

func (s *serviceState) runColorPickerLoop(ch <-chan struct{}) {
	for range ch {
		s.colorPickerMu.Lock()
		if s.colorPickerRunning {
			s.colorPickerMu.Unlock()
			continue
		}
		s.colorPickerRunning = true
		s.colorPickerMu.Unlock()

		go func() {
			defer func() {
				s.colorPickerMu.Lock()
				s.colorPickerRunning = false
				s.colorPickerMu.Unlock()
			}()

			if freshCfg, _, err := config.LoadConfig(); err == nil {
				s.cfg = freshCfg
			}
			fmt.Println("Launching interactive color picker...")

			capCfg := capture.CaptureConfig{
				Display:     ":0.0",
				X:           -1,
				Y:           -1,
				Interactive: false,
			}
			img, err := capture.CaptureScreen(capCfg)
			if err != nil {
				fmt.Printf("Error capturing fullscreen for color picker: %v\n", err)
				return
			}

			colorsText, err := capture.InteractiveColorPicker(img, s.cfg.ColorPickerFormat)
			if err != nil {
				fmt.Printf("Color picker error: %v\n", err)
				return
			}

			if colorsText == "" {
				return
			}

			fmt.Printf("[ColorPicker] Copied colors to clipboard: %s\n", colorsText)
			numColors := strings.Count(colorsText, "\n") + 1
			if numColors == 1 {
				sendNotification("Zen-Cap Color Picker", fmt.Sprintf("Copied color %s to clipboard!", colorsText))
			} else {
				sendNotification("Zen-Cap Color Picker", fmt.Sprintf("Copied %d colors to clipboard!", numColors))
			}
		}()
	}
}

func (s *serviceState) runRecordMarkFullscreenLoop(ch <-chan struct{}) {
	for range ch {
		s.markedAreaMu.Lock()
		s.markedArea = MarkedArea{
			X:      -1,
			Y:      -1,
			Width:  -1,
			Height: -1,
			Type:   "fullscreen",
		}
		s.markedAreaMu.Unlock()
		fmt.Println("[Recorder] Marked fullscreen area for recording")
		sendNotification("Zen-Cap", "Marked fullscreen for video recording!")
	}
}

func (s *serviceState) runRecordAudioOnlyLoop(ch <-chan struct{}) {
	for range ch {
		s.recMu.Lock()
		s.recordAudioOnly = !s.recordAudioOnly
		status := s.recordAudioOnly
		s.recMu.Unlock()

		var msg string
		if status {
			msg = "Audio-only recording enabled!"
		} else {
			msg = "Audio-only recording disabled (video + audio)!"
		}
		fmt.Printf("[Recorder] %s\n", msg)
		sendNotification("Zen-Cap", msg)
	}
}

func (s *serviceState) runRecordMarkRegionLoop(ch <-chan struct{}) {
	for range ch {
		go func() {
			if freshCfg, _, err := config.LoadConfig(); err == nil {
				s.cfg = freshCfg
			}
			fmt.Println("[Recorder] Launching interactive region select to mark recording area...")
			var action string
			var chosenX, chosenY, chosenW, chosenH int
			capCfg := capture.CaptureConfig{
				Display:         ":0.0",
				X:               -1,
				Y:               -1,
				Interactive:     true,
				ClipboardAction: &action,
				OutX:            &chosenX,
				OutY:            &chosenY,
				OutWidth:        &chosenW,
				OutHeight:       &chosenH,
			}
			_, err := capture.CaptureScreen(capCfg)
			if err != nil {
				fmt.Printf("[Recorder] Error marking region: %v\n", err)
				return
			}

			if chosenW%2 != 0 {
				chosenW--
			}
			if chosenH%2 != 0 {
				chosenH--
			}

			s.markedAreaMu.Lock()
			s.markedArea = MarkedArea{
				X:      chosenX,
				Y:      chosenY,
				Width:  chosenW,
				Height: chosenH,
				Type:   "region",
			}
			s.markedAreaMu.Unlock()

			msg := fmt.Sprintf("Marked region %dx%d at (%d, %d) for recording!", chosenW, chosenH, chosenX, chosenY)
			fmt.Printf("[Recorder] %s\n", msg)
			sendNotification("Zen-Cap", msg)
		}()
	}
}

func (s *serviceState) runRecordMarkWindowLoop(ch <-chan struct{}) {
	for range ch {
		go func() {
			if freshCfg, _, err := config.LoadConfig(); err == nil {
				s.cfg = freshCfg
			}
			fmt.Println("[Recorder] Launching interactive window select to mark recording area...")
			var action string
			var chosenX, chosenY, chosenW, chosenH int
			var chosenWinID uint32
			capCfg := capture.CaptureConfig{
				Display:         ":0.0",
				X:               -1,
				Y:               -1,
				Interactive:     true,
				WindowSelect:    true,
				ClipboardAction: &action,
				OutX:            &chosenX,
				OutY:            &chosenY,
				OutWidth:        &chosenW,
				OutHeight:       &chosenH,
				OutWindowID:     &chosenWinID,
			}
			_, err := capture.CaptureScreen(capCfg)
			if err != nil {
				fmt.Printf("[Recorder] Error marking window: %v\n", err)
				return
			}

			if chosenW%2 != 0 {
				chosenW--
			}
			if chosenH%2 != 0 {
				chosenH--
			}

			s.markedAreaMu.Lock()
			s.markedArea = MarkedArea{
				X:        chosenX,
				Y:        chosenY,
				Width:    chosenW,
				Height:   chosenH,
				WindowID: chosenWinID,
				Type:     "window",
			}
			s.markedAreaMu.Unlock()

			msg := fmt.Sprintf("Marked window (ID 0x%x) %dx%d at (%d, %d) for recording!", chosenWinID, chosenW, chosenH, chosenX, chosenY)
			fmt.Printf("[Recorder] %s\n", msg)
			sendNotification("Zen-Cap", msg)
		}()
	}
}

func (s *serviceState) runRecordShowAreaLoop(ch <-chan struct{}) {
	for range ch {
		s.activeBordersMu.Lock()
		if len(s.activeBorders) > 0 {
			for _, w := range s.activeBorders {
				xproto.DestroyWindow(s.X.Conn(), w)
			}
			s.activeBorders = nil
			s.activeBordersMu.Unlock()
			fmt.Println("[Recorder] Cleared recording area highlight overlay")
			continue
		}

		s.markedAreaMu.Lock()
		area := s.markedArea
		s.markedAreaMu.Unlock()

		screen := s.X.Screen()
		screenW := int(screen.WidthInPixels)
		screenH := int(screen.HeightInPixels)

		x, y, w, h := area.X, area.Y, area.Width, area.Height
		if area.Type == "fullscreen" || x < 0 || y < 0 || w <= 0 || h <= 0 {
			x, y, w, h = 0, 0, screenW, screenH
		}

		fmt.Printf("[Recorder] Highlighting recording area: %dx%d at (%d, %d)\n", w, h, x, y)

		borderThickness := 3
		borders := []struct{ x, y, width, height int }{
			{x, y - borderThickness, w, borderThickness},
			{x, y + h, w, borderThickness},
			{x - borderThickness, y, borderThickness, h},
			{x + w, y, borderThickness, h},
		}

		var created []xproto.Window
		success := true

		for _, b := range borders {
			bx := b.x
			by := b.y
			bw := b.width
			bh := b.height

			if bx < 0 {
				bw += bx
				bx = 0
			}
			if by < 0 {
				bh += by
				by = 0
			}
			if bw <= 0 || bh <= 0 {
				continue
			}

			winID, err := xproto.NewWindowId(s.X.Conn())
			if err != nil {
				success = false
				break
			}

			var backPixel uint32 = 0x00F0FF
			var overrideRedirect uint32 = 1

			err = xproto.CreateWindowChecked(
				s.X.Conn(),
				screen.RootDepth,
				winID,
				screen.Root,
				int16(bx), int16(by), uint16(bw), uint16(bh),
				0,
				xproto.WindowClassInputOutput,
				screen.RootVisual,
				xproto.CwOverrideRedirect|xproto.CwBackPixel,
				[]uint32{overrideRedirect, backPixel},
			).Check()

			if err != nil {
				success = false
				break
			}

			err = xproto.MapWindowChecked(s.X.Conn(), winID).Check()
			if err != nil {
				success = false
				break
			}

			created = append(created, winID)
		}

		if !success {
			for _, w := range created {
				xproto.DestroyWindow(s.X.Conn(), w)
			}
			s.activeBorders = nil
			fmt.Println("[Recorder] Failed to create highlight overlay windows")
		} else {
			s.activeBorders = created
			fmt.Println("[Recorder] Recording area highlight overlay mapped")
		}
		s.activeBordersMu.Unlock()
	}
}

func (s *serviceState) runRecordAnnotateLoop(ch <-chan struct{}) {
	for range ch {
		s.annotateMu.Lock()
		if s.annotateRunning {
			s.annotateMu.Unlock()
			continue
		}
		s.annotateRunning = true
		s.annotateMu.Unlock()

		go func() {
			defer func() {
				s.annotateMu.Lock()
				s.annotateRunning = false
				s.annotateMu.Unlock()
			}()

			if freshCfg, _, err := config.LoadConfig(); err == nil {
				s.cfg = freshCfg
			}

			fmt.Println("Opening annotation overlay...")

			capCfg := capture.CaptureConfig{
				Display:     ":0.0",
				X:           -1,
				Y:           -1,
				Interactive: false,
			}
			img, err := capture.CaptureScreen(capCfg)
			if err != nil {
				fmt.Printf("Annotation capture error: %v\n", err)
				return
			}

			rgba, ok := img.(*image.RGBA)
			if !ok {
				b := img.Bounds()
				rgba = image.NewRGBA(b)
				draw.Draw(rgba, b, img, b.Min, draw.Src)
			}

			result, err := capture.InteractiveAnnotate(rgba, s.cfg.Edit.BrushThickness, s.cfg.Edit.FontScale)
			if err != nil {
				fmt.Printf("Annotation error: %v\n", err)
			} else if result != nil {
				fmt.Printf("Annotation committed\n")
			} else {
				fmt.Println("Annotation cancelled")
			}
		}()
	}
}

func (s *serviceState) runRecordToggleLoop(ch <-chan struct{}) {
	for range ch {
		s.recMu.Lock()
		if s.activeRec == nil {
			if freshCfg, _, err := config.LoadConfig(); err == nil {
				s.cfg = freshCfg
			}
			timestamp := time.Now().Format("20060102_150405")
			ext := ".mp4"
			if s.recordAudioOnly {
				ext = ".m4a"
			}
			filename := filepath.Join(s.cfg.OutputDir, fmt.Sprintf("recording_%s%s", timestamp, ext))

			s.markedAreaMu.Lock()
			area := s.markedArea
			s.markedAreaMu.Unlock()

			var recordingMsg string
			recCfg := recorder.RecorderConfigFromConfig(s.cfg)
			recCfg.OutputPath = filename
			recCfg.AudioOnly = s.recordAudioOnly

			if s.recordAudioOnly {
				recCfg.AudioEnabled = true
				recordingMsg = fmt.Sprintf("[%s] Starting audio-only recording to %s...", time.Now().Format("15:04:05"), filename)
			} else if area.Type == "fullscreen" || area.X < 0 || area.Y < 0 || area.Width <= 0 || area.Height <= 0 {
				recCfg.X = -1
				recCfg.Y = -1
				recCfg.InternalWidth = -1
				recCfg.InternalHeight = -1
				recordingMsg = fmt.Sprintf("[%s] Starting fullscreen recording to %s...", time.Now().Format("15:04:05"), filename)
			} else {
				recCfg.X = area.X
				recCfg.Y = area.Y
				recCfg.InternalWidth = area.Width
				recCfg.InternalHeight = area.Height
				recCfg.WindowID = area.WindowID
				if area.Type == "window" && area.WindowID != 0 {
					recordingMsg = fmt.Sprintf("[%s] Starting window recording (ID: 0x%x, %dx%d at %d,%d) to %s...", time.Now().Format("15:04:05"), area.WindowID, area.Width, area.Height, area.X, area.Y, filename)
				} else {
					recordingMsg = fmt.Sprintf("[%s] Starting region recording (%dx%d at %d,%d) to %s...", time.Now().Format("15:04:05"), area.Width, area.Height, area.X, area.Y, filename)
				}
			}

			s.activeBordersMu.Lock()
			if len(s.activeBorders) > 0 {
				for _, w := range s.activeBorders {
					xproto.DestroyWindow(s.X.Conn(), w)
				}
				s.activeBorders = nil
				fmt.Println("[Recorder] Hidden preview overlay before starting recording")
			}
			s.activeBordersMu.Unlock()

			fmt.Println(recordingMsg)

			_ = os.MkdirAll(s.cfg.OutputDir, 0755)

			rec := recorder.NewRecorder(recCfg)
			if err := rec.Start(); err != nil {
				fmt.Printf("Error starting recorder: %v\n", err)
				s.recMu.Unlock()
				continue
			}
			s.activeRec = rec
		} else {
			fmt.Printf("[%s] Stopping recording...\n", time.Now().Format("15:04:05"))
			rec := s.activeRec
			s.activeRec = nil
			go func() {
				if err := rec.Stop(); err != nil {
					fmt.Printf("Error stopping recorder: %v\n", err)
				} else {
					fmt.Printf("Recording saved successfully\n")
				}
			}()
		}
		s.recMu.Unlock()
	}
}
