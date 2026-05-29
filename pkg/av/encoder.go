package av

import (
	"fmt"

	"github.com/asticode/go-astiav"
)

type VideoEncoder struct {
	codecCtx *astiav.CodecContext
	codec    *astiav.Codec
	pkt      *astiav.Packet
	pts      int64
	fps      int
}

func NewVideoEncoder(w, h, fps int, bitrate int64) (*VideoEncoder, error) {
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
	codecCtx.SetPixelFormat(astiav.PixelFormatYuv420P)
	codecCtx.SetTimeBase(astiav.NewRational(1, fps))
	codecCtx.SetFramerate(astiav.NewRational(fps, 1))
	// 2-second GOP gives good seeking granularity without bloating the file.
	codecCtx.SetGopSize(fps * 2)

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
	options.Set("preset", "ultrafast", 0)
	options.Set("crf", "23", 0)
	options.Set("tune", "zerolatency", 0)

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

func (e *VideoEncoder) Close() {
	if e.pkt != nil {
		e.pkt.Free()
	}
	if e.codecCtx != nil {
		e.codecCtx.Free()
	}
}
