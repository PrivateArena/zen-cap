package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/jezek/xgbutil"

	"zen-cap/pkg/automation"
	"zen-cap/pkg/config"
)

// The /automation HTTP surface: one write endpoint that takes the engine's own
// Step JSON, plus read endpoints for everything an agent cannot infer from a
// screenshot.
//
// Design notes that are easy to lose later:
//
//   - Stateless on purpose. Every request names its target window; the response
//     returns the resolved id, title, class and geometry so the caller can thread
//     state through requests. Nothing survives a daemon restart.
//   - No authentication. The daemon binds localhost for a single trusted user,
//     and `{"action":"run"}` is arbitrary bash — that is the accepted threat
//     model, recorded in ZEN_CAP_SERVER_API.md. deny_api_input is the only gate.
//   - No pipeline on automation captures: the pipeline can open an overlay that
//     grabs the pointer and keyboard, which would deadlock the agent's own input.
//   - Everything below the handlers lives in pkg/automation, so it is all testable
//     without an HTTP server. This file must not call xtest/xproto directly.

// Error codes of the API. They are part of the wire format; the HTTP status is
// derived from them in one place, httpStatusFor.
const (
	codeBadRequest     = "bad_request"
	codeUnknownAction  = "unknown_action"
	codeInputDisabled  = "input_disabled"
	codeWindowNotFound = "window_not_found"
	codeWindowGone     = "window_gone"
	codeTimeout        = "timeout"
	codeAborted        = "aborted"
	codeBusy           = "busy_timeout"
	codeTooLarge       = "too_large"
	codeXError         = "x_error"
	codeNoDisplay      = "no_display"
	codeDanglingGoto   = "dangling_goto"
	codeStepFailed     = "step_failed"
	// maxRequestBodyBytes bounds a request body: the run itself is bounded by the
	// watchdog, but a 20 MB steps array should be refused at the door rather than
	// decoded and validated while it holds the input gate.
	maxRequestBodyBytes = 1 << 20

	defaultExecTimeout  = 30 * time.Second
	defaultInlineMaxLen = 8 << 20
)

// APIError is an HTTP-layer failure with a machine-readable code.
type APIError struct {
	Code    string
	Message string
	Status  int
}

func (e *APIError) Error() string {
	if e.Message == "" {
		return e.Code
	}
	return e.Message
}

func newAPIError(status int, code, format string, args ...interface{}) *APIError {
	return &APIError{Code: code, Message: fmt.Sprintf(format, args...), Status: status}
}

// automationAPI owns the two pieces of process state the API needs: the registry
// of in-flight runs, and the input gate.
type automationAPI struct {
	cfg  *config.Config
	runs *automation.RunRegistry

	// inputGate is a capacity-1 channel used as a mutex with a queue: exactly one
	// run may inject input at a time, and queued senders are served first-come
	// first-served (a sync.Mutex offers neither cancellation nor ordering).
	inputGate chan struct{}
}

func newAutomationAPI(cfg *config.Config) *automationAPI {
	return &automationAPI{cfg: cfg, runs: automation.NewRunRegistry(), inputGate: make(chan struct{}, 1)}
}

// register wires the routes. Called from startAPIServer, and the only place the
// automation routes are attached (A13).
func (a *automationAPI) register(mux *http.ServeMux) {
	mux.HandleFunc("/automation/actions", a.handleActions)
	mux.HandleFunc("/automation/exec", a.handleExec)
	mux.HandleFunc("/automation/screenshot", a.handleScreenshot)
	mux.HandleFunc("/automation/windows", a.handleWindows)
	mux.HandleFunc("/automation/window", a.handleWindow)
	mux.HandleFunc("/automation/state", a.handleState)
	mux.HandleFunc("/automation/abort", a.handleAbort)

	fmt.Printf("[API] Automation routes registered (deny_api_input=%t, max_run=%ds)\n",
		a.cfg.DenyAPIInput, a.cfg.APIMaxRunDurationSeconds)
}

// gate acquires the input lock, or reports ctx.Done() having fired. The caller
// must call the returned release function.
func (a *automationAPI) gate(ctx context.Context) (func(), *APIError) {
	release := func() { <-a.inputGate }

	// Try the fast path first so an idle daemon does not queue behind itself.
	select {
	case a.inputGate <- struct{}{}:
		return release, nil
	default:
	}

	select {
	case a.inputGate <- struct{}{}:
		return release, nil
	case <-ctx.Done():
		// 429, not 408: the run was not too slow, the caller was one of two.
		return nil, newAPIError(http.StatusTooManyRequests, codeBusy,
			"another run holds the pointer and this request gave up waiting (%v)", ctx.Err())
	}
}

