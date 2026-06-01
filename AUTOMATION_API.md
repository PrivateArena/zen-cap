# Zen-Cap Desktop Automation Engine YAML API Specification

Zen-Cap features a YAML-driven, focusless desktop automation engine. Scripts are stored individually as `.yaml` files inside the `automations/` directory next to the binary or in your workspace.

---

## 1. Script Schema Root

Every automation script represents a single YAML document with the following top-level keys:

```yaml
# The user-facing name displayed in the X11 GUI selector
name: "Daily Game Rewards"

# Optional window targeting for focusless execution
window:
  title: "MyGame"
  class: "my-game-client"

# Ordered list of execution steps
steps:
  - action: wait
    duration: 1s
```

### `window` Configuration
If a `window` block is provided, Zen-Cap uses `xdotool` search queries to resolve the active `WindowID`:
- **`title`**: String to match against window title.
- **`class`**: String to match against window class name.
- Zen-Cap runs all input actions (`click`, `type`, `key`, etc.) directly targeting the resolved `WindowID` without stealing mouse focus or switching active desktops.
- If omitted, coordinates and actions target the active fullscreen coordinate system.

---

## 2. Core Actions

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

### `run`
Spawns background OS shell commands.
```yaml
action: run
command: "xdg-open https://google.com"
```

### `clipboard`
Manipulates the system clipboard.
```yaml
action: clipboard
action_type: copy   # Options: "copy", "clear"
text: "Copied value"
```

---

## 3. Control Flow & Branching

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

```yaml
action: if_found
type: image                  # Options: "image", "text"
target: "templates/play.png" # Path to template image OR target text substring
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

---

## 4. Vision Engine Mechanics

### Image Template Matching (`type: image`)
- Uses optimized **Sum of Absolute Differences (SAD)** pixel calculation.
- Automatically handles downsampling and early-exit thresholds to ensure high-performance evaluations.
- Paths are resolved relative to the script's directory.

### OCR Substring Finding (`type: text`)
- Communicates natively with the local `zen-lights` PaddleOCR server (`http://localhost:8765/recognize`).
- Extracts precise bounding-boxes (`Bounds`) matching substrings.
- Supports coordinates re-mapping: automatically converts text coordinates into focusless mouse clicks.
