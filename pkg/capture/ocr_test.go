package capture

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"testing"

	"golang.org/x/image/font"
	"golang.org/x/image/font/basicfont"
	"golang.org/x/image/math/fixed"
)

// legacyRenderOCRBoxes is a verbatim copy of the original overlay render loop
// (pre-generalization). It is used as the parity reference to prove that the
// extracted RenderOCRBoxes produces byte-identical output.
func legacyRenderOCRBoxes(img image.Image, results []OCRResult) *image.RGBA {
	bounds := img.Bounds()
	rgbaImg := image.NewRGBA(bounds)
	draw.Draw(rgbaImg, bounds, img, bounds.Min, draw.Src)

	for _, res := range results {
		if res.Text == "" {
			continue
		}

		text := res.Text

		minX := res.Bounds.Min.X + bounds.Min.X
		minY := res.Bounds.Min.Y + bounds.Min.Y
		maxX := res.Bounds.Max.X + bounds.Min.X
		maxY := res.Bounds.Max.Y + bounds.Min.Y
		boxW := maxX - minX
		boxH := maxY - minY

		if boxW <= 0 || boxH <= 0 {
			continue
		}

		estimatedUnits := 0.0
		for _, r := range text {
			if (r >= 0x4E00 && r <= 0x9FFF) || (r >= 0xAC00 && r <= 0xD7AF) || (r >= 0x3040 && r <= 0x309F) || (r >= 0x30A0 && r <= 0x30FF) {
				estimatedUnits += 1.0
			} else if r == ' ' {
				estimatedUnits += 0.35
			} else {
				estimatedUnits += 0.55
			}
		}
		if estimatedUnits < 1.0 {
			estimatedUnits = 1.0
		}

		fontSizeByHeight := float64(boxH) * 0.85
		fontSizeByWidth := (float64(boxW) / estimatedUnits) * 0.90

		fontSize := fontSizeByHeight
		if fontSizeByWidth > fontSize {
			maxScaleUp := fontSizeByHeight * 3.0
			if fontSizeByWidth > maxScaleUp {
				fontSizeByWidth = maxScaleUp
			}
			fontSize = fontSizeByWidth
		}

		maxAllowedFontSize := float64(bounds.Dy()) * 0.50
		if fontSize > maxAllowedFontSize {
			fontSize = maxAllowedFontSize
		}

		minFontSize := 16.0
		if fontSize < minFontSize {
			fontSize = minFontSize
		}

		var face font.Face
		var metrics font.Metrics
		var textWidth int
		for {
			var err error
			face, err = loadSystemFont(fontSize, hasCJK(text))
			if err != nil {
				face = basicfont.Face7x13
			}

			dMeasure := &font.Drawer{Face: face}
			textWidth = dMeasure.MeasureString(text).Round()

			if textWidth+8 <= boxW || fontSize <= minFontSize {
				break
			}

			fontSize -= 1.0
			if fontSize < minFontSize {
				fontSize = minFontSize
			}
		}

		metrics = face.Metrics()
		ascent := metrics.Ascent.Round()
		if face == basicfont.Face7x13 {
			ascent = 11
		}

		requiredHeight := metrics.Height.Round() + 6
		if boxH < requiredHeight {
			centerY := (minY + maxY) / 2
			minY = centerY - requiredHeight/2
			maxY = centerY + requiredHeight/2
			boxH = maxY - minY
		}

		requiredWidth := textWidth + 10
		if boxW < requiredWidth {
			centerX := (minX + maxX) / 2
			minX = centerX - requiredWidth/2
			maxX = centerX + requiredWidth/2
			boxW = maxX - minX
		}

		boxRect := image.Rect(minX, minY, maxX, maxY)
		bgColor := color.RGBA{R: 20, G: 20, B: 30, A: 240}
		draw.Draw(rgbaImg, boxRect, &image.Uniform{bgColor}, image.Point{}, draw.Src)

		borderColor := color.RGBA{R: 0, G: 240, B: 255, A: 255}
		drawRectOutline(rgbaImg, minX, minY, maxX, maxY, borderColor)

		textX := minX + 5
		textY := minY + ascent + (boxH-metrics.Height.Round())/2
		if textY < minY+ascent {
			textY = minY + ascent
		}

		d := &font.Drawer{
			Dst:  rgbaImg,
			Src:  image.NewUniform(color.RGBA{R: 255, G: 255, B: 255, A: 255}),
			Face: face,
			Dot:  fixed.P(textX, textY),
		}
		d.DrawString(text)
	}

	return rgbaImg
}

func testGradient(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetRGBA(x, y, color.RGBA{uint8(x * 3), uint8(y * 3), 128, 255})
		}
	}
	return img
}

func pngBytes(img *image.RGBA) []byte {
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		panic(err)
	}
	return buf.Bytes()
}

func TestRenderOCRBoxesParity(t *testing.T) {
	img := testGradient(320, 200)
	results := []OCRResult{
		{Text: "Hello World", Bounds: OCRBounds{Min: OCRPoint{X: 10, Y: 20}, Max: OCRPoint{X: 200, Y: 60}}},
		{Text: "你好世界", Bounds: OCRBounds{Min: OCRPoint{X: 50, Y: 90}, Max: OCRPoint{X: 260, Y: 150}}},
		{Text: "", Bounds: OCRBounds{Min: OCRPoint{X: 0, Y: 0}, Max: OCRPoint{X: 10, Y: 10}}},
	}
	got := pngBytes(RenderOCRBoxes(img, results))
	want := pngBytes(legacyRenderOCRBoxes(img, results))
	if !bytes.Equal(got, want) {
		t.Fatal("RenderOCRBoxes output diverges from the legacy overlay render path")
	}
}

func TestRenderOCRBoxesPreservesInput(t *testing.T) {
	img := testGradient(100, 60)
	out := RenderOCRBoxes(img, nil)
	if out.Rect != img.Bounds() {
		t.Fatalf("output size %v != input bounds %v", out.Rect, img.Bounds())
	}
	if !bytes.Equal(pngBytes(out), pngBytes(img)) {
		t.Fatal("RenderOCRBoxes modified the image with no OCR results")
	}
}

func TestHasCJK(t *testing.T) {
	if !hasCJK("你好") || hasCJK("hello") {
		t.Fatal("hasCJK classification mismatch")
	}
}
