package automation

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"zen-cap/pkg/config"
)

// Unit tests for the run layer (plan step A2). They need no X server: every run
// here injects a fake target through RunOptions.TargetOverride, which is exactly
// the seam that makes the engine testable end to end — control flow, variable
// interpolation, aborts and the trace all run for real.

func runWith(t *testing.T, tgt *mockTarget, opts RunOptions) (*RunResult, *RunError) {
	t.Helper()
	opts.TargetOverride = tgt
	if opts.Token == nil {
		opts.Token = NewRunToken("test")
	}
	if opts.Config == nil {
		opts.Config = &config.Config{OutputDir: t.TempDir()}
	}
	return RunSteps(opts)
}

func TestRunStepsReportsEveryStep(t *testing.T) {
	mt := newMockTarget(nil)
	res, runErr := runWith(t, mt, RunOptions{
		StopOnError: true,
		Script: Script{Steps: []Step{
			{Action: "log", Message: "one"},
			{Action: "click", X: 5, Y: 6},
			{Action: "log", Message: "two"},
		}},
	})
	if runErr != nil {
		t.Fatalf("unexpected failure: %v", runErr)
	}
	if !res.OK || len(res.Steps) != 3 {
		t.Fatalf("want 3 successful steps, got ok=%v %+v", res.OK, res.Steps)
	}
	for i, want := range []string{"log", "click", "log"} {
		if res.Steps[i].Action != want {
			t.Errorf("step %d is %q, want %q (order must be execution order)", i, res.Steps[i].Action, want)
		}
		if !res.Steps[i].OK || res.Steps[i].Index != i {
			t.Errorf("step %d wrong: %+v", i, res.Steps[i])
		}
		if res.Steps[i].ElapsedMS < 0 {
			t.Errorf("step %d has a negative duration", i)
		}
	}
	if res.Steps[1].X != 5 || res.Steps[1].Y != 6 {
		t.Errorf("the click step must report the coordinates it used, got %d,%d", res.Steps[1].X, res.Steps[1].Y)
	}
	if res.Steps[1].ElapsedMS < 0 || len(res.Log) == 0 {
		t.Errorf("no log collected: %+v", res.Log)
	}
	if res.Screen.W != 100 || res.Screen.H != 100 {
		t.Errorf("screen size not reported: %+v", res.Screen)
	}
	// The screen pair is an array on the wire, per the API contract.
	b, err := json.Marshal(res.Screen)
	if err != nil || string(b) != "[100,100]" {
		t.Errorf("screen marshals as %s (err %v), want [100,100]", b, err)
	}
}

// TestRunStepsTraceIsPostOrder pins the nested-trace shape: a loop's body entries
// come before the entry for the loop itself, because the parent is recorded when
// it finishes.
func TestRunStepsTraceIsPostOrder(t *testing.T) {
	mt := newMockTarget(nil)
	res, runErr := runWith(t, mt, RunOptions{
		StopOnError: true,
		Script: Script{Steps: []Step{
			{Action: "loop", Count: 2, Steps: []Step{{Action: "log", Message: "tick"}}},
			{Action: "log", Message: "after"},
		}},
	})
	if runErr != nil {
		t.Fatalf("loop failed: %v", runErr)
	}
	var got []string
	for _, s := range res.Steps {
		got = append(got, s.Action)
	}
	want := []string{"log", "log", "loop", "log"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("trace = %v, want %v", got, want)
	}
	for i, s := range res.Steps {
		if s.Index != i {
			t.Errorf("step %d reports index %d; indices must be dense and unique", i, s.Index)
		}
	}
}

func TestRunStepsStopOnErrorHaltsAtTheFailure(t *testing.T) {
	mt := newMockTarget(nil)
	res, runErr := runWith(t, mt, RunOptions{
		StopOnError: true,
		Script: Script{Steps: []Step{
			{Action: "log", Message: "fine"},
			{Action: "wait", Duration: "not-a-duration"}, // fails
			{Action: "log", Message: "never"},
		}},
	})
	if runErr == nil {
		t.Fatal("want a run error")
	}
	if runErr.Code != CodeStepFailed {
		t.Errorf("code = %q, want %q", runErr.Code, CodeStepFailed)
	}
	if res.OK {
		t.Error("a failed run must report ok=false")
	}
	if len(res.Steps) != 2 {
		t.Fatalf("want the failing step included and the third skipped, got %+v", res.Steps)
	}
	if res.Steps[1].OK || !strings.Contains(res.Steps[1].Error, "duration") {
		t.Errorf("the failing step must carry its error, got %+v", res.Steps[1])
	}
	if res.StoppedAt != 1 {
		t.Errorf("stopped_at = %d, want 1", res.StoppedAt)
	}
	if len(mt.types) != 0 {
		t.Error("nothing should have been injected after the failure")
	}
	for _, l := range res.Log {
		if strings.Contains(l, "never") {
			t.Errorf("step 3 ran anyway: %v", res.Log)
		}
	}
}

