# Zen-Cap Local HTTP API Reference

The `zen-cap` background service daemon runs a local HTTP API server designed to let external applications, user automation scripts, and AI agents control window captures programmatically.

---

## Configuration

The API server address is configured via `api_address` in the `config.json` file (typically stored at `~/.config/zen-cap/config.json`):

```json
{
  "api_address": "localhost:4444"
}
```

*Default fallback is `localhost:4444`.*

Three more keys govern the API, all optional:

```json
{
  "deny_api_input": false,               // true = refuse any request that would synthesise a click or keypress
  "api_max_run_duration_seconds": 60,    // hard ceiling of one /automation/exec run
  "api_max_inline_bytes": 8388608        // cap of a base64 image returned inline (8 MiB)
}
```

**Gotcha:** `./run.sh` watches `go`, `mod`, `sum`, `yaml`, `yml` and `toml` files only.
Editing `config.json` does **not** restart the daemon — restart it by hand for a config
change to take effect.

---

## API Endpoints

### 1. Capture Screenshot (`POST /screenshot`)
Takes a screenshot according to target parameters, raises/focuses the window if targeted, and writes the output file.

#### Request Body Schema
```json
{
  "output": "/tmp/screenshot.png",     // Destination path (defaults to auto-generated name in output_dir)
  "region": "100,200,800,600",         // Specific X,Y,W,H box OR "interactive" to prompt user region select
  "window": "active",                  // "active", "interactive" (user select), window ID (hex/dec), or class/title substring
  "pid": 1234,                         // Focus and capture a specific Process ID
  "class": "firefox",                  // Focus and capture window matching WM_CLASS
  "title": "GitHub",                   // Focus and capture window matching Title
  "launch": "firefox --new-window",    // Command to launch if the targeted window isn't open yet
  "clip_mode": "none",                 // Post-capture clipboard action: "image", "path", "ocr", "translate", "none"
  "inline": false                      // true = also return the PNG as base64 in the response
}
```

#### Response (Success - 200 OK)
```json
{
  "status": "success",
  "path": "/tmp/screenshot.png"
}
```

With `"inline": true` the response additionally carries `base64`, `encoding`, `mime`,
`bytes` and `truncated` (true, with no `base64`, when the image exceeds
`api_max_inline_bytes` — the file is written either way).

#### Response (Error - 500 Internal Server Error)
```json
{
  "error": "window not found (pid=0, class=\"firefox\", title=\"\")"
}
```

