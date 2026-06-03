package target

// VNCTarget implements the RFB 3.8 protocol (Remote Framebuffer).
// It supports None and VNCAuth security, RAW pixel encoding only,
// PointerEvent for click/move/scroll, and KeyEvent for keyboard input.
// No third-party dependencies required.

import (
	"crypto/des"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"io"
	"net"
	"strings"

	"golang.org/x/image/draw"
)

// rfb message type constants
const (
	rfbSetPixelFormat       = 0
	rfbSetEncodings         = 2
	rfbFBUpdateRequest      = 3
	rfbKeyEvent             = 4
	rfbPointerEvent         = 5
	rfbServerFBUpdate       = 0
	rfbEncodingRaw          = 0
)

// pixelFormat mirrors the 16-byte RFB PixelFormat structure.
type pixelFormat struct {
	BPP        uint8
	Depth      uint8
	BigEndian  uint8
	TrueColour uint8
	RedMax     uint16
	GreenMax   uint16
	BlueMax    uint16
	RedShift   uint8
	GreenShift uint8
	BlueShift  uint8
	_          [3]byte
}

// VNCTarget is a Target backed by a VNC/RFB server over TCP.
type VNCTarget struct {
	conn   net.Conn
	pf     pixelFormat
	w, h   int
	scale  float64
	scaledW, scaledH int
}

// NewVNCTarget connects to a VNC server and performs the RFB handshake.
func NewVNCTarget(cfg Config) (*VNCTarget, error) {
	port := cfg.Port
	if port == 0 {
		port = 5900
	}
	addr := fmt.Sprintf("%s:%d", cfg.Host, port)
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("vnc: dial %s: %w", addr, err)
	}

	t := &VNCTarget{conn: conn, scale: cfg.Scale}

	if err := t.handshake(cfg.Password); err != nil {
		conn.Close()
		return nil, fmt.Errorf("vnc: handshake: %w", err)
	}
	if cfg.Scale > 0 && cfg.Scale != 1.0 {
		t.scaledW = int(float64(t.w) * cfg.Scale)
		t.scaledH = int(float64(t.h) * cfg.Scale)
	} else {
		t.scaledW = t.w
		t.scaledH = t.h
	}
	return t, nil
}

