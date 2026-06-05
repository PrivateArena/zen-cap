// [VERIFIED]
package main

import (
	"fmt"
	"image"
	"os/exec"
	"strconv"

	"zen-cap/pkg/capture"
	"zen-cap/pkg/config"
	"zen-cap/pkg/display"
)

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
		translated, err := capture.TranslateText(cfg.TranslationEngine, cfg.OCRAddress, text, cfg.TranslationTarget)
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
