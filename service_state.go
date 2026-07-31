package main

import (
	"sync"
	"sync/atomic"

	"github.com/jezek/xgb/xproto"
	"github.com/jezek/xgbutil"

	"zen-cap/pkg/config"
	"zen-cap/pkg/recorder"
)

type MarkedArea struct {
	X        int
	Y        int
	Width    int
	Height   int
	WindowID uint32
	Type     string // "fullscreen", "region", "window"
}

type serviceState struct {
	cfg atomic.Pointer[config.Config]
	X   *xgbutil.XUtil

	recMu           sync.Mutex
	activeRec       *recorder.Recorder
	activeRecPath   string
	recordAudioOnly bool

	markedAreaMu sync.Mutex
	markedArea   MarkedArea

	activeBordersMu sync.Mutex
	activeBorders   []xproto.Window

	windowClassGrabMu      sync.Mutex
	windowClassGrabRunning bool

	colorPickerMu      sync.Mutex
	colorPickerRunning bool

	annotateMu      sync.Mutex
	annotateRunning bool

	ocrAutoMu      sync.Mutex
	ocrAutoRunning bool
	ocrAutoFPS     float64
	ocrAutoCancel  chan struct{}
}

func (s *serviceState) getCfg() *config.Config {
	if cfg := s.cfg.Load(); cfg != nil {
		return cfg
	}
	return config.DefaultConfig()
}

func (s *serviceState) setCfg(cfg *config.Config) {
	s.cfg.Store(cfg)
}
