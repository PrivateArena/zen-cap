package magnifier

import (
	"fmt"
	"image"
	"image/color"

	"github.com/jezek/xgb/shape"
	"github.com/jezek/xgb/xproto"
	"github.com/jezek/xgbutil"
)

// applyWindowShape applies an XShape mask to win based on lensShape.
// ShapeSquare → no mask (full rectangle).
// ShapeCircle → circular mask.
// ShapeVerticalStrip → narrow vertical rect mask.
// ShapeHorizontalStrip → wide short rect mask.
//
// Both the bounding region and input region are set, so mouse clicks pass
// through the invisible parts of the lens window.
func applyWindowShape(xu *xgbutil.XUtil, win xproto.Window, size int, ls LensShape) error {
	conn := xu.Conn()
	if err := shape.Init(conn); err != nil {
		// XShape unavailable: just leave rectangular window.
		return nil
	}

	switch ls {
	case ShapeSquare:
		// Full rectangle — no shape mask needed; the default window shape is rectangular.
		return nil

	case ShapeCircle:
		return applyCircleMask(xu, win, size)

	case ShapeVerticalStrip:
		w := size / 3
		if w < 1 {
			w = 1
		}
		x := int16((size - w) / 2)
		return applyRectMask(xu, win, x, 0, uint16(w), uint16(size))

	case ShapeHorizontalStrip:
		h := size / 3
		if h < 1 {
			h = 1
		}
		y := int16((size - h) / 2)
		return applyRectMask(xu, win, 0, y, uint16(size), uint16(h))
	}
	return nil
}

// applyCircleMask creates a depth-1 pixmap with a filled circle and uses it
// as the bounding + input shape mask for win.
func applyCircleMask(xu *xgbutil.XUtil, win xproto.Window, size int) error {
	conn := xu.Conn()

	// Create a 1-bit depth pixmap for the mask.
	maskPix, err := xproto.NewPixmapId(conn)
	if err != nil {
		return fmt.Errorf("shape: NewPixmapId: %w", err)
	}
	if err := xproto.CreatePixmapChecked(conn, 1, maskPix,
		xproto.Drawable(win), uint16(size), uint16(size)).Check(); err != nil {
		return fmt.Errorf("shape: CreatePixmap: %w", err)
	}
	defer xproto.FreePixmap(conn, maskPix)

	// GC for the mask pixmap (depth 1).
	maskGC, err := xproto.NewGcontextId(conn)
	if err != nil {
		return fmt.Errorf("shape: NewGcontextId: %w", err)
	}
	if err := xproto.CreateGCChecked(conn, maskGC,
		xproto.Drawable(maskPix), 0, nil).Check(); err != nil {
		return fmt.Errorf("shape: CreateGC: %w", err)
	}
	defer xproto.FreeGC(conn, maskGC)

	// Fill mask black (0 = excluded).
	xproto.ChangeGC(conn, maskGC, xproto.GcForeground, []uint32{0})
	xproto.PolyFillRectangle(conn, xproto.Drawable(maskPix), maskGC,
		[]xproto.Rectangle{{X: 0, Y: 0, Width: uint16(size), Height: uint16(size)}})

	// Draw filled circle in white (1 = included) using the RGBA path.
	// XProto has no arc fill directly on depth-1 pixmaps via plain GC in XGB,
	// so we compute a set of covering rectangles from the circle rasterisation.
	rects := rasteriseCircle(size)
	if len(rects) > 0 {
		xproto.ChangeGC(conn, maskGC, xproto.GcForeground, []uint32{1})
		xproto.PolyFillRectangle(conn, xproto.Drawable(maskPix), maskGC, rects)
	}

	// Apply as both bounding and input shape.
	shape.MaskChecked(conn, shape.SoSet, shape.SkBounding, win, 0, 0, maskPix).Check()
	shape.MaskChecked(conn, shape.SoSet, shape.SkInput, win, 0, 0, maskPix).Check()
	return nil
}

// applyRectMask applies a rectangular sub-region shape to win.
func applyRectMask(xu *xgbutil.XUtil, win xproto.Window, x, y int16, w, h uint16) error {
	conn := xu.Conn()
	rects := []xproto.Rectangle{{X: x, Y: y, Width: w, Height: h}}
	shape.RectanglesChecked(conn, shape.SoSet, shape.SkBounding, 0, win, 0, 0, rects).Check()
	shape.RectanglesChecked(conn, shape.SoSet, shape.SkInput, 0, win, 0, 0, rects).Check()
	return nil
}

