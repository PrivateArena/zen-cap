# Zen Cap — Project Architecture Overview

Generated: 2026-07-05 | 59 files | ~610 symbols | ~935 edges

---

## Project Purpose

Zen Cap is a Linux-first (X11) desktop productivity tool that provides screenshot capture, screen recording, on-screen magnifier, clipboard management with session slots, text snippet management with smart dynamic snippets (emoji/date/IP/prompt), desktop automation scripting (vision-based find/click/type), and an HTTP API server. It targets both local desktop interaction and remote device control (Android via ADB, iOS via WebDriverAgent, VNC, virtual framebuffer via Xvfb+scrcpy).

---

## Architecture Overview

```
main.go
  |
  +-- cli.go          (dispatch hub)
        |
        +-- service.go          (interactive X11 daemon — chord keys, overlays)
        |     +-- api.go        (HTTP API server — screenshot, record, collaborate)
        |     +-- helpers.go    (X11 screen/window queries, clipboard actions)
        |     +-- record.go     (screen recording handler)
        |     +-- screenshot.go (screenshot handler)
        |
        +-- pkg/config/config.go          (JSON config loader, defaults)
        +-- pkg/automation/manager.go     (YAML automation script store)
        +-- pkg/automation/picker.go      (X11 overlay picker for scripts)
        +-- pkg/tui/tui.go                (tview TUI for snippet management)
        +-- pkg/capture/clipboard.go      (clipboard daemon subcommand)
        |
        v  (called by service.go / api.go at runtime)
 
  pkg/capture/          -- screenshot, interactive region/window/color/OCR overlays
  pkg/clipboard/        -- multi-slot clipboard with text transforms
  pkg/snippet/          -- text snippet manager + smart dynamic snippets
  pkg/automation/       -- script engine, image/text finder, step executor
  pkg/magnifier/        -- fullscreen & lens magnifier with XSHM capture
  pkg/recorder/         -- screen+audio recorder via FFmpeg
  pkg/target/           -- unified Target interface (X11/ADB/VNC/WDA/VFB)
  pkg/av/               -- FFmpeg A/V wrappers (device, encoder, muxer, scaler)
  pkg/display/          -- display manager (screen/window geometry)
  pkg/config/           -- configuration types and persistence
  pkg/tui/              -- terminal UI (tview-based snippet manager)
```

---

## Component Roles & Key Exports

### Root Package — CLI Dispatch

| File | Role | Key Symbols |
|------|------|-------------|
| `main.go` | Entry point | `main()` → calls `runCLI()` |
| `cli.go` | CLI argument parser, subcommand dispatch | `runCLI()` — dispatches to `handleService`, `handleScreenshot`, `handleRecord`, `RunClipboardServer`, `ShowPicker` (automation), `RunManager` (TUI) |
| `service.go` | Interactive X11 daemon — key chords, overlays, screenshot/record/magnifier/OCR automation dispatch | `handleService()`, `ChordManager` (Register, Start, handleKey binds) |
| `api.go` | HTTP API server (REST) for remote screenshot, record, collaborate | `startAPIServer()`, `ScreenshotOptions`, `CollaborateRequest`, `captureScreenshotWithOptions()`, `resolveAndActivateWindow()` |
| `helpers.go` | X11 info queries & clipboard action routing | `listScreens()`, `listWindows()`, `getScreenInfo()`, `getActiveWindowInfo()`, `processClipboardAction()`, `sendNotification()` |
| `screenshot.go` | Screenshot subcommand handler | `handleScreenshot()` |
| `record.go` | Recording subcommand handler | `handleRecord()` |

### pkg/config — Configuration

JSON-based config at `~/.config/zen-cap/config.json`. Key types:

- `Config` — top-level: `OutputDir`, `Hotkeys`, `RecorderSettings`, `EncoderSettings`, `AudioSettings`, `MagnifierConfig`, `SnippetPickerConfig`, `TransformRules`
- `LoadConfig()` — reads file or creates defaults
- `SaveConfig()` — persists
- `HotkeysConfig` — all keybinding pairs

### pkg/capture — Screenshot & Interactive Overlays

X11-based interactive capture tools. Each overlay runs its own X11 event loop.

