package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestServiceSignals(t *testing.T) {
	// Skip if we are running in a GUI-less environment without X11 DISPLAY
	if os.Getenv("DISPLAY") == "" {
		t.Skip("Skipping test because DISPLAY environment variable is not set")
	}

	// 1. Build the binary
	tmpDir, err := os.MkdirTemp("", "zencap-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	binPath := filepath.Join(tmpDir, "zen-cap-test")

	// Get workspace root
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get current working directory: %v", err)
	}

	rootPath := wd
	for {
		if _, err := os.Stat(filepath.Join(rootPath, "ffmpeg8")); err == nil {
			break
		}
		parent := filepath.Dir(rootPath)
		if parent == rootPath {
			break
		}
		rootPath = parent
	}

	ffmpegLibPath := filepath.Join(rootPath, "ffmpeg8", "lib")
	ffmpegIncludePath := filepath.Join(rootPath, "ffmpeg8", "include")

	cmdBuild := exec.Command("go", "build", "-o", binPath, ".")
	cmdBuild.Env = os.Environ()
	cmdBuild.Env = append(cmdBuild.Env,
		fmt.Sprintf("PKG_CONFIG_PATH=%s", filepath.Join(ffmpegLibPath, "pkgconfig")),
		fmt.Sprintf("CGO_CFLAGS=-I%s", ffmpegIncludePath),
		fmt.Sprintf("CGO_LDFLAGS=-L%s -Wl,-rpath,%s -Wl,--disable-new-dtags", ffmpegLibPath, ffmpegLibPath),
	)

	var buildBuf bytes.Buffer
	cmdBuild.Stderr = &buildBuf
	cmdBuild.Stdout = &buildBuf

	t.Log("Building zen-cap binary for testing...")
	if err := cmdBuild.Run(); err != nil {
		t.Fatalf("failed to build binary: %v\nOutput:\n%s", err, buildBuf.String())
	}

	// 2. Start the service subprocess in the temp directory
	// Write a local config.json in the temp directory to ensure outputs are saved there
	configContent := fmt.Sprintf(`{
  "output_dir": %q,
  "hotkeys": {
    "screenshot": "Control-Shift-s",
    "record_toggle": "Control-Shift-r"
  }
}`, tmpDir)
	if err := os.WriteFile(filepath.Join(tmpDir, "config.json"), []byte(configContent), 0644); err != nil {
		t.Fatalf("failed to write config.json in temp dir: %v", err)
	}

	cmdService := exec.Command(binPath, "service")
	cmdService.Dir = tmpDir
	cmdService.Env = os.Environ()
	cmdService.Env = append(cmdService.Env,
		fmt.Sprintf("LD_LIBRARY_PATH=%s", ffmpegLibPath),
	)

	var serviceOut bytes.Buffer
	cmdService.Stdout = &serviceOut
	cmdService.Stderr = &serviceOut

	t.Log("Starting zen-cap service...")
	if err := cmdService.Start(); err != nil {
		t.Fatalf("failed to start service: %v", err)
	}

	// Wait for startup initialization
	time.Sleep(1 * time.Second)

	// 3. Send SIGUSR1 to trigger screenshot
	t.Log("Sending SIGUSR1 to trigger screenshot...")
	if err := cmdService.Process.Signal(syscall.SIGUSR1); err != nil {
		t.Fatalf("failed to send SIGUSR1: %v", err)
	}

	// Wait for the screenshot file to be generated (max 5 seconds)
	screenshotFound := false
	var screenshotPath string
	for i := 0; i < 50; i++ {
		files, err := os.ReadDir(tmpDir)
		if err == nil {
			for _, file := range files {
				if strings.HasPrefix(file.Name(), "screenshot_") && strings.HasSuffix(file.Name(), ".png") {
					screenshotFound = true
					screenshotPath = filepath.Join(tmpDir, file.Name())
					break
				}
			}
		}
		if screenshotFound {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	if !screenshotFound {
		t.Errorf("expected screenshot file to be created, but none found. Service output:\n%s", serviceOut.String())
	} else {
		t.Logf("Screenshot successfully generated at %s", screenshotPath)
	}

	// 4. Send SIGUSR2 to start recording
	t.Log("Sending SIGUSR2 to start recording...")
	if err := cmdService.Process.Signal(syscall.SIGUSR2); err != nil {
		t.Fatalf("failed to send SIGUSR2 (start): %v", err)
	}

	// Wait for 2 seconds to capture some video frames
	time.Sleep(2 * time.Second)

	// 5. Send SIGUSR2 again to stop recording
	t.Log("Sending SIGUSR2 to stop recording...")
	if err := cmdService.Process.Signal(syscall.SIGUSR2); err != nil {
		t.Fatalf("failed to send SIGUSR2 (stop): %v", err)
	}

	// Wait for the recording file to be written and finalized (max 5 seconds)
	recordingFound := false
	var recordingPath string
	for i := 0; i < 50; i++ {
		files, err := os.ReadDir(tmpDir)
		if err == nil {
			for _, file := range files {
				if strings.HasPrefix(file.Name(), "recording_") && strings.HasSuffix(file.Name(), ".mp4") {
					recordingFound = true
					recordingPath = filepath.Join(tmpDir, file.Name())
					break
				}
			}
		}
		if recordingFound {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	if !recordingFound {
		t.Errorf("expected recording file to be created, but none found. Service output:\n%s", serviceOut.String())
	} else {
		t.Logf("Recording successfully generated at %s", recordingPath)
	}

	// 6. Terminate service with syscall.SIGINT
	t.Log("Sending SIGINT to stop service...")
	if err := cmdService.Process.Signal(syscall.SIGINT); err != nil {
		t.Errorf("failed to send SIGINT: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		done <- cmdService.Wait()
	}()

	select {
	case err := <-done:
		t.Logf("Service process exited cleanly: %v", err)
	case <-time.After(5 * time.Second):
		t.Log("Service process did not exit within 5 seconds, killing...")
		cmdService.Process.Kill()
		t.Fail()
	}

	t.Logf("Service stdout/stderr output:\n%s", serviceOut.String())
}
