# Zen-Cap Desktop Automation Engine YAML API Specification

Zen-Cap features a YAML-driven, focusless desktop automation engine. Scripts are stored individually as `.yaml` files inside the `automations/` directory next to the binary or in your workspace.

---

## 1. Script Schema Root

Every automation script represents a single YAML document with the following top-level keys:

```yaml
# The user-facing name displayed in the GUI selector
name: "Daily Game Rewards"

# Optional: target backend (see Section 2). Omit for default local X11.
target:
  type: x11

# Optional window targeting (X11/VFB targets only)
window:
  title: "MyGame"
  class: "my-game-client"

# Ordered list of execution steps
steps:
  - action: wait
    duration: 1s
```

### `window` Configuration
If a `window` block is provided, Zen-Cap queries window properties via EWMH/ICCCM APIs to resolve the active `WindowID`:
- **`title`**: String to match against window title.
- **`class`**: String to match against window class name.
- Zen-Cap runs all input actions (`click`, `type`, `key`, etc.) directly targeting the resolved `WindowID` without stealing mouse focus or switching active desktops.
- If omitted, coordinates and actions target the active fullscreen coordinate system.

---

## 2. Target Backends

The `target:` block selects the I/O backend used for screenshots and input injection. **Omitting the block defaults to `x11`**, preserving full backward compatibility with existing scripts.

All backends expose the same action API — `click`, `move`, `hold`, `release`, `drag`, `type`, `key`, `scroll`, `screenshot`, `find_image`, `find_text`, `ocr`, `wait_until`, `window_state`, `if_found`, `if_pixel`, `if_window`, etc. work identically regardless of target.

> `scroll` is a real action (`target.Target.Scroll` plus the `scroll` step). Its
> counts are wheel *ticks*, not pixels: vertical scroll is X button 4 (up) / 5
> (down), horizontal 6 (left) / 7 (right). Non-X11 backends map those ticks onto
> whatever their protocol offers, so a large `amount` may behave differently over
> VNC/ADB than locally.

### Common Fields

| Field | Type | Description |
|-------|------|-------------|
| `type` | string | Backend selector: `x11`, `vnc`, `adb`, `wda`, `vfb` |
| `scale` | float | Coordinate scale factor. Set to `0.5` if your templates were captured at half resolution. Default: `1.0` |

---

### `type: x11` — Local Linux Display (default)

Drives the local X server via XTEST. No cursor theft; all events are injected directly into the target window.

```yaml
target:
  type: x11
  display: ":0"    # Optional. Defaults to $DISPLAY env var.
```

**Full example — local game automation:**
```yaml
name: "Local Reward Clicker"
target:
  type: x11
window:
  title: "MyGame"
steps:
  - action: if_found
    type: image
    target: "templates/reward_btn.png"
    wait_timeout: 10s
    steps:
      - action: click
        x: -1
        y: -1
```

---

### `type: vfb` — Virtual Framebuffer (headless Android via scrcpy)

Launches a private Xvfb display and optionally starts scrcpy into it. The Android device window is captured and driven entirely off-screen — **no physical cursor movement, no visible window**.

The display number is auto-selected (`:99`–`:199`); a socket poll ensures Xvfb is ready before scrcpy launches.

```yaml
target:
  type: vfb
  vfb_serial: "192.168.1.10:5555"  # ADB serial of the Android device
  vfb_width: 540                    # Virtual display width (default: 1024)
  vfb_height: 1140                  # Virtual display height (default: 768)
  vfb_display: ":99"               # Optional: force a specific display number
  scale: 0.5                        # Optional: if templates captured at 270x570
```

**Full example — headless Android shop automation:**
```yaml
name: "Shopee Daily Rewards"
target:
  type: vfb
  vfb_serial: "192.168.1.10:5555"
  vfb_width: 540
  vfb_height: 1140
steps:
  - action: wait
    duration: 3s
  - action: if_found
    type: image
    target: "templates/collect_btn.png"
    confidence: 0.88
    wait_timeout: 15s
    steps:
      - action: click
        x: -1
        y: -1
      - action: notify
        title: "Rewards"
        message: "Collected!"
  - action: loop
    count: 3
    steps:
      - action: key
        keys: "back"
      - action: wait
        duration: 1s
```

> **Requirements:** `Xvfb` and `scrcpy` must be installed. The ADB daemon must be running (`adb start-server`).

