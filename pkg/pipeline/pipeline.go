package pipeline

import (
	"context"
	"fmt"
	"image"

	"zen-cap/pkg/capture"
	"zen-cap/pkg/config"
)

// Kind is immutable input metadata describing the artifact flowing through the
// pipeline. Tasks gate on field presence, not on Kind alone.
type Kind int

const (
	KindImage Kind = iota
	KindFile
	KindText
)

// Source identifies which caller initiated the pipeline. It is matched against
// TaskProfile.AppliesTo entries via Source.String().
type Source int

const (
	SourceCapture Source = iota
	SourceOCR
	SourceOCRAuto
	SourceRecord
)

func (s Source) String() string {
	switch s {
	case SourceCapture:
		return "capture"
	case SourceOCR:
		return "ocr"
	case SourceOCRAuto:
		return "ocr_auto"
	case SourceRecord:
		return "record"
	}
	return "unknown"
}

// Result carries state through the pipeline. Each task reads prior results
// and may overwrite its own fields before returning.
type Result struct {
	Kind     Kind
	Source   Source
	Quiet    bool // loop sources set true: suppress notification spam
	Image    image.Image
	FilePath string // png OR mp4 artifact path
	Text     string // current text (OCR / translated)
	OCRBoxes []capture.OCRResult
	UploadURL string
	LLMText   string
	OffsetX   int // overlay window placement hint (region/window OCR)
	OffsetY   int
}

// Seed is the per-invocation input passed to Run.
type Seed struct {
	Source   Source
	Kind     Kind
	Image    image.Image
	FilePath string
	Chosen   string // in-crop chosenAction: "", "image", "path", "ocr", "translate", "none"
	Quiet    bool
	OffsetX  int
	OffsetY  int
}

// Task is a single unit of work in the post-capture pipeline.
type Task interface {
	Name() string
	Enabled(cfg *config.Config, r *Result) bool
	Requires() []string // task names that must appear earlier in the chain
	Terminal() bool     // pipeline halts after running this task
	Run(ctx context.Context, r *Result, cfg *config.Config, opts *Options) error
}

// DisplaySink receives rendered frames from display_live (implemented by the
// persistent overlay in pkg/capture).
type DisplaySink interface {
	Update(img *image.RGBA) error
	Close() error
}

// Options binds per-invocation extras such as the persistent overlay sink.
type Options struct {
	DisplaySink DisplaySink
}

// Pipeline is a fixed, ordered set of tasks.
type Pipeline struct {
	tasks []Task
	opts  *Options
}

func registry() map[string]Task {
	return map[string]Task{
		"edit":         EditTask{},
		"upload":       UploadTask{},
		"vision":       VisionTask{},
		"ocr":          OCRTask{},
		"translate":    TranslateTask{},
		"copy_text":    CopyTextTask{},
		"copy_path":    CopyPathTask{},
		"copy_image":   CopyImageTask{},
		"copy_url":     CopyURLTask{},
		"copy_llm":     CopyLLMTask{},
		"display":      DisplayTask{},
		"display_live": DisplayLiveTask{},
	}
}

// New builds a Pipeline from an explicit ordered name list. Resolution against
// config is the caller's job (ResolveChain). It warns on Requires() violations
// and on a Terminal() task that is not chain-last.
func New(names []string, opts *Options) *Pipeline {
	reg := registry()
	p := &Pipeline{opts: opts}
	terminalSeen := false
	for _, n := range names {
		t, ok := reg[n]
		if !ok {
			fmt.Printf("[pipeline] unknown task %q, skipping\n", n)
			continue
		}
		if terminalSeen {
			fmt.Printf("[pipeline] warning: task %q appears after terminal task %q and will never run\n", n, p.tasks[len(p.tasks)-1].Name())
		}
		for _, req := range t.Requires() {
			found := false
			for _, prev := range p.tasks {
				if prev.Name() == req {
					found = true
					break
				}
			}
			if !found {
				fmt.Printf("[pipeline] warning: task %q requires %q to appear earlier in the chain\n", n, req)
			}
		}
		p.tasks = append(p.tasks, t)
		if t.Terminal() {
			terminalSeen = true
		}
	}
	return p
}

// Run resolves the chain for the seed, executes the enabled tasks in order,
// and halts after the first Terminal() task. Task failures are logged and, for
// non-quiet sources, surfaced as a desktop notification.
func Run(ctx context.Context, cfg *config.Config, seed Seed, opts ...*Options) *Result {
	names := ResolveChain(seed.Source, cfg, seed.Chosen)
	r := &Result{
		Kind:     seed.Kind,
		Source:   seed.Source,
		Quiet:    seed.Quiet,
		Image:    seed.Image,
		FilePath: seed.FilePath,
		OffsetX:  seed.OffsetX,
		OffsetY:  seed.OffsetY,
	}

	var pOpts *Options
	if len(opts) > 0 {
		pOpts = opts[0]
	}
	p := New(names, pOpts)
	p.Execute(ctx, cfg, r)
	return r
}

