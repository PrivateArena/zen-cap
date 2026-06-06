package capture

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/image/font"
	"golang.org/x/image/font/basicfont"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
)

type OCRPoint struct {
	X int `json:"X"`
	Y int `json:"Y"`
}

type OCRBounds struct {
	Min OCRPoint `json:"Min"`
	Max OCRPoint `json:"Max"`
}

type OCRResult struct {
	Text       string    `json:"Text"`
	Confidence float32   `json:"Confidence"`
	Bounds     OCRBounds `json:"Bounds"`
}

type recognizeResponse struct {
	Results []OCRResult `json:"results"`
	Error   string      `json:"error,omitempty"`
}

// pingServer checks if the OCR server at addr is active.
func pingServer(addr string) bool {
	statusURL := fmt.Sprintf("%s/status", strings.TrimSuffix(addr, "/"))
	client := &http.Client{Timeout: 300 * time.Millisecond}
	resp, err := client.Get(statusURL)
	if err == nil && resp.StatusCode == http.StatusOK {
		resp.Body.Close()
		return true
	}
	return false
}

// EnsureOCRServer checks if the OCR server at ocrAddress is running.
// If it is not running, it tries standard local fallback ports (8765 and 8080).
// If no server is running, it attempts to launch the zen-lights ocr-server in the background.
// Returns the resolved running server address.
func EnsureOCRServer(ocrAddress string) (string, error) {
	// 1. Try to ping configured address
	if pingServer(ocrAddress) {
		return ocrAddress, nil
	}

	// 2. If it is localhost/127.0.0.1, check common fallback ports
	isLocal := strings.Contains(ocrAddress, "localhost") || strings.Contains(ocrAddress, "127.0.0.1")
	if isLocal {
		fallbacks := []string{"http://localhost:8765", "http://localhost:8080"}
		for _, f := range fallbacks {
			if f != ocrAddress && pingServer(f) {
				fmt.Fprintf(os.Stderr, "[OCR] Configured address %s is down, but active OCR server was found at %s. Reusing it!\n", ocrAddress, f)
				return f, nil
			}
		}
	}

	// 3. Server is down. Try to locate zen-lights binary and start it
	fmt.Fprintf(os.Stderr, "[OCR] Server at %s is down. Attempting to start zen-lights ocr-server...\n", ocrAddress)

	lightsDir := "/media/jang/home/Deve/zen-lights"
	binaryPaths := []string{
		filepath.Join(lightsDir, "zen-lights"),
		filepath.Join(lightsDir, "zenlights"),
	}

	var foundBinary string
	for _, p := range binaryPaths {
		if _, err := os.Stat(p); err == nil {
			foundBinary = p
			break
		}
	}

	if foundBinary == "" {
		return "", fmt.Errorf("OCR server is not running, and zen-lights binary was not found at %s", lightsDir)
	}

	// Extract address (host:port) from URL
	addr := ocrAddress
	addr = strings.TrimPrefix(addr, "http://")
	addr = strings.TrimPrefix(addr, "https://")

	// Start zen-lights in background
	cmd := exec.Command(foundBinary, "ocr-server", "-addr", addr)
	cmd.Dir = lightsDir

	// Open log file to store zen-lights outputs
	logFile, err := os.OpenFile(filepath.Join(lightsDir, "ocr_server_daemon.log"), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err == nil {
		cmd.Stdout = logFile
		cmd.Stderr = logFile
	} else {
		cmd.Stdout = nil
		cmd.Stderr = nil
	}

	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("failed to start zen-lights: %w", err)
	}

	// 4. Poll /status for up to 3 seconds until server is ready
	statusURL := fmt.Sprintf("%s/status", strings.TrimSuffix(ocrAddress, "/"))
	pollClient := &http.Client{Timeout: 200 * time.Millisecond}
	for i := 0; i < 15; i++ {
		time.Sleep(200 * time.Millisecond)
		resp, err := pollClient.Get(statusURL)
		if err == nil && resp.StatusCode == http.StatusOK {
			resp.Body.Close()
			fmt.Fprintln(os.Stderr, "[OCR] Automatically started zen-lights OCR server successfully!")
			return ocrAddress, nil
		}
	}

	return "", fmt.Errorf("started zen-lights but OCR server failed to respond on %s within 3 seconds", ocrAddress)
}

