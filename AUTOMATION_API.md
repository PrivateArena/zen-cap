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
If a `window` block is provided, Zen-Cap queries window properties via EWMH/ICCCM APIs to resolve the active `WindowID`:
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
Manipulates the system clipboard.
```yaml
action: clipboard
action_type: copy   # Options: "copy", "clear"
text: "Copied value"
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

---

## 4. Vision Engine Mechanics

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

## 5. Advanced Scripting Extensions

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
