// [VERIFIED]
package main

import (
	"fmt"
	"log"
	"os"

	"zen-cap/pkg/automation"
	"zen-cap/pkg/capture"
	"zen-cap/pkg/config"
	"zen-cap/pkg/snippet"
	"zen-cap/pkg/tui"
)

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
