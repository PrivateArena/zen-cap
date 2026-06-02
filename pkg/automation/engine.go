package automation

import (
	"fmt"
	"image"
	"image/png"
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
		Variables: make(map[string]interface{}),
		Functions: script.Functions,
	}

	logger("[Automation] Starting script: %q", script.Name)
	if err := executeStepList(script.Steps, ctx); err != nil {
		if stopErr, ok := err.(StopError); ok {
			logger("[Automation] Script execution stopped: %v", stopErr)
			return nil
		}
		return fmt.Errorf("script execution failed: %w", err)
	}

	logger("[Automation] Script %q finished successfully!", script.Name)
	return nil
}

type GotoError struct {
	Target string
}

func (e GotoError) Error() string {
	return "goto: " + e.Target
}

type StopError struct {
	Message string
}

func (e StopError) Error() string {
	if e.Message != "" {
		return "stopped: " + e.Message
	}
	return "stopped"
}

func executeStepList(steps []Step, ctx *ExecContext) error {
	for i := 0; i < len(steps); {
		select {
		case <-ctx.AbortChan:
			return fmt.Errorf("execution aborted by user")
		default:
		}

		step := steps[i]
		ctx.Logger("[Automation] Step %d/%d: %s", i+1, len(steps), step.Action)
		err := executeStepWithControl(step, ctx)
		if err != nil {
			if gotoErr, ok := err.(GotoError); ok {
				// Search for target label in the current block first (local jump)
				targetIdx := -1
				for idx, s := range steps {
					if s.Label == gotoErr.Target {
						targetIdx = idx
						break
					}
				}
				if targetIdx != -1 {
					i = targetIdx
					continue
				}
				// Otherwise propagate GotoError upwards
				return gotoErr
			}
			return err
		}
		i++
	}
	return nil
}

