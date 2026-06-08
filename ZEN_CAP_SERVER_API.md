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
  "clip_mode": "none"                  // Post-capture clipboard action: "image", "path", "ocr", "translate", "none"
}
```

#### Response (Success - 200 OK)
```json
{
  "status": "success",
  "path": "/tmp/screenshot.png"
}
```

#### Response (Error - 500 Internal Server Error)
```json
{
  "error": "window not found (pid=0, class=\"firefox\", title=\"\")"
}
```

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
