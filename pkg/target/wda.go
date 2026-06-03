package target

// WDATarget drives an iOS device via WebDriverAgent (WDA), a standard
// XCTest-based HTTP server that runs on the device. No jailbreak required.
// WDA must already be running and port-forwarded (e.g. via `tidevice` or
// `iproxy 8100 8100`).

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"io"
	"net/http"
	"strings"
)

// WDATarget is a Target backed by a WebDriverAgent HTTP server.
type WDATarget struct {
	base    string
	session string
	client  *http.Client
	w, h    int
	scale   float64
	scaledW int
	scaledH int
}

// NewWDATarget connects to a WDA server, creates a session, and determines
// screen dimensions via the first screenshot.
func NewWDATarget(cfg Config) (*WDATarget, error) {
	host := cfg.WDAHost
	if host == "" {
		host = "localhost:8100"
	}
	t := &WDATarget{
		base:   "http://" + host,
		client: &http.Client{},
		scale:  cfg.Scale,
	}

	if err := t.createSession(); err != nil {
		return nil, fmt.Errorf("wda: create session: %w", err)
	}

	// Probe screen dimensions
	img, err := t.doScreenshot()
	if err != nil {
		return nil, fmt.Errorf("wda: initial screenshot: %w", err)
	}
	b := img.Bounds()
	t.w, t.h = b.Dx(), b.Dy()
	if cfg.Scale > 0 && cfg.Scale != 1.0 {
		t.scaledW = int(float64(t.w) * cfg.Scale)
		t.scaledH = int(float64(t.h) * cfg.Scale)
	} else {
		t.scaledW, t.scaledH = t.w, t.h
	}
	return t, nil
}

type wdaResp struct {
	Value   json.RawMessage `json:"value"`
	Status  int             `json:"status"`
	Message string          `json:"message"`
}

func (t *WDATarget) get(path string, out interface{}) error {
	resp, err := t.client.Get(t.base + path)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var r wdaResp
	if err := json.Unmarshal(body, &r); err != nil {
		return fmt.Errorf("wda: decode response: %w", err)
	}
	if out != nil {
		return json.Unmarshal(r.Value, out)
	}
	return nil
}

func (t *WDATarget) post(path string, payload, out interface{}) error {
	data, _ := json.Marshal(payload)
	resp, err := t.client.Post(t.base+path, "application/json", bytes.NewReader(data))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var r wdaResp
	if err := json.Unmarshal(body, &r); err != nil {
		return fmt.Errorf("wda: decode response: %w", err)
	}
	if out != nil {
		return json.Unmarshal(r.Value, out)
	}
	return nil
}

func (t *WDATarget) createSession() error {
	var result struct {
		SessionID string `json:"sessionId"`
	}
	if err := t.post("/session", map[string]interface{}{
		"capabilities": map[string]interface{}{},
	}, &result); err != nil {
		return err
	}
	t.session = result.SessionID
	if t.session == "" {
		return fmt.Errorf("wda: empty session ID")
	}
	return nil
}

func (t *WDATarget) sessionPath(sub string) string {
	return "/session/" + t.session + sub
}

func (t *WDATarget) doScreenshot() (image.Image, error) {
	var b64 string
	if err := t.get("/screenshot", &b64); err != nil {
		return nil, err
	}
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return nil, fmt.Errorf("wda: base64 decode: %w", err)
	}
	return png.Decode(bytes.NewReader(raw))
}

func (t *WDATarget) Screenshot() (image.Image, error) {
	img, err := t.doScreenshot()
	if err != nil {
		return nil, err
	}
	if t.scale > 0 && t.scale != 1.0 {
		return scaleImg(img, t.scaledW, t.scaledH), nil
	}
	return img, nil
}

func (t *WDATarget) ScreenSize() (int, int) { return t.scaledW, t.scaledH }

func (t *WDATarget) deviceCoord(x, y int) (int, int) {
	if t.scale > 0 && t.scale != 1.0 {
		return int(float64(x) / t.scale), int(float64(y) / t.scale)
	}
	return x, y
}

func (t *WDATarget) Click(x, y int, button string) error {
	dx, dy := t.deviceCoord(x, y)
	return t.post(t.sessionPath("/wda/tap/0"), map[string]int{"x": dx, "y": dy}, nil)
}

func (t *WDATarget) Move(x, y int) error {
	// WDA W3C actions: pointer move
	dx, dy := t.deviceCoord(x, y)
	return t.post(t.sessionPath("/actions"), map[string]interface{}{
		"actions": []interface{}{map[string]interface{}{
			"type": "pointer",
			"id":   "finger1",
			"actions": []interface{}{
				map[string]interface{}{"type": "pointerMove", "x": dx, "y": dy},
			},
		}},
	}, nil)
}

func (t *WDATarget) Type(text string, _ int64) error {
	chars := make([]string, 0, len([]rune(text)))
	for _, r := range text {
		chars = append(chars, string(r))
	}
	return t.post(t.sessionPath("/wda/keys"), map[string]interface{}{
		"value": chars,
	}, nil)
}

func (t *WDATarget) Key(keys string) error {
	// Map to XCUITest key names
	parts := strings.Split(keys, "-")
	chars := make([]string, 0, len(parts))
	for _, p := range parts {
		chars = append(chars, wdaKeyName(strings.ToLower(p)))
	}
	return t.post(t.sessionPath("/wda/keys"), map[string]interface{}{
		"value": chars,
	}, nil)
}

func (t *WDATarget) Scroll(x, y, dx, dy int) error {
	devX, devY := t.deviceCoord(x, y)
	return t.post(t.sessionPath("/wda/dragfrompoint:topoint:"), map[string]interface{}{
		"fromX": devX, "fromY": devY,
		"toX": devX - dx*50, "toY": devY - dy*50,
		"duration": 0.3,
	}, nil)
}

func (t *WDATarget) Close() error {
	t.post(t.sessionPath(""), nil, nil) //nolint: DELETE session
	return nil
}

func wdaKeyName(name string) string {
	m := map[string]string{
		"return": "\n", "enter": "\n",
		"backspace": "\x08",
		"escape": "\x1b",
		"tab": "\t",
		"home": "\ue011",
		"end": "\ue012",
	}
	if v, ok := m[name]; ok {
		return v
	}
	return name
}