---

### `type: adb` — Android (Direct ADB Wire Protocol)

Speaks directly to the ADB host daemon on `localhost:5037` without requiring the `adb` binary in `PATH`. Screenshots use `screencap -p`; input uses `input tap/swipe/text/keyevent`.

```yaml
target:
  type: adb
  serial: "emulator-5554"   # Optional. Omit for single-device mode.
  scale: 0.5                # Optional coordinate scale
```

**Full example — multi-step Android login:**
```yaml
name: "Android Auto Login"
target:
  type: adb
  serial: "192.168.1.5:5555"
  scale: 0.5
steps:
  - action: if_found
    type: image
    target: "templates/login_field.png"
    wait_timeout: 8s
    steps:
      - action: click
        x: -1
        y: -1
      - action: type
        text: "myuser@example.com"
      - action: key
        keys: "tab"
      - action: type
        text: "p@ssw0rd"
      - action: key
        keys: "return"
```

**Supported key names for `action: key`:**

| Name | Android Keycode |
|------|----------------|
| `return` / `enter` | `KEYCODE_ENTER` |
| `backspace` | `KEYCODE_DEL` |
| `back` | `KEYCODE_BACK` |
| `home` | `KEYCODE_HOME` |
| `escape` | `KEYCODE_ESCAPE` |
| `tab` | `KEYCODE_TAB` |
| `up/down/left/right` | `KEYCODE_DPAD_*` |
| `volumeup/volumedown` | `KEYCODE_VOLUME_*` |

> **Requirements:** ADB daemon must be running (`adb start-server`). Network ADB requires the device to be paired (`adb connect <ip>:5555`).

---

### `type: wda` — iOS (WebDriverAgent)

Controls an iOS device via the WebDriverAgent HTTP server running on-device. No jailbreak required. Supports screenshot, tap, type, key, and scroll.

```yaml
target:
  type: wda
  wda_host: "localhost:8100"  # WDA server address (after port forwarding)
  scale: 1.0
```

**Setup:** Start WDA on device, then forward the port:
```bash
# Using tidevice (recommended, no Xcode required):
tidevice wdaproxy -B com.facebook.WebDriverAgentRunner.xctrunner --port 8100

# Or with iproxy (from libimobiledevice):
iproxy 8100 8100
```

**Full example — iOS app automation:**
```yaml
name: "iOS Daily Check-in"
target:
  type: wda
  wda_host: "localhost:8100"
steps:
  - action: wait
    duration: 2s
  - action: if_found
    type: image
    target: "templates/checkin_btn.png"
    confidence: 0.90
    wait_timeout: 10s
    steps:
      - action: click
        x: -1
        y: -1
      - action: wait
        duration: 1s
      - action: key
        keys: "home"
```

> **Requirements:** WebDriverAgent must be installed on the device and running. Use `tidevice` or `iproxy` to forward port 8100.

---

### `type: vnc` — Remote Desktop (VNC/RFB)

Connects to any VNC-compatible server (Windows, macOS, Linux, Android via VNC app) using pure Go RFB 3.8 protocol. Supports `None` and `VNCAuth` security. Screenshots use RAW pixel encoding for maximum performance.

```yaml
target:
  type: vnc
  host: "192.168.1.20"
  port: 5900              # Default VNC port
  password: "secret"      # Optional. Leave empty for no-auth servers.
  scale: 1.0
```

**Full example — remote Windows machine automation:**
```yaml
name: "Remote PC Task"
target:
  type: vnc
  host: "192.168.1.20"
  port: 5900
  password: "mypassword"
steps:
  - action: if_found
    type: image
    target: "templates/start_button.png"
    wait_timeout: 10s
    steps:
      - action: click
        x: -1
        y: -1
      - action: wait
        duration: 500ms
      - action: type
        text: "notepad"
      - action: key
        keys: "Return"
```

> **Notes:** The remote display must have VNC enabled. For macOS, enable "Remote Management" or "Screen Sharing" in System Preferences. For Windows, use TigerVNC or RealVNC server. SSL/TLS tunneling (e.g. via SSH port forwarding) is recommended for remote targets.

---

### Scale Factor Reference

The `scale` field handles template resolution mismatches between capture and playback:

| Scenario | `scale` value |
|----------|--------------|
| Templates captured at full device res, playing back at full res | `1.0` (default) |
| Templates captured at 540px width on a 1080px device | `0.5` |
| Playing back on a 4K display but templates from 1080p | `0.5` |

Internally, coordinates sent to `Click`/`Move` are divided by `scale` before delivery to the device, so your template images remain platform-independent.

---


## Coordinate space

Every coordinate in a script — `x`/`y` on `click`, `hold`, `drag`, `if_pixel`, `region` bounds, and the offsets that `find_image`/`ocr` produce — is expressed in **the pixel space of the screenshot the same run takes**. Which screenshot that is depends on the target:

| Run | `Screenshot()` returns | `(0, 0)` is | `ScreenSize()` |
|-----|------------------------|-------------|----------------|
| Full screen (no `window:` block) | the whole X root / display | the top-left of the monitor | monitor size |
| Window-scoped (top-level `window:` block) | the **client area** of that window only, decorations excluded | the top-left of the client area | the client area size |

Consequences worth knowing:

- **A `find_image` → `click` round trip always lands.** The template is matched inside the captured image and the resulting coordinates are injected with the same origin, so a match found at `(x, y)` is clicked at `(x, y)`. Under a reparenting window manager this was not true for window-scoped runs: capture used the client origin while input used the decorated frame origin, so clicks landed one title-bar and border too far down and right.
- **Window-scoped scripts are position-independent.** A script written against a window's client area keeps working after the window is moved, and never clicks through to whatever is behind it.
- **Title bars and window borders are not addressable** in a window-scoped run. They are not part of the captured image, so no coordinate can reach them. Use a full-screen run to drive window chrome.
- **Region shorthands resolve against that same image.** In a window-scoped run `region: BR` is the bottom-right quadrant of the *client area*, `region: "0,0,200,120"` is a box measured from the client top-left.
- `window` mode `geometry` reports and accepts **client-area** `x`/`y` too, so a geometry you read back can be reused as a click origin without conversion.

The one place frame (decorated) geometry is still reported is the window-picker API (`pkg/display`), which lists windows the way the user sees them on screen. Those coordinates are for display, not for clicking.

---

## 3. Core Actions

### `wait`
Pauses execution for a specified duration.
```yaml
action: wait
duration: 1.5s  # Supports "ns", "us", "ms", "s", "m", "h" (Go time format)
```

### `click`
Simulates a mouse click at window-relative (or screen) coordinates.
```yaml
action: click
x: 450
y: 320
button: left     # "left", "middle", "right", or a raw X button number (e.g. "8" = browser Back)
clicks: 1        # Press/release pairs at ONE pointer warp: 2 is a real double-click
click_gap: 40ms  # Gap between the repeated presses
no_activate: false
```
Omitting `x`/`y` clicks the last match made by `find_image`/`find_text`/`ocr`/`wait_until`.

`clicks: N` sends N press/release pairs without re-warping between them, which is what
makes the pair a double-click to a fast application instead of two single clicks.

An **HTTP** run raises and activates the run's target window (`_NET_ACTIVE_WINDOW`,
plus a 150 ms settle) before the first press, so `click` in a field followed by `type`
lands where it was aimed. **YAML scripts never do this** — activation is chosen by the
caller, not by an absent step field, so a script written to be focusless stays
focusless. `no_activate: true` switches it off for a single step of an HTTP run.
Focus cannot be *verified* generically (focus-follows-mouse, embedded windows, WMs
without `_NET_ACTIVE_WINDOW`), which is why the engine acts on the assumption instead of
detecting it.

### `move`
Moves the cursor to specific coordinate bounds.
```yaml
action: move
x: 100
y: 200
```

### `hold`
Presses and **keeps the pointer button held down** at a coordinate. Use it
together with `move` and `release` to build drag-and-drop, sliding (e.g.
scrcpy phone screen swipes), and long-press gestures step by step.
```yaml
action: hold
x: 450
y: 320
button: left     # "left", "middle", "right"
```

### `release`
Lets the held pointer button go at a coordinate. Releasing a button that was
never pressed is tolerated. Coordinates are optional — they fall back to the
last-found match position when omitted.
```yaml
action: release
x: 700
y: 500
button: left
```

