# Review: go.mod & X11 Linux Portability — zen-cap

## Scope

This review covers the Go module definition (`go.mod`), the build script (`build.sh`), and the OS-level packages that interact directly with the X11 server, FFmpeg, and Linux system libraries. The review objective is to determine whether the current setup will work when moved to another X11 Linux PC, and where compatibility or code simplicity can be improved. Wayland compatibility is explicitly out of scope.

**Files reviewed:**
- `go.mod`
- `build.sh`
- `pkg/av/av.go`
- `pkg/av/device.go`
- `pkg/av/adevice.go`
- `pkg/av/encoder.go`
- `pkg/av/muxer.go`
- `pkg/capture/capture.go`
- `pkg/capture/x11.go`
- `pkg/magnifier/capture.go`
- `pkg/display/x11.go`
- `pkg/target/x11.go`
- `pkg/annotation/overlay/x11util.go`

**Review passes:** First-pass findings by code inspection, cross-validated against an independent second opinion from Claude via `browser({ action: 'chat', provider: 'claude', ... })`.

---

## Top Issues

### Critical

#### C1. `build.sh` — Hardcoded absolute paths block any other machine from building

**Location:** `build.sh:3`

```sh
PKG_CONFIG_PATH=/media/jang/home/Deve/zen-cap/ffmpeg8/lib/pkgconfig \
CGO_CFLAGS="-I/media/jang/home/Deve/zen-cap/zen-cap/ffmpeg8/include" \
CGO_LDFLAGS="-L/media/jang/home/Deve/zen-cap/ffmpeg8/lib ..." \
go build -o zen-cap .
```

**What happens:** `PKG_CONFIG_PATH`, `CGO_CFLAGS`, and `CGO_LDFLAGS` all point to `/media/jang/home/Deve/zen-cap/ffmpeg8/...`. The `ffmpeg8/` directory itself is not part of the repository. On any other machine, `go build` will fail at the CGO compilation step because the headers and libraries cannot be found.

**Portability impact:** The binary cannot be rebuilt on any machine except the original dev box, unless the user manually edits `build.sh` or replicates the exact filesystem layout. This is a hard blocker for the "clone repo, build, run on any X11 box" workflow.

**Remediation:** Replace absolute paths with a relative path (e.g. `./ffmpeg8`) or make the FFmpeg bundle root configurable via an environment variable (`FFMPEG_ROOT` or `ZEN_CAP_FFMPEG`) with a documented default. The build script should also verify the bundle exists and emit a clear error if it does not.

---

#### C2. `build.sh` — Broken `$ORIGIN` rpath due to escaped dollar inside single quotes

**Location:** `build.sh:3`

```sh
-Wl,-rpath,'\$ORIGIN/ffmpeg8/lib'
```

**What happens:** Inside single quotes, the backslash is not interpreted by the shell, so the linker literally receives `\$ORIGIN/ffmpeg8/lib`. Modern linkers do not recognize `\$ORIGIN`; only the bare `ORIGIN` token is expanded at runtime to the executable's directory. This means the "ship ffmpeg libs next to the binary" fallback is dead — even if `ffmpeg8/lib/*.so` is placed alongside the binary, the dynamic linker will not find them.

**Portability impact:** The binary will fail to start on a machine without the private FFmpeg libraries installed system-wide, with `error while loading shared libraries`. Combined with C1, this means the binary is effectively locked to the dev machine.

**Remediation:** Change to `-Wl,-rpath,'$ORIGIN/ffmpeg8/lib'` (no backslash; single quotes already protect `$ORIGIN` from shell expansion).

---

#### C3. Runtime dependency on private FFmpeg 8 shared libraries

**Locations:** `build.sh:3`, `pkg/av/av.go:14`

Even with a fixed rpath, the binary depends on `ffmpeg8/lib/libavcodec.so`, `libavformat.so`, `libavdevice.so`, `libswscale.so`, etc. being present at runtime. The `pkg/av/av.go` `Init()` function calls `astiav.RegisterAllDevices()`, which requires these shared libraries to be loadable.

**What happens:** On a fresh X11 Linux PC, the binary will not start unless the `ffmpeg8/lib/` directory is shipped alongside it (and the rpath is fixed per C2), or the exact same FFmpeg 8 build is installed system-wide.

**Portability impact:** This is the single biggest runtime portability risk. It turns the binary into a self-contained bundle requirement rather than a standard Linux executable.

