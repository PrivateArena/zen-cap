# ARCHITECTURE_PLAN — Generalized Post-Process Pipeline (capture / OCR / OCR-auto / recording) — v2 Implementation Spec

## Summary

zen-cap's `pkg/pipeline` is a screenshot-only post-capture chain: a 4-task registry (`edit`, `upload`, `vision`, `clipboard`) over an image+PNG-path `Result`, invoked only by screenshot paths. OCR and recording never use it — OCR runs a monolithic `capture.PerformOCROverlay` (OCR + per-box translate + PNG save + *modal blocking X11 window*), and recording runs zero post-processing. This plan generalizes the pipeline into a **single engine with a typed `Result`** and granular tasks (`ocr`, `translate`, `copy_text`, `copy_path`, `copy_image`, `copy_url`, `copy_llm`, `display`, `display_live`) so chains like `["ocr","translate","copy_text"]` compose identically across screenshots, OCR screenshots, the realtime OCR auto-toggle loop (persistent updating overlay for game-speech translation), and recordings (copy path + placeholders). **Breaking config change, accepted by user:** `ClipboardMode` and `TaskProfile.ClipboardMode` are deleted; profiles become pure task lists. v2 adds: all open questions answered, a corrected `chosenAction` rule (it overrides only the output segment, preserving edit/upload/vision), the exact artifact-gating table, the persistent-overlay lifecycle, the race fix, a file-by-file change list, and a test strategy.

---

## Open Questions — Answered

