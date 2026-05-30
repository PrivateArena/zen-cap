package capture

import (
	"image"
	"image/color"
	"image/draw"
	"math"
)

// drawLine draws a thick line on a Go draw.Image using Bresenham's line algorithm with brush thickness.
func drawLine(img draw.Image, x0, y0, x1, y1 int, col color.Color, thickness int) {
	dx := abs(x1 - x0)
	dy := abs(y1 - y0)
	sx, sy := 1, 1
	if x0 > x1 {
		sx = -1
	}
	if y0 > y1 {
		sy = -1
	}
	err := dx - dy

	for {
		// Draw brush of specified thickness
		for ty := -thickness / 2; ty <= thickness/2; ty++ {
			for tx := -thickness / 2; tx <= thickness/2; tx++ {
				// Circle brush constraint
				if tx*tx+ty*ty <= (thickness*thickness)/4 {
					img.Set(x0+tx, y0+ty, col)
				}
			}
		}

		if x0 == x1 && y0 == y1 {
			break
		}
		e2 := 2 * err
		if e2 > -dy {
			err -= dy
			x0 += sx
		}
		if e2 < dx {
			err += dx
			y0 += sy
		}
	}
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// drawRect draws a rectangle with thickness on a draw.Image
func drawRect(img draw.Image, x0, y0, x1, y1 int, col color.Color, thickness int) {
	// Draw the 4 edges using drawLine
	drawLine(img, x0, y0, x1, y0, col, thickness)
	drawLine(img, x1, y0, x1, y1, col, thickness)
	drawLine(img, x1, y1, x0, y1, col, thickness)
	drawLine(img, x0, y1, x0, y0, col, thickness)
}

// drawCircle draws a circle with thickness on a draw.Image
func drawCircle(img draw.Image, cx, cy, r int, col color.Color, thickness int) {
	for y := cy - r - thickness; y <= cy+r+thickness; y++ {
		for x := cx - r - thickness; x <= cx+r+thickness; x++ {
			dx := x - cx
			dy := y - cy
			dist := math.Sqrt(float64(dx*dx + dy*dy))
			if math.Abs(dist-float64(r)) <= float64(thickness)/2.0 {
				img.Set(x, y, col)
			}
		}
	}
}

// drawHUDTextScaled draws a string scaled onto a draw.Image inside a solid background rectangle
func drawHUDTextScaled(img draw.Image, text string, x, y int, fg, bg color.Color, scale int) {
	w := len(text)*6*scale + 6
	h := 7*scale + 4
	for dy := 0; dy < h; dy++ {
		for dx := 0; dx < w; dx++ {
			img.Set(x+dx, y+dy, bg)
		}
	}
	drawStringScaled(img, text, x+3, y+2, fg, scale)
}

// copyImage creates a deep copy of an *image.RGBA
func copyImage(src *image.RGBA) *image.RGBA {
	bounds := src.Bounds()
	dst := image.NewRGBA(bounds)
	copy(dst.Pix, src.Pix)
	return dst
}
