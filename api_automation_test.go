package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"zen-cap/pkg/automation"
	"zen-cap/pkg/config"
)

// HTTP-layer tests for /automation. They need no X server: every one of them
// asserts that a request is refused *before* the handler touches the display,
// which is the property that makes the endpoint safe to poke at.

func testConfig() *config.Config {
	cfg := config.DefaultConfig()
	cfg.OutputDir = os.TempDir()
	cfg.APIMaxRunDurationSeconds = 60
	cfg.APIMaxInlineBytes = 1 << 20
	return cfg
}

func doJSON(t *testing.T, h http.HandlerFunc, method, body string) (*httptest.ResponseRecorder, map[string]interface{}) {
	t.Helper()
	req := httptest.NewRequest(method, "/automation/x", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h(rec, req)
	doc := map[string]interface{}{}
	respBody := rec.Body.Bytes()
	// A plain-text 405 from http.Error is a legitimate response shape too.
	if len(respBody) > 0 && respBody[0] == '{' {
		if err := json.Unmarshal(respBody, &doc); err != nil {
			t.Fatalf("response is not JSON (%d %s): %q", rec.Code, rec.Header().Get("Content-Type"), respBody)
		}
	}
	return rec, doc
}

// --- GET /automation/actions ---------------------------------------------

func TestActionsEndpointDescribesEveryAction(t *testing.T) {
	a := newAutomationAPI(testConfig())
	rec, doc := doJSON(t, a.handleActions, http.MethodGet, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	if doc["status"] != "success" {
		t.Errorf("status field = %v", doc["status"])
	}
	raw, _ := json.Marshal(doc["actions"])
	var actions []automation.ActionSpec
	if err := json.Unmarshal(raw, &actions); err != nil {
		t.Fatalf("actions is not a list of ActionSpec: %v", err)
	}
	if len(actions) < 20 {
		t.Errorf("only %d actions published", len(actions))
	}
	seen := map[string]bool{}
	for _, act := range actions {
		seen[act.Action] = true
	}
	for _, want := range []string{"click", "type", "key", "scroll", "screenshot", "wait_until", "window_state", "window", "find_image", "loop", "if_found", "stop"} {
		if !seen[want] {
			t.Errorf("the catalog omits %q; an agent reading only this endpoint could never use it", want)
		}
	}
	if _, ok := doc["step_schema"]; !ok {
		t.Error("no step_schema: the endpoint cannot teach a client what a step looks like")
	}

	// Method discipline, same as the existing endpoints.
	rec, _ = doJSON(t, a.handleActions, http.MethodPost, "{}")
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST /automation/actions = %d, want 405", rec.Code)
	}
}

// --- POST /automation/exec: refusal before any input ----------------------

func TestExecRejectsUnknownActionWithoutTouchingX(t *testing.T) {
	a := newAutomationAPI(testConfig())
	rec, doc := doJSON(t, a.handleExec, http.MethodPost, `{"steps":[{"action":"nope"}]}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (%s)", rec.Code, rec.Body.String())
	}
	if doc["code"] != codeUnknownAction {
		t.Errorf("code = %v, want %q", doc["code"], codeUnknownAction)
	}
	if strings.Contains(rec.Body.String(), "window") {
		t.Errorf("the failure should be about the action, not the target: %s", rec.Body.String())
	}
}

func TestExecRejectsMalformedRequests(t *testing.T) {
	a := newAutomationAPI(testConfig())
	cases := []struct{ body, want string }{
		{`{`, codeBadRequest},
		{`{"steps":[]}`, codeBadRequest},
		{`{"steps":[{"action":"wait_until"}]}`, codeBadRequest},
		{`{"steps":[{"action":"scroll","direction":"sideways"}]}`, codeBadRequest},
		{`{"steps":[{"action":"window","mode":"teleport"}]}`, codeBadRequest},
		{`{"steps":[{"action":"click","x":1,"y":1}],"timeout":"soon"}`, codeBadRequest},
	}
	for _, c := range cases {
		rec, doc := doJSON(t, a.handleExec, http.MethodPost, c.body)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("body %s: status %d, want 400", c.body, rec.Code)
		}
		if doc["code"] != c.want {
			t.Errorf("body %s: code %v, want %q", c.body, doc["code"], c.want)
		}
		if doc["status"] != "error" {
			t.Errorf("body %s: envelope %v", c.body, doc["status"])
		}
	}

	rec, _ := doJSON(t, a.handleExec, http.MethodGet, "")
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET /automation/exec = %d, want 405", rec.Code)
	}
}

// TestExecDenyAPIInput is the whole point of the config key: a read-only batch is
// allowed, the moment a step would move the pointer it is 403 — and the refusal
// happens before the target is resolved, so it works with no window at all.
func TestExecDenyAPIInput(t *testing.T) {
	cfg := testConfig()
	cfg.DenyAPIInput = true
	a := newAutomationAPI(cfg)

	rec, doc := doJSON(t, a.handleExec, http.MethodPost, `{"steps":[{"action":"click","x":1,"y":1}]}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (%s)", rec.Code, rec.Body.String())
	}
	if doc["code"] != codeInputDisabled {
		t.Errorf("code = %v, want %q", doc["code"], codeInputDisabled)
	}

	// Input nested inside control flow is still input.
	rec, _ = doJSON(t, a.handleExec, http.MethodPost, `{"steps":[{"action":"loop","count":1,"steps":[{"action":"key","keys":"ctrl-c"}]}]}`)
	if rec.Code != http.StatusForbidden {
		t.Errorf("nested input escaped deny_api_input: %d", rec.Code)
	}

	// A read-only batch gets past the guard: on a machine with no display the next
	// gate it hits is the connection, which proves the guard let it through.
	rec, doc = doJSON(t, a.handleExec, http.MethodPost, `{"steps":[{"action":"screenshot"}]}`)
	if rec.Code == http.StatusForbidden {
		t.Errorf("a read-only batch was refused by deny_api_input: %s", rec.Body.String())
	}
	if doc["code"] == codeInputDisabled {
		t.Errorf("read-only step blocked anyway: %v", doc)
	}
}

// TestHeadlessExecReportsNoDisplay pins the failure an agent sees when the daemon
// has no X session: a code it can act on, not a 200 with a nil body.
func TestHeadlessExecReportsNoDisplay(t *testing.T) {
	if os.Getenv("DISPLAY") != "" {
		t.Skip("needs a machine with no X display (an agent on a Wayland session)")
	}
	a := newAutomationAPI(testConfig())
	rec, doc := doJSON(t, a.handleExec, http.MethodPost, `{"steps":[{"action":"screenshot"}]}`)
	if doc["code"] != codeNoDisplay {
		t.Errorf("code = %v, want %q (status %d)", doc["code"], codeNoDisplay, rec.Code)
	}
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", rec.Code)
	}

	// The read endpoints must answer the same way rather than 500 out of XGB.
	for _, path := range []string{"/automation/windows", "/automation/state", "/automation/window?id=1"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		a.handleWindows(rec, req)
		if rec.Code != http.StatusServiceUnavailable && rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status %d", path, rec.Code)
		}
	}
}

