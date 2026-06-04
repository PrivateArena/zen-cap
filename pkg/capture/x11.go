package capture

import (
	"fmt"
	"image"

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

// ImageToBGRA is an exported wrapper around imageToBGRA
func ImageToBGRA(img image.Image) []byte {
	return imageToBGRA(img)
}

// UploadImageChunked is an exported wrapper around uploadImageChunked
func UploadImageChunked(xu *xgbutil.XUtil, drawable xproto.Drawable, gc xproto.Gcontext, depth byte, w, h int, bgraData []byte) error {
	return uploadImageChunked(xu, drawable, gc, depth, w, h, bgraData)
}

