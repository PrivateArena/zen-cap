// [VERIFIED]
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"zen-cap/pkg/capture"
	"zen-cap/pkg/config"
)

func handleScreenshot() error {
	fs := flag.NewFlagSet("screenshot", flag.ExitOnError)
	output := fs.String("o", "screenshot.png", "Output file path")
	region := fs.String("r", "", "Region geometry (X,Y,W,H e.g. 100,200,800,600 or 'interactive')")
	window := fs.String("w", "", "Target window: 'active', 'list', 'interactive', or specific window ID (e.g. 0x40000a)")
	screen := fs.String("s", "", "Target screen index: 'list' or screen index (e.g. 0, 1)")
	disp := fs.String("d", ":0.0", "X11 display")
	clipMode := fs.String("c", "", "Clipboard mode: 'image', 'path', 'ocr', 'translate', 'none' (overrides config)")

	if err := fs.Parse(os.Args[2:]); err != nil {
		return err
	}

	x, y, w, h := -1, -1, 0, 0
	var windowID uint32
	interactive := false
	windowSelect := false

	// List screens
	if *screen == "list" {
		return listScreens()
	}

	// List windows
	if *window == "list" {
		return listWindows()
	}

	// Handle window selection
	if *window != "" {
		if *window == "active" {
			win, err := getActiveWindowInfo()
			if err != nil {
				return err
			}
			windowID = win.ID
			fmt.Printf("Targeting active window: %s (0x%x)\n", win.Title, win.ID)
		} else if *window == "interactive" {
			interactive = true
			windowSelect = true
		} else {
			id, err := parseWindowID(*window)
			if err != nil {
				return err
			}
			windowID = id
		}
	} else if *screen != "" {
		// Handle screen selection
		idx, err := strconv.Atoi(*screen)
		if err != nil {
			return fmt.Errorf("invalid screen index %q: %w", *screen, err)
		}
		scr, err := getScreenInfo(idx)
		if err != nil {
			return err
		}
		x = scr.Geometry.X
		y = scr.Geometry.Y
		w = scr.Geometry.Width
		h = scr.Geometry.Height
		fmt.Printf("Targeting screen %d: %s (%dx%d at %d,%d)\n", idx, scr.Name, w, h, x, y)
	} else if *region != "" {
		if *region == "interactive" {
			interactive = true
		} else {
			// Handle custom region selection
			_, err := fmt.Sscanf(*region, "%d,%d,%d,%d", &x, &y, &w, &h)
			if err != nil {
				return fmt.Errorf("invalid region format, must be X,Y,W,H or 'interactive': %w", err)
			}
		}
	}

	cfg, _, err := config.LoadConfig()
	if err != nil {
		cfg = config.DefaultConfig()
	}

	outputPath := *output
	if !filepath.IsAbs(outputPath) {
		outputPath = filepath.Join(cfg.OutputDir, outputPath)
	}

	if err := os.MkdirAll(filepath.Dir(outputPath), 0755); err != nil {
		return fmt.Errorf("failed to create directory for output: %w", err)
	}

	var chosenAction string
	capCfg := capture.CaptureConfig{
		Display:         *disp,
		X:               x,
		Y:               y,
		Width:           w,
		Height:          h,
		WindowID:        windowID,
		Interactive:     interactive,
		WindowSelect:    windowSelect,
		ClipboardAction: &chosenAction,
	}

	img, err := capture.CaptureScreen(capCfg)
	if err != nil {
		return err
	}

	if err := capture.SavePNG(img, outputPath); err != nil {
		return err
	}

	fmt.Printf("Screenshot saved successfully to %s\n", outputPath)

	// Resolve clipboard action
	action := cfg.ClipboardMode
	if *clipMode != "" {
		action = *clipMode
	}
	if chosenAction != "" {
		action = chosenAction // Dynamic in-crop selection takes precedence!
	}

	absPath, err := filepath.Abs(outputPath)
	if err != nil {
		absPath = outputPath
	}
	processClipboardAction(img, absPath, action, cfg)

	return nil
}
