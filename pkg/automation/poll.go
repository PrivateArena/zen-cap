package automation

import (
	"fmt"
	"image"
	"time"
)

// Default polling cadences for every screen-polling action.
//
//	capturePollInterval — how often find_image / find_text / ocr / if_found grab
//	                      a fresh frame. Twice a second is enough for a UI to
//	                      settle without keeping FFmpeg spinning.
//	statePollInterval   — how often the capture-free probes (if_window) re-run.
//	defaultFindTimeout  — the timeout a step gets when it does not specify one.
const (
	capturePollInterval = 500 * time.Millisecond
	statePollInterval   = 100 * time.Millisecond
	defaultFindTimeout  = 10 * time.Second
)

// outcome is the result of a single polling attempt performed by screenPoller.
//
// The whole point of this type is that *every* code path through a poll loop —
// including "the screen capture blew up" — has to go through the deadline
// check. Before this helper existed, three of the four polling actions
// (find_image, find_text, ocr) answered a capture error with `continue`, which
// skipped their own `time.Now().After(deadline)` test and turned a dead window
// into an infinite FFmpeg recapture loop.
type outcome int

const (
	// outDone: the match callback reported a hit (or a hard, non-retryable
	// condition it turned into an error, in which case step also returns it).
	outDone outcome = iota
	// outMissed: no match this attempt; the caller should call step again.
	outMissed
	// outTimeout: the deadline passed with no match. Terminal.
	outTimeout
	// outCaptureFailed: the deadline passed while every capture attempt was
	// failing. Terminal, but distinguishable from outTimeout by callers that
	// want to report "the screen went away" rather than "the needle vanished".
	outCaptureFailed
)

func (o outcome) String() string {
	switch o {
	case outDone:
		return "done"
	case outMissed:
		return "missed"
	case outTimeout:
		return "timeout"
	case outCaptureFailed:
		return "capture-failed"
	}
	return fmt.Sprintf("outcome(%d)", int(o))
}

// ErrAborted is returned by every poll site when the run is cancelled.
var ErrAborted = fmt.Errorf("execution aborted by user")

// ErrNotFound is quoted by timeout messages when no attempt got far enough to
// produce a matcher error — e.g. when every capture failed.
var ErrNotFound = fmt.Errorf("no match before the deadline")

// screenPoller drives one bounded capture-and-match loop.
//
// It owns the cadence (interval), the deadline, the abort check, the optional
// region crop and the capture-error bookkeeping. It never loops itself: the
// caller drives it with `for { ... step() ... }` and keeps its own match
// handler, so each action's semantics (output images, then: click, best
// confidence reporting) stay where they belong.
type screenPoller struct {
	ctx      *ExecContext
	interval time.Duration
	deadline time.Time

	// lastCapErr holds the most recent Screenshot error, for terminal messages.
	lastCapErr string
	// captures counts Screenshot attempts, okCaptures the successful ones.
	captures   int
	okCaptures int
}

// newScreenPoller builds a poller bounded by timeout, retrying every interval.
// A non-positive interval falls back to 500ms so a misconfigured caller cannot
// busy-loop against the X server.
func newScreenPoller(ctx *ExecContext, timeout, interval time.Duration) *screenPoller {
	if interval <= 0 {
		interval = 500 * time.Millisecond
	}
	if timeout < 0 {
		timeout = 0
	}
	return &screenPoller{
		ctx:      ctx,
		interval: interval,
		deadline: time.Now().Add(timeout),
	}
}

// tick is the capture-free half of step: abort check, a caller-supplied probe,
// deadline handling and the inter-attempt sleep. Poll sites whose condition is
// not a screen capture (if_window) use it so they get the same bounded cadence
// as the capture sites instead of hand-rolling another deadline loop.
func (p *screenPoller) tick(probe func() (bool, error)) (outcome, error) {
	if err := p.checkAbort(); err != nil {
		return outTimeout, err
	}
	ok, err := probe()
	if err != nil {
		return outDone, err
	}
	if ok {
		return outDone, nil
	}
	if time.Now().After(p.deadline) {
		return outTimeout, nil
	}
	if err := p.pause(); err != nil {
		return outTimeout, err
	}
	return outMissed, nil
}

