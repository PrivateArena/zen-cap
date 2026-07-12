package capture

import (
	"fmt"
	"image"
	"image/color"
	"math"

	"github.com/jezek/xgb/xproto"
	"github.com/jezek/xgbutil"

	"zen-cap/pkg/annotation"
)

// Magnifier handles magnifier positioning and rendering for pixel-level feedback.
type Magnifier struct {
	Width      int
	Height     int
	PickerSize int // if > 0, draws a border outline of this size inside the magnifier
}

// NewMagnifier initializes a standard 120x120 magnifier.
func NewMagnifier() *Magnifier {
	return &Magnifier{
		Width:      120,
		Height:     120,
		PickerSize: 0,
	}
}

// Render calculates coordinates and draws the magnifier loupe onto the double-buffer pixmap.
func (m *Magnifier) Render(
	xu *xgbutil.XUtil,
	bufPixmapID xproto.Pixmap,
	gcID xproto.Gcontext,
	depth byte,
	rgbaImg *image.RGBA,
	mx, my int,
	screenWidth, screenHeight int,
	dragging bool,
	startX, startY int,
) {
	// Position calculation: Float loupe above/below cursor
	lx := mx + 20
	ly := my - 140
	if ly < 10 {
		ly = my + 20 // Flip below cursor
	}
	if lx+m.Width > screenWidth {
		lx = mx - 140 // Flip to left of cursor
	}
	if lx < 10 {
		lx = 10
	}
	if ly+m.Height > screenHeight {
		ly = screenHeight - 130
	}
	if ly < 10 {
		ly = 10
	}

	magImg := m.getMagnifierImage(rgbaImg, mx, my, lx, ly, screenWidth, screenHeight, dragging, startX, startY)
	magBGRA := imageToBGRA(magImg)

	// Upload magnifier to the buffer pixmap
	xproto.PutImage(
		xu.Conn(),
		xproto.ImageFormatZPixmap,
		xproto.Drawable(bufPixmapID),
		gcID,
		uint16(m.Width),
		uint16(m.Height),
		int16(lx),
		int16(ly),
		0, // leftPad
		depth,
		magBGRA,
	)
}

func (m *Magnifier) getMagnifierImage(rgbaImg *image.RGBA, mx, my, lx, ly, screenWidth, screenHeight int, dragging bool, startX, startY int) *image.RGBA {
	mag := image.NewRGBA(image.Rect(0, 0, 120, 120))
	magPix := mag.Pix
	magStride := mag.Stride
	srcPix := rgbaImg.Pix
	srcStride := rgbaImg.Stride

	const (
		pinkR, pinkG, pinkB, pinkA = 255, 0, 127, 255
		cyanR, cyanG, cyanB, cyanA = 0, 240, 255, 255
	)

	for dy := 0; dy < 120; dy++ {
		for dx := 0; dx < 120; dx++ {
			oi := dy*magStride + dx*4
			rx := dx - 60
			ry := dy - 60
			distSq := rx*rx + ry*ry

			var r, g, b, a uint8
			if distSq > 60*60 {
				sx := lx + dx
				sy := ly + dy
				if sx >= 0 && sx < screenWidth && sy >= 0 && sy < screenHeight {
					i := sy*srcStride + sx*4
					r, g, b, a = srcPix[i+0], srcPix[i+1], srcPix[i+2], srcPix[i+3]
				} else {
					a = 255
				}
			} else if distSq >= 58*58 && distSq <= 60*60 {
				r, g, b, a = cyanR, cyanG, cyanB, cyanA
			} else {
				sx := mx - 15 + dx/4
				sy := my - 15 + dy/4
				if sx >= 0 && sx < screenWidth && sy >= 0 && sy < screenHeight {
					i := sy*srcStride + sx*4
					r, g, b, a = srcPix[i+0], srcPix[i+1], srcPix[i+2], srcPix[i+3]
				} else {
					a = 255
				}

				isOnBorder := false
				if m.PickerSize > 0 {
					ox := dx/4 - 15
					oy := dy/4 - 15
					half := m.PickerSize / 2
					if (ox == -half && dx%4 == 0 && oy >= -half && oy <= half) ||
						(ox == half && dx%4 == 3 && oy >= -half && oy <= half) ||
						(oy == -half && dy%4 == 0 && ox >= -half && ox <= half) ||
						(oy == half && dy%4 == 3 && ox >= -half && ox <= half) {
						isOnBorder = true
					}
					if isOnBorder {
						r, g, b, a = cyanR, cyanG, cyanB, cyanA
					}
				}

				if !isOnBorder && ((rx == 0 && ry >= -10 && ry <= 10) || (ry == 0 && rx >= -10 && rx <= 10)) {
					r, g, b, a = pinkR, pinkG, pinkB, pinkA
				}
			}
			magPix[oi+0], magPix[oi+1], magPix[oi+2], magPix[oi+3] = r, g, b, a
		}
	}

	var infoStr string
	if dragging {
		w := int(math.Abs(float64(mx - startX)))
		h := int(math.Abs(float64(my - startY)))
		infoStr = fmt.Sprintf("%dX%d", w, h)
	} else {
		infoStr = fmt.Sprintf("%d,%d", mx, my)
	}

	hudWidth := len(infoStr)*4 + 6
	hudStartX := (120 - hudWidth) / 2
	for ty := 95; ty < 103; ty++ {
		for tx := hudStartX; tx < hudStartX+hudWidth; tx++ {
			rx := tx - 60
			ry := ty - 60
			if rx*rx+ry*ry < 56*56 {
				oi := ty*magStride + tx*4
				magPix[oi+0], magPix[oi+1], magPix[oi+2], magPix[oi+3] = 0, 0, 0, 220
			}
		}
	}

	annotation.DrawStringScaled(mag, infoStr, hudStartX+3, 90, color.White, 1)

	return mag
}
