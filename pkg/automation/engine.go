package automation

import (
	"fmt"
	"image"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jezek/xgb/xproto"
	"github.com/jezek/xgbutil"
	"github.com/jezek/xgbutil/ewmh"
	"github.com/jezek/xgbutil/icccm"
	"github.com/jezek/xgbutil/keybind"

	"zen-cap/pkg/capture"
	"zen-cap/pkg/config"
)

// RunScript starts the sequential execution of a Script.
func RunScript(script Script, cfg *config.Config, scriptDir string, abortChan chan struct{}, logger func(string, ...interface{})) error {
	xu, err := xgbutil.NewConn()
	if err != nil {
		return fmt.Errorf("failed to open X connection: %w", err)
	}
	defer xu.Conn().Close()
	keybind.Initialize(xu)

	var WID uint32
	if script.Window != nil {
		id, err := ResolveWindow(xu, script.Window)
		if err != nil {
			logger("[Automation] Warning: %v. Running script in absolute screen space instead.", err)
		} else {
			WID = id
			logger("[Automation] Target window resolved: WID=%d", WID)
		}
	}

	ctx := &ExecContext{
		X:         xu,
		WindowID:  WID,
		AbortChan: abortChan,
		Logger:    logger,
		ScriptDir: scriptDir,
		Config:    cfg,
	}

	logger("[Automation] Starting script: %q", script.Name)
	for i, step := range script.Steps {
		select {
		case <-ctx.AbortChan:
			return fmt.Errorf("execution aborted by user")
		default:
		}

		logger("[Automation] Step %d/%d: %s", i+1, len(script.Steps), step.Action)
		if err := executeStepWithControl(step, ctx); err != nil {
			return fmt.Errorf("step %d (%s) failed: %w", i+1, step.Action, err)
		}
	}

	logger("[Automation] Script %q finished successfully!", script.Name)
	return nil
}

// ResolveWindow finds a matching window ID using native X11 window listing.
func ResolveWindow(xu *xgbutil.XUtil, target *WindowTarget) (uint32, error) {
	if target == nil {
		return 0, nil
	}
	clientIDs, err := ewmh.ClientListGet(xu)
	if err != nil {
		tree, err := xproto.QueryTree(xu.Conn(), xu.RootWin()).Reply()
		if err != nil {
			return 0, fmt.Errorf("failed to query window tree: %w", err)
		}
		clientIDs = tree.Children
	}

	for _, winID := range clientIDs {
		if target.Title != "" {
			title, err := ewmh.WmNameGet(xu, winID)
			if err != nil || title == "" {
				title, err = icccm.WmNameGet(xu, winID)
			}
			if err == nil && strings.Contains(strings.ToLower(title), strings.ToLower(target.Title)) {
				return uint32(winID), nil
			}
		} else if target.Class != "" {
			classInfo, err := icccm.WmClassGet(xu, winID)
			if err == nil && classInfo != nil {
				if strings.Contains(strings.ToLower(classInfo.Class), strings.ToLower(target.Class)) ||
					strings.Contains(strings.ToLower(classInfo.Instance), strings.ToLower(target.Class)) {
					return uint32(winID), nil
				}
			}
		}
	}

	return 0, fmt.Errorf("no window matching target found")
}

