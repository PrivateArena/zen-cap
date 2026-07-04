package av

import (
	"fmt"

	astiav "github.com/asticode/go-astiav"
)

type EncoderOptions struct {
	Preset      string
	CRF         string
	Tune        string
	Profile     string
	PixelFormat astiav.PixelFormat
}

type VideoEncoder struct {
	codecCtx *astiav.CodecContext
	codec    *astiav.Codec
	pkt      *astiav.Packet
	pts      int64
	fps      int
}

func NewVideoEncoder(w, h, fps int, bitrate int64, opts *EncoderOptions) (*VideoEncoder, error) {
	Init()

	codec := astiav.FindEncoder(astiav.CodecIDH264)
	if codec == nil {
		return nil, fmt.Errorf("H.264 encoder not found")
	}

	codecCtx := astiav.AllocCodecContext(codec)
	if codecCtx == nil {
		return nil, fmt.Errorf("failed to allocate codec context")
	}

	codecCtx.SetWidth(w)
	codecCtx.SetHeight(h)

	pixFmt := astiav.PixelFormatYuv420P
	if opts != nil && opts.PixelFormat > 0 {
		pixFmt = opts.PixelFormat
	}
	codecCtx.SetPixelFormat(pixFmt)

	codecCtx.SetTimeBase(astiav.NewRational(1, fps))
	codecCtx.SetFramerate(astiav.NewRational(fps, 1))
	// 2-second GOP gives good seeking granularity without bloating the file.
	codecCtx.SetGopSize(fps * 2)

	// FIX (pink video): declare color metadata explicitly instead of leaving
	// it AVCOL_*_UNSPECIFIED. This must match what we stamp on the YUV frame
	// in recorder.go. Limited range + BT.709 is the standard for HD screen
	// recordings; it ensures swscale's frame-based ScaleFrame programs the
	// correct RGB->YUV conversion matrix and players interpret the stream.
	codecCtx.SetColorRange(astiav.ColorRangeMpeg)
	codecCtx.SetColorSpace(astiav.ColorSpaceBt709)
	codecCtx.SetColorPrimaries(astiav.ColorPrimariesBt709)
	codecCtx.SetColorTransferCharacteristic(astiav.ColorTransferCharacteristicBt709)

	if bitrate > 0 {
		codecCtx.SetBitRate(bitrate)
	}

	// NOTE: GlobalHeader flag must be set BEFORE Open() so the encoder writes
	// SPS/PPS into the extradata rather than inlining them into every keyframe.
	// The muxer checks this flag separately and sets it again after the stream
	// is created — that path is correct. Setting it here too is harmless for
	// formats that don't require it and required for MP4/MOV.
	codecCtx.SetFlags(codecCtx.Flags().Add(astiav.CodecContextFlagGlobalHeader))

	options := astiav.NewDictionary()
	defer options.Free()

	preset := "ultrafast"
	crf := "23"
	tune := "zerolatency"
	if opts != nil {
		if opts.Preset != "" {
			preset = opts.Preset
		}
		if opts.CRF != "" {
			crf = opts.CRF
		}
		if opts.Tune != "" {
			tune = opts.Tune
		}
	}
	options.Set("preset", preset, 0)
	options.Set("crf", crf, 0)
	options.Set("tune", tune, 0)

	if opts != nil && opts.Profile != "" {
		if p := h264Profile(opts.Profile); p >= 0 {
			codecCtx.SetProfile(p)
		}
	}

	if err := codecCtx.Open(codec, options); err != nil {
		codecCtx.Free()
		return nil, fmt.Errorf("failed to open encoder: %w", err)
	}

	pkt := astiav.AllocPacket()
	if pkt == nil {
		codecCtx.Free()
		return nil, fmt.Errorf("failed to allocate encoder packet")
	}

	return &VideoEncoder{
		codecCtx: codecCtx,
		codec:    codec,
		pkt:      pkt,
		fps:      fps,
	}, nil
}

