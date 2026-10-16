package macro

import (
	"testing"
)

func TestCompileEmpty(t *testing.T) {
	_, err := Compile("x", nil, DefaultCompileOptions())
	if err != ErrNoEvents {
		t.Fatalf("expected ErrNoEvents, got %v", err)
	}
}

func TestCompileName(t *testing.T) {
	script, err := Compile("my-name", []Event{
		{Kind: EventButtonPress, X: 10, Y: 20, Button: 1, TimeMs: 1000},
		{Kind: EventButtonRelease, X: 10, Y: 20, Button: 1, TimeMs: 1010},
	}, DefaultCompileOptions())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if script.Name != "my-name" {
		t.Errorf("expected name 'my-name', got %q", script.Name)
	}
}

func TestCompileClick(t *testing.T) {
	script, err := Compile("click", []Event{
		{Kind: EventButtonPress, X: 10, Y: 20, Button: 1, TimeMs: 1000},
		{Kind: EventButtonRelease, X: 10, Y: 20, Button: 1, TimeMs: 1010},
	}, DefaultCompileOptions())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// A plain click must compile to hold+release, never an atomic "click".
	steps := script.Steps
	if len(steps) != 2 {
		t.Fatalf("expected 2 steps, got %d: %+v", len(steps), steps)
	}
	if steps[0].Action != "hold" || steps[0].X != 10 || steps[0].Y != 20 || steps[0].Button != "left" {
		t.Errorf("expected hold(10,20,left), got %+v", steps[0])
	}
	if steps[1].Action != "release" || steps[1].X != 10 || steps[1].Y != 20 || steps[1].Button != "left" {
		t.Errorf("expected release(10,20,left), got %+v", steps[1])
	}
}

func TestCompileDrag(t *testing.T) {
	script, err := Compile("drag", []Event{
		{Kind: EventButtonPress, X: 0, Y: 0, Button: 1, TimeMs: 1000},
		{Kind: EventButtonRelease, X: 100, Y: 100, Button: 1, TimeMs: 1350},
	}, DefaultCompileOptions())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	steps := script.Steps
	if len(steps) != 1 {
		t.Fatalf("expected 1 drag step, got %d: %+v", len(steps), steps)
	}
	s := steps[0]
	if s.Action != "drag" || s.X != 0 || s.Y != 0 || s.X2 != 100 || s.Y2 != 100 ||
		s.Button != "left" || s.Duration != "350ms" {
		t.Errorf("unexpected drag step: %+v", s)
	}
}

func TestCompileDragIgnoresMidGestureMotion(t *testing.T) {
	script, err := Compile("drag", []Event{
		{Kind: EventButtonPress, X: 0, Y: 0, Button: 1, TimeMs: 1000},
		{Kind: EventMotion, X: 50, Y: 50, TimeMs: 1100},
		{Kind: EventMotion, X: 90, Y: 90, TimeMs: 1200},
		{Kind: EventButtonRelease, X: 100, Y: 100, Button: 1, TimeMs: 1300},
	}, DefaultCompileOptions())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Motion during a gesture is absorbed: no move/wait steps, one drag.
	steps := script.Steps
	if len(steps) != 1 {
		t.Fatalf("expected 1 drag step, got %d: %+v", len(steps), steps)
	}
	if steps[0].Action != "drag" || steps[0].X2 != 100 || steps[0].Y2 != 100 || steps[0].Duration != "300ms" {
		t.Errorf("unexpected drag step: %+v", steps[0])
	}
}

func TestCompileLongPress(t *testing.T) {
	script, err := Compile("hold", []Event{
		{Kind: EventButtonPress, X: 5, Y: 5, Button: 1, TimeMs: 1000},
		{Kind: EventButtonRelease, X: 5, Y: 5, Button: 1, TimeMs: 1900},
	}, DefaultCompileOptions())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	steps := script.Steps
	if len(steps) != 1 {
		t.Fatalf("expected 1 long-press drag step, got %d: %+v", len(steps), steps)
	}
	s := steps[0]
	if s.Action != "drag" || s.X != 5 || s.Y != 5 || s.X2 != 5 || s.Y2 != 5 ||
		s.Button != "left" || s.Duration != "900ms" {
		t.Errorf("expected long-press drag with coincident endpoints, got %+v", s)
	}
}

