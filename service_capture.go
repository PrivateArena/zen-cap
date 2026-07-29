package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"zen-cap/pkg/capture"
	"zen-cap/pkg/config"
	"zen-cap/pkg/pipeline"
)

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
