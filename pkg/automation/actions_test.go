package automation

import (
	"fmt"
	"testing"
)

func TestRunLog(t *testing.T) {
	var loggedMessage string
	logger := func(format string, args ...interface{}) {
		loggedMessage = fmt.Sprintf(format, args...)
	}

	ctx := &ExecContext{
		Logger: logger,
	}

	step := Step{
		Action:  "log",
		Message: "Hello, testing log action!",
	}

	err := ExecuteStep(step, ctx)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	expected := "[Automation] Log: Hello, testing log action!"
	if loggedMessage != expected {
		t.Errorf("expected logged message %q, got %q", expected, loggedMessage)
	}

	// Test fallback to Text
	loggedMessage = ""
	stepFallback := Step{
		Action: "log",
		Text:   "Hello, text fallback!",
	}

	err = ExecuteStep(stepFallback, ctx)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	expectedFallback := "[Automation] Log: Hello, text fallback!"
	if loggedMessage != expectedFallback {
		t.Errorf("expected logged message %q, got %q", expectedFallback, loggedMessage)
	}
}