func (t *VNCTarget) handshake(password string) error {
	// 1. Read server protocol version
	ver := make([]byte, 12)
	if _, err := io.ReadFull(t.conn, ver); err != nil {
		return fmt.Errorf("read version: %w", err)
	}
	// Send back RFB 003.008
	if _, err := t.conn.Write([]byte("RFB 003.008\n")); err != nil {
		return fmt.Errorf("send version: %w", err)
	}

	// 2. Security type negotiation (RFB 3.8)
	var nSec uint8
	if err := binary.Read(t.conn, binary.BigEndian, &nSec); err != nil {
		return fmt.Errorf("read num-security-types: %w", err)
	}
	if nSec == 0 {
		// Server sent failure string
		var slen uint32
		binary.Read(t.conn, binary.BigEndian, &slen)
		msg := make([]byte, slen)
		io.ReadFull(t.conn, msg)
		return fmt.Errorf("server refused connection: %s", msg)
	}
	secTypes := make([]byte, nSec)
	if _, err := io.ReadFull(t.conn, secTypes); err != nil {
		return fmt.Errorf("read security types: %w", err)
	}

	// Choose security type: prefer None (1), fall back to VNCAuth (2)
	chosen := byte(0)
	for _, st := range secTypes {
		if st == 1 { // None
			chosen = 1
			break
		}
	}
	if chosen == 0 {
		for _, st := range secTypes {
			if st == 2 { // VNCAuth
				chosen = 2
				break
			}
		}
	}
	if chosen == 0 {
		return fmt.Errorf("no supported security type (server offers %v)", secTypes)
	}
	if _, err := t.conn.Write([]byte{chosen}); err != nil {
		return fmt.Errorf("send security type: %w", err)
	}

	// 3. VNC Auth challenge-response
	if chosen == 2 {
		challenge := make([]byte, 16)
		if _, err := io.ReadFull(t.conn, challenge); err != nil {
			return fmt.Errorf("read auth challenge: %w", err)
		}
		response, err := vncDesEncrypt(password, challenge)
		if err != nil {
			return fmt.Errorf("des encrypt: %w", err)
		}
		if _, err := t.conn.Write(response); err != nil {
			return fmt.Errorf("send auth response: %w", err)
		}
	}

	// 4. Security result
	var result uint32
	if err := binary.Read(t.conn, binary.BigEndian, &result); err != nil {
		return fmt.Errorf("read security result: %w", err)
	}
	if result != 0 {
		var slen uint32
		binary.Read(t.conn, binary.BigEndian, &slen)
		msg := make([]byte, slen)
		io.ReadFull(t.conn, msg)
		return fmt.Errorf("auth failed: %s", msg)
	}

	// 5. ClientInit (shared=1)
	if _, err := t.conn.Write([]byte{1}); err != nil {
		return fmt.Errorf("send client-init: %w", err)
	}

	// 6. ServerInit: width(2) height(2) pixel-format(16) name-len(4) name(N)
	var w, h uint16
	binary.Read(t.conn, binary.BigEndian, &w)
	binary.Read(t.conn, binary.BigEndian, &h)
	if err := binary.Read(t.conn, binary.BigEndian, &t.pf); err != nil {
		return fmt.Errorf("read pixel format: %w", err)
	}
	var nameLen uint32
	binary.Read(t.conn, binary.BigEndian, &nameLen)
	name := make([]byte, nameLen)
	io.ReadFull(t.conn, name)
	t.w = int(w)
	t.h = int(h)

	// 7. SetEncodings: RAW only
	msg := make([]byte, 4+4)
	msg[0] = rfbSetEncodings
	msg[1] = 0 // padding
	binary.BigEndian.PutUint16(msg[2:], 1) // 1 encoding
	binary.BigEndian.PutUint32(msg[4:], rfbEncodingRaw)
	if _, err := t.conn.Write(msg); err != nil {
		return fmt.Errorf("send set-encodings: %w", err)
	}

	return nil
}

// Screenshot requests a full framebuffer update in RAW encoding and decodes
// the result into an image.RGBA, scaling if configured.
func (t *VNCTarget) Screenshot() (image.Image, error) {
	// Request full update (incremental=0)
	req := make([]byte, 10)
	req[0] = rfbFBUpdateRequest
	req[1] = 0 // non-incremental
	binary.BigEndian.PutUint16(req[2:], 0)           // x
	binary.BigEndian.PutUint16(req[4:], 0)           // y
	binary.BigEndian.PutUint16(req[6:], uint16(t.w)) // w
	binary.BigEndian.PutUint16(req[8:], uint16(t.h)) // h
	if _, err := t.conn.Write(req); err != nil {
		return nil, fmt.Errorf("vnc: send FBUpdateRequest: %w", err)
	}

	// Read FramebufferUpdate messages until we have a full frame
	img := image.NewRGBA(image.Rect(0, 0, t.w, t.h))
	for {
		var msgType uint8
		if err := binary.Read(t.conn, binary.BigEndian, &msgType); err != nil {
			return nil, fmt.Errorf("vnc: read msg type: %w", err)
		}
		if msgType != rfbServerFBUpdate {
			// Skip non-update messages (e.g. server cut-text)
			continue
		}
		var padding uint8
		var nRects uint16
		binary.Read(t.conn, binary.BigEndian, &padding)
		binary.Read(t.conn, binary.BigEndian, &nRects)

		for i := uint16(0); i < nRects; i++ {
			var rx, ry, rw, rh uint16
			var enc int32
			binary.Read(t.conn, binary.BigEndian, &rx)
			binary.Read(t.conn, binary.BigEndian, &ry)
			binary.Read(t.conn, binary.BigEndian, &rw)
			binary.Read(t.conn, binary.BigEndian, &rh)
			binary.Read(t.conn, binary.BigEndian, &enc)

			if enc != rfbEncodingRaw {
				return nil, fmt.Errorf("vnc: unexpected encoding %d", enc)
			}

			bpp := int(t.pf.BPP) / 8
			pixels := make([]byte, int(rw)*int(rh)*bpp)
			if _, err := io.ReadFull(t.conn, pixels); err != nil {
				return nil, fmt.Errorf("vnc: read pixel data: %w", err)
			}
			t.blitPixels(img, int(rx), int(ry), int(rw), int(rh), pixels, bpp)
		}
		break // one full FramebufferUpdate received
	}

	if t.scale > 0 && t.scale != 1.0 {
		return scaleImg(img, t.scaledW, t.scaledH), nil
	}
	return img, nil
}