// guardInput refuses input-injecting batches while deny_api_input is set. Read-only
// steps stay allowed, so an agent can keep looking at the screen.
func (a *automationAPI) guardInput(steps []automation.Step) *APIError {
	if a.cfg.DenyAPIInput && automation.InjectsInput(steps) {
		return newAPIError(http.StatusForbidden, codeInputDisabled,
			"deny_api_input is set in the configuration; read-only actions are still allowed")
	}
	return nil
}

// TargetSelector is the shared target half of every request body. At most one of
// these should be set; precedence is documented on resolve.
type TargetSelector struct {
	Window     string `json:"window,omitempty"`      // "active", a window id (0x… or decimal), or a title/class fragment
	WindowID   string `json:"window_id,omitempty"`   // an explicit window id
	PID        int    `json:"pid,omitempty"`         // owner process
	Class      string `json:"class,omitempty"`       // WM_CLASS substring
	Title      string `json:"title,omitempty"`       // window title substring
	Launch     string `json:"launch,omitempty"`      // command to start, then wait for its window
	Display    string `json:"display,omitempty"`     // "" = the daemon's $DISPLAY
	NoActivate bool   `json:"no_activate,omitempty"` // do not raise/focus while resolving
}

// lookup turns a selector into a concrete window id, with the same leniency the
// existing /screenshot endpoint has always shown: an unparsable `window` string is
// tried as a title/class fragment before it is an error.
//
// Precedence: window_id > window > launch > pid/class/title > nothing (whole screen).
//
// It returns an id, not a target: RunSteps opens (and closes) its own connection,
// so that the run's lifetime is not entangled with the request's.
//
// And it changes nothing on screen. Raising and focusing is a separate step
// (raise), because focus is a side effect on *someone else's* run: it has to
// happen inside the input gate, not while another run is clicking.
func (t TargetSelector) lookup(xu *xgbutil.XUtil) (uint32, *APIError) {
	adopt := func(id uint32) (uint32, *APIError) {
		if id == 0 {
			return 0, newAPIError(http.StatusNotFound, codeWindowNotFound, "window id 0 is not a window")
		}
		return id, nil
	}

	switch {
	case t.WindowID != "":
		id, err := parseWindowID(t.WindowID)
		if err != nil {
			return 0, newAPIError(http.StatusBadRequest, codeBadRequest, "window_id %q is not a hex (0x…) or decimal id", t.WindowID)
		}
		return adopt(id)

	case t.Window != "":
		switch strings.ToLower(t.Window) {
		case "active":
			id, err := automation.ActiveWindowID(xu)
			if err != nil {
				return 0, newAPIError(http.StatusNotFound, codeWindowNotFound, "no active window: %v", err)
			}
			return id, nil // "active" is an explicit choice, never re-activated
		case "screen", "root", "none":
			return 0, nil
		}
		if id, err := parseWindowID(t.Window); err == nil {
			return adopt(id)
		}
		// Not an id: a title or class fragment, matched like a YAML selector would.
		info, err := automation.FindWindow(xu, t.Window, t.Window, 0)
		if err != nil {
			return 0, newAPIError(http.StatusNotFound, codeWindowNotFound, "%v", err)
		}
		return adopt(info.ID)

	case t.Launch != "":
		id, err := resolveAndActivateWindow(xu, 0, "", "", t.Launch)
		if err != nil {
			return 0, newAPIError(http.StatusNotFound, codeWindowNotFound, "launch %q: %v", t.Launch, err)
		}
		return id, nil

	case t.PID > 0 || t.Class != "" || t.Title != "":
		if t.NoActivate {
			info, err := automation.FindWindow(xu, t.Class, t.Title, t.PID)
			if err != nil {
				return 0, newAPIError(http.StatusNotFound, codeWindowNotFound, "%v", err)
			}
			return info.ID, nil
		}
		id, err := resolveAndActivateWindow(xu, t.PID, t.Class, t.Title, "")
		if err != nil {
			return 0, newAPIError(http.StatusNotFound, codeWindowNotFound, "%v", err)
		}
		return id, nil
	}

	// No selector at all: the whole screen, which is a legitimate target.
	return 0, nil
}

