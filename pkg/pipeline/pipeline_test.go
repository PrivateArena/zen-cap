package pipeline

import (
	"context"
	"image"
	"testing"

	"zen-cap/pkg/capture"
	"zen-cap/pkg/config"
)

func defaultTestCfg() *config.Config {
	cfg := config.DefaultConfig()
	cfg.CurrentTaskProfile = ""
	return cfg
}

func equal(ss1, ss2 []string) bool {
	if len(ss1) != len(ss2) {
		return false
	}
	for i := range ss1 {
		if ss1[i] != ss2[i] {
			return false
		}
	}
	return true
}

func TestResolveChainCaptureDefault(t *testing.T) {
	got := ResolveChain(SourceCapture, defaultTestCfg(), "")
	want := []string{"edit", "upload", "vision", "copy_image"}
	if !equal(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestResolveChainCaptureChosen(t *testing.T) {
	cfg := defaultTestCfg()
	cases := []struct {
		chosen string
		want   []string
	}{
		{"image", []string{"edit", "upload", "vision", "copy_image"}},
		{"path", []string{"edit", "upload", "vision", "copy_path"}},
		{"ocr", []string{"edit", "upload", "vision", "ocr", "copy_text"}},
		{"translate", []string{"edit", "upload", "vision", "ocr", "translate", "copy_text"}},
		{"none", []string{"edit", "upload", "vision"}},
	}
	for _, c := range cases {
		got := ResolveChain(SourceCapture, cfg, c.chosen)
		if !equal(got, c.want) {
			t.Errorf("chosen %q: got %v, want %v", c.chosen, got, c.want)
		}
	}
}

func TestResolveChainCaptureProfile(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.CurrentTaskProfile = "Copy Path"
	if got := ResolveChain(SourceCapture, cfg, ""); !equal(got, []string{"copy_path"}) {
		t.Fatalf("got %v, want [copy_path]", got)
	}

	cfg.CurrentTaskProfile = "Realtime Translate"
	if got := ResolveChain(SourceCapture, cfg, ""); !equal(got, []string{"edit", "upload", "vision", "copy_image"}) {
		t.Fatalf("non-applying profile leaked into capture chain: %v", got)
	}
}

func TestResolveChainOCRDefault(t *testing.T) {
	cfg := defaultTestCfg()
	got := ResolveChain(SourceOCR, cfg, "")
	want := []string{"ocr", "translate", "display"}
	if !equal(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestResolveChainOCRChosenText(t *testing.T) {
	cfg := defaultTestCfg()
	got := ResolveChain(SourceOCR, cfg, "translate")
	want := []string{"ocr", "translate", "copy_text", "display"}
	if !equal(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestResolveChainOCRChosenIgnored(t *testing.T) {
	cfg := defaultTestCfg()
	got := ResolveChain(SourceOCR, cfg, "path")
	want := []string{"ocr", "translate", "display"}
	if !equal(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestResolveChainOCRAutoCopy(t *testing.T) {
	cfg := defaultTestCfg()
	cfg.OCRAutoCopy = true
	got := ResolveChain(SourceOCRAuto, cfg, "")
	want := []string{"ocr", "translate", "copy_text", "display_live"}
	if !equal(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestResolveChainOCRAutoProfile(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.CurrentTaskProfile = "Realtime Translate"
	got := ResolveChain(SourceOCRAuto, cfg, "")
	want := []string{"ocr", "translate", "copy_text", "display_live"}
	if !equal(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestNewSkipsInvalid(t *testing.T) {
	p := New([]string{"edit", "invalid-task", "copy_text"}, nil)
	if len(p.tasks) != 2 {
		t.Fatalf("expected 2 tasks, got %d", len(p.tasks))
	}
	if p.tasks[0].Name() != "edit" || p.tasks[1].Name() != "copy_text" {
		t.Fatalf("unexpected task order: %v", []string{p.tasks[0].Name(), p.tasks[1].Name()})
	}
}

func TestTaskGating(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Edit.Enabled = true
	cfg.Uploader.Enabled = true
	cfg.Uploader.Endpoint = "http://example.com"
	cfg.Vision.Enabled = true
	img := image.NewRGBA(image.Rect(0, 0, 10, 10))
	cases := []struct {
		name string
		task Task
		r    *Result
		want bool
	}{
		{"ocr no image", OCRTask{}, &Result{}, false},
		{"ocr image", OCRTask{}, &Result{Image: img}, true},
		{"translate no boxes", TranslateTask{}, &Result{Image: img}, false},
		{"translate boxes", TranslateTask{}, &Result{OCRBoxes: []capture.OCRResult{{Text: "hi"}}}, true},
		{"copy_text no text", CopyTextTask{}, &Result{}, false},
		{"copy_text text", CopyTextTask{}, &Result{Text: "hi"}, true},
		{"copy_path no path", CopyPathTask{}, &Result{}, false},
		{"copy_image no path", CopyImageTask{}, &Result{Image: img}, false},
		{"copy_image both", CopyImageTask{}, &Result{Image: img, FilePath: "x.png"}, true},
		{"copy_url no url", CopyURLTask{}, &Result{}, false},
		{"copy_url url", CopyURLTask{}, &Result{UploadURL: "http://x"}, true},
		{"copy_llm no llm", CopyLLMTask{}, &Result{}, false},
		{"copy_llm llm", CopyLLMTask{}, &Result{LLMText: "hi"}, true},
		{"edit no image", EditTask{}, &Result{}, false},
		{"edit image", EditTask{}, &Result{Image: img}, true},
		{"upload no image", UploadTask{}, &Result{}, false},
		{"upload image", UploadTask{}, &Result{Image: img}, true},
		{"vision no path", VisionTask{}, &Result{Image: img}, false},
		{"vision both", VisionTask{}, &Result{Image: img, FilePath: "x.png"}, true},
		{"display no boxes", DisplayTask{}, &Result{Image: img}, false},
		{"display boxes", DisplayTask{}, &Result{Image: img, OCRBoxes: []capture.OCRResult{{Text: "hi"}}}, true},
	}
	for _, c := range cases {
		if got := c.task.Enabled(cfg, c.r); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}

type fakeSink struct {
	updated []*image.RGBA
}

func (f *fakeSink) Update(img *image.RGBA) error {
	f.updated = append(f.updated, img)
	return nil
}

func (f *fakeSink) Close() error { return nil }

func TestTerminalHalt(t *testing.T) {
	cfg := config.DefaultConfig()
	sink := &fakeSink{}
	box := capture.OCRResult{Text: "hi", Bounds: capture.OCRBounds{
		Min: capture.OCRPoint{X: 5, Y: 5},
		Max: capture.OCRPoint{X: 25, Y: 20},
	}}
	r := &Result{
		Image:    image.NewRGBA(image.Rect(0, 0, 50, 50)),
		OCRBoxes: []capture.OCRResult{box},
	}
	p := New([]string{"display_live", "display_live"}, &Options{DisplaySink: sink})
	p.Execute(context.Background(), cfg, r)
	if len(sink.updated) != 1 {
		t.Fatalf("expected 1 display update, got %d (terminal task did not halt the chain)", len(sink.updated))
	}
}

func TestRunNoop(t *testing.T) {
	cfg := defaultTestCfg()
	cfg.Edit.Enabled = false
	cfg.Uploader.Enabled = false
	cfg.Vision.Enabled = false

	img := image.NewRGBA(image.Rect(0, 0, 10, 10))
	res := Run(context.Background(), cfg, Seed{
		Kind:     KindImage,
		Source:   SourceCapture,
		Image:    img,
		FilePath: "test.png",
	})
	if res == nil {
		t.Fatal("expected non-nil result")
	}
	if res.FilePath != "test.png" {
		t.Errorf("expected FilePath 'test.png', got %q", res.FilePath)
	}
	if res.UploadURL != "" || res.LLMText != "" || res.Text != "" {
		t.Errorf("expected empty outputs, got url=%q llm=%q text=%q", res.UploadURL, res.LLMText, res.Text)
	}
}

func TestTranslateTask(t *testing.T) {
	old := capture.TranslateTextFn
	defer func() { capture.TranslateTextFn = old }()

	cfg := config.DefaultConfig()
	box := capture.OCRResult{Text: "hello", Bounds: capture.OCRBounds{
		Min: capture.OCRPoint{X: 0, Y: 0},
		Max: capture.OCRPoint{X: 40, Y: 15},
	}}
	r := &Result{OCRBoxes: []capture.OCRResult{box}}

	capture.TranslateTextFn = func(engine, addr, text, target string) (string, error) {
		if engine != cfg.TranslationEngine || target != cfg.TranslationTarget {
			t.Fatalf("unexpected args: %q %q", engine, target)
		}
		return "translated:" + text, nil
	}
	if err := (TranslateTask{}).Run(context.Background(), r, cfg, nil); err != nil {
		t.Fatal(err)
	}
	if r.OCRBoxes[0].Text != "translated:hello" || r.Text != "translated:hello" {
		t.Fatalf("translation not applied: %+v", r)
	}

	capture.TranslateTextFn = func(engine, addr, text, target string) (string, error) {
		return "", assertError{}
	}
	r = &Result{OCRBoxes: []capture.OCRResult{box}}
	if err := (TranslateTask{}).Run(context.Background(), r, cfg, nil); err != nil {
		t.Fatal(err)
	}
	if r.OCRBoxes[0].Text != "hello" || r.Text != "hello" {
		t.Fatalf("expected original text preserved on failure, got %+v", r)
	}
}

type assertError struct{}

func (assertError) Error() string { return "boom" }