// blitPixels decodes raw VNC pixel data into dst at (ox, oy).
func (t *VNCTarget) blitPixels(dst *image.RGBA, ox, oy, w, h int, data []byte, bpp int) {
	pf := t.pf
	bigEndian := pf.BigEndian != 0
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			idx := (y*w + x) * bpp
			var pixel uint32
			switch bpp {
			case 4:
				if bigEndian {
					pixel = binary.BigEndian.Uint32(data[idx:])
				} else {
					pixel = binary.LittleEndian.Uint32(data[idx:])
				}
			case 3:
				pixel = uint32(data[idx]) | uint32(data[idx+1])<<8 | uint32(data[idx+2])<<16
			case 2:
				if bigEndian {
					pixel = uint32(binary.BigEndian.Uint16(data[idx:]))
				} else {
					pixel = uint32(binary.LittleEndian.Uint16(data[idx:]))
				}
			default:
				pixel = uint32(data[idx])
			}
			r := uint8((pixel >> pf.RedShift) & uint32(pf.RedMax) * 255 / uint32(pf.RedMax))
			g := uint8((pixel >> pf.GreenShift) & uint32(pf.GreenMax) * 255 / uint32(pf.GreenMax))
			b := uint8((pixel >> pf.BlueShift) & uint32(pf.BlueMax) * 255 / uint32(pf.BlueMax))
			dst.SetRGBA(ox+x, oy+y, color.RGBA{R: r, G: g, B: b, A: 255})
		}
	}
}

func (t *VNCTarget) ScreenSize() (int, int) { return t.scaledW, t.scaledH }

// deviceCoord maps a template-space coordinate back to device-space.
func (t *VNCTarget) deviceCoord(x, y int) (int, int) {
	if t.scale > 0 && t.scale != 1.0 {
		return int(float64(x) / t.scale), int(float64(y) / t.scale)
	}
	return x, y
}

func (t *VNCTarget) Click(x, y int, button string) error {
	dx, dy := t.deviceCoord(x, y)
	mask := byte(1) // left
	switch strings.ToLower(button) {
	case "right":
		mask = 4
	case "middle":
		mask = 2
	}
	if err := t.sendPointer(uint8(mask), uint16(dx), uint16(dy)); err != nil {
		return err
	}
	return t.sendPointer(0, uint16(dx), uint16(dy)) // release
}

func (t *VNCTarget) Move(x, y int) error {
	dx, dy := t.deviceCoord(x, y)
	return t.sendPointer(0, uint16(dx), uint16(dy))
}

func (t *VNCTarget) Scroll(x, y, dx, dy int) error {
	devX, devY := t.deviceCoord(x, y)
	btn := byte(4) // wheel-up
	if dy > 0 || dx > 0 {
		btn = 5
	}
	n := abs(dy) + abs(dx)
	if n == 0 {
		n = 1
	}
	for i := 0; i < n; i++ {
		t.sendPointer(btn, uint16(devX), uint16(devY))
		t.sendPointer(0, uint16(devX), uint16(devY))
	}
	return nil
}

