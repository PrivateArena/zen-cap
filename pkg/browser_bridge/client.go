package browser_bridge

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// ChatRequest defines the payload structure for calling the browser bridge chat action.
type ChatRequest struct {
	Action   string   `json:"action"`
	Provider string   `json:"provider"`
	Message  string   `json:"message"`
	Path     []string `json:"path"`
}

// CallChat sends a chat query to the local browser bridge API at the specified endpoint.
func CallChat(ctx context.Context, endpoint, provider, message string, paths []string) (string, error) {
	if endpoint == "" {
		endpoint = "http://127.0.0.1:9999"
	}
	reqBody := ChatRequest{
		Action:   "chat",
		Provider: provider,
		Message:  message,
		Path:     paths,
	}
	payload, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("failed to marshal bridge request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("bridge request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("bridge returned status %d: %s", resp.StatusCode, string(body))
	}

	// Try parsing standard response fields
	var parsed map[string]interface{}
	if err := json.Unmarshal(body, &parsed); err == nil {
		for _, key := range []string{"result", "response", "text", "content"} {
			if res, ok := parsed[key].(string); ok {
				return res, nil
			}
		}
	}
	return string(body), nil
}
