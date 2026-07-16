// [VERIFIED]
package main

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"syscall"

	"github.com/jezek/xgb/xproto"
	"github.com/jezek/xgbutil"
	"github.com/jezek/xgbutil/keybind"
	"github.com/jezek/xgbutil/xevent"

	"zen-cap/pkg/clipboard"
	"zen-cap/pkg/config"
	"zen-cap/pkg/hotkey"
	"zen-cap/pkg/magnifier"
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

	printHotkeyBanner(cfg)

	ch := newServiceChannels()

	// Initialize X11 connection for global hotkeys
	X, err := xgbutil.NewConn()
	if err != nil {
		return fmt.Errorf("failed to connect to X server: %w", err)
	}
	keybind.Initialize(X)

	cm := hotkey.NewChordManager(X)

	// Register Screenshot Hotkey
	cm.Register(cfg.Hotkeys.Screenshot, func() {
		fmt.Println("Hotkey pressed: Triggering screenshot...")
		select {
		case ch.Screenshot <- struct{}{}:
		default:
		}
	})

	// Register Region Screenshot Hotkey
	cm.Register(cfg.Hotkeys.RegionScreenshot, func() {
		fmt.Println("Hotkey pressed: Triggering interactive region screenshot...")
		select {
		case ch.RegionScreenshot <- struct{}{}:
		default:
		}
	})

	// Register Window Screenshot Hotkey
	cm.Register(cfg.Hotkeys.WindowScreenshot, func() {
		fmt.Println("Hotkey pressed: Triggering interactive window screenshot...")
		select {
		case ch.WindowScreenshot <- struct{}{}:
		default:
		}
	})

	// Register Fullscreen OCR Hotkey
	cm.Register(cfg.Hotkeys.OCRScreenshot, func() {
		fmt.Println("Hotkey pressed: Triggering fullscreen OCR/Translation overlay...")
		select {
		case ch.OCRScreenshot <- struct{}{}:
		default:
		}
	})

	// Register Region OCR Hotkey
	cm.Register(cfg.Hotkeys.OCRRegionScreenshot, func() {
		fmt.Println("Hotkey pressed: Triggering region OCR/Translation overlay...")
		select {
		case ch.OCRRegionScreenshot <- struct{}{}:
		default:
		}
	})

	// Register Window OCR Hotkey
	cm.Register(cfg.Hotkeys.OCRWindowScreenshot, func() {
		fmt.Println("Hotkey pressed: Triggering window OCR/Translation overlay...")
		select {
		case ch.OCRWindowScreenshot <- struct{}{}:
		default:
		}
	})

	// Register OCR Model Cycle Hotkey
	cm.Register(cfg.Hotkeys.OcrCycleModel, func() {
		fmt.Println("Hotkey pressed: Triggering OCR model cycle...")
		select {
		case ch.OCRCycleModel <- struct{}{}:
		default:
		}
	})

	// Register Window Class Grab Hotkey
	cm.Register(cfg.Hotkeys.WindowClassGrab, func() {
		fmt.Println("Hotkey pressed: Triggering interactive window class grab...")
		select {
		case ch.WindowClassGrab <- struct{}{}:
		default:
		}
	})

	// Register Color Picker Hotkey
	cm.Register(cfg.Hotkeys.ColorPicker, func() {
		fmt.Println("Hotkey pressed: Triggering interactive color picker...")
		select {
		case ch.ColorPicker <- struct{}{}:
		default:
		}
	})

	// Register Recording Hotkey
	cm.Register(cfg.Hotkeys.RecordToggle, func() {
		fmt.Println("Hotkey pressed: Triggering recording toggle...")
		select {
		case ch.Record <- struct{}{}:
		default:
		}
	})

	// Register Hotkey for on-demand annotation overlay
	cm.Register(cfg.Hotkeys.RecordAnnotate, func() {
		fmt.Println("Hotkey pressed: Triggering record annotation...")
		select {
		case ch.RecordAnnotate <- struct{}{}:
		default:
		}
	})

	// Register Record Mark Fullscreen Hotkey
	cm.Register(cfg.Hotkeys.RecordMarkFullscreen, func() {
		fmt.Println("Hotkey pressed: Triggering record mark fullscreen...")
		select {
		case ch.RecordMarkFullscreen <- struct{}{}:
		default:
		}
	})

	// Register Record Audio Only Hotkey
	cm.Register(cfg.Hotkeys.RecordAudioOnly, func() {
		fmt.Println("Hotkey pressed: Triggering record audio only toggle...")
		select {
		case ch.RecordAudioOnly <- struct{}{}:
		default:
		}
	})

	// Register Record Mark Region Hotkey
	cm.Register(cfg.Hotkeys.RecordMarkRegion, func() {
		fmt.Println("Hotkey pressed: Triggering record mark region...")
		select {
		case ch.RecordMarkRegion <- struct{}{}:
		default:
		}
	})

	// Register Record Mark Window Hotkey
	cm.Register(cfg.Hotkeys.RecordMarkWindow, func() {
		fmt.Println("Hotkey pressed: Triggering record mark window...")
		select {
		case ch.RecordMarkWindow <- struct{}{}:
		default:
		}
	})

	// Register Record Show Area Hotkey
	cm.Register(cfg.Hotkeys.RecordShowArea, func() {
		fmt.Println("Hotkey pressed: Triggering record show/hide area...")
		select {
		case ch.RecordShowArea <- struct{}{}:
		default:
		}
	})

	// Initialize Clipboard Manager
	mgr, err := clipboard.NewManager(cfg)
	if err != nil {
		log.Printf("Warning: Failed to initialize clipboard manager: %v", err)
	}

	if mgr != nil {
		// Register Copy Slots (0-9 and KP_0..KP_9)
		for i := 0; i <= 9; i++ {
			slot := i
			handler := func() {
				go mgr.CopyToSlot(slot)
			}
			// Keyboard row digits
			cm.Register(fmt.Sprintf("%s-%d", cfg.Hotkeys.ClipboardCopyMod, slot), handler)
			// Numpad digits
			cm.Register(fmt.Sprintf("%s-KP_%d", cfg.Hotkeys.ClipboardCopyMod, slot), handler)
		}

		// Register Paste Slots (0-9 and KP_0..KP_9)
		for i := 0; i <= 9; i++ {
			slot := i
			handler := func() {
				go mgr.PasteFromSlot(slot)
			}
			// Keyboard row digits
			cm.Register(fmt.Sprintf("%s-%d", cfg.Hotkeys.ClipboardPasteMod, slot), handler)
			// Numpad digits
			cm.Register(fmt.Sprintf("%s-KP_%d", cfg.Hotkeys.ClipboardPasteMod, slot), handler)
		}

		// Register Cycle Rule Hotkey
		cm.Register(cfg.Hotkeys.ClipboardCycleRule, func() {
			go mgr.CycleTransform()
		})
	}

	// Register Snippet Picker Hotkey
	cm.Register(cfg.Hotkeys.SnippetPicker, func() {
		exe, err := os.Executable()
		if err != nil {
			exe = "zen-cap"
		}
		// Save the currently focused window XID BEFORE the picker steals focus,
		// so the paste logic can restore and verify focus deterministically.
		var prevFocusEnv string
		if focusReply, ferr := xproto.GetInputFocus(X.Conn()).Reply(); ferr == nil && focusReply.Focus != 0 {
			prevFocusEnv = fmt.Sprintf("ZENCAP_PREV_FOCUS=%d", uint32(focusReply.Focus))
		}
		cmd := exec.Command(exe, "snippet-picker")
		cmd.Env = os.Environ()
		if prevFocusEnv != "" {
			cmd.Env = append(cmd.Env, prevFocusEnv)
		}
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Start(); err != nil {
			fmt.Printf("[Service] Failed to start snippet-picker: %v\n", err)
		}
	})

	// Register Snippet Editor Hotkey (Shift+Alt+`) for instant manual editing in default app
	cm.Register("Shift-"+cfg.Hotkeys.SnippetPicker, func() {
		fmt.Printf("[Service] Opening snippet file for editing: %s\n", cfg.SnippetFile)
		cmd := exec.Command("xdg-open", cfg.SnippetFile)
		if err := cmd.Start(); err != nil {
			fmt.Printf("[Service] Failed to open snippet file: %v\n", err)
		}
	})

	// Register Snippet Mode Cycle Hotkey
	cm.Register(cfg.Hotkeys.SnippetCycleMode, func() {
		fmt.Println("Hotkey pressed: Triggering snippet mode cycle...")
		select {
		case ch.SnippetCycleMode <- struct{}{}:
		default:
		}
	})

	// Register Task Profile Cycle Hotkey
	cm.Register(cfg.Hotkeys.CycleTaskProfile, func() {
		fmt.Println("Hotkey pressed: Triggering task profile cycle...")
		select {
		case ch.TaskProfileCycle <- struct{}{}:
		default:
		}
	})

	// Register Automation Picker Hotkey
	cm.Register(cfg.Hotkeys.AutomationPicker, func() {
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
	})

	// Register Automation Editor Hotkey (Shift+Alt+a) for instant manual editing in default app
	cm.Register("Shift-"+cfg.Hotkeys.AutomationPicker, func() {
		fmt.Printf("[Service] Opening automation directory: %s\n", cfg.AutomationDir)
		cmd := exec.Command("xdg-open", cfg.AutomationDir)
		if err := cmd.Start(); err != nil {
			fmt.Printf("[Service] Failed to open automation directory: %v\n", err)
		}
	})

	// Register Global Safety Kill Hotkey (Ctrl+Shift+X) - emergency exit
	safetyKillHandler := func() {
		fmt.Println("CRITICAL: Global safety kill hotkey pressed! Terminating zen-cap service immediately.")
		os.Exit(1)
	}
	cm.Register("Control-Shift-x", safetyKillHandler)
	cm.Register("Control-Shift-X", safetyKillHandler)

	cm.Start()

	s := &serviceState{
		cfg: cfg,
		X:   X,
		markedArea: MarkedArea{
			X:      -1,
			Y:      -1,
			Width:  -1,
			Height: -1,
			Type:   "fullscreen",
		},
	}

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM, syscall.SIGUSR1, syscall.SIGUSR2)
	go s.runSignalHandler(sigChan, ch)

	go s.runScreenshotLoop(ch.Screenshot)

	go s.runRegionScreenshotLoop(ch.RegionScreenshot)

	go s.runWindowScreenshotLoop(ch.WindowScreenshot)

	go s.runOCRScreenshotLoop(ch.OCRScreenshot)

	go s.runOCRRegionScreenshotLoop(ch.OCRRegionScreenshot)

	go s.runOCRWindowScreenshotLoop(ch.OCRWindowScreenshot)

	go s.runOCRCycleModelLoop(ch.OCRCycleModel)

	go s.runSnippetCycleModeLoop(ch.SnippetCycleMode)

	go s.runTaskProfileCycleLoop(ch.TaskProfileCycle)

	go s.runWindowClassGrabLoop(ch.WindowClassGrab)

	go s.runColorPickerLoop(ch.ColorPicker)

	go s.runRecordMarkFullscreenLoop(ch.RecordMarkFullscreen)

	// Background loop for ch.RecordAudioOnly
	go s.runRecordAudioOnlyLoop(ch.RecordAudioOnly)

	go s.runRecordMarkRegionLoop(ch.RecordMarkRegion)

	go s.runRecordMarkWindowLoop(ch.RecordMarkWindow)

	go s.runRecordShowAreaLoop(ch.RecordShowArea)

	// Background loop for ch.RecordAnnotate — standalone annotation like Color Picker
	go s.runRecordAnnotateLoop(ch.RecordAnnotate)

	go s.runRecordToggleLoop(ch.Record)

	xevent.Main(X)
	return nil
}
