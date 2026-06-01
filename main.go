// [VERIFIED]
package main

import (
	"flag"
	"fmt"
	"image"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/jezek/xgbutil"
	"github.com/jezek/xgbutil/keybind"
	"github.com/jezek/xgbutil/xevent"

	"zen-cap/pkg/automation"
	"zen-cap/pkg/capture"
	"zen-cap/pkg/clipboard"
	"zen-cap/pkg/config"
	"zen-cap/pkg/display"
	"zen-cap/pkg/recorder"
	"zen-cap/pkg/snippet"
	"zen-cap/pkg/tui"
)

func main() {
	runCLI()
}

func runCLI() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	subcommand := os.Args[1]
	if subcommand == "clipboard-daemon" {
		if len(os.Args) >= 4 {
			capture.RunClipboardServer(os.Args[2], os.Args[3])
		}
		os.Exit(0)
	}

	switch subcommand {
	case "screenshot":
		if err := handleScreenshot(); err != nil {
			log.Fatalf("Screenshot failed: %v", err)
		}
	case "record":
		if err := handleRecord(); err != nil {
			log.Fatalf("Recording failed: %v", err)
		}
	case "service":
		if err := handleService(); err != nil {
			log.Fatalf("Service error: %v", err)
		}
	case "manage":
		cfg, _, err := config.LoadConfig()
		if err != nil {
			cfg = config.DefaultConfig()
		}
		snipMgr, err := snippet.NewManager(cfg.SnippetFile)
		if err != nil {
			log.Fatalf("Failed to initialize Snippet Manager: %v", err)
		}
		if err := tui.RunManager(cfg, snipMgr); err != nil {
			log.Fatalf("TUI Manager failed: %v", err)
		}
	case "snippet-picker":
		cfg, _, err := config.LoadConfig()
		if err != nil {
			cfg = config.DefaultConfig()
		}
		snipMgr, err := snippet.NewManager(cfg.SnippetFile)
		if err != nil {
			log.Fatalf("Failed to initialize Snippet Manager: %v", err)
		}
		if err := snippet.ShowPicker(snipMgr); err != nil {
			log.Fatalf("Snippet Picker failed: %v", err)
		}
	case "automation-picker":
		cfg, _, err := config.LoadConfig()
		if err != nil {
			cfg = config.DefaultConfig()
		}
		autoMgr, err := automation.NewManager(cfg.AutomationDir)
		if err != nil {
			log.Fatalf("Failed to initialize Automation Manager: %v", err)
		}
		if err := automation.ShowPicker(autoMgr, cfg); err != nil {
			log.Fatalf("Automation Picker failed: %v", err)
		}
	default:
		fmt.Printf("Unknown subcommand: %s\n", subcommand)
		printUsage()
		os.Exit(1)
	}
	os.Exit(0)
}

func printUsage() {
	fmt.Println("Usage: zen-cap <subcommand> [flags]")
	fmt.Println("\nSubcommands:")
	fmt.Println("  screenshot        Capture a screen, region, or window to PNG")
	fmt.Println("  record            Record video of a screen, region, or window to H.264 MP4")
	fmt.Println("  service           Run in background listening for global hotkeys:")
	fmt.Println("                      Ctrl+Shift+S -> Capture Fullscreen Screenshot")
	fmt.Println("                      Ctrl+Shift+R -> Toggle Recording")
	fmt.Println("                      Alt+`        -> Open Snippet Picker GUI")
	fmt.Println("                      Alt+a        -> Open Automation Picker GUI")
	fmt.Println("  manage            Launch the Snippet Manager TUI")
	fmt.Println("  snippet-picker    Open the native X11 Snippet Picker GUI")
	fmt.Println("  automation-picker Open the native X11 Automation Picker GUI")
}

