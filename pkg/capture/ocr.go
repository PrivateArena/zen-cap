package capture

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type OCRResult struct {
	Text       string  `json:"Text"`
	Confidence float32 `json:"Confidence"`
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
	recognizeURL := fmt.Sprintf("%s/recognize?lang=%s", strings.TrimSuffix(resolvedAddress, "/"), url.QueryEscape(lang))

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

// TranslateText translates the given text to the target language using Google's free translation API.
func TranslateText(text string, targetLang string) (string, error) {
	if text == "" {
		return "", nil
	}

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
