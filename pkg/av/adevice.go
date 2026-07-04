package av

import (
	"fmt"
	"strconv"

	"github.com/asticode/go-astiav"
)

type AudioDeviceConfig struct {
	Device     string
	SampleRate int
	Channels   int
}

type AudioDevice struct {
	formatCtx *astiav.FormatContext
	decCtx    *astiav.CodecContext
	stream    *astiav.Stream
	packet    *astiav.Packet
	frame     *astiav.Frame
	streamIdx int
}

func OpenAudioDevice(cfg AudioDeviceConfig) (*AudioDevice, error) {
	Init()

	inputFormat := astiav.FindInputFormat("alsa")
	if inputFormat == nil {
		return nil, fmt.Errorf("ALSA audio input format not found (ffmpeg built without ALSA)")
	}

	options := astiav.NewDictionary()
	defer options.Free()

	if cfg.Device != "" {
		options.Set("device", cfg.Device, 0)
	}
	if cfg.SampleRate > 0 {
		options.Set("sample_rate", strconv.Itoa(cfg.SampleRate), 0)
	}
	if cfg.Channels > 0 {
		options.Set("channels", strconv.Itoa(cfg.Channels), 0)
	}

	formatCtx := astiav.AllocFormatContext()
	if formatCtx == nil {
		return nil, fmt.Errorf("failed to allocate format context")
	}

	if err := formatCtx.OpenInput(cfg.Device, inputFormat, options); err != nil {
		formatCtx.Free()
		return nil, fmt.Errorf("failed to open audio input device: %w", err)
	}

	var audioStream *astiav.Stream
	streamIdx := -1
	for idx, s := range formatCtx.Streams() {
		if s.CodecParameters().MediaType() == astiav.MediaTypeAudio {
			audioStream = s
			streamIdx = idx
			break
		}
	}
	if audioStream == nil {
		formatCtx.CloseInput()
		return nil, fmt.Errorf("no audio stream found in input device")
	}

	decoder := astiav.FindDecoder(audioStream.CodecParameters().CodecID())
	if decoder == nil {
		formatCtx.CloseInput()
		return nil, fmt.Errorf("failed to find decoder for audio codec %v", audioStream.CodecParameters().CodecID())
	}

	decCtx := astiav.AllocCodecContext(decoder)
	if decCtx == nil {
		formatCtx.CloseInput()
		return nil, fmt.Errorf("failed to allocate audio codec context")
	}
	if err := decCtx.FromCodecParameters(audioStream.CodecParameters()); err != nil {
		decCtx.Free()
		formatCtx.CloseInput()
		return nil, fmt.Errorf("failed to load audio codec parameters: %w", err)
	}
	if err := decCtx.Open(decoder, nil); err != nil {
		decCtx.Free()
		formatCtx.CloseInput()
		return nil, fmt.Errorf("failed to open audio codec: %w", err)
	}

	packet := astiav.AllocPacket()
	if packet == nil {
		decCtx.Free()
		formatCtx.CloseInput()
		return nil, fmt.Errorf("failed to allocate audio packet")
	}

	frame := astiav.AllocFrame()
	if frame == nil {
		packet.Free()
		decCtx.Free()
		formatCtx.CloseInput()
		return nil, fmt.Errorf("failed to allocate audio frame")
	}

	return &AudioDevice{
		formatCtx: formatCtx,
		decCtx:    decCtx,
		stream:    audioStream,
		packet:    packet,
		frame:     frame,
		streamIdx: streamIdx,
	}, nil
}

func (d *AudioDevice) Close() {
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

func (d *AudioDevice) ReadFrame() (*astiav.Frame, error) {
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
			return nil, fmt.Errorf("failed to send audio packet to decoder: %w", err)
		}
		d.frame.Unref()
		err = d.decCtx.ReceiveFrame(d.frame)
		if err == nil {
			return d.frame, nil
		}
		if err == astiav.ErrEagain || err == astiav.ErrEof {
			continue
		}
		return nil, fmt.Errorf("audio decoder error: %w", err)
	}
}

func (d *AudioDevice) SampleRate() int {
	return d.decCtx.SampleRate()
}

func (d *AudioDevice) Channels() int {
	return d.decCtx.ChannelLayout().Channels()
}

func (d *AudioDevice) SampleFormat() astiav.SampleFormat {
	return d.decCtx.SampleFormat()
}