func handleScreenshot() error {
	fs := flag.NewFlagSet("screenshot", flag.ExitOnError)
	output := fs.String("o", "screenshot.png", "Output file path")
	region := fs.String("r", "", "Region geometry (X,Y,W,H e.g. 100,200,800,600 or 'interactive')")
	window := fs.String("w", "", "Target window: 'active', 'list', or specific window ID (e.g. 0x40000a)")
	screen := fs.String("s", "", "Target screen index: 'list' or screen index (e.g. 0, 1)")
	disp := fs.String("d", ":0.0", "X11 display")
	clipMode := fs.String("c", "", "Clipboard mode: 'image', 'path', 'ocr', 'translate', 'none' (overrides config)")

	if err := fs.Parse(os.Args[2:]); err != nil {
		return err
	}

	x, y, w, h := -1, -1, 0, 0
	var windowID uint32
	interactive := false

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

func handleService() error {
	cfg, cfgPath, err := config.LoadConfig()
	if err != nil {
		log.Printf("Warning: Failed to load config, using defaults: %v", err)
		cfg = config.DefaultConfig()
	} else if cfgPath != "" {
		fmt.Printf("Loaded config from: %s\n", cfgPath)
	}

	// Ensure OutputDir exists
	if err := os.MkdirAll(cfg.OutputDir, 0755); err != nil {
		log.Printf("Warning: Failed to create output directory %q: %v", cfg.OutputDir, err)
	} else {
		fmt.Printf("Outputs will be saved to: %s\n", cfg.OutputDir)
	}

	fmt.Println("Zen-Cap hotkey service running in background...")
	fmt.Println("Hotkeys:")
	fmt.Printf("  %-14s -> Fullscreen Screenshot\n", cfg.Hotkeys.Screenshot)
	fmt.Printf("  %-14s -> Interactive Region Screenshot\n", cfg.Hotkeys.RegionScreenshot)
	fmt.Printf("  %-14s -> Toggle Fullscreen Recording\n", cfg.Hotkeys.RecordToggle)
	fmt.Printf("  %-14s -> Clipboard Manager: Copy (0-9)\n", cfg.Hotkeys.ClipboardCopyMod+"-[0-9]")
	fmt.Printf("  %-14s -> Clipboard Manager: Paste (0-9)\n", cfg.Hotkeys.ClipboardPasteMod+"-[0-9]")
	fmt.Printf("  %-14s -> Clipboard Manager: Cycle Transform Rules\n", cfg.Hotkeys.ClipboardCycleRule)
	fmt.Printf("  %-14s -> Snippet Picker: Open GUI\n", cfg.Hotkeys.SnippetPicker)
	fmt.Printf("  %-14s -> Snippet Editor: Open snippets.yaml\n", "Shift-"+cfg.Hotkeys.SnippetPicker)
	fmt.Printf("  %-14s -> Automation Picker: Open GUI\n", cfg.Hotkeys.AutomationPicker)
	fmt.Printf("  %-14s -> Automation Editor: Open automations.yaml\n", "Shift-"+cfg.Hotkeys.AutomationPicker)
	fmt.Println("UNIX Signals:")
	fmt.Println("  SIGUSR1       -> Fullscreen Screenshot")
	fmt.Println("  SIGUSR2       -> Toggle Fullscreen Recording")
	fmt.Println("Safety Net:")
	fmt.Println("  Ctrl+Shift+X  -> Instantly kill zen-cap service (emergency fallback)")
	fmt.Println("Press Ctrl+C in terminal to exit service.")

	screenshotChan := make(chan struct{}, 1)
	regionScreenshotChan := make(chan struct{}, 1)
	recordChan := make(chan struct{}, 1)

	// Initialize X11 connection for global hotkeys
	X, err := xgbutil.NewConn()
	if err != nil {
		return fmt.Errorf("failed to connect to X server: %w", err)
	}
	keybind.Initialize(X)

	// Register Screenshot Hotkey
	keybind.KeyPressFun(func(xu *xgbutil.XUtil, ev xevent.KeyPressEvent) {
		fmt.Println("Hotkey pressed: Triggering screenshot...")
		select {
		case screenshotChan <- struct{}{}:
		default:
		}
	}).Connect(X, X.RootWin(), cfg.Hotkeys.Screenshot, true)

	// Register Region Screenshot Hotkey
	keybind.KeyPressFun(func(xu *xgbutil.XUtil, ev xevent.KeyPressEvent) {
		fmt.Println("Hotkey pressed: Triggering interactive region screenshot...")
		select {
		case regionScreenshotChan <- struct{}{}:
		default:
		}
	}).Connect(X, X.RootWin(), cfg.Hotkeys.RegionScreenshot, true)

	// Register Recording Hotkey
	keybind.KeyPressFun(func(xu *xgbutil.XUtil, ev xevent.KeyPressEvent) {
		fmt.Println("Hotkey pressed: Triggering recording toggle...")
		select {
		case recordChan <- struct{}{}:
		default:
		}
	}).Connect(X, X.RootWin(), cfg.Hotkeys.RecordToggle, true)

	// Initialize Clipboard Manager
	mgr, err := clipboard.NewManager(cfg)
	if err != nil {
		log.Printf("Warning: Failed to initialize clipboard manager: %v", err)
	}

	if mgr != nil {
		// Register Copy Slots (0-9 and KP_0..KP_9)
		for i := 0; i <= 9; i++ {
			slot := i
			handler := func(xu *xgbutil.XUtil, ev xevent.KeyPressEvent) {
				go mgr.CopyToSlot(slot)
			}
			// Keyboard row digits
			keybind.KeyPressFun(handler).Connect(X, X.RootWin(), fmt.Sprintf("%s-%d", cfg.Hotkeys.ClipboardCopyMod, slot), true)
			// Numpad digits
			keybind.KeyPressFun(handler).Connect(X, X.RootWin(), fmt.Sprintf("%s-KP_%d", cfg.Hotkeys.ClipboardCopyMod, slot), true)
		}

		// Register Paste Slots (0-9 and KP_0..KP_9)
		for i := 0; i <= 9; i++ {
			slot := i
			handler := func(xu *xgbutil.XUtil, ev xevent.KeyPressEvent) {
				go mgr.PasteFromSlot(slot)
			}
			// Keyboard row digits
			keybind.KeyPressFun(handler).Connect(X, X.RootWin(), fmt.Sprintf("%s-%d", cfg.Hotkeys.ClipboardPasteMod, slot), true)
			// Numpad digits
			keybind.KeyPressFun(handler).Connect(X, X.RootWin(), fmt.Sprintf("%s-KP_%d", cfg.Hotkeys.ClipboardPasteMod, slot), true)
		}

		// Register Cycle Rule Hotkey
		keybind.KeyPressFun(func(xu *xgbutil.XUtil, ev xevent.KeyPressEvent) {
			go mgr.CycleTransform()
		}).Connect(X, X.RootWin(), cfg.Hotkeys.ClipboardCycleRule, true)
	}

	// Register Snippet Picker Hotkey
	keybind.KeyPressFun(func(xu *xgbutil.XUtil, ev xevent.KeyPressEvent) {
		exe, err := os.Executable()
		if err != nil {
			exe = "zen-cap"
		}
		cmd := exec.Command(exe, "snippet-picker")
		cmd.Env = os.Environ()
		if err := cmd.Start(); err != nil {
			fmt.Printf("[Service] Failed to start snippet-picker: %v\n", err)
		}
	}).Connect(X, X.RootWin(), cfg.Hotkeys.SnippetPicker, true)

	// Register Snippet Editor Hotkey (Shift+Alt+`) for instant manual editing in default app
	keybind.KeyPressFun(func(xu *xgbutil.XUtil, ev xevent.KeyPressEvent) {
		fmt.Printf("[Service] Opening snippet file for editing: %s\n", cfg.SnippetFile)
		cmd := exec.Command("xdg-open", cfg.SnippetFile)
		if err := cmd.Start(); err != nil {
			fmt.Printf("[Service] Failed to open snippet file: %v\n", err)
		}
	}).Connect(X, X.RootWin(), "Shift-"+cfg.Hotkeys.SnippetPicker, true)

	// Register Automation Picker Hotkey
	keybind.KeyPressFun(func(xu *xgbutil.XUtil, ev xevent.KeyPressEvent) {
		exe, err := os.Executable()
		if err != nil {
			exe = "zen-cap"
		}
		cmd := exec.Command(exe, "automation-picker")
		cmd.Env = os.Environ()
		if err := cmd.Start(); err != nil {
			fmt.Printf("[Service] Failed to start automation-picker: %v\n", err)
		}
	}).Connect(X, X.RootWin(), cfg.Hotkeys.AutomationPicker, true)

	// Register Automation Editor Hotkey (Shift+Alt+a) for instant manual editing in default app
	keybind.KeyPressFun(func(xu *xgbutil.XUtil, ev xevent.KeyPressEvent) {
		fmt.Printf("[Service] Opening automation directory: %s\n", cfg.AutomationDir)
		cmd := exec.Command("xdg-open", cfg.AutomationDir)
		if err := cmd.Start(); err != nil {
			fmt.Printf("[Service] Failed to open automation directory: %v\n", err)
		}
	}).Connect(X, X.RootWin(), "Shift-"+cfg.Hotkeys.AutomationPicker, true)

	// Register Global Safety Kill Hotkey (Ctrl+Shift+X) - emergency exit
	safetyKillHandler := func(xu *xgbutil.XUtil, ev xevent.KeyPressEvent) {
		fmt.Println("CRITICAL: Global safety kill hotkey pressed! Terminating zen-cap service immediately.")
		os.Exit(1)
	}
	keybind.KeyPressFun(safetyKillHandler).Connect(X, X.RootWin(), "Control-Shift-x", true)
	keybind.KeyPressFun(safetyKillHandler).Connect(X, X.RootWin(), "Control-Shift-X", true)

	var activeRec *recorder.Recorder
	var recMu sync.Mutex

	// Monitor OS signals for termination, screenshot, and recording
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM, syscall.SIGUSR1, syscall.SIGUSR2)

	go func() {
		for sig := range sigChan {
			switch sig {
			case os.Interrupt, syscall.SIGTERM:
				fmt.Println("\nShutting down service...")
				recMu.Lock()
				if activeRec != nil {
					fmt.Println("Stopping active recording before exit...")
					activeRec.Stop()
				}
				recMu.Unlock()
				os.Exit(0)
			case syscall.SIGUSR1:
				fmt.Println("Received SIGUSR1: Triggering screenshot...")
				select {
				case screenshotChan <- struct{}{}:
				default:
				}
			case syscall.SIGUSR2:
				fmt.Println("Received SIGUSR2: Triggering recording toggle...")
				select {
				case recordChan <- struct{}{}:
				default:
				}
			}
		}
	}()

	go func() {
		for range screenshotChan {
			go func() {
				timestamp := time.Now().Format("20060102_150405")
				filename := filepath.Join(cfg.OutputDir, fmt.Sprintf("screenshot_%s.png", timestamp))
				fmt.Printf("[%s] Capturing fullscreen to %s...\n", time.Now().Format("15:04:05"), filename)

				// Ensure folder exists (e.g. if deleted mid-run)
				_ = os.MkdirAll(cfg.OutputDir, 0755)

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
				processClipboardAction(img, absPath, cfg.ClipboardMode, cfg)
			}()
		}
	}()

	go func() {
		for range regionScreenshotChan {
			go func() {
				timestamp := time.Now().Format("20060102_150405")
				filename := filepath.Join(cfg.OutputDir, fmt.Sprintf("screenshot_region_%s.png", timestamp))
				fmt.Printf("[%s] Launching interactive region screenshot to %s...\n", time.Now().Format("15:04:05"), filename)

				// Ensure folder exists
				_ = os.MkdirAll(cfg.OutputDir, 0755)

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

				action := cfg.ClipboardMode
				if chosenAction != "" {
					action = chosenAction
				}
				absPath, err := filepath.Abs(filename)
				if err != nil {
					absPath = filename
				}
				processClipboardAction(img, absPath, action, cfg)
			}()
		}
	}()

	go func() {
		for range recordChan {
			recMu.Lock()
			if activeRec == nil {
				timestamp := time.Now().Format("20060102_150405")
				filename := filepath.Join(cfg.OutputDir, fmt.Sprintf("recording_%s.mp4", timestamp))
				fmt.Printf("[%s] Starting fullscreen recording to %s...\n", time.Now().Format("15:04:05"), filename)

				// Ensure folder exists (e.g. if deleted mid-run)
				_ = os.MkdirAll(cfg.OutputDir, 0755)

				recCfg := recorder.RecorderConfig{
					Display:    ":0.0",
					X:          -1,
					Y:          -1,
					FPS:        30,
					OutputPath: filename,
					Bitrate:    4000000,
				}
				rec := recorder.NewRecorder(recCfg)
				if err := rec.Start(); err != nil {
					fmt.Printf("Error starting recorder: %v\n", err)
					recMu.Unlock()
					continue
				}
				activeRec = rec
			} else {
				fmt.Printf("[%s] Stopping recording...\n", time.Now().Format("15:04:05"))
				rec := activeRec
				activeRec = nil
				go func() {
					if err := rec.Stop(); err != nil {
						fmt.Printf("Error stopping recorder: %v\n", err)
					} else {
						fmt.Printf("Recording saved successfully\n")
					}
				}()
			}
			recMu.Unlock()
		}
	}()

	xevent.Main(X)
	return nil
}

