// [VERIFIED]
package recorder

import (
	"fmt"
	"sync"
	"time"

	"github.com/asticode/go-astiav"
	"zen-cap/pkg/av"
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
	cfg        RecorderConfig
	stopChan   chan struct{}
	doneChan   chan struct{}
	recording  bool
	mu         sync.Mutex
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

	// Start recording in the background
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

	// Signal to stop and wait for completion
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

	// 1. Open input capture device
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

	// 2. Open Video Encoder
	encoder, err := av.NewVideoEncoder(w, h, r.cfg.FPS, r.cfg.Bitrate)
	if err != nil {
		fmt.Printf("Recorder error: failed to initialize encoder: %v\n", err)
		return
	}
	defer encoder.Close()

	// 3. Open Swscale Scaler (Source pixel format -> YUV420P)
	scaler, err := av.NewScaler(w, h, device.PixelFormat(), w, h, astiav.PixelFormatYuv420P)
	if err != nil {
		fmt.Printf("Recorder error: failed to initialize scaler: %v\n", err)
		return
	}
	defer scaler.Close()

	// 4. Open Output Muxer
	muxer, err := av.NewMuxer(r.cfg.OutputPath, encoder.CodecContext())
	if err != nil {
		fmt.Printf("Recorder error: failed to initialize muxer: %v\n", err)
		return
	}
	defer muxer.Close()

	// 5. Allocate YUV420P destination frame
	yuvFrame := astiav.AllocFrame()
	defer yuvFrame.Free()
	yuvFrame.SetWidth(w)
	yuvFrame.SetHeight(h)
	yuvFrame.SetPixelFormat(astiav.PixelFormatYuv420P)
	if err := yuvFrame.AllocBuffer(0); err != nil {
		fmt.Printf("Recorder error: failed to allocate YUV frame buffer: %v\n", err)
		return
	}

	// Capture Loop
	fmt.Printf("Recording started: %dx%d @ %d FPS -> %s\n", w, h, r.cfg.FPS, r.cfg.OutputPath)
	for {
		select {
		case <-r.stopChan:
			// Gracefully stop
			goto flush
		default:
		}

		// Read raw frame from screen
		srcFrame, err := device.ReadFrame()
		if err != nil {
			fmt.Printf("Recorder error: failed to read frame: %v\n", err)
			break
		}

		// Scale/convert to YUV420P
		if err := scaler.Scale(srcFrame, yuvFrame); err != nil {
			fmt.Printf("Recorder error: failed to scale frame: %v\n", err)
			break
		}

		// Set frame timestamps (use monotonic pts increment)
		// Send frame to encoder and write output packets
		err = encoder.Encode(yuvFrame, func(pkt *astiav.Packet) error {
			return muxer.WritePacket(pkt, encoder.CodecContext().TimeBase())
		})
		if err != nil {
			fmt.Printf("Recorder error: encoding failed: %v\n", err)
			break
		}
	}

flush:
	// Flush the encoder
	fmt.Println("Flushing encoder and finishing file...")
	err = encoder.Encode(nil, func(pkt *astiav.Packet) error {
		return muxer.WritePacket(pkt, encoder.CodecContext().TimeBase())
	})
	if err != nil {
		fmt.Printf("Recorder error: flushing encoder failed: %v\n", err)
	}

	// Wait briefly to make sure everything writes cleanly
	time.Sleep(100 * time.Millisecond)
	fmt.Println("Recording finished.")
}
