package automation

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"

	"zen-cap/pkg/config"
	"zen-cap/pkg/target"
)

// This file is the single execution path shared by every caller: YAML scripts
// (RunScript, the Alt+a picker, `zen-cap run`) and the HTTP API
// (POST /automation/exec). Both funnel through RunSteps, so per-step results,
// abort handling, the watchdog and the panic release behave identically no
// matter who started the run — and so neither caller can accidentally bypass
// interpolation or control flow, which a direct ExecuteStep call would do.

// Error codes reported by RunError.Code. They map 1:1 onto the HTTP status
// codes documented in ZEN_CAP_SERVER_API.md; keep the two in sync.
const (
	CodeWindowGone    = "window_gone"
	CodeWindowMissing = "window_not_found"
	CodeAborted       = "aborted"
	CodeTimeout       = "timeout"
	CodeXError        = "x_error"
	CodeDanglingGoto  = "dangling_goto"
	CodeStepFailed    = "step_failed"
	CodeBadRequest    = "bad_request"
)

// Bounds on the bookkeeping of a single run. A `loop count: 1000000` is legal
// YAML and, post-watchdog, also bounded in time — but it must not be able to
// grow the result document without limit.
const (
	maxTrackedResults = 4096
	maxTrackedLogs    = 2048
)

// RunError is the typed failure of a run. StepIndex is -1 when the failure is
// not attributable to a particular step (target resolution, bad request).
type RunError struct {
	Code      string
	Message   string
	StepIndex int
	Cause     error
}

func (e *RunError) Error() string {
	switch {
	case e.Cause != nil && e.Message == "":
		// A typed wrapper must not pad the message of the step that failed —
		// RunScript re-wraps it into the text users see.
		return e.Cause.Error()
	case e.Cause != nil:
		return fmt.Sprintf("%s: %v", e.Message, e.Cause)
	default:
		return e.Message
	}
}

func (e *RunError) Unwrap() error { return e.Cause }

func newRunError(code, format string, args ...interface{}) *RunError {
	return &RunError{Code: code, Message: fmt.Sprintf(format, args...), StepIndex: -1}
}

// RunToken is the cancellation handle of one in-flight run. Abort is closed at
// most once; every polling/waiting loop in the engine selects on it.
//
// The channel is a public field because ExecContext.AbortChan is exactly that
// channel — the engine keeps its historical non-nil-channel contract.
type RunToken struct {
	ID        string
	Abort     chan struct{}
	StartedAt time.Time

	cancelOnce sync.Once
	mu         sync.Mutex
	reason     string
}

// NewRunToken builds a token with the given id ("" is legal but then the
// registry cannot address this run by id).
func NewRunToken(id string) *RunToken {
	return &RunToken{ID: id, Abort: make(chan struct{}), StartedAt: time.Now()}
}

// Cancel requests cancellation on behalf of a caller (the HTTP abort route).
func (t *RunToken) Cancel() { t.cancelWith(CodeAborted) }

// CancelWith records why the run is being cut short, so RunSteps can tell an
// agent "your watchdog fired" from "someone pressed abort".
func (t *RunToken) CancelWith(reason string) { t.cancelWith(reason) }

func (t *RunToken) cancelWith(reason string) {
	t.cancelOnce.Do(func() {
		t.mu.Lock()
		t.reason = reason
		t.mu.Unlock()
		close(t.Abort)
	})
}

// Done lets a caller wait for cancellation without touching the channel.
func (t *RunToken) Done() <-chan struct{} { return t.Abort }

// Reason returns CodeAborted / CodeTimeout / "" (not cancelled).
func (t *RunToken) Reason() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.reason
}

// RunRegistry is the set of in-flight runs, so POST /automation/abort can name
// what it cancelled and GET /automation/state can report it.
type RunRegistry struct {
	mu   sync.Mutex
	runs map[string]*RunToken
	seq  int
}

func NewRunRegistry() *RunRegistry {
	return &RunRegistry{runs: make(map[string]*RunToken)}
}

// Register adds a token, filling in a process-unique id when it has none.
func (r *RunRegistry) Register(t *RunToken) {
	if t == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.runs == nil {
		r.runs = make(map[string]*RunToken)
	}
	if t.ID == "" {
		r.seq++
		t.ID = "r" + strconv.Itoa(r.seq)
	}
	r.runs[t.ID] = t
}

func (r *RunRegistry) Unregister(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.runs, id)
}