// Helper X11 queries

func listScreens() error {
	dm, err := display.NewX11DisplayManager()
	if err != nil {
		return err
	}
	defer dm.Close()

	screens, err := dm.GetScreens()
	if err != nil {
		return err
	}

	fmt.Printf("%-6s %-15s %s\n", "Index", "Name", "Geometry")
	for _, s := range screens {
		fmt.Printf("%-6d %-15s %dx%d at (%d,%d)\n", s.Index, s.Name, s.Geometry.Width, s.Geometry.Height, s.Geometry.X, s.Geometry.Y)
	}
	return nil
}

func listWindows() error {
	dm, err := display.NewX11DisplayManager()
	if err != nil {
		return err
	}
	defer dm.Close()

	windows, err := dm.GetWindows()
	if err != nil {
		return err
	}

	fmt.Printf("%-12s %-20s %s\n", "Window ID", "Class", "Title")
	for _, w := range windows {
		fmt.Printf("0x%08x   %-20s %s\n", w.ID, w.Class, w.Title)
	}
	return nil
}

func getScreenInfo(idx int) (display.Screen, error) {
	dm, err := display.NewX11DisplayManager()
	if err != nil {
		return display.Screen{}, err
	}
	defer dm.Close()

	screens, err := dm.GetScreens()
	if err != nil {
		return display.Screen{}, err
	}

	if idx < 0 || idx >= len(screens) {
		return display.Screen{}, fmt.Errorf("screen index %d out of range (found %d screens)", idx, len(screens))
	}
	return screens[idx], nil
}