// --- the input gate -------------------------------------------------------

// TestGateSerialisesRuns is the plan's core concurrency claim: two simultaneous
// runs must never interleave synthetic pointer events, a queued caller is told to
// go away when its own deadline expires, and the queue is first-come-first-served.
func TestGateSerialisesRuns(t *testing.T) {
	a := newAutomationAPI(testConfig())

	// gate() queues rather than failing fast — that is the documented behaviour —
	// so every "must be refused" probe below carries a short deadline.
	budget := func() context.Context {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Millisecond)
		t.Cleanup(cancel)
		return ctx
	}

	held, apiErr := a.gate(context.Background())
	if apiErr != nil {
		t.Fatalf("acquiring a free gate: %v", apiErr)
	}

	_, apiErr = a.gate(budget())
	if apiErr == nil {
		t.Fatal("a second holder got in while the first was inside")
	}
	if apiErr.Code != codeBusy || apiErr.Status != http.StatusTooManyRequests {
		t.Errorf("a queued-out caller got %s/%d, want %s/429", apiErr.Code, apiErr.Status, codeBusy)
	}

	// Releasing hands it to the next waiter, not to nobody.
	held()
	next, apiErr := a.gate(context.Background())
	if apiErr != nil {
		t.Fatalf("the gate stayed wedged after a timed-out waiter: %v", apiErr)
	}
	next()

	// FIFO: the waiter that arrived first is served first. With sync.Mutex the order
	// is unspecified, which is exactly why the gate is a capacity-1 channel.
	held, apiErr = a.gate(context.Background())
	if apiErr != nil {
		t.Fatalf("re-acquire: %v", apiErr)
	}
	order := make(chan string, 4)
	acquire := func(name string) {
		release, err := a.gate(context.Background())
		if err != nil {
			order <- name + "-err"
			return
		}
		order <- name
		time.Sleep(30 * time.Millisecond) // hold it long enough for the order to matter
		release()
	}
	go acquire("A")
	time.Sleep(60 * time.Millisecond) // make sure A is queued before B even starts
	go acquire("B")
	time.Sleep(60 * time.Millisecond)
	held()

	var got []string
	deadline := time.After(3 * time.Second)
	for len(got) < 2 {
		select {
		case v := <-order:
			got = append(got, v)
		case <-deadline:
			t.Fatalf("only %v of the two queued waiters were served", got)
		}
	}
	if got[0] != "A" || got[1] != "B" {
		t.Errorf("waiters were served %v, want [A B]", got)
	}

	// And the gate is usable again afterwards.
	cleared, apiErr := a.gate(context.Background())
	if apiErr != nil {
		t.Fatalf("gate exhausted after the queue drained: %v", apiErr)
	}
	cleared()
}

