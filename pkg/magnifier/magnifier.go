// Package magnifier implements a cross-DE fullscreen and lens magnifier for
// X11. It runs as a persistent background service, registering passive key
// and button grabs on the root window so it works regardless of which
// application has focus.
//
// Two modes are available:
//   - Fullscreen: the entire monitor is replaced with a zoomed view centred
//     on the cursor.
//   - Lens: a small loupe window follows the cursor, showing a magnified view
//     of the area under the pointer.
//
// Both modes share a single zoom level that is adjusted with Modifier+Scroll.
// Each mode is toggled independently via its own configurable hotkey, so both
// can coexist (e.g. lens always on, fullscreen toggled on demand).
package magnifier

import (
	"fmt"
	"image"
	"os"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"github.com/jezek/xgb/xproto"
	"github.com/jezek/xgbutil"
	"github.com/jezek/xgbutil/keybind"
)

// Service is the long-running magnifier daemon.
type Service struct {
	cfg Config

	// X connection used exclusively by the magnifier goroutine.
	xu      *xgbutil.XUtil
	rootWin xproto.Window

	// Parsed hotkey tokens.
	fsMod uint16
	fsKC  xproto.Keycode
	lnMod uint16
	lnKC  xproto.Keycode
	scMod uint16 // scroll modifier mask

	// Mode state (atomic to allow safe read from other goroutines).
	fsActive int32 // 1 = fullscreen mode on
	lnActive int32 // 1 = lens mode on

	// Shared zoom factor, protected by mu.
	mu         sync.Mutex
	zoomFactor float64

	// Active overlays (nil when the corresponding mode is off).
	fsOverlay *overlayWindow
	lnOverlay *overlayWindow

	// Capturer (SHM or XGetImage).
	cap capturer

	// Monitors detected via RandR.
	monitors []MonitorGeometry

	// Stop signals for the render goroutines.
	fsStop chan struct{}
	lnStop chan struct{}

	// Global stop for the event loop.
	stopCh chan struct{}

	// CaptureCurrentView returns a snapshot of what is currently displayed.
	// Protected by frameMu.
	frameMu      sync.Mutex
	currentFrame *image.RGBA
}

// NewService creates a Service with the given configuration.
// Call cfg.Normalize() before passing it in if loading from JSON.
func NewService(cfg Config) *Service {
	cfg.Normalize()
	return &Service{
		cfg:        cfg,
		zoomFactor: cfg.InitialZoom,
		stopCh:     make(chan struct{}),
		fsStop:     make(chan struct{}),
		lnStop:     make(chan struct{}),
	}
}

// Start connects to the X server, registers hotkeys, and enters the event
// loop. It blocks until Stop is called or an unrecoverable error occurs.
func (s *Service) Start() error {
	// Detect session type and warn about Wayland.
	if warn := detectSessionWarning(); warn != "" {
		fmt.Printf("[Magnifier] %s\n", warn)
		if os.Getenv("DISPLAY") == "" {
			return fmt.Errorf("magnifier: %s", warn)
		}
	}

	// Open a dedicated X connection (separate from service.go's connection).
	var err error
	s.xu, err = xgbutil.NewConnDisplay(s.cfg.Display)
	if err != nil {
		return fmt.Errorf("magnifier: X connect %q: %w", s.cfg.Display, err)
	}
	defer s.xu.Conn().Close()

	keybind.Initialize(s.xu)
	s.rootWin = s.xu.RootWin()

	// Detect monitors.
	s.monitors, _ = detectMonitors(s.xu)

	// Initialise capturer (tries MIT-SHM first, falls back to XGetImage).
	screen := s.xu.Screen()
	maxW := int(screen.WidthInPixels)
	maxH := int(screen.HeightInPixels)
	s.cap = newCapturer(s.xu, maxW, maxH)
	defer s.cap.close()

	// Parse hotkeys.
	if s.fsMod, s.fsKC, err = parseHotkey(s.xu, s.cfg.FullscreenHotkey); err != nil {
		fmt.Printf("[Magnifier] Warning: invalid fullscreen hotkey %q: %v\n",
			s.cfg.FullscreenHotkey, err)
	}
	if s.lnMod, s.lnKC, err = parseHotkey(s.xu, s.cfg.LensHotkey); err != nil {
		fmt.Printf("[Magnifier] Warning: invalid lens hotkey %q: %v\n",
			s.cfg.LensHotkey, err)
	}
	s.scMod = modifierMask(s.cfg.ScrollModifier)

	// Register passive grabs on root.
	if err := grabHotkey(s.xu, s.rootWin, s.fsMod, s.fsKC); err != nil {
		fmt.Printf("[Magnifier] Warning: could not grab fullscreen hotkey: %v\n", err)
	}
	if err := grabHotkey(s.xu, s.rootWin, s.lnMod, s.lnKC); err != nil {
		fmt.Printf("[Magnifier] Warning: could not grab lens hotkey: %v\n", err)
	}
	grabScrollButtons(s.xu, s.rootWin, s.scMod)

	defer func() {
		ungrabHotkey(s.xu, s.rootWin, s.fsMod, s.fsKC)
		ungrabHotkey(s.xu, s.rootWin, s.lnMod, s.lnKC)
		ungrabScrollButtons(s.xu, s.rootWin, s.scMod)
	}()

	fmt.Printf("[Magnifier] Started. Fullscreen=%s  Lens=%s  Scroll-Mod=%s+Wheel\n",
		s.cfg.FullscreenHotkey, s.cfg.LensHotkey, s.cfg.ScrollModifier)

	return s.eventLoop()
}