> This endpoint runs the **capture pipeline** (clipboard, upload, LLM, annotation
> overlay) according to your `after_capture_tasks`. For a bare capture with no
> side effects — the one an automation loop wants — use
> [`POST /automation/screenshot`](#post-automationscreenshot--capture-without-the-pipeline).

---

### 2. Register Collaborative Session (`POST /collaborate`)
Registers a one-shot webhook URL. The next manual capture performed by the user (via hotkeys or daemon signal) will POST the captured file path to this webhook, and clear the registration.

#### Request Body Schema
```json
{
  "url": "http://localhost:3001/api/collaborate?id=collab_session_123"
}
```

#### Response (Success - 200 OK)
```json
{
  "status": "success"
}
```

#### Webhook POST Payload (Sent to the registered URL)
```json
{
  "path": "/tmp/zen-cap/screenshot_20260608_130000.png"
}
```

---

## Automation API

`zen-cap` is not only a capture tool: it contains a desktop automation engine, and
`/automation/*` exposes it so an AI agent (or a script) can drive the app it is
debugging — enumerate a window, click a button it located in a screenshot, type
into a field, scroll a list, read back what the window manager actually did.

Requirements and shape of the design, stated up front because they are not guesses:

* **X11 only.** Input synthesis needs the XTEST extension, so there is nothing to
  automate on a Wayland session.
* **Stateless.** Every request names its target window; nothing survives a daemon
  restart, and no session id or TTL exists. Variables are returned by each run and
  re-sent in the next request's `vars`.
* **No authentication.** The daemon binds localhost for a single trusted user; that
  is the threat model. See [Security](#security-and-the-residual-risk).
* **No pipeline** on automation captures.

**Coordinate space — read this before sending a coordinate.** A window-scoped
request addresses the **client area**: `(0, 0)` is the top-left of the window's
client rectangle, which is the same origin `Screenshot()` captures from. The title
bar and borders are *not* included, and `window_id`/`geometry` in every response
are reported in that same client space. The frame rectangle (what the window
manager drew around the window) is only ever shown by the window-picker UI.

**One pointer.** XTEST drives the real, global pointer, so `POST /automation/exec`
requests are serialised: a second run waits for the first to finish, and if its own
timeout expires first it is answered `429 busy_timeout`. Reads
(`/automation/windows`, `/window`, `/state`, `/actions`, `/screenshot`) never wait.

### `GET /automation/actions` — learn the vocabulary

The machine-readable catalog of every action, its fields, types, enums, defaults and
example `data` payloads, plus the wire schema of a step. It is generated from the
engine's own dispatch table, so it cannot be out of date.

```bash
curl -s localhost:4444/automation/actions | jq -r '.actions[].action'
curl -s localhost:4444/automation/actions | jq '.actions[] | select(.action=="scroll")'
```

```json
{
  "status": "success",
  "actions": [
    {
      "action": "scroll",
      "summary": "Scroll wheel clicks at a point (X buttons 4-7).",
      "injects_input": true,
      "fields": [
        { "name": "direction", "type": "string", "description": "Scroll direction.", "required": true, "enum": ["up", "down", "left", "right"] },
        { "name": "amount", "type": "integer", "description": "Ticks, clamped to 50.", "default": "3" }
      ]
    }
  ],
  "step_schema": [ { "name": "x", "type": "integer", "description": "Primary x coordinate (target space)." } ]
}
```

### `POST /automation/exec` — run steps

The only write endpoint. `steps` is **exactly** the step object the YAML scripts use
(see `AUTOMATION_API.md`), so the whole script vocabulary — `loop`, `if_found`,
`if_pixel`, `if_window`, `var`, `call`, `goto`, `stop` and `${variable}`
interpolation — works over HTTP with no translation layer.

#### Request Body Schema
```json
{
  "window": "gnome-terminal",          // "active", a window id (hex 0x… or decimal), or a title/class substring
  "window_id": "0x400001",             // an explicit window id (takes precedence over "window")
  "pid": 1234,                         // target by process id
  "class": "firefox",                   // target by WM_CLASS substring
  "title": "GitHub",                    // target by title substring
  "launch": "firefox --new-window",     // start this, then wait for its window
  "display": ":0",                      // X display, default = the daemon's $DISPLAY
  "no_activate": false,                 // true = do not raise/focus the window while resolving
  "steps": [ { "action": "click", "x": 480, "y": 320, "clicks": 2 } ],
  "timeout": "30s",                     // clamped by api_max_run_duration_seconds
  "stop_on_error": true,                // default true, like a YAML script
  "vars": { "counter": 3 },             // variables to seed this run with
  "with_log": false                      // true = include the run's log lines in the response
}
```

Omit every target field for a full-screen run (`window_id: 0` is *not* the same thing
— it is rejected, because "no window" and "window zero" are different requests).

#### Response (Success - 200 OK)
```json
{
  "status": "success",
  "ok": true,
  "run_id": "r7",
  "window_id": 4194305,
  "window_title": "Preview",
  "window_class": "org.gnome.Nautilus",
  "geometry": { "x": 100, "y": 100, "w": 800, "h": 600 },
  "screen": [1920, 1080],
  "steps": [
    { "index": 0, "action": "click",  "ok": true, "elapsed_ms": 12, "x": 480, "y": 320 },
    { "index": 1, "action": "find_text", "ok": true, "elapsed_ms": 210, "x": 812, "y": 233,
      "data": { "x": 812, "y": 233, "confidence": 0.97 } }
  ],
  "variables": { "counter": 3 },
  "stopped": false
}
```

`steps` is a trace of **every** step that ran, in execution order, including the body
of a `loop` or `if_found`; the entry for the enclosing step comes after its children
(it is recorded when it finishes). `data` is whatever the action produced — match
coordinates, a screenshot path, a window state. `x`/`y` are the coordinates the step
used, so the next request can be built from them.

A runaway `loop` cannot grow the response without limit: past 4096 trace entries (or
2048 log lines) the arrays stop and `steps_truncated` / `log_truncated` say so.

#### Response (Error)
```json
{
  "status": "error",
  "error": "step_failed: text \"Submit\" not found within timeout",
  "code": "step_failed",
  "step_index": 1,
  "run_id": "r8",
  "ok": false,
  "steps": [ /* the partial trace, exactly as in a success response */ ]
}
```

A failure still returns the partial trace: "how far did it get and what did it match
before that" is the useful part of the answer.

| Status | `code` | Meaning |
|---|---|---|
| 400 | `bad_request` | malformed body, empty `steps`, unparsable `timeout` |
| 400 | `unknown_action` | the action name does not exist (`GET /automation/actions`) |
| 400 | `dangling_goto` | `goto` out of the top-level step list; never a silent success |
| 403 | `input_disabled` | `deny_api_input` is set and the batch injects input |
| 404 | `window_not_found` | the selector matched no window |
| 409 | `window_gone` | the window died between requests |
| 429 | `busy_timeout` | another run held the pointer and this request ran out of time |
| 413 | `too_large` | an explicit `max_inline_bytes` was exceeded |
| 503 | `no_display` | the daemon has no usable X connection (e.g. a Wayland session) |
| 504 | `timeout` | the run hit its deadline or `api_max_run_duration_seconds` |
| 500 | `x_error` | X protocol failure, target creation failed |
| 500 | `step_failed` | a step failed (with `stop_on_error: true`) |
| 200 | `aborted` | someone called `POST /automation/abort`; `ok` is `false` |

### `POST /automation/screenshot` — capture without the pipeline

Same target fields as `exec`, and no `after_capture_tasks` side effects whatsoever —
no clipboard write, no upload, no LLM call, and above all no annotation overlay,
which grabs the pointer and keyboard and would deadlock the agent's own input.

```json
{
  "window": "active",
  "output": "/tmp/after.png",           // path under output_dir, or an absolute path
  "region": "0,0,800,600",              // "x,y,w,h" or a shorthand (full/TL/BR/…), against the captured area
  "inline": true,
  "max_inline_bytes": 262144            // optional, never above api_max_inline_bytes
}
```

```json
{
  "status": "success",
  "path": "/tmp/after.png",
  "width": 800, "height": 600, "bytes": 41233,
  "mime": "image/png", "encoding": "base64", "base64": "iVBORw0…",
  "truncated": false,
  "window_id": 4194305, "window_title": "Preview"
}
```

`output` is resolved inside `output_dir`, and a relative path that walks out of it
(`"../../etc/x"`) is refused with `bad_request`. An absolute path is taken as given:
an agent that names `/tmp` gets `/tmp`.

### `GET /automation/windows` — read the window list

Every viewable client window, including untitled ones — unlike the picker listing,
which drops them.

```json
{
  "status": "success",
  "active_window_id": 4194305,
  "count": 3,
  "windows": [
    {
      "id": 4194305, "id_hex": "0x400001",
      "pid": 31337, "class": "firefox", "instance": "Firefox",
      "title": "New Tab — Mozilla Firefox",
      "geometry": { "x": 0, "y": 27, "w": 1707, "h": 933 },
      "state": { "fullscreen": false, "maximized": true, "minimized": false,
                 "demands_attention": false, "states": ["_NET_WM_STATE_MAXIMIZED_HORZ", "_NET_WM_STATE_MAXIMIZED_VERT"] },
      "active": true, "map_state": "viewable"
    }
  ]
}
```

A minimized window is unmapped as far as X is concerned, so it is **not** in this
list; address it by id with `GET /automation/window?id=` instead.

### `GET /automation/window` — read one window

`?id=0x400001` (hex or decimal) or `?class=`/`?title=`/`?pid=`. Class and title use
the engine's own matching rule — case-insensitive substring, first hit in
`_NET_CLIENT_LIST` order — which is why the response also lists every other
`candidates` match: when two windows share a title, re-ask with the `id`.

### `GET /automation/state` — read the whole display

Never blocked by the input gate, so it answers while a run is mid-click.

```json
{
  "status": "success",
  "display": ":0",
  "screens": [ { "index": 0, "name": "Screen 0", "geometry": { "x": 0, "y": 0, "w": 1920, "h": 1080 } } ],
  "active_window": { "…": "as in /automation/windows" },
  "pointer": { "x": 812, "y": 233 },
  "held_buttons": [],
  "deny_api_input": false,
  "active_runs": ["r7"],
  "max_run_seconds": 60
}
```

`held_buttons` is the pointer's live button mask, read from the X server. After a
clean run it is `[]`; a non-empty list means something left a button down, which is
what `POST /automation/abort` is for.

### `POST /automation/abort` — stop everything

Cancels every in-flight run *and* force-releases pointer buttons 1-3, so an aborted
`drag` cannot leave the desktop acting as if the mouse were held. It never queues
behind the run it is trying to kill.

```json
{ "status": "success", "aborted": 1, "run_ids": ["r7"], "released_buttons": true }
```

### New actions and step fields

These exist because an HTTP agent has nothing else to lean on. They work in YAML
scripts too.

| Step | What it does |
|---|---|
| `scroll` | `direction` (up/down/left/right), `amount` (default 3, clamped to 50), optional `x`/`y` (default: last match, else the centre of the capture) |
| `screenshot` | `output`, `region`; writes a PNG and returns `path`/`width`/`height`/`bytes` |
| `wait_until` | `wait_for`: `window`, `window_gone`, `image`, `text`, `pixel`; `timeout` (default 10s). The timeout error says what was wanted *and* what was seen |
| `window_state` | reads the window back: client `geometry`, `map_state`, the `_NET_WM_STATE` atoms |
| `click: clicks` | `clicks: N` press/release pairs at **one** pointer warp (`2` is a real double-click), `click_gap` between them (default `40ms`) |
| `click: no_activate` | the default (`false`) raises and activates the window before the first press, which is what makes a later `type` land in the field you clicked |
| `type: chunk_runes` | runes per injection batch (default 200); the abort channel is checked between batches, so a 5000-character paste is cancellable |

### Variables across requests

There is no server-side session. A run returns its variable table, and the next
request sends it back:

```bash
curl -s -XPOST localhost:4444/automation/exec -d '{"steps":[{"action":"var","name":"n","value":1}]}' | jq .variables
# {"n":1} → send it forward:
curl -s -XPOST localhost:4444/automation/exec -d '{"vars":{"n":1},"steps":[{"action":"log","message":"n is ${n}"}]}'
```

The interpolation syntax is `${name}` — the same as in YAML scripts.

### Security and the residual risk

There is **no authentication**, by decision: the daemon binds localhost and the
machine has one trusted user. That has consequences worth stating plainly:

* `{"action":"run","command":"…"}` is arbitrary shell. So are the `file` and
  `clipboard` actions. A malicious web page that can make a simple cross-origin POST
  to `localhost:4444` (or a DNS-rebinding attack) could use them. Not mitigated.
* `deny_api_input` refuses actions that synthesise pointer or keyboard input. It does
  **not** cover `run`, `file` or `clipboard`, because those inject no X input. To
  block them too: stop the daemon. `automation.InjectsInput` is the single place to
  broaden that predicate if the policy ever changes.
* Synthetic keystrokes can re-trigger zen-cap's own global hotkeys, including the
  `Control-Shift-x` kill switch. Accepted; `GET /automation/state`'s `active_runs` is
  the hook for suppressing them if it ever bites.
* Focus cannot be verified generically (focus-follows-mouse, embedding, WMs without
  `_NET_ACTIVE_WINDOW`). Rather than pretend to detect it, the API makes the click
  focus the window by default and reports `active_window` before and after, so the
  agent can see reality instead of assuming it.


---

### The golden path

The reason all of this exists: make a windowed app fullscreen and look at the
result, from code, without a human touching the mouse.

```bash
BASE=http://localhost:4444
# 0. learn the vocabulary (once)
curl -s $BASE/automation/actions | jq -r '.actions[].action'
# 1. find the app
W=$(curl -s $BASE/automation/windows | jq -r '.windows[]|select(.class=="org.gnome.Nautilus")|.id')
# 2. confirm it is NOT fullscreen yet
curl -s "$BASE/automation/window?id=$W" | jq '.window.state'
# 3. double-click its canvas, then wait for the fullscreen transition, then look
curl -s -XPOST $BASE/automation/exec -H 'Content-Type: application/json' -d '{
  "window_id": "'"$W"'",
  "steps": [
    { "action": "click", "x": 480, "y": 320, "clicks": 2 },
    { "action": "wait_until", "wait_for": "window", "window": { "title": "Preview" }, "timeout": "5s" },
    { "action": "window_state" },
    { "action": "screenshot", "output": "/tmp/after.png" }
  ] }' | jq '{ok, steps: [.steps[] | {action, ok, data}]}'
curl -s $BASE/automation/window?id=$W | jq '.window.state.fullscreen'   # → true
```

Why this shape: the `window_id` from step 1 is a decimal string, accepted as decimal
or `0x…`; `clicks: 2` is one XTEST double-click at a single warp, so a fast app does
not see two single clicks; `wait_until` means the agent never sleeps blindly;
`window_state` asks whether the window manager *honoured* the fullscreen request
rather than whether it was sent; and the response carries the resolved coordinates so
the next request can be built from them.

### Checking a build

```bash
bash scripts/automation_api_smoke.sh                 # against localhost:4444
BASE=http://localhost:4444 bash scripts/automation_api_smoke.sh
```

It polls for readiness first (the dev watcher restarts the daemon mid-test), then
exercises enumeration, capture, `window_state`, a click, a scroll, a `wait_until` that
must time out **quickly** (the regression tripwire for the old unbounded poll loop),
abort and state. It exits non-zero on the first failure with the offending body.
Steps that need a window are skipped on a display that has none. To keep the input
steps off your own desktop, point a scratch daemon at Xvfb:

```bash
Xvfb :99 -screen 0 1280x800x24 &
DISPLAY=:99 LD_LIBRARY_PATH=$PWD/ffmpeg8/lib ./zen-cap service &
BASE=http://localhost:4444 bash scripts/automation_api_smoke.sh
```

---

## Integration Examples

### Curl
#### Capture Active Window
```bash
curl -X POST http://localhost:4444/screenshot \
  -H "Content-Type: application/json" \
  -d '{"window": "active", "output": "/tmp/active_win.png"}'
```

#### Focus and Capture Application by WM_CLASS (Launch if missing)
```bash
curl -X POST http://localhost:4444/screenshot \
  -H "Content-Type: application/json" \
  -d '{
    "class": "chromium",
    "launch": "chromium-browser",
    "output": "/tmp/chromium.png"
  }'
```

---

### Node.js / TypeScript
```typescript
import axios from 'axios';

async function captureActiveWindow(outputPath: string): Promise<string> {
  const response = await axios.post('http://localhost:4444/screenshot', {
    window: 'active',
    output: outputPath
  });
  return response.data.path;
}

async function startCollaborativeSession(webhookUrl: string) {
  await axios.post('http://localhost:4444/collaborate', {
    url: webhookUrl
  });
  console.log('Registered webhook. Waiting for user screenshot...');
}
```

---

### Python
```python
import requests

def capture_by_pid(pid: int, output_path: str):
    payload = {
        "pid": pid,
        "output": output_path
    }
    response = requests.post("http://localhost:4444/screenshot", json=payload)
    if response.status_code == 200:
        return response.json().get("path")
    else:
        raise Exception(f"Failed to capture: {response.json().get('error')}")
```