func getActiveWindowInfo() (display.Window, error) {
	dm, err := display.NewX11DisplayManager()
	if err != nil {
		return display.Window{}, err
	}
	defer dm.Close()

	win, err := dm.GetActiveWindow()
	if err != nil {
		return display.Window{}, err
	}
	return *win, nil
}

func parseWindowID(str string) (uint32, error) {
	// Parse window ID as hex or decimal
	var id uint64
	var err error
	if len(str) > 2 && str[:2] == "0x" {
		id, err = strconv.ParseUint(str[2:], 16, 32)
	} else {
		id, err = strconv.ParseUint(str, 10, 32)
	}
	if err != nil {
		return 0, fmt.Errorf("invalid window ID %q: %w", str, err)
	}
	return uint32(id), nil
}

func processClipboardAction(img image.Image, absPath string, action string, cfg *config.Config) {
	if action == "" || action == "none" {
		return
	}

	switch action {
	case "image":
		if err := capture.SpawnClipboardDaemon("--image", absPath); err != nil {
			fmt.Printf("Error spawning clipboard daemon for image: %v\n", err)
		} else {
			fmt.Println("[Clipboard] Copied image to clipboard.")
			sendNotification("Zen-Cap", "Copied captured image to clipboard!")
		}
	case "path":
		if err := capture.SpawnClipboardDaemon("--text", absPath); err != nil {
			fmt.Printf("Error spawning clipboard daemon for path: %v\n", err)
		} else {
			fmt.Printf("[Clipboard] Copied path to clipboard: %s\n", absPath)
			sendNotification("Zen-Cap", "Copied image file path to clipboard!")
		}
	case "ocr":
		fmt.Println("[OCR] Running OCR on captured region...")
		text, err := capture.PerformOCR(img, cfg.OCRAddress, cfg.OCRLanguage)
		if err != nil {
			fmt.Printf("OCR failed: %v\n", err)
			sendNotification("Zen-Cap OCR", fmt.Sprintf("OCR failed: %v", err))
			return
		}
		if text == "" {
			fmt.Println("[OCR] No text was detected in region.")
			sendNotification("Zen-Cap OCR", "No text was detected in captured region.")
			return
		}
		if err := capture.SpawnClipboardDaemon("--text", text); err != nil {
			fmt.Printf("Error spawning clipboard daemon for OCR text: %v\n", err)
		} else {
			fmt.Printf("[OCR] Copied extracted text to clipboard:\n%s\n", text)
			sendNotification("Zen-Cap OCR", fmt.Sprintf("Copied extracted text to clipboard (%d chars)!", len(text)))
		}
	case "translate":
		fmt.Println("[OCR] Running OCR for translation...")
		text, err := capture.PerformOCR(img, cfg.OCRAddress, cfg.OCRLanguage)
		if err != nil {
			fmt.Printf("OCR failed: %v\n", err)
			sendNotification("Zen-Cap Translate", fmt.Sprintf("OCR failed: %v", err))
			return
		}
		if text == "" {
			fmt.Println("[OCR] No text was detected for translation.")
			sendNotification("Zen-Cap Translate", "No text was detected in captured region.")
			return
		}
		fmt.Printf("[Translate] Translating extracted text to %s...\n", cfg.TranslationTarget)
		translated, err := capture.TranslateText(text, cfg.TranslationTarget)
		if err != nil {
			fmt.Printf("Translation failed: %v\n", err)
			sendNotification("Zen-Cap Translate", fmt.Sprintf("Translation failed: %v", err))
			return
		}
		if err := capture.SpawnClipboardDaemon("--text", translated); err != nil {
			fmt.Printf("Error spawning clipboard daemon for translation: %v\n", err)
		} else {
			fmt.Printf("[Translate] Copied translated text to clipboard:\n%s\n", translated)
			sendNotification("Zen-Cap Translate", "Copied translated text to clipboard!")
		}
	}
}

func sendNotification(title, message string) {
	_ = exec.Command("notify-send", "-a", "Zen-Cap", title, message).Run()
}