| File | Key Exports |
|------|-------------|
| `capture.go` | `CaptureConfig`, `captureScreenImpl()`, `SavePNG()` |
| `region.go` | `InteractiveSelectRegion()`, `InteractiveSelectRegionExt()` — drag-select rectangle on screen, returns bounds |
| `window.go` | `InteractiveSelectWindowExt()` — hover-highlight window picker; `InteractiveSelectWindowClass()` — get WM_CLASS; `ShowOCROverlayWindow()` — display OCR results |
| `color_picker.go` | `InteractiveColorPicker()` — zoomed color picker, copies HEX/HSL to clipboard |
| `ocr.go` | `EnsureOCRServer()`, `PerformOCR()`, `TranslateText()`, `PerformOCRWithDetails()`, `PerformOCROverlay()` — communicates with external OCR server (`zen-lights`) |
| `notations.go` | `NotationState` — freehand drawing, rectangle/circle/text annotations on screenshots |
| `magnifier.go` | `Magnifier` — 120x120 loupe overlay for precise pixel targeting |
| `draw.go` | Primitive drawing: `drawLine` (Bresenham), `drawRect`, `drawCircle`, `drawHUDTextScaled` |
| `font.go` | 3x5 and 5x7 bitmap font rendering: `drawChar`, `drawString`, `DrawStringScaled` |
| `clipboard.go` | `CopyImageToClipboard()`, `ReadImageFromClipboard()`, `SpawnClipboardDaemon()`, `RunClipboardServer()` |
| `x11.go` | `ImageToBGRA()`, `UploadImageChunked()` — X11 pixel format conversion helpers |

### pkg/clipboard — Multi-Slot Clipboard

Persistent clipboard history with text transformation rules.

| File | Key Exports |
|------|-------------|
| `manager.go` | `Manager` (NewManager, CopyToSlot, PasteFromSlot, CycleTransform) — slot-based clipboard with JSON session persistence |
| `transform.go` | `ApplyTransform()` — applies regex-based text transforms |

### pkg/snippet — Text Snippets & Smart Snippets

Persistent text expansion snippets with four "smart" dynamic types.

| File | Key Exports |
|------|-------------|
| `manager.go` | `Snippet`, `Manager` (NewManager, GetAll, Add, Update, Delete, Paste, TypeHumanly) — YAML-backed snippet store |
| `picker.go` | `ShowPicker()` — X11 overlay popup with search, category tabs, live preview |
| `smart.go` | `SmartState` (Content, CycleNext, CyclePrev, AppendQuery, BackspaceQuery, ClearQuery) — base smart snippet state machine |
| `smart_time.go` | Time/date presets, IANA timezone locations, fuzzy resolution |
| `smart_emoji.go` | `resolveEmojiQuery()` — fuzzy emoji search |
| `smart_ip.go` | `TriggerIPFetch()` — dynamic public IP |
| `smart_prompt.go` | `PromptRole`, `resolvePromptQuery()` — AI prompt templates |

### pkg/automation — Desktop Automation Scripting

Declarative YAML script engine with image/text finding, conditional branching, goto.

| File | Key Exports |
|------|-------------|
| `types.go` | `Step` (click, move, type, key, wait, notify, log, command, clipboard, file, window, findImage, findText, ocr, goto, stop), `Script`, `TargetConfig` |
| `engine.go` | `RunScript()` — sequential executor with goto/stop control flow; `executeStepList()`, `executeStepWithControl()` |
| `actions.go` | `ExecuteStep()`, `ExecContext` — concrete implementations for all step types; `runFindImage()`, `runFindText()`, `runOCR()`, `runWindow()`, etc. |
| `finder.go` | `FindImage()` (SAD template matching), `FindText()` / `FindTextWithBounds()` (OCR-based), `ParseRegion()`, `CropImage()` |
| `eval.go` | `Interpolate()` — `${var.name}` substitution; `evaluateValue()` — math/type conversion; `evaluateCondition()` — comparison for `while`/`if` |
| `manager.go` | `Manager` (NewManager, GetAll, Save, Add) — per-file YAML script directory |
| `picker.go` | `ShowPicker()` — X11 overlay popup to select and run scripts |

### pkg/magnifier — On-Screen Magnifier

Fullscreen and lens (circle/rectangle) magnifier with X SHM acceleration.

| File | Key Exports |
|------|-------------|
| `magnifier.go` | `Service` (NewService, Start, Stop, CurrentMode, ZoomFactor, CaptureCurrentView) — event loop, mode transitions |
| `config.go` | `Config` (Mode: Fullscreen/Lens, LensShape: Circle/Rectangle), `Normalize()` |
| `capture.go` | `capturer` interface — `shmCapturer` (MIT-SHM) / `xgetCapturer` (fallback); `sourceViewport()` — dynamic viewport for zoom |
| `overlay.go` | `overlayWindow` — X11 overlapped window with double-buffer blit |
| `render.go` | `scaleImage()`, `rgbaToBGRA()`, `drawCrosshair()` |
| `shapes.go` | `applyCircleMask()`, `applyRectMask()`, `applyWindowShape()`, `rasteriseCircle()`, `drawOSD()` |
| `hotkeys.go` | `grabHotkey()`, `ungrabHotkey()`, `grabScrollButtons()` — XGrabKey with NumLock/CapsLock variants |
| `monitors.go` | `detectMonitors()` — XRandR monitor geometry detection |