// Stop signals the event loop and render goroutines to exit.
func (s *Service) Stop() {
	select {
	case <-s.stopCh:
	default:
		close(s.stopCh)
	}
}

// CurrentMode returns a bitmask of active modes.
// Use atomic.LoadInt32(&s.fsActive) / lnActive directly for performance.
func (s *Service) CurrentMode() (fsOn, lnOn bool) {
	return atomic.LoadInt32(&s.fsActive) == 1, atomic.LoadInt32(&s.lnActive) == 1
}

// ZoomFactor returns the current zoom level (thread-safe).
func (s *Service) ZoomFactor() float64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.zoomFactor
}

// CaptureCurrentView returns a copy of the last rendered frame.
// Returns nil if no mode is currently active.
func (s *Service) CaptureCurrentView() *image.RGBA {
	s.frameMu.Lock()
	defer s.frameMu.Unlock()
	if s.currentFrame == nil {
		return nil
	}
	b := s.currentFrame.Bounds()
	out := image.NewRGBA(b)
	copy(out.Pix, s.currentFrame.Pix)
	return out
}

// ----- event loop -----

func (s *Service) eventLoop() error {
	conn := s.xu.Conn()
	for {
		// Poll for X events without blocking forever so we can honour stopCh.
		ev, xerr := conn.PollForEvent()
		if xerr != nil {
			// Ignore benign errors (e.g. grabs already taken by another client).
		}
		if ev == nil {
			select {
			case <-s.stopCh:
				s.teardown()
				return nil
			default:
				// No event ready: sleep briefly to avoid busy-wait.
				time.Sleep(2 * time.Millisecond)
				continue
			}
		}

		switch e := ev.(type) {
		case xproto.KeyPressEvent:
			s.handleKeyPress(e)
		case xproto.ButtonPressEvent:
			s.handleButtonPress(e)
		}
	}
}

func (s *Service) handleKeyPress(e xproto.KeyPressEvent) {
	switch {
	case matchesHotkey(e, s.fsMod, s.fsKC):
		if atomic.LoadInt32(&s.fsActive) == 1 {
			s.stopFullscreen()
		} else {
			s.startFullscreen()
		}
	case matchesHotkey(e, s.lnMod, s.lnKC):
		if atomic.LoadInt32(&s.lnActive) == 1 {
			s.stopLens()
		} else {
			s.startLens()
		}
	}
}

func (s *Service) handleButtonPress(e xproto.ButtonPressEvent) {
	fsOn, lnOn := s.CurrentMode()
	if !fsOn && !lnOn {
		// No active mode: replay the event so the target app receives it.
		xproto.AllowEvents(s.xu.Conn(), xproto.AllowReplayPointer, e.Time)
		return
	}
	if !matchesScrollButton(e, s.scMod) {
		xproto.AllowEvents(s.xu.Conn(), xproto.AllowReplayPointer, e.Time)
		return
	}
	s.mu.Lock()
	zoom := s.zoomFactor
	if e.Detail == 4 { // scroll up → zoom in
		zoom += s.cfg.ZoomStep
	} else { // scroll down → zoom out
		zoom -= s.cfg.ZoomStep
	}
	if zoom < s.cfg.ZoomMin {
		zoom = s.cfg.ZoomMin
	}
	if zoom > s.cfg.ZoomMax {
		zoom = s.cfg.ZoomMax
	}
	s.zoomFactor = zoom
	s.mu.Unlock()
	// Consume the event (do not replay to original window).
}

// ----- mode transitions -----

func (s *Service) startFullscreen() {
	cur := s.queryPointer()
	mon := monitorForPoint(s.monitors, cur.X, cur.Y)

	ov, err := createFullscreenOverlay(s.xu, mon)
	if err != nil {
		fmt.Printf("[Magnifier] Error creating fullscreen overlay: %v\n", err)
		return
	}
	s.fsOverlay = ov
	atomic.StoreInt32(&s.fsActive, 1)

	stopCh := make(chan struct{})
	s.fsStop = stopCh
	go s.fullscreenLoop(ov, mon, stopCh)
	fmt.Printf("[Magnifier] Fullscreen mode ON (zoom=%.1f×)\n", s.ZoomFactor())
}