// --- error mapping --------------------------------------------------------

func TestRunErrorToAPIStatuses(t *testing.T) {
	cases := []struct {
		code        string
		wantStatus  int
		wantAPICode string
	}{
		{automation.CodeWindowGone, http.StatusConflict, codeWindowGone},
		{automation.CodeTimeout, http.StatusGatewayTimeout, codeTimeout},
		{automation.CodeAborted, http.StatusOK, codeAborted},
		{automation.CodeWindowMissing, http.StatusNotFound, codeWindowNotFound},
		{automation.CodeDanglingGoto, http.StatusBadRequest, codeDanglingGoto},
		{automation.CodeXError, http.StatusInternalServerError, codeXError},
		{automation.CodeStepFailed, http.StatusInternalServerError, codeStepFailed},
	}
	for _, c := range cases {
		err := &automation.RunError{Code: c.code, Message: "because", StepIndex: 2}
		apiErr := runErrorToAPI(err)
		if apiErr.Status != c.wantStatus || apiErr.Code != c.wantAPICode {
			t.Errorf("%s → %s/%d, want %s/%d", c.code, apiErr.Code, apiErr.Status, c.wantAPICode, c.wantStatus)
		}
	}
}

func TestTimeoutCeilingIsTheConfig(t *testing.T) {
	a := newAutomationAPI(testConfig())

	got, apiErr := a.runTimeout("")
	if apiErr != nil || got != defaultExecTimeout {
		t.Errorf("default = %v (%v), want %v", got, apiErr, defaultExecTimeout)
	}
	got, _ = a.runTimeout("5s")
	if got != 5*time.Second {
		t.Errorf("5s → %v", got)
	}
	// The whole reason the ceiling exists: a run may not outlive api_max_run_duration_seconds.
	got, _ = a.runTimeout("1h")
	if got != time.Duration(a.cfg.APIMaxRunDurationSeconds)*time.Second {
		t.Errorf("1h → %v, want the configured ceiling", got)
	}
	if _, apiErr := a.runTimeout("0s"); apiErr == nil {
		t.Error("a zero timeout must be refused, not turned into an instant kill")
	}
	if _, apiErr := a.runTimeout("-5s"); apiErr == nil {
		t.Error("a negative timeout must be refused")
	}
}

// --- target selector ------------------------------------------------------

func TestSelectorValidation(t *testing.T) {
	// window_id must parse; a name is only allowed in `window`.
	sel := TargetSelector{WindowID: "not-a-window"}
	_, apiErr := sel.resolve(nil)
	if apiErr == nil || apiErr.Code != codeBadRequest {
		t.Errorf("bad window_id: %#v", apiErr)
	}
	sel = TargetSelector{Window: "0xzz"}
	_, apiErr = sel.resolve(nil)
	if apiErr == nil || apiErr.Code != codeWindowNotFound {
		t.Errorf("unfindable window name: %#v", apiErr)
	}
}

func TestRoutesAreRegisteredOnce(t *testing.T) {
	cfg := testConfig()
	mux := http.NewServeMux()
	newAutomationAPI(cfg).register(mux)

	for _, path := range []string{
		"/automation/actions", "/automation/exec", "/automation/screenshot",
		"/automation/windows", "/automation/window", "/automation/state", "/automation/abort",
	} {
		rec := httptest.NewRecorder()
		// An unknown path reaches the mux's own 404 handler; a registered one answers
		// 405 for the wrong method or a real body otherwise.
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code == http.StatusNotFound {
			t.Errorf("%s is not registered", path)
		}
	}
}

// TestAbortNeedsNoGate is the deadlock guard: the request that kills a stuck run
// must never queue behind it.
func TestAbortDoesNotQueueBehindTheGate(t *testing.T) {
	a := newAutomationAPI(testConfig())
	release, apiErr := a.gate(context.Background())
	if apiErr != nil {
		t.Fatalf("acquire: %v", apiErr)
	}
	defer release()

	if os.Getenv("DISPLAY") != "" {
		t.Skip("aborts touch the pointer; needs a headless machine to test the shape")
	}
	rec, _ := doJSON(t, a.handleAbort, http.MethodPost, "")
	// Headless: no display, so 503 — but a *response*, not a hang behind the gate.
	if rec.Code == http.StatusOK {
		t.Error("an abort cannot succeed with no display")
	}
}
