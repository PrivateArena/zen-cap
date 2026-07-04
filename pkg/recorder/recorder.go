package recorder

import (
	"fmt"
	"sync"
	"time"
	"unsafe"

	"zen-cap/pkg/av"

	astiav "github.com/asticode/go-astiav"
)

const aacFrameSamples = 1024

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

	ScaleAlgo        string
	EncoderPreset    string
	EncoderCRF       string
	EncoderTune      string
	EncoderProfile   string
	EncoderPixFormat string

	AudioDevice     string
	AudioEnabled    bool
	AudioSampleRate int
	AudioChannels   int
	AudioBitrate    int64
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

	firstFrame, err := device.ReadFrame()
	if err != nil {
		fmt.Printf("Recorder error: failed to read first frame: %v\n", err)
		return
	}

	srcPixFmt := device.PixelFormat()
	fmt.Printf("Capture pixel format: %v\n", srcPixFmt)

	encOpts := &av.EncoderOptions{
		Preset:      r.cfg.EncoderPreset,
		CRF:         r.cfg.EncoderCRF,
		Tune:        r.cfg.EncoderTune,
		Profile:     r.cfg.EncoderProfile,
		PixelFormat: av.PixelFormatFromString(r.cfg.EncoderPixFormat),
	}
	encoder, err := av.NewVideoEncoder(w, h, r.cfg.FPS, r.cfg.Bitrate, encOpts)
	if err != nil {
		fmt.Printf("Recorder error: failed to initialize encoder: %v\n", err)
		return
	}
	defer encoder.Close()

	scaleAlgo := r.cfg.ScaleAlgo
	if scaleAlgo == "" {
		scaleAlgo = "lanczos"
	}
	scaler, err := av.NewScaler(w, h, srcPixFmt, w, h, astiav.PixelFormatYuv420P, scaleAlgo)
	if err != nil {
		fmt.Printf("Recorder error: failed to initialize scaler: %v\n", err)
		return
	}
	defer scaler.Close()

	muxer, err := av.NewMuxer(r.cfg.OutputPath)
	if err != nil {
		fmt.Printf("Recorder error: failed to initialize muxer: %v\n", err)
		return
	}
	defer muxer.Close()

	videoStreamIdx, err := muxer.AddStream(encoder.CodecContext())
	if err != nil {
		fmt.Printf("Recorder error: failed to add video stream: %v\n", err)
		return
	}

	// Allocate reusable YUV420P destination frame
	yuvFrame := astiav.AllocFrame()
	defer yuvFrame.Free()
	yuvFrame.SetWidth(w)
	yuvFrame.SetHeight(h)
	yuvFrame.SetPixelFormat(astiav.PixelFormatYuv420P)

	yuvFrame.SetColorRange(astiav.ColorRangeMpeg)
	yuvFrame.SetColorSpace(astiav.ColorSpaceBt709)

	if err := yuvFrame.AllocBuffer(0); err != nil {
		fmt.Printf("Recorder error: failed to allocate YUV frame buffer: %v\n", err)
		return
	}

	// Audio setup
	var audioDevice *av.AudioDevice
	var audioEncoder *av.AudioEncoder
	var audioStreamIdx int
	var audioFifo *astiav.AudioFifo
	var audioFifoFmt astiav.SampleFormat
	var audioFifoCh int
	var stopAudio chan struct{}
	var audioDone chan struct{}

	if r.cfg.AudioEnabled {
		sr := r.cfg.AudioSampleRate
		if sr <= 0 {
			sr = 48000
		}
		ch := r.cfg.AudioChannels
		if ch <= 0 {
			ch = 2
		}

		audioCfg := av.AudioDeviceConfig{
			Device:     r.cfg.AudioDevice,
			SampleRate: sr,
			Channels:   ch,
		}

		audioDevice, err = av.OpenAudioDevice(audioCfg)
		if err != nil {
			fmt.Printf("Recorder error: failed to open audio device: %v\n", err)
			return
		}

		abr := r.cfg.AudioBitrate
		if abr <= 0 {
			abr = 128000
		}
		audioEncoder, err = av.NewAudioEncoder(sr, ch, abr)
		if err != nil {
			audioDevice.Close()
			fmt.Printf("Recorder error: failed to initialize audio encoder: %v\n", err)
			return
		}

		audioStreamIdx, err = muxer.AddStream(audioEncoder.CodecContext())
		if err != nil {
			audioDevice.Close()
			audioEncoder.Close()
			fmt.Printf("Recorder error: failed to add audio stream: %v\n", err)
			return
		}

		// Detect actual decoder format from the codec context. Pulse
		// typically outputs S16LE interleaved, but the AAC encoder needs
		// FLTP planar — we'll convert in runAudio.
		audioFifoFmt = audioDevice.SampleFormat()
		audioFifoCh = audioDevice.Channels()
		fmt.Printf("Audio format: %v %dch @ %dHz\n", audioFifoFmt, audioFifoCh, audioDevice.SampleRate())

		// Allocate FIFO with the DEVICE's format (not encoder's) so the
		// raw pulse frames can be written directly. Conversion to encoder
		// format happens right before encoding.
		audioFifo = astiav.AllocAudioFifo(audioFifoFmt, audioFifoCh, aacFrameSamples*4)
		if audioFifo == nil {
			audioDevice.Close()
			audioEncoder.Close()
			fmt.Printf("Recorder error: failed to allocate audio FIFO\n")
			return
		}

		stopAudio = make(chan struct{})
		audioDone = make(chan struct{})
	}

	// Write header once all streams are added
	if err := muxer.WriteHeader(); err != nil {
		fmt.Printf("Recorder error: %v\n", err)
		return
	}

	encodeVideo := func(srcFrame *astiav.Frame) error {
		if err := scaler.Scale(srcFrame, yuvFrame); err != nil {
			return fmt.Errorf("scale failed: %w", err)
		}
		return encoder.Encode(yuvFrame, func(pkt *astiav.Packet) error {
			return muxer.WritePacket(pkt, videoStreamIdx, encoder.CodecContext().TimeBase())
		})
	}

	// Start audio capture goroutine
	if r.cfg.AudioEnabled && audioDevice != nil {
		go func() {
			defer close(audioDone)
			r.runAudio(audioDevice, audioEncoder, audioFifo, audioFifoFmt, audioFifoCh, audioStreamIdx, muxer, stopAudio)
		}()
	}

	fmt.Printf("Recording started: %dx%d @ %d FPS -> %s", w, h, r.cfg.FPS, r.cfg.OutputPath)
	if r.cfg.AudioEnabled {
		fmt.Printf(" (with audio)")
	}
	fmt.Println()

	if err := encodeVideo(firstFrame); err != nil {
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

		if err := encodeVideo(srcFrame); err != nil {
			fmt.Printf("Recorder error: %v\n", err)
			break
		}
	}

