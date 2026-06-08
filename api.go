// [VERIFIED]
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/jezek/xgb/xproto"
	"github.com/jezek/xgbutil"
	"github.com/jezek/xgbutil/ewmh"
	"github.com/jezek/xgbutil/icccm"
	"github.com/jezek/xgbutil/xwindow"

	"zen-cap/pkg/capture"
	"zen-cap/pkg/config"
)

type ScreenshotOptions struct {
	Output   string `json:"output"`
	Region   string `json:"region"`
	Window   string `json:"window"`
	PID      int    `json:"pid"`
	Class    string `json:"class"`
	Title    string `json:"title"`
	Launch   string `json:"launch"`
	ClipMode string `json:"clip_mode"`
}

type CollaborateRequest struct {
	URL string `json:"url"`
}

var (
	collaborateURL string
	collaborateMu  sync.Mutex
)

func checkAndTriggerCollaborate(path string) {
	collaborateMu.Lock()
	urlStr := collaborateURL
	collaborateURL = "" // Clear after one-shot trigger!
	collaborateMu.Unlock()

	if urlStr == "" {
		return
	}

	fmt.Printf("[API] Webhook triggered. Sending saved screenshot path to collaborate URL: %s\n", urlStr)

	go func() {
		absPath, err := filepath.Abs(path)
		if err != nil {
			absPath = path
		}
		payload := map[string]string{
			"path": absPath,
		}
		jsonBytes, err := json.Marshal(payload)
		if err != nil {
			log.Printf("[API] Failed to marshal collaborate payload: %v\n", err)
			return
		}
		resp, err := http.Post(urlStr, "application/json", bytes.NewBuffer(jsonBytes))
		if err != nil {
			log.Printf("[API] Webhook POST to %s failed: %v\n", urlStr, err)
			return
		}
		resp.Body.Close()
		fmt.Printf("[API] Webhook POST to %s completed successfully.\n", urlStr)
	}()
}

func startAPIServer(cfg *config.Config) {
	if cfg.APIAddress == "" {
		cfg.APIAddress = "localhost:4444"
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/screenshot", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var opts ScreenshotOptions
		if err := json.NewDecoder(r.Body).Decode(&opts); err != nil {
			http.Error(w, "Bad request: "+err.Error(), http.StatusBadRequest)
			return
		}

		path, err := captureScreenshotWithOptions(opts, cfg)
		if err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"status": "success",
			"path":   path,
		})
	})

	mux.HandleFunc("/collaborate", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var req CollaborateRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "Bad request: "+err.Error(), http.StatusBadRequest)
			return
		}

		collaborateMu.Lock()
		collaborateURL = req.URL
		collaborateMu.Unlock()

		fmt.Printf("[API] Registered collaborate webhook URL: %s\n", req.URL)

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"status": "success",
		})
	})

	fmt.Printf("[API] Starting HTTP API server on http://%s...\n", cfg.APIAddress)
	if err := http.ListenAndServe(cfg.APIAddress, mux); err != nil {
		log.Fatalf("[API] API server failed to start: %v\n", err)
	}
}

func captureScreenshotWithOptions(opts ScreenshotOptions, cfg *config.Config) (string, error) {
	var windowID uint32
	var interactive bool
	var windowSelect bool
	x, y, w, h := -1, -1, 0, 0

	xu, err := xgbutil.NewConn()
	if err != nil {
		return "", fmt.Errorf("failed to connect to X: %w", err)
	}
	defer xu.Conn().Close()

	if opts.Launch != "" || opts.PID > 0 || opts.Class != "" || opts.Title != "" {
		winID, err := resolveAndActivateWindow(xu, opts.PID, opts.Class, opts.Title, opts.Launch)
		if err != nil {
			return "", err
		}
		windowID = winID
	} else if opts.Window != "" {
		if opts.Window == "active" {
			activeWinID, err := ewmh.ActiveWindowGet(xu)
			if err != nil {
				return "", fmt.Errorf("failed to get active window: %w", err)
			}
			windowID = uint32(activeWinID)
		} else if opts.Window == "interactive" {
			interactive = true
			windowSelect = true
		} else {
			id, err := parseWindowID(opts.Window)
			if err == nil {
				windowID = id
			} else {
				// Try matching as title or class name directly
				winID, err := resolveAndActivateWindow(xu, 0, opts.Window, opts.Window, "")
				if err != nil {
					return "", fmt.Errorf("failed to find window matching %q: %w", opts.Window, err)
				}
				windowID = winID
			}
		}
	} else if opts.Region != "" {
		if opts.Region == "interactive" {
			interactive = true
		} else {
			_, err := fmt.Sscanf(opts.Region, "%d,%d,%d,%d", &x, &y, &w, &h)
			if err != nil {
				return "", fmt.Errorf("invalid region format X,Y,W,H: %w", err)
			}
		}
	}

	outputPath := opts.Output
	if outputPath == "" {
		outputPath = fmt.Sprintf("screenshot_%s.png", time.Now().Format("20060102_150405"))
	}
	if !filepath.IsAbs(outputPath) {
		outputPath = filepath.Join(cfg.OutputDir, outputPath)
	}
	if err := os.MkdirAll(filepath.Dir(outputPath), 0755); err != nil {
		return "", fmt.Errorf("failed to create output directory: %w", err)
	}

	var chosenAction string
	capCfg := capture.CaptureConfig{
		Display:         ":0.0",
		X:               x,
		Y:               y,
		Width:           w,
		Height:          h,
		WindowID:        windowID,
		Interactive:     interactive,
		WindowSelect:    windowSelect,
		ClipboardAction: &chosenAction,
	}

	img, err := capture.CaptureScreen(capCfg)
	if err != nil {
		return "", err
	}

	if err := capture.SavePNG(img, outputPath); err != nil {
		return "", err
	}

	absPath, err := filepath.Abs(outputPath)
	if err != nil {
		absPath = outputPath
	}

	clipAction := cfg.ClipboardMode
	if opts.ClipMode != "" {
		clipAction = opts.ClipMode
	}
	if chosenAction != "" {
		clipAction = chosenAction
	}
	processClipboardAction(img, absPath, clipAction, cfg)

	return absPath, nil
}