// raise brings the resolved window forward and focuses it, unless the caller asked
// not to. It is deliberately separate from lookup: see the note there.
func (t TargetSelector) raise(xu *xgbutil.XUtil, id uint32) {
	if t.NoActivate || id == 0 {
		return
	}
	activateAndRaiseWindow(xu, id)
}

// ExecRequest is the body of POST /automation/exec.
type ExecRequest struct {
	TargetSelector
	Steps []automation.Step `json:"steps"`
	// Timeout bounds this run; it is always clamped by api_max_run_duration_seconds.
	Timeout string `json:"timeout,omitempty"`
	// StopOnError defaults to true, matching what a YAML script does.
	StopOnError *bool                  `json:"stop_on_error,omitempty"`
	Vars        map[string]interface{} `json:"vars,omitempty"`
	WithLog     bool                   `json:"with_log,omitempty"`
}

func (a *automationAPI) handleActions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	raw, err := automation.CatalogJSON()
	if err != nil {
		writeAPIError(w, newAPIError(http.StatusInternalServerError, codeXError, "catalog: %v", err))
		return
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(raw, &doc); err != nil {
		writeAPIError(w, newAPIError(http.StatusInternalServerError, codeXError, "catalog is not JSON: %v", err))
		return
	}
	doc["status"] = json.RawMessage(`"success"`)
	writeJSON(w, http.StatusOK, doc)
}

// execResponse keeps the envelope ({"status":"success",…}) while inlining the run
// report, so a client reads ok/steps/variables at the top level.
type execResponse struct {
	Status string `json:"status"`
	*automation.RunResult
}

// execErrorResponse is the failure envelope, and still carries the partial trace:
// "which step failed and what did it match before that" is the whole point of the
// endpoint, and is worthless if a 500 hides it.
type execErrorResponse struct {
	Status string `json:"status"`
	Error  string `json:"error"`
	Code   string `json:"code"`
	StepAt int    `json:"step_index,omitempty"`
	*automation.RunResult
}

func (a *automationAPI) handleExec(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBodyBytes)
	var req ExecRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAPIError(w, newAPIError(http.StatusBadRequest, codeBadRequest, "bad request body: %v", err))
		return
	}
	if len(req.Steps) == 0 {
		writeAPIError(w, newAPIError(http.StatusBadRequest, codeBadRequest, "steps is empty; nothing to do"))
		return
	}

	// Ordered so that a malformed request is refused before it can touch X at all:
	// every check up to and including runTimeout is pure, so a typo costs no
	// connection and no pointer movement.
	if err := automation.ValidateSteps(req.Steps); err != nil {
		writeAPIError(w, badRequestToAPI(err))
		return
	}
	if apiErr := a.guardInput(req.Steps); apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}

	timeout, apiErr := a.runTimeout(req.Timeout)
	if apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}

	xu, err := automation.Connect(req.Display)
	if err != nil {
		writeAPIError(w, newAPIError(http.StatusServiceUnavailable, codeNoDisplay, "%v", err))
		return
	}
	defer xu.Conn().Close()

	// Read-only resolution first: a request for a window that does not exist must
	// fail without having touched focus, and without waiting behind an unrelated run.
	winID, apiErr := req.TargetSelector.lookup(xu)
	if apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}

	release, apiErr := a.gate(r.Context())
	if apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}
	defer release()

	// Only now is it safe to move the window manager's attention: this run owns the
	// pointer from here on.
	req.TargetSelector.raise(xu, winID)

	token := automation.NewRunToken("")
	a.runs.Register(token)
	defer a.runs.Unregister(token.ID)

	stopOnError := true
	if req.StopOnError != nil {
		stopOnError = *req.StopOnError
	}

	res, runErr := automation.RunSteps(automation.RunOptions{
		Script: automation.Script{
			Name:   "http-exec",
			Steps:  req.Steps,
			Target: &automation.TargetConfig{Type: "x11", Display: req.Display},
		},
		WindowID:       winID,
		Config:         a.cfg,
		ScriptDir:      a.cfg.AutomationDir,
		SeedVars:       req.Vars,
		Timeout:        timeout,
		StopOnError:    stopOnError,
		ActivateClicks: true,
		Token:          token,
	})

	if runErr != nil {
		apiErr = runErrorToAPI(runErr)
		if res != nil && !req.WithLog {
			res.Log = nil
		}
		writeJSON(w, apiErr.Status, execErrorResponse{
			Status:    "error",
			Error:     apiErr.Message,
			Code:      apiErr.Code,
			StepAt:    runErr.StepIndex,
			RunResult: res,
		})
		return
	}

	if !req.WithLog && res != nil {
		res.Log = nil
	}
	writeJSON(w, http.StatusOK, execResponse{Status: "success", RunResult: res})
}