// rasteriseCircle returns a slice of horizontal span rectangles that together
// fill a circle of the given diameter (centred in a size×size bounding box).
// This approach works on depth-1 pixmaps where FillArc is not reliable.
func rasteriseCircle(size int) []xproto.Rectangle {
	r := size / 2
	cx, cy := r, r
	var rects []xproto.Rectangle

	for y := 0; y < size; y++ {
		dy := y - cy
		// x² ≤ r² − dy²
		dx2 := r*r - dy*dy
		if dx2 < 0 {
			continue
		}
		dx := intSqrt(dx2)
		x0 := cx - dx
		x1 := cx + dx
		if x0 < 0 {
			x0 = 0
		}
		if x1 >= size {
			x1 = size - 1
		}
		w := x1 - x0 + 1
		if w > 0 {
			rects = append(rects, xproto.Rectangle{
				X:      int16(x0),
				Y:      int16(y),
				Width:  uint16(w),
				Height: 1,
			})
		}
	}
	return rects
}

// intSqrt returns the integer square root of n (floor).
func intSqrt(n int) int {
	if n <= 0 {
		return 0
	}
	x := n
	for {
		x1 := (x + n/x) / 2
		if x1 >= x {
			return x
		}
		x = x1
	}
}

// drawOSD renders a small zoom-level indicator (e.g. "2.0×") at the
// bottom-centre of img using a hand-drawn 3×5 pixel font.
// It reuses the drawString function defined in pkg/capture/font.go — but since
// we are in a different package we replicate a minimal subset here.
func drawOSD(img *image.RGBA, zoom float64) {
	text := fmt.Sprintf("%.1f×", zoom)
	const charW, charH = 4, 6
	textPx := len(text) * charW
	w, h := img.Bounds().Dx(), img.Bounds().Dy()

	startX := (w - textPx) / 2
	startY := h - charH - 4

	// Draw semi-transparent black background pill.
	bg := color.RGBA{R: 0, G: 0, B: 0, A: 200}
	for py := startY - 2; py < startY+charH+2; py++ {
		for px := startX - 4; px < startX+textPx+4; px++ {
			if px >= 0 && py >= 0 && px < w && py < h {
				img.SetRGBA(px, py, bg)
			}
		}
	}

	// Draw text using the embedded 3×5 bitmaps.
	fg := color.RGBA{R: 255, G: 255, B: 255, A: 255}
	for i, ch := range text {
		bm, ok := osdFont[ch]
		if !ok {
			continue
		}
		for row := 0; row < 5; row++ {
			for col := 0; col < 3; col++ {
				if bm[row]&(1<<uint(2-col)) != 0 {
					px := startX + i*charW + col
					py := startY + row
					if px >= 0 && py >= 0 && px < w && py < h {
						img.SetRGBA(px, py, fg)
					}
				}
			}
		}
	}
}

// osdFont is a minimal 3×5 raster font for digits, '.', and '×'.
// Each entry is [5]byte where each byte is a 3-bit bitmask (bit2=left, bit0=right).
var osdFont = map[rune][5]byte{
	'0': {0b111, 0b101, 0b101, 0b101, 0b111},
	'1': {0b010, 0b110, 0b010, 0b010, 0b111},
	'2': {0b111, 0b001, 0b111, 0b100, 0b111},
	'3': {0b111, 0b001, 0b011, 0b001, 0b111},
	'4': {0b101, 0b101, 0b111, 0b001, 0b001},
	'5': {0b111, 0b100, 0b111, 0b001, 0b111},
	'6': {0b111, 0b100, 0b111, 0b101, 0b111},
	'7': {0b111, 0b001, 0b001, 0b001, 0b001},
	'8': {0b111, 0b101, 0b111, 0b101, 0b111},
	'9': {0b111, 0b101, 0b111, 0b001, 0b111},
	'.': {0b000, 0b000, 0b000, 0b000, 0b010},
	'×': {0b101, 0b010, 0b000, 0b010, 0b101},
}