func resolveAndActivateWindow(xu *xgbutil.XUtil, pid int, class string, title string, launchCmd string) (uint32, error) {
	var spawnedPID int
	if launchCmd != "" {
		parts := strings.Fields(launchCmd)
		if len(parts) > 0 {
			cmd := exec.Command(parts[0], parts[1:]...)
			err := cmd.Start()
			if err != nil {
				return 0, fmt.Errorf("failed to launch application: %w", err)
			}
			spawnedPID = cmd.Process.Pid
			fmt.Printf("[API] Launched application %q (PID: %d). Waiting for window...\n", launchCmd, spawnedPID)
		}
	}

	deadline := time.Now().Add(5 * time.Second)
	for {
		clientIDs, err := ewmh.ClientListGet(xu)
		if err != nil {
			tree, err := xproto.QueryTree(xu.Conn(), xu.RootWin()).Reply()
			if err == nil {
				clientIDs = tree.Children
			}
		}

		for _, winID := range clientIDs {
			// Match by PID
			if pid > 0 || (spawnedPID > 0 && pid == 0 && class == "" && title == "") {
				targetPID := pid
				if targetPID == 0 {
					targetPID = spawnedPID
				}
				wPid, err := ewmh.WmPidGet(xu, winID)
				if err == nil && int(wPid) == targetPID {
					_ = activateAndRaiseWindow(xu, uint32(winID))
					return uint32(winID), nil
				}
			}
			// Match by Class
			if class != "" {
				classInfo, err := icccm.WmClassGet(xu, winID)
				if err == nil && classInfo != nil {
					if strings.Contains(strings.ToLower(classInfo.Class), strings.ToLower(class)) ||
						strings.Contains(strings.ToLower(classInfo.Instance), strings.ToLower(class)) {
						_ = activateAndRaiseWindow(xu, uint32(winID))
						return uint32(winID), nil
					}
				}
			}
			// Match by Title
			if title != "" {
				wTitle, err := ewmh.WmNameGet(xu, winID)
				if err != nil || wTitle == "" {
					wTitle, _ = icccm.WmNameGet(xu, winID)
				}
				if strings.Contains(strings.ToLower(wTitle), strings.ToLower(title)) {
					_ = activateAndRaiseWindow(xu, uint32(winID))
					return uint32(winID), nil
				}
			}
		}

		if time.Now().After(deadline) {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	if spawnedPID > 0 && pid == 0 && class == "" && title == "" {
		return 0, fmt.Errorf("timeout waiting for window with PID %d", spawnedPID)
	}
	return 0, fmt.Errorf("window not found (pid=%d, class=%q, title=%q)", pid, class, title)
}

func activateAndRaiseWindow(xu *xgbutil.XUtil, winID uint32) error {
	// Raise window
	xwindow.New(xu, xproto.Window(winID)).Stack(xproto.StackModeAbove)
	// Focus/Activate
	err := ewmh.ActiveWindowReq(xu, xproto.Window(winID))
	if err != nil {
		_ = xproto.SetInputFocus(xu.Conn(), xproto.InputFocusParent, xproto.Window(winID), xproto.TimeCurrentTime).Check()
	}
	// Give WM time to map/render
	time.Sleep(250 * time.Millisecond)
	return nil
}