// runTimeout applies the documented precedence: the request asks, the
// configuration caps.
func (a *automationAPI) runTimeout(spec string) (time.Duration, *APIError) {
	if spec == "" {
		spec = defaultExecTimeout.String()
	}
	d, err := time.ParseDuration(spec)
	if err != nil {
		return 0, newAPIError(http.StatusBadRequest, codeBadRequest, "invalid timeout %q: %v", spec, err)
	}
	if d <= 0 {
		return 0, newAPIError(http.StatusBadRequest, codeBadRequest, "timeout must be positive, got %q", spec)
	}
	ceiling := time.Duration(a.cfg.APIMaxRunDurationSeconds) * time.Second
	if ceiling > 0 && d > ceiling {
		return ceiling, nil
	}
	return d, nil
}

// ScreenshotRequest is the body of POST /automation/screenshot.
type ScreenshotRequest struct {
	TargetSelector
	Output         string `json:"output,omitempty"`
	Region         string `json:"region,omitempty"`
	Inline         bool   `json:"inline,omitempty"`
	MaxInlineBytes int    `json:"max_inline_bytes,omitempty"`
}

type screenshotResponse struct {
	Status    string `json:"status"`
	Path      string `json:"path"`
	Width     int    `json:"width"`
	Height    int    `json:"height"`
	Bytes     int    `json:"bytes"`
	MIME      string `json:"mime,omitempty"`
	Base64    string `json:"base64,omitempty"`
	Encoding  string `json:"encoding,omitempty"`
	Truncated bool   `json:"truncated"`
	WindowID  uint32 `json:"window_id,omitempty"`
	Title     string `json:"window_title,omitempty"`
}

func (a *automationAPI) handleScreenshot(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBodyBytes)
	var req ScreenshotRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAPIError(w, newAPIError(http.StatusBadRequest, codeBadRequest, "bad request body: %v", err))
		return
	}

	xu, err := automation.Connect(req.Display)
	if err != nil {
		writeAPIError(w, newAPIError(http.StatusServiceUnavailable, codeNoDisplay, "%v", err))
		return
	}
	defer xu.Conn().Close()

	// No input gate here, and no raise: a capture injects nothing and xcbgrab reads
	// the window's own drawable, so an occluded window captures perfectly well and
	// the request has no business changing the user's focus. An agent can watch the
	// screen while a click sequence is queued behind it.
	winID, apiErr := req.TargetSelector.lookup(xu)
	if apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}
	tgt, resolvedID, err := automation.OpenTarget(automation.TargetSpec{
		Display:         req.Display,
		Backend:         "x11",
		WindowID:        winID,
		AllowUnresolved: false,
		Config:          a.cfg,
	})
	if err != nil {
		writeAPIError(w, targetErrorToAPI(err))
		return
	}
	defer func() { _ = tgt.Close() }()

	shot, err := automation.CaptureTo(tgt, automation.ShotSpec{
		Output: req.Output,
		Region: req.Region,
		Config: a.cfg,
	})
	if err != nil {
		writeAPIError(w, targetErrorToAPI(err))
		return
	}

	resp := screenshotResponse{Status: "success", Path: shot.Path, Width: shot.Width, Height: shot.Height, Bytes: shot.Bytes, WindowID: resolvedID}
	if info, err := automation.DescribeWindowFromTarget(tgt, resolvedID); err == nil {
		resp.Title = info.Title
	}

	if req.Inline {
		data, err := os.ReadFile(shot.Path)
		if err != nil {
			writeAPIError(w, newAPIError(http.StatusInternalServerError, codeXError, "cannot read back %s: %v", shot.Path, err))
			return
		}
		limit := a.cfg.APIMaxInlineBytes
		if limit <= 0 {
			limit = defaultInlineMaxLen
		}
		explicit := req.MaxInlineBytes
		if explicit > 0 && explicit < limit {
			limit = explicit
		}
		resp.MIME = "image/png"
		resp.Encoding = "base64"
		if len(data) > limit {
			if explicit > 0 {
				// The caller named a budget for the bytes it asked for; saying
				// "here is a file path instead" with a 200 would bury that.
				writeAPIError(w, newAPIError(http.StatusRequestEntityTooLarge, codeTooLarge,
					"%s is %d bytes, over the requested max_inline_bytes=%d; the file is still written at this path",
					shot.Path, len(data), limit))
				return
			}
			// The configuration ceiling: keep the 200 and the path.
			resp.Truncated = true
		} else {
			resp.Base64 = base64.StdEncoding.EncodeToString(data)
		}
	}

	writeJSON(w, http.StatusOK, resp)
}

