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

	"zen-cap/pkg/config"
)

// RunScript starts the sequential execution of a Script.
//
// It is a thin adapter over RunSteps — the HTTP API and the YAML/picker path
// share one implementation, so per-step results, abort handling and the panic
// release cannot drift between them. The signature is load-bearing: the picker
// (picker.go) and `zen-cap run` call it directly.
func RunScript(script Script, cfg *config.Config, scriptDir string, abortChan chan struct{}, logger func(string, ...interface{})) error {
	token := NewRunToken("script:" + script.Name)
	if abortChan != nil {
		// Bridge the historical channel form to the token, so the picker's abort
		// hotkey can still cut a running script short.
		go func() {
			select {
			case <-abortChan:
				token.Cancel()
			case <-token.Done():
			}
		}()
	}

	logger("[Automation] Starting script: %q", script.Name)
	res, runErr := RunSteps(RunOptions{
		Script:      script,
		Config:      cfg,
		ScriptDir:   scriptDir,
		StopOnError: true, // a failing step has always aborted a YAML script
		OnLog:       logger,
		Token:       token,
	})
	if runErr != nil {
		return fmt.Errorf("script execution failed: %w", runErr)
	}
	if res != nil && res.Stopped {
		// `action: stop` ended the run on purpose (RunSteps logged why): reported
		// as success, exactly what this function did before the extraction.
		return nil
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
			return ErrAborted
		default:
		}

		step := steps[i]
		ctx.logf("[Automation] Step %d/%d: %s", i+1, len(steps), step.Action)
		start := time.Now()
		ctx.beginResult()
		err := executeStepWithControl(step, ctx)

		if gotoErr, ok := err.(GotoError); ok {
			// A jump inside the current block is control flow, not a failure; it
			// is recorded as a completed step so the trace shows the visit.
			ctx.recordResult(step.Action, start, nil)
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
		if _, isStop := err.(StopError); isStop {
			// `stop` did exactly what it was asked: recorded as a success, and it
			// ends the run (RunSteps turns that into OK + Stopped).
			ctx.recordResult(step.Action, start, nil)
			return err
		}
		ctx.recordResult(step.Action, start, err)
		if err != nil {
			// Cancellation is never "an error to continue past": every later step
			// would fail instantly and spin against the X server.
			if isAbortErr(err) {
				return err
			}
			if ctx.stopOnError {
				return err
			}
			ctx.logf("[Automation] Step %d (%s) failed, continuing: %v", i+1, step.Action, err)
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
			ctx.logf("[Automation] Skipping step %s (condition %s not met)", interpolatedStep.Action, interpolatedStep.When)
			return nil
		}
	}

	switch strings.ToLower(interpolatedStep.Action) {
	case "loop":
		count := interpolatedStep.Count
		if count <= 0 {
			count = 1
		}
		ctx.logf("[Automation] Loop: entering count=%d", count)
		for i := 0; i < count; i++ {
			select {
			case <-ctx.AbortChan:
				return fmt.Errorf("execution aborted by user")
			default:
			}
			ctx.logf("[Automation] Loop iteration %d/%d", i+1, count)
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
			var err error
			if needle, err = decodeTemplate(targetVal, ctx.ScriptDir); err != nil {
				return err
			}
		}

		var waitTimeout time.Duration
		if interpolatedStep.WaitTimeout != "" {
			if d, err := time.ParseDuration(interpolatedStep.WaitTimeout); err == nil {
				waitTimeout = d
			}
		}

		// One probe per poll attempt, chosen by find type. An unknown find type
		// is a script error and must surface before anything is captured, just
		// as the hand-rolled loop this replaces did.
		var probe func(haystack image.Image, offX, offY int) (bool, error)
		switch findType {
		case "image":
			confidence := interpolatedStep.Confidence
			if confidence <= 0 {
				confidence = 0.90
			}
			probe = func(haystack image.Image, offX, offY int) (bool, error) {
				x, y, _, err := FindImage(haystack, needle, confidence)
				if err != nil {
					return false, nil
				}
				ctx.setFound(x+offX, y+offY)
				return true, nil
			}
		case "text", "ocr":
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
			probe = func(haystack image.Image, offX, offY int) (bool, error) {
				fx, fy, bounds, _, err := FindTextWithBounds(haystack, ocrAddr, ocrLang, ocrModel, targetVal)
				if err != nil {
					return false, nil
				}
				ctx.setFound(fx+offX, fy+offY)

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
					// Best-effort, as before: a failed debug dump must not fail the branch.
					if err := os.MkdirAll(filepath.Dir(outputPath), 0755); err == nil {
						if outF, err := os.Create(outputPath); err == nil {
							if err := png.Encode(outF, textboxImg); err == nil {
								ctx.logf("[Automation] if_found cropped textbox image saved to %s", outputPath)
							}
							outF.Close()
						}
					}
				}
				return true, nil
			}
		default:
			return fmt.Errorf("unknown find target in if_found step: %q", findType)
		}

		// if_found never errors out on a miss — taking the else branch *is* the
		// result. The poller only supplies the cadence, the abort check and,
		// unlike the old loop, a deadline that capture failures cannot outrun.
		poller := newScreenPoller(ctx, waitTimeout, statePollInterval)
		for {
			res, err := poller.step(interpolatedStep.Region, probe)
			if err != nil {
				return err
			}
			if res != outMissed {
				found = res == outDone
				break
			}
		}

		var targetSteps []Step
		if found {
			ctx.logf("[Automation] Condition MET (found %s)", findType)
			targetSteps = interpolatedStep.Steps
		} else {
			ctx.logf("[Automation] Condition NOT MET (found %s)", findType)
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

		img, err := ctx.Target.Screenshot()
		if err != nil {
			return fmt.Errorf("failed to capture screen for if_pixel: %w", err)
		}

		got, ok := pixelAt(img, x, y)
		if !ok {
			return fmt.Errorf("pixel coordinate (%d, %d) out of bounds (%v)", x, y, img.Bounds())
		}

		want, err := parseHexColor(interpolatedStep.Color)
		if err != nil {
			return err
		}

		tolerance := 0
		if interpolatedStep.Tolerance > 0 {
			tolerance = interpolatedStep.Tolerance
		}

		match := colorNear(got, want, tolerance)
		ctx.logf("[Automation] if_pixel at (%d, %d): color is %s, target is %s, tolerance=%d, match=%v",
			x, y, got.Hex(), want.Hex(), tolerance, match)
		ctx.setData(map[string]interface{}{"x": x, "y": y, "color": got.Hex(), "match": match})

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

		// The condition is a window lookup, not a screen capture, so it drives the
		// poller's capture-free tick: same bounded cadence and abort handling as
		// every other poll site, without a second hand-rolled deadline loop.
		poller := newScreenPoller(ctx, waitTimeout, statePollInterval)
		found := false
		for {
			res, err := poller.tick(func() (bool, error) {
				var exists bool
				if x11 := ctx.x11ctx(); x11 != nil {
					_, err := ResolveWindow(x11.XUtil(), interpolatedStep.Window)
					exists = (err == nil)
				}
				if checkAbsent {
					return !exists, nil
				}
				return exists, nil
			})
			if err != nil {
				return err
			}
			if res != outMissed {
				found = res == outDone
				break
			}
		}

		var targetSteps []Step
		if found {
			ctx.logf("[Automation] if_window condition MET (mode: %s)", interpolatedStep.Mode)
			targetSteps = interpolatedStep.Steps
		} else {
			ctx.logf("[Automation] if_window condition NOT MET (mode: %s)", interpolatedStep.Mode)
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
