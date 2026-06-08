// [VERIFIED]
package main

import (
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/jezek/xgb/xproto"
	"github.com/jezek/xgbutil"
	"github.com/jezek/xgbutil/keybind"
	"github.com/jezek/xgbutil/xevent"

	"zen-cap/pkg/capture"
	"zen-cap/pkg/clipboard"
	"zen-cap/pkg/config"
	"zen-cap/pkg/magnifier"
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

	// Start local HTTP API server
	go startAPIServer(cfg)

	// Ensure OutputDir exists
	if err := os.MkdirAll(cfg.OutputDir, 0755); err != nil {
		log.Printf("Warning: Failed to create output directory %q: %v", cfg.OutputDir, err)
	} else {
		fmt.Printf("Outputs will be saved to: %s\n", cfg.OutputDir)
	}

	// Start the system magnifier service if enabled in config.
	var magService *magnifier.Service
	if cfg.Magnifier.Enabled {
		magCfg := magnifier.Config{
			Display:          cfg.Magnifier.Display,
			FullscreenHotkey: cfg.Magnifier.FullscreenHotkey,
			LensHotkey:       cfg.Magnifier.LensHotkey,
			ScrollModifier:   cfg.Magnifier.ScrollModifier,
			ZoomMin:          cfg.Magnifier.ZoomMin,
			ZoomMax:          cfg.Magnifier.ZoomMax,
			ZoomStep:         cfg.Magnifier.ZoomStep,
			InitialZoom:      cfg.Magnifier.InitialZoom,
			LensSize:         cfg.Magnifier.LensSize,
			LensShapeStr:     cfg.Magnifier.LensShape,
			SmoothScaling:    cfg.Magnifier.SmoothScaling,
			Enabled:          true,
		}
		magCfg.Normalize()
		magService = magnifier.NewService(magCfg)
		go func() {
			if err := magService.Start(); err != nil {
				log.Printf("[Magnifier] Service exited: %v", err)
			}
		}()
		defer magService.Stop()
		fmt.Printf("  %-14s -> Fullscreen Magnifier (toggle)\n", cfg.Magnifier.FullscreenHotkey)
		fmt.Printf("  %-14s -> Lens Magnifier (toggle)\n", cfg.Magnifier.LensHotkey)
		fmt.Printf("  %-14s -> Zoom In/Out (while magnifier active)\n", cfg.Magnifier.ScrollModifier+"+Wheel")
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
	fmt.Printf("  %-14s -> Color Picker (Grab Pixels to Clipboard)\n", cfg.Hotkeys.ColorPicker)
	fmt.Printf("  %-14s -> Toggle Recording (Start/Stop)\n", cfg.Hotkeys.RecordToggle)
	fmt.Printf("  %-14s -> Mark Fullscreen for Recording\n", cfg.Hotkeys.RecordMarkFullscreen)
	fmt.Printf("  %-14s -> Mark Region for Recording\n", cfg.Hotkeys.RecordMarkRegion)
	fmt.Printf("  %-14s -> Mark Window for Recording\n", cfg.Hotkeys.RecordMarkWindow)
	fmt.Printf("  %-14s -> Show/Hide Recording Area Overlay\n", cfg.Hotkeys.RecordShowArea)
	fmt.Printf("  %-14s -> Clipboard Manager: Copy (0-9)\n", cfg.Hotkeys.ClipboardCopyMod+"-[0-9]")
	fmt.Printf("  %-14s -> Clipboard Manager: Paste (0-9)\n", cfg.Hotkeys.ClipboardPasteMod+"-[0-9]")
	fmt.Printf("  %-14s -> Clipboard Manager: Cycle Transform Rules\n", cfg.Hotkeys.ClipboardCycleRule)
	fmt.Printf("  %-14s -> OCR Manager: Cycle OCR Model/Language\n", cfg.Hotkeys.OcrCycleModel)
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
	ocrCycleModelChan := make(chan struct{}, 1)
	windowClassGrabChan := make(chan struct{}, 1)
	colorPickerChan := make(chan struct{}, 1)
	recordChan := make(chan struct{}, 1)
	recordMarkFullscreenChan := make(chan struct{}, 1)
	recordMarkRegionChan := make(chan struct{}, 1)
	recordMarkWindowChan := make(chan struct{}, 1)
	recordShowAreaChan := make(chan struct{}, 1)

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

	// Register OCR Model Cycle Hotkey
	keybind.KeyPressFun(func(xu *xgbutil.XUtil, ev xevent.KeyPressEvent) {
		fmt.Println("Hotkey pressed: Triggering OCR model cycle...")
		select {
		case ocrCycleModelChan <- struct{}{}:
		default:
		}
	}).Connect(X, X.RootWin(), cfg.Hotkeys.OcrCycleModel, true)

	// Register Window Class Grab Hotkey
	keybind.KeyPressFun(func(xu *xgbutil.XUtil, ev xevent.KeyPressEvent) {
		fmt.Println("Hotkey pressed: Triggering interactive window class grab...")
		select {
		case windowClassGrabChan <- struct{}{}:
		default:
		}
	}).Connect(X, X.RootWin(), cfg.Hotkeys.WindowClassGrab, true)

	// Register Color Picker Hotkey
	keybind.KeyPressFun(func(xu *xgbutil.XUtil, ev xevent.KeyPressEvent) {
		fmt.Println("Hotkey pressed: Triggering interactive color picker...")
		select {
		case colorPickerChan <- struct{}{}:
		default:
		}
	}).Connect(X, X.RootWin(), cfg.Hotkeys.ColorPicker, true)

	// Register Recording Hotkey
	keybind.KeyPressFun(func(xu *xgbutil.XUtil, ev xevent.KeyPressEvent) {
		fmt.Println("Hotkey pressed: Triggering recording toggle...")
		select {
		case recordChan <- struct{}{}:
		default:
		}
	}).Connect(X, X.RootWin(), cfg.Hotkeys.RecordToggle, true)

	// Register Record Mark Fullscreen Hotkey
	keybind.KeyPressFun(func(xu *xgbutil.XUtil, ev xevent.KeyPressEvent) {
		fmt.Println("Hotkey pressed: Triggering record mark fullscreen...")
		select {
		case recordMarkFullscreenChan <- struct{}{}:
		default:
		}
	}).Connect(X, X.RootWin(), cfg.Hotkeys.RecordMarkFullscreen, true)

	// Register Record Mark Region Hotkey
	keybind.KeyPressFun(func(xu *xgbutil.XUtil, ev xevent.KeyPressEvent) {
		fmt.Println("Hotkey pressed: Triggering record mark region...")
		select {
		case recordMarkRegionChan <- struct{}{}:
		default:
		}
	}).Connect(X, X.RootWin(), cfg.Hotkeys.RecordMarkRegion, true)

	// Register Record Mark Window Hotkey
	keybind.KeyPressFun(func(xu *xgbutil.XUtil, ev xevent.KeyPressEvent) {
		fmt.Println("Hotkey pressed: Triggering record mark window...")
		select {
		case recordMarkWindowChan <- struct{}{}:
		default:
		}
	}).Connect(X, X.RootWin(), cfg.Hotkeys.RecordMarkWindow, true)

	// Register Record Show Area Hotkey
	keybind.KeyPressFun(func(xu *xgbutil.XUtil, ev xevent.KeyPressEvent) {
		fmt.Println("Hotkey pressed: Triggering record show/hide area...")
		select {
		case recordShowAreaChan <- struct{}{}:
		default:
		}
	}).Connect(X, X.RootWin(), cfg.Hotkeys.RecordShowArea, true)

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

	type MarkedArea struct {
		X        int
		Y        int
		Width    int
		Height   int
		WindowID uint32
		Type     string // "fullscreen", "region", "window"
	}

	markedArea := MarkedArea{
		X:      -1,
		Y:      -1,
		Width:  -1,
		Height: -1,
		Type:   "fullscreen",
	}
	var markedAreaMu sync.Mutex

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
		for range ocrCycleModelChan {
			go func() {
				// 1. Load latest config
				freshCfg, cfgPath, err := config.LoadConfig()
				if err != nil {
					fmt.Printf("[OCR Cycle] Error loading config: %v\n", err)
					return
				}
				cfg = freshCfg

				if len(cfg.OCRLanguages) == 0 {
					fmt.Println("[OCR Cycle] No OCR models/languages defined in config")
					return
				}

				// 2. Find index of current ocr_language
				currentIndex := -1
				for i, lang := range cfg.OCRLanguages {
					if lang == cfg.OCRLanguage {
						currentIndex = i
						break
					}
				}

				// 3. Cycle to next
				nextIndex := (currentIndex + 1) % len(cfg.OCRLanguages)
				nextLang := cfg.OCRLanguages[nextIndex]
				cfg.OCRLanguage = nextLang

				// 4. Save config
				if cfgPath != "" {
					if err := config.SaveConfig(cfg, cfgPath); err != nil {
						fmt.Printf("[OCR Cycle] Error saving config: %v\n", err)
					} else {
						fmt.Printf("[OCR Cycle] Updated config.json: ocr_language = %s\n", nextLang)
					}
				}

				// 5. Notify the OCR server
				resolvedAddress, err := capture.EnsureOCRServer(cfg.OCRAddress)
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

				// 6. Send desktop notification
				sendNotification("Zen-Cap OCR", fmt.Sprintf("Cycled OCR model to: %s", nextLang))
			}()
		}
	}()

	var windowClassGrabMu sync.Mutex
	var windowClassGrabRunning bool

	go func() {
		for range windowClassGrabChan {
			windowClassGrabMu.Lock()
			if windowClassGrabRunning {
				windowClassGrabMu.Unlock()
				continue
			}
			windowClassGrabRunning = true
			windowClassGrabMu.Unlock()

			go func() {
				defer func() {
					windowClassGrabMu.Lock()
					windowClassGrabRunning = false
					windowClassGrabMu.Unlock()
				}()

				if freshCfg, _, err := config.LoadConfig(); err == nil {
					cfg = freshCfg
				}
				fmt.Println("Launching interactive window class grab...")

				wClass, err := capture.InteractiveSelectWindowClass(":0.0")
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
	}()

	var colorPickerMu sync.Mutex
	var colorPickerRunning bool

	go func() {
		for range colorPickerChan {
			colorPickerMu.Lock()
			if colorPickerRunning {
				colorPickerMu.Unlock()
				continue
			}
			colorPickerRunning = true
			colorPickerMu.Unlock()

			go func() {
				defer func() {
					colorPickerMu.Lock()
					colorPickerRunning = false
					colorPickerMu.Unlock()
				}()

				if freshCfg, _, err := config.LoadConfig(); err == nil {
					cfg = freshCfg
				}
				fmt.Println("Launching interactive color picker...")

				// Capture base fullscreen screen first
				capCfg := capture.CaptureConfig{
					Display:     ":0.0",
					X:           -1,
					Y:           -1,
					Interactive: false,
				}
				img, err := capture.CaptureScreen(capCfg)
				if err != nil {
					fmt.Printf("Error capturing fullscreen for color picker: %v\n", err)
					return
				}

				colorsText, err := capture.InteractiveColorPicker(img, cfg.ColorPickerFormat)
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
	}()

	var activeBorders []xproto.Window
	var activeBordersMu sync.Mutex

	// Background loop for recordMarkFullscreenChan
	go func() {
		for range recordMarkFullscreenChan {
			markedAreaMu.Lock()
			markedArea = MarkedArea{
				X:      -1,
				Y:      -1,
				Width:  -1,
				Height: -1,
				Type:   "fullscreen",
			}
			markedAreaMu.Unlock()
			fmt.Println("[Recorder] Marked fullscreen area for recording")
			sendNotification("Zen-Cap", "Marked fullscreen for video recording!")
		}
	}()

	// Background loop for recordMarkRegionChan
	go func() {
		for range recordMarkRegionChan {
			go func() {
				if freshCfg, _, err := config.LoadConfig(); err == nil {
					cfg = freshCfg
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
				
				// Ensure even dimensions for h264 compatibility
				if chosenW % 2 != 0 {
					chosenW--
				}
				if chosenH % 2 != 0 {
					chosenH--
				}

				markedAreaMu.Lock()
				markedArea = MarkedArea{
					X:      chosenX,
					Y:      chosenY,
					Width:  chosenW,
					Height: chosenH,
					Type:   "region",
				}
				markedAreaMu.Unlock()

				msg := fmt.Sprintf("Marked region %dx%d at (%d, %d) for recording!", chosenW, chosenH, chosenX, chosenY)
				fmt.Printf("[Recorder] %s\n", msg)
				sendNotification("Zen-Cap", msg)
			}()
		}
	}()

	// Background loop for recordMarkWindowChan
	go func() {
		for range recordMarkWindowChan {
			go func() {
				if freshCfg, _, err := config.LoadConfig(); err == nil {
					cfg = freshCfg
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

				// Ensure even dimensions
				if chosenW % 2 != 0 {
					chosenW--
				}
				if chosenH % 2 != 0 {
					chosenH--
				}

				markedAreaMu.Lock()
				markedArea = MarkedArea{
					X:        chosenX,
					Y:        chosenY,
					Width:    chosenW,
					Height:   chosenH,
					WindowID: chosenWinID,
					Type:     "window",
				}
				markedAreaMu.Unlock()

				msg := fmt.Sprintf("Marked window (ID 0x%x) %dx%d at (%d, %d) for recording!", chosenWinID, chosenW, chosenH, chosenX, chosenY)
				fmt.Printf("[Recorder] %s\n", msg)
				sendNotification("Zen-Cap", msg)
			}()
		}
	}()

	// Background loop for recordShowAreaChan
	go func() {
		for range recordShowAreaChan {
			activeBordersMu.Lock()
			if len(activeBorders) > 0 {
				// Destroy existing borders
				for _, w := range activeBorders {
					xproto.DestroyWindow(X.Conn(), w)
				}
				activeBorders = nil
				activeBordersMu.Unlock()
				fmt.Println("[Recorder] Cleared recording area highlight overlay")
				continue
			}

			// Get current marked area
			markedAreaMu.Lock()
			area := markedArea
			markedAreaMu.Unlock()

			// Resolve actual coordinates
			screen := X.Screen()
			screenW := int(screen.WidthInPixels)
			screenH := int(screen.HeightInPixels)

			x, y, w, h := area.X, area.Y, area.Width, area.Height
			if area.Type == "fullscreen" || x < 0 || y < 0 || w <= 0 || h <= 0 {
				x, y, w, h = 0, 0, screenW, screenH
			}

			fmt.Printf("[Recorder] Highlighting recording area: %dx%d at (%d, %d)\n", w, h, x, y)

			// Create 4 thin border windows around the area
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

				winID, err := xproto.NewWindowId(X.Conn())
				if err != nil {
					success = false
					break
				}

				var backPixel uint32 = 0x00F0FF // Neon cyan
				var overrideRedirect uint32 = 1

				err = xproto.CreateWindowChecked(
					X.Conn(),
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

				err = xproto.MapWindowChecked(X.Conn(), winID).Check()
				if err != nil {
					success = false
					break
				}

				created = append(created, winID)
			}

			if !success {
				// Rollback
				for _, w := range created {
					xproto.DestroyWindow(X.Conn(), w)
				}
				activeBorders = nil
				fmt.Println("[Recorder] Failed to create highlight overlay windows")
			} else {
				activeBorders = created
				fmt.Println("[Recorder] Recording area highlight overlay mapped")
			}
			activeBordersMu.Unlock()
		}
	}()

	go func() {
		for range recordChan {
			recMu.Lock()
			if activeRec == nil {
				if freshCfg, _, err := config.LoadConfig(); err == nil {
					cfg = freshCfg
				}
				timestamp := time.Now().Format("20060102_150405")
				filename := filepath.Join(cfg.OutputDir, fmt.Sprintf("recording_%s.mp4", timestamp))

				markedAreaMu.Lock()
				area := markedArea
				markedAreaMu.Unlock()

				var recordingMsg string
				recCfg := recorder.RecorderConfig{
					Display:    ":0.0",
					FPS:        30,
					OutputPath: filename,
					Bitrate:    4000000,
				}

				if area.Type == "fullscreen" || area.X < 0 || area.Y < 0 || area.Width <= 0 || area.Height <= 0 {
					recCfg.X = -1
					recCfg.Y = -1
					recCfg.Width = -1
					recCfg.Height = -1
					recordingMsg = fmt.Sprintf("[%s] Starting fullscreen recording to %s...", time.Now().Format("15:04:05"), filename)
				} else {
					recCfg.X = area.X
					recCfg.Y = area.Y
					recCfg.Width = area.Width
					recCfg.Height = area.Height
					recCfg.WindowID = area.WindowID
					if area.Type == "window" && area.WindowID != 0 {
						recordingMsg = fmt.Sprintf("[%s] Starting window recording (ID: 0x%x, %dx%d at %d,%d) to %s...", time.Now().Format("15:04:05"), area.WindowID, area.Width, area.Height, area.X, area.Y, filename)
					} else {
						recordingMsg = fmt.Sprintf("[%s] Starting region recording (%dx%d at %d,%d) to %s...", time.Now().Format("15:04:05"), area.Width, area.Height, area.X, area.Y, filename)
					}
				}

				// Hide preview overlay if visible
				activeBordersMu.Lock()
				if len(activeBorders) > 0 {
					for _, w := range activeBorders {
						xproto.DestroyWindow(X.Conn(), w)
					}
					activeBorders = nil
					fmt.Println("[Recorder] Hidden preview overlay before starting recording")
				}
				activeBordersMu.Unlock()

				fmt.Println(recordingMsg)

				// Ensure folder exists (e.g. if deleted mid-run)
				_ = os.MkdirAll(cfg.OutputDir, 0755)

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
