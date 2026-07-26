<!-- codegraph-file-count: 83 -->
# zen-cap — X11 Screen Capture & Automation Toolkit

## Purpose
zen-cap is an X11-native Linux utility for screen capture, screencast recording, GUI automation, and clipboard management, all driven by global hotkeys. Written in Go, it exposes a daemon/service mode with an HTTP API, a standalone CLI, and a pipeline system for post-capture tasks (clipboard, external editor, image upload, AI vision query). The automation engine supports scripted GUI actions (click, type, find-image, OCR) across multiple target backends (X11, ADB/Android, VNC, WDA/iOS, Xvfb).

## Architecture
```
Hotkey (XGrabKey) → ChordManager → handleService → serviceState loops
                                                      ├─ Capture (screenshot, region, window, color picker, OCR)
                                                      ├─ Recording (FFmpeg av/ → screen+audio → muxer)
                                                      ├─ Magnifier (fullscreen zoom × lens overlay)
                                                      ├─ Automation (script engine → target backend)
                                                      └─ Snippet (picker → manager → smart types)
      HTTP API (api.go) → captureScreenshotWithOptions → Pipeline
                                                          ├─ ClipboardTask
                                                          ├─ EditTask (annotator / external editor)
                                                          ├─ UploadTask
                                                          └─ VisionTask
```

## File Tree
```
zen-cap/
├── main.go                  CLI dispatch entry point
├── cli.go                   CLI flag parsing + usage
├── api.go                   HTTP API server (config, screenshot, collaborate)
├── service.go               Hotkey service loop + dispatch table
├── service_*.go             7 files: capture, ocr, recording, picker, misc loops
├── record.go                Recording mode handler
├── screenshot.go            Screenshot mode handler
├── helpers.go               Shared helpers (screen/window info, clipboard actions)
├── banner.go                Startup hotkey banner
├── pkg/config/              Configuration loading + defaults
├── pkg/capture/             Screen capture, region/window selection, color picker, OCR, clipboard
├── pkg/av/                  FFmpeg A/V (input device, encoder, muxer, scaler, audio device)
├── pkg/recorder/            Screen+audio recorder orchestrator
├── pkg/annotation/          Drawing annotator + command model + overlay renderer (X11)
├── pkg/automation/          Script engine, step execution, image/text finding
├── pkg/target/              Target abstraction (X11, ADB, VNC, WDA, VFB)
├── pkg/display/             Display interface + X11 implementation
├── pkg/magnifier/           Fullscreen zoom + lens magnifier service
├── pkg/snippet/             Text snippet manager, picker, smart types (time, emoji, IP, prompt)
├── pkg/pipeline/            Post-capture task pipeline (clipboard, edit, upload, vision)
├── pkg/clipboard/           Multi-slot clipboard manager + text transforms
├── pkg/prompt/              Prompt definition loading + skill content resolution
├── pkg/hotkey/              X11 hotkey chord manager
├── pkg/browser_bridge/      HTTP client for browser AI chat API
└── go.mod                   Module: zen-cap, Go 1.24
```

## Component Roles

### Backend (Go)