// Encode encodes a frame. Pass nil to flush the encoder.
func (e *VideoEncoder) Encode(frame *astiav.Frame, callback func(*astiav.Packet) error) error {
	if frame != nil {
		frame.SetPts(e.pts)
		e.pts++
	}

	if err := e.codecCtx.SendFrame(frame); err != nil {
		return fmt.Errorf("failed to send frame to encoder: %w", err)
	}

	for {
		e.pkt.Unref()
		err := e.codecCtx.ReceivePacket(e.pkt)
		if err == nil {
			if err := callback(e.pkt); err != nil {
				return err
			}
			continue
		}

		if err == astiav.ErrEagain || err == astiav.ErrEof {
			return nil
		}

		return fmt.Errorf("encoder error: %w", err)
	}
}

func (e *VideoEncoder) CodecContext() *astiav.CodecContext {
	return e.codecCtx
}

type AudioEncoder struct {
	codecCtx *astiav.CodecContext
	codec    *astiav.Codec
	pkt      *astiav.Packet
	pts      int64
}

func NewAudioEncoder(sampleRate, channels int, bitrate int64) (*AudioEncoder, error) {
	Init()

	codec := astiav.FindEncoder(astiav.CodecIDAac)
	if codec == nil {
		return nil, fmt.Errorf("AAC encoder not found")
	}
	codecCtx := astiav.AllocCodecContext(codec)
	if codecCtx == nil {
		return nil, fmt.Errorf("failed to allocate audio codec context")
	}

	codecCtx.SetSampleRate(sampleRate)
	codecCtx.SetChannelLayout(astiav.ChannelLayoutStereo)
	codecCtx.SetSampleFormat(astiav.SampleFormatFltp)
	codecCtx.SetTimeBase(astiav.NewRational(1, sampleRate))

	if bitrate > 0 {
		codecCtx.SetBitRate(bitrate)
	}

	if err := codecCtx.Open(codec, nil); err != nil {
		codecCtx.Free()
		return nil, fmt.Errorf("failed to open audio encoder: %w", err)
	}

	pkt := astiav.AllocPacket()
	if pkt == nil {
		codecCtx.Free()
		return nil, fmt.Errorf("failed to allocate audio packet")
	}

	return &AudioEncoder{
		codecCtx: codecCtx,
		codec:    codec,
		pkt:      pkt,
	}, nil
}

func (e *AudioEncoder) Encode(frame *astiav.Frame, callback func(*astiav.Packet) error) error {
	if frame != nil {
		frame.SetPts(e.pts)
		e.pts += int64(frame.NbSamples())
	}
	if err := e.codecCtx.SendFrame(frame); err != nil {
		return fmt.Errorf("failed to send audio frame to encoder: %w", err)
	}
	for {
		e.pkt.Unref()
		err := e.codecCtx.ReceivePacket(e.pkt)
		if err == nil {
			if err := callback(e.pkt); err != nil {
				return err
			}
			continue
		}
		if err == astiav.ErrEagain || err == astiav.ErrEof {
			return nil
		}
		return fmt.Errorf("audio encoder error: %w", err)
	}
}

func (e *AudioEncoder) CodecContext() *astiav.CodecContext {
	return e.codecCtx
}

func (e *AudioEncoder) Close() {
	if e.pkt != nil {
		e.pkt.Free()
	}
	if e.codecCtx != nil {
		e.codecCtx.Free()
	}
}

func (e *VideoEncoder) Close() {
	if e.pkt != nil {
		e.pkt.Free()
	}
	if e.codecCtx != nil {
		e.codecCtx.Free()
	}
}

func h264Profile(s string) astiav.Profile {
	switch s {
	case "baseline":
		return astiav.ProfileH264Baseline
	case "constrained_baseline":
		return astiav.ProfileH264ConstrainedBaseline
	case "main":
		return astiav.ProfileH264Main
	case "high":
		return astiav.ProfileH264High
	case "high10":
		return astiav.ProfileH264High10
	case "high422":
		return astiav.ProfileH264High422
	case "high444":
		return astiav.ProfileH264High444
	default:
		return -1
	}
}

func PixelFormatFromString(s string) astiav.PixelFormat {
	switch s {
	case "yuv420p":
		return astiav.PixelFormatYuv420P
	case "yuvj420p":
		return astiav.PixelFormatYuvj420P
	case "nv12":
		return astiav.PixelFormatNv12
	case "bgr0":
		return astiav.PixelFormatBgr0
	case "bgra":
		return astiav.PixelFormatBgra
	case "rgba":
		return astiav.PixelFormatRgba
	case "rgb0":
		return astiav.PixelFormatRgb0
	default:
		return astiav.PixelFormatNone
	}
}
