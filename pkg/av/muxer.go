package av

import (
	"fmt"
	"sync"

	"github.com/asticode/go-astiav"
)

type Muxer struct {
	formatCtx     *astiav.FormatContext
	ioCtx         *astiav.IOContext
	path          string
	headerWritten bool
	mu            sync.Mutex
}

func NewMuxer(path string) (*Muxer, error) {
	Init()

	formatCtx, err := astiav.AllocOutputFormatContext(nil, "", path)
	if err != nil {
		return nil, fmt.Errorf("failed to allocate output format context: %w", err)
	}
	if formatCtx == nil {
		return nil, fmt.Errorf("allocated output format context is nil")
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

	return &Muxer{
		formatCtx: formatCtx,
		ioCtx:     ioCtx,
		path:      path,
	}, nil
}

func (m *Muxer) AddStream(encCtx *astiav.CodecContext) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	stream := m.formatCtx.NewStream(nil)
	if stream == nil {
		return -1, fmt.Errorf("failed to create output stream")
	}
	if err := stream.CodecParameters().FromCodecContext(encCtx); err != nil {
		return -1, fmt.Errorf("failed to copy codec parameters to stream: %w", err)
	}
	stream.SetTimeBase(encCtx.TimeBase())

	return stream.Index(), nil
}

func (m *Muxer) WriteHeader() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.headerWritten {
		return nil
	}
	if err := m.formatCtx.WriteHeader(nil); err != nil {
		return fmt.Errorf("failed to write output header: %w", err)
	}
	m.headerWritten = true
	return nil
}

func (m *Muxer) WritePacket(pkt *astiav.Packet, streamIdx int, encTimeBase astiav.Rational) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	stream := m.formatCtx.Streams()[streamIdx]
	pkt.RescaleTs(encTimeBase, stream.TimeBase())
	pkt.SetStreamIndex(streamIdx)

	if err := m.formatCtx.WriteInterleavedFrame(pkt); err != nil {
		return fmt.Errorf("failed to write frame: %w", err)
	}
	return nil
}

func (m *Muxer) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()

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