| File / Module | Role | LOC | Key Exports (with signatures) |
|---|---|---|---|
| `main.go` | CLI entry: dispatches to runCLI, handleService, handleScreenshot, handleRecord | ~7 | `main()` |
| `cli.go` | Flag parsing and dispatch | ~105 | `runCLI()`, `printUsage()` |
| `api.go` | HTTP API server: screenshot, collaborate, config | ~329 | `startAPIServer(cfg *Config)`, `captureScreenshotWithOptions(opts ScreenshotOptions, cfg *Config) (string, error)` |
| `service.go` | Main service loop: hotkey dispatch to all capture/record/snippet/automation/magnifier loops | ~408 | `handleService() error` |
| `service_state.go` | Shared service state + marked-area struct | ~46 | `struct serviceState`, `struct MarkedArea` |
| `service_channels.go` | Channel hub for service goroutines | ~47 | `struct serviceChannels`, `newServiceChannels() *serviceChannels` |
| `service_capture.go` | Screenshot/region/window capture loops | ~133 | `runScreenshotLoop(ch)`, `runRegionScreenshotLoop(ch)`, `runWindowScreenshotLoop(ch)` |
| `service_ocr.go` | OCR screenshot/region/window capture + cycle model loops | ~146 | `runOCRScreenshotLoop(ch)`, `runOCRCycleModelLoop(ch)` |
| `service_recording.go` | Recording loops: fullscreen, region, window, audio-only, annotate toggle | ~399 | `runRecordMarkFullscreenLoop(ch)`, `runRecordMarkRegionLoop(ch)`, `runRecordAnnotateLoop(ch)` |
| `service_picker.go` | Window class grab + color picker loops | ~108 | `runWindowClassGrabLoop(ch)`, `runColorPickerLoop(ch)` |
| `service_misc.go` | Signal handler, snippet cycle, task profile cycle loops | ~112 | `runSignalHandler(sigChan, ch)`, `runSnippetCycleModeLoop(ch)`, `runTaskProfileCycleLoop(ch)` |
| `record.go` | Recording mode: starts recorder, manages output | ~149 | `handleRecord() error` |
| `screenshot.go` | Screenshot mode: capture + pipeline | ~143 | `handleScreenshot() error` |
| `helpers.go` | Screen/window info, clipboard action processing, notifications | ~172 | `processClipboardAction(img, path, action, cfg)`, `sendNotification(title, message)`, `listScreens()` |
| `banner.go` | Hotkey banner print | ~44 | `printHotkeyBanner(cfg *Config)` |
| `pkg/config/config.go` | YAML config: load/save, default profiles, transform rules, notification | ~923 | `LoadConfig() (*Config, string, error)`, `DefaultConfig() *Config`, `SaveConfig(cfg, path) error` |
| `pkg/capture/capture.go` | Screen capture implementation via X11 | ~136 | `captureScreenImpl(cfg CaptureConfig) (image.Image, error)`, `SavePNG(img, path) error` |
| `pkg/capture/region.go` | Interactive region selection with X11 overlay | ~714 | `InteractiveSelectRegionExt(fullImg, outClipboardAction, outX, outY, outW, outH) (image.Image, error)` |
| `pkg/capture/window.go` | Interactive window selection + OCR overlay window | ~893 | `InteractiveSelectWindowExt(fullImg, outClipboardAction, outX, outY, outW, outH, outWinID) (image.Image, error)` |
| `pkg/capture/color_picker.go` | Interactive color picker with loupe | ~501 | `InteractiveColorPicker(fullImg, formatStr) (string, error)` |
| `pkg/capture/ocr.go` | OCR client: ensure server, perform OCR, translate, overlay | ~571 | `EnsureOCRServer(addr) (string, error)`, `PerformOCR(img, addr, lang) (string, error)`, `TranslateText(engine, addr, text, targetLang) (string, error)` |
| `pkg/capture/clipboard.go` | Native clipboard: copy image/text, clipboard daemon, read back | ~185 | `CopyImageToClipboard(pngBytes) error`, `SpawnClipboardDaemon(mode, payload) error`, `ReadImageFromClipboard() ([]byte, error)` |
| `pkg/capture/annotate.go` | Interactive annotation overlay launcher | ~46 | `InteractiveAnnotate(img, brushThickness, fontScale, display...) (*image.RGBA, error)` |
| `pkg/capture/magnifier.go` | Magnifier loupe rendering | ~172 | `NewMagnifier() *Magnifier`, `Render(xu, bufPixmapID, gcID, depth, rgbaImg, mx, my, ...)` |
| `pkg/capture/x11.go` | X11 image upload helpers (BGRA, chunked) | ~89 | `ImageToBGRA(img) []byte`, `UploadImageChunked(xu, drawable, gc, depth, w, h, bgraData) error` |
| `pkg/av/av.go` | FFmpeg library init | ~18 | `Init()` |
| `pkg/av/device.go` | FFmpeg input device (screen capture via x11grab) | ~235 | `OpenDevice(cfg DeviceConfig) (*InputDevice, error)`, `ReadFrame() (*astiav.Frame, error)` |
| `pkg/av/adevice.go` | FFmpeg audio input device (PulseAudio ALSA) | ~168 | `OpenAudioDevice(cfg AudioDeviceConfig) (*AudioDevice, error)`, `ReadFrame() (*astiav.Frame, error)` |
| `pkg/av/encoder.go` | H.264/AAC video/audio encoders | ~285 | `NewVideoEncoder(w, h, fps, bitrate, opts) (*VideoEncoder, error)`, `NewAudioEncoder(sampleRate, channels, bitrate) (*AudioEncoder, error)` |
| `pkg/av/muxer.go` | MP4/MKV muxer via FFmpeg | ~117 | `NewMuxer(path) (*Muxer, error)`, `AddStream(encCtx) (int, error)`, `WritePacket(pkt, streamIdx, encTimeBase) error` |
| `pkg/av/scaler.go` | Software pixel scaler (FFmpeg swscale) | ~58 | `NewScaler(srcW, srcH, srcFmt, dstW, dstH, dstFmt, algo) (*Scaler, error)`, `Scale(src, dst) error` |
| `pkg/recorder/recorder.go` | Recorder orchestrator: video+audio goroutines, FIFO, mux | ~583 | `NewRecorder(cfg RecorderConfig) *Recorder`, `Start() error`, `Stop() error`, `IsRecording() bool` |
| `pkg/display/display.go` | Display abstraction: screens, windows, active window | ~30 | `interface DisplayManager { GetScreens(), GetWindows(), GetActiveWindow(), Close() }` |
| `pkg/display/x11.go` | X11 DisplayManager implementation | ~182 | `NewX11DisplayManager() (*X11DisplayManager, error)` |
| `pkg/annotation/types.go` | Annotation types: Tool, Config, InputEvent | ~52 | `struct Config`, `struct InputEvent`, `DefaultConfig() Config` |
| `pkg/annotation/annotator.go` | Drawing annotator: tool state, undo, event handling | ~301 | `NewAnnotator(base *image.RGBA, cfg Config) *Annotator`, `HandleEvent(ev InputEvent) (bool, bool)` |
| `pkg/annotation/command.go` | Command model: stroke/text commands, undo log | ~82 | `interface Command { apply() }`, `struct UndoLog { Push(), Pop(), Replay() }` |
| `pkg/annotation/draw.go` | Primitive drawing: line, rect, circle, HUD text | ~90 | `drawLine(img, x0, y0, x1, y1, col, thickness)`, `drawRect(...)`, `drawCircle(...)` |
| `pkg/annotation/font.go` | Bitmap font rendering (scaled) | ~135 | `DrawStringScaled(img, s, x, y, col, scale)` |
| `pkg/annotation/overlay/overlay.go` | X11 annotation overlay: fullscreen compositing, event translation | ~419 | `NewX11Overlay(ann, cfg OverlayConfig) *X11Overlay`, `Start() error`, `Stop()`, `WaitDone() error` |
| `pkg/annotation/overlay/render_loop.go` | Render loop goroutine | ~24 | `runRenderLoop()` |
| `pkg/annotation/overlay/x11util.go` | X11 pixel upload, ARGB visual find, alpha compositing | ~162 | `findARGBVisual(screen) (Visualid, byte)`, `alphaOver(base, layer) *image.RGBA` |
| `pkg/automation/types.go` | Step, Script, TargetConfig types | ~76 | `struct Step`, `struct Script`, `struct TargetConfig` |
| `pkg/automation/engine.go` | Script execution engine: sequential steps, goto, stop, conditionals | ~523 | `RunScript(script Script, cfg *Config, scriptDir string, abortChan chan struct{}, logger func) error` |
| `pkg/automation/actions.go` | Step executors: click, move, type, key, wait, find-image, find-text, OCR, window, clipboard, file, command | ~649 | `ExecuteStep(step Step, ctx *ExecContext) error` |
| `pkg/automation/eval.go` | Variable interpolation, expression evaluation, condition parser | ~216 | `Interpolate(str, vars) string`, `evaluateCondition(cond, vars) (bool, error)`, `InterpolateStep(step, vars) Step` |
| `pkg/automation/finder.go` | Image template matching (SAD), OCR text finding, region parsing | ~343 | `FindImage(haystack, needle, threshold) (int, int, float64, error)`, `FindText(img, ocrAddr, lang, text) (int, int, float64, error)` |
| `pkg/automation/manager.go` | Automation script CRUD manager (YAML files) | ~159 | `NewManager(dirPath) (*Manager, error)`, `Save() error`, `GetAll() []Script`, `Add(script) error` |
| `pkg/automation/picker.go` | X11 popup picker for automation scripts | ~385 | `ShowPicker(mgr *Manager, cfg *Config) error` |
| `pkg/target/target.go` | Target abstraction interface + factory | ~94 | `interface Target { Screenshot, Click, Move, Type, Key, Scroll, Close }`, `New(cfg Config, windowID uint32) (Target, error)` |
| `pkg/target/x11.go` | X11 target: screenshot, mouse/key input via XTest | ~290 | `NewX11Target(cfg, windowID) (*X11Target, error)` |
| `pkg/target/adb.go` | Android ADB target: screencap, input via ADB shell | ~225 | `NewADBTarget(cfg) (*ADBTarget, error)` |
| `pkg/target/vnc.go` | VNC/RFB target: raw framebuffer, pointer/key events | ~447 | `NewVNCTarget(cfg) (*VNCTarget, error)` |
| `pkg/target/wda.go` | iOS WebDriverAgent target: HTTP to WDA REST API | ~226 | `NewWDATarget(cfg) (*WDATarget, error)` |
| `pkg/target/vfb.go` | Xvfb virtual framebuffer target (wraps scrcpy + X11) | ~151 | `NewVFBTarget(cfg) (*VFBTarget, error)` |
| `pkg/magnifier/config.go` | Magnifier config: mode, lens shape, zoom, hotkeys | ~132 | `struct Config`, `DefaultConfig() Config`, `Normalize()` |
| `pkg/magnifier/magnifier.go` | Magnifier service: fullscreen zoom + lens mode, event loop | ~451 | `NewService(cfg Config) *Service`, `Start() error`, `Stop()`, `CaptureCurrentView() *image.RGBA` |
| `pkg/magnifier/capture.go` | Screen capture via MIT-SHM or XGetImage fallback | ~251 | `newCapturer(xu, maxW, maxH) capturer` |
| `pkg/magnifier/hotkeys.go` | X11 hotkey grab/ungrab, scroll button grab, modifier handling | ~162 | `grabHotkey(xu, root, modMask, kc) error`, `matchesHotkey(ev, modMask, kc) bool` |
| `pkg/magnifier/monitors.go` | Multi-monitor detection via XRandR | ~87 | `detectMonitors(xu) ([]MonitorGeometry, error)` |
| `pkg/magnifier/overlay.go` | X11 overlay window: fullscreen + lens, double-buffer blit | ~222 | `createFullscreenOverlay(xu, mon)`, `createLensOverlay(xu, size, ls)` |
| `pkg/magnifier/render.go` | Image scaling, BGRA conversion, crosshair drawing | ~75 | `scaleImage(src, dstW, dstH, smooth) *image.RGBA`, `drawCrosshair(img, cx, cy)` |
| `pkg/magnifier/shapes.go` | XShape mask: circle/rect window shapes, OSD zoom label | ~223 | `applyWindowShape(xu, win, size, ls) error`, `drawOSD(img, zoom)` |
| `pkg/snippet/manager.go` | Snippet CRUD, paste, human-like typing | ~471 | `NewManager(filePath) (*Manager, error)`, `Paste(content, mode, prevFocusWin) error`, `TypeHumanly(xu, text) error` |
| `pkg/snippet/picker.go` | X11 snippet picker popup: search, preview, categories | ~865 | `ShowPicker(mgr *Manager, cfg *Config) error` |
| `pkg/snippet/smart.go` | Smart snippet state: time, emoji, IP, prompt resolution | ~215 | `struct SmartState`, `Content(format, skillsPath) string`, `CycleNext()`, `AppendQuery(rune)`, `resolve*Query()` |
| `pkg/snippet/smart_time.go` | Timezone-aware datetime smart snippet | ~332 | `newSmartState() *SmartState`, `tryResolveQuery()` |
| `pkg/snippet/smart_emoji.go` | Emoji search smart snippet | ~224 | `resolveEmojiQuery()` |
| `pkg/snippet/smart_ip.go` | Public IP fetch smart snippet | ~83 | `TriggerIPFetch(onDone func())` |
| `pkg/snippet/smart_prompt.go` | Prompt selector smart snippet | ~67 | `resolvePromptQuery()` |
| `pkg/pipeline/pipeline.go` | Pipeline runner: ordered task chain | ~110 | `New(cfg *Config) *Pipeline`, `Run(ctx, cfg, img, outputPath, clipboardOverride) *Result` |
| `pkg/pipeline/clipboard_task.go` | Pipeline task: copy result to clipboard | ~105 | `struct ClipboardTask`, `Run(ctx, r, cfg) error` |
| `pkg/pipeline/edit_task.go` | Pipeline task: external editor or built-in annotator | ~85 | `struct EditTask`, `Run(ctx, r, cfg) error` |
| `pkg/pipeline/upload_task.go` | Pipeline task: upload to remote (custom URL/JSON path) | ~147 | `struct UploadTask`, `Run(ctx, r, cfg) error` |
| `pkg/pipeline/vision_task.go` | Pipeline task: send to AI vision API via browser bridge | ~68 | `struct VisionTask`, `Run(ctx, r, cfg) error` |
| `pkg/clipboard/manager.go` | Multi-slot clipboard with transform cycling | ~342 | `NewManager(cfg) (*Manager, error)`, `CopyToSlot(n)`, `PasteFromSlot(n)`, `CycleTransform()` |
| `pkg/clipboard/transform.go` | Text transform rule engine (regex replace) | ~42 | `ApplyTransform(text string, rule TransformRule) string` |
| `pkg/prompt/loader.go` | YAML prompt definition loader | ~68 | `LoadPrompts(promptsPath) ([]PromptDef, error)` |
| `pkg/prompt/resolver.go` | Prompt content resolution with skill injection | ~31 | `ResolveContent(def PromptDef, skillsPath) string` |
| `pkg/prompt/skill.go` | Skill file loader | ~33 | `LoadSkillContent(skillsPath, id) (string, error)` |
| `pkg/prompt/types.go` | PromptDef struct | ~14 | `struct PromptDef` |
| `pkg/hotkey/chord.go` | X11 hotkey chord manager: register, start, dispatch | ~110 | `NewChordManager(X) *ChordManager`, `Register(hotkey, callback)`, `Start()` |
| `pkg/browser_bridge/client.go` | HTTP client for browser AI chat endpoint | ~66 | `CallChat(ctx, endpoint, provider, message, paths) (string, error)` |

