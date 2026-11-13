package automation

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"zen-cap/pkg/capture"
	"zen-cap/pkg/config"
	"zen-cap/pkg/target"
)

// One capture, written to a file, with no side effects.
//
// This is deliberately NOT capture.CaptureScreen + pipeline.Run: the pipeline can
// copy to the clipboard, upload, call an LLM, or open an overlay that grabs the
// pointer and keyboard — which would deadlock the very input an agent is waiting
// for. Automation captures are pixels and nothing else.

// ShotSpec is the input of CaptureTo. Empty fields take the defaults: output under
// cfg.OutputDir, whole captured area.
type ShotSpec struct {
	Output string
	Region string
	// Config supplies OutputDir, which is where every relative output lands; nil
	// falls back to "." rather than a panic, since a library caller may legitimately
	// have no config. (Unlike `ocr`/`if_found`, a screenshot is not resolved against
	// the script directory: it is machine output, not a script asset.)
	Config *config.Config
}

type ShotResult struct {
	Path   string `json:"path"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
	Bytes  int    `json:"bytes"`
}

func (r ShotResult) String() string {
	return fmt.Sprintf("%s (%dx%d, %d bytes)", r.Path, r.Width, r.Height, r.Bytes)
}

// CaptureTo grabs the target's current frame, optionally crops it, and writes a
// PNG. The coordinates are in the target's own space, so for a window-scoped
// target a region addresses the client area (0,0 = client top-left).
func CaptureTo(t target.Target, spec ShotSpec) (*ShotResult, error) {
	if t == nil {
		return nil, fmt.Errorf("capture: no target")
	}
	img, err := t.Screenshot()
	if err != nil {
		return nil, fmt.Errorf("capture failed: %w", err)
	}

	if spec.Region != "" {
		b := img.Bounds()
		rx, ry, rw, rh, err := ParseRegion(spec.Region, b.Dx(), b.Dy())
		if err != nil {
			return nil, fmt.Errorf("invalid region %q: %w", spec.Region, err)
		}
		img, _, _ = CropImage(img, rx, ry, rw, rh)
	}

	base := ""
	if spec.Config != nil {
		base = spec.Config.OutputDir
	}
	path, err := ResolveOutputPath(spec.Output, base)
	if err != nil {
		return nil, err
	}

	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return nil, fmt.Errorf("cannot create output directory: %w", err)
	}
	if err := capture.SavePNG(img, path); err != nil {
		return nil, fmt.Errorf("cannot write %s: %w", path, err)
	}

	b := img.Bounds()
	res := &ShotResult{Path: path, Width: b.Dx(), Height: b.Dy()}
	if st, err := os.Stat(path); err == nil {
		res.Bytes = int(st.Size())
	}
	if abs, err := filepath.Abs(path); err == nil {
		res.Path = abs
	}
	return res, nil
}

// defaultShotPattern names an automatic screenshot; it is dated because agents
// capture repeatedly and want each frame kept.
const defaultShotPattern = "shot_20060102_150405.png"

// ResolveOutputPath turns a caller-supplied output path into one that is safe to
// write.
//
//   - ""              → <base>/automations/shot_<timestamp>.png
//   - "name.png"      → <base>/name.png
//   - "/abs/name.png" → exactly that (an agent that names /tmp gets /tmp)
//   - anything whose Clean()ed form escapes <base> (a ".." walk) → error
//
// The last rule is what makes `{"action":"screenshot","output":"../../etc/x"}` a
// refused request instead of a write outside the output directory.
func ResolveOutputPath(out, base string) (string, error) {
	if base == "" {
		base = "."
	}
	absBase, err := filepath.Abs(base)
	if err != nil {
		return "", fmt.Errorf("cannot resolve output directory: %w", err)
	}

	if out == "" {
		return filepath.Join(absBase, "automations", time.Now().Format(defaultShotPattern)), nil
	}

	if filepath.IsAbs(out) {
		return filepath.Clean(out), nil
	}

	clean := filepath.Clean(filepath.Join(absBase, out))
	rel, err := filepath.Rel(absBase, clean)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("output path %q escapes the output directory %s", out, absBase)
	}
	return clean, nil
}
