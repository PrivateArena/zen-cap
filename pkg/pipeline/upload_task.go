package pipeline

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"strings"
	"time"

	"zen-cap/pkg/config"
)

type UploadTask struct{}

func (UploadTask) Name() string { return "upload" }

func (UploadTask) Enabled(cfg *config.Config) bool {
	return cfg.Uploader.Enabled && cfg.Uploader.Endpoint != ""
}

func (UploadTask) Run(ctx context.Context, r *Result, cfg *config.Config) error {
	var imgBuf bytes.Buffer
	if err := png.Encode(&imgBuf, r.Image); err != nil {
		return fmt.Errorf("failed to encode image for upload: %w", err)
	}

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	fieldName := cfg.Uploader.FieldName
	if fieldName == "" {
		fieldName = "file"
	}
	part, err := writer.CreateFormFile(fieldName, baseName(r.OutputPath))
	if err != nil {
		return fmt.Errorf("failed to create multipart field: %w", err)
	}
	if _, err := io.Copy(part, &imgBuf); err != nil {
		return fmt.Errorf("failed to write image into multipart body: %w", err)
	}
	for k, v := range cfg.Uploader.ExtraFields {
		if err := writer.WriteField(k, v); err != nil {
			return fmt.Errorf("failed to write extra field %q: %w", k, err)
		}
	}
	if err := writer.Close(); err != nil {
		return fmt.Errorf("failed to finalize multipart body: %w", err)
	}

	timeout := time.Duration(cfg.Uploader.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	reqCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, cfg.Uploader.Endpoint, body)
	if err != nil {
		return fmt.Errorf("failed to build upload request: %w", err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())

	if cfg.Uploader.AuthHeader != "" {
		token := cfg.Uploader.AuthToken
		if cfg.Uploader.AuthTokenEnv != "" {
			if v := os.Getenv(cfg.Uploader.AuthTokenEnv); v != "" {
				token = v
			}
		}
		if token != "" {
			req.Header.Set(cfg.Uploader.AuthHeader, token)
		}
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("upload request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read upload response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("upload host returned %d: %s", resp.StatusCode, truncate(string(respBody), 300))
	}

	url, err := extractURL(respBody, cfg.Uploader.URLJSONPath)
	if err != nil {
		return fmt.Errorf("uploaded successfully but could not extract URL: %w", err)
	}
	r.UploadURL = url

	fmt.Printf("[Upload] %s\n", url)
	sendNotification("Zen-Cap Upload", fmt.Sprintf("Uploaded! %s", url))
	return nil
}

// extractURL walks a dotted JSON path (e.g. "data.link") into the response body.
func extractURL(body []byte, path string) (string, error) {
	if path == "" {
		path = "data.link"
	}
	var raw interface{}
	if err := json.Unmarshal(body, &raw); err != nil {
		return "", fmt.Errorf("response is not valid JSON: %w", err)
	}
	cur := raw
	for _, seg := range strings.Split(path, ".") {
		m, ok := cur.(map[string]interface{})
		if !ok {
			return "", fmt.Errorf("path segment %q: not an object", seg)
		}
		v, ok := m[seg]
		if !ok {
			return "", fmt.Errorf("path segment %q: not found", seg)
		}
		cur = v
	}
	s, ok := cur.(string)
	if !ok {
		return "", fmt.Errorf("value at path %q is not a string", path)
	}
	return s, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

func baseName(p string) string {
	if i := strings.LastIndexAny(p, `/\`); i >= 0 {
		return p[i+1:]
	}
	return p
}