func (s *Service) stopFullscreen() {
	atomic.StoreInt32(&s.fsActive, 0)
	select {
	case <-s.fsStop:
	default:
		close(s.fsStop)
	}
	if s.fsOverlay != nil {
		s.fsOverlay.destroy()
		s.fsOverlay = nil
	}
	fmt.Println("[Magnifier] Fullscreen mode OFF")
}

func (s *Service) startLens() {
	ov, err := createLensOverlay(s.xu, s.cfg.LensSize, s.cfg.LensShape)
	if err != nil {
		fmt.Printf("[Magnifier] Error creating lens overlay: %v\n", err)
		return
	}
	s.lnOverlay = ov
	atomic.StoreInt32(&s.lnActive, 1)

	stopCh := make(chan struct{})
	s.lnStop = stopCh
	go s.lensLoop(ov, stopCh)
	fmt.Printf("[Magnifier] Lens mode ON (zoom=%.1f×, shape=%s)\n",
		s.ZoomFactor(), s.cfg.LensShapeStr)
}

func (s *Service) stopLens() {
	atomic.StoreInt32(&s.lnActive, 0)
	select {
	case <-s.lnStop:
	default:
		close(s.lnStop)
	}
	if s.lnOverlay != nil {
		s.lnOverlay.destroy()
		s.lnOverlay = nil
	}
	fmt.Println("[Magnifier] Lens mode OFF")
}

func (s *Service) teardown() {
	s.stopFullscreen()
	s.stopLens()
}

// ----- render goroutines -----

const renderInterval = 16 * time.Millisecond // ~60fps target; actual depends on capture speed

func (s *Service) fullscreenLoop(ov *overlayWindow, mon MonitorGeometry, stop <-chan struct{}) {
	ticker := time.NewTicker(renderInterval)
	defer ticker.Stop()

	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
		}

		cur := s.queryPointer()
		s.mu.Lock()
		zoom := s.zoomFactor
		s.mu.Unlock()

		srcX, srcY, srcW, srcH := sourceViewport(cur.X, cur.Y, zoom, mon)
		srcImg, err := s.cap.capture(srcX, srcY, srcW, srcH)
		if err != nil {
			continue
		}

		scaled := scaleImage(srcImg, mon.W, mon.H, s.cfg.SmoothScaling)

		// Draw crosshair at the centre of the scaled image (= true cursor position).
		drawCrosshair(scaled, mon.W/2, mon.H/2)
		drawOSD(scaled, zoom)

		s.frameMu.Lock()
		s.currentFrame = scaled
		s.frameMu.Unlock()

		ov.blit(rgbaToBGRA(scaled))
	}
}

func (s *Service) lensLoop(ov *overlayWindow, stop <-chan struct{}) {
	ticker := time.NewTicker(renderInterval)
	defer ticker.Stop()

	screenW := int(s.xu.Screen().WidthInPixels)
	screenH := int(s.xu.Screen().HeightInPixels)

	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
		}

		cur := s.queryPointer()
		s.mu.Lock()
		zoom := s.zoomFactor
		s.mu.Unlock()

		// Source region: a square centred on cursor, scaled so it fills the lens.
		srcSize := int(float64(ov.width) / zoom)
		if srcSize < 1 {
			srcSize = 1
		}
		srcX := cur.X - srcSize/2
		srcY := cur.Y - srcSize/2
		srcX, srcY, srcSize, srcSize = clampRegion(srcX, srcY, srcSize, srcSize, screenW, screenH)

		srcImg, err := s.cap.capture(srcX, srcY, srcSize, srcSize)
		if err != nil {
			continue
		}

		scaled := scaleImage(srcImg, ov.width, ov.height, s.cfg.SmoothScaling)
		drawCrosshair(scaled, ov.width/2, ov.height/2)
		drawOSD(scaled, zoom)

		s.frameMu.Lock()
		s.currentFrame = scaled
		s.frameMu.Unlock()

		// Move the lens overlay to follow the cursor (this is cheap: 1 X request).
		ov.moveTo(cur.X, cur.Y)
		ov.blit(rgbaToBGRA(scaled))
	}
}

// ----- utilities -----

type point struct{ X, Y int }

func (s *Service) queryPointer() point {
	reply, err := xproto.QueryPointer(s.xu.Conn(), s.rootWin).Reply()
	if err != nil {
		return point{}
	}
	return point{int(reply.RootX), int(reply.RootY)}
}

// detectSessionWarning returns a non-empty string if the session type is
// likely to prevent the magnifier from working correctly.
func detectSessionWarning() string {
	wayland := os.Getenv("WAYLAND_DISPLAY")
	display := os.Getenv("DISPLAY")
	if wayland != "" && display == "" {
		return "Pure Wayland session detected — magnifier requires X11 or XWayland. Set DISPLAY to use."
	}
	if wayland != "" {
		return "XWayland session: hotkeys may conflict with compositor scroll bindings."
	}
	return ""
}

// ensure unsafe is used (avoids "imported and not used" if only used in capture.go).
var _ = unsafe.Sizeof(0)