## Cross-References

| File | Called by / calls | Why it's central |
|---|---|---|
| `pkg/av/adevice.go` | Called by: `pkg/recorder/recorder.go` (runAudio). Calls: `Close` | Highest reference count (99): audio device is consumed by recorder, capture, and multiple service loops |
| `helpers.go` | Called by: `api.go`, `service_*.go`, `record.go`, `screenshot.go`. Calls: display, clipboard, pipeline | Central utility hub for notifications, clipboard actions, screen/window queries used across all modes |
| `pkg/display/display.go` | Called by: `pkg/display/x11.go`, `helpers.go`, `pkg/capture/window.go`. Calls: GetScreens, GetWindows, GetActiveWindow | Core abstraction that every capture and automation path depends on for screen/window geometry |
| `pkg/annotation/annotator.go` | Called by: `annotation/overlay/overlay.go`, `pkg/capture/annotate.go`. Calls: HandleEvent, GetLayer, GetComposite | Central event-driven drawing engine for both capture annotation and recording overlay modes |
| `pkg/magnifier/magnifier.go` | Called by: `service.go`, `pkg/magnifier/capture.go`, `pkg/magnifier/hotkeys.go`. Calls: Start, lensLoop, fullscreenLoop | Orchestrates the fullscreen zoom and lens magnifier service with independent event/render loops |
| `pkg/target/adb.go` | Called by: `pkg/target/target.go` (factory), `pkg/automation/actions.go`. Calls: Screenshot, Click, Move, Type, Key | One of five target backends; ADB is the primary mobile automation target with the most complex protocol logic |