func TestRunStepsWithoutStopOnErrorTriesEverything(t *testing.T) {
	mt := newMockTarget(nil)
	res, runErr := runWith(t, mt, RunOptions{
		StopOnError: false,
		Script: Script{Steps: []Step{
			{Action: "log", Message: "one"},
			{Action: "wait", Duration: "nope"},
			{Action: "log", Message: "three"},
		}},
	})
	if runErr == nil || runErr.Code != CodeStepFailed {
		t.Fatalf("want a step_failed summary, got %v", runErr)
	}
	if len(res.Steps) != 3 {
		t.Fatalf("want all 3 steps attempted, got %+v", res.Steps)
	}
	if !res.Steps[0].OK || res.Steps[1].OK || !res.Steps[2].OK {
		t.Errorf("per-step outcomes wrong: %+v", res.Steps)
	}
}

func TestRunStepsStopActionIsSuccess(t *testing.T) {
	mt := newMockTarget(nil)
	res, runErr := runWith(t, mt, RunOptions{
		StopOnError: true,
		Script: Script{Steps: []Step{
			{Action: "log", Message: "before"},
			{Action: "stop", Message: "done on purpose"},
			{Action: "log", Message: "after"},
		}},
	})
	if runErr != nil {
		t.Fatalf("stop must not be a failure: %v", runErr)
	}
	if !res.OK || !res.Stopped {
		t.Errorf("want ok=true stopped=true, got ok=%v stopped=%v", res.OK, res.Stopped)
	}
	if res.StoppedAt != 1 {
		t.Errorf("stopped_at = %d, want 1", res.StoppedAt)
	}
	for _, l := range res.Log {
		if strings.Contains(l, "after") {
			t.Errorf("steps after `stop` ran: %v", res.Log)
		}
	}
}

func TestRunStepsAbortDuringWait(t *testing.T) {
	mt := newMockTarget(nil)
	token := NewRunToken("aborting")
	done := make(chan struct{})
	go func() {
		time.Sleep(30 * time.Millisecond)
		token.Cancel()
		close(done)
	}()

	start := time.Now()
	res, runErr := runWith(t, mt, RunOptions{
		Token:       token,
		StopOnError: true,
		Script:      Script{Steps: []Step{{Action: "wait", Duration: "30s"}, {Action: "log", Message: "never"}}},
	})
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("the abort was not honoured (took %v)", elapsed)
	}
	<-done
	if runErr == nil || runErr.Code != CodeAborted {
		t.Fatalf("want code %q, got %v", CodeAborted, runErr)
	}
	if res.OK {
		t.Error("an aborted run is not ok")
	}
	if len(res.Steps) != 1 || res.Steps[0].OK {
		t.Errorf("the interrupted step should be reported, got %+v", res.Steps)
	}
}

// TestRunStepsWatchdogReportsTimeout is the gate-hostage guard: a `loop count:
// 1000000` must end as a timeout, not as an endless hold of the input gate.
func TestRunStepsWatchdogReportsTimeout(t *testing.T) {
	mt := newMockTarget(nil)
	start := time.Now()
	res, runErr := runWith(t, mt, RunOptions{
		StopOnError: true,
		Timeout:     150 * time.Millisecond,
		Script: Script{Steps: []Step{
			{Action: "loop", Count: 1000000, Steps: []Step{{Action: "wait", Duration: "1s"}}},
		}},
	})
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("watchdog did not fire (took %v)", elapsed)
	}
	if runErr == nil || runErr.Code != CodeTimeout {
		t.Fatalf("want code %q, got %v", CodeTimeout, runErr)
	}
	if res == nil {
		t.Fatal("a timed-out run must still report the partial trace")
	}
}

// TestRunStepsTruncatesA runaway loop must not grow the response without bound.
func TestRunStepsTruncatesLongTraces(t *testing.T) {
	mt := newMockTarget(nil)
	res, runErr := runWith(t, mt, RunOptions{
		StopOnError: true,
		Script: Script{Steps: []Step{
			{Action: "loop", Count: maxTrackedResults + 50, Steps: []Step{{Action: "log", Message: "spam"}}},
		}},
	})
	if runErr != nil {
		t.Fatalf("run failed: %v", runErr)
	}
	if len(res.Steps) != maxTrackedResults || !res.StepsTruncated {
		t.Fatalf("got %d steps, truncated=%v; want the trace capped at %d and flagged",
			len(res.Steps), res.StepsTruncated, maxTrackedResults)
	}
}

