// [VERIFIED]
package main

import (
	"fmt"

	"zen-cap/pkg/config"
)

func printHotkeyBanner(cfg *config.Config) {
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
	fmt.Printf("  %-14s -> Annotate Overlay (Fullscreen freeze-frame)\n", cfg.Hotkeys.RecordAnnotate)
	fmt.Printf("  %-14s -> Mark Fullscreen for Recording\n", cfg.Hotkeys.RecordMarkFullscreen)
	fmt.Printf("  %-14s -> Mark Region for Recording\n", cfg.Hotkeys.RecordMarkRegion)
	fmt.Printf("  %-14s -> Mark Window for Recording\n", cfg.Hotkeys.RecordMarkWindow)
	fmt.Printf("  %-14s -> Show/Hide Recording Area Overlay\n", cfg.Hotkeys.RecordShowArea)
	fmt.Printf("  %-14s -> Toggle Audio-Only Recording Mode\n", cfg.Hotkeys.RecordAudioOnly)
	fmt.Printf("  %-14s -> Clipboard Manager: Copy (0-9)\n", cfg.Hotkeys.ClipboardCopyMod+"-[0-9]")
	fmt.Printf("  %-14s -> Clipboard Manager: Paste (0-9)\n", cfg.Hotkeys.ClipboardPasteMod+"-[0-9]")
	fmt.Printf("  %-14s -> Clipboard Manager: Cycle Transform Rules\n", cfg.Hotkeys.ClipboardCycleRule)
	fmt.Printf("  %-14s -> OCR Manager: Cycle OCR Model/Language\n", cfg.Hotkeys.OcrCycleModel)
	fmt.Printf("  %-14s -> Snippet Picker: Open GUI\n", cfg.Hotkeys.SnippetPicker)
	fmt.Printf("  %-14s -> Snippet Editor: Open snippets.yaml\n", "Shift-"+cfg.Hotkeys.SnippetPicker)
	fmt.Printf("  %-14s -> Snippet Mode: Cycle (Paste vs Human Typing)\n", cfg.Hotkeys.SnippetCycleMode)
	fmt.Printf("  %-14s -> Automation Picker: Open GUI\n", cfg.Hotkeys.AutomationPicker)
	fmt.Printf("  %-14s -> Automation Editor: Open automations.yaml\n", "Shift-"+cfg.Hotkeys.AutomationPicker)
	fmt.Println("UNIX Signals:")
	fmt.Println("  SIGUSR1       -> Fullscreen Screenshot")
	fmt.Println("  SIGUSR2       -> Toggle Fullscreen Recording")
	fmt.Println("Safety Net:")
	fmt.Println("  Ctrl+Shift+X  -> Instantly kill zen-cap service (emergency fallback)")
	fmt.Println("Press Ctrl+C in terminal to exit service.")
}
