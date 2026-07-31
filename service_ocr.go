package main

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"zen-cap/pkg/capture"
	"zen-cap/pkg/config"
	"zen-cap/pkg/pipeline"
)

func (s *serviceState) runOCRScreenshotLoop(ch <-chan struct{}) {
	for range ch {
		go func() {
			if freshCfg, _, err := config.LoadConfig(); err == nil {
				s.setCfg(freshCfg)
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
			pipeline.Run(context.Background(), s.getCfg(), pipeline.Seed{
				Source: pipeline.SourceOCR,
				Kind:   pipeline.KindImage,
				Image:  img,
			})
		}()
	}
}

func (s *serviceState) runOCRRegionScreenshotLoop(ch <-chan struct{}) {
	for range ch {
		go func() {
			if freshCfg, _, err := config.LoadConfig(); err == nil {
				s.setCfg(freshCfg)
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
			pipeline.Run(context.Background(), s.getCfg(), pipeline.Seed{
				Source:  pipeline.SourceOCR,
				Kind:    pipeline.KindImage,
				Image:   img,
				Chosen:  chosenAction,
				OffsetX: chosenX,
				OffsetY: chosenY,
			})
		}()
	}
}

func (s *serviceState) runOCRWindowScreenshotLoop(ch <-chan struct{}) {
	for range ch {
		go func() {
			if freshCfg, _, err := config.LoadConfig(); err == nil {
				s.setCfg(freshCfg)
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
			pipeline.Run(context.Background(), s.getCfg(), pipeline.Seed{
				Source:  pipeline.SourceOCR,
				Kind:    pipeline.KindImage,
				Image:   img,
				Chosen:  chosenAction,
				OffsetX: chosenX,
				OffsetY: chosenY,
			})
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

			if len(freshCfg.OCRLanguages) == 0 {
				fmt.Println("[OCR Cycle] No OCR models/languages defined in config")
				return
			}

			currentIndex := -1
			for i, lang := range freshCfg.OCRLanguages {
				if lang == freshCfg.OCRLanguage {
					currentIndex = i
					break
				}
			}

			nextIndex := (currentIndex + 1) % len(freshCfg.OCRLanguages)
			nextLang := freshCfg.OCRLanguages[nextIndex]
			freshCfg.OCRLanguage = nextLang
			s.setCfg(freshCfg)

			if cfgPath != "" {
				if err := config.SaveConfig(freshCfg, cfgPath); err != nil {
					fmt.Printf("[OCR Cycle] Error saving config: %v\n", err)
				} else {
					fmt.Printf("[OCR Cycle] Updated config.json: ocr_language = %s\n", nextLang)
				}
			}

			resolvedAddress, err := capture.EnsureOCRServer(freshCfg.OCRAddress)
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
				s.setCfg(freshCfg)
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

			var overlay *capture.PersistentOverlay
			overlayNotified := false
			defer func() {
				if overlay != nil {
					overlay.Close()
				}
			}()

			var cfgPath string
			var cfgMtime time.Time

			for {
				select {
				case <-cancel:
					return
				default:
				}

				// Reload config only when the file on disk actually changed (red-team #13)
				if freshCfg, p, err := config.LoadConfig(); err == nil {
					changed := p != cfgPath
					if !changed {
						if st, statErr := os.Stat(p); statErr == nil {
							changed = !st.ModTime().Equal(cfgMtime)
						}
					}
					if changed {
						s.setCfg(freshCfg)
						cfgPath = p
						if st, statErr := os.Stat(p); statErr == nil {
							cfgMtime = st.ModTime()
						}
					}
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

				// Lazy-create the overlay sized to the region, or recreate if dims changed (F8/F13)
				b := img.Bounds()
				if overlay == nil || overlay.Width() != b.Dx() || overlay.Height() != b.Dy() {
					if overlay != nil {
						overlay.Close()
						overlay = nil
					}
					ov, err := capture.NewPersistentOverlay(img, offsetX, offsetY)
					if err != nil {
						if !overlayNotified {
							fmt.Printf("[OCR Auto] Failed to create overlay: %v\n", err)
							sendNotification("Zen-Cap OCR", "Auto-OCR overlay unavailable; continuing without display")
							overlayNotified = true
						}
					} else {
						overlay = ov
						overlayNotified = false
					}
				}

				opts := &pipeline.Options{}
				if overlay != nil {
					opts.DisplaySink = overlay
				}
				pipeline.Run(context.Background(), s.getCfg(), pipeline.Seed{
					Source:  pipeline.SourceOCRAuto,
					Kind:    pipeline.KindImage,
					Image:   img,
					Quiet:   true,
					OffsetX: offsetX,
					OffsetY: offsetY,
				}, opts)

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

			current := freshCfg.OCRAutoFPS
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

			s.ocrAutoMu.Lock()
			s.ocrAutoFPS = next
			s.ocrAutoMu.Unlock()

			freshCfg.OCRAutoFPS = next
			s.setCfg(freshCfg)

			if cfgPath != "" {
				if err := config.SaveConfig(freshCfg, cfgPath); err != nil {
					fmt.Printf("[OCR Auto FPS] Error saving config: %v\n", err)
				}
			}

			fmt.Printf("[OCR Auto FPS] Set to %.2f\n", next)
			sendNotification("Zen-Cap OCR", fmt.Sprintf("Auto-OCR FPS: %.2f", next))
		}()
	}
}