**Remediation options (choose one):**
- **(a) Bundle + fixed rpath:** Fix C2, ship `ffmpeg8/lib/*.so` with the binary, set `LD_LIBRARY_PATH` or `DT_RPATH` to point to the bundled directory.
- **(b) Static linking:** Statically link the needed FFmpeg components into the binary. This eliminates the runtime shared-library dependency entirely.
- **(c) Distro FFmpeg:** Build against the distro's `ffmpeg-dev` package instead of a private build. This removes the bundle requirement but sacrifices control over the exact FFmpeg version and codec availability.

---

#### C4. `pkg/av/device.go` — `grab_x`/`grab_y` are not valid `xcbgrab` AVOptions

**Locations:** `pkg/av/device.go:64`, `pkg/av/device.go:67`

```go
options.Set("grab_x", strconv.Itoa(cfg.X), 0)
options.Set("grab_y", strconv.Itoa(cfg.Y), 0)
```

**What happens:** The code tries `xcbgrab` first, then falls back to `x11grab`:

```go
inputFormat := astiav.FindInputFormat("xcbgrab")
if inputFormat == nil {
    inputFormat = astiav.FindInputFormat("x11grab")
}
```

`x11grab` historically accepted `grab_x`/`grab_y` as AVOptions. `xcbgrab` does not — for `xcbgrab`, the offset must be embedded in the input URL itself (e.g. `":0.0+100,200"`). When unknown AVOptions are passed via `AVDictionary` to `avformat_open_input`, FFmpeg typically leaves them unconsumed rather than returning an error. This means:

- On machines where FFmpeg exposes `xcbgrab` (the increasingly common case on modern distros), the region/window offset is silently ignored.
- The capture opens successfully but grabs the wrong region (or the full screen) with no error surfaced.
- The same code behaves correctly on one X11 PC (older FFmpeg, `x11grab` used) and silently captures the wrong area on another (newer FFmpeg, `xcbgrab` used).

**Portability impact:** This is precisely the "works on my machine, breaks silently on the next one" failure mode. It will cause incorrect capture geometry on any machine where `xcbgrab` is the available device.

**Remediation:** Build the offset into the display URL when `window_id` is not used:

```go
if cfg.WindowID != 0 {
    options.Set("window_id", fmt.Sprintf("0x%x", cfg.WindowID), 0)
} else {
    display := cfg.Display
    if display == "" {
        display = ":0.0"
    }
    if cfg.X > 0 || cfg.Y > 0 {
        display = fmt.Sprintf("%s+%d,%d", display, cfg.X, cfg.Y)
    }
    // pass display as the input URL, not as an option
}
```

Remove the `grab_x`/`grab_y` options entirely. The URL-based offset works for both `xcbgrab` and `x11grab`.

---

#### C5. Multiple files — Hardcoded `":0.0"` display default instead of respecting `$DISPLAY`

**Locations:**
- `pkg/av/device.go:78`
- `pkg/target/x11.go:34`
- `pkg/recorder/recorder.go:20`
- `pkg/config/config.go:293`, `pkg/config/config.go:460`
- `pkg/magnifier/config.go:85`, `pkg/magnifier/config.go:104`
- `pkg/annotation/overlay/overlay.go:65`
- Plus 15+ service and CLI default-config sites

```go
display := cfg.Display
if display == "" {
    display = ":0.0"
}
```

**What happens:** On a machine where the X session is not on display 0 — multi-seat setups, xrdp/x11vnc remote sessions, SSH X11 forwarding (`:10`, `:11`), some display managers — the code silently connects to the wrong display or a nonexistent one. The connection fails or captures from the wrong session.

**Inconsistency:** `pkg/display/x11.go:19` correctly uses `xgbutil.NewConn()` with no display argument, which auto-resolves from the `$DISPLAY` environment variable. The same correct pattern is not used elsewhere.

**Portability impact:** Breaks on any X11 setup where the session is not on `:0.0`. This includes common remote/forwarded X scenarios and multi-seat deployments.

**Remediation:** In every location that currently defaults to `":0.0"`, fall back to `os.Getenv("DISPLAY")` when `cfg.Display` is empty. If both are empty, return a clear error rather than silently connecting to the wrong display.

---

#### C6. `pkg/magnifier/capture.go` — Raw `syscall.SYS_SHM*` numbers won't compile on arm64

**Locations:** `pkg/magnifier/capture.go:50`, `pkg/magnifier/capture.go:59`, `pkg/magnifier/capture.go:61`, `pkg/magnifier/capture.go:78`, `pkg/magnifier/capture.go:124`, `pkg/magnifier/capture.go:128`, `pkg/magnifier/capture.go:129`