### pkg/recorder — Screen & Audio Recording

FFmpeg-based screen+audio capture with MP4/MKV output.

| File | Key Exports |
|------|-------------|
| `recorder.go` | `Recorder` (NewRecorder, Start, Stop, IsRecording, run, runAudio) — concurrent video/audio capture loop; `RecorderConfigFromConfig()` |

### pkg/target — Unified Remote Target Interface

Abstraction for controlling desktop (X11) or remote devices (Android/iOS/VNC/virtual).

| File | Key Exports |
|------|-------------|
| `target.go` | `Target` interface — Screenshot, ScreenSize, Click, Move, Type, Key, Scroll, Close; `New()` factory (auto-detects type from Config) |
| `x11.go` | `X11Target` — native X11 via xgbutil; scoped window support |
| `adb.go` | `ADBTarget` — Android via ADB protocol (TCP socket, shell commands, `input tap`/`text`/`keyevent`) |
| `vnc.go` | `VNCTarget` — RFB protocol, VNC auth, pixel format negotiation, keysym mapping |
| `wda.go` | `WDATarget` — iOS via WebDriverAgent (HTTP JSON API) |
| `vfb.go` | `VFBTarget` — virtual framebuffer (Xvfb + scrcpy), wraps X11Target |

### pkg/av — FFmpeg A/V Wrappers

CGO bindings around FFmpeg libraries (libavcodec, libavformat, libavdevice, libswscale, libavutil).

| File | Key Exports |
|------|-------------|
| `av.go` | `Init()` — registers all FFmpeg devices/codecs |
| `device.go` | `InputDevice` (OpenDevice, ReadFrame, Width, Height, PixelFormat) — screen capture input |
| `adevice.go` | `AudioDevice` (OpenAudioDevice, ReadFrame, SampleRate, Channels, SampleFormat) — pulse/ALSA audio input |
| `encoder.go` | `VideoEncoder` / `AudioEncoder` (New, Encode, Close) — H.264/AAC encoding |
| `muxer.go` | `Muxer` (NewMuxer, AddStream, WriteHeader, WritePacket, Close) — MP4/MKV muxing |
| `scaler.go` | `Scaler` (NewScaler, Scale, Close) —色彩空间/尺寸转换 |

### pkg/display — Display Geometry

Abstraction for querying screens and windows.

| File | Key Exports |
|------|-------------|
| `display.go` | `DisplayManager` interface (GetScreens, GetWindows, GetActiveWindow, Close), `Geometry`, `Screen`, `Window` |
| `x11.go` | `X11DisplayManager` — X11 implementation via xgbutil |

### pkg/tui — Terminal UI

| File | Key Exports |
|------|-------------|
| `tui.go` | `RunManager()` — tview-based snippet manager; `ScrollAccelerate()` |

---

## Architectural Patterns

### 1. Interactive Overlay Pattern (pkg/capture, pkg/snippet, pkg/automation)

All X11 interactive dialogs (region select, window picker, color picker, snippet picker, automation picker, OCR overlay) follow the same structure:

- Grab the root window, capture fullscreen as static background
- Create an `XMask` or fullscreen overlay window
- Run a `select{}` event loop processing `KeyPress`, `ButtonPress`, `ButtonRelease`, `MotionNotify`
- Use double-buffered `Pixmap` → `PutImage` for flicker-free rendering
- Return selected data (bounds, text, color, script ID) when user confirms/cancels

### 2. Target Interface Pattern (pkg/target)

The `Target` interface provides a uniform remote-control API across five backends:

```
Target interface
  +-- X11Target   (native X, scoped to window)
  +-- ADBTarget   (Android Debug Bridge TCP)
  +-- VNCTarget   (RFB protocol)
  +-- WDATarget   (WebDriverAgent HTTP)
  +-- VFBTarget   (Xvfb + scrcpy, wraps X11Target)
```

`target.New()` config auto-detects the backend type. All automation actions and screenshot flows go through this abstraction.

### 3. FFmpeg Pipeline (pkg/av + pkg/recorder)

```
InputDevice (X11/gdi) ─┐
                        ├──> VideoEncoder (H.264) ─┐
                        │                          ├──> Muxer → .mp4
AudioDevice (pulse) ────┘──> AudioEncoder (AAC) ───┘
```

`pkg/recorder/Recorder` manages the concurrency: separate goroutines for video capture loop and audio capture loop, feeding encoded packets to a shared Muxer.

### 4. Automation Script Engine (pkg/automation)

Scripts are YAML files with typed steps. The engine (`RunScript`) executes them sequentially with control flow:

```
RunScript → executeStepList → for each step:
  InterpolateStep (${var} substitution)
  evaluateCondition (if/while)
  ExecuteStep → typed handler (runClick, runFindImage, ...)
              → may return GotoError/StopError for flow control
```

Finders use template matching (SAD) for images and an external OCR server for text.

### 5. Key Chord System (service.go)

`ChordManager` registers multi-key hotkeys (e.g. `PrintScreen` → region capture, `Ctrl+PrintScreen` → window capture). It fires on key release after validating no modifier held. Each chord maps to a callback chain that runs the appropriate capture/annotation/clipboard pipeline.

### 6. Smart Snippet State Machine (pkg/snippet)

`SmartState` implements a state machine with four concrete subtypes (time, emoji, IP, prompt). The `Content()` method returns the resolved text. Users can `AppendQuery`/`BackspaceQuery` for search, and `CycleNext`/`CyclePrev` to scroll predictions. The snippet picker overlay calls `syncSmartState()` to bind UI to the live state.

---

## Component Dependency Graph (plain text)

```
main.go
  └── cli.go
        ├── pkg/config/config.go        [config loading]
        ├── pkg/automation/manager.go   [script store init]
        ├── pkg/automation/picker.go    [script picker overlay]
        ├── pkg/tui/tui.go              [TUI snippet manager]
        ├── pkg/capture/clipboard.go    [clipboard daemon]
        │
        ├── service.go  (interactive daemon — long-running)
        │     ├── pkg/config/config.go
        │     ├── pkg/capture/capture.go       [SavePNG]
        │     ├── pkg/capture/clipboard.go     [clipboard daemon spawn]
        │     ├── pkg/capture/region.go        [interactive region select]
        │     ├── pkg/capture/window.go        [interactive window select]
        │     ├── pkg/capture/color_picker.go  [color picker]
        │     ├── pkg/capture/ocr.go           [OCR + overlay]
        │     ├── pkg/magnifier/magnifier.go   [magnifier service]
        │     ├── pkg/magnifier/config.go      [magnifier config]
        │     ├── pkg/recorder/recorder.go     [recorder]
        │     ├── pkg/clipboard/manager.go     [slot clipboard]
        │     ├── pkg/snippet/manager.go       [snippet store]
        │     ├── pkg/snippet/picker.go        [snippet picker overlay]
        │     ├── pkg/snippet/smart.go         [smart snippet state]
        │     ├── pkg/display/display.go       [screen/window info]
        │     ├── pkg/display/x11.go
        │     ├── pkg/target/target.go         [target factory]
        │     ├── pkg/automation/manager.go    [script CRUD]
        │     └── api.go (HTTP server)
        │           ├── pkg/av/device.go       [FFmpeg input device]
        │           ├── pkg/av/adevice.go      [FFmpeg audio device]
        │           ├── pkg/av/encoder.go      [H.264/AAC encoder]
        │           ├── pkg/av/muxer.go        [MP4 muxer]
        │           ├── pkg/target/target.go   [target for remote]
        │           └── pkg/capture/capture.go [screenshot]
        │
        ├── screenshot.go
        │     ├── pkg/config/config.go
        │     ├── pkg/capture/capture.go
        │     └── helpers.go
        │           ├── pkg/display/x11.go     [X11DisplayManager]
        │           └── pkg/display/display.go [DisplayManager interface]
        │
        └── record.go
              ├── pkg/config/config.go
              ├── pkg/recorder/recorder.go
              ├── pkg/av/av.go                [FFmpeg Init]
              ├── pkg/av/device.go
              ├── pkg/av/adevice.go
              └── pkg/av/muxer.go
```

---

## Data Flow: Screenshot → Annotation → Clipboard (the "happy path")

```
1. ChordManager detects hotkey
2. service.go dispatches to captureScreenWithOptions or InteractiveSelectRegion
3. captureScreenImpl() via X11 GetImage → image.Image
4. InteractiveSelectRegionExt() overlay lets user drag region
5. NotationState allows doodling/arrows/text on screenshot
6. SavePNG() writes to OutputDir
7. processClipboardAction() → CopyImageToClipboard() (native clipboard)
8. sendNotification() with path to saved file
```

## Data Flow: Automation Script Execution

```
1. ShowPicker() → user selects script from X11 overlay
2. RunScript() loads YAML → []Step
3. executeStepList loops:
     a. InterpolateStep (${env} substitution)
     b. evaluateCondition (if/while branching)
     c. ExecuteStep → typed action:
          - runFindImage → FindImage (SAD match) → returns (x,y)
          - runClick → Target.Click(x,y)
          - runType → Target.Type(text)
          - runOCR → PerformOCR → returns text
4. On error/stop/goto → flow control
```
