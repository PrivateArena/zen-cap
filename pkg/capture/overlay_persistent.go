package capture

import (
	"errors"
	"image"
	"sync"

	"github.com/jezek/xgb/shape"
	"github.com/jezek/xgb/xproto"
)

// ErrOverlayClosed is returned when Update is called after Close.
var ErrOverlayClosed = errors.New("persistent overlay is closed")

// PersistentOverlay is a non-modal, override-redirect X11 window that shows
// rendered OCR frames for the realtime auto-loop. It owns no event loop, grabs
// no input, and sets an empty input shape so pointer events pass through to
// the application underneath (F14).
type PersistentOverlay struct {
	ow     *overlayWindow
	mu     sync.Mutex
	closed bool
}

// NewPersistentOverlay creates a mapped overlay sized to init at (winX, winY)
// and uploads init as the first frame.
func NewPersistentOverlay(init image.Image, winX, winY int) (*PersistentOverlay, error) {
	ow, err := createOverlayWindow(init, winX, winY, false)
	if err != nil {
		return nil, err
	}
	makeClickThrough(ow)
	return &PersistentOverlay{ow: ow}, nil
}

// makeClickThrough empties the window's input shape so clicks reach the window
// below. If the SHAPE extension is unavailable it degrades gracefully to a
// rectangular input window.
func makeClickThrough(ow *overlayWindow) {
	conn := ow.xu.Conn()
	if err := shape.Init(conn); err != nil {
		return
	}
	// An empty input mask (PixmapNone) makes the whole window click-through.
	_ = shape.MaskChecked(conn, shape.SoSet, shape.SkInput, ow.winID, 0, 0, xproto.PixmapNone).Check()
}

func (o *PersistentOverlay) Width() int  { return o.ow.w }
func (o *PersistentOverlay) Height() int { return o.ow.h }

// Update replaces the window content with rgba and copies it to the screen.
// It is safe to call concurrently with Close.
func (o *PersistentOverlay) Update(rgba *image.RGBA) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.closed {
		return ErrOverlayClosed
	}
	bgra := imageToBGRA(rgba)
	if err := uploadImageChunked(o.ow.xu, xproto.Drawable(o.ow.pixmapID), o.ow.gcID, o.ow.xu.Screen().RootDepth, o.ow.w, o.ow.h, bgra); err != nil {
		return err
	}
	xproto.CopyArea(o.ow.xu.Conn(), xproto.Drawable(o.ow.pixmapID), xproto.Drawable(o.ow.winID), o.ow.gcID, 0, 0, 0, 0, uint16(o.ow.w), uint16(o.ow.h))
	return nil
}

// Close destroys the window and its resources. It is idempotent.
func (o *PersistentOverlay) Close() error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.closed {
		return nil
	}
	o.closed = true
	o.ow.destroy()
	return nil
}
