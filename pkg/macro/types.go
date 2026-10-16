// Package macro implements a non-intrusive X11 macro recorder. It captures
// pointer events via the X RECORD extension (a server-side wiretap that does
// not interfere with the applications under the pointer), then compiles the
// captured stream into an automation.Script whose steps reuse the existing
// hold / release / drag primitives of the automation engine.
package macro

// EventKind distinguishes the X11 input events captured by the recorder.
type EventKind int

const (
	// EventMotion is a pointer move with no button held.
	EventMotion EventKind = iota
	// EventButtonPress is a pointer button going down.
	EventButtonPress
	// EventButtonRelease is a pointer button coming up.
	EventButtonRelease
)

// Event is a single timestamped pointer event captured during recording.
// TimeMs is the X11 server time (milliseconds since server start) reported by
// the core event itself, which is monotonic within a server session.
type Event struct {
	Kind   EventKind
	X, Y   int
	Button byte // X11 button number: 1=left, 2=middle, 3=right
	TimeMs uint32
}

// CompileOptions controls script-generation thresholds and hard caps.
// The zero value is not usable; use DefaultCompileOptions.
type CompileOptions struct {
	// WaitGapMs is the minimum idle gap between actions that becomes a wait
	// step. Shorter gaps are considered part of a continuous action.
	WaitGapMs uint32
	// DragThresholdPx is the minimum displacement between press and release
	// that classifies a gesture as a drag instead of a click.
	DragThresholdPx int
	// LongPressMs is the minimum dwell (press->release) for a stationary
	// gesture to be emitted as a long press instead of an instant click.
	LongPressMs uint32
	// SampleIntervalMs is the motion decimation interval for stationary
	// pointer movement (no button held).
	SampleIntervalMs uint32
	// SampleMinDistPx is the minimum pointer distance that forces a motion
	// sample to be kept, regardless of the time interval.
	SampleMinDistPx int
	// MaxEvents is the hard cap of raw events kept while recording. Events
	// beyond the cap are dropped to bound memory on runaway sessions.
	MaxEvents int
}

// DefaultCompileOptions returns the thresholds used for macro generation.
func DefaultCompileOptions() CompileOptions {
	return CompileOptions{
		WaitGapMs:        400,
		DragThresholdPx:  8,
		LongPressMs:      500,
		SampleIntervalMs: 15,
		SampleMinDistPx:  4,
		MaxEvents:        50000,
	}
}
