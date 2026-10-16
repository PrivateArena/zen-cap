package macro

import (
	"encoding/binary"
	"testing"
)

// wireEvent builds a 32-byte core event in the little-endian layout expected by
// xgb's event decoders. code is the event code (4/5/6), detail the button.
func wireEvent(code byte, detail byte, timeMs uint32, rootX, rootY int16) []byte {
	b := make([]byte, wireEventLen)
	b[0] = code
	b[1] = detail
	binary.LittleEndian.PutUint32(b[4:8], timeMs)
	binary.LittleEndian.PutUint16(b[20:22], uint16(rootX))
	binary.LittleEndian.PutUint16(b[22:24], uint16(rootY))
	b[30] = 1 // same screen
	return b
}

func TestParseWireDataButtonPress(t *testing.T) {
	buf := wireEvent(wireButtonPress, 1, 12345, 100, -200)
	evs := parseWireData(buf)
	if len(evs) != 1 {
		t.Fatalf("expected 1 event, got %d", len(evs))
	}
	e := evs[0]
	if e.Kind != EventButtonPress || e.X != 100 || e.Y != -200 ||
		e.Button != 1 || e.TimeMs != 12345 {
		t.Errorf("unexpected event: %+v", e)
	}
}

func TestParseWireDataReleaseAndMotion(t *testing.T) {
	buf := wireEvent(wireButtonRelease, 3, 5000, 10, 20)
	buf = append(buf, wireEvent(wireMotionNotify, 0, 5001, 30, 40)...)

	evs := parseWireData(buf)
	if len(evs) != 2 {
		t.Fatalf("expected 2 events, got %d", len(evs))
	}
	if evs[0].Kind != EventButtonRelease || evs[0].Button != 3 || evs[0].X != 10 {
		t.Errorf("unexpected release event: %+v", evs[0])
	}
	if evs[1].Kind != EventMotion || evs[1].X != 30 || evs[1].Y != 40 || evs[1].TimeMs != 5001 {
		t.Errorf("unexpected motion event: %+v", evs[1])
	}
}

func TestParseWireDataConsecutiveEvents(t *testing.T) {
	var buf []byte
	buf = append(buf, wireEvent(wireButtonPress, 1, 100, 1, 2)...)
	buf = append(buf, wireEvent(wireButtonRelease, 1, 110, 1, 2)...)
	buf = append(buf, wireEvent(wireButtonPress, 2, 200, 3, 4)...)

	evs := parseWireData(buf)
	if len(evs) != 3 {
		t.Fatalf("expected 3 events, got %d", len(evs))
	}
	if evs[0].Kind != EventButtonPress || evs[1].Kind != EventButtonRelease || evs[2].Kind != EventButtonPress {
		t.Errorf("unexpected kinds: %+v", evs)
	}
	if evs[1].Button != 1 || evs[2].Button != 2 {
		t.Errorf("unexpected buttons: %+v", evs)
	}
}

func TestParseWireDataUnknownCodeSkipped(t *testing.T) {
	buf := wireEvent(0x00, 0, 0, 0, 0) // e.g. EndOfData marker
	evs := parseWireData(buf)
	if len(evs) != 0 {
		t.Fatalf("expected unknown code to be skipped, got %d events", len(evs))
	}
}

func TestParseWireDataPartialTrailing(t *testing.T) {
	buf := wireEvent(wireButtonPress, 1, 42, 5, 5)
	buf = append(buf, make([]byte, 10)...) // truncated trailing datum
	evs := parseWireData(buf)
	if len(evs) != 1 {
		t.Fatalf("expected partial trailing data to be dropped, got %d events", len(evs))
	}
}

func TestBoundedEventsCap(t *testing.T) {
	b := newBoundedEvents(3)
	for i := 1; i <= 5; i++ {
		b.addAll([]Event{{Kind: EventMotion, X: i, TimeMs: uint32(i)}})
	}
	got := b.drain()
	if len(got) != 3 {
		t.Fatalf("expected cap of 3 events, got %d: %+v", len(got), got)
	}
	// drain resets the accumulator.
	if again := b.drain(); len(again) != 0 {
		t.Fatalf("expected drain to reset, got %d", len(again))
	}
}

func TestBoundedEventsBatchRespectsCap(t *testing.T) {
	b := newBoundedEvents(2)
	var batch []Event
	for i := 1; i <= 4; i++ {
		batch = append(batch, Event{Kind: EventMotion, X: i, TimeMs: uint32(i)})
	}
	b.addAll(batch)
	if got := b.drain(); len(got) != 2 {
		t.Fatalf("expected cap of 2 from a batch, got %d", len(got))
	}
}

func TestBoundedEventsNoCap(t *testing.T) {
	b := newBoundedEvents(0)
	for i := 1; i <= 100; i++ {
		b.addAll([]Event{{Kind: EventMotion, X: i, TimeMs: uint32(i)}})
	}
	if got := b.drain(); len(got) != 100 {
		t.Fatalf("expected no cap, got %d events", len(got))
	}
}
