// [VERIFIED]
package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"zen-cap/pkg/config"
	"zen-cap/pkg/recorder"
)

func handleRecord() error {
	fs := flag.NewFlagSet("record", flag.ExitOnError)
	output := fs.String("o", "output.mp4", "Output file path")
	fps := fs.Int("fps", 30, "Frame rate")
	bitrate := fs.Int64("b", 4000000, "Bitrate in bps (default: 4Mbps)")
	duration := fs.Duration("t", 0, "Recording duration (e.g. 10s, 2m, default 0 for manual stop)")
	region := fs.String("r", "", "Region geometry (X,Y,W,H e.g. 100,200,800,600)")
	window := fs.String("w", "", "Target window: 'active', 'list', or specific window ID (e.g. 0x40000a)")
	screen := fs.String("s", "", "Target screen index: 'list' or screen index (e.g. 0, 1)")
	disp := fs.String("d", ":0.0", "X11 display")

	if err := fs.Parse(os.Args[2:]); err != nil {
		return err
	}

	x, y, w, h := -1, -1, 0, 0
	var windowID uint32

	if *screen == "list" {
		return listScreens()
	}

	if *window == "list" {
		return listWindows()
	}

	if *window != "" {
		if *window == "active" {
			win, err := getActiveWindowInfo()
			if err != nil {
				return err
			}
			windowID = win.ID
			fmt.Printf("Targeting active window: %s (0x%x)\n", win.Title, win.ID)
		} else {
			id, err := parseWindowID(*window)
			if err != nil {
				return err
			}
			windowID = id
		}
	} else if *screen != "" {
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
		_, err := fmt.Sscanf(*region, "%d,%d,%d,%d", &x, &y, &w, &h)
		if err != nil {
			return fmt.Errorf("invalid region format, must be X,Y,W,H: %w", err)
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

	recCfg := recorder.RecorderConfig{
		Display:    *disp,
		X:          x,
		Y:          y,
		Width:      w,
		Height:     h,
		FPS:        *fps,
		OutputPath: outputPath,
		Bitrate:    *bitrate,
		WindowID:   windowID,
	}

	rec := recorder.NewRecorder(recCfg)
	if err := rec.Start(); err != nil {
		return err
	}

	// Handle OS interrupt signals
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	if *duration > 0 {
		fmt.Printf("Recording for %v... Press Ctrl+C to stop early.\n", *duration)
		select {
		case <-sigChan:
			fmt.Println("\nReceived interrupt, stopping...")
		case <-time.After(*duration):
			fmt.Println("\nDuration reached, stopping...")
		}
	} else {
		fmt.Println("Recording... Press Ctrl+C to stop.")
		<-sigChan
		fmt.Println("\nStopping recording...")
	}

	if err := rec.Stop(); err != nil {
		return fmt.Errorf("failed to stop recorder: %w", err)
	}

	fmt.Printf("Recording saved successfully to %s\n", outputPath)
	return nil
}