## Key Architectural Patterns

1. **Hotkey → Service Loop dispatch**: All interactive features are triggered by global X11 hotkeys registered in `pkg/hotkey/chord.go`. The `service.go` `handleService()` builds a dispatch table mapping hotkey names → lambdas that send on typed channels (`serviceChannels`). Each loop goroutine (`runScreenshotLoop`, etc.) blocks on its channel and performs the action. This avoids polling and keeps the hotkey response sub-millisecond.

2. **Pipeline task chain**: Post-capture processing follows the `pipeline.Task` interface (`Name()`, `Enabled(cfg)`, `Run(ctx, result, cfg)`). The `Pipeline` loads tasks by name from `config.AfterCaptureTasks` order. Each task mutates a shared `Result` (paths, text, image). This makes it trivial to add new post-capture stages without touching capture code.

3. **Target abstraction for automation**: All automation step executors in `pkg/automation/actions.go` operate against the `target.Target` interface (`Screenshot`, `Click`, `Move`, `Type`, `Key`, `Scroll`, `Close`). The factory `target.New(cfg, windowID)` selects the backend based on config type. This single interface supports X11 native, ADB/Android, VNC, WebDriverAgent/iOS, and Xvfb virtual framebuffer — the automation scripts are backend-agnostic.

4. **X11 overlay double-buffering**: Both the region/window selectors and the magnifier use a consistent pattern: create an X11 window with a pixmap double-buffer, render the composited image into an `image.RGBA`, upload via `PutImage` (with X11 chunking for large buffers), then `CopyArea` from the pixmap to the window. This eliminates flicker and keeps interactive overlays responsive at 60fps.

