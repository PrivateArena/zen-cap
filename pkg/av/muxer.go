// [VERIFIED]
package av

import (
	"fmt"

	"github.com/asticode/go-astiav"
)

type Muxer struct {
	formatCtx *astiav.FormatContext
	stream    *astiav.Stream
	ioCtx     *astiav.IOContext
	path      string
	headerWritten bool
}

func NewMuxer(path string, encCtx *astiav.CodecContext) (*Muxer, error) {
	Init()

	formatCtx, err := astiav.AllocOutputFormatContext(nil, "", path)
	if err != nil {
		return nil, fmt.Errorf("failed to allocate output format context: %w", err)
	}
	if formatCtx == nil {
		return nil, fmt.Errorf("allocated output format context is nil")
	}

	stream := formatCtx.NewStream(nil)
	if stream == nil {
		formatCtx.Free()
		return nil, fmt.Errorf("failed to create output stream")
	}

	if err := stream.CodecParameters().FromCodecContext(encCtx); err != nil {
		formatCtx.Free()
		return nil, fmt.Errorf("failed to copy codec parameters to stream: %w", err)
	}

	stream.SetTimeBase(encCtx.TimeBase())

	// Check if global header is required by the output format
	if formatCtx.OutputFormat().Flags().Has(astiav.IOFormatFlagGlobalheader) {
		encCtx.SetFlags(encCtx.Flags().Add(astiav.CodecContextFlagGlobalHeader))
	}

	var ioCtx *astiav.IOContext
	if !formatCtx.OutputFormat().Flags().Has(astiav.IOFormatFlagNofile) {
		ioCtx, err = astiav.OpenIOContext(path, astiav.NewIOContextFlags(astiav.IOContextFlagWrite), nil, nil)
		if err != nil {
			formatCtx.Free()
			return nil, fmt.Errorf("failed to open IO context: %w", err)
		}
		formatCtx.SetPb(ioCtx)
	}

	if err := formatCtx.WriteHeader(nil); err != nil {
		if ioCtx != nil {
			ioCtx.Close()
		}
		formatCtx.Free()
		return nil, fmt.Errorf("failed to write output header: %w", err)
	}

	return &Muxer{
		formatCtx:     formatCtx,
		stream:        stream,
		ioCtx:         ioCtx,
		path:          path,
		headerWritten: true,
	}, nil
}

func (m *Muxer) WritePacket(pkt *astiav.Packet, encTimeBase astiav.Rational) error {
	// Rescale timestamps from encoder time base to stream time base
	pkt.RescaleTs(encTimeBase, m.stream.TimeBase())
	pkt.SetStreamIndex(m.stream.Index())

	if err := m.formatCtx.WriteInterleavedFrame(pkt); err != nil {
		return fmt.Errorf("failed to write frame: %w", err)
	}
	return nil
}

func (m *Muxer) Close() error {
	var writeTrailerErr error
	if m.headerWritten && m.formatCtx != nil {
		if err := m.formatCtx.WriteTrailer(); err != nil {
			writeTrailerErr = fmt.Errorf("failed to write trailer: %w", err)
		}
	}

	var ioCloseErr error
	if m.ioCtx != nil {
		if err := m.ioCtx.Close(); err != nil {
			ioCloseErr = fmt.Errorf("failed to close IO context: %w", err)
		}
	}

	if m.formatCtx != nil {
		m.formatCtx.Free()
	}

	if writeTrailerErr != nil {
		return writeTrailerErr
	}
	return ioCloseErr
}
