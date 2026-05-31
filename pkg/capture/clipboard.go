package capture

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"time"

	"golang.design/x/clipboard"
)

var (
	clipOnce sync.Once
	clipErr  error
)

// initClipboard ensures the native clipboard is initialized once.
func initClipboard() error {
	clipOnce.Do(func() {
		clipErr = clipboard.Init()
	})
	return clipErr
}

// CopyImageToClipboard copies raw PNG bytes to the system clipboard natively.
func CopyImageToClipboard(pngBytes []byte) error {
	if err := initClipboard(); err != nil {
		return fmt.Errorf("failed to initialize clipboard: %w", err)
	}
	if len(pngBytes) == 0 {
		return fmt.Errorf("cannot copy empty image to clipboard")
	}
	clipboard.Write(clipboard.FmtImage, pngBytes)
	return nil
}

// CopyTextToClipboard copies a text string to the system clipboard natively.
func CopyTextToClipboard(text string) error {
	if err := initClipboard(); err != nil {
		return fmt.Errorf("failed to initialize clipboard: %w", err)
	}
	clipboard.Write(clipboard.FmtText, []byte(text))
	return nil
}

// SpawnClipboardDaemon starts a detached background daemon to keep the clipboard selection alive.
func SpawnClipboardDaemon(mode string, payload string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := exec.Command(exe, "clipboard-daemon", mode, payload)
	return cmd.Start()
}

// RunClipboardServer handles the "clipboard-daemon" background command-line mode.
func RunClipboardServer(mode string, payload string) {
	err := initClipboard()
	if err != nil {
		os.Exit(1)
	}

	var format clipboard.Format
	if mode == "--image" {
		format = clipboard.FmtImage
		data, err := os.ReadFile(payload)
		if err != nil {
			os.Exit(1)
		}
		clipboard.Write(clipboard.FmtImage, data)
	} else if mode == "--text" {
		format = clipboard.FmtText
		clipboard.Write(clipboard.FmtText, []byte(payload))
	} else {
		os.Exit(1)
	}

	// Keep the daemon alive until another application overwrites the selection
	ch := clipboard.Watch(context.Background(), format)
	
	// Skip the initial event (which triggers on our own write)
	select {
	case <-ch:
	case <-time.After(200 * time.Millisecond):
	}

	// Wait for the next event and then exit cleanly
	<-ch
	os.Exit(0)
}

// ReadTextFromClipboard reads the current text content from the system clipboard.
// Returns empty string if clipboard contains no text.
func ReadTextFromClipboard() (string, error) {
	if err := initClipboard(); err != nil {
		return "", fmt.Errorf("failed to initialize clipboard: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	type result struct {
		data []byte
		err  error
	}
	resChan := make(chan result, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				resChan <- result{nil, fmt.Errorf("clipboard read panic: %v", r)}
			}
		}()
		data := clipboard.Read(clipboard.FmtText)
		resChan <- result{data, nil}
	}()

	select {
	case res := <-resChan:
		if res.err != nil {
			return "", res.err
		}
		return string(res.data), nil
	case <-ctx.Done():
		return "", fmt.Errorf("clipboard read text timeout")
	}
}

// ReadImageFromClipboard reads the current image (PNG) from the system clipboard.
// Returns nil if clipboard contains no image.
func ReadImageFromClipboard() ([]byte, error) {
	if err := initClipboard(); err != nil {
		return nil, fmt.Errorf("failed to initialize clipboard: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	type result struct {
		data []byte
		err  error
	}
	resChan := make(chan result, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				resChan <- result{nil, fmt.Errorf("clipboard read panic: %v", r)}
			}
		}()
		data := clipboard.Read(clipboard.FmtImage)
		resChan <- result{data, nil}
	}()

	select {
	case res := <-resChan:
		if res.err != nil {
			return nil, res.err
		}
		return res.data, nil
	case <-ctx.Done():
		return nil, fmt.Errorf("clipboard read image timeout")
	}
}

// ReadTextFromSelection reads text from the specified X11 selection ("primary" or "clipboard").
// Since golang.design/x/clipboard does not support the X11 PRIMARY selection, it uses xclip for "primary",
// and native Go library for "clipboard".
func ReadTextFromSelection(sel string) (string, error) {
	if sel == "clipboard" {
		return ReadTextFromClipboard()
	}

	// Use xclip for X11 PRIMARY selection
	cmd := exec.Command("xclip", "-o", "-selection", "primary")
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// ReadImageFromSelection reads a PNG image from the system clipboard natively.
func ReadImageFromSelection() ([]byte, error) {
	return ReadImageFromClipboard()
}