### `drag`
Performs a full **hold → move → release** gesture in one step: presses the
button at `(x, y)`, moves to `(x2, y2)` along a smooth interpolated path
spread over `duration`, then releases. Ideal for one-shot slides and drags on
any target. When `(x2, y2)` equals `(x, y)` it degrades to a **long press**.
```yaml
action: drag
x: 450
y: 320
x2: 450
y2: 800
button: left
duration: 500ms   # Optional. Default 300ms. Controls path smoothness.
```

**Example — slide an scrcpy phone screen (swipe to the next page):**
```yaml
steps:
  - action: drag
    x: 540
    y: 900
    x2: 540
    y2: 200
    button: left
    duration: 300ms
```

**Example — drag-and-drop an icon:**
```yaml
steps:
  - action: hold
    x: 120
    y: 400
    button: left
  - action: move
    x: 360
    y: 640
  - action: release
    x: 360
    y: 640
    button: left
```

> **Backend notes:** `drag`/`hold`/`release` work on all targets. On X11/VFB
> (including scrcpy) they are injected as native press/motion/release events.
> On ADB they use Android's `input motionevent` (Android 10+). On VNC/WDA they
> use the protocol's button-mask / W3C pointer actions.

### `type`
Simulates focusless text input typing.
```yaml
action: type
text: "admin_login"
delay: 12ms          # Per-key delay
chunk_runes: 200     # Runes per injection batch
```
`chunk_runes` exists because a single `Type` call cannot be interrupted: the run is
cancelled *between* chunks, so a 5000-character paste stops when you ask it to rather
than when it finishes.

### `key`
Simulates special keyboard shortcut keypresses.
```yaml
action: key
keys: "Return"   # Supports single keys (e.g. "Tab", "space") or shortcuts (e.g. "ctrl+shift+t")
```

### `notify`
Triggers native OS notifications via standard desktop notification services.
```yaml
action: notify
title: "Automation Started"
message: "Now executing rewards loop..."
```

### `log`
Logs a message to the application standard output (stdout/stderr) for silent/agent debugging.
```yaml
action: log
message: "Clicking target element..."
# Fallback field:
# text: "Clicking target element..."
```


### `run`
Spawns background OS shell commands.
```yaml
action: run
command: "xdg-open https://google.com"
```

### `clipboard`
Manipulates the system clipboard (copy, clear, and read-back).
```yaml
# Copy text to clipboard
action: clipboard
mode: copy             # Options: "copy", "clear", "read" (or fallback via 'action_type')
text: "Copied value"

# Read text from clipboard into a variable
action: clipboard
mode: read
name: my_variable
```

### `file`
Performs file operations (reading, writing, appending).
```yaml
# Write text to a file (overwrites existing file content)
action: file
mode: write            # Options: "write", "append", "read"
target: "logs.txt"     # Relative to script directory, or absolute path
text: "Initialized log file\n"

# Append text to a file
action: file
mode: append
target: "logs.txt"
text: "Append this log message\n"

# Read file contents into a variable
action: file
mode: read
target: "logs.txt"
name: file_contents
```

### `window`
Manages native OS window state, stack placement, and geometry coordinates focuslessly.
```yaml
action: window
mode: activate         # Options: "activate", "close", "minimize", "maximize", "fullscreen", "restore", "raise", "lower", "geometry"
window:                # Optional target filter (defaults to current window context)
  title: "VLC media player"
```
For `geometry` mode, window position and dimensions are adjusted (omitted coordinates retain original window values). `x`/`y` address the **client area** origin — the same `(0, 0)` the screenshots of that window start from, so the value can be used as a click origin without conversion. `offset_x`/`offset_y` are the target width and height.
```yaml
action: window
mode: geometry
x: 100                 # Target X position of the client area (variables supported)
y: 150                 # Target Y position of the client area
offset_x: 800          # Target width
offset_y: 600          # Target height
```

**Requests are verified, and a refusal is an error.** A window manager is free to ignore, clamp or adjust any of these requests, and it used to be able to do so silently. The action now reads the answer back from the X server and fails when the window did not end up where it was asked to:

