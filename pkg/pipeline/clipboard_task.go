package pipeline

import (
	"context"
	"fmt"

	"zen-cap/pkg/capture"
	"zen-cap/pkg/config"
)

type ClipboardTask struct{}

func (ClipboardTask) Name() string { return "clipboard" }

func (ClipboardTask) Enabled(cfg *config.Config) bool {
	return cfg.ClipboardMode != "" && cfg.ClipboardMode != "none"
}

func (ClipboardTask) Run(ctx context.Context, r *Result, cfg *config.Config) error {
	action := cfg.ClipboardMode
	if r.ClipboardOverride != "" {
		action = r.ClipboardOverride // dynamic in-crop selection takes precedence
	}
	if action == "" || action == "none" {
		return nil
	}

	switch action {
	case "image":
		if err := capture.SpawnClipboardDaemon("--image", r.OutputPath); err != nil {
			return fmt.Errorf("clipboard image copy failed: %w", err)
		}
		fmt.Println("[Clipboard] Copied image to clipboard.")
		sendNotification("Zen-Cap", "Copied captured image to clipboard!")

	case "path":
		if err := capture.SpawnClipboardDaemon("--text", r.OutputPath); err != nil {
			return fmt.Errorf("clipboard path copy failed: %w", err)
		}
		fmt.Printf("[Clipboard] Copied path to clipboard: %s\n", r.OutputPath)
		sendNotification("Zen-Cap", "Copied image file path to clipboard!")

	case "ocr":
		fmt.Println("[OCR] Running OCR on captured region...")
		text, err := capture.PerformOCR(r.Image, cfg.OCRAddress, cfg.OCRLanguage)
		if err != nil {
			sendNotification("Zen-Cap OCR", fmt.Sprintf("OCR failed: %v", err))
			return fmt.Errorf("OCR failed: %w", err)
		}
		if text == "" {
			sendNotification("Zen-Cap OCR", "No text was detected in captured region.")
			return nil
		}
		if err := capture.SpawnClipboardDaemon("--text", text); err != nil {
			return fmt.Errorf("clipboard OCR text copy failed: %w", err)
		}
		fmt.Printf("[OCR] Copied extracted text to clipboard:\n%s\n", text)
		sendNotification("Zen-Cap OCR", fmt.Sprintf("Copied extracted text to clipboard (%d chars)!", len(text)))

	case "translate":
		fmt.Println("[OCR] Running OCR for translation...")
		text, err := capture.PerformOCR(r.Image, cfg.OCRAddress, cfg.OCRLanguage)
		if err != nil {
			sendNotification("Zen-Cap Translate", fmt.Sprintf("OCR failed: %v", err))
			return fmt.Errorf("OCR failed: %w", err)
		}
		if text == "" {
			sendNotification("Zen-Cap Translate", "No text was detected in captured region.")
			return nil
		}
		fmt.Printf("[Translate] Translating extracted text to %s...\n", cfg.TranslationTarget)
		translated, err := capture.TranslateText(cfg.TranslationEngine, cfg.OCRAddress, text, cfg.TranslationTarget)
		if err != nil {
			sendNotification("Zen-Cap Translate", fmt.Sprintf("Translation failed: %v", err))
			return fmt.Errorf("translation failed: %w", err)
		}
		if err := capture.SpawnClipboardDaemon("--text", translated); err != nil {
			return fmt.Errorf("clipboard translation copy failed: %w", err)
		}
		fmt.Printf("[Translate] Copied translated text to clipboard:\n%s\n", translated)
		sendNotification("Zen-Cap Translate", "Copied translated text to clipboard!")

	case "url":
		if r.UploadURL == "" {
			return fmt.Errorf("clipboard mode 'url' requested but no upload URL is available")
		}
		if err := capture.SpawnClipboardDaemon("--text", r.UploadURL); err != nil {
			return fmt.Errorf("clipboard URL copy failed: %w", err)
		}
		fmt.Printf("[Clipboard] Copied upload URL to clipboard: %s\n", r.UploadURL)
		sendNotification("Zen-Cap", "Copied upload URL to clipboard!")

	case "llm-text":
		if r.LLMText == "" {
			return fmt.Errorf("clipboard mode 'llm-text' requested but no vision result is available")
		}
		if err := capture.SpawnClipboardDaemon("--text", r.LLMText); err != nil {
			return fmt.Errorf("clipboard LLM text copy failed: %w", err)
		}
		fmt.Printf("[Clipboard] Copied LLM explanation to clipboard (%d chars).\n", len(r.LLMText))
		sendNotification("Zen-Cap", "Copied LLM explanation to clipboard!")
	}
	return nil
}