| # | Question | Answer |
|---|---|---|
| OQ1 | Persistent overlay render throughput at 5fps | **Feasible.** The per-tick render is the same work the modal path already does per invocation (font fit + BGRA upload + `CopyArea`). For a small region (speech box ≈ 300×100) this is trivial; **the real latency bound is the remote OCR server round-trip**, so the wall-clock ticker self-limits. One real cost: `loadSystemFont` re-parses a TTF per box per tick → **cache `font.Face` keyed by (size bucket, CJK)** in the renderer. Reuse one pixmap + `CopyArea` (pattern from magnifier, PROJECT_OVERVIEW pattern #4). |
| OQ2 | Pixel-identical `RenderOCRBoxes` extraction | Mechanical: move ocr.go:415-555 verbatim into `RenderOCRBoxes(img, boxes) *image.RGBA`. **Diff-test**: unit test runs `PerformOCROverlay`'s old render path vs the extracted function on the same fixture and byte-compares `png.Encode` output. Extraction is low-risk; refactor drift is the risk, and this test kills it. |
| OQ3 | Google translate rate limits per-box at 5fps | The repo default is **0.5fps** (config.json:46) and a speech box has ~1-4 boxes → ~1-2 calls/sec, safely under limits. Worst case (5fps × 4 boxes = 20 calls/sec) risks throttling, but this is the **status quo** — the current auto-loop already does per-box translate per tick. Mitigation documented as follow-up (text-diff per box, translate only changed boxes); not required for v1. |
| OQ4 | `TaskProfile.AppliesTo` empty default | **Defaults to `["capture"]`** — preserves today's semantics (profiles affect screenshots only) unless explicitly opted into `ocr`/`ocr_auto`/`record`. Normalized in `readConfig`. |
| OQ5 | `ocr_auto_copy` flag vs pure config | **Keep the flag**, but implement it as one line in `ResolveChain` (if `cfg.OCRAutoCopy && source == ocr_auto` → append `copy_text`). It maps to the existing FPS-toggle UX and keeps chains user-editable without editing them. |

---

## System Boundaries & Component Breakdown

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                               SOURCES (service_*.go)                        │
│  capture │  ocr(shot/region/window) │ ocr_auto (persistent loop) │ record   │
│   │              │                         │  owns                       │
│   │              │                         │  PersistentOverlay           │
│   ▼              ▼                         ▼  (sink)                       ▼
│  ┌───────────────────────────────────────────────────────────────────────┐  │
│  │                      pkg/pipeline  (single engine)                    │  │
│  │   Seed{Source,Kind,Image,FilePath,Chosen,Quiet}                       │  │
│  │     → ResolveChain(source,cfg,chosen) → []task names                  │  │
│  │     → pipeline.New(names, Options{DisplaySink})                       │  │
│  │     → run tasks in order; halt after Terminal() task                  │  │
│  │   Result{Kind, Source, Quiet, Image, FilePath, Text,                  │  │
│  │           OCRBoxes, UploadURL, LLMText}                               │  │
│  └───────────────────────────────────────────────────────────────────────┘  │
│       │                    │                     │                          │
│       ▼                    ▼                     ▼                          │
│  clipboard           OCR/LLM server       X11 overlay (modal / persistent)  │
│  (SpawnClipboardDaemon)                    └─ capture.PersistentOverlay     │
└─────────────────────────────────────────────────────────────────────────────┘
```

### Package dependency rule (unchanged layering)
`main` (service_*.go) → `pipeline` → `capture`, `config`. `pipeline` never imports `main`. The persistent-overlay sink crosses this boundary as an **interface** (`pipeline.DisplaySink`), implemented by `capture.PersistentOverlay`; `serviceState` constructs the concrete type and injects it. No import cycle.

### Component table

| Component | Responsibility | Change |
|---|---|---|
| `pipeline.Result` | Typed artifact flowing through tasks. `Kind` is **immutable input metadata** (image/file/text); gating is by **field presence** (see gating table) | Rewrite |
| `pipeline.Seed` | Per-invocation input struct passed to `Run` | New |
| `pipeline.Task` | `Name()` / `Enabled(cfg, r)` / `Requires() []string` / `Terminal() bool` / `Run(ctx, r, cfg)` | Interface change |
| `pipeline.Options` | `{DisplaySink DisplaySink}` — binds persistent overlay for loop context | New |
| `pipeline.DisplaySink` | `interface { Update(*image.RGBA) error; Close() error }` | New |
| `pipeline.New(names []string, opts *Options)` | Build chain, validate `Requires()` ordering + terminal placement | Rewrite |
| `pipeline.Run(ctx, cfg, seed, opts)` | Resolve chain → run → halt after terminal task | Rewrite |
| `ResolveChain(source, cfg, chosen) []string` | Single deterministic chain-resolution function (profile → per-source default → chosenAction override) | New |
| `ocr_task` | `image`→text; fills `Text` + `OCRBoxes` via `PerformOCRWithDetails` | New |
| `translate_task` | Per-box translate; sets joined `Text`; single call-set serves copy + display | New |
| `copy_text` / `copy_path` / `copy_image` / `copy_url` / `copy_llm` | Granular clipboard tasks via `SpawnClipboardDaemon` | New (replaces `clipboard_task.go`) |
| `display` (terminal) | `RenderOCRBoxes` + modal one-shot window (owns X11 lifecycle, `ShowOCROverlayWindow`); saves overlay PNG | New |
| `display_live` (terminal) | `RenderOCRBoxes` → route to `DisplaySink`; no window ownership | New |
| `capture.RenderOCRBoxes` | Pure compositing, extracted from `PerformOCROverlay`; **cached font faces** | Refactor |
| `capture.PersistentOverlay` | Non-modal X11 window (no grabs, click-through via empty input shape); `New`/`Update`/`Close` | New |
| `config.Config` | Adds `after_ocr_tasks`, `after_ocr_auto_tasks`, `after_record_tasks`, `OCRAutoCopy`; `TaskProfile{Tasks, AppliesTo}`; deletes `ClipboardMode` | Breaking |
| `serviceState` | `cfg atomic.Pointer[config.Config]`; `activeRecPath`; owns `PersistentOverlay` in loop | Change |
| `runOCRScreenshotLoop` / region / window | Build OCR seed → `pipeline.Run` (chain ends in `display` modal) | Rewrite |
| `runOCRAutoToggleLoop` | Per tick: capture → run chain → `display_live` updates sink; overlay created lazily from region dims | Rewrite |
| `runRecordToggleLoop` | After `rec.Stop()` success → `pipeline.Run` on `Kind=file` seed | Change |
| `api.go` `captureScreenshotWithOptions` | `opts.ClipMode` maps to chosenAction override instead of mutating `ClipboardMode` | Change |
| `record.go` (CLI) | After `rec.Stop()` → run record chain | Change |

---

## Interfaces & Signatures (implementation contract)

```go
// pkg/pipeline/pipeline.go

type Kind int
const (
    KindImage Kind = iota
    KindFile
    KindText
)

type Source int
const (
    SourceCapture Source = iota
    SourceOCR
    SourceOCRAuto
    SourceRecord
)

// Source is exposed as a string for config matching:
func (s Source) String() string // "capture","ocr","ocr_auto","record"

type Result struct {
    Kind       Kind
    Source     Source
    Quiet      bool               // loop sources set true: suppress notifications
    Image      image.Image
    FilePath   string             // png OR mp4 (artifact path)
    Text       string             // current text (OCR / translated)
    OCRBoxes   []capture.OCRResult
    UploadURL  string
    LLMText    string
}

type Seed struct {
    Source   Source
    Kind     Kind
    Image    image.Image
    FilePath string
    Chosen   string               // in-crop chosenAction: "", "image", "path", "ocr", "translate"
    Quiet    bool
}

type Task interface {
    Name() string
    Enabled(cfg *config.Config, r *Result) bool   // config gate AND artifact gate
    Requires() []string                           // task names that must appear earlier
    Terminal() bool                               // pipeline halts after this
    Run(ctx context.Context, r *Result, cfg *config.Config) error
}

type DisplaySink interface {
    Update(img *image.RGBA) error
    Close() error
}

type Options struct {
    DisplaySink DisplaySink
}

// New builds from an explicit ordered name list (NOT from cfg — resolution is caller's job).
func New(names []string, opts *Options) *Pipeline // validates Requires() ordering; warns if Terminal() not last

// Run is the only entry point. Resolves the chain, executes in order, halts at Terminal().
func Run(ctx context.Context, cfg *config.Config, seed Seed, opts *Options) *Result

// ResolveChain is exported for testing and for the auto-loop's preview tooling.
func ResolveChain(source Source, cfg *config.Config, chosen string) []string
```

**Breaking:** `Task.Enabled` drops its old single-arg form; old `Run(ctx, cfg, img, outputPath, clipboardOverride)` replaced by `Run(ctx, cfg, seed, opts)`. Existing task bodies (`EditTask`, `UploadTask`, `VisionTask`) migrate mechanically: `Enabled(cfg, r)` adds `r.Image != nil`; `r.OutputPath` → `r.FilePath`.

---

## Artifact Gating Table (what each task needs to run)

| Task | Gate (`Enabled(cfg, r)`) | Sets | Requires() |
|---|---|---|---|
| `ocr` | `r.Image != nil` | `Text`, `OCRBoxes` | — |
| `translate` | `len(r.OCRBoxes) > 0` | per-box `Text` + joined `Text` | `ocr` |
| `copy_text` | `r.Text != ""` | — | `ocr` |
| `copy_path` | `r.FilePath != ""` | — | — |
| `copy_image` | `r.Image != nil && r.FilePath != ""` | — | — |
| `copy_url` | `r.UploadURL != ""` | — | `upload` |
| `copy_llm` | `r.LLMText != ""` | — | `vision` |
| `edit` | `cfg.Edit.Enabled && r.Image != nil` | `Image` (re-saved) | — |
| `upload` | `cfg.Uploader.Enabled && cfg.Uploader.Endpoint != "" && r.Image != nil` | `UploadURL` | — |
| `vision` | `cfg.Vision.Enabled && r.Image != nil && r.FilePath != ""` | `LLMText` | — |
| `display` (terminal) | `r.Image != nil && len(r.OCRBoxes) > 0` | saves overlay PNG | `ocr` |
| `display_live` (terminal) | `r.Image != nil && len(r.OCRBoxes) > 0` | renders → sink | `ocr` |

- **`edit`/`upload`/`vision`/`display` on a `Kind=file` (recording) result**: gate short-circuits on `r.Image == nil` → skipped. This is the nil-deref guard (F4) AND the "record can't edit a video" rule in one check.
- `display` vs `display_live` are **distinct task names** (red-team #3): `display` owns a modal window; `display_live` routes to an injected sink. Chains for `ocr_auto` use `display_live`; chains for `ocr`/`capture` use `display`.

---

## Chain Resolution (single deterministic lookup)

```
ResolveChain(source, cfg, chosen) []string:
  1. profile := cfg.TaskProfiles[cfg.CurrentTaskProfile]
  2. if profile.AppliesTo ∋ source  → base := profile.Tasks
     else:
        capture → base := cfg.AfterCaptureTasks
        ocr     → base := cfg.AfterOCRTasks
        ocr_auto→ base := cfg.AfterOCRAutoTasks
        record  → base := cfg.AfterRecordTasks
  3. if chosen == "": goto DONE
  4. if source == capture:                       # chosenAction overrides ONLY the output segment
        seg := { "image"    → ["copy_image"],
                  "path"    → ["copy_path"],
                  "ocr"     → ["ocr","copy_text"],
                  "translate"→ ["ocr","translate","copy_text"] }[chosen]
        i := first index of any copy_* / ocr task in base   # the "output segment" start
        if i < 0: base = base + seg
        else:     base = base[:i] + seg                      # keeps edit/upload/vision intact
  5. if source ∈ {ocr, ocr_auto} and chosen ∈ {ocr, translate}:
        if "copy_text" ∉ base: base = base + ["copy_text"]   # text action appends copy
  6. DONE:
     if cfg.OCRAutoCopy && source == ocr_auto && "copy_text" ∉ base:
        base = base + ["copy_text"]
     return base
```

**Behavior fidelity note:** on `capture`, chosenAction no longer replaces the whole chain (my v1 said that — wrong). It replaces only the clipboard/output tail, so the pre-existing edit→upload→vision flow still runs, exactly as the current `ClipboardOverride` precedence does (clipboard_task.go:21-22). This was caught in the final analysis pass.

**Config for `current_task_profile` empty:** falls through to per-source defaults (step 2) — no special case needed.

---

## Per-Source Flows (caller wiring)

### Capture (screenshot paths — service_capture.go:47/87/128, screenshot.go:138, api.go:237)

```go
seed := pipeline.Seed{
    Source:   pipeline.SourceCapture,
    Kind:     pipeline.KindImage,
    Image:    img,
    FilePath: absPath,
    Chosen:   chosenAction,   // may be ""
}
pipeline.Run(context.Background(), cfg, seed, nil)
```
`api.go:234-236` change: `opts.ClipMode` (CLI/API flag) sets `seed.Chosen` when non-empty, **instead of** `cfg.ClipboardMode = opts.ClipMode`. CLI `-c` flag (screenshot.go:130-132) likewise.

### OCR single-shot (service_ocr.go:14-101)

```go
seed := pipeline.Seed{Source: pipeline.SourceOCR, Kind: pipeline.KindImage,
                      Image: img, Chosen: chosenAction}   // chosenAction now READ (was dead)
pipeline.Run(context.Background(), s.getCfg(), seed, nil)
```
- `runOCRScreenshotLoop`: `chosenAction` stays `""` (fullscreen, no interactive crop).
- `runOCRRegionScreenshotLoop` / `runOCRWindowScreenshotLoop`: `chosenAction` is already wired via `CaptureConfig.ClipboardAction` (service_ocr.go:52,85) — **pass it through**. Text actions append `copy_text`; `path`/`image` ignored with log (per Q8).
- The terminal `display` task reproduces today's modal overlay + `ocr_overlay_TIMESTAMP.png` save.
- **Delete** `capture.PerformOCROverlay` after refactor; `capture.PerformOCR` (joined text) retained for automation/helpers.

### OCR auto-loop (service_ocr.go:157-262) — persistent overlay

```go
func (s *serviceState) runOCRAutoToggleLoop(ch <-chan struct{}) {
    for range ch { go func() {
        // ...existing toggle logic (cancel old, wasRunning)...
        var overlay *capture.PersistentOverlay
        opts := &pipeline.Options{}
        ticker := time.NewTicker(...)
        defer func() { if overlay != nil { overlay.Close() } }()
        for {
            select { case <-cancel: return; default: }
            // reload cfg only if config file mtime changed (OQ-adjacent, red-team #13)
            // capture markedArea as today (capCfg from s.markedArea)
            img, err := capture.CaptureScreen(capCfg); if err != nil { /* skip tick */ }
            // lazy-create overlay sized to region (dimensions known pre-render)
            if overlay == nil {
                b := img.Bounds()
                overlay, err = capture.NewPersistentOverlay(img, offsetX, offsetY)
                if err == nil { opts.DisplaySink = overlay } else { /* notify once, continue */ }
            } else if !sameDims(overlay, img) {   // user re-marked a different area
                overlay.Close(); overlay, opts.DisplaySink = nil, nil
                continue
            }
            seed := pipeline.Seed{Source: pipeline.SourceOCRAuto, Kind: pipeline.KindImage,
                                  Image: img, Quiet: true}
            pipeline.Run(ctx, cfg, seed, opts)    // chain default: ocr>translate>display_live
            select { case <-cancel: return; case <-ticker.C: }
        }
    }()}
}
```
`display_live` runs last, renders boxes, calls `opts.DisplaySink.Update(...)`. `Quiet:true` → `Result.Quiet` suppresses per-task failure notifications (F1/F9). The cancel path `Close()`s the overlay (F8).

### Recording (service_recording.go:383-393)

```go
// at START: store the computed filename
s.activeRecPath = filename                 // new field on serviceState, under recMu
// at STOP:
} else {
    rec := s.activeRec; s.activeRec = nil
    path := s.activeRecPath
    go func() {
        if err := rec.Stop(); err != nil {
            fmt.Printf("Error stopping recorder: %v\n", err)
            return                                // F3: skip chain on failure
        }
        seed := pipeline.Seed{Source: pipeline.SourceRecord, Kind: pipeline.KindFile,
                              FilePath: path}
        pipeline.Run(context.Background(), s.getCfg(), seed, nil)  // default ["copy_path"]
    }()
}
```
`rec.Stop()` is **blocking** (recorder.go:115-116: `close(stopChan); <-doneChan`) so the file is fully finalized before the chain runs. The goroutine is intentional: the next recording may start concurrently (red-team #10, documented decision). `record.go` CLI does the same after its `rec.Stop()`.

---

## Persistent Overlay — Implementation Detail

```go
// pkg/capture/overlay_persistent.go
type PersistentOverlay struct {
    xu       *xgbutil.XUtil
    winID    xproto.Window
    gcID     xproto.Gcontext
    pixmapID xproto.Pixmap
    w, h     int
    mu       sync.Mutex
    closed   bool
}