```go
shmid, _, errno := syscall.Syscall(syscall.SYS_SHMGET, ...)
addr, _, errno := syscall.Syscall(syscall.SYS_SHMAT, shmid, 0, 0)
syscall.Syscall(syscall.SYS_SHMCTL, shmid, uintptr(ipcRmid), 0)
syscall.Syscall(syscall.SYS_SHMDT, addr, 0, 0)
```

**What happens:** Go's standard `syscall` package only defines `SYS_SHMGET`, `SYS_SHMAT`, `SYS_SHMDT`, `SYS_SHMCTL` on architectures that still expose the legacy multiplexed/direct IPC syscalls. On `linux/amd64` and `linux/386`, these constants exist. On `linux/arm64` — increasingly common for X11 desktops (Raspberry Pi, ARM laptops, cloud X11 boxes) — these constants are typically **not defined** in the generated `zsysnum_linux_arm64.go`.

**Portability impact:** The `pkg/magnifier` package is a hard compile-time failure on arm64. The binary cannot be built at all on ARM X11 hosts.

**Remediation:** Replace raw `syscall.SYS_*` numbers with `golang.org/x/sys/unix`'s portable wrappers:

```go
import "golang.org/x/sys/unix"

shmid, _, errno := unix.SysvShmGet(0, size, unix.IPC_CREAT|0600)
addr, _, errno := unix.SysvShmAttach(shmid, 0, 0)
unix.SysvShmDetach(addr)
unix.SysvShmCtl(shmid, unix.IPC_RMID, nil)
```

`golang.org/x/sys/unix` is already an indirect dependency in `go.mod` (`golang.org/x/sys v0.38.0`). These wrappers are maintained per-architecture and will build on every supported Go target.

---

### Major

#### M1. `pkg/av/adevice.go` — ALSA-only audio with no PulseAudio/PipeWire fallback

**Location:** `pkg/av/adevice.go:28`

```go
inputFormat := astiav.FindInputFormat("alsa")
if inputFormat == nil {
    return nil, fmt.Errorf("ALSA audio input format not found (ffmpeg built without ALSA)")
}
```

**What happens:** Unlike the video path (`pkg/av/device.go:37-43`), which tries `xcbgrab` then `x11grab` in sequence, audio capture hardcodes ALSA with no fallback. On modern X11 Linux distros where PulseAudio or PipeWire owns the sound device, raw ALSA device names (`hw:0`, `default`) may not exist, may be exclusively locked by the sound server, or may capture silence/the wrong device.

**Portability impact:** Audio recording is unreliable on a large class of modern X11 Linux PCs. This is arguably the single most distro-variable piece of the stack, more so than X11 itself.

**Remediation:** Add a fallback chain mirroring the video device pattern:

```go
inputFormat := astiav.FindInputFormat("pulse")
if inputFormat == nil {
    inputFormat = astiav.FindInputFormat("pipewire")
}
if inputFormat == nil {
    inputFormat = astiav.FindInputFormat("alsa")
}
```

Note: `pipewire` FFmpeg input format uses the same options as `pulse` (device name, sample_rate, channels), so the fallback is straightforward.

---

#### M2. Version/ABI coupling between `go-astiav v0.41.0` and private FFmpeg 8

**Locations:** `go.mod:8`, `build.sh:3`

`go-astiav v0.41.0` is a cgo binding generated against a specific FFmpeg API version. The project builds against a private "ffmpeg8" checkout. If the headers and ABI of that private build drift from what `go-astiav v0.41.0` expects, the code will compile but crash or misbehave at runtime.

**Portability impact:** If the project ever switches to building against a distro's `ffmpeg-dev` package (remediation for C3), the exact FFmpeg version must match what `go-astiav v0.41.0` targets. This coupling should be documented.

**Remediation:** Document the exact FFmpeg commit/tag that `go-astiav v0.41.0` targets. When using a distro FFmpeg, pin to a compatible version or rebuild `go-astiav` bindings against the distro's headers.

---

#### M3. `pkg/target/x11.go` — Decorated vs undecorated geometry mismatch between discovery and input

**Locations:**
- `pkg/display/x11.go:114` — `DecorGeometry()` (outer bounds, includes WM chrome)
- `pkg/display/x11.go:160` — `DecorGeometry()` for active window
- `pkg/target/x11.go:79` — `Geometry()` (inner bounds, excludes WM chrome)
- `pkg/target/x11.go:114` — `Geometry()` for Move
- `pkg/target/x11.go:243` — `Geometry()` for Scroll