// AbortAll cancels every in-flight run and returns the ids it cancelled. A run
// that finished between listing and cancelling is still reported: the caller
// asked for it to stop, and it is stopped either way.
func (r *RunRegistry) AbortAll() []string {
	r.mu.Lock()
	tokens := make([]*RunToken, 0, len(r.runs))
	for _, t := range r.runs {
		tokens = append(tokens, t)
	}
	r.mu.Unlock()

	ids := make([]string, 0, len(tokens))
	for _, t := range tokens {
		t.Cancel()
		ids = append(ids, t.ID)
	}
	return ids
}

// Active lists the ids of the runs currently executing.
func (r *RunRegistry) Active() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	ids := make([]string, 0, len(r.runs))
	for id := range r.runs {
		ids = append(ids, id)
	}
	return ids
}

// RunOptions is everything RunSteps needs. It is the union of what a Script
// carries and what an HTTP request carries; the HTTP layer builds it directly.
type RunOptions struct {
	Script    Script
	Config    *config.Config
	ScriptDir string

	// WindowID is a pre-resolved client window id, for callers that selected the
	// window themselves (the HTTP API resolves and activates before starting the
	// run, so the run and the response agree on one id). It takes precedence over
	// Script.Window, and 0 means "screen".
	WindowID uint32

	// SeedVars pre-populates the variable table. HTTP requests are stateless,
	// so an agent threads state by sending back the `variables` of the previous
	// response here.
	SeedVars map[string]interface{}

	// Timeout is the hard ceiling of the whole run, enforced by a watchdog that
	// cancels the token. 0 means no ceiling (what a YAML script gets today).
	Timeout time.Duration

	// StopOnError aborts the run at the first failing step. RunScript always
	// sets it (that is historical YAML behaviour); HTTP callers may ask to keep
	// going and read the per-step errors.
	StopOnError bool

	OnLog func(format string, args ...interface{})

	// ActivateClicks raises and activates the run's window before the first click
	// (see ExecContext.activateClicks). The HTTP API sets it; YAML callers do not,
	// so a focusless script written before this existed still never touches focus.
	ActivateClicks bool

	Token *RunToken

	// TargetOverride short-circuits OpenTarget. Tests use it to run a real
	// step sequence against a fake target; nothing else should need it.
	TargetOverride target.Target
}

// StepResult is the outcome of one executed step, in order, including nested
// steps of loops and conditionals — an agent debugging a UI wants to see the
// whole trace, not just the top level.
type StepResult struct {
	Index     int             `json:"index"`
	Action    string          `json:"action"`
	OK        bool            `json:"ok"`
	Error     string          `json:"error,omitempty"`
	ElapsedMS int64           `json:"elapsed_ms"`
	X         int             `json:"x,omitempty"`
	Y         int             `json:"y,omitempty"`
	Data      json.RawMessage `json:"data,omitempty"`
}

// ScreenSize renders as the two-element array [width, height].
type ScreenSize struct {
	W int
	H int
}

func (s ScreenSize) MarshalJSON() ([]byte, error) {
	return []byte("[" + strconv.Itoa(s.W) + "," + strconv.Itoa(s.H) + "]"), nil
}

func (s *ScreenSize) UnmarshalJSON(b []byte) error {
	var pair []int
	if err := json.Unmarshal(b, &pair); err != nil {
		return err
	}
	if len(pair) != 2 {
		return fmt.Errorf("screen must be [width, height], got %s", b)
	}
	s.W, s.H = pair[0], pair[1]
	return nil
}

// RunResult is the full report of one run.
type RunResult struct {
	RunID     string `json:"run_id"`
	OK        bool   `json:"ok"`
	Stopped   bool   `json:"stopped,omitempty"`
	StoppedAt int    `json:"stopped_at,omitempty"`

	WindowID    uint32     `json:"window_id"`
	WindowTitle string     `json:"window_title"`
	WindowClass string     `json:"window_class"`
	Geometry    WindowGeom `json:"geometry"`
	Screen      ScreenSize `json:"screen"`

	Steps     []StepResult           `json:"steps"`
	Variables map[string]interface{} `json:"variables,omitempty"`
	Log       []string               `json:"log,omitempty"`

	// StepsTruncated/LogTruncated say the trace hit maxTrackedResults/Logs.
	// Without them a runaway `loop` would look like a successful short script.
	StepsTruncated bool `json:"steps_truncated,omitempty"`
	LogTruncated   bool `json:"log_truncated,omitempty"`
}

