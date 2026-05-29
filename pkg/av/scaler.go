// [VERIFIED]
package av

import (
	"fmt"

	"github.com/asticode/go-astiav"
)

type Scaler struct {
	swsCtx *astiav.SoftwareScaleContext
}

func NewScaler(srcW, srcH int, srcFmt astiav.PixelFormat, dstW, dstH int, dstFmt astiav.PixelFormat) (*Scaler, error) {
	// Create software scale context using bilinear interpolation
	flags := astiav.SoftwareScaleContextFlags(astiav.SoftwareScaleContextFlagBilinear)
	swsCtx, err := astiav.CreateSoftwareScaleContext(srcW, srcH, srcFmt, dstW, dstH, dstFmt, flags)
	if err != nil {
		return nil, fmt.Errorf("failed to create software scale context: %w", err)
	}

	return &Scaler{swsCtx: swsCtx}, nil
}

func (s *Scaler) Scale(src *astiav.Frame, dst *astiav.Frame) error {
	if err := s.swsCtx.ScaleFrame(src, dst); err != nil {
		return fmt.Errorf("failed to scale frame: %w", err)
	}
	return nil
}

func (s *Scaler) Close() {
	if s.swsCtx != nil {
		s.swsCtx.Free()
	}
}
