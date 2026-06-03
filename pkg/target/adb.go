package target

// ADBTarget drives an Android device via the ADB host-daemon wire protocol.
// It connects directly to the ADB daemon on localhost:5037, so no `adb` binary
// needs to be in PATH at runtime (the daemon must be running).

import (
	"bytes"
	"fmt"
	"image"
	"image/png"
	"io"
	"net"
	"strings"
)

// ADBTarget is a Target backed by ADB wire protocol (TCP to localhost:5037).
type ADBTarget struct {
	serial   string
	scale    float64
	w, h     int
	scaledW  int
	scaledH  int
}

// NewADBTarget creates an ADB target. cfg.Serial selects the device; leave
// empty for single-device mode. Connects briefly to verify reachability.
func NewADBTarget(cfg Config) (*ADBTarget, error) {
	t := &ADBTarget{serial: cfg.Serial, scale: cfg.Scale}

	// Quick connectivity check + get device screen size via screencap
	img, err := t.screencap()
	if err != nil {
		return nil, fmt.Errorf("adb: initial screencap failed: %w", err)
	}
	b := img.Bounds()
	t.w, t.h = b.Dx(), b.Dy()
	if cfg.Scale > 0 && cfg.Scale != 1.0 {
		t.scaledW = int(float64(t.w) * cfg.Scale)
		t.scaledH = int(float64(t.h) * cfg.Scale)
	} else {
		t.scaledW, t.scaledH = t.w, t.h
	}
	return t, nil
}

// adbDial opens a fresh connection to the ADB host daemon and switches to the
// configured device transport. The caller owns the returned conn.
func (t *ADBTarget) adbDial() (net.Conn, error) {
	conn, err := net.Dial("tcp", "localhost:5037")
	if err != nil {
		return nil, fmt.Errorf("adb: connect to daemon: %w", err)
	}
	transport := "host:transport-any"
	if t.serial != "" {
		transport = "host:transport:" + t.serial
	}
	if err := adbSend(conn, transport); err != nil {
		conn.Close()
		return nil, err
	}
	if err := adbRecvOK(conn); err != nil {
		conn.Close()
		return nil, fmt.Errorf("adb: transport select: %w", err)
	}
	return conn, nil
}

// adbExec runs a shell command on the device and returns its output.
func (t *ADBTarget) adbExec(cmd string) ([]byte, error) {
	conn, err := t.adbDial()
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	if err := adbSend(conn, "exec:"+cmd); err != nil {
		return nil, err
	}
	if err := adbRecvOK(conn); err != nil {
		return nil, fmt.Errorf("adb exec %q: %w", cmd, err)
	}
	return io.ReadAll(conn)
}

// adbShell runs a shell command, discarding output.
func (t *ADBTarget) adbShell(cmd string) error {
	_, err := t.adbExec("shell " + cmd)
	return err
}

// screencap captures the Android screen as a PNG.
func (t *ADBTarget) screencap() (image.Image, error) {
	data, err := t.adbExec("screencap -p")
	if err != nil {
		return nil, err
	}
	return png.Decode(bytes.NewReader(data))
}

func (t *ADBTarget) Screenshot() (image.Image, error) {
	img, err := t.screencap()
	if err != nil {
		return nil, err
	}
	if t.scale > 0 && t.scale != 1.0 {
		return scaleImg(img, t.scaledW, t.scaledH), nil
	}
	return img, nil
}

func (t *ADBTarget) ScreenSize() (int, int) { return t.scaledW, t.scaledH }

func (t *ADBTarget) deviceCoord(x, y int) (int, int) {
	if t.scale > 0 && t.scale != 1.0 {
		return int(float64(x) / t.scale), int(float64(y) / t.scale)
	}
	return x, y
}

func (t *ADBTarget) Click(x, y int, button string) error {
	dx, dy := t.deviceCoord(x, y)
	return t.adbShell(fmt.Sprintf("input tap %d %d", dx, dy))
}

func (t *ADBTarget) Move(x, y int) error {
	// ADB has no native hover; send a zero-duration swipe as a move.
	dx, dy := t.deviceCoord(x, y)
	return t.adbShell(fmt.Sprintf("input swipe %d %d %d %d 1", dx, dy, dx, dy))
}

func (t *ADBTarget) Type(text string, _ int64) error {
	// Escape special shell characters for ADB text input.
	escaped := adbEscapeText(text)
	return t.adbShell("input text " + escaped)
}

func (t *ADBTarget) Key(keys string) error {
	// Map human-readable key names to Android KEYCODE constants.
	parts := strings.Split(keys, "-")
	for _, p := range parts {
		kc := androidKeycode(strings.ToLower(p))
		if err := t.adbShell(fmt.Sprintf("input keyevent %s", kc)); err != nil {
			return err
		}
	}
	return nil
}

func (t *ADBTarget) Scroll(x, y, dx, dy int) error {
	devX, devY := t.deviceCoord(x, y)
	// Scroll via swipe: end-point relative to start
	endX := devX - dx*30
	endY := devY - dy*30
	return t.adbShell(fmt.Sprintf("input swipe %d %d %d %d 200", devX, devY, endX, endY))
}

func (t *ADBTarget) Close() error { return nil }

// --- ADB wire protocol helpers ---

// adbSend writes a 4-hex-length-prefixed message to conn.
func adbSend(conn net.Conn, msg string) error {
	frame := fmt.Sprintf("%04x%s", len(msg), msg)
	_, err := conn.Write([]byte(frame))
	return err
}

// adbRecvOK reads a 4-byte status word and returns nil if it is "OKAY".
func adbRecvOK(conn net.Conn) error {
	status := make([]byte, 4)
	if _, err := io.ReadFull(conn, status); err != nil {
		return fmt.Errorf("read status: %w", err)
	}
	if string(status) != "OKAY" {
		// Read error message length + body
		lenBuf := make([]byte, 4)
		io.ReadFull(conn, lenBuf)
		var msgLen int
		fmt.Sscanf(string(lenBuf), "%04x", &msgLen)
		msg := make([]byte, msgLen)
		io.ReadFull(conn, msg)
		return fmt.Errorf("adb: %s: %s", status, msg)
	}
	return nil
}

// adbEscapeText escapes special characters for `adb shell input text`.
func adbEscapeText(s string) string {
	var b strings.Builder
	b.WriteByte('\'')
	for _, r := range s {
		switch r {
		case '\'':
			b.WriteString("'\\''")
		case '\\':
			b.WriteString("\\\\")
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('\'')
	return b.String()
}

// androidKeycode maps common key names to Android KEYCODE values.
func androidKeycode(name string) string {
	m := map[string]string{
		"return": "KEYCODE_ENTER", "enter": "KEYCODE_ENTER",
		"backspace": "KEYCODE_DEL", "del": "KEYCODE_FORWARD_DEL",
		"escape": "KEYCODE_ESCAPE", "esc": "KEYCODE_ESCAPE",
		"tab": "KEYCODE_TAB",
		"home": "KEYCODE_HOME",
		"back": "KEYCODE_BACK",
		"menu": "KEYCODE_MENU",
		"up": "KEYCODE_DPAD_UP", "down": "KEYCODE_DPAD_DOWN",
		"left": "KEYCODE_DPAD_LEFT", "right": "KEYCODE_DPAD_RIGHT",
		"volumeup": "KEYCODE_VOLUME_UP", "volumedown": "KEYCODE_VOLUME_DOWN",
	}
	if v, ok := m[name]; ok {
		return v
	}
	return name // pass through raw keycode numbers
}