func NewPersistentOverlay(init image.Image, winX, winY int) (*PersistentOverlay, error)
    // Copy the window-creation block from ShowOCROverlayWindow (window.go:735-823):
    // create window (override-redirect, size = init dims), pixmap, GC, BGRA upload, MapWindow.
    // DIFFERENCE #1: eventMask = 0 (no ButtonPress/KeyPress/Exposure).
    // DIFFERENCE #2: NO GrabPointer / GrabKeyboard (non-modal).
    // DIFFERENCE #3: apply EMPTY input shape via XShapeSetMask so clicks pass through
    //                to the game (mirror magnifier shapes.go pattern; if the X11
    //                SHAPE extension is unavailable, fall back to no-shape + document).
    // DIFFERENCE #4: no xevent.Main loop.

func (o *PersistentOverlay) Update(rgba *image.RGBA) error
    o.mu.Lock(); defer o.mu.Unlock()
    if o.closed { return ErrOverlayClosed }
    bgra := ImageToBGRA(rgba)                      // existing helper (x11.go:89)
    if err := UploadImageChunked(o.xu, o.pixmapID, o.gcID, o.xu.Screen().RootDepth,
                                o.w, o.h, bgra); err != nil { return err }
    xproto.CopyArea(o.xu.Conn(), o.pixmapID, o.winID, o.gcID, 0, 0, 0, 0, uint16(o.w), uint16(o.h))
    return nil