func TestCompileWaitGap(t *testing.T) {
	script, err := Compile("gaps", []Event{
		{Kind: EventButtonPress, X: 10, Y: 20, Button: 1, TimeMs: 1000},
		{Kind: EventButtonRelease, X: 10, Y: 20, Button: 1, TimeMs: 1010},
		{Kind: EventButtonPress, X: 50, Y: 60, Button: 1, TimeMs: 1700},
		{Kind: EventButtonRelease, X: 50, Y: 60, Button: 1, TimeMs: 1710},
	}, DefaultCompileOptions())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	steps := script.Steps
	if len(steps) != 5 {
		t.Fatalf("expected 5 steps (hold,release,wait,hold,release), got %d: %+v", len(steps), steps)
	}
	if steps[2].Action != "wait" || steps[2].Duration != "690ms" {
		t.Errorf("expected wait 690ms between clicks, got %+v", steps[2])
	}
}

func TestCompileNoWaitBelowThreshold(t *testing.T) {
	script, err := Compile("fast", []Event{
		{Kind: EventButtonPress, X: 10, Y: 20, Button: 1, TimeMs: 1000},
		{Kind: EventButtonRelease, X: 10, Y: 20, Button: 1, TimeMs: 1010},
		{Kind: EventButtonPress, X: 50, Y: 60, Button: 1, TimeMs: 1210},
		{Kind: EventButtonRelease, X: 50, Y: 60, Button: 1, TimeMs: 1220},
	}, DefaultCompileOptions())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, s := range script.Steps {
		if s.Action == "wait" {
			t.Fatalf("expected no wait step for sub-threshold gap, got %+v", script.Steps)
		}
	}
}

func TestCompileGapThenMove(t *testing.T) {
	script, err := Compile("gapmove", []Event{
		{Kind: EventButtonPress, X: 10, Y: 20, Button: 1, TimeMs: 1000},
		{Kind: EventButtonRelease, X: 10, Y: 20, Button: 1, TimeMs: 1010},
		{Kind: EventMotion, X: 5, Y: 5, TimeMs: 3000},
	}, DefaultCompileOptions())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	steps := script.Steps
	if len(steps) != 4 {
		t.Fatalf("expected 4 steps (hold,release,wait,move), got %d: %+v", len(steps), steps)
	}
	if steps[2].Action != "wait" || steps[2].Duration != "1.99s" {
		t.Errorf("expected wait 1.99s before the move, got %+v", steps[2])
	}
	if steps[3].Action != "move" || steps[3].X != 5 || steps[3].Y != 5 {
		t.Errorf("expected move(5,5), got %+v", steps[3])
	}
}

func TestCompilePureMoveDecimation(t *testing.T) {
	script, err := Compile("moves", []Event{
		{Kind: EventMotion, X: 0, Y: 0, TimeMs: 1000},
		{Kind: EventMotion, X: 2, Y: 0, TimeMs: 1005},
		{Kind: EventMotion, X: 4, Y: 0, TimeMs: 1010},
		{Kind: EventMotion, X: 6, Y: 0, TimeMs: 1015},
		{Kind: EventMotion, X: 8, Y: 0, TimeMs: 1020},
		{Kind: EventMotion, X: 20, Y: 0, TimeMs: 1025},
	}, DefaultCompileOptions())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// With interval 15ms and min-dist 4px the kept samples are
	// (0,0),(4,0),(8,0),(20,0): 4 moves, no waits.
	var positions [][2]int
	for _, s := range script.Steps {
		if s.Action == "wait" {
			t.Fatalf("expected no wait steps during continuous motion, got %+v", script.Steps)
		}
		if s.Action != "move" {
			t.Fatalf("expected only move steps, got %q", s.Action)
		}
		positions = append(positions, [2]int{s.X.(int), s.Y.(int)})
	}
	want := [][2]int{{0, 0}, {4, 0}, {8, 0}, {20, 0}}
	if len(positions) != len(want) {
		t.Fatalf("expected %d kept moves, got %d: %v", len(want), len(positions), positions)
	}
	for i := range want {
		if positions[i] != want[i] {
			t.Errorf("move %d: expected %v, got %v", i, want[i], positions[i])
		}
	}
}