func TestRunStepsSeedsAndReturnsVariables(t *testing.T) {
	mt := newMockTarget(nil)
	res, runErr := runWith(t, mt, RunOptions{
		StopOnError: true,
		SeedVars:    map[string]interface{}{"greeting": "hello", "n": 3},
		Script: Script{Steps: []Step{
			{Action: "log", Message: "${greeting} world"},
			{Action: "var", Name: "greeting", Value: "bye"},
		}},
	})
	if runErr != nil {
		t.Fatalf("run failed: %v", runErr)
	}
	// Interpolation is the proof that HTTP requests go through the real engine and
	// not a stripped-down per-step shortcut.
	found := false
	for _, l := range res.Log {
		if strings.Contains(l, "hello world") {
			found = true
		}
	}
	if !found {
		t.Errorf("${greeting} was not interpolated from SeedVars; log=%v", res.Log)
	}
	if res.Variables["greeting"] != "bye" {
		t.Errorf("the updated variable table was not returned: %+v", res.Variables)
	}
}

func TestRunStepsDanglingGotoIsAnError(t *testing.T) {
	mt := newMockTarget(nil)
	_, runErr := runWith(t, mt, RunOptions{
		StopOnError: true,
		Script:      Script{Steps: []Step{{Action: "goto", Target: "nowhere"}}},
	})
	if runErr == nil || runErr.Code != CodeDanglingGoto {
		t.Fatalf("want %q, got %v", CodeDanglingGoto, runErr)
	}
}

func TestRunStepsGotoWithinList(t *testing.T) {
	mt := newMockTarget(nil)
	res, runErr := runWith(t, mt, RunOptions{
		StopOnError: true,
		Script: Script{Steps: []Step{
			{Action: "log", Message: "a"},
			{Action: "goto", Target: "skip"},
			{Action: "log", Message: "skipped"},
			{Action: "log", Message: "b", Label: "skip"},
		}},
	})
	if runErr != nil {
		t.Fatalf("a local goto must succeed: %v", runErr)
	}
	for _, l := range res.Log {
		if strings.Contains(l, "skipped") {
			t.Errorf("goto did not skip the step: %v", res.Log)
		}
	}
}

func TestRunStepsOnLogReceivesEveryLine(t *testing.T) {
	var lines []string
	mt := newMockTarget(nil)
	res, runErr := runWith(t, mt, RunOptions{
		StopOnError: true,
		OnLog:       func(format string, args ...interface{}) { lines = append(lines, format) },
		Script:      Script{Steps: []Step{{Action: "log", Message: "hi"}}},
	})
	if runErr != nil {
		t.Fatalf("run failed: %v", runErr)
	}
	if len(lines) == 0 || len(res.Log) == 0 {
		t.Fatalf("OnLog got %d lines, res.Log %d lines", len(lines), len(res.Log))
	}
}

func TestRunStepsRespectsPreCancelledToken(t *testing.T) {
	mt := newMockTarget(nil)
	token := NewRunToken("already")
	token.Cancel()
	res, runErr := runWith(t, mt, RunOptions{
		Token:       token,
		StopOnError: true,
		Script:      Script{Steps: []Step{{Action: "log", Message: "nope"}, {Action: "click", X: 1, Y: 1}}},
	})
	if runErr == nil {
		t.Fatal("a pre-cancelled run must not report success")
	}
	if runErr.Code != CodeAborted {
		t.Errorf("code = %q, want %q", runErr.Code, CodeAborted)
	}
	if len(mt.clicks) != 0 {
		t.Error("input was injected by a run that was already cancelled")
	}
	if res != nil && len(res.Steps) > 1 {
		t.Errorf("every step should have been refused, got %+v", res.Steps)
	}
}

func TestRunRegistryAbortAllAndActive(t *testing.T) {
	reg := NewRunRegistry()
	a, b := NewRunToken(""), NewRunToken("named")
	reg.Register(a)
	reg.Register(b)
	reg.Unregister(b.ID)

	if got := reg.Active(); len(got) != 1 || got[0] != a.ID {
		t.Fatalf("Active() = %v, want the one registered run (ids are auto-filled on Register)", got)
	}
	aborted := reg.AbortAll()
	if len(aborted) != 1 || aborted[0] != a.ID {
		t.Fatalf("AbortAll() = %v", aborted)
	}
	select {
	case <-a.Done():
	default:
		t.Error("AbortAll did not cancel the token")
	}
}

// TestRunErrorTextKeepsTheStepMessage matters because YAML users see this string
// in notify-send dialogs: the code prefix must not bury the actual reason.
func TestRunErrorTextKeepsTheStepMessage(t *testing.T) {
	err := &RunError{Code: CodeStepFailed, Cause: errSample}
	if err.Error() != "boom" {
		t.Errorf("Error() = %q, want the bare step message", err.Error())
	}
	typed := &RunError{Code: CodeWindowGone, Message: "the window is gone"}
	if !strings.Contains(typed.Error(), "window is gone") {
		t.Errorf("a code-only error should say why: %q", typed.Error())
	}
}

var errSample = &staticErr{"boom"}

type staticErr struct{ s string }

func (e *staticErr) Error() string { return e.s }