// ResolveWindow finds a matching window ID using native X11 window listing.
var ResolveWindow = func(xu *xgbutil.XUtil, target *WindowTarget) (uint32, error) {
	if target == nil {
		return 0, nil
	}
	if xu == nil {
		return 0, fmt.Errorf("X connection is nil")
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
	// 1. Interpolate string fields in a copy of the step
	interpolatedStep := InterpolateStep(step, ctx.Variables)

	// 2. Check conditional expression "when"
	if interpolatedStep.When != "" {
		condMet, err := evaluateCondition(interpolatedStep.When, ctx.Variables)
		if err != nil {
			return err
		}
		if !condMet {
			ctx.Logger("[Automation] Skipping step %s (condition %s not met)", interpolatedStep.Action, interpolatedStep.When)
			return nil
		}
	}

	switch strings.ToLower(interpolatedStep.Action) {
	case "loop":
		count := interpolatedStep.Count
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
			if err := executeStepList(interpolatedStep.Steps, ctx); err != nil {
				return err
			}
		}
		return nil
	case "if_found":
		found := false
		findType := interpolatedStep.Find
		if findType == "" {
			findType = interpolatedStep.Type
		}
		findType = strings.ToLower(findType)

		targetVal := interpolatedStep.Image
		if targetVal == "" {
			targetVal = interpolatedStep.Target
		}
		if targetVal == "" {
			targetVal = interpolatedStep.Text
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
		if interpolatedStep.WaitTimeout != "" {
			if d, err := time.ParseDuration(interpolatedStep.WaitTimeout); err == nil {
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
				confidence := interpolatedStep.Confidence
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
					if interpolatedStep.Region != "" {
						rx, ry, rw, rh, err := ParseRegion(interpolatedStep.Region, haystack.Bounds().Dx(), haystack.Bounds().Dy())
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
			} else if findType == "text" || findType == "ocr" {
				if targetVal == "" {
					return fmt.Errorf("missing target text in if_found step")
				}
				ocrAddr := "http://localhost:8765"
				ocrLang := "ch"
				if ctx.Config != nil {
					ocrAddr = ctx.Config.OCRAddress
					ocrLang = ctx.Config.OCRLanguage
				}
				if interpolatedStep.Language != "" {
					ocrLang = interpolatedStep.Language
				}
				ocrModel := interpolatedStep.Model
				capCfg := capture.CaptureConfig{
					Display:  ":0.0",
					WindowID: ctx.WindowID,
				}
				haystack, err := capture.CaptureScreen(capCfg)
				if err == nil {
					offsetX, offsetY := 0, 0
					if interpolatedStep.Region != "" {
						rx, ry, rw, rh, err := ParseRegion(interpolatedStep.Region, haystack.Bounds().Dx(), haystack.Bounds().Dy())
						if err == nil {
							haystack, offsetX, offsetY = CropImage(haystack, rx, ry, rw, rh)
						}
					}
					fx, fy, bounds, _, err := FindTextWithBounds(haystack, ocrAddr, ocrLang, ocrModel, targetVal)
					if err == nil {
						found = true
						ctx.LastFoundX = fx + offsetX
						ctx.LastFoundY = fy + offsetY

						if interpolatedStep.Output != "" {
							minX, minY := bounds.Min.X, bounds.Min.Y
							maxX, maxY := bounds.Max.X, bounds.Max.Y
							rw := maxX - minX
							rh := maxY - minY

							textboxImg, _, _ := CropImage(haystack, minX, minY, rw, rh)

							outputPath := interpolatedStep.Output
							if !filepath.IsAbs(outputPath) && ctx.ScriptDir != "" {
								outputPath = filepath.Join(ctx.ScriptDir, outputPath)
							}
							if err := os.MkdirAll(filepath.Dir(outputPath), 0755); err == nil {
								if outF, err := os.Create(outputPath); err == nil {
									if err := png.Encode(outF, textboxImg); err == nil {
										ctx.Logger("[Automation] if_found cropped textbox image saved to %s", outputPath)
									}
									outF.Close()
								}
							}
						}
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
			targetSteps = interpolatedStep.Steps
		} else {
			ctx.Logger("[Automation] Condition NOT MET (found %s)", findType)
			targetSteps = interpolatedStep.Else
		}

		if err := executeStepList(targetSteps, ctx); err != nil {
			return err
		}
		return nil
	case "if_pixel":
		x := getIntField(interpolatedStep.X, ctx.Variables, -1)
		y := getIntField(interpolatedStep.Y, ctx.Variables, -1)
		if x == -1 || y == -1 {
			return fmt.Errorf("invalid or missing pixel coordinate for if_pixel: x=%v, y=%v", interpolatedStep.X, interpolatedStep.Y)
		}

		capCfg := capture.CaptureConfig{
			Display:  ":0.0",
			WindowID: ctx.WindowID,
		}
		img, err := capture.CaptureScreen(capCfg)
		if err != nil {
			return fmt.Errorf("failed to capture screen for if_pixel: %w", err)
		}

		bounds := img.Bounds()
		if x < bounds.Min.X || x >= bounds.Max.X || y < bounds.Min.Y || y >= bounds.Max.Y {
			return fmt.Errorf("pixel coordinate (%d, %d) out of bounds (%v)", x, y, bounds)
		}

		col := img.At(x, y)
		r, g, b, _ := col.RGBA()
		currR, currG, currB := uint8(r>>8), uint8(g>>8), uint8(b>>8)

		hexStr := strings.TrimPrefix(interpolatedStep.Color, "#")
		var tr, tg, tb uint8
		if n, err := fmt.Sscanf(hexStr, "%02x%02x%02x", &tr, &tg, &tb); err != nil || n != 3 {
			return fmt.Errorf("invalid hex color string %q for if_pixel", interpolatedStep.Color)
		}

		tolerance := 0
		if interpolatedStep.Tolerance > 0 {
			tolerance = interpolatedStep.Tolerance
		}

		diff := func(a, b uint8) int {
			if a > b {
				return int(a - b)
			}
			return int(b - a)
		}

		match := diff(currR, tr) <= tolerance && diff(currG, tg) <= tolerance && diff(currB, tb) <= tolerance
		ctx.Logger("[Automation] if_pixel at (%d, %d): color is RGB(%d,%d,%d), target is RGB(%d,%d,%d), tolerance=%d, match=%v",
			x, y, currR, currG, currB, tr, tg, tb, tolerance, match)

		var targetSteps []Step
		if match {
			targetSteps = interpolatedStep.Steps
		} else {
			targetSteps = interpolatedStep.Else
		}

		if err := executeStepList(targetSteps, ctx); err != nil {
			return err
		}
		return nil
	case "if_window":
		if interpolatedStep.Window == nil {
			return fmt.Errorf("missing window target in if_window step")
		}

		var waitTimeout time.Duration
		if interpolatedStep.WaitTimeout != "" {
			if d, err := time.ParseDuration(interpolatedStep.WaitTimeout); err == nil {
				waitTimeout = d
			}
		} else if interpolatedStep.Timeout != "" {
			if d, err := time.ParseDuration(interpolatedStep.Timeout); err == nil {
				waitTimeout = d
			}
		}

		checkAbsent := strings.ToLower(interpolatedStep.Mode) == "absent" || strings.ToLower(interpolatedStep.Mode) == "not_exists"

		found := false
		deadline := time.Now().Add(waitTimeout)
		for {
			select {
			case <-ctx.AbortChan:
				return fmt.Errorf("execution aborted by user")
			default:
			}

			_, err := ResolveWindow(ctx.X, interpolatedStep.Window)
			exists := (err == nil)

			if checkAbsent {
				if !exists {
					found = true
					break
				}
			} else {
				if exists {
					found = true
					break
				}
			}

			if time.Now().After(deadline) {
				break
			}
			time.Sleep(100 * time.Millisecond)
		}

		var targetSteps []Step
		if found {
			ctx.Logger("[Automation] if_window condition MET (mode: %s)", interpolatedStep.Mode)
			targetSteps = interpolatedStep.Steps
		} else {
			ctx.Logger("[Automation] if_window condition NOT MET (mode: %s)", interpolatedStep.Mode)
			targetSteps = interpolatedStep.Else
		}

		if err := executeStepList(targetSteps, ctx); err != nil {
			return err
		}
		return nil
	case "var":
		ctx.Variables[interpolatedStep.Name] = evaluateValue(interpolatedStep.Value, ctx.Variables)
		return nil
	case "call":
		funcSteps, exists := ctx.Functions[interpolatedStep.Target]
		if !exists {
			return fmt.Errorf("function not found: %q", interpolatedStep.Target)
		}
		// Shadow params and restore afterwards
		backup := make(map[string]interface{})
		backupExists := make(map[string]bool)
		for k, v := range interpolatedStep.Args {
			val, exists := ctx.Variables[k]
			if exists {
				backup[k] = val
				backupExists[k] = true
			}
			ctx.Variables[k] = evaluateValue(v, ctx.Variables)
		}
		defer func() {
			for k := range interpolatedStep.Args {
				if backupExists[k] {
					ctx.Variables[k] = backup[k]
				} else {
					delete(ctx.Variables, k)
				}
			}
		}()

		if err := executeStepList(funcSteps, ctx); err != nil {
			return err
		}
		return nil
	default:
		return ExecuteStep(interpolatedStep, ctx)
	}
}
