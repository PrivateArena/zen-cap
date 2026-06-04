package capture

import (
	"fmt"
	"image"
	"image/color"
	"math"

	"github.com/jezek/xgb/xproto"
	"github.com/jezek/xgbutil"
)

// Magnifier handles magnifier positioning and rendering for pixel-level feedback.
type Magnifier struct {
	Width  int
	Height int
}

// NewMagnifier initializes a standard 120x120 magnifier.
func NewMagnifier() *Magnifier {
	return &Magnifier{
		Width:  120,
		Height: 120,
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

	magImg := getMagnifierImage(rgbaImg, mx, my, lx, ly, screenWidth, screenHeight, dragging, startX, startY)
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

func getMagnifierImage(rgbaImg *image.RGBA, mx, my, lx, ly, screenWidth, screenHeight int, dragging bool, startX, startY int) *image.RGBA {
	mag := image.NewRGBA(image.Rect(0, 0, 120, 120))
	pinkColor := color.RGBA{R: 255, G: 0, B: 127, A: 255}
	cyanColor := color.RGBA{R: 0, G: 240, B: 255, A: 255}

	for dy := 0; dy < 120; dy++ {
		for dx := 0; dx < 120; dx++ {
			rx := dx - 60
			ry := dy - 60
			distSq := rx*rx + ry*ry

			var col color.Color
			if distSq > 60*60 {
				// Outside the circular magnifier glass: draw background screen pixel
				sx := lx + dx
				sy := ly + dy
				if sx >= 0 && sx < screenWidth && sy >= 0 && sy < screenHeight {
					col = rgbaImg.At(sx, sy)
				} else {
					col = color.Black
				}
			} else if distSq >= 58*58 && distSq <= 60*60 {
				// Outer bezel: Neon cyan
				col = cyanColor
			} else {
				// Inside the circular magnifier glass: 4x nearest-neighbor magnification
				sx := mx - 15 + dx/4
				sy := my - 15 + dy/4
				if sx >= 0 && sx < screenWidth && sy >= 0 && sy < screenHeight {
					col = rgbaImg.At(sx, sy)
				} else {
					col = color.Black
				}

				// Draw circular central crosshairs (Neon pink, length 10px in 4 directions)
				if (rx == 0 && ry >= -10 && ry <= 10) || (ry == 0 && rx >= -10 && rx <= 10) {
					col = pinkColor
				}
			}
			mag.Set(dx, dy, col)
		}
	}

	// Determine info string to display in the bottom HUD bar
	var infoStr string
	if dragging {
		w := int(math.Abs(float64(mx - startX)))
		h := int(math.Abs(float64(my - startY)))
		infoStr = fmt.Sprintf("%dX%d", w, h)
	} else {
		infoStr = fmt.Sprintf("%d,%d", mx, my)
	}

	// Render HUD background bar inside the bottom of the magnifier (transparent black)
	hudWidth := len(infoStr)*4 + 6
	hudStartX := (120 - hudWidth) / 2
	for ty := 95; ty < 103; ty++ {
		for tx := hudStartX; tx < hudStartX+hudWidth; tx++ {
			rx := tx - 60
			ry := ty - 60
			if rx*rx+ry*ry < 56*56 {
				mag.Set(tx, ty, color.RGBA{R: 0, G: 0, B: 0, A: 220})
			}
		}
	}

	// Draw custom 3x5 pixel font coordinates/dims centered
	drawString(mag, infoStr, hudStartX+3, 97, color.White)

	return mag
}
