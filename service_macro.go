package main

import (
	"fmt"
	"time"

	"zen-cap/pkg/macro"
)

// runMacroRecordLoop toggles the X11 macro recorder on each hotkey press. The
// first press starts a recording session; the second stops it, compiles the
// captured events into an automation script and renders the configured output
// chain.
func (s *serviceState) runMacroRecordLoop(ch <-chan struct{}) {
	for range ch {
		s.macroMu.Lock()
		if s.macroRec != nil {
			rec := s.macroRec
			s.macroRec = nil
			s.macroRecording = false
			s.macroMu.Unlock()
			s.finishMacro(rec)
			continue
		}

		rec, err := macro.NewRecorder(macro.DefaultCompileOptions().MaxEvents)
		if err != nil {
			s.macroMu.Unlock()
			fmt.Printf("[Macro] Recorder unavailable: %v\n", err)
			sendNotification("Zen-Cap Macro", "Recorder unavailable (RECORD extension?)")
			continue
		}
		if err := rec.Start(); err != nil {
			s.macroMu.Unlock()
			fmt.Printf("[Macro] Failed to start recording: %v\n", err)
			sendNotification("Zen-Cap Macro", "Failed to start recording")
			continue
		}
		s.macroRec = rec
		s.macroRecording = true
		s.macroMu.Unlock()

		fmt.Println("[Macro] Recording started — press the macro hotkey again to stop and save.")
		sendNotification("Zen-Cap Macro", "Recording started")
	}
}

// finishMacro stops the recorder, compiles the captured events and renders the
// task-profile-driven output chain (save to file and/or copy to clipboard).
func (s *serviceState) finishMacro(rec *macro.Recorder) {
	events := rec.Stop()
	if len(events) == 0 {
		fmt.Println("[Macro] Nothing was recorded.")
		sendNotification("Zen-Cap Macro", "Stopped — nothing recorded")
		return
	}

	name := "macro-" + time.Now().Format("20060102-150405")
	script, err := macro.Compile(name, events, macro.DefaultCompileOptions())
	if err != nil {
		fmt.Printf("[Macro] Failed to compile macro: %v\n", err)
		sendNotification("Zen-Cap Macro", "Failed to compile macro")
		return
	}
	if len(script.Steps) == 0 {
		fmt.Println("[Macro] Nothing was recorded.")
		sendNotification("Zen-Cap Macro", "Stopped — nothing recorded")
		return
	}

	cfg := s.getCfg()
	chain := macro.OutputChain(cfg)
	results, err := macro.Render(chain, script, cfg.AutomationDir)
	if err != nil {
		fmt.Printf("[Macro] Output failed: %v\n", err)
		sendNotification("Zen-Cap Macro", fmt.Sprintf("Macro output failed: %v", err))
		return
	}

	summary := "Macro recorded"
	if len(results) > 0 {
		summary = fmt.Sprintf("Macro recorded: %d steps — %s", len(script.Steps), results[0])
	}
	for _, r := range results {
		fmt.Printf("[Macro] %s\n", r)
	}
	sendNotification("Zen-Cap Macro", summary)
}
