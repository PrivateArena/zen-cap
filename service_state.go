package main

import (
	"sync"

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
	cfg *config.Config
	X   *xgbutil.XUtil

	recMu           sync.Mutex
	activeRec       *recorder.Recorder
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
