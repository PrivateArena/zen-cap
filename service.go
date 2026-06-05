// [VERIFIED]
package main

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/jezek/xgbutil"
	"github.com/jezek/xgbutil/keybind"
	"github.com/jezek/xgbutil/xevent"

	"zen-cap/pkg/capture"
	"zen-cap/pkg/clipboard"
	"zen-cap/pkg/config"
	"zen-cap/pkg/recorder"
)

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
	fmt.Printf("  %-14s -> Interactive Window Screenshot\n", cfg.Hotkeys.WindowScreenshot)
	fmt.Printf("  %-14s -> Fullscreen OCR / Translation Overlay\n", cfg.Hotkeys.OCRScreenshot)
	fmt.Printf("  %-14s -> Interactive Region OCR / Translation Overlay\n", cfg.Hotkeys.OCRRegionScreenshot)
	fmt.Printf("  %-14s -> Interactive Window OCR / Translation Overlay\n", cfg.Hotkeys.OCRWindowScreenshot)
	fmt.Printf("  %-14s -> Grab Window Class to Clipboard\n", cfg.Hotkeys.WindowClassGrab)
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
	windowScreenshotChan := make(chan struct{}, 1)
	ocrScreenshotChan := make(chan struct{}, 1)
	ocrRegionScreenshotChan := make(chan struct{}, 1)
	ocrWindowScreenshotChan := make(chan struct{}, 1)
	windowClassGrabChan := make(chan struct{}, 1)
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

	// Register Window Screenshot Hotkey
	keybind.KeyPressFun(func(xu *xgbutil.XUtil, ev xevent.KeyPressEvent) {
		fmt.Println("Hotkey pressed: Triggering interactive window screenshot...")
		select {
		case windowScreenshotChan <- struct{}{}:
		default:
		}
	}).Connect(X, X.RootWin(), cfg.Hotkeys.WindowScreenshot, true)

	// Register Fullscreen OCR Hotkey
	keybind.KeyPressFun(func(xu *xgbutil.XUtil, ev xevent.KeyPressEvent) {
		fmt.Println("Hotkey pressed: Triggering fullscreen OCR/Translation overlay...")
		select {
		case ocrScreenshotChan <- struct{}{}:
		default:
		}
	}).Connect(X, X.RootWin(), cfg.Hotkeys.OCRScreenshot, true)

	// Register Region OCR Hotkey
	keybind.KeyPressFun(func(xu *xgbutil.XUtil, ev xevent.KeyPressEvent) {
		fmt.Println("Hotkey pressed: Triggering region OCR/Translation overlay...")
		select {
		case ocrRegionScreenshotChan <- struct{}{}:
		default:
		}
	}).Connect(X, X.RootWin(), cfg.Hotkeys.OCRRegionScreenshot, true)

	// Register Window OCR Hotkey
	keybind.KeyPressFun(func(xu *xgbutil.XUtil, ev xevent.KeyPressEvent) {
		fmt.Println("Hotkey pressed: Triggering window OCR/Translation overlay...")
		select {
		case ocrWindowScreenshotChan <- struct{}{}:
		default:
		}
	}).Connect(X, X.RootWin(), cfg.Hotkeys.OCRWindowScreenshot, true)

	// Register Window Class Grab Hotkey
	keybind.KeyPressFun(func(xu *xgbutil.XUtil, ev xevent.KeyPressEvent) {
		fmt.Println("Hotkey pressed: Triggering interactive window class grab...")
		select {
		case windowClassGrabChan <- struct{}{}:
		default:
		}
	}).Connect(X, X.RootWin(), cfg.Hotkeys.WindowClassGrab, true)

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
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
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
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
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
				if freshCfg, _, err := config.LoadConfig(); err == nil {
					cfg = freshCfg
				}
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
				if freshCfg, _, err := config.LoadConfig(); err == nil {
					cfg = freshCfg
				}
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
		for range windowScreenshotChan {
			go func() {
				if freshCfg, _, err := config.LoadConfig(); err == nil {
					cfg = freshCfg
				}
				timestamp := time.Now().Format("20060102_150405")
				filename := filepath.Join(cfg.OutputDir, fmt.Sprintf("screenshot_window_%s.png", timestamp))
				fmt.Printf("[%s] Launching interactive window screenshot to %s...\n", time.Now().Format("15:04:05"), filename)

				// Ensure folder exists
				_ = os.MkdirAll(cfg.OutputDir, 0755)

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
		for range ocrScreenshotChan {
			go func() {
				if freshCfg, _, err := config.LoadConfig(); err == nil {
					cfg = freshCfg
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
				if err := capture.PerformOCROverlay(img, cfg.OCRAddress, cfg.OCRLanguage, cfg.TranslationTarget, cfg.TranslationEngine, cfg.AutoTranslate, cfg.OutputDir); err != nil {
					fmt.Printf("OCR Overlay error: %v\n", err)
				}
			}()
		}
	}()

	go func() {
		for range ocrRegionScreenshotChan {
			go func() {
				if freshCfg, _, err := config.LoadConfig(); err == nil {
					cfg = freshCfg
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
				if err := capture.PerformOCROverlay(img, cfg.OCRAddress, cfg.OCRLanguage, cfg.TranslationTarget, cfg.TranslationEngine, cfg.AutoTranslate, cfg.OutputDir); err != nil {
					fmt.Printf("OCR Overlay error: %v\n", err)
				}
			}()
		}
	}()

	go func() {
		for range ocrWindowScreenshotChan {
			go func() {
				if freshCfg, _, err := config.LoadConfig(); err == nil {
					cfg = freshCfg
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
				if err := capture.PerformOCROverlay(img, cfg.OCRAddress, cfg.OCRLanguage, cfg.TranslationTarget, cfg.TranslationEngine, cfg.AutoTranslate, cfg.OutputDir); err != nil {
					fmt.Printf("OCR Overlay error: %v\n", err)
				}
			}()
		}
	}()

	go func() {
		for range windowClassGrabChan {
			go func() {
				if freshCfg, _, err := config.LoadConfig(); err == nil {
					cfg = freshCfg
				}
				fmt.Println("Launching interactive window class grab...")

				wClass, err := capture.InteractiveSelectWindowClass(":0.0")
				if err != nil {
					fmt.Printf("Error grabbing window class: %v\n", err)
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
