package recorder

import (
	"fmt"
	"sync"
	"time"

	"zen-cap/pkg/av"

	"github.com/asticode/go-astiav"
)

type RecorderConfig struct {
	Display    string
	X          int
	Y          int
	Width      int
	Height     int
	FPS        int
	OutputPath string
	Bitrate    int64
	WindowID   uint32
}

type Recorder struct {
	cfg       RecorderConfig
	stopChan  chan struct{}
	doneChan  chan struct{}
	recording bool
	mu        sync.Mutex
}

func NewRecorder(cfg RecorderConfig) *Recorder {
	if cfg.FPS <= 0 {
		cfg.FPS = 30
	}
	return &Recorder{
		cfg:      cfg,
		stopChan: make(chan struct{}),
		doneChan: make(chan struct{}),
	}
}

func (r *Recorder) Start() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.recording {
		return fmt.Errorf("recorder is already running")
	}

	r.recording = true
	r.stopChan = make(chan struct{})
	r.doneChan = make(chan struct{})

	go r.run()
	return nil
}

func (r *Recorder) Stop() error {
	r.mu.Lock()
	if !r.recording {
		r.mu.Unlock()
		return fmt.Errorf("recorder is not running")
	}
	r.mu.Unlock()

	close(r.stopChan)
	<-r.doneChan

	r.mu.Lock()
	r.recording = false
	r.mu.Unlock()

	return nil
}

func (r *Recorder) IsRecording() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.recording
}

func (r *Recorder) run() {
	defer close(r.doneChan)

	devCfg := av.DeviceConfig{
		Display:  r.cfg.Display,
		X:        r.cfg.X,
		Y:        r.cfg.Y,
		Width:    r.cfg.Width,
		Height:   r.cfg.Height,
		FPS:      r.cfg.FPS,
		WindowID: r.cfg.WindowID,
	}

	device, err := av.OpenDevice(devCfg)
	if err != nil {
		fmt.Printf("Recorder error: failed to open capture device: %v\n", err)
		return
	}
	defer device.Close()

	w := device.Width()
	h := device.Height()

	// FIX: Prime the device with one real frame before creating the scaler so
	// that device.PixelFormat() reflects the actual decoded frame format rather
	// than the codec-context guess. This is the root cause of pink video.
	firstFrame, err := device.ReadFrame()
	if err != nil {
		fmt.Printf("Recorder error: failed to read first frame: %v\n", err)
		return
	}

	// Snapshot the real pixel format now that we have a decoded frame.
	srcPixFmt := device.PixelFormat()
	fmt.Printf("Capture pixel format: %v\n", srcPixFmt)

	// Open Video Encoder
	encoder, err := av.NewVideoEncoder(w, h, r.cfg.FPS, r.cfg.Bitrate)
	if err != nil {
		fmt.Printf("Recorder error: failed to initialize encoder: %v\n", err)
		return
	}
	defer encoder.Close()

	// Open Scaler with the now-accurate source pixel format
	scaler, err := av.NewScaler(w, h, srcPixFmt, w, h, astiav.PixelFormatYuv420P)
	if err != nil {
		fmt.Printf("Recorder error: failed to initialize scaler: %v\n", err)
		return
	}
	defer scaler.Close()

	// Open Output Muxer
	muxer, err := av.NewMuxer(r.cfg.OutputPath, encoder.CodecContext())
	if err != nil {
		fmt.Printf("Recorder error: failed to initialize muxer: %v\n", err)
		return
	}
	defer muxer.Close()

	// Allocate reusable YUV420P destination frame
	yuvFrame := astiav.AllocFrame()
	defer yuvFrame.Free()
	yuvFrame.SetWidth(w)
	yuvFrame.SetHeight(h)
	yuvFrame.SetPixelFormat(astiav.PixelFormatYuv420P)
	if err := yuvFrame.AllocBuffer(0); err != nil {
		fmt.Printf("Recorder error: failed to allocate YUV frame buffer: %v\n", err)
		return
	}

	encodeFrame := func(srcFrame *astiav.Frame) error {
		if err := scaler.Scale(srcFrame, yuvFrame); err != nil {
			return fmt.Errorf("scale failed: %w", err)
		}
		return encoder.Encode(yuvFrame, func(pkt *astiav.Packet) error {
			return muxer.WritePacket(pkt, encoder.CodecContext().TimeBase())
		})
	}

	fmt.Printf("Recording started: %dx%d @ %d FPS -> %s\n", w, h, r.cfg.FPS, r.cfg.OutputPath)

	// Process the already-read first frame before entering the loop.
	if err := encodeFrame(firstFrame); err != nil {
		fmt.Printf("Recorder error: encoding first frame failed: %v\n", err)
		return
	}

	for {
		select {
		case <-r.stopChan:
			goto flush
		default:
		}

		srcFrame, err := device.ReadFrame()
		if err != nil {
			fmt.Printf("Recorder error: failed to read frame: %v\n", err)
			break
		}

		if err := encodeFrame(srcFrame); err != nil {
			fmt.Printf("Recorder error: %v\n", err)
			break
		}
	}

flush:
	fmt.Println("Flushing encoder and finishing file...")
	if err := encoder.Encode(nil, func(pkt *astiav.Packet) error {
		return muxer.WritePacket(pkt, encoder.CodecContext().TimeBase())
	}); err != nil {
		fmt.Printf("Recorder error: flushing encoder failed: %v\n", err)
	}

	// Give the muxer a moment to drain before Close() writes the trailer.
	time.Sleep(100 * time.Millisecond)
	fmt.Println("Recording finished.")
}
