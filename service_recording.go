package main

import (
	"fmt"
	"image"
	"image/draw"
	"os"
	"path/filepath"
	"time"

	"github.com/jezek/xgb/xproto"

	"zen-cap/pkg/capture"
	"zen-cap/pkg/config"
	"zen-cap/pkg/recorder"
)

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
