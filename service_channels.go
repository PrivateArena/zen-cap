package main

type serviceChannels struct {
	Screenshot           chan struct{}
	RegionScreenshot     chan struct{}
	WindowScreenshot     chan struct{}
	OCRScreenshot        chan struct{}
	OCRRegionScreenshot  chan struct{}
	OCRWindowScreenshot  chan struct{}
	OCRCycleModel        chan struct{}
	OCRAutoToggle        chan struct{}
	OCRAutoFPS           chan struct{}
	WindowClassGrab      chan struct{}
	ColorPicker          chan struct{}
	Record               chan struct{}
	RecordAnnotate       chan struct{}
	RecordMarkFullscreen chan struct{}
	RecordMarkRegion     chan struct{}
	RecordMarkWindow     chan struct{}
	RecordShowArea       chan struct{}
	RecordAudioOnly      chan struct{}
	SnippetCycleMode     chan struct{}
	TaskProfileCycle     chan struct{}
}

func newServiceChannels() *serviceChannels {
	return &serviceChannels{
		Screenshot:           make(chan struct{}, 1),
		RegionScreenshot:     make(chan struct{}, 1),
		WindowScreenshot:     make(chan struct{}, 1),
		OCRScreenshot:        make(chan struct{}, 1),
		OCRRegionScreenshot:  make(chan struct{}, 1),
		OCRWindowScreenshot:  make(chan struct{}, 1),
		OCRCycleModel:        make(chan struct{}, 1),
		OCRAutoToggle:        make(chan struct{}, 1),
		OCRAutoFPS:           make(chan struct{}, 1),
		WindowClassGrab:      make(chan struct{}, 1),
		ColorPicker:          make(chan struct{}, 1),
		Record:               make(chan struct{}, 1),
		RecordAnnotate:       make(chan struct{}, 1),
		RecordMarkFullscreen: make(chan struct{}, 1),
		RecordMarkRegion:     make(chan struct{}, 1),
		RecordMarkWindow:     make(chan struct{}, 1),
		RecordShowArea:       make(chan struct{}, 1),
		RecordAudioOnly:      make(chan struct{}, 1),
		SnippetCycleMode:     make(chan struct{}, 1),
		TaskProfileCycle:     make(chan struct{}, 1),
	}
}
