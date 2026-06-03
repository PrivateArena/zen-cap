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

All backends expose the same action API — `click`, `move`, `type`, `key`, `scroll`, `find_image`, `if_found`, `if_pixel`, etc. work identically regardless of target.

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
button: left     # Options: "left", "middle", "right", "wheel-up", "wheel-down"
count: 1         # Number of clicks (e.g. 2 for double-click)
```

### `move`
Moves the cursor to specific coordinate bounds.
```yaml
action: move
x: 100
y: 200
```

### `type`
Simulates focusless text input typing.
```yaml
action: type
text: "admin_login"
```

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
For `geometry` mode, window position and dimensions are adjusted (omitted coordinates retain original window values):
```yaml
action: window
mode: geometry
x: 100                 # Target X position (variables supported)
y: 150                 # Target Y position
offset_x: 800          # Target width
offset_y: 600          # Target height
```

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
