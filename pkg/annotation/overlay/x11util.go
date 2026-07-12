package overlay

import (
	"image"
	"image/color"

	"github.com/jezek/xgb/xproto"
	"github.com/jezek/xgbutil"
)

// pixmapFormatFor looks up the server's real bits-per-pixel and scanline
// padding for a given depth. Depth 24 is *not* 3 bytes/pixel on virtually
// any real X server — it's almost always packed into 32-bit pixels. You
// must not derive bytes-per-pixel from depth via division; the server
// decides it, and PutImage validates its request length against that
// server-declared value.
func pixmapFormatFor(xu *xgbutil.XUtil, depth byte) (bitsPerPixel int, scanlinePad int) {
	setup := xproto.Setup(xu.Conn())
	for _, f := range setup.PixmapFormats {
		if f.Depth == depth {
			return int(f.BitsPerPixel), int(f.ScanlinePad)
		}
	}
	return 32, 32
}

// rowStrideBytes computes the padded scanline length in bytes. Each
// scanline is padded up to a multiple of scanlinePad bits.
func rowStrideBytes(width, bitsPerPixel, scanlinePad int) int {
	bitsPerRow := width * bitsPerPixel
	paddedBits := ((bitsPerRow + scanlinePad - 1) / scanlinePad) * scanlinePad
	return paddedBits / 8
}

// imageToPixelData packs img into the byte layout the server actually
// expects: bytesPerPixel derived from the server's pixmap format (not
// depth/8), each row padded out to stride bytes.
func imageToPixelData(img image.Image, bytesPerPixel, stride int) []byte {
	bounds := img.Bounds()
	w, h := bounds.Dx(), bounds.Dy()
	data := make([]byte, stride*h)
	for row := 0; row < h; row++ {
		y := bounds.Min.Y + row
		rowOff := row * stride
		idx := rowOff
		for col := 0; col < w; col++ {
			x := bounds.Min.X + col
			r, g, b, a := img.At(x, y).RGBA()
			data[idx] = byte(b >> 8)
			data[idx+1] = byte(g >> 8)
			data[idx+2] = byte(r >> 8)
			if bytesPerPixel >= 4 {
				data[idx+3] = byte(a >> 8)
			}
			idx += bytesPerPixel
		}
	}
	return data
}

func uploadImageChunked(xu *xgbutil.XUtil, drawable xproto.Drawable, gc xproto.Gcontext, depth byte, w, h int, pixelData []byte) {
	bpp, pad := pixmapFormatFor(xu, depth)
	stride := rowStrideBytes(w, bpp, pad)

	setup := xproto.Setup(xu.Conn())
	maxReq := 0
	if setup != nil {
		maxReq = int(setup.MaximumRequestLength) * 4
	}
	if maxReq <= 0 {
		maxReq = 65536 * 4
	}
	maxDataBytes := maxReq - 1024
	chunkRows := maxDataBytes / stride
	if chunkRows < 1 {
		chunkRows = 1
	}

	for y := 0; y < h; y += chunkRows {
		rows := chunkRows
		if y+rows > h {
			rows = h - y
		}
		offset := y * stride
		length := rows * stride
		xproto.PutImage(xu.Conn(),
			xproto.ImageFormatZPixmap,
			drawable, gc,
			uint16(w), uint16(rows),
			0, int16(y), 0, depth,
			pixelData[offset:offset+length],
		)
	}
}

func findARGBVisual(screen *xproto.ScreenInfo) (xproto.Visualid, byte) {
	for _, d := range screen.AllowedDepths {
		if d.Depth != 32 {
			continue
		}
		for _, vis := range d.Visuals {
			if vis.RedMask == 0x00ff0000 &&
				vis.GreenMask == 0x0000ff00 &&
				vis.BlueMask == 0x000000ff {
				return vis.VisualId, d.Depth
			}
		}
	}
	return 0, 0
}

func setAtom(xu *xgbutil.XUtil, win xproto.Window, propName, valueName string) {
	conn := xu.Conn()
	propAtom, err := xproto.InternAtom(conn, false, uint16(len(propName)), propName).Reply()
	if err != nil {
		return
	}
	valueAtom, err := xproto.InternAtom(conn, false, uint16(len(valueName)), valueName).Reply()
	if err != nil {
		return
	}
	atomType, err := xproto.InternAtom(conn, false, 4, "ATOM").Reply()
	if err != nil {
		return
	}
	data := []byte{
		byte(valueAtom.Atom),
		byte(valueAtom.Atom >> 8),
		byte(valueAtom.Atom >> 16),
		byte(valueAtom.Atom >> 24),
	}
	xproto.ChangeProperty(conn, xproto.PropModeReplace, win, propAtom.Atom, atomType.Atom, 32, 1, data)
}

func alphaOver(base, layer *image.RGBA) *image.RGBA {
	b := layer.Bounds()
	dst := image.NewRGBA(b)
	if base != nil {
		for i, p := range base.Pix {
			dst.Pix[i] = p
		}
	}
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			lr, lg, lb, la := layer.At(x, y).RGBA()
			if la == 0 {
				continue
			}
			if la == 0xffff {
				dst.Set(x, y, color.RGBA{R: uint8(lr >> 8), G: uint8(lg >> 8), B: uint8(lb >> 8), A: 255})
				continue
			}
			dr, dg, db, _ := dst.At(x, y).RGBA()
			r := uint8((lr*la + dr*(0xffff-la)) >> 24)
			g := uint8((lg*la + dg*(0xffff-la)) >> 24)
			b2 := uint8((lb*la + db*(0xffff-la)) >> 24)
			dst.Set(x, y, color.RGBA{R: r, G: g, B: b2, A: 255})
		}
	}
	return dst
}
