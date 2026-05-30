package capture

import (
	"fmt"
	"image"
	"image/color"
	"math"

	"github.com/jezek/xgb/xproto"
	"github.com/jezek/xgbutil"
)

// Convert image.Image to BGRA bytes for X11
func imageToBGRA(img image.Image) []byte {
	bounds := img.Bounds()
	w, h := bounds.Dx(), bounds.Dy()
	data := make([]byte, w*h*4)
	idx := 0
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			r, g, b, a := img.At(x, y).RGBA()
			// Go's RGBA() returns alpha-premultiplied values in [0, 65535]
			data[idx] = byte(b >> 8)
			data[idx+1] = byte(g >> 8)
			data[idx+2] = byte(r >> 8)
			data[idx+3] = byte(a >> 8)
			idx += 4
		}
	}
	return data
}

// Upload image data in chunks to prevent X11 packet size limits
func uploadImageChunked(xu *xgbutil.XUtil, drawable xproto.Drawable, gc xproto.Gcontext, depth byte, w, h int, bgraData []byte) error {
	rowBytes := w * 4

	// Determine safe chunkRows based on X connection's MaximumRequestLength (which is in 4-byte units)
	maxReq := 0
	setup := xproto.Setup(xu.Conn())
	if setup != nil {
		maxReq = int(setup.MaximumRequestLength) * 4
	}
	if maxReq <= 0 {
		maxReq = 65536 * 4 // Core X11 fallback (256KB)
	}
	// Subtract 1024 bytes buffer for X11 packet headers (PutImage is a few dozen bytes)
	maxDataBytes := maxReq - 1024
	chunkRows := maxDataBytes / rowBytes
	if chunkRows < 1 {
		chunkRows = 1
	}

	for y := 0; y < h; y += chunkRows {
		rows := chunkRows
		if y+rows > h {
			rows = h - y
		}
		offset := y * rowBytes
		length := rows * rowBytes

		err := xproto.PutImageChecked(
			xu.Conn(),
			xproto.ImageFormatZPixmap,
			drawable,
			gc,
			uint16(w),
			uint16(rows),
			0,        // dstX
			int16(y), // dstY
			0,        // leftPad
			depth,
			bgraData[offset:offset+length],
		).Check()
		if err != nil {
			return fmt.Errorf("PutImage failed at row %d (chunk size %d rows): %w", y, rows, err)
		}
	}
	return nil
}

// getMagnifierImage generates a 120x120 circle magnifier image with a neon-cyan bezel, neon-pink crosshairs,
// and coordinates or current crop dimensions rendered in a custom 3x5 font at the bottom.
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
				// Map dx, dy [0..120] to source offsets [-15..15]
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
