package pipeline

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"zen-cap/pkg/browser_bridge"
	"zen-cap/pkg/config"
)

type VisionTask struct{}

func (VisionTask) Name() string { return "vision" }

func (VisionTask) Enabled(cfg *config.Config) bool { return cfg.Vision.Enabled }

func (VisionTask) Run(ctx context.Context, r *Result, cfg *config.Config) error {
	prompt := cfg.Vision.Prompt
	if prompt == "" {
		prompt = "Describe what is shown in this screenshot in 2-3 concise sentences."
	}

	provider := cfg.Vision.Provider
	if provider == "" {
		provider = "claude"
	}

	timeout := time.Duration(cfg.Vision.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	reqCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	fmt.Printf("[Vision] Requesting visual analysis from browser bridge using %s...\n", provider)
	text, err := browser_bridge.CallChat(reqCtx, "", provider, prompt, []string{r.OutputPath})
	if err != nil {
		return err
	}
	r.LLMText = text

	if cfg.Vision.SaveSidecar {
		sidecar := strings.TrimSuffix(r.OutputPath, ".png") + ".txt"
		if werr := os.WriteFile(sidecar, []byte(text), 0644); werr != nil {
			fmt.Printf("[Vision] failed to write sidecar %s: %v\n", sidecar, werr)
		} else {
			fmt.Printf("[Vision] Saved explanation to %s\n", sidecar)
		}
	}

	fmt.Printf("[Vision] %s\n", text)
	sendNotification("Zen-Cap Vision", truncate(text, 150))
	return nil
}