5. **FFmpeg av/ pipeline**: Recording uses a goroutine-per-stream model: the `Recorder` spawns a video capture goroutine (`av/device.go` → `av/encoder.go`) and an audio goroutine (`av/adevice.go` → `av/encoder.go` → `astiav.AudioFifo`), both feeding the same `av/muxer.go`. The FIFO handles audio/video interleaving. This avoids blocking captures on encoding.

6. **Smart snippet resolution**: The snippet system has a "smart snippet" subsystem (`pkg/snippet/smart*.go`) where each smart type (time, emoji, IP, prompt) implements `Content()`, `CycleNext()`, and query methods on a shared `SmartState` struct. The picker dynamically calls `syncSmartState()` on every redraw to reflect live data (current time, emoji search results, IP fetch status).

## Read Triggers

| If you need to... | Open these files |
|---|---|
| Add a new hotkey-triggered mode | `service.go` (handleService switch + channel dispatch), `service_channels.go` (add channel), new `service_*.go` loop |
| Add a new pipeline task | `pkg/pipeline/pipeline.go` (registry), `pkg/config/config.go` (TaskProfile), new file in `pkg/pipeline/` |
| Modify region/window selection interaction | `pkg/capture/region.go` (state machine), `pkg/capture/window.go` (window highlight) |
| Add a new target backend | `pkg/target/target.go` (interface + factory), new `pkg/target/<name>.go` |
| Change OCR behavior | `pkg/capture/ocr.go` (server comms), `pkg/automation/finder.go` (FindText) |
| Add smart snippet type | `pkg/snippet/smart.go` (SmartState + smartType enum), new `pkg/snippet/smart_<name>.go` |
| Modify recording pipeline | `pkg/recorder/recorder.go` (Start/Stop/run), `pkg/av/device.go` (input), `pkg/av/encoder.go` (codec), `pkg/av/muxer.go` (output) |
| Change magnifier behavior | `pkg/magnifier/magnifier.go` (modes + event loop), `pkg/magnifier/capture.go` (capture strategy), `pkg/magnifier/overlay.go` (window) |
| Add annotation tool | `pkg/annotation/types.go` (Tool enum), `pkg/annotation/annotator.go` (HandleEvent dispatch), `pkg/annotation/draw.go` (render) |
| Change automation script format | `pkg/automation/types.go` (Step/Script structs), `pkg/automation/engine.go` (execution), `pkg/automation/eval.go` (interpolation) |