func (o *PersistentOverlay) Close() error          // DestroyWindow + FreePixmap + conn close; idempotent
```
- Threading: `Update` is called from the loop goroutine; mutex guards `closed` (hotkey stop could race a mid-tick render).
- Reuse: `ShowOCROverlayWindow` and `NewPersistentOverlay` share a private `createOverlayWindow(img, winX, winY, modal bool)` helper in `pkg/capture` to avoid drift.
- Font cache (OQ1): `loadSystemFont` → add `var fontCache sync.Map` keyed by `fmt.Sprintf("%.0f|%v", fontSize, preferCJK)`, store `font.Face`, `LoadOrStore` in `RenderOCRBoxes`.

---

## Failure Modes & Mitigations

| # | Failure | Mitigation |
|---|---|---|
| F1 | OCR server down during auto-loop | Skip tick, keep loop alive; print + single notification on first failure; `Result.Quiet` suppresses subsequent notification spam |
| F2 | Per-box translation partial failure | Keep original text for that box; chain continues (existing behavior) |
| F3 | `rec.Stop()` error | Notify, skip post-process chain entirely |
| F4 | `r.Image` nil on `Kind=file` result | Field-presence gating (table above) — **this also kills the confirmed nil-derefs at edit_task.go:62, upload_task.go:29** |
| F5 | Dependent task misordered (`copy_url` before `upload`, `copy_llm` before `vision`, `copy_text` before `ocr`) | `Requires() []string`; `pipeline.New` warns on violation |
| F6 | `display` misconfigured mid-chain / no terminal | `pipeline.New` warns if a `Terminal()` task isn't chain-last |
| F7 | Toggle pressed mid-tick (pipeline running) | Cancel channel checked between capture→run→render steps; mid-task interrupt unsupported (documented) |
| F8 | Persistent overlay X11 update fails / overlay closed mid-loop | `ErrOverlayClosed` → recreate once via `NewPersistentOverlay`; else stop loop cleanly |
| F9 | `s.cfg` concurrent read/write | `atomic.Pointer[config.Config]` (see below) |
| F10 | Google translate rate-limit at high fps | Status-quo cost; default 0.5fps is ~1-2 calls/sec; text-diff follow-up documented |
| F11 | Clipboard copy per tick overwrites user clipboard | `OCRAutoCopy` off by default; copy runs only if chain contains `copy_text` |
| F12 | Chain on file-only result (record) reaches image tasks | Gating skips them; record defaults to `["copy_path"]` |
| F13 | User re-marks area mid auto-loop (size change) | dims check → recreate overlay (see loop code) |
| F14 | Auto-loop overlay blocks game input | Empty input shape (click-through); falls back to documented no-shape |
| F15 | Profile cycled (hotkey) mid-loop | Auto-loop reads chain at `pipeline.Run` each tick from live cfg; profile change takes effect next tick — document this; no crash risk |

---

## Config Schema (breaking)

```go
type TaskProfile struct {
    Name      string   `json:"name"`
    Tasks     []string `json:"tasks"`
    AppliesTo []string `json:"applies_to"`   // "capture","ocr","ocr_auto","record"; empty → ["capture"]
}
// Config fields:
AfterCaptureTasks  []string `json:"after_capture_tasks"`    // default ["edit","upload","vision","copy_image"]
AfterOCRTasks      []string `json:"after_ocr_tasks"`        // default ["ocr","translate","display"]
AfterOCRAutoTasks  []string `json:"after_ocr_auto_tasks"`   // default ["ocr","translate","display_live"]
AfterRecordTasks   []string `json:"after_record_tasks"`     // default ["copy_path"]
OCRAutoCopy        bool     `json:"ocr_auto_copy"`          // default false
// DELETED: ClipboardMode, TaskProfile.ClipboardMode, and (documented) AutoTranslate is subsumed
//          by chain composition — translation runs iff `translate` is in the chain.
```
`readConfig` normalizations: empty `AppliesTo` → `["capture"]`; empty `Tasks` → empty chain (no-op pipeline is valid); defaults per source as above.

### Default profile migration table (manual — no auto-migration per user decision)

| Old profile | New |
|---|---|
| "LLM Vision": `["clipboard"]`+`llm-text` | `Tasks:["vision","copy_llm"]`, `AppliesTo:["capture"]` |
| "Copy Path": `["clipboard"]`+`path` | `Tasks:["copy_path"]` |
| "Copy Image": `["clipboard"]`+`image` | `Tasks:["copy_image"]` |
| "OCR": `["clipboard"]`+`ocr` | `Tasks:["ocr","copy_text"]` |
| "Translate": `["clipboard"]`+`translate` | `Tasks:["ocr","translate","copy_text"]` |
| **New (recommended)** "Realtime Translate" | `Tasks:["ocr","translate","copy_text","display_live"]`, `AppliesTo:["ocr_auto"]` |
| Root `after_capture_tasks` `["edit","upload","vision","clipboard"]` | `["edit","upload","vision","copy_image"]` |

`json.Unmarshal` silently ignores unknown fields, so old `clipboard_mode` keys do not break parsing — behavior just changes to the new defaults. Documented in upgrade notes.

---

## File-by-File Change List (implementation order)

**Phase 0 — scaffolding (no behavior change):**
1. `pkg/config/config.go`: add 4 new fields + `OCRAutoCopy`; rework `TaskProfile`; delete `ClipboardMode`; update `DefaultConfig`/`DefaultPortableConfig` (config.go:228-558); add `readConfig` normalization; keep `readConfig` defaults updated (config.go:753,837).
2. `config.json`: apply migration table manually.
3. `service_state.go`: add `cfg atomic.Pointer[config.Config]`; add `getCfg()/setCfg()`; add `activeRecPath string`.
4. Grep-replace all `s.cfg = freshCfg` → `s.setCfg(freshCfg)` and reads → `s.getCfg()` across `service_*.go`, `api.go`. **Do this before Phase 1** (red-team #1 precondition).

**Phase 1 — pipeline core (pure additive, tests green without wiring):**
5. `pkg/pipeline/pipeline.go`: `Kind`/`Source`/`Result`/`Seed`/`Options`/`DisplaySink`/`ResolveChain`/`New(names,opts)`/`Run(ctx,cfg,seed,opts)`.
6. `pkg/pipeline/ocr_task.go`, `translate_task.go`, `copy_task.go` (five copy tasks), `display_task.go`, `display_live_task.go`; **delete** `clipboard_task.go`.
7. `pkg/pipeline/edit_task.go`, `upload_task.go`, `vision_task.go`: migrate `Enabled(cfg,r)`, `r.OutputPath`→`r.FilePath`, nil-Image gates.
8. Unit tests: `ResolveChain` (all sources × chosen values × profile opt-in), gating table, `Requires()` validation, terminal-halt, display-after-copy ordering.

**Phase 2 — OCR refactor (no user-visible change yet):**
9. `pkg/capture/ocr.go`: extract `RenderOCRBoxes` (verbatim move of 415-555) + font cache; keep `PerformOCRWithDetails`; delete `PerformOCROverlay` at the end of Phase 3.
10. `pkg/capture/window.go`: extract `createOverlayWindow(img, winX, winY, modal bool)`; `ShowOCROverlayWindow` calls it with `modal=true`.
11. `pkg/capture/overlay_persistent.go`: `PersistentOverlay` + empty-input-shape helper (from magnifier shapes.go).
12. Diff-test: `RenderOCRBoxes` output byte-equals old path output.

**Phase 3 — wire sources:**
13. `service_capture.go` (3 call sites), `screenshot.go`, `api.go` → `Seed` + chosenAction mapping.
14. `service_ocr.go` → 3 single-shot loops rewrite + chosenAction passthrough; auto-loop rewrite (persistent overlay).
15. `service_recording.go` + `record.go` → record chain after stop.
16. `helpers.go` `processClipboardAction`: **delete** — its `ocr`/`translate` cases are superseded by `ocr`/`translate`/`copy_text` tasks; the interactive-crop paths now flow chosenAction into the pipeline.

**Phase 4 — verification:**
17. `go build ./...`; `go vet ./...`; run `-race` service smoke test; manual hotkey pass across all 6 capture modes + OCR + record.

---

## Test Strategy

| Level | What | How |
|---|---|---|
| Unit | `ResolveChain` matrix | Table-driven: 4 sources × 5 chosen values × profile/AppliesTo present-absent; assert exact name lists |
| Unit | Gating table | Each task with and without required fields; assert `Enabled` |
| Unit | Ordering validation | `copy_url` before `upload` → warning; `display` mid-chain → warning |
| Unit | Terminal halt | Chain `["ocr","translate","copy_text","display"]` → Result has copy side-effect, chain stopped |
| Unit | Render parity (OQ2) | `RenderOCRBoxes` vs legacy render path on fixture image → byte-equal PNG |
| Unit | `translate` per-box | Mock `TranslateText` via existing injection seam (capture.go already uses var indirection for `CaptureScreen`) — add `var TranslateTextFn = TranslateText` for testability |
| Integration | `go test -race` | Exercise two concurrent loop goroutines + `setCfg` to prove the race fix |
| Manual | Hotkey smoke | All 3 screenshot modes, 3 OCR modes, OCR auto toggle at 0.5/2fps with `copy_text`, record + copy-path |

---

## Red-Team Critique Summary (browser.chat, provider=claude) — disposition

| # | Critique | Disposition |
|---|---|---|
| 1 | `s.cfg` data race confirmed; more config surface multiplies it | **Folded**: atomic pointer, Phase 0 precondition |
| 2 | Persistent overlay can't live in stateless Task/per-run Result | **Folded**: lifetime in `serviceState`; `display_live` routes to `pipeline.Options.DisplaySink` |
| 3 | One `display` name, two behaviors is dishonest | **Folded**: split `display` (modal) / `display_live` (sink) |
| 4 | `chosenAction` already dead on OCR paths | **Folded**: wired through; text-only mapping (Q8); v2 additionally fixes the capture override to output-segment-only |
| 5 | translate per-box vs joined-text is two ops | **Folded**: single per-box task that also produces joined `Text`; cost documented (F10/OQ3) |
| 6 | No migration for `ClipboardMode` deletion | **Rejected** per user decision (clean break); manual table + unknown-field-ignored note |
| 7 | Terminal halting fails silently on misconfig | **Folded**: load-time warning in `pipeline.New` |
| 8 | Order-dependent tasks lack chain validation | **Folded**: `Requires() []string` |
| 9 | Reused task code spams notifications in loop | **Folded**: `Result.Quiet` for loop sources |
| 10 | Record post-pipeline races next recording; nil-Image | **Folded**: goroutine intentional; gating kills nil-derefs |
| 11 | `AppliesTo` must be consulted where profile matching happens | **Folded**: gating lives inside `ResolveChain` |
| 12 | Four override mechanisms over-engineered | **Partially folded**: single `ResolveChain`; per-source defaults retained per user (Q1/Q4/Q7) |
| 13 | Per-tick `LoadConfig` at 5Hz | **Folded**: reload on mtime change |
| 14 | EditTask/UploadTask unverifiable | **Resolved locally**: read both; nil-derefs confirmed and gated |

---

## Low-Confidence Items

- **I am not fully confident about** X11 click-through via empty input shape: the magnifier uses `XShape` (pkg/magnifier/shapes.go) but I have not verified the exact shape-mask API version this repo's xgb exposes. Implementation must probe for the SHAPE extension at runtime and degrade to documented no-shape.
- **I am not fully confident about** pixel-identical `RenderOCRBoxes` extraction — mitigated by the parity test (OQ2), not by inspection.
- **I am not fully confident about** translate rate limits at sustained 5fps — status quo cost, but the text-diff optimization should land before marketing >2fps realtime use.