flush:
	if stopAudio != nil {
		close(stopAudio)
		<-audioDone
	}

	fmt.Println("Flushing video encoder...")
	if err := encoder.Encode(nil, func(pkt *astiav.Packet) error {
		return muxer.WritePacket(pkt, videoStreamIdx, encoder.CodecContext().TimeBase())
	}); err != nil {
		fmt.Printf("Recorder error: flushing video encoder failed: %v\n", err)
	}

	if audioEncoder != nil {
		fmt.Println("Flushing audio encoder...")
		r.flushAudioFifo(audioFifo, audioEncoder, audioFifoFmt, audioFifoCh, audioStreamIdx, muxer)
		if err := audioEncoder.Encode(nil, func(pkt *astiav.Packet) error {
			return muxer.WritePacket(pkt, audioStreamIdx, audioEncoder.CodecContext().TimeBase())
		}); err != nil {
			fmt.Printf("Recorder error: flushing audio encoder failed: %v\n", err)
		}
	}

	if audioFifo != nil {
		audioFifo.Free()
	}
	if audioEncoder != nil {
		audioEncoder.Close()
	}

	time.Sleep(100 * time.Millisecond)
	fmt.Println("Recording finished.")
}

func (r *Recorder) runAudio(device *av.AudioDevice, enc *av.AudioEncoder, fifo *astiav.AudioFifo, fifoFmt astiav.SampleFormat, fifoCh int, streamIdx int, muxer *av.Muxer, stop <-chan struct{}) {
	encSampleFmt := enc.CodecContext().SampleFormat()
	encSampleRate := enc.CodecContext().SampleRate()

	for {
		select {
		case <-stop:
			r.flushAudioFifo(fifo, enc, fifoFmt, fifoCh, streamIdx, muxer)
			return
		default:
		}

		frame, err := device.ReadFrame()
		if err != nil {
			fmt.Printf("Audio error: %v\n", err)
			return
		}

		// Write into FIFO (in device-native format)
		if _, err := fifo.Write(frame); err != nil {
			fmt.Printf("Audio error: failed to write to FIFO: %v\n", err)
			return
		}

		// Drain FIFO in AAC frame chunks
		for fifo.Size() >= aacFrameSamples {
			tmpFrame := astiav.AllocFrame()
			tmpFrame.SetSampleFormat(fifoFmt)
		tmpFrame.SetChannelLayout(chLayoutFromCount(fifoCh))
		tmpFrame.SetSampleRate(encSampleRate)
		tmpFrame.SetNbSamples(aacFrameSamples)
		if err := tmpFrame.AllocBuffer(0); err != nil {
				tmpFrame.Free()
				fmt.Printf("Audio error: failed to allocate tmp frame: %v\n", err)
				return
		}

		n, err := fifo.Read(tmpFrame)
		if err != nil || n <= 0 {
			tmpFrame.Free()
			break
		}

		// Convert device format → encoder format
		outFrame, err := convertAudioFrame(tmpFrame, encSampleFmt, encSampleRate)
			tmpFrame.Free()
			if err != nil {
				fmt.Printf("Audio error: format conversion failed: %v\n", err)
				return
			}

			if err := enc.Encode(outFrame, func(pkt *astiav.Packet) error {
				return muxer.WritePacket(pkt, streamIdx, enc.CodecContext().TimeBase())
			}); err != nil {
				outFrame.Free()
				fmt.Printf("Audio error: encode failed: %v\n", err)
				return
			}
			outFrame.Free()
		}
	}
}