// PerformOCR sends an image to the zen-lights OCR server and returns the joined recognized text.
func PerformOCR(img image.Image, ocrAddress string, lang string) (string, error) {
	// First ensure the server is running (with fallback detection)
	resolvedAddress, err := EnsureOCRServer(ocrAddress)
	if err != nil {
		return "", fmt.Errorf("OCR server check failed: %w", err)
	}

	// Encode image to PNG bytes in memory
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return "", fmt.Errorf("failed to encode PNG for OCR: %w", err)
	}

	// Construct request URL using the resolved address
	recognizeURL := fmt.Sprintf("%s/ocr?lang=%s", strings.TrimSuffix(resolvedAddress, "/"), url.QueryEscape(lang))

	// Send POST request
	resp, err := http.Post(recognizeURL, "image/png", &buf)
	if err != nil {
		return "", fmt.Errorf("HTTP POST request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("OCR server returned status %s: %s", resp.Status, string(bodyBytes))
	}

	// Unmarshal response
	var ocrResp recognizeResponse
	if err := json.NewDecoder(resp.Body).Decode(&ocrResp); err != nil {
		return "", fmt.Errorf("failed to decode OCR JSON response: %w", err)
	}

	if ocrResp.Error != "" {
		return "", fmt.Errorf("OCR server error: %s", ocrResp.Error)
	}

	if len(ocrResp.Results) == 0 {
		return "", nil // No text detected
	}

	// Join all text blocks with newlines
	var lines []string
	for _, r := range ocrResp.Results {
		if r.Text != "" {
			lines = append(lines, r.Text)
		}
	}

	return strings.Join(lines, "\n"), nil
}