| `mode` | what is verified | typical failure text |
|--------|------------------|----------------------|
| `minimize` | the window actually left the screen (frame unmapped, or `_NET_WM_STATE_HIDDEN`) | `did not become off screen (iconified or unmapped) … the window manager ignored the request` |
| `fullscreen` | `_NET_WM_STATE` really contains `_NET_WM_STATE_FULLSCREEN` (maximized is cleared first, because a maximized window keeps its work-area constraint) | `did not enter _NET_WM_STATE_FULLSCREEN …` |
| `maximize` / `restore` | the state atom, and that the window is viewable again | as above |
| `geometry` | the client rectangle the window manager applied (±2 px, 1 s budget) | `did not reach geometry 800x600+100+150 within 1s (still 1707x960+64+64) …` |

Two consequences for scripts:

- Requests outside the 16-bit geometry range (a position beyond ±32767, or a size beyond 65535) are **rejected before anything is sent**. X encodes window geometry in 16 bits, so such a request used to wrap around into a tiny or misplaced window.
- A `minimize` on a window manager that does not iconify (some tiling WMs) now reports an error instead of succeeding. Guard it with `when:`/`if_window` or accept the failure.

Under a window manager there is no way to force a managed window's geometry, so `geometry` asks politely via `_NET_MOVERESIZE_WINDOW` and then measures what happened. With no window manager at all (a bare `Xvfb`, or an `override_redirect` window) it configures the window directly, which is what makes headless runs deterministic.

### `scroll`
Sends wheel-button clicks at one point.
```yaml
action: scroll
direction: down      # "up", "down", "left", "right"
amount: 3            # Wheel ticks; default 3, clamped to 50
x: 640               # Optional; default: the last match, else the centre of the capture
y: 400
```
`amount` is a tick count, not a distance: how far a tick scrolls is the scrolled
widget's decision, not the engine's. An unknown `direction` is an error, so a typo
never becomes a silent no-op.

### `screenshot`
Writes one capture of the run's target to disk.
```yaml
action: screenshot
output: "state/after.png"   # Under output_dir, or an absolute path (`ocr`/`if_found` resolve against the script directory; a capture is machine output, not a script asset)
region: "0,0,800,600"       # Optional crop, resolved against the captured image
```
The step's `data` is `{path, width, height, bytes}`. Over HTTP this writes no
pipeline side effects — no clipboard copy, no upload, no overlay (see
`ZEN_CAP_SERVER_API.md`); from YAML it is equally inert.

### `wait_until`
Blocks on a condition, so a script waits for a state instead of guessing a duration.
```yaml
action: wait_until
wait_for: window       # "window", "window_gone", "image", "text", "pixel"
window:                # for window / window_gone
  title: "Preview"
timeout: 10s           # default 10s
```
| `wait_for` | also needs | succeeds when |
|------------|----------|---------------|
| `window` | `window:` | a window matches the selector |
| `window_gone` | `window:` | no window matches any more |
| `image` | `image:` (template), optional `confidence:` (0.9), `region:` | the template is on screen |
| `text` | `text:` (or `target:`), optional `language:`/`model:`/`region:` | OCR reads the string |
| `pixel` | `x:`, `y:`, `color:` (`#rrggbb`), optional `tolerance:` | the pixel matches |

On success the match coordinates are recorded exactly as `find_image`/`find_text`
records them, and `data` carries them. On failure the message names both halves of
the mismatch — what was wanted, and what was actually seen — because over HTTP that
string is the whole diagnostic. Every condition is bounded by `timeout` *and* by the
run's own deadline, and a capture that has started failing (a window that died) cannot
outrun either.

### `window_state`
Reads a window back from the X server: the client rectangle, the map state and the
`_NET_WM_STATE` atoms.
```yaml
action: window_state
window:                # Optional; defaults to the run's own window
  class: "firefox"
```
Use it after a `window` mutation to learn whether the window manager *obeyed*, rather
than whether the request was sent — `window` modes already verify, but this is how a
script asserts the state it expects.

### `stop`
Terminates the script execution prematurely with a success status.
```yaml
action: stop
message: "Execution successfully finished early"
```

### `ocr`
Performs OCR to find a target text substring on the active window or screen, saves the cropped bounding box (textbox) to the specified output file, and optionally triggers click/move actions on it.
```yaml
action: ocr
text: "Play"                  # Target text substring to locate (or 'target' / 'find')
output: "templates/play.png"  # Optional path to save the cropped textbox image to
region: "BL"                 # Optional region search bounds (e.g. "TL", "BL", "x,y,w,h")
language: "ja"               # Optional language override (e.g. "ja", "ch", "en")
model: "ja"                  # Optional OCR model identifier override
timeout: 5s                  # Max wait time for the text to appear
then: click                  # Optional action to execute if found: "click", "move", "none"
button: left                 # Optional mouse button for click action
offset_x: 10                 # Optional horizontal click offset
offset_y: 5                  # Optional vertical click offset
```