func (r *Recorder) flushAudioFifo(fifo *astiav.AudioFifo, enc *av.AudioEncoder, fifoFmt astiav.SampleFormat, fifoCh int, streamIdx int, muxer *av.Muxer) {
	encSampleFmt := enc.CodecContext().SampleFormat()
	encSampleRate := enc.CodecContext().SampleRate()

	for fifo.Size() > 0 {
		nb := fifo.Size()
		if nb > aacFrameSamples {
			nb = aacFrameSamples
		}
		tmpFrame := astiav.AllocFrame()
		tmpFrame.SetSampleFormat(fifoFmt)
		tmpFrame.SetChannelLayout(chLayoutFromCount(fifoCh))
		tmpFrame.SetSampleRate(encSampleRate)
		tmpFrame.SetNbSamples(nb)
		if err := tmpFrame.AllocBuffer(0); err != nil {
			tmpFrame.Free()
			return
		}
		if _, err := fifo.Read(tmpFrame); err != nil {
			tmpFrame.Free()
			return
		}
		outFrame, err := convertAudioFrame(tmpFrame, encSampleFmt, encSampleRate)
		tmpFrame.Free()
		if err != nil {
			return
		}
		if err := enc.Encode(outFrame, func(pkt *astiav.Packet) error {
			return muxer.WritePacket(pkt, streamIdx, enc.CodecContext().TimeBase())
		}); err != nil {
			outFrame.Free()
			return
		}
		outFrame.Free()
	}
}

// convertAudioFrame converts src (device-native format) to dstFmt and returns
// a newly allocated frame. Handles S16LE interleaved → FLTP planar, and
// pass-through when formats match.
func convertAudioFrame(src *astiav.Frame, dstFmt astiav.SampleFormat, dstSampleRate int) (*astiav.Frame, error) {
	srcFmt := src.SampleFormat()
	srcCh := src.ChannelLayout().Channels()
	srcNb := src.NbSamples()
	srcRate := src.SampleRate()

	if srcFmt == dstFmt && srcRate == dstSampleRate {
		dst := src.Clone()
		return dst, nil
	}

	dst := astiav.AllocFrame()
	dst.SetSampleFormat(dstFmt)
	dst.SetChannelLayout(chLayoutFromCount(srcCh))
	dst.SetSampleRate(dstSampleRate)
	dst.SetNbSamples(srcNb)
	if err := dst.AllocBuffer(0); err != nil {
		dst.Free()
		return nil, fmt.Errorf("alloc dst frame: %w", err)
	}

	// Handle conversion: S16LE (interleaved, plane 0) → FLTP (planar)
	if srcFmt == astiav.SampleFormatS16 && dstFmt == astiav.SampleFormatFltp {
		srcBytes, err := src.Data().Bytes(0)
		if err != nil {
			dst.Free()
			return nil, fmt.Errorf("src bytes: %w", err)
		}
		if len(srcBytes) < srcNb*srcCh*2 {
			dst.Free()
			return nil, fmt.Errorf("unexpected src data size %d < %d", len(srcBytes), srcNb*srcCh*2)
		}

		planeBytes := srcNb * 4
		left := make([]byte, planeBytes)
		right := make([]byte, planeBytes)

		for i := 0; i < srcNb; i++ {
			off := i * 4
			l := float32(int16(srcBytes[off])|int16(srcBytes[off+1])<<8) / 32768.0
			r := float32(int16(srcBytes[off+2])|int16(srcBytes[off+3])<<8) / 32768.0

			*(*float32)(unsafe.Pointer(&left[i*4])) = l
			*(*float32)(unsafe.Pointer(&right[i*4])) = r
		}

		if err := dst.Data().SetBytes(left, 0); err != nil {
			dst.Free()
			return nil, fmt.Errorf("set dst plane 0: %w", err)
		}
		if err := dst.Data().SetBytes(right, 1); err != nil {
			dst.Free()
			return nil, fmt.Errorf("set dst plane 1: %w", err)
		}
		return dst, nil
	}

	dst.Free()
	return nil, fmt.Errorf("unsupported audio conversion: %v → %v", srcFmt, dstFmt)
}

func chLayoutFromCount(n int) astiav.ChannelLayout {
	if n == 1 {
		return astiav.ChannelLayoutMono
	}
	return astiav.ChannelLayoutStereo
}