type windowsResponse struct {
	Status       string                  `json:"status"`
	ActiveWindow uint32                  `json:"active_window_id,omitempty"`
	Windows      []automation.WindowInfo `json:"windows"`
	Count        int                     `json:"count"`
}

func (a *automationAPI) handleWindows(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	xu, apiErr := connectQuery(r, a.cfg)
	if apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}
	defer xu.Conn().Close()

	windows, err := automation.ListWindows(xu)
	if err != nil {
		writeAPIError(w, newAPIError(http.StatusInternalServerError, codeXError, "list windows: %v", err))
		return
	}
	resp := windowsResponse{Status: "success", Windows: windows, Count: len(windows)}
	if id, err := automation.ActiveWindowID(xu); err == nil {
		resp.ActiveWindow = id
	}
	writeJSON(w, http.StatusOK, resp)
}

type windowResponse struct {
	Status string                `json:"status"`
	Window automation.WindowInfo `json:"window"`
	// Candidates lists every match when a class/title query is ambiguous, so an
	// agent can re-ask with an id instead of guessing.
	Candidates []automation.WindowInfo `json:"candidates,omitempty"`
}

func (a *automationAPI) handleWindow(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	xu, apiErr := connectQuery(r, a.cfg)
	if apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}
	defer xu.Conn().Close()

	q := r.URL.Query()
	if raw := q.Get("id"); raw != "" {
		id, err := parseWindowID(raw)
		if err != nil {
			writeAPIError(w, newAPIError(http.StatusBadRequest, codeBadRequest, "id %q is not a hex (0x…) or decimal window id", raw))
			return
		}
		info, err := automation.DescribeWindow(xu, id)
		if err != nil {
			writeAPIError(w, newAPIError(http.StatusNotFound, codeWindowNotFound, "%v", err))
			return
		}
		writeJSON(w, http.StatusOK, windowResponse{Status: "success", Window: info})
		return
	}

	class, title := q.Get("class"), q.Get("title")
	pid, _ := strconv.Atoi(q.Get("pid"))
	if class == "" && title == "" && pid == 0 {
		writeAPIError(w, newAPIError(http.StatusBadRequest, codeBadRequest, "one of id, class, title or pid is required"))
		return
	}

	// FindWindow applies the engine's first-match rule (same as a YAML selector);
	// the candidates list is the disambiguation an agent needs when several windows
	// share a title.
	info, err := automation.FindWindow(xu, class, title, pid)
	if err != nil {
		writeAPIError(w, newAPIError(http.StatusNotFound, codeWindowNotFound, "%v", err))
		return
	}
	resp := windowResponse{Status: "success", Window: info}
	if all, err := automation.ListWindows(xu); err == nil {
		sel := &automation.WindowTarget{Title: title, Class: class}
		for _, cand := range all {
			if pid > 0 && cand.PID != pid {
				continue
			}
			if (title != "" || class != "") && !automation.MatchWindowTitleClass(cand, sel) {
				continue
			}
			resp.Candidates = append(resp.Candidates, cand)
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

type stateResponse struct {
	Status        string                  `json:"status"`
	Display       string                  `json:"display"`
	Screens       []automation.ScreenInfo `json:"screens"`
	ActiveWindow  *automation.WindowInfo  `json:"active_window,omitempty"`
	Pointer       automation.Point        `json:"pointer"`
	HeldButtons   []string                `json:"held_buttons"`
	DenyAPIInput  bool                    `json:"deny_api_input"`
	ActiveRuns    []string                `json:"active_runs"`
	MaxRunSeconds int                     `json:"max_run_seconds"`
}

// handleState never takes the gate: it is how an agent checks whether the desktop
// is sane while a run holds the pointer, so it must always be answerable.
func (a *automationAPI) handleState(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	xu, apiErr := connectQuery(r, a.cfg)
	if apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}
	defer xu.Conn().Close()

	displayName := r.URL.Query().Get("display")
	if displayName == "" {
		displayName = os.Getenv("DISPLAY")
	}
	state, err := automation.DescribeDisplayOn(xu, displayName)
	if err != nil {
		writeAPIError(w, newAPIError(http.StatusInternalServerError, codeXError, "display state: %v", err))
		return
	}
	resp := stateResponse{
		Status:        "success",
		Display:       state.Display,
		Screens:       state.Screens,
		ActiveWindow:  state.Active,
		Pointer:       state.Pointer,
		HeldButtons:   []string{},
		DenyAPIInput:  a.cfg.DenyAPIInput,
		ActiveRuns:    a.runs.Active(),
		MaxRunSeconds: a.cfg.APIMaxRunDurationSeconds,
	}
	if _, held, err := automation.PointerState(xu); err == nil {
		resp.HeldButtons = held
	}
	writeJSON(w, http.StatusOK, resp)
}

type abortResponse struct {
	Status          string   `json:"status"`
	Aborted         int      `json:"aborted"`
	RunIDs          []string `json:"run_ids,omitempty"`
	ReleasedButtons bool     `json:"released_buttons"`
}

// handleAbort is deliberately not queued behind the gate it escapes: a hung run is
// exactly the situation in which the gate is held.
func (a *automationAPI) handleAbort(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	ids := a.runs.AbortAll()

	// Cancelling the token stops the run at its next check; the buttons come free
	// in case it was interrupted between a press and its release.
	display := r.URL.Query().Get("display")
	if display == "" {
		display = os.Getenv("DISPLAY")
	}
	released, err := automation.ReleasePointerButtons(display)
	if err != nil {
		writeAPIError(w, newAPIError(http.StatusServiceUnavailable, codeNoDisplay, "aborted %d run(s) but could not release buttons: %v", len(ids), err))
		return
	}
	writeJSON(w, http.StatusOK, abortResponse{Status: "success", Aborted: len(ids), RunIDs: ids, ReleasedButtons: released})
}

// connectQuery opens the display named in ?display= (or the daemon's own).
func connectQuery(r *http.Request, cfg *config.Config) (*xgbutil.XUtil, *APIError) {
	display := r.URL.Query().Get("display")
	xu, err := automation.Connect(display)
	if err != nil {
		return nil, newAPIError(http.StatusServiceUnavailable, codeNoDisplay, "%v", err)
	}
	return xu, nil
}

// badRequestToAPI keeps the specific code an agent can act on: "fix this action
// name" is not the same advice as "your JSON is malformed".
func badRequestToAPI(err error) *APIError {
	msg := err.Error()
	code := codeBadRequest
	if strings.Contains(msg, "unknown action") {
		code = codeUnknownAction
	}
	return newAPIError(http.StatusBadRequest, code, "%v", err)
}

// runErrorToAPI is the one place engine codes become HTTP statuses.
func runErrorToAPI(e *automation.RunError) *APIError {
	switch e.Code {
	case automation.CodeWindowGone:
		return newAPIError(http.StatusConflict, codeWindowGone, "%v", e)
	case automation.CodeTimeout:
		return newAPIError(http.StatusGatewayTimeout, codeTimeout, "%v", e)
	case automation.CodeAborted:
		// The caller asked for this; it is not a server error.
		return newAPIError(http.StatusOK, codeAborted, "%v", e)
	case automation.CodeWindowMissing:
		return newAPIError(http.StatusNotFound, codeWindowNotFound, "%v", e)
	case automation.CodeBadRequest:
		return badRequestToAPI(e)
	case automation.CodeDanglingGoto:
		return newAPIError(http.StatusBadRequest, codeDanglingGoto, "%v", e)
	case automation.CodeXError:
		return newAPIError(http.StatusInternalServerError, codeXError, "%v", e)
	default:
		return newAPIError(http.StatusInternalServerError, codeStepFailed, "%v", e)
	}
}

// targetErrorToAPI maps an OpenTarget/CaptureTo failure, which is a plain error
// unless the engine already typed it.
func targetErrorToAPI(err error) *APIError {
	if re, ok := err.(*automation.RunError); ok {
		return runErrorToAPI(re)
	}
	if strings.Contains(err.Error(), "no display") || strings.Contains(err.Error(), "cannot open display") {
		return newAPIError(http.StatusServiceUnavailable, codeNoDisplay, "%v", err)
	}
	return newAPIError(http.StatusInternalServerError, codeXError, "%v", err)
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	body, err := json.Marshal(v)
	if err != nil {
		http.Error(w, "encoding failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

func writeAPIError(w http.ResponseWriter, e *APIError) {
	if e == nil {
		return
	}
	status := e.Status
	if status == 0 {
		status = http.StatusInternalServerError
	}
	writeJSON(w, status, map[string]string{"status": "error", "error": e.Error(), "code": e.Code})
}
