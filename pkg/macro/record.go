package macro

import (
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/jezek/xgb"
	"github.com/jezek/xgb/record"
	"github.com/jezek/xgb/xproto"
)

// Core event codes captured by the recorder. The RECORD range is configured
// as DeviceEvents 4..6 (ButtonPress, ButtonRelease, MotionNotify).
const (
	wireButtonPress   byte = 4
	wireButtonRelease byte = 5
	wireMotionNotify  byte = 6
)

// wireEventLen is the on-wire size of every core protocol event, which is what
// the RECORD extension delivers for each datum when no element headers are
// requested.
const wireEventLen = 32

// Recorder captures core pointer events through the X RECORD extension. It
// opens two dedicated connections: a control connection used to create and
// disable the context, and a data connection that streams the recorded events.
// Recording is non-intrusive: the server duplicates matching events to this
// client while delivering them normally to their real destination.
type Recorder struct {
	control *xgb.Conn
	data    *xgb.Conn
	ctx     record.Context

	running atomic.Bool
	done    chan struct{}
	wg      sync.WaitGroup

	events boundedEvents
}

// NewRecorder opens the control and data connections and allocates a RECORD
// context. It does not start capturing; call Start.
func NewRecorder(maxEvents int) (*Recorder, error) {
	control, err := xgb.NewConn()
	if err != nil {
		return nil, fmt.Errorf("macro: RECORD control connection: %w", err)
	}
	if err := record.Init(control); err != nil {
		control.Close()
		return nil, fmt.Errorf("macro: RECORD extension unavailable: %w", err)
	}

	data, err := xgb.NewConn()
	if err != nil {
		control.Close()
		return nil, fmt.Errorf("macro: RECORD data connection: %w", err)
	}
	if err := record.Init(data); err != nil {
		control.Close()
		data.Close()
		return nil, fmt.Errorf("macro: RECORD extension unavailable (data): %w", err)
	}

	ctx, err := record.NewContextId(control)
	if err != nil {
		control.Close()
		data.Close()
		return nil, fmt.Errorf("macro: allocate RECORD context: %w", err)
	}

	return &Recorder{
		control: control,
		data:    data,
		ctx:     ctx,
		events:  newBoundedEvents(maxEvents),
	}, nil
}

// Start arms the RECORD context and begins capturing device events from all
// clients. It is safe to call once per Recorder; a second call is a no-op.
func (r *Recorder) Start() error {
	if !r.running.CompareAndSwap(false, true) {
		return nil
	}

	// ElementHeader 0: rely on the per-event Time field carried by every core
	// event, so the datum stream is plain 32-byte core events with no
	// prepended 4-byte timestamp header.
	err := record.CreateContext(r.control, r.ctx, record.ElementHeader(0),
		1, 1,
		[]record.ClientSpec{record.ClientSpec(record.CsAllClients)},
		[]record.Range{{
			DeviceEvents: record.Range8{First: wireButtonPress, Last: wireMotionNotify},
		}},
	).Check()
	if err != nil {
		r.running.Store(false)
		return fmt.Errorf("macro: create RECORD context: %w", err)
	}

	r.done = make(chan struct{})
	r.wg.Add(1)
	go r.stream()
	return nil
}

// stream reads RECORD data batches until the context is disabled or the data
// connection is closed.
func (r *Recorder) stream() {
	defer r.wg.Done()
	for {
		reply, err := record.EnableContext(r.data, r.ctx).Reply()
		if err != nil {
			return
		}
		r.events.addAll(parseWireData(reply.Data))
		select {
		case <-r.done:
			return
		default:
		}
	}
}

// Stop disables the context, joins the streaming goroutine and returns every
// captured event. Calling Stop on an idle Recorder returns nil.
func (r *Recorder) Stop() []Event {
	if !r.running.CompareAndSwap(true, false) {
		return nil
	}

	_ = record.DisableContext(r.control, r.ctx).Check()
	close(r.done)
	r.control.Close()
	r.data.Close()
	r.wg.Wait()

	return r.events.drain()
}

// IsRunning reports whether the recorder is currently capturing.
func (r *Recorder) IsRunning() bool {
	return r.running.Load()
}

// parseWireData decodes a RECORD EnableContext data buffer into Events. With
// elementHeader 0 every datum is a raw 32-byte core protocol event whose first
// byte is the event code. Unrecognized codes are skipped defensively so a
// stray EndOfData element cannot corrupt the stream.
func parseWireData(buf []byte) []Event {
	var out []Event
	off := 0
	for off+wireEventLen <= len(buf) {
		raw := buf[off : off+wireEventLen]
		off += wireEventLen
		switch raw[0] {
		case wireButtonPress:
			ev := xproto.ButtonPressEventNew(raw).(xproto.ButtonPressEvent)
			out = append(out, Event{
				Kind:   EventButtonPress,
				X:      int(ev.RootX),
				Y:      int(ev.RootY),
				Button: byte(ev.Detail),
				TimeMs: uint32(ev.Time),
			})
		case wireButtonRelease:
			ev := xproto.ButtonReleaseEventNew(raw).(xproto.ButtonReleaseEvent)
			out = append(out, Event{
				Kind:   EventButtonRelease,
				X:      int(ev.RootX),
				Y:      int(ev.RootY),
				Button: byte(ev.Detail),
				TimeMs: uint32(ev.Time),
			})
		case wireMotionNotify:
			ev := xproto.MotionNotifyEventNew(raw).(xproto.MotionNotifyEvent)
			out = append(out, Event{
				Kind:   EventMotion,
				X:      int(ev.RootX),
				Y:      int(ev.RootY),
				TimeMs: uint32(ev.Time),
			})
		}
	}
	return out
}

// boundedEvents is the concurrency-safe event accumulator used by Recorder.
// It caps the number of kept events so a runaway recording session cannot grow
// without bound. A max <= 0 disables the cap.
type boundedEvents struct {
	mu  sync.Mutex
	evs []Event
	max int
}

func newBoundedEvents(max int) boundedEvents {
	return boundedEvents{evs: make([]Event, 0), max: max}
}

func (b *boundedEvents) addAll(in []Event) {
	if len(in) == 0 {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, e := range in {
		if b.max > 0 && len(b.evs) >= b.max {
			return
		}
		b.evs = append(b.evs, e)
	}
}

func (b *boundedEvents) drain() []Event {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := b.evs
	b.evs = nil
	return out
}