func TestCompileButtonMapping(t *testing.T) {
	cases := []struct {
		btn  byte
		want string
	}{
		{1, "left"}, {2, "middle"}, {3, "right"}, {8, "8"},
	}
	for _, c := range cases {
		if got := buttonName(c.btn); got != c.want {
			t.Errorf("buttonName(%d): expected %q, got %q", c.btn, c.want, got)
		}
	}
}

func TestCompileRightButtonClick(t *testing.T) {
	script, err := Compile("right", []Event{
		{Kind: EventButtonPress, X: 40, Y: 50, Button: 3, TimeMs: 1000},
		{Kind: EventButtonRelease, X: 40, Y: 50, Button: 3, TimeMs: 1010},
	}, DefaultCompileOptions())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(script.Steps) != 2 || script.Steps[0].Button != "right" {
		t.Fatalf("expected right-button hold/release, got %+v", script.Steps)
	}
}

func TestCompileUnclosedGestureDrag(t *testing.T) {
	script, err := Compile("open", []Event{
		{Kind: EventButtonPress, X: 30, Y: 40, Button: 1, TimeMs: 1000},
		{Kind: EventMotion, X: 50, Y: 60, TimeMs: 1100},
	}, DefaultCompileOptions())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Recording ended mid-gesture: close it at the last known position so the
	// script never begins with a stuck button.
	steps := script.Steps
	if len(steps) != 1 {
		t.Fatalf("expected 1 closing drag step, got %d: %+v", len(steps), steps)
	}
	if steps[0].Action != "drag" || steps[0].X != 30 || steps[0].Y != 40 ||
		steps[0].X2 != 50 || steps[0].Y2 != 60 || steps[0].Duration != "100ms" {
		t.Errorf("unexpected closing step: %+v", steps[0])
	}
}

func TestCompileUnclosedGestureStationaryClick(t *testing.T) {
	script, err := Compile("open", []Event{
		{Kind: EventButtonPress, X: 5, Y: 5, Button: 1, TimeMs: 1000},
		{Kind: EventMotion, X: 5, Y: 5, TimeMs: 1010},
	}, DefaultCompileOptions())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	steps := script.Steps
	if len(steps) != 2 {
		t.Fatalf("expected closing hold+release click, got %d: %+v", len(steps), steps)
	}
	if steps[0].Action != "hold" || steps[1].Action != "release" {
		t.Errorf("expected hold+release, got %+v", steps)
	}
}

func TestCompileChordIgnoresSecondButton(t *testing.T) {
	script, err := Compile("chord", []Event{
		{Kind: EventButtonPress, X: 10, Y: 20, Button: 1, TimeMs: 1000},
		{Kind: EventButtonPress, X: 30, Y: 40, Button: 3, TimeMs: 1005},
		{Kind: EventButtonRelease, X: 30, Y: 40, Button: 3, TimeMs: 1010},
		{Kind: EventButtonRelease, X: 10, Y: 20, Button: 1, TimeMs: 1015},
	}, DefaultCompileOptions())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	steps := script.Steps
	if len(steps) != 2 {
		t.Fatalf("expected a single left click, got %d steps: %+v", len(steps), steps)
	}
	for _, s := range steps {
		if s.Button != "left" {
			t.Errorf("expected only left button, got %+v", s)
		}
	}
	if steps[0].X != 10 || steps[0].Y != 20 {
		t.Errorf("expected press position (10,20), got %+v", steps[0])
	}
}

func TestCompileReleaseWithoutPress(t *testing.T) {
	script, err := Compile("orphan", []Event{
		{Kind: EventButtonRelease, X: 10, Y: 20, Button: 1, TimeMs: 1000},
	}, DefaultCompileOptions())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(script.Steps) != 0 {
		t.Fatalf("expected orphan release to be ignored, got %+v", script.Steps)
	}
}