---

## 4. Control Flow & Branching

### `loop`
Repeats a sequence of nested steps. Loops can be nested infinitely.
```yaml
action: loop
count: 5
steps:
  - action: click
    x: 150
    y: 200
  - action: wait
    duration: 500ms
```

### `if_found` (Conditionals)
Performs vision-based search queries on the targeted window (or screen) and branches execution based on existence.

**Example 1: Image Template Matching (`type: image`)**
```yaml
action: if_found
type: image                  # Options: "image", "text", "ocr"
target: "templates/play.png" # Path to template image
region: "BL"                 # Optional search bounds: e.g. "x,y,w,h" OR quick-dirty templates:
                             # "TL" (Top Left), "TR" (Top Right), "BL" (Bottom Left), "BR" (Bottom Right)
                             # "HL" (Left Half), "HR" (Right Half), "HT" (Top Half), "HB" (Bottom Half)
similarity: 0.85             # Visual threshold (0.0 to 1.0)
wait_timeout: 5s             # Max wait time for element to appear
steps:
  - action: click            # Executes if target is found
    x: -1                    # -1 means auto-click the detected target center!
    y: -1
    button: left
else:
  - action: notify           # Executes if target is not found
    title: "Not Found"
    message: "Could not find play button"
```

**Example 2: OCR Text Matching (`type: ocr` or `type: text`)**
```yaml
action: if_found
type: ocr                     # Options: "image", "text", "ocr"
target: "target text"        # Target text substring
region: "BL"                 # Optional search bounds
similarity: 0.85             # Visual threshold
language: "ja"               # OCR specific: language code (e.g., "ch", "en", "ja", "ko")
model: "ja"                  # OCR specific: model selector
output: "cropped_textbox.png"# OCR specific: Path to save the cropped textbox image if found
wait_timeout: 5s             # Max wait time for element to appear
steps:
  - action: click            # Executes if target is found
    x: -1                    # -1 means auto-click the detected target center!
    y: -1
    button: left
else:
  - action: notify           # Executes if target is not found
    title: "Not Found"
    message: "Could not find text"
```

### `if_pixel` (Pixel Color Check)
Samples the color at a coordinate on the targeted window (or screen) and checks it against a target hex color within a tolerance threshold.

```yaml
action: if_pixel
x: 450                         # X coordinate (variables/expressions supported)
y: 320                         # Y coordinate
color: "#FF0000"               # Target hex color (starts with #)
tolerance: 10                  # Allowed RGB channel difference (0 to 255)
steps:
  - action: log
    message: "Red pixel detected!"
else:
  - action: log
    message: "Color mismatched."
```

### `if_window` (Window Condition Check)
Checks if a window matching title/class exists, waits for it, and branches execution.

```yaml
action: if_window
mode: exists                   # Options: "exists" (or "present"), "absent" (or "not_exists")
window:
  title: "Firefox"             # Substring to match window title
  class: "firefox"             # Substring to match window class
wait_timeout: "5s"             # Maximum time to wait for condition to be met (Go duration format)
steps:
  - action: log
    message: "Firefox detected, performing tasks..."
else:
  - action: log
    message: "Firefox was not found."
```

---

## 5. Vision Engine Mechanics

### Image Template Matching (`type: image`)
- Uses optimized **Sum of Absolute Differences (SAD)** pixel calculation.
- Automatically handles downsampling and early-exit thresholds to ensure high-performance evaluations.
- Paths are resolved relative to the script's directory.

### OCR Substring Finding (`type: text` or `type: ocr`)
- Communicates natively with the local `zen-lights` PaddleOCR server (`http://localhost:8765/recognize` or `/ocr`).
- Extracts precise bounding-boxes (`Bounds`) matching substrings.
- Supports coordinates re-mapping: automatically converts text coordinates into focusless mouse clicks.
- Supports step-level `language` and `model` overrides.
- Supports cropping and saving the matched bounding-box image to the file specified in the `output` field for hybrid workflow integration (e.g. `output: "textbox.png"`).

