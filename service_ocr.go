// [VERIFIED]
package main

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"

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