func executeStepWithControl(step Step, ctx *ExecContext) error {
	switch strings.ToLower(step.Action) {
	case "loop":
		count := step.Count
		if count <= 0 {
			count = 1
		}
		ctx.Logger("[Automation] Loop: entering count=%d", count)
		for i := 0; i < count; i++ {
			select {
			case <-ctx.AbortChan:
				return fmt.Errorf("execution aborted by user")
			default:
			}
			ctx.Logger("[Automation] Loop iteration %d/%d", i+1, count)
			for _, substep := range step.Steps {
				if err := executeStepWithControl(substep, ctx); err != nil {
					return err
				}
			}
		}
		return nil
	case "if_found":
		found := false
		findType := step.Find
		if findType == "" {
			findType = step.Type
		}
		findType = strings.ToLower(findType)

		targetVal := step.Image
		if targetVal == "" {
			targetVal = step.Target
		}
		if targetVal == "" {
			targetVal = step.Text
		}

		var needle image.Image
		if findType == "image" {
			if targetVal == "" {
				return fmt.Errorf("missing template image path in if_found step")
			}
			imgPath := targetVal
			if !filepath.IsAbs(imgPath) && ctx.ScriptDir != "" {
				imgPath = filepath.Join(ctx.ScriptDir, imgPath)
			}
			f, err := os.Open(imgPath)
			if err != nil {
				return fmt.Errorf("failed to open template image: %w", err)
			}
			needle, _, err = image.Decode(f)
			f.Close()
			if err != nil {
				return fmt.Errorf("failed to decode template image: %w", err)
			}
		}

		var waitTimeout time.Duration
		if step.WaitTimeout != "" {
			if d, err := time.ParseDuration(step.WaitTimeout); err == nil {
				waitTimeout = d
			}
		}

		deadline := time.Now().Add(waitTimeout)
		for {
			select {
			case <-ctx.AbortChan:
				return fmt.Errorf("execution aborted by user")
			default:
			}

			if findType == "image" {
				confidence := step.Confidence
				if confidence <= 0 {
					confidence = 0.90
				}
				capCfg := capture.CaptureConfig{
					Display:  ":0.0",
					WindowID: ctx.WindowID,
				}
				haystack, err := capture.CaptureScreen(capCfg)
				if err == nil {
					offsetX, offsetY := 0, 0
					if step.Region != "" {
						rx, ry, rw, rh, err := ParseRegion(step.Region, haystack.Bounds().Dx(), haystack.Bounds().Dy())
						if err == nil {
							haystack, offsetX, offsetY = CropImage(haystack, rx, ry, rw, rh)
						}
					}
					fx, fy, _, err := FindImage(haystack, needle, confidence)
					if err == nil {
						found = true
						ctx.LastFoundX = fx + offsetX
						ctx.LastFoundY = fy + offsetY
					}
				}
			} else if findType == "text" {
				if targetVal == "" {
					return fmt.Errorf("missing target text in if_found step")
				}
				ocrAddr := "http://localhost:8765"
				ocrLang := "ch"
				if ctx.Config != nil {
					ocrAddr = ctx.Config.OCRAddress
					ocrLang = ctx.Config.OCRLanguage
				}
				capCfg := capture.CaptureConfig{
					Display:  ":0.0",
					WindowID: ctx.WindowID,
				}
				haystack, err := capture.CaptureScreen(capCfg)
				if err == nil {
					offsetX, offsetY := 0, 0
					if step.Region != "" {
						rx, ry, rw, rh, err := ParseRegion(step.Region, haystack.Bounds().Dx(), haystack.Bounds().Dy())
						if err == nil {
							haystack, offsetX, offsetY = CropImage(haystack, rx, ry, rw, rh)
						}
					}
					fx, fy, _, err := FindText(haystack, ocrAddr, ocrLang, targetVal)
					if err == nil {
						found = true
						ctx.LastFoundX = fx + offsetX
						ctx.LastFoundY = fy + offsetY
					}
				}
			} else {
				return fmt.Errorf("unknown find target in if_found step: %q", findType)
			}

			if found {
				break
			}
			if time.Now().After(deadline) {
				break
			}
			time.Sleep(100 * time.Millisecond)
		}

		var targetSteps []Step
		if found {
			ctx.Logger("[Automation] Condition MET (found %s)", findType)
			targetSteps = step.Steps
		} else {
			ctx.Logger("[Automation] Condition NOT MET (found %s)", findType)
			targetSteps = step.Else
		}

		for _, substep := range targetSteps {
			if err := executeStepWithControl(substep, ctx); err != nil {
				return err
			}
		}
		return nil
	default:
		return ExecuteStep(step, ctx)
	}
}