// Execute runs the pipeline's enabled tasks in order against r and halts after
// the first Terminal() task. Task failures are logged and, for non-quiet
// sources, surfaced as a desktop notification.
func (p *Pipeline) Execute(ctx context.Context, cfg *config.Config, r *Result) {
	for _, t := range p.tasks {
		if !t.Enabled(cfg, r) {
			continue
		}
		if err := t.Run(ctx, r, cfg, p.opts); err != nil {
			msg := fmt.Sprintf("[pipeline] task %q failed: %v", t.Name(), err)
			fmt.Println(msg)
			if !r.Quiet {
				sendNotification("Zen-Cap Pipeline", msg)
			}
		}
		if t.Terminal() {
			break
		}
	}
}

// ResolveChain deterministically produces the ordered task-name list for a
// source: active profile (if it applies) → per-source default → chosenAction
// output-segment override → ocr_auto_copy append.
func ResolveChain(source Source, cfg *config.Config, chosen string) []string {
	var base []string
	profileApplied := false

	if cfg.CurrentTaskProfile != "" {
		for _, p := range cfg.TaskProfiles {
			if p.Name == cfg.CurrentTaskProfile {
				if contains(p.AppliesTo, source.String()) {
					base = append([]string(nil), p.Tasks...)
					profileApplied = true
				}
				break
			}
		}
	}

	if !profileApplied {
		switch source {
		case SourceCapture:
			base = append([]string(nil), cfg.AfterCaptureTasks...)
		case SourceOCR:
			base = append([]string(nil), cfg.AfterOCRTasks...)
		case SourceOCRAuto:
			base = append([]string(nil), cfg.AfterOCRAutoTasks...)
		case SourceRecord:
			base = append([]string(nil), cfg.AfterRecordTasks...)
		}
	}

	if chosen == "none" && source == SourceCapture {
		// "none" strips the output/clipboard segment entirely.
		if i := firstOutputIndex(base); i >= 0 {
			base = base[:i]
		}
		return base
	}

	if chosen != "" {
		var seg []string
		switch chosen {
		case "image":
			seg = []string{"copy_image"}
		case "path":
			seg = []string{"copy_path"}
		case "ocr":
			seg = []string{"ocr", "copy_text"}
		case "translate":
			seg = []string{"ocr", "translate", "copy_text"}
		}
		if seg != nil {
			if source == SourceCapture {
				// chosenAction overrides ONLY the output segment, preserving
				// edit/upload/vision at the head of the chain.
				i := firstOutputIndex(base)
				if i < 0 {
					base = append(base, seg...)
				} else {
					base = append(append([]string(nil), base[:i]...), seg...)
				}
			} else if (source == SourceOCR || source == SourceOCRAuto) &&
				(chosen == "ocr" || chosen == "translate") {
				// Text actions on OCR paths append a copy. Insert before any
				// terminal display task so it actually runs.
				base = insertCopyText(base)
			}
		} else if source == SourceOCR || source == SourceOCRAuto {
			fmt.Printf("[pipeline] chosen action %q ignored on source %q\n", chosen, source)
		}
	}

	if cfg.OCRAutoCopy && source == SourceOCRAuto {
		base = insertCopyText(base)
	}

	return base
}

// insertCopyText adds "copy_text" to the chain before the first terminal
// display task, or at the end when the chain has no display.
func insertCopyText(base []string) []string {
	if contains(base, "copy_text") {
		return base
	}
	for i, n := range base {
		if n == "display" || n == "display_live" {
			out := make([]string, 0, len(base)+1)
			out = append(out, base[:i]...)
			out = append(out, "copy_text")
			out = append(out, base[i:]...)
			return out
		}
	}
	return append(base, "copy_text")
}

// firstOutputIndex returns the index of the first clipboard/OCR output task in
// names, or -1 if the chain has no output segment.
func firstOutputIndex(names []string) int {
	for i, n := range names {
		switch n {
		case "ocr", "copy_text", "copy_path", "copy_image", "copy_url", "copy_llm":
			return i
		}
	}
	return -1
}

func contains(ss []string, s string) bool {
	for _, v := range ss {
		if v == s {
			return true
		}
	}
	return false
}

func sendNotification(title, msg string) {
	config.SendNotification(title, msg)
}
