package automation

import (
	"errors"
	"image"
	"strings"
	"testing"
	"time"

	"zen-cap/pkg/target"
)

// captureFailTarget is a mockTarget whose Screenshot always fails — the shape
// of a dead window or a vanished display, which is precisely what used to send
// the polling actions into an unbounded recapture loop.
type captureFailTarget struct {
	*mockTarget
	captures int
}

func newCaptureFailTarget() *captureFailTarget {
	return &captureFailTarget{mockTarget: newMockTarget(nil)}
}

func (m *captureFailTarget) Screenshot() (image.Image, error) {
	m.captures++
	return nil, errors.New("capture device gone")
}

func newPollCtx(t target.Target, logger func(string, ...interface{})) *ExecContext {
	if logger == nil {
		logger = func(string, ...interface{}) {}
	}
	return &ExecContext{
		Target:    t,
		AbortChan: make(chan struct{}),
		Logger:    logger,
		Variables: make(map[string]interface{}),
	}
}

func TestScreenPollerCaptureErrorsStillTimeout(t *testing.T) {
	mt := newCaptureFailTarget()
	ctx := newPollCtx(mt, nil)
	p := newScreenPoller(ctx, 300*time.Millisecond, 50*time.Millisecond)

	start := time.Now()
	var got outcome
	var err error
	for i := 0; i < 100; i++ {
		got, err = p.step("", func(image.Image, int, int) (bool, error) { return false, nil })
		if got == outTimeout || got == outCaptureFailed {
			break
		}
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	}
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != outCaptureFailed {
		t.Fatalf("expected outCaptureFailed, got %v", got)
	}
	if elapsed < 300*time.Millisecond || elapsed >= 600*time.Millisecond {
		t.Errorf("timeout not respected: elapsed %v, want >=300ms <600ms", elapsed)
	}
	// Bounded recapture: 300ms at 50ms cadence is ~7 attempts, never "many".
	if p.Captures() > 8 {
		t.Errorf("too many capture attempts: %d", p.Captures())
	}
	if mt.captures != p.Captures() {
		t.Errorf("poller capture counter %d != target capture count %d", p.Captures(), mt.captures)
	}
	if note := p.captureNote(); !strings.Contains(note, "capture device gone") {
		t.Errorf("captureNote should carry the capture error, got %q", note)
	}
}

func TestScreenPollerNoMatchTimesOut(t *testing.T) {
	ctx := newPollCtx(newMockTarget(nil), nil)
	p := newScreenPoller(ctx, 250*time.Millisecond, 50*time.Millisecond)

	start := time.Now()
	var got outcome
	for {
		var err error
		got, err = p.step("", func(image.Image, int, int) (bool, error) { return false, nil })
		if got != outMissed {
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			break
		}
	}
	elapsed := time.Since(start)

	if got != outTimeout {
		t.Fatalf("expected outTimeout, got %v", got)
	}
	if elapsed < 250*time.Millisecond || elapsed >= 550*time.Millisecond {
		t.Errorf("timeout not respected: elapsed %v", elapsed)
	}
	if p.captureNote() != "" {
		t.Errorf("no capture failure expected, got note %q", p.captureNote())
	}
}

func TestScreenPollerMatchAndRegion(t *testing.T) {
	ctx := newPollCtx(newMockTarget(nil), nil)
	p := newScreenPoller(ctx, 5*time.Second, 10*time.Millisecond)

	calls := 0
	var got outcome
	for {
		calls++
		var err error
		got, err = p.step("10,10,20,20", func(h image.Image, offX, offY int) (bool, error) {
			if offX != 10 || offY != 10 {
				t.Errorf("expected crop offset (10,10), got (%d,%d)", offX, offY)
			}
			if b := h.Bounds(); b.Dx() != 20 || b.Dy() != 20 {
				t.Errorf("expected 20x20 crop, got %v", b)
			}
			return calls == 3, nil
		})
		if got != outMissed {
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			break
		}
	}
	if got != outDone {
		t.Fatalf("expected outDone, got %v", got)
	}
	if calls != 3 {
		t.Errorf("expected 3 attempts, got %d", calls)
	}
}

func TestScreenPollerPropagatesHandlerError(t *testing.T) {
	// A hard error from the match handler (template decode, OCR transport) must
	// surface verbatim instead of being retried forever.
	ctx := newPollCtx(newMockTarget(nil), nil)
	p := newScreenPoller(ctx, 5*time.Second, 10*time.Millisecond)

	sentinel := errors.New("failed to open template image: nope")
	got, err := p.step("", func(image.Image, int, int) (bool, error) { return false, sentinel })
	if got != outDone {
		t.Errorf("expected outDone, got %v", got)
	}
	if err != sentinel {
		t.Errorf("expected sentinel error, got %v", err)
	}
}

func TestScreenPollerAbort(t *testing.T) {
	ctx := newPollCtx(newMockTarget(nil), nil)
	close(ctx.AbortChan)
	p := newScreenPoller(ctx, time.Hour, time.Second)

	if _, err := p.step("", func(image.Image, int, int) (bool, error) { return false, nil }); err == nil {
		t.Fatal("expected abort error")
	}

	// Abort during the inter-attempt sleep must not wait out the interval.
	ctx2 := newPollCtx(newMockTarget(nil), nil)
	p2 := newScreenPoller(ctx2, time.Hour, 30*time.Second)
	go func() {
		time.Sleep(30 * time.Millisecond)
		close(ctx2.AbortChan)
	}()
	start := time.Now()
	_, err := p2.step("", func(image.Image, int, int) (bool, error) { return false, nil })
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("abort did not interrupt the sleep: %v", elapsed)
	}
	if err == nil {
		t.Error("expected abort error from sleep")
	}
}

func TestScreenPollerTickIsBounded(t *testing.T) {
	ctx := newPollCtx(newMockTarget(nil), nil)
	p := newScreenPoller(ctx, 200*time.Millisecond, 50*time.Millisecond)

	probes := 0
	start := time.Now()
	var got outcome
	for {
		var err error
		got, err = p.tick(func() (bool, error) { probes++; return false, nil })
		if got != outMissed {
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			break
		}
	}
	if got != outTimeout {
		t.Fatalf("expected outTimeout, got %v", got)
	}
	if elapsed := time.Since(start); elapsed < 200*time.Millisecond || elapsed >= 500*time.Millisecond {
		t.Errorf("tick timeout not respected: elapsed %v", elapsed)
	}
	if probes < 3 {
		t.Errorf("expected several probes, got %d", probes)
	}
}