// RunSteps executes opts and reports every step. It owns the target lifecycle
// (OpenTarget … Close) and never leaves a pointer button held: a run that ends
// in an error, a timeout or an abort force-releases the buttons of a backend
// that can do it (target.PanicReleaser).
//
// res is returned even when runErr is non-nil, so a caller can show the agent
// how far the run got and what it matched before it failed.
func RunSteps(opts RunOptions) (*RunResult, *RunError) {
	cfg := opts.Config
	token := opts.Token
	if token == nil {
		token = NewRunToken("")
	}

	// An aborted-before-start run must not open an X connection at all.
	res := &RunResult{RunID: token.ID, Variables: map[string]interface{}{}}
	select {
	case <-token.Done():
		return res, &RunError{Code: CodeAborted, StepIndex: -1, Cause: ErrAborted}
	default:
	}

	// One log sink for the whole run: it feeds the caller's callback, the
	// target-resolution chatter inside OpenTarget, and the response's `log`.
	tracker := &stepTracker{}
	logf := func(format string, args ...interface{}) {
		line := fmt.Sprintf(format, args...)
		tracker.appendLog(line)
		if opts.OnLog != nil {
			opts.OnLog("%s", line)
		}
	}

	tgt, winID, runErr := openRunTarget(opts, token, logf)
	if runErr != nil {
		// Still hand back the (empty) envelope: a caller reading .ok/.steps must get
		// a well-formed answer even when the window was already gone.
		res.OK = false
		return res, runErr
	}
	defer func() { _ = tgt.Close() }()

	// Watchdog: the hard ceiling. Cancelling the token is the same mechanism a
	// user abort uses, so no step has to know about timeouts specifically.
	var watchdog *time.Timer
	if opts.Timeout > 0 {
		watchdog = time.AfterFunc(opts.Timeout, func() { token.CancelWith(CodeTimeout) })
		defer watchdog.Stop()
	}

	vars := make(map[string]interface{}, len(opts.SeedVars)+8)
	for k, v := range opts.SeedVars {
		vars[k] = v
	}

	ctx := &ExecContext{
		Target:         tgt,
		AbortChan:      token.Abort,
		Logger:         logf,
		ScriptDir:      opts.ScriptDir,
		Config:         cfg,
		Variables:      vars,
		Functions:      opts.Script.Functions,
		tracker:        tracker,
		stopOnError:    opts.StopOnError,
		activateClicks: opts.ActivateClicks,
	}

	ctx.logf("[Automation] Starting run %s: %q (%d steps)", token.ID, opts.Script.Name, len(opts.Script.Steps))
	runErr = executeRun(ctx, opts.Script.Steps, res, token)

	// Coordinate space of the report: for a window-scoped run the geometry and
	// the screenshot both address the CLIENT area (Phase 0 F1); Screen is the
	// size of the captured image.
	res.RunID = token.ID
	res.OK = runErr == nil
	if runErr != nil && runErr.StepIndex >= 0 {
		// stopped_at is where execution halted, whether that was a `stop`, a
		// failure that stopped the run, or the watchdog.
		res.StoppedAt = runErr.StepIndex
	}
	res.Steps = tracker.results
	res.StepsTruncated = tracker.resultsTruncated
	res.Log = tracker.logs
	res.LogTruncated = tracker.logsTruncated
	res.Variables = vars
	fillWindowReport(res, tgt, winID)

	if runErr != nil && InjectsInput(opts.Script.Steps) {
		// A step that injects pointer input can have died between a press and its
		// release, leaving the desktop acting as if the mouse were held. The release
		// is scoped to runs that *could* have pressed something: doing it for a
		// read-only batch would cancel a drag the human is performing right now.
		if ReleaseButtons(tgt) {
			ctx.logf("[Automation] Force-released pointer buttons after failed run")
		}
	}
	if watchdog != nil {
		watchdog.Stop()
	}

	// Report the terminal reason: a watchdog cancellation is a timeout, a
	// registry/HTTP cancellation is an abort.
	if runErr != nil && runErr.Code == CodeAborted {
		if r := token.Reason(); r != "" {
			runErr.Code = r
		}
	}
	return res, runErr
}

// openRunTarget resolves the target for a run: the test seam first, then
// OpenTarget on the script's own target/window configuration.
func openRunTarget(opts RunOptions, token *RunToken, logf func(string, ...interface{})) (target.Target, uint32, *RunError) {
	if opts.TargetOverride != nil {
		return opts.TargetOverride, 0, nil
	}
	spec := TargetSpecFromScript(opts.Script, opts.Config)
	spec.Logf = logf
	// The HTTP layer names the window explicitly; a YAML script names it by
	// title/class and lets OpenTarget resolve it.
	if opts.WindowID != 0 {
		spec.WindowID = opts.WindowID
		spec.Window = nil
		spec.AllowUnresolved = false
	}
	tgt, winID, err := OpenTarget(spec)
	if err != nil {
		if re, ok := asRunError(err); ok {
			return nil, 0, re
		}
		return nil, 0, &RunError{Code: CodeXError, Message: "failed to create automation target", Cause: err}
	}
	return tgt, winID, nil
}