---

## 6. Advanced Scripting Extensions

Zen-Cap includes a powerful declarative scripting layer to handle dynamic state, conditional logic, and reusable code blocks without requiring external scripting dependencies.

### Variables & Expressions (`action: var`)
Variables are stored in a persistent execution scope and can be set, updated, and interpolated dynamically using the `${variable_name}` syntax.

#### Setting Variables
Use the `var` action to assign or calculate values:
```yaml
action: var
name: counter
value: 0
```

#### Math & Arithmetic
Basic binary arithmetic operations (`+`, `-`, `*`, `/`) are supported:
```yaml
action: var
name: counter
value: "${counter} + 1"
```

#### String Interpolation
Variables can be embedded within string parameters and are automatically resolved prior to step execution:
```yaml
action: log
message: "Current loop iteration is ${counter}"
```

#### Dotted Path Navigation
For structured outputs, nested fields can be traversed using dotted path notation:
```yaml
action: var
name: ocr_x
value: "${last.ocr.bounds.Min.X}"
```

### Conditional Execution (`when` filter)
Any automation action can include a `when` clause. The step will only execute if the condition evaluates to `true`.
```yaml
action: click
x: 100
y: 200
when: "${counter} < 5"
```
Supported operators: `<=`, `>=`, `==`, `!=`, `<`, `>` for numbers, booleans, and strings.

### Control Flow Jumps (`label` and `goto`)
You can define custom execution labels and jump to them conditionally or unconditionally, enabling loops, retries, and custom branches.

```yaml
steps:
  - label: retry_start
    action: log
    message: "Attempting task..."

  - action: if_found
    type: image
    target: "success.png"
    wait_timeout: "2s"
    steps:
      - action: goto
        target: success_done
    else:
      - action: var
        name: retries
        value: "${retries} + 1"
      - action: goto
        target: retry_start
        when: "${retries} < 3"

  - label: success_done
    action: notify
    title: "Success"
    message: "Task completed successfully"
```

### Reusable Functions (`functions` & `action: call`)
Procedures can be defined under the top-level `functions` block and called via `action: call`. Parameters passed via `args` shadow variables within the function's scope but restore their caller-level state when the procedure finishes.

```yaml
name: "Reusable Procedure Example"
functions:
  click_target:
    steps:
      - action: log
        message: "Clicking target text: ${target_text}"
      - action: if_found
        type: ocr
        target: "${target_text}"
        steps:
          - action: click
            x: -1
            y: -1

steps:
  - action: call
    target: click_target
    args:
      target_text: "Submit"
```
---

## 7. Over HTTP

Everything in this document is also reachable without a YAML file: the daemon exposes
the same step vocabulary at `POST /automation/exec`, where `steps` is exactly the step
object described above. Full wire format, error codes, the read endpoints
(`/automation/actions`, `/automation/windows`, `/automation/window`,
`/automation/state`, `/automation/abort`) and the security notes live in
**[`ZEN_CAP_SERVER_API.md`](ZEN_CAP_SERVER_API.md)**; `GET /automation/actions` is the
machine-readable version of this specification, generated from the engine's own
dispatch table.

One behavioural difference matters, and it is the only one:

> **Variables do not survive a request.** Within one `POST /automation/exec`, `${name}`,
> `loop`, `if_found`, `var` and `call` behave exactly as in a script. Across requests
> there is no server state: the response returns the run's `variables`, and the next
> request sends them back in `vars`. There is deliberately no session registry, no TTL
> and nothing to expire — which is also why restarting the daemon mid-loop is harmless.

Two smaller things worth knowing before writing an agent:

- **Requests are serialised, not concurrent.** One run may inject input at a time, and
  a queued request that runs out of time is answered `429 busy_timeout`. This is not a
  limitation to work around: XTEST drives the single global pointer, so two "parallel"
  runs would interleave into garbage gestures.
- **A run has a hard ceiling** (`api_max_run_duration_seconds`, default 60 s), and a
  cancelled or timed-out run force-releases the pointer buttons before responding, so
  `loop: count: 1000000` cannot hold the desktop hostage.

Actions that produce no X input (`screenshot`, `window_state`, `file`, `clipboard`,
`run`) are still accepted when `deny_api_input` is set; see the security section of
`ZEN_CAP_SERVER_API.md` for what that implies.
