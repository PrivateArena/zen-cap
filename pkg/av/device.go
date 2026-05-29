// [VERIFIED]
package av

import (
	"fmt"
	"strconv"

	"github.com/asticode/go-astiav"
)

type DeviceConfig struct {
	Display  string // e.g. ":0.0"
	X        int
	Y        int
	Width    int
	Height   int
	FPS      int
	WindowID uint32 // 0 for fullscreen/region, non-zero for specific window
}

type InputDevice struct {
	formatCtx *astiav.FormatContext
	decCtx    *astiav.CodecContext
	stream    *astiav.Stream
	packet    *astiav.Packet
	frame     *astiav.Frame
	streamIdx int
}

func OpenDevice(cfg DeviceConfig) (*InputDevice, error) {
	Init()

	inputFormat := astiav.FindInputFormat("xcbgrab")
	if inputFormat == nil {
		inputFormat = astiav.FindInputFormat("x11grab")
	}
	if inputFormat == nil {
		return nil, fmt.Errorf("neither xcbgrab nor x11grab input format found")
	}

	options := astiav.NewDictionary()
	defer options.Free()

	// Set resolution
	if cfg.Width > 0 && cfg.Height > 0 {
		options.Set("video_size", fmt.Sprintf("%dx%d", cfg.Width, cfg.Height), 0)
	}
	// Set framerate
	if cfg.FPS > 0 {
		options.Set("framerate", strconv.Itoa(cfg.FPS), 0)
	}

	// Set window ID or offsets
	if cfg.WindowID != 0 {
		// xcbgrab/x11grab supports window_id
		options.Set("window_id", fmt.Sprintf("0x%x", cfg.WindowID), 0)
	} else {
		if cfg.X >= 0 {
			options.Set("grab_x", strconv.Itoa(cfg.X), 0)
		}
		if cfg.Y >= 0 {
			options.Set("grab_y", strconv.Itoa(cfg.Y), 0)
		}
	}

	formatCtx := astiav.AllocFormatContext()
	if formatCtx == nil {
		return nil, fmt.Errorf("failed to allocate format context")
	}

	display := cfg.Display
	if display == "" {
		display = ":0.0"
	}

	// Open input device
	if err := formatCtx.OpenInput(display, inputFormat, options); err != nil {
		formatCtx.Free()
		return nil, fmt.Errorf("failed to open input device: %w", err)
	}

	// Retrieve stream info
	if err := formatCtx.FindStreamInfo(nil); err != nil {
		formatCtx.CloseInput()
		return nil, fmt.Errorf("failed to find stream info: %w", err)
	}

	// Find video stream
	var videoStream *astiav.Stream
	streamIdx := -1
	for idx, s := range formatCtx.Streams() {
		if s.CodecParameters().MediaType() == astiav.MediaTypeVideo {
			videoStream = s
			streamIdx = idx
			break
		}
	}

	if videoStream == nil {
		formatCtx.CloseInput()
		return nil, fmt.Errorf("no video stream found in input device")
	}

	// Find decoder
	decoder := astiav.FindDecoder(videoStream.CodecParameters().CodecID())
	if decoder == nil {
		formatCtx.CloseInput()
		return nil, fmt.Errorf("failed to find decoder for codec %v", videoStream.CodecParameters().CodecID())
	}

	decCtx := astiav.AllocCodecContext(decoder)
	if decCtx == nil {
		formatCtx.CloseInput()
		return nil, fmt.Errorf("failed to allocate codec context")
	}

	if err := decCtx.FromCodecParameters(videoStream.CodecParameters()); err != nil {
		decCtx.Free()
		formatCtx.CloseInput()
		return nil, fmt.Errorf("failed to load codec parameters: %w", err)
	}

	if err := decCtx.Open(decoder, nil); err != nil {
		decCtx.Free()
		formatCtx.CloseInput()
		return nil, fmt.Errorf("failed to open codec: %w", err)
	}

	packet := astiav.AllocPacket()
	if packet == nil {
		decCtx.Free()
		formatCtx.CloseInput()
		return nil, fmt.Errorf("failed to allocate packet")
	}

	frame := astiav.AllocFrame()
	if frame == nil {
		packet.Free()
		decCtx.Free()
		formatCtx.CloseInput()
		return nil, fmt.Errorf("failed to allocate frame")
	}

	return &InputDevice{
		formatCtx: formatCtx,
		decCtx:    decCtx,
		stream:    videoStream,
		packet:    packet,
		frame:     frame,
		streamIdx: streamIdx,
	}, nil
}

func (d *InputDevice) Close() {
	if d.frame != nil {
		d.frame.Free()
	}
	if d.packet != nil {
		d.packet.Free()
	}
	if d.decCtx != nil {
		d.decCtx.Free()
	}
	if d.formatCtx != nil {
		d.formatCtx.CloseInput()
	}
}

// ReadFrame reads a packet and decodes it, returning the decoded frame.
// The returned frame is owned by the device; subsequent calls will modify it.
func (d *InputDevice) ReadFrame() (*astiav.Frame, error) {
	for {
		d.packet.Unref()
		err := d.formatCtx.ReadFrame(d.packet)
		if err != nil {
			return nil, err
		}

		if d.packet.StreamIndex() != d.streamIdx {
			continue
		}

		if err := d.decCtx.SendPacket(d.packet); err != nil {
			return nil, fmt.Errorf("failed to send packet to decoder: %w", err)
		}

		d.frame.Unref()
		err = d.decCtx.ReceiveFrame(d.frame)
		if err == nil {
			return d.frame, nil
		}

		// Handle EAGAIN or EOF - read more packets
		if err == astiav.ErrEagain || err == astiav.ErrEof {
			continue
		}

		return nil, fmt.Errorf("decoder error: %w", err)
	}
}

func (d *InputDevice) Width() int {
	return d.decCtx.Width()
}

func (d *InputDevice) Height() int {
	return d.decCtx.Height()
}

func (d *InputDevice) PixelFormat() astiav.PixelFormat {
	return d.decCtx.PixelFormat()
}
