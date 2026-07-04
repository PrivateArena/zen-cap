// [VERIFIED]
package av

import (
	"fmt"

	"github.com/asticode/go-astiav"
)

type Scaler struct {
	swsCtx *astiav.SoftwareScaleContext
}

func NewScaler(srcW, srcH int, srcFmt astiav.PixelFormat, dstW, dstH int, dstFmt astiav.PixelFormat, scaleAlgo string) (*Scaler, error) {
	flags := scalerFlags(scaleAlgo).
		Add(astiav.SoftwareScaleContextFlagAccurateRnd)
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

func scalerFlags(algo string) astiav.SoftwareScaleContextFlags {
	switch algo {
	case "lanczos":
		return astiav.NewSoftwareScaleContextFlags(astiav.SoftwareScaleContextFlagLanczos)
	case "spline":
		return astiav.NewSoftwareScaleContextFlags(astiav.SoftwareScaleContextFlagSpline)
	case "bicubic":
		return astiav.NewSoftwareScaleContextFlags(astiav.SoftwareScaleContextFlagBicubic)
	case "bilinear":
		return astiav.NewSoftwareScaleContextFlags(astiav.SoftwareScaleContextFlagBilinear)
	case "fast_bilinear":
		return astiav.NewSoftwareScaleContextFlags(astiav.SoftwareScaleContextFlagFastBilinear)
	case "gauss":
		return astiav.NewSoftwareScaleContextFlags(astiav.SoftwareScaleContextFlagGauss)
	case "area":
		return astiav.NewSoftwareScaleContextFlags(astiav.SoftwareScaleContextFlagArea)
	default:
		return astiav.NewSoftwareScaleContextFlags(astiav.SoftwareScaleContextFlagLanczos)
	}
}