// Captures returns the number of Screenshot attempts made so far.
func (p *screenPoller) Captures() int { return p.captures }

// captureSucceeded reports whether at least one frame was grabbed, which is how
// callers tell "the window died" from "the needle isn't on screen".
func (p *screenPoller) captureSucceeded() bool { return p.okCaptures > 0 }

// step grabs one frame (cropping it to regionStr when set) and hands it to fn.
//
// fn returns (matched, err): a false/nil pair means "keep looking", an error is
// propagated verbatim (polling is not the place to swallow a decode or OCR
// transport failure), and true means "found".
//
// step performs exactly one capture. It always evaluates the deadline, even
// when the capture fails, and it is abort-aware in both the entry check and the
// inter-attempt sleep.
func (p *screenPoller) step(regionStr string, fn func(haystack image.Image, offX, offY int) (bool, error)) (outcome, error) {
	if err := p.checkAbort(); err != nil {
		return outTimeout, err
	}

	p.captures++
	haystack, capErr := p.ctx.Target.Screenshot()
	if capErr != nil {
		p.lastCapErr = capErr.Error()
		if p.ctx.Logger != nil {
			p.ctx.Logger("[Automation] Capture failed while polling: %v", capErr)
		}
		if time.Now().After(p.deadline) {
			if p.captureSucceeded() {
				return outTimeout, nil
			}
			return outCaptureFailed, nil
		}
		if err := p.pause(); err != nil {
			return outTimeout, err
		}
		return outMissed, nil
	}
	p.okCaptures++

	offX, offY := 0, 0
	if regionStr != "" {
		b := haystack.Bounds()
		rx, ry, rw, rh, err := ParseRegion(regionStr, b.Dx(), b.Dy())
		if err != nil {
			if p.ctx.Logger != nil {
				p.ctx.Logger("[Automation] Warning parsing region: %v", err)
			}
		} else {
			haystack, offX, offY = CropImage(haystack, rx, ry, rw, rh)
		}
	}

	matched, err := fn(haystack, offX, offY)
	if err != nil {
		return outDone, err
	}
	if matched {
		return outDone, nil
	}

	if time.Now().After(p.deadline) {
		return outTimeout, nil
	}
	if err := p.pause(); err != nil {
		return outTimeout, err
	}
	return outMissed, nil
}

// checkAbort is the non-blocking abort probe every poll iteration starts with.
func (p *screenPoller) checkAbort() error {
	select {
	case <-p.ctx.AbortChan:
		return ErrAborted
	default:
		return nil
	}
}

// pause waits one interval, waking early (with ErrAborted) when the run is
// cancelled, so a dead window never holds a script for the full interval.
func (p *screenPoller) pause() error {
	if p.interval <= 0 {
		return nil
	}
	t := time.NewTimer(p.interval)
	defer t.Stop()
	select {
	case <-p.ctx.AbortChan:
		return ErrAborted
	case <-t.C:
		return nil
	}
}

// captureNote explains, for inclusion in a "not found within timeout" error,
// how much of the polling window was spent failing to capture at all. It is
// empty when every capture succeeded, so a plain "the needle is not on screen"
// error keeps its old wording.
func (p *screenPoller) captureNote() string {
	if p.lastCapErr == "" {
		return ""
	}
	if !p.captureSucceeded() {
		return fmt.Sprintf(", and all %d screen capture attempt(s) failed (%s)", p.captures, p.lastCapErr)
	}
	return fmt.Sprintf(", after %d failed screen capture attempt(s) (last: %s)", p.captures-p.okCaptures, p.lastCapErr)
}

// Read-back budgets for window mutations. A WM acts within a frame or two; if
// it has not by now it is not going to, and the request was refused.
//
// The coverage fractions are what counts as "the window really did get
// maximized / fullscreened" when the WM chose not to record the state atom:
// maximized keeps a panel or a border, fullscreen does not.
const (
	windowStateTimeout    = 500 * time.Millisecond
	windowGeometryTimeout = time.Second

	maximizeCoverage   = 0.80
	fullscreenCoverage = 0.97
)
