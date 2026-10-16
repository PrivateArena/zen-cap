package macro

import (
	"errors"
	"fmt"
	"time"

	"zen-cap/pkg/automation"
)

// ErrNoEvents is returned by Compile when the event stream is empty.
var ErrNoEvents = errors.New("macro: no input events recorded")

// buttonName maps X11 button numbers to the button strings understood by the
// automation engine's resolveButton ("left"/"middle"/"right"); any other
// number passes through numerically so exotic buttons stay reproducible.
func buttonName(btn byte) string {
	switch btn {
	case 1:
		return "left"
	case 2:
		return "middle"
	case 3:
		return "right"
	default:
		return fmt.Sprintf("%d", btn)
	}
}

// msString renders a millisecond count as a duration string parseable by
// time.ParseDuration (the format used by automation Step.Duration).
func msString(ms uint32) string {
	return (time.Duration(ms) * time.Millisecond).String()
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

func normalizeOptions(o CompileOptions) CompileOptions {
	d := DefaultCompileOptions()
	if o.WaitGapMs == 0 {
		o.WaitGapMs = d.WaitGapMs
	}
	if o.DragThresholdPx == 0 {
		o.DragThresholdPx = d.DragThresholdPx
	}
	if o.LongPressMs == 0 {
		o.LongPressMs = d.LongPressMs
	}
	if o.SampleIntervalMs == 0 {
		o.SampleIntervalMs = d.SampleIntervalMs
	}
	if o.SampleMinDistPx == 0 {
		o.SampleMinDistPx = d.SampleMinDistPx
	}
	return o
}

// pushWait inserts a wait step when the idle gap between the previous action
// and now is at or above WaitGapMs. It always advances the emission cursor so
// sub-threshold gaps are absorbed without accumulating.
func pushWait(steps *[]automation.Step, lastEmit *uint32, now uint32, opts CompileOptions) {
	if now > *lastEmit && now-*lastEmit >= opts.WaitGapMs {
		*steps = append(*steps, automation.Step{
			Action:   "wait",
			Duration: msString(now - *lastEmit),
		})
	}
	*lastEmit = now
}

// flushGesture emits the steps for one press->release gesture. Stationary
// gestures with a short dwell become a hold+release click pair (the gold
// standard: clicks are never emitted as the atomic "click" step); a long dwell
// becomes a long press (drag with coincident endpoints, which runDrag plays as
// a timed hold); displacement above DragThresholdPx becomes a single drag step
// from press to release position.
func flushGesture(steps *[]automation.Step, pressX, pressY int, pressTime uint32, btn byte, endX, endY int, endTime uint32, opts CompileOptions) {
	dwell := uint32(0)
	if endTime > pressTime {
		dwell = endTime - pressTime
	}
	btnStr := buttonName(btn)

	if abs(endX-pressX) < opts.DragThresholdPx && abs(endY-pressY) < opts.DragThresholdPx {
		if dwell >= opts.LongPressMs {
			*steps = append(*steps, automation.Step{
				Action:   "drag",
				X:        pressX,
				Y:        pressY,
				X2:       pressX,
				Y2:       pressY,
				Button:   btnStr,
				Duration: msString(dwell),
			})
			return
		}
		*steps = append(*steps,
			automation.Step{Action: "hold", X: pressX, Y: pressY, Button: btnStr},
			automation.Step{Action: "release", X: pressX, Y: pressY, Button: btnStr},
		)
		return
	}

	*steps = append(*steps, automation.Step{
		Action:   "drag",
		X:        pressX,
		Y:        pressY,
		X2:       endX,
		Y2:       endY,
		Button:   btnStr,
		Duration: msString(dwell),
	})
}

// Compile converts a recorded pointer-event stream into an automation.Script.
//
// Gestures (press -> release) become hold/release clicks, long presses, or
// drags; stationary movement is decimated into move steps; idle gaps of at
// least WaitGapMs become wait steps so replay timing stays faithful while
// output stays compact. If the stream ends with a button still held, the
// gesture is closed at the last known pointer position so the resulting script
// never begins with a stuck button.
func Compile(name string, events []Event, opts CompileOptions) (automation.Script, error) {
	opts = normalizeOptions(opts)
	if len(events) == 0 {
		return automation.Script{}, ErrNoEvents
	}

	steps := make([]automation.Step, 0, len(events)/2)
	lastEmit := events[0].TimeMs

	inGesture := false
	pressX, pressY := 0, 0
	pressTime := uint32(0)
	holdBtn := byte(0)

	lastMoveTime := uint32(0)
	lastMoveX, lastMoveY := -1, -1

	lastPosX, lastPosY := 0, 0
	lastTime := events[0].TimeMs

	for _, ev := range events {
		lastPosX, lastPosY, lastTime = ev.X, ev.Y, ev.TimeMs

		switch ev.Kind {
		case EventButtonPress:
			// A press while a button is already held (e.g. chorded buttons)
			// is ignored: the first button owns the gesture.
			if inGesture {
				continue
			}
			pushWait(&steps, &lastEmit, ev.TimeMs, opts)
			pressX, pressY, pressTime, holdBtn = ev.X, ev.Y, ev.TimeMs, ev.Button
			inGesture = true

		case EventButtonRelease:
			// Only a release of the held button closes the gesture; releases
			// of other buttons (chorded fingers) are ignored so they cannot
			// end the gesture at a spurious position.
			if !inGesture || ev.Button != holdBtn {
				continue
			}
			flushGesture(&steps, pressX, pressY, pressTime, holdBtn, ev.X, ev.Y, ev.TimeMs, opts)
			inGesture = false
			lastEmit = ev.TimeMs

		case EventMotion:
			if inGesture {
				// Motion during a gesture is absorbed into the drag duration;
				// the endpoint is the release position.
				continue
			}
			if lastMoveX >= 0 &&
				ev.TimeMs-lastMoveTime < opts.SampleIntervalMs &&
				abs(ev.X-lastMoveX) < opts.SampleMinDistPx &&
				abs(ev.Y-lastMoveY) < opts.SampleMinDistPx {
				continue
			}
			pushWait(&steps, &lastEmit, ev.TimeMs, opts)
			steps = append(steps, automation.Step{Action: "move", X: ev.X, Y: ev.Y})
			lastMoveTime, lastMoveX, lastMoveY = ev.TimeMs, ev.X, ev.Y
			lastEmit = ev.TimeMs
		}
	}

	if inGesture {
		flushGesture(&steps, pressX, pressY, pressTime, holdBtn, lastPosX, lastPosY, lastTime, opts)
	}

	return automation.Script{Name: name, Steps: steps}, nil
}
