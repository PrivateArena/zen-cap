package magnifier

import (
	"image"
	"image/draw"

	xdraw "golang.org/x/image/draw"
)

// scaleImage scales src to dstW×dstH.
// smooth=true uses bilinear interpolation; false uses nearest-neighbour.
func scaleImage(src *image.RGBA, dstW, dstH int, smooth bool) *image.RGBA {
	dst := image.NewRGBA(image.Rect(0, 0, dstW, dstH))
	if smooth {
		xdraw.BiLinear.Scale(dst, dst.Bounds(), src, src.Bounds(), draw.Src, nil)
	} else {
		xdraw.NearestNeighbor.Scale(dst, dst.Bounds(), src, src.Bounds(), draw.Src, nil)
	}
	return dst
}

// rgbaToBGRA converts an *image.RGBA pixel buffer to BGRA byte order
// expected by X11 ZPixmap PutImage.
func rgbaToBGRA(img *image.RGBA) []byte {
	pix := img.Pix
	n := len(pix)
	out := make([]byte, n)
	for i := 0; i < n; i += 4 {
		out[i] = pix[i+2]   // B
		out[i+1] = pix[i+1] // G
		out[i+2] = pix[i]   // R
		out[i+3] = pix[i+3] // A
	}
	return out
}

// drawCrosshair paints a small "+" crosshair at (cx, cy) in the image,
// indicating the true cursor position within the magnified view.
func drawCrosshair(img *image.RGBA, cx, cy int) {
	w := img.Bounds().Dx()
	h := img.Bounds().Dy()
	const arm = 12
	// Neon pink
	pink := [4]byte{255, 0, 127, 255}
	outline := [4]byte{0, 0, 0, 180}

	setIfInBounds := func(x, y int, px [4]byte) {
		if x >= 0 && y >= 0 && x < w && y < h {
			base := (y*w + x) * 4
			img.Pix[base] = px[0]
			img.Pix[base+1] = px[1]
			img.Pix[base+2] = px[2]
			img.Pix[base+3] = px[3]
		}
	}

	// Outline first (one pixel border around the arms).
	for dy := -arm; dy <= arm; dy++ {
		for _, dx := range []int{-1, 1} {
			setIfInBounds(cx+dx, cy+dy, outline)
			setIfInBounds(cx+dy, cy+dx, outline)
		}
	}
	setIfInBounds(cx, cy-arm-1, outline)
	setIfInBounds(cx, cy+arm+1, outline)
	setIfInBounds(cx-arm-1, cy, outline)
	setIfInBounds(cx+arm+1, cy, outline)

	// Arms.
	for d := -arm; d <= arm; d++ {
		setIfInBounds(cx, cy+d, pink)
		setIfInBounds(cx+d, cy, pink)
	}
}
