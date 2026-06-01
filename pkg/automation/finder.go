package automation

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	_ "image/png" // Support PNG loading
	"math"
	"net/http"
	"net/url"
	"strings"

	"zen-cap/pkg/capture"
)

// FindImage searches for a needle image within a haystack image using Sum of Absolute Differences (SAD).
// It returns the X, Y coordinates of the center of the best match, and the confidence score.
func FindImage(haystack, needle image.Image, threshold float64) (int, int, float64, error) {
	hBounds := haystack.Bounds()
	nBounds := needle.Bounds()

	hW, hH := hBounds.Dx(), hBounds.Dy()
	nW, nH := nBounds.Dx(), nBounds.Dy()

	if nW > hW || nH > hH {
		return 0, 0, 0, fmt.Errorf("needle image is larger than haystack image")
	}

	var bestX, bestY int
	bestScore := math.MaxFloat64 // lower score is better

	// Performance optimization: downsample/skip steps for larger haystacks to find coarse match
	step := 1
	if hW > 500 || hH > 500 {
		step = 3
	}

	// Coarse pass
	for y := 0; y <= hH-nH; y += step {
		for x := 0; x <= hW-nW; x += step {
			score := computeSAD(haystack, needle, x, y, nW, nH, bestScore)
			if score < bestScore {
				bestScore = score
				bestX = x
				bestY = y
			}
		}
	}

	// Fine pass around the best candidate
	if step > 1 {
		coarseX, coarseY := bestX, bestY
		fineRange := step
		for y := coarseY - fineRange; y <= coarseY+fineRange; y++ {
			for x := coarseX - fineRange; x <= coarseX+fineRange; x++ {
				if x < 0 || y < 0 || x > hW-nW || y > hH-nH {
					continue
				}
				score := computeSAD(haystack, needle, x, y, nW, nH, bestScore)
				if score < bestScore {
					bestScore = score
					bestX = x
					bestY = y
				}
			}
		}
	}

	// Calculate confidence score (1.0 = perfect match, 0.0 = completely different)
	maxPotentialSAD := float64(255 * 3 * nW * nH)
	confidence := 1.0 - (bestScore / maxPotentialSAD)

	if confidence < threshold {
		return 0, 0, confidence, fmt.Errorf("best match confidence %f is below threshold %f", confidence, threshold)
	}

	// Return center coordinates relative to haystack
	centerX := bestX + nW/2
	centerY := bestY + nH/2

	return centerX, centerY, confidence, nil
}

func computeSAD(haystack, needle image.Image, startX, startY, nW, nH int, currentBest float64) float64 {
	var totalDiff float64
	for y := 0; y < nH; y++ {
		for x := 0; x < nW; x++ {
			nc := needle.At(x, y)
			nr, ng, nb, na := nc.RGBA()

			// Skip fully transparent pixels (support alpha masks)
			if na == 0 {
				continue
			}

			hc := haystack.At(startX+x, startY+y)
			hr, hg, hb, _ := hc.RGBA()

			// Convert to 8-bit color space (0-255)
			nRed, nGreen, nBlue := float64(nr>>8), float64(ng>>8), float64(nb>>8)
			hRed, hGreen, hBlue := float64(hr>>8), float64(hg>>8), float64(hb>>8)

			diff := math.Abs(nRed-hRed) + math.Abs(nGreen-hGreen) + math.Abs(nBlue-hBlue)
			totalDiff += diff

			// Early exit
			if totalDiff >= currentBest {
				return totalDiff
			}
		}
	}
	return totalDiff
}

type OCRPoint struct {
	X int `json:"X"`
	Y int `json:"Y"`
}

type OCRBounds struct {
	Min OCRPoint `json:"Min"`
	Max OCRPoint `json:"Max"`
}

type FullOCRResult struct {
	Text       string    `json:"Text"`
	Confidence float32   `json:"Confidence"`
	Bounds     OCRBounds `json:"Bounds"`
}

type fullRecognizeResponse struct {
	Results []FullOCRResult `json:"results"`
	Error   string          `json:"error,omitempty"`
}

// FindText captures a screenshot and sends it to the OCR server to locate target text.
func FindText(img image.Image, ocrAddr, lang, targetText string) (int, int, float64, error) {
	resolvedAddr, err := capture.EnsureOCRServer(ocrAddr)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("OCR server check failed: %w", err)
	}

	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return 0, 0, 0, fmt.Errorf("failed to encode PNG: %w", err)
	}

	// Try unified /ocr first, then fallback to /recognize
	endpoints := []string{
		fmt.Sprintf("%s/ocr?lang=%s", strings.TrimSuffix(resolvedAddr, "/"), url.QueryEscape(lang)),
		fmt.Sprintf("%s/recognize?lang=%s", strings.TrimSuffix(resolvedAddr, "/"), url.QueryEscape(lang)),
	}

	var resp *http.Response
	var lastErr error

	for _, ep := range endpoints {
		bufReader := bytes.NewReader(buf.Bytes())
		resp, err = http.Post(ep, "image/png", bufReader)
		if err == nil && resp.StatusCode == http.StatusOK {
			break
		}
		if err == nil {
			resp.Body.Close()
		}
		lastErr = err
	}

	if resp == nil || resp.StatusCode != http.StatusOK {
		return 0, 0, 0, fmt.Errorf("OCR server request failed: %v", lastErr)
	}
	defer resp.Body.Close()

	var ocrResp fullRecognizeResponse
	if err := json.NewDecoder(resp.Body).Decode(&ocrResp); err != nil {
		return 0, 0, 0, fmt.Errorf("failed to decode response: %w", err)
	}

	if ocrResp.Error != "" {
		return 0, 0, 0, fmt.Errorf("OCR server error: %s", ocrResp.Error)
	}

	targetLower := strings.ToLower(targetText)
	for _, res := range ocrResp.Results {
		if strings.Contains(strings.ToLower(res.Text), targetLower) {
			minX, minY := res.Bounds.Min.X, res.Bounds.Min.Y
			maxX, maxY := res.Bounds.Max.X, res.Bounds.Max.Y

			centerX := minX + (maxX-minX)/2
			centerY := minY + (maxY-minY)/2

			return centerX, centerY, float64(res.Confidence), nil
		}
	}

	return 0, 0, 0, fmt.Errorf("text %q not found in OCR results", targetText)
}