// TranslateText translates the given text using the configured translation engine ("google" or "local").
func TranslateText(engine, ocrAddress, text, targetLang string) (string, error) {
	if text == "" {
		return "", nil
	}

	if engine == "local" {
		resolvedAddress, err := EnsureOCRServer(ocrAddress)
		if err != nil {
			return "", fmt.Errorf("OCR server check failed: %w", err)
		}

		translateURL := fmt.Sprintf("%s/translate", strings.TrimSuffix(resolvedAddress, "/"))

		requestData := map[string]string{
			"text":   text,
			"target": targetLang,
		}
		jsonData, err := json.Marshal(requestData)
		if err != nil {
			return "", fmt.Errorf("failed to marshal translation request: %w", err)
		}

		resp, err := http.Post(translateURL, "application/json", bytes.NewBuffer(jsonData))
		if err != nil {
			return "", fmt.Errorf("translation HTTP request failed: %w", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			bodyBytes, _ := io.ReadAll(resp.Body)
			return "", fmt.Errorf("translation returned bad status %s: %s", resp.Status, string(bodyBytes))
		}

		type translateResponse struct {
			Translated string `json:"translated"`
			Error      string `json:"error,omitempty"`
		}

		var transResp translateResponse
		if err := json.NewDecoder(resp.Body).Decode(&transResp); err != nil {
			return "", fmt.Errorf("failed to decode translation response: %w", err)
		}

		if transResp.Error != "" {
			return "", fmt.Errorf("translation error: %s", transResp.Error)
		}

		return transResp.Translated, nil
	}

	// Default to Google Translate
	apiURL := fmt.Sprintf("https://translate.googleapis.com/translate_a/single?client=gtx&sl=auto&tl=%s&dt=t&q=%s",
		targetLang, url.QueryEscape(text))

	resp, err := http.Get(apiURL)
	if err != nil {
		return "", fmt.Errorf("translation HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("translation returned bad status: %s", resp.Status)
	}

	var raw []interface{}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return "", fmt.Errorf("failed to parse translation response: %w", err)
	}

	if len(raw) == 0 {
		return "", fmt.Errorf("empty translation response")
	}

	sentences, ok := raw[0].([]interface{})
	if !ok {
		return "", fmt.Errorf("invalid translation response format")
	}

	translated := ""
	for _, s := range sentences {
		sentence, ok := s.([]interface{})
		if ok && len(sentence) > 0 {
			if t, ok := sentence[0].(string); ok {
				translated += t
			}
		}
	}

	return translated, nil
}

func hasCJK(s string) bool {
	for _, r := range s {
		// CJK Unified Ideographs (Chinese)
		if r >= 0x4E00 && r <= 0x9FFF {
			return true
		}
		// Hangul Syllables (Korean)
		if r >= 0xAC00 && r <= 0xD7AF {
			return true
		}
		// Hiragana & Katakana (Japanese)
		if (r >= 0x3040 && r <= 0x309F) || (r >= 0x30A0 && r <= 0x30FF) {
			return true
		}
	}
	return false
}

func loadSystemFont(fontSize float64, preferCJK bool) (font.Face, error) {
	var fontPaths []string
	if preferCJK {
		fontPaths = []string{
			"/usr/share/fonts/truetype/droid/DroidSansFallbackFull.ttf",
			"/usr/share/fonts/truetype/droid/DroidSansFallback.ttf",
			"fonts/NotoSans-Regular.ttf",
			"../fonts/NotoSans-Regular.ttf",
			"/media/jang/home/Deve/zen-cap/fonts/NotoSans-Regular.ttf",
			"/usr/share/fonts/truetype/liberation/LiberationSans-Regular.ttf",
			"/usr/share/fonts/truetype/dejavu/DejaVuSans.ttf",
		}
	} else {
		fontPaths = []string{
			"fonts/NotoSans-Regular.ttf",
			"../fonts/NotoSans-Regular.ttf",
			"/media/jang/home/Deve/zen-cap/fonts/NotoSans-Regular.ttf",
			"/usr/share/fonts/truetype/liberation/LiberationSans-Regular.ttf",
			"/usr/share/fonts/truetype/dejavu/DejaVuSans.ttf",
			"/usr/share/fonts/truetype/droid/DroidSansFallbackFull.ttf",
			"/usr/share/fonts/truetype/droid/DroidSansFallback.ttf",
		}
	}

	var fontBytes []byte
	var err error
	for _, path := range fontPaths {
		fontBytes, err = os.ReadFile(path)
		if err == nil {
			break
		}
	}

	if err != nil {
		return basicfont.Face7x13, nil
	}

	f, err := opentype.Parse(fontBytes)
	if err != nil {
		return basicfont.Face7x13, nil
	}

	face, err := opentype.NewFace(f, &opentype.FaceOptions{
		Size:    fontSize,
		DPI:     72,
		Hinting: font.HintingNone,
	})
	if err != nil {
		return basicfont.Face7x13, nil
	}

	return face, nil
}

func drawRectOutline(img draw.Image, x0, y0, x1, y1 int, col color.Color) {
	for x := x0; x < x1; x++ {
		img.Set(x, y0, col)
		img.Set(x, y1-1, col)
	}
	for y := y0; y < y1; y++ {
		img.Set(x0, y, col)
		img.Set(x1-1, y, col)
	}
}

// PerformOCRWithDetails retrieves OCR results along with bounding boxes from the recognize endpoint.
func PerformOCRWithDetails(img image.Image, ocrAddress string, lang string) ([]OCRResult, error) {
	resolvedAddress, err := EnsureOCRServer(ocrAddress)
	if err != nil {
		return nil, fmt.Errorf("OCR server check failed: %w", err)
	}

	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, fmt.Errorf("failed to encode PNG for OCR: %w", err)
	}

	recognizeURL := fmt.Sprintf("%s/ocr?lang=%s", strings.TrimSuffix(resolvedAddress, "/"), url.QueryEscape(lang))

	resp, err := http.Post(recognizeURL, "image/png", &buf)
	if err != nil {
		return nil, fmt.Errorf("HTTP POST request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("OCR server returned status %s: %s", resp.Status, string(bodyBytes))
	}

	var ocrResp recognizeResponse
	if err := json.NewDecoder(resp.Body).Decode(&ocrResp); err != nil {
		return nil, fmt.Errorf("failed to decode OCR JSON response: %w", err)
	}

	if ocrResp.Error != "" {
		return nil, fmt.Errorf("OCR server error: %s", ocrResp.Error)
	}

	return ocrResp.Results, nil
}

// PerformOCROverlay executes the OCR pipeline, overlays the recognized/translated text onto the image,
// saves it as a PNG file in OutputDir, and displays it in an interactive overlay window.
func PerformOCROverlay(img image.Image, ocrAddress, ocrLanguage, translationTarget, translationEngine string, autoTranslate bool, outputDir string) error {
	results, err := PerformOCRWithDetails(img, ocrAddress, ocrLanguage)
	if err != nil {
		return fmt.Errorf("OCR failed: %w", err)
	}

	bounds := img.Bounds()
	rgbaImg := image.NewRGBA(bounds)
	draw.Draw(rgbaImg, bounds, img, bounds.Min, draw.Src)

	for _, res := range results {
		if res.Text == "" {
			continue
		}

		text := res.Text
		if autoTranslate {
			translated, err := TranslateText(translationEngine, ocrAddress, text, translationTarget)
			if err == nil && translated != "" {
				text = translated
			}
		}

		minX := res.Bounds.Min.X + bounds.Min.X
		minY := res.Bounds.Min.Y + bounds.Min.Y
		maxX := res.Bounds.Max.X + bounds.Min.X
		maxY := res.Bounds.Max.Y + bounds.Min.Y
		boxW := maxX - minX
		boxH := maxY - minY

		if boxW <= 0 || boxH <= 0 {
			continue
		}

		// Estimate text length in font-height units (characters)
		estimatedUnits := 0.0
		for _, r := range text {
			if (r >= 0x4E00 && r <= 0x9FFF) || (r >= 0xAC00 && r <= 0xD7AF) || (r >= 0x3040 && r <= 0x309F) || (r >= 0x30A0 && r <= 0x30FF) {
				estimatedUnits += 1.0 // CJK characters are square
			} else if r == ' ' {
				estimatedUnits += 0.35
			} else {
				estimatedUnits += 0.55 // Latin/other characters are usually narrower
			}
		}
		if estimatedUnits < 1.0 {
			estimatedUnits = 1.0
		}

		fontSizeByHeight := float64(boxH) * 0.85
		fontSizeByWidth := (float64(boxW) / estimatedUnits) * 0.90 // leave 10% horizontal padding

		// If DBNet under-detected the text height, allow scaling up using the width-based estimate
		// (up to 3x the height-based estimate, clamped to 50% of the total image height)
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

		// Loop to dynamically shrink font size to fit box width if it exceeds it,
		// but do not shrink below minFontSize.
		var face font.Face
		var metrics font.Metrics
		var textWidth int
		for {
			var err error
			face, err = loadSystemFont(fontSize, hasCJK(text))
			if err != nil {
				face = basicfont.Face7x13
			}

			// Measure text width using temporary drawer
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

		// Now adjust bounding box dimensions based on the final font size & text width
		metrics = face.Metrics()
		ascent := metrics.Ascent.Round()
		if face == basicfont.Face7x13 {
			ascent = 11
		}

		// Ensure box is high enough for the font
		requiredHeight := metrics.Height.Round() + 6
		if boxH < requiredHeight {
			centerY := (minY + maxY) / 2
			minY = centerY - requiredHeight/2
			maxY = centerY + requiredHeight/2
			boxH = maxY - minY
		}

		// Ensure box is wide enough for the text + padding
		requiredWidth := textWidth + 10
		if boxW < requiredWidth {
			centerX := (minX + maxX) / 2
			minX = centerX - requiredWidth/2
			maxX = centerX + requiredWidth/2
			boxW = maxX - minX
		}

		// Draw background box to erase original text
		boxRect := image.Rect(minX, minY, maxX, maxY)
		bgColor := color.RGBA{R: 20, G: 20, B: 30, A: 240} // Sleek dark mode background
		draw.Draw(rgbaImg, boxRect, &image.Uniform{bgColor}, image.Point{}, draw.Src)

		// Draw neon-cyan border outline
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

	timestamp := time.Now().Format("20060102_150405")
	filename := filepath.Join(outputDir, fmt.Sprintf("ocr_overlay_%s.png", timestamp))
	_ = os.MkdirAll(outputDir, 0755)
	if err := SavePNG(rgbaImg, filename); err != nil {
		return fmt.Errorf("failed to save OCR overlay image: %w", err)
	}
	fmt.Printf("[OCR Overlay] Saved overlay image to %s\n", filename)

	if err := ShowOCROverlayWindow(rgbaImg); err != nil {
		return fmt.Errorf("failed to show OCR overlay window: %w", err)
	}

	return nil
}