```go
// pkg/display/x11.go (window discovery)
geom, err := xwindow.New(m.xu, winID).DecorGeometry()  // outer bounds

// pkg/target/x11.go (input injection)
geom, err := xwindow.New(t.xu, xproto.Window(t.windowID)).Geometry()  // inner bounds
tx = geom.X() + x
```

**What happens:** `pkg/display/x11.go` reports window geometry using `DecorGeometry()` (which includes the window manager's title bar and borders). `pkg/target/x11.go` computes click offsets using `Geometry()` (which returns only the content area, excluding WM chrome). If a caller gets a window's position from `GetActiveWindow()` or `GetWindows()` and then passes coordinates relative to that, the click will land offset by the thickness of the WM's decorations.

**Portability impact:** Decoration thickness varies significantly between window managers and desktop environments. On a tiling WM with no decorations, clicks land correctly. On a stacking WM with chunky title bars, clicks are consistently off by 20-40 pixels. This is a per-machine behavior difference that is extremely confusing to debug.

**Remediation:** Unify on one geometry source. Either:
- Use `Geometry()` consistently (content area) and document that callers must pass coordinates relative to the content area, or
- Use `DecorGeometry()` consistently and subtract the decoration offset in the input injection path.

The second option is more user-friendly because it matches how humans perceive window positions.

---

#### M4. `pkg/target/x11.go` — `Type()` silently drops characters needing AltGr/Level3

**Location:** `pkg/target/x11.go:149-182`

```go
for _, r := range text {
    sym := xproto.Keysym(r)
    var targetKC, col byte
    found := false
    for kc := int(minKC); kc <= int(maxKC); kc++ {
        offset := (kc - int(minKC)) * int(per)
        for ci := 0; ci < int(per); ci++ {
            if keyMap.Keysyms[offset+ci] == sym {
                targetKC = byte(kc)
                col = byte(ci)
                found = true
                break
            }
        }
        if found {
            break
        }
    }
    if !found {
        continue  // <-- SILENT DROP, no error returned
    }
    needShift := col%2 == 1  // <-- only handles 2 levels
```

**What happens:** Real X keyboard layouts can have 3-4 shift levels (Level3/AltGr for `€`, accented characters, symbols on non-US layouts). The code only toggles Shift based on `col%2 == 1`. Any character requiring AltGr (Level3) or a language-group switch is silently skipped (`continue`), with no error returned to the caller.

**Portability impact:** Keyboard layout is a per-machine/per-user X server setting (`setxkbmap`). `Type()` will work correctly on a US-QWERTY machine and silently mangle text (missing characters, no error) on a machine configured with a different layout.

**Remediation:** At minimum, return an error or count of dropped characters so the caller knows typing was incomplete. Ideally, use the XTEST "fake keycode remap" trick (what `xdotool` does) to type arbitrary Unicode reliably regardless of the active layout.

---

#### M5. `pkg/target/x11.go` — `Scroll()` ignores `dx` (horizontal scroll)

**Location:** `pkg/target/x11.go:236-259`

```go
btn := byte(4) // scroll-up
if dy > 0 {
    btn = 5 // scroll-down
}
for i := 0; i < abs(dy)+abs(dx); i++ {
    xtest.FakeInput(c, xproto.ButtonPress, btn, 0, 0, 0, 0, 0)
    xtest.FakeInput(c, xproto.ButtonRelease, btn, 0, 0, 0, 0, 0)
}
```

**What happens:** Horizontal scroll intent (`dx`) is added to the vertical scroll tick count. Buttons 6/7 (horizontal scroll in X11) are never sent. A horizontal scroll of 3 ticks becomes 3 extra vertical scroll ticks.

**Portability impact:** This is wrong everywhere, not just on specific machines. Any automation script that uses horizontal scroll will produce incorrect results.

**Remediation:** Send buttons 4/5 for vertical scroll (`dy`), 6/7 for horizontal scroll (`dx`):

```go
for i := 0; i < abs(dy); i++ {
    btn := byte(4)
    if dy > 0 { btn = 5 }
    xtest.FakeInput(c, xproto.ButtonPress, btn, ...)
    xtest.FakeInput(c, xproto.ButtonRelease, btn, ...)
}
for i := 0; i < abs(dx); i++ {
    btn := byte(6)
    if dx > 0 { btn = 7 }
    xtest.FakeInput(c, xproto.ButtonPress, btn, ...)
    xtest.FakeInput(c, xproto.ButtonRelease, btn, ...)
}
```

---

#### M6. `golang.design/x/clipboard` pulls in `golang.org/x/exp/shiny` and `golang.org/x/mobile`

**Location:** `go.mod:17`, `go.mod:18`

```
golang.org/x/exp/shiny v0.0.0-20250606033433-dcc06ee1d476 // indirect
golang.org/x/mobile v0.0.0-20250606033058-a2a15c67f36f // indirect
```

**What happens:** `golang.design/x/clipboard v0.7.1` is a cross-platform clipboard library. On Linux, it compiles against X11/XCB via cgo, but it also pulls in `shiny` (a GUI screen driver) and `mobile` (mobile event handling) as transitive dependencies. These exist to support clipboard access on macOS/Windows/mobile; they are irrelevant for a Linux-only tool.

**Impact:** This roughly triples build complexity and binary surface. It also introduces additional cgo and GUI library requirements on fresh machines.

**Remediation:** Replace with a Linux-native clipboard implementation using `jezek/xgb`'s `CLIPBOARD` selection support, which is already a direct dependency. This removes the entire `shiny`/`mobile` dependency branch.

---

#### M7. `go.mod` — Go 1.24 requirement limits rebuild portability

**Location:** `go.mod:3`

```
go 1.24.0
```

**What happens:** The module requires Go 1.24 to build. Not a runtime issue (the shipped binary doesn't need Go installed), but if the "move to another X11 PC" workflow includes rebuilding there, older-Go LTS distros (Ubuntu 22.04 ships Go 1.18, Debian 12 ships Go 1.19) will fail.

**Remediation:** Document the minimum Go version clearly. Consider whether the codebase can target Go 1.22+ if that widens compatibility without sacrificing needed features.

---

### Minor

#### m1. `pkg/magnifier/capture.go` — SHM capturer has no bounds check on buffer writes

**Location:** `pkg/magnifier/capture.go:89-118`

```go
func (c *shmCapturer) capture(x, y, w, h int) (*image.RGBA, error) {
    // ...
    pixCount := w * h
    for i := 0; i < pixCount; i++ {
        base := i * 4
        b := c.dataPtr[base]
        // ...
    }
}
```

`dataPtr` was sized at construction as `maxW * maxH * 4` (line 47). If a later `capture()` call requests a region larger than the initial max dimensions — due to screen resolution change via hotplug/xrandr after the capturer was created, or a differently-sized multi-monitor virtual screen on a new machine — the loop indexes past the end of the fixed-length slice and panics.

**Contrast:** The `xgetCapturer.capture()` fallback (lines 161) already bounds-checks: `(i+1)*4 <= len(data)`.

**Remediation:** Clamp or bounds-check `w`/`h` against the allocated buffer size before entering the pixel loop, matching the defensive pattern in the fallback capturer.

---

#### m2. `pkg/display/x11.go` — Xinerama instead of RandR for multi-monitor geometry

**Location:** `pkg/display/x11.go:36-59`

```go
err := xinerama.Init(c)
if err == nil {
    active, err := xinerama.IsActive(c).Reply()
    if err == nil && active.State > 0 {
        screensReply, err := xinerama.QueryScreens(c).Reply()
        // ...
        for i, s := range screensReply.ScreenInfo {
            screens = append(screens, Screen{
                Name: fmt.Sprintf("Screen %d", i),  // <-- generic name
                Geometry: Geometry{ X: int(s.XOrg), ... },
            })
        }
    }
}
```

**What happens:** Xinerama works but loses real output names (`"Screen 0"` vs `"eDP-1"`, `"HDMI-1"`) and can report merged/stale geometry on unusual multi-DPI/rotated setups. Modern X11 desktops configure monitors via RandR; Xinerama data is populated as a compatibility layer but may not reflect the true state.

**Portability impact:** Won't break capture, but window/monitor selection UX will be degraded. Users selecting a monitor won't see recognizable output names.

**Remediation:** Consider migrating to RandR via `jezek/xgb/randr` for accurate monitor identification and DPI-aware geometry.

---

#### m3. Two independent capture pipelines with divergent color-handling logic

**Locations:**
- `pkg/capture/capture.go:88-109` — FFmpeg/avcodec path with `SetColorRange(Jpeg)` fix
- `pkg/magnifier/capture.go:89-118` — MIT-SHM path with manual BGRA decode

Both paths convert X11 screen data to Go `image.RGBA`, but each maintains its own pixel-format and color-range logic independently. The pink-video fix in `pkg/av/device.go:204` (`SetColorRange(Jpeg)`) has no equivalent in the magnifier path.

**Impact:** A bug fixed in one path may not propagate to the other. Future color-handling changes must be applied twice.

**Remediation:** Extract shared pixel-format conversion into a common utility package (e.g. `pkg/x11pixel`) used by both capture paths.

---

#### m4. `pkg/target/x11.go` — `xtest.Init(c)` called on every input event

**Locations:** `pkg/target/x11.go:73`, `pkg/target/x11.go:108`, `pkg/target/x11.go:130`, `pkg/target/x11.go:192`, `pkg/target/x11.go:238`

```go
func (t *X11Target) Click(x, y int, button string) error {
    c := t.xu.Conn()
    if err := xtest.Init(c); err != nil {  // called every Click()
        return fmt.Errorf("x11: xtest init: %w", err)
    }
    // ...
}
```

**What happens:** `xtest.Init()` queries the X server for the XTEST extension presence. It is idempotent, but calling it on every `Click`, `Move`, `Type`, `Key`, and `Scroll` invocation adds unnecessary round-trips to the X server.

**Remediation:** Call `xtest.Init(c)` once in `NewX11Target()` and store the result. Fail target creation if XTEST is unavailable.

---

#### m5. `pkg/target/x11.go` — Hardcoded `100ms` sleep after `ActiveWindowReq`

**Location:** `pkg/target/x11.go:127`, `pkg/target/x11.go:189`

```go
if t.windowID != 0 {
    _ = ewmh.ActiveWindowReq(t.xu, xproto.Window(t.windowID))
    time.Sleep(100 * time.Millisecond)
}
```

**What happens:** The code assumes 100ms is enough time for the WM to process the focus-change request and actually focus the window. Some WMs process focus asynchronously or with different timing.

**Impact:** Under a slow or unusual WM, input may be sent to the wrong window. This is a timing assumption, not a portability break.

**Remediation:** Poll for the `_NET_ACTIVE_WINDOW` property to match the requested window, rather than sleeping a fixed duration. Or make the delay configurable.

---

#### m6. `pkg/av/device.go` — Unconditional `SetColorRange(Jpeg)` on every decoded frame

**Location:** `pkg/av/device.go:204`

```go
d.frame.SetColorRange(astiav.ColorRangeJpeg)
```

**What happens:** This stamps full-range (0-255) color metadata on every decoded frame, which is correct for raw x11grab/xcbgrab output. However, it is a blanket assumption with no format check. If the input format ever isn't raw RGB, this would silently mis-color output.

**Remediation:** Add a defensive comment or debug log stating the assumed input range, so future maintainers understand why this line exists.

---

#### m7. `pkg/capture/capture.go` — `"lanczos"` scaler used for same-size format conversion

**Location:** `pkg/capture/capture.go:100`

```go
scaler, err := av.NewScaler(w, h, srcPixFmt, w, h, astiav.PixelFormatRgba, "lanczos")
```

**What happens:** The scaler is constructed with identical source and destination dimensions (`w,h -> w,h`). No actual resizing occurs; only the pixel format changes (native → RGBA). Lanczos is an expensive resampling filter meant for resizing; for a pure format conversion, a cheaper algorithm does the same job.

**Impact:** Wasted CPU on every single-frame capture. Not a portability bug, but a performance issue.

**Remediation:** Use `"fast_bilinear"` or a format-only conversion path when source and destination dimensions are identical.

---

### Nit

#### n1. `build.sh` — Missing `set -e`

**Location:** `build.sh:1`

**What happens:** The script has no `set -e`. If a future edit adds more commands (e.g. a pre-build check that fails), the failure will be silently ignored and `go build` will run anyway.

**Remediation:** Add `set -e` at the top of the script.

---

#### n2. `go.mod` — Three separate `require` blocks

**Location:** `go.mod:5-27`

```go
require (
    github.com/JohannesKaufmann/html-to-markdown v1.6.0
    // ...
)

require (
    github.com/PuerkitoBio/goquery v1.9.2 // indirect
    // ...
)

require (
    github.com/asticode/go-astikit v0.42.0 // indirect
    github.com/jezek/xgb v1.3.0
)
```

**What happens:** The three separate `require` blocks are a cosmetic artifact of `go mod tidy` runs at different times. They are functionally harmless.

**Remediation:** Run `go mod tidy` to consolidate into a single clean block.

---

#### n3. `build.sh` — `--disable-new-dtags` forces `DT_RPATH`

**Location:** `build.sh:3`

```sh
-Wl,--disable-new-dtags
```

**What happens:** This forces `DT_RPATH` instead of `DT_RPATH`'s successor `DT_RUNPATH`. `DT_RPATH` applies transitively to the binary's dependencies' own dependencies and cannot be overridden by `LD_LIBRARY_PATH`. This is likely intentional here (to force use of the bundled ffmpeg8 libs over system ones), but it is a deliberate non-default choice that warrants documentation.

**Remediation:** Add a comment explaining why `--disable-new-dtags` is used.

---

## Red-Team Critique Summary

A second-pass review was performed by Claude via `browser({ action: 'chat', provider: 'claude', upload_files: [go.mod, build.sh, pkg/av/device.go, pkg/av/adevice.go, pkg/av/av.go, pkg/capture/capture.go, pkg/magnifier/capture.go, pkg/display/x11.go, pkg/target/x11.go] })`.

The following table reconciles the first-pass findings with the second opinion. Where findings overlap, they are merged. Where they disagree, the disposition is stated explicitly.

| # | Finding | First-pass | Second opinion | Disposition |
|---|---|---|---|---|
| 1 | `build.sh` hardcoded absolute paths | Critical | Critical | **Folded in** — C1 |
| 2 | `build.sh` broken `$ORIGIN` rpath | Critical | Critical | **Folded in** — C2 |
| 3 | Runtime dependency on private FFmpeg 8 libs | Critical | Critical | **Folded in** — C3 |
| 4 | `grab_x`/`grab_y` invalid for `xcbgrab` | Critical | Critical | **Folded in** — C4 |
| 5 | Hardcoded `":0.0"` display default | Critical | Critical | **Folded in** — C5 |
| 6 | Raw `syscall.SYS_SHM*` won't compile on arm64 | Critical | Critical | **Folded in** — C6 |
| 7 | ALSA-only audio, no PulseAudio/PipeWire fallback | Major | Major | **Folded in** — M1 |
| 8 | Version/ABI coupling `go-astiav` / private FFmpeg | — | Major | **Folded in** — M2 |
| 9 | Decorated vs undecorated geometry mismatch | Major | Major | **Folded in** — M3 |
| 10 | `Type()` silently drops AltGr characters | Major | Major | **Folded in** — M4 |
| 11 | `Scroll()` ignores `dx` | Major | Major | **Folded in** — M5 |
| 12 | `golang.design/x/clipboard` shiny/mobile bloat | — | Major | **Folded in** — M6 |
| 13 | Go 1.24 requirement limits rebuild portability | — | Major | **Folded in** — M7 |
| 14 | SHM capturer no bounds check | Minor | Minor | **Folded in** — m1 |
| 15 | Xinerama instead of RandR | Minor | Minor | **Folded in** — m2 |
| 16 | Two independent capture pipelines | Minor | Minor | **Folded in** — m3 |
| 17 | `xtest.Init` called every invocation | Minor | Minor | **Folded in** — m4 |
| 18 | Hardcoded `100ms` sleep for focus | Minor | Minor | **Folded in** — m5 |
| 19 | Unconditional `SetColorRange(Jpeg)` | Minor | Minor | **Folded in** — m6 |
| 20 | `"lanczos"` scaler for same-size conversion | Minor | Minor | **Folded in** — m7 |
| 21 | `build.sh` missing `set -e` | Nit | Nit | **Folded in** — n1 |
| 22 | Three `require` blocks in `go.mod` | Nit | Nit | **Folded in** — n2 |
| 23 | `--disable-new-dtags` `DT_RPATH` note | Nit | Nit | **Folded in** — n3 |
| 24 | `go.sum` supply-chain integrity | — | Minor | **Rejected** — `go.sum` exists in the repository and is committed; the concern is already addressed |
| 25 | Multi-screen X11 (`:0.1`, `:0.2`) not handled | — | Minor | **Rejected** — Multi-screen X11 setups are effectively extinct; RandR multi-monitor (covered by m2) is the correct modern abstraction |
| 26 | Hardcoded little-endian pixel assumption in magnifier | — | Minor | **Rejected** — All mainstream X11 Linux targets are little-endian; the dead code (`hostLittleEndian`/`u32LE`) should be cleaned up but is not a portability risk |
| 27 | No `go.sum` provided in review context | — | Minor | **Rejected** — `go.sum` is present in the repo at `/media/jang/home/Deve/zen-cap/go.sum` |

---

## Compatibility Verdict

**Will it work on another X11 Linux PC?**

The **built binary** may work if the target machine has the exact same private FFmpeg 8 libraries installed system-wide and the X session is on `:0.0`. However, several silent failures will occur under realistic variation:

- **Region/window capture** will silently grab the wrong area on machines with newer FFmpeg (xcbgrab preferred) — no error, just wrong output.
- **Audio recording** will likely fail or capture silence on modern PipeWire-based distros.
- **Automation clicks** will be offset by WM-dependent decoration thickness.
- **Keyboard typing** will silently drop characters on non-US keyboard layouts.
- **Horizontal scroll** will produce extra vertical ticks instead of horizontal scroll events.
- **Magnifier** will fail to compile on ARM X11 hosts.

The **build** will fail on any machine that does not replicate the exact `ffmpeg8/` directory structure at the absolute path specified in `build.sh`.

**Can compatibility and simplicity be improved?**

Yes. The highest-impact changes are:

1. **Fix `build.sh`** — relative paths, fixed `$ORIGIN`, or switch to distro FFmpeg. This alone would make the project buildable anywhere.
2. **Fix the xcbgrab offset bug** — embed offset in the display URL. This eliminates the most dangerous silent failure.
3. **Respect `$DISPLAY`** — change all `":0.0"` defaults to `os.Getenv("DISPLAY")`. This is a one-line fix in multiple places.
4. **Replace `golang.design/x/clipboard`** — drop the `shiny`/`mobile` dependency tree and use `jezek/xgb` directly. This simplifies the build and reduces the attack surface.
5. **Fix `Scroll()` and `Type()`** — correct the horizontal scroll bug and surface errors for dropped characters.

---

## Decision Record (executed 2026-08-04)

Improvement pass executed by Zen (Package Maintainer). Critical + Major items handled;
Minor/Nit deferred. Decisions on hard/risky items recorded below.

### Implemented

| # | Finding | Change |
|---|---|---|
| C1 | build.sh hardcoded absolute paths | Rewrote `build.sh`: `ZEN_CAP_FFMPEG` env override, relative `./ffmpeg8` default, existence validation with clear error, added `set -e`. |
| C2 | Broken `$ORIGIN` rpath | Removed stray `\'` so linker receives bare `$ORIGIN`. Verified via `readelf` → RPATH `$ORIGIN/ffmpeg8/lib:...`. |
| C4 | `grab_x`/`grab_y` invalid for xcbgrab | Offset now embedded in display URL (`:0.0+100,200`) in `pkg/av/device.go`; `grab_x`/`grab_y` removed. |
| C5 | Hardcoded `":0.0"` display | New `resolveDisplay()` in `pkg/av` falls back to `$DISPLAY`, errors if unset. Applied across `device.go`, `target/x11.go`, `overlay.go`, `magnifier/config.go`, `config.go` defaults, `recorder.go`, `service_*.go`, `api.go`, and CLI flags (`-d` default = `$DISPLAY`). |
| C6 | Raw `syscall.SYS_SHM*` breaks arm64 | Migrated `pkg/magnifier/capture.go` to `golang.org/x/sys/unix` (`SysvShmGet/Attach/Detach/Ctl`). |
| M1 | ALSA-only audio | `pkg/av/adevice.go` now tries pulse → pipewire → alsa. |
| M3 | Decor vs content geometry mismatch | `pkg/target/x11.go` input offsets now use `DecorGeometry` (fallback `Geometry`) via new `windowFrame()` helper — consistent with `display/x11.go` discovery. |
| M4 | `Type()` silent char drop | Now returns error with dropped-character count when a char needs AltGr/Level3/group switch. |
| M5 | `Scroll()` ignores dx | Sends buttons 6/7 for horizontal, 4/5 for vertical. |

### Decided (documented, not code-changed)

- **C3** — Keep bundled FFmpeg + fixed `$ORIGIN` rpath (option a). Static linking (b) and distro FFmpeg (c) deferred; they are larger, riskier reworks.
- **M2** — go-astiav v0.41.0 ABI coupling documented in `build.sh` header. No distro-FFmpeg switch.
- **M6** — `golang.design/x/clipboard` replacement **deferred**. Swapping the clipboard backend mid-flight risks the multi-slot clipboard daemon and the OCR/text copy paths; needs a dedicated rework with tests. Mitigation for the shiny/mobile bloat stays as-is for now.
- **M7** — Go 1.24 kept; documented minimum in `build.sh`. No code change.

### Deferred (Minor/Nit — saved for later)

m1 (SHM bounds check), m2 (RandR), m3 (pixel-path unification), m4 (xtest.Init once), m5 (focus sleep), m6 (color-range comment), m7 (lanczos scaler), n1 (`set -e` — already applied as part of C1), n2 (require-block tidy), n3 (dtags comment — covered in build.sh).

### Noted

Rejected findings #24/#25/#26/#27 (go.sum integrity, multi-screen X11, endianness, go.sum presence) remain rejected per the review's red-team table.