func (t *VNCTarget) sendPointer(mask uint8, x, y uint16) error {
	msg := []byte{rfbPointerEvent, mask, 0, 0, 0, 0}
	binary.BigEndian.PutUint16(msg[2:], x)
	binary.BigEndian.PutUint16(msg[4:], y)
	_, err := t.conn.Write(msg)
	return err
}

// Key sends a key event. keysym values follow X11 conventions.
// Common names: "Return", "Escape", "BackSpace", "Tab", "ctrl-c", etc.
func (t *VNCTarget) Type(text string, delayMs int64) error {
	for _, r := range text {
		if err := t.sendKey(uint32(r), true); err != nil {
			return err
		}
		if err := t.sendKey(uint32(r), false); err != nil {
			return err
		}
	}
	return nil
}

func (t *VNCTarget) Key(keys string) error {
	parts := strings.Split(strings.ToLower(keys), "-")
	var syms []uint32
	for _, p := range parts {
		syms = append(syms, rfbKeysym(p))
	}
	for _, s := range syms {
		t.sendKey(s, true)
	}
	for i := len(syms) - 1; i >= 0; i-- {
		t.sendKey(syms[i], false)
	}
	return nil
}

func (t *VNCTarget) sendKey(keysym uint32, down bool) error {
	msg := make([]byte, 8)
	msg[0] = rfbKeyEvent
	if down {
		msg[1] = 1
	}
	binary.BigEndian.PutUint32(msg[4:], keysym)
	_, err := t.conn.Write(msg)
	return err
}

func (t *VNCTarget) Close() error { return t.conn.Close() }

// rfbKeysym maps common key names to X11 keysym values used in RFB.
func rfbKeysym(name string) uint32 {
	table := map[string]uint32{
		"return": 0xff0d, "enter": 0xff0d,
		"escape": 0xff1b, "esc": 0xff1b,
		"backspace": 0xff08,
		"tab": 0xff09,
		"delete": 0xffff, "del": 0xffff,
		"home": 0xff50, "end": 0xff57,
		"pageup": 0xff55, "pagedown": 0xff56,
		"left": 0xff51, "up": 0xff52, "right": 0xff53, "down": 0xff54,
		"f1": 0xffbe, "f2": 0xffbf, "f3": 0xffc0, "f4": 0xffc1,
		"f5": 0xffc2, "f6": 0xffc3, "f7": 0xffc4, "f8": 0xffc5,
		"f9": 0xffc6, "f10": 0xffc7, "f11": 0xffc8, "f12": 0xffc9,
		"ctrl": 0xffe3, "control": 0xffe3,
		"shift": 0xffe1,
		"alt": 0xffe9, "mod1": 0xffe9,
		"super": 0xffeb, "mod4": 0xffeb, "win": 0xffeb,
		"space": 0x0020,
	}
	if v, ok := table[name]; ok {
		return v
	}
	// Single printable character
	if len([]rune(name)) == 1 {
		return uint32([]rune(name)[0])
	}
	return 0
}

// vncDesEncrypt encrypts the 16-byte challenge with a DES key derived from
// password (bit-reversed per the VNC spec).
func vncDesEncrypt(password string, challenge []byte) ([]byte, error) {
	key := make([]byte, 8)
	copy(key, []byte(password))
	for i, b := range key {
		var rb byte
		for bit := 0; bit < 8; bit++ {
			if b&(1<<uint(bit)) != 0 {
				rb |= 1 << uint(7-bit)
			}
		}
		key[i] = rb
	}
	block, err := des.NewCipher(key)
	if err != nil {
		return nil, err
	}
	response := make([]byte, 16)
	block.Encrypt(response[0:8], challenge[0:8])
	block.Encrypt(response[8:16], challenge[8:16])
	return response, nil
}

// scaleImg resizes src to (w, h) using bilinear interpolation.
func scaleImg(src image.Image, w, h int) image.Image {
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.BiLinear.Scale(dst, dst.Bounds(), src, src.Bounds(), draw.Over, nil)
	return dst
}