// executeRun drives the top-level list and turns the engine's control-flow
// errors into typed run outcomes.
func executeRun(ctx *ExecContext, steps []Step, res *RunResult, token *RunToken) *RunError {
	err := executeStepList(steps, ctx)
	if err == nil {
		// With stop_on_error false the walk finishes, but a step that failed is
		// still a failed run: ok=false, and the first failure is the summary.
		return summariseFailures(ctx)
	}

	switch e := err.(type) {
	case StopError:
		// `action: stop` is a normal, successful end of a script.
		ctx.logf("[Automation] Script execution stopped: %v", e)
		res.Stopped = true
		res.StoppedAt = ctx.lastIndex
		return nil
	case GotoError:
		// A jump out of the top-level list can never be satisfied; treating it
		// as success would silently truncate the script.
		return &RunError{Code: CodeDanglingGoto, Message: fmt.Sprintf("goto target %q not found in the current step list", e.Target), StepIndex: ctx.lastIndex, Cause: err}
	}

	if isAbortErr(err) {
		code := CodeAborted
		if r := token.Reason(); r != "" {
			code = r
		}
		return &RunError{Code: code, StepIndex: ctx.lastIndex, Cause: err}
	}
	return &RunError{Code: CodeStepFailed, StepIndex: ctx.lastIndex, Cause: err}
}

// summariseFailures turns "the walk completed but some steps failed" into the
// same typed error shape as an early stop, so `ok` never lies to the caller.
func summariseFailures(ctx *ExecContext) *RunError {
	if ctx.tracker == nil {
		return nil
	}
	failed, first := 0, -1
	var cause *StepResult
	for i := range ctx.tracker.results {
		if ctx.tracker.results[i].OK {
			continue
		}
		failed++
		if first == -1 {
			first = i
			cause = &ctx.tracker.results[i]
		}
	}
	if failed == 0 {
		return nil
	}
	if first == -1 {
		first = ctx.lastIndex
	}
	return &RunError{
		Code:      CodeStepFailed,
		Message:   fmt.Sprintf("%d of %d step(s) failed", failed, len(ctx.tracker.results)),
		StepIndex: first,
		Cause:     stepErr{cause},
	}
}

// stepErr adapts a recorded step failure to error, so RunError.Cause carries the
// original text without pretending to be a richer error type.
type stepErr struct{ res *StepResult }

func (e stepErr) Error() string {
	if e.res == nil {
		return "step failed"
	}
	return e.res.Error
}

func isAbortErr(err error) bool {
	return errors.Is(err, ErrAborted)
}

// asRunError unwraps a *RunError from an error chain.
func asRunError(err error) (*RunError, bool) {
	for err != nil {
		if re, ok := err.(*RunError); ok {
			return re, true
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return nil, false
		}
		err = u.Unwrap()
	}
	return nil, false
}

// fillWindowReport records what the run actually addressed, so an agent can
// detect a recycled window id (the id it sent now belongs to something else).
func fillWindowReport(res *RunResult, tgt target.Target, winID uint32) {
	res.Screen.W, res.Screen.H = tgt.ScreenSize()
	res.WindowID = winID

	switch t := tgt.(type) {
	case *target.X11Target:
		if t.WinID() != 0 {
			res.WindowID = t.WinID()
		}
		info, err := DescribeWindow(t.XUtil(), res.WindowID)
		if err != nil {
			return
		}
		res.WindowTitle, res.WindowClass = info.Title, info.Class
		res.Geometry = info.Geometry
	case *target.VFBTarget:
		if x11 := t.X11(); x11 != nil {
			info, err := DescribeWindow(x11.XUtil(), res.WindowID)
			if err == nil {
				res.WindowTitle, res.WindowClass = info.Title, info.Class
				res.Geometry = info.Geometry
			}
		}
	}
}

// stepTracker accumulates the per-step trace of one run. Only the executor
// goroutine touches it, so it needs no lock.
type stepTracker struct {
	results          []StepResult
	logs             []string
	Seq              int
	resultsTruncated bool
	logsTruncated    bool
}

func (s *stepTracker) appendResult(r StepResult) {
	if len(s.results) >= maxTrackedResults {
		s.resultsTruncated = true
		return
	}
	s.results = append(s.results, r)
}

func (s *stepTracker) appendLog(line string) {
	if len(s.logs) >= maxTrackedLogs {
		s.logsTruncated = true
		return
	}
	s.logs = append(s.logs, line)
}
