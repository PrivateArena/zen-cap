package automation

import (
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"zen-cap/pkg/config"
)

// --- ResolveOutputPath ----------------------------------------------------

func TestResolveOutputPath(t *testing.T) {
	base := t.TempDir()

	got, err := ResolveOutputPath("", base)
	if err != nil {
		t.Fatalf("empty output: %v", err)
	}
	if !strings.HasPrefix(got, filepath.Join(base, "automations", "shot_")) || !strings.HasSuffix(got, ".png") {
		t.Errorf("default name = %q, want a dated file under %s/automations", got, base)
	}
	// The default name must not collide when two captures land in the same second,
	// which is routine for an agent loop.
	if second := time.Now().Format("20060102_150405"); !strings.Contains(got, second) {
		t.Logf("note: default name %q is not obviously timestamped", got)
	}

	joined, err := ResolveOutputPath("a.png", base)
	if err != nil || joined != filepath.Join(base, "a.png") {
		t.Errorf("relative path = %q (err %v), want it under the output dir", joined, err)
	}

	abs := filepath.Join(t.TempDir(), "elsewhere.png")
	asGiven, err := ResolveOutputPath(abs, base)
	if err != nil || asGiven != abs {
		t.Errorf("absolute path = %q (err %v), want it used verbatim", asGiven, err)
	}

	for _, escape := range []string{"../../etc/passwd", "../outside.png", "a/../../b.png"} {
		if out, err := ResolveOutputPath(escape, base); err == nil {
			t.Errorf("%q was accepted as %q: a relative path must not escape the output dir", escape, out)
		}
	}

	// Nested but contained relative paths are fine — that is how sub-shots are named.
	nested, err := ResolveOutputPath("runs/1.png", base)
	if err != nil || nested != filepath.Join(base, "runs", "1.png") {
		t.Errorf("nested relative path = %q (err %v)", nested, err)
	}
}

// --- CaptureTo ------------------------------------------------------------

func TestCaptureToWritesAndCrops(t *testing.T) {
	dir := t.TempDir()
	mt := newMockTarget(gradientImage(100, 100))
	cfg := &config.Config{OutputDir: dir}

	res, err := CaptureTo(mt, ShotSpec{Output: "full.png", Config: cfg})
	if err != nil {
		t.Fatalf("capture: %v", err)
	}
	if res.Width != 100 || res.Height != 100 {
		t.Errorf("full capture is %dx%d, want 100x100", res.Width, res.Height)
	}
	if !filepath.IsAbs(res.Path) {
		t.Errorf("path %q should be absolute", res.Path)
	}

	// A region addresses the captured area, which for a window target is the
	// client rectangle: 10,10,20,20 must yield a 20x20 image.
	cropped, err := CaptureTo(mt, ShotSpec{Output: "crop.png", Region: "10,10,20,20", Config: cfg})
	if err != nil {
		t.Fatalf("crop: %v", err)
	}
	if cropped.Width != 20 || cropped.Height != 20 {
		t.Fatalf("crop is %dx%d, want 20x20", cropped.Width, cropped.Height)
	}
	f, err := os.Open(cropped.Path)
	if err != nil {
		t.Fatalf("open crop: %v", err)
	}
	defer f.Close()
	img, err := png.Decode(f)
	if err != nil {
		t.Fatalf("decode crop: %v", err)
	}
	if b := img.Bounds(); b.Dx() != 20 || b.Dy() != 20 {
		t.Errorf("PNG on disk is %dx%d, want 20x20", b.Dx(), b.Dy())
	}
	// The crop must actually be the requested window of pixels, not a resized copy.
	if !sameColor(img.At(0, 0), mt.screenshotImg.At(10, 10)) {
		t.Error("the crop did not start at (10,10)")
	}

	if _, err := CaptureTo(mt, ShotSpec{Output: "x.png", Region: "nonsense", Config: cfg}); err == nil {
		t.Error("an unparsable region must fail the step, not silently capture everything")
	}

	if _, err := CaptureTo(mt, ShotSpec{Output: "../../escape.png", Config: cfg}); err == nil {
		t.Error("a path escaping the output dir must be refused")
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(dir), "escape.png")); err == nil {
		t.Error("the refused path was written anyway")
	}

	failing := newMockTarget(nil)
	failing.screenshotErr = os.ErrClosed
	if _, err := CaptureTo(failing, ShotSpec{Config: cfg}); err == nil {
		t.Error("a capture failure must surface")
	}
}

func sameColor(a, b color.Color) bool {
	ar, ag, ab, _ := a.RGBA()
	br, bg, bb, _ := b.RGBA()
	return ar == br && ag == bg && ab == bb
}
