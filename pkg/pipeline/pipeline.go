package pipeline

import (
	"context"
	"fmt"
	"image"

	"zen-cap/pkg/config"
)

// Result carries state through the pipeline. Each task reads prior results
// and may overwrite its own fields before returning.
type Result struct {
	Image             image.Image // current image; EditTask may replace it
	OutputPath        string      // current on-disk PNG path; EditTask may re-save it
	UploadURL         string      // set by UploadTask
	LLMText           string      // set by VisionTask
	ClipboardOverride string      // dynamic choices from capture overlay
}

// Task is a single unit of work in the after-capture pipeline.
type Task interface {
	Name() string
	Enabled(cfg *config.Config) bool
	Run(ctx context.Context, r *Result, cfg *config.Config) error
}

// Pipeline is a fixed, ordered set of enabled tasks.
type Pipeline struct {
	tasks []Task
}

func registry() map[string]Task {
	return map[string]Task{
		"edit":      EditTask{},
		"upload":    UploadTask{},
		"vision":    VisionTask{},
		"clipboard": ClipboardTask{},
	}
}

// New builds a Pipeline from cfg.AfterCaptureTasks, in the order given.
func New(cfg *config.Config) *Pipeline {
	reg := registry()
	names := cfg.AfterCaptureTasks
	if len(names) == 0 {
		names = []string{"edit", "upload", "vision", "clipboard"}
	}
	p := &Pipeline{}
	for _, n := range names {
		t, ok := reg[n]
		if !ok {
			fmt.Printf("[pipeline] unknown task %q in after_capture_tasks, skipping\n", n)
			continue
		}
		p.tasks = append(p.tasks, t)
	}
	return p
}

// Run executes all enabled tasks in order against a freshly captured image
// and returns the accumulated Result.
func Run(ctx context.Context, cfg *config.Config, img image.Image, outputPath string, clipboardOverride string) *Result {
	r := &Result{
		Image:             img,
		OutputPath:        outputPath,
		ClipboardOverride: clipboardOverride,
	}

	localCfg := *cfg
	if cfg.CurrentTaskProfile != "" {
		for _, p := range cfg.TaskProfiles {
			if p.Name == cfg.CurrentTaskProfile {
				localCfg.AfterCaptureTasks = p.AfterCaptureTasks
				localCfg.ClipboardMode = p.ClipboardMode
				// Enable overridden tasks dynamically
				localCfg.Edit.Enabled = false
				localCfg.Uploader.Enabled = false
				localCfg.Vision.Enabled = false
				for _, tName := range p.AfterCaptureTasks {
					switch tName {
					case "edit":
						localCfg.Edit.Enabled = true
					case "upload":
						localCfg.Uploader.Enabled = true
					case "vision":
						localCfg.Vision.Enabled = true
					}
				}
				break
			}
		}
	}

	for _, t := range New(&localCfg).tasks {
		if !t.Enabled(&localCfg) {
			continue
		}
		if err := t.Run(ctx, r, &localCfg); err != nil {
			fmt.Printf("[pipeline] task %q failed: %v\n", t.Name(), err)
		}
	}
	return r
}

func sendNotification(title, msg string) {
	config.SendNotification(title, msg)
}

