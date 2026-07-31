package main

import (
	"fmt"
	"os"
	"syscall"

	"zen-cap/pkg/config"
)

func (s *serviceState) runSignalHandler(sigChan chan os.Signal, ch *serviceChannels) {
	for sig := range sigChan {
		switch sig {
		case os.Interrupt, syscall.SIGTERM:
			fmt.Println("\nShutting down service...")
			s.recMu.Lock()
			if s.activeRec != nil {
				fmt.Println("Stopping active recording before exit...")
				s.activeRec.Stop()
			}
			s.recMu.Unlock()
			os.Exit(0)
		case syscall.SIGUSR1:
			fmt.Println("Received SIGUSR1: Triggering screenshot...")
			select {
			case ch.Screenshot <- struct{}{}:
			default:
			}
		case syscall.SIGUSR2:
			fmt.Println("Received SIGUSR2: Triggering recording toggle...")
			select {
			case ch.Record <- struct{}{}:
			default:
			}
		}
	}
}

func (s *serviceState) runSnippetCycleModeLoop(ch <-chan struct{}) {
	for range ch {
		go func() {
			freshCfg, cfgPath, err := config.LoadConfig()
			if err != nil {
				fmt.Printf("[Snippet Mode Cycle] Error loading config: %v\n", err)
				return
			}

			newMode := "type"
			if freshCfg.SnippetMode == "type" {
				newMode = "paste"
			}
			freshCfg.SnippetMode = newMode
			s.setCfg(freshCfg)

			if cfgPath != "" {
				if err := config.SaveConfig(freshCfg, cfgPath); err != nil {
					fmt.Printf("[Snippet Mode Cycle] Error saving config: %v\n", err)
				} else {
					fmt.Printf("[Snippet Mode Cycle] Updated config.json: snippet_mode = %s\n", newMode)
				}
			}

			modeLabel := "Normal Paste"
			if newMode == "type" {
				modeLabel = "Human Typing"
			}
			sendNotification("Zen-Cap Snippets", fmt.Sprintf("Cycled snippet mode to: %s", modeLabel))
		}()
	}
}

func (s *serviceState) runTaskProfileCycleLoop(ch <-chan struct{}) {
	for range ch {
		go func() {
			freshCfg, cfgPath, err := config.LoadConfig()
			if err != nil {
				fmt.Printf("[Profile Cycle] Error loading config: %v\n", err)
				return
			}

			if len(freshCfg.TaskProfiles) == 0 {
				fmt.Println("[Profile Cycle] No task profiles defined in config")
				return
			}

			currentIndex := -1
			for i, p := range freshCfg.TaskProfiles {
				if p.Name == freshCfg.CurrentTaskProfile {
					currentIndex = i
					break
				}
			}

			nextIndex := (currentIndex + 1) % len(freshCfg.TaskProfiles)
			nextProfile := freshCfg.TaskProfiles[nextIndex]
			freshCfg.CurrentTaskProfile = nextProfile.Name
			s.setCfg(freshCfg)

			if cfgPath != "" {
				if err := config.SaveConfig(freshCfg, cfgPath); err != nil {
					fmt.Printf("[Profile Cycle] Error saving config: %v\n", err)
				} else {
					fmt.Printf("[Profile Cycle] Updated config.json: current_task_profile = %s\n", nextProfile.Name)
				}
			}

			sendNotification("Zen-Cap Profile", fmt.Sprintf("Cycled task profile to: %s", nextProfile.Name))
		}()
	}
}