## Dependencies

### Audio/Video (FFmpeg via go-astiav)
| Package | Role | Version |
|---|---|---|
| `github.com/asticode/go-astiav` | FFmpeg bindings (avcodec, avformat, avdevice, swscale) | v0.41.0 |
| `github.com/asticode/go-astikit` | Async helper for astiav | indirect |

### X11
| Package | Role | Version |
|---|---|---|
| `github.com/jezek/xgbutil` | X11 client library (XGB fork) | v0.0.0-20260124 |
| `github.com/jezek/xgb` | Raw X11 protocol bindings | v1.3.0 |

### Clipboard & Image
| Package | Role | Version |
|---|---|---|
| `golang.design/x/clipboard` | Cross-platform clipboard (used for clipboard manager) | v0.7.1 |
| `golang.org/x/image` | Extended image processing | v0.28.0 |
| `golang.org/x/exp/shiny` | Screen driver (indirect) | indirect |
| `golang.org/x/mobile` | Mobile event handling (indirect) | indirect |

### Web & Parsing
| Package | Role | Version |
|---|---|---|
| `github.com/JohannesKaufmann/html-to-markdown` | HTML→Markdown conversion (vision pipeline) | v1.6.0 |
| `github.com/PuerkitoBio/goquery` | HTML selector engine | indirect v1.9.2 |
| `gopkg.in/yaml.v3` | YAML config parsing | v3.0.1 |

### Standard Library (system)
| Package | Role |
|---|---|
| `golang.org/x/sys` | Unix syscall wrappers | indirect |
| `golang.org/x/net` | HTTP networking | indirect |
| `golang.org/x/text` | Unicode text processing | indirect |

## Build & Run

| Command | Purpose |
|---|---|
| `build.sh` | Build zen-cap binary |

