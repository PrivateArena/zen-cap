package main

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"zen-cap/pkg/capture"
	"zen-cap/pkg/config"
)

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
			if err := capture.PerformOCROverlay(img, s.cfg.OCRAddress, s.cfg.OCRLanguage, s.cfg.TranslationTarget, s.cfg.TranslationEngine, s.cfg.AutoTranslate, s.cfg.OutputDir, 0, 0); err != nil {
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
			var chosenX, chosenY, chosenW, chosenH int
			capCfg := capture.CaptureConfig{
				Display:         ":0.0",
				X:               -1,
				Y:               -1,
				Interactive:     true,
				ClipboardAction: &chosenAction,
				OutX:            &chosenX,
				OutY:            &chosenY,
				OutWidth:        &chosenW,
				OutHeight:       &chosenH,
			}
			img, err := capture.CaptureScreen(capCfg)
			if err != nil {
				fmt.Printf("Error capturing region for OCR: %v\n", err)
				return
			}
			if err := capture.PerformOCROverlay(img, s.cfg.OCRAddress, s.cfg.OCRLanguage, s.cfg.TranslationTarget, s.cfg.TranslationEngine, s.cfg.AutoTranslate, s.cfg.OutputDir, chosenX, chosenY); err != nil {
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
			var chosenX, chosenY, chosenW, chosenH int
			capCfg := capture.CaptureConfig{
				Display:         ":0.0",
				X:               -1,
				Y:               -1,
				Interactive:     true,
				WindowSelect:    true,
				ClipboardAction: &chosenAction,
				OutX:            &chosenX,
				OutY:            &chosenY,
				OutWidth:        &chosenW,
				OutHeight:       &chosenH,
			}
			img, err := capture.CaptureScreen(capCfg)
			if err != nil {
				fmt.Printf("Error capturing window for OCR: %v\n", err)
				return
			}
			if err := capture.PerformOCROverlay(img, s.cfg.OCRAddress, s.cfg.OCRLanguage, s.cfg.TranslationTarget, s.cfg.TranslationEngine, s.cfg.AutoTranslate, s.cfg.OutputDir, chosenX, chosenY); err != nil {
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

func (s *serviceState) runOCRAutoToggleLoop(ch <-chan struct{}) {
	for range ch {
		go func() {
			freshCfg, _, err := config.LoadConfig()
			if err == nil {
				s.cfg = freshCfg
			}

			s.ocrAutoMu.Lock()
			wasRunning := s.ocrAutoRunning
			if wasRunning {
				close(s.ocrAutoCancel)
				s.ocrAutoCancel = nil
				s.ocrAutoRunning = false
			}
			s.ocrAutoMu.Unlock()

			if wasRunning {
				fmt.Println("[OCR Auto] Stopped")
				sendNotification("Zen-Cap OCR", "Auto-OCR stopped")
				return
			}

			s.ocrAutoMu.Lock()
			s.ocrAutoCancel = make(chan struct{})
			s.ocrAutoRunning = true
			fps := s.ocrAutoFPS
			if fps <= 0 {
				fps = 1.0
			}
			cancel := s.ocrAutoCancel
			s.ocrAutoMu.Unlock()

			fmt.Printf("[OCR Auto] Started (FPS=%.2f)\n", fps)
			sendNotification("Zen-Cap OCR", fmt.Sprintf("Auto-OCR started (%.2f FPS)", fps))

			ticker := time.NewTicker(time.Duration(float64(time.Second) / fps))
			defer ticker.Stop()

			for {
				select {
				case <-cancel:
					return
				default:
				}

				if freshCfg2, _, err2 := config.LoadConfig(); err2 == nil {
					s.cfg = freshCfg2
				}

				s.markedAreaMu.Lock()
				area := s.markedArea
				s.markedAreaMu.Unlock()

				capCfg := capture.CaptureConfig{
					Display:  ":0.0",
					WindowID: 0,
				}

				offsetX, offsetY := 0, 0
				switch area.Type {
				case "region":
					if area.Width > 0 && area.Height > 0 {
						capCfg.X = area.X
						capCfg.Y = area.Y
						capCfg.Width = area.Width
						capCfg.Height = area.Height
						offsetX = area.X
						offsetY = area.Y
					}
				case "window":
					if area.WindowID != 0 {
						capCfg.WindowID = area.WindowID
						capCfg.X = area.X
						capCfg.Y = area.Y
						capCfg.Width = area.Width
						capCfg.Height = area.Height
						offsetX = area.X
						offsetY = area.Y
					}
				}

				img, err := capture.CaptureScreen(capCfg)
				if err != nil {
					fmt.Printf("[OCR Auto] Capture error: %v\n", err)
					select {
					case <-cancel:
						return
					case <-ticker.C:
					}
					continue
				}

				if err := capture.PerformOCROverlay(img, s.cfg.OCRAddress, s.cfg.OCRLanguage, s.cfg.TranslationTarget, s.cfg.TranslationEngine, s.cfg.AutoTranslate, s.cfg.OutputDir, offsetX, offsetY); err != nil {
					fmt.Printf("[OCR Auto] OCR error: %v\n", err)
				}

				select {
				case <-cancel:
					return
				case <-ticker.C:
				}
			}
		}()
	}
}

func (s *serviceState) runOCRAutoFPSLoop(ch <-chan struct{}) {
	presets := []float64{0.2, 0.5, 1.0, 2.0, 5.0}
	for range ch {
		go func() {
			freshCfg, cfgPath, err := config.LoadConfig()
			if err != nil {
				fmt.Printf("[OCR Auto FPS] Error loading config: %v\n", err)
				return
			}
			s.cfg = freshCfg

			current := s.cfg.OCRAutoFPS
			if current <= 0 {
				current = 1.0
			}

			idx := 0
			for i, p := range presets {
				if p == current {
					idx = i
					break
				}
			}
			next := presets[(idx+1)%len(presets)]
			s.cfg.OCRAutoFPS = next

			s.ocrAutoMu.Lock()
			s.ocrAutoFPS = next
			s.ocrAutoMu.Unlock()

			if cfgPath != "" {
				if err := config.SaveConfig(s.cfg, cfgPath); err != nil {
					fmt.Printf("[OCR Auto FPS] Error saving config: %v\n", err)
				}
			}

			fmt.Printf("[OCR Auto FPS] Set to %.2f\n", next)
			sendNotification("Zen-Cap OCR", fmt.Sprintf("Auto-OCR FPS: %.2f", next))
		}()
	}
}
