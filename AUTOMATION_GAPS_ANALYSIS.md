Good, I have the full Actiona action list. Here's a thorough gap analysis:

---

## Zen-Cap vs. Actiona: Gap Analysis

### ✅ What You Already Cover Well

**Devices (input simulation)** — full coverage:
- `click`, `move`, `type`, `key` all map cleanly. Your `button: wheel-up/wheel-down` covers Actiona's dedicated Wheel action. You're arguably ahead here with focusless targeting.

**System actions** — mostly covered:
- `run` covers both Command and Detached Command (and Open URL via `xdg-open`). `notify` covers Notification. `if_found type:image` covers Find Image — and your OCR on top is a significant advantage Actiona lacks entirely.

**Internal/Control flow** — well covered:
- `loop`, `goto`/`label`, `var`, `when` (variable condition), `log` (console) all map cleanly. `wait` covers Pause.

**Procedures** — covered with `functions` + `action: call`, and your scoped args are actually cleaner than Actiona's begin/end procedure blocks.

---

### ❌ Gaps vs. Actiona

**Windows category — entirely missing.** This is your biggest gap for general-purpose use:

| Actiona Action | What It Does | Your Status |
|---|---|---|
| Window condition | Check if a window exists, wait for it, branch | ❌ Missing |
| Window | Close/minimize/maximize/resize/move a window | ❌ Missing |
| Message Box | Show a dialog, get yes/no from user | ❌ Missing |
| Data input / Multi data input | Prompt user for text or choice mid-script | ❌ Missing |

You can *target* a window for input, but you can't manage windows (close, resize, move, bring to front) or do interactive user prompts.

**Data category — significant gap:**

| Actiona Action | Your Status |
|---|---|
| Read/Write text file | ❌ Missing |
| Read/Write clipboard | Partially covered (`clipboard copy/clear`, but no *read* back into a variable) |
| Read environment variable | ❌ Missing |
| Web download | ❌ (you can `run curl` as a workaround, but it's not native) |
| Read/Write INI file | ❌ Missing |
| Read/Write binary file | ❌ Missing |
| Send mail | ❌ Missing |
| Copy file | ❌ Missing |

The most impactful ones for real automations are file read/write and clipboard read-back. The rest (INI, binary, mail) are lower priority.

**System category — smaller gaps:**

| Actiona Action | Your Status |
|---|---|
| Pixel color check | ❌ Missing — Actiona can sample a pixel's color and branch on it, useful for state detection without image templates |
| Kill process | ❌ Missing (workaround: `run kill`) |
| Play sound | ❌ Missing |
| Text to speech | ❌ Missing |
| System (restart/shutdown/lock) | ❌ Missing |

Of these, **pixel color** is the most practically useful for automation scripts.

**Internal — two notable gaps:**

- **Time condition**: branch based on current time/date (e.g. "only run between 9am-5pm"). Unique to Actiona, no equivalent in your API.
- **Stop/Exit**: a way to cleanly halt script execution mid-run. You have `goto` which can jump past everything, but there's no explicit `stop` or `exit` action.

**JavaScript/scripting escape hatch**: Actiona lets users drop into full EcmaScript in any field or via a Code action. Your `when` expressions and `var` math are solid but limited to simple binary operations — no string manipulation, no regex, no math functions beyond `+/-/*//`.

---

### 🏆 Where You're Ahead of Actiona

These are meaningful advantages, not just parity:

1. **Focusless execution** — Actiona requires the target window to be active/focused. Your EWMH/XTest approach is a fundamental architectural win for background automation.
2. **OCR (PaddleOCR integration)** — Actiona has zero OCR support. Your `ocr` action and `if_found type:ocr` with bounding-box click, language/model overrides, and image cropping output is a major differentiator.
3. **YAML as the format** — Plain-text, diffable, shareable scripts vs. Actiona's GUI-native format. Much better for version control and script sharing.
4. **Dotted path navigation** for structured OCR output (`${last.ocr.bounds.Min.X}`) — no equivalent in Actiona.
5. **Nested infinite loops** — Actiona's Loop action is simpler; your nesting is more powerful.

---

### Priority Recommendation

If you want parity on the features that matter most for real users:

1. **Clipboard read** — extend `clipboard` with `action_type: read` that stores result into a variable. Small lift, high value.
2. **File read/write** — a `file` action with `action_type: read/write/append`. Unlocks config-driven automations.
3. **Window management** (`action: window`) — close, minimize, maximize, resize a window by title/class. Complements your existing window targeting nicely.
4. **`stop` action** — trivial to add, but users will want it for error paths.
5. **Pixel color check** — `if_pixel` or extend `if_found type:pixel` — lightweight state detection that doesn't need a template image.

The mail, INI, binary file, TTS, and system shutdown actions are lower priority — most users either don't need them or can shell out via `run`.