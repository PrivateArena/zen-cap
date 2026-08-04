package main

import (
	"fmt"
	"strings"

	"zen-cap/pkg/capture"
	"zen-cap/pkg/config"
)

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
				s.setCfg(freshCfg)
			}
			fmt.Println("Launching interactive window class grab...")

			wClass, err := capture.InteractiveSelectWindowClass("")
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
				s.setCfg(freshCfg)
			}
			fmt.Println("Launching interactive color picker...")

			capCfg := capture.CaptureConfig{
				Display:     "",
				X:           -1,
				Y:           -1,
				Interactive: false,
			}
			img, err := capture.CaptureScreen(capCfg)
			if err != nil {
				fmt.Printf("Error capturing fullscreen for color picker: %v\n", err)
				return
			}

			colorsText, err := capture.InteractiveColorPicker(img, s.getCfg().ColorPickerFormat)
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
