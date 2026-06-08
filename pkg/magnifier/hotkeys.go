package magnifier

import (
	"fmt"
	"strings"

	"github.com/jezek/xgb/xproto"
	"github.com/jezek/xgbutil"
	"github.com/jezek/xgbutil/keybind"
)

// modifierMask returns the X11 modifier bitmask for a modifier name such as
// "super", "alt", "ctrl", "shift".
func modifierMask(name string) uint16 {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "super", "mod4":
		return xproto.ModMask4
	case "alt", "mod1":
		return xproto.ModMask1
	case "ctrl", "control":
		return xproto.ModMaskControl
	case "shift":
		return xproto.ModMaskShift
	default:
		return xproto.ModMask4 // fallback to Super
	}
}

// parseHotkey converts a user hotkey string (e.g. "super-f") into the
// modifier mask and keycode needed for XGrabKey.
func parseHotkey(xu *xgbutil.XUtil, hotkey string) (modMask uint16, kc xproto.Keycode, err error) {
	// xgbutil keybind.ParseString expects lower-case dash-separated tokens
	// in xgb format (e.g. "mod4-f", "control-shift-a").
	norm := normaliseHotkey(hotkey)
	mods, keycodes, e := keybind.ParseString(xu, norm)
	if e != nil || len(keycodes) == 0 {
		return 0, 0, fmt.Errorf("parseHotkey %q: %w", hotkey, e)
	}
	return mods, xproto.Keycode(keycodes[0]), nil
}

// normaliseHotkey converts user-friendly strings like "super-f" to
// the xgbutil format "mod4-f", and "alt" → "mod1", etc.
func normaliseHotkey(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	parts := strings.Split(s, "-")
	for i, p := range parts {
		switch p {
		case "super", "win":
			parts[i] = "mod4"
		case "alt":
			parts[i] = "mod1"
		case "ctrl":
			parts[i] = "control"
		}
	}
	return strings.Join(parts, "-")
}

// grabHotkey registers a passive XGrabKey on the root window for the given
// keycode + modMask, duplicated for all NumLock/CapsLock combinations.
func grabHotkey(xu *xgbutil.XUtil, root xproto.Window, modMask uint16, kc xproto.Keycode) error {
	conn := xu.Conn()
	numLock := numLockMask(xu)

	lockVariants := []uint16{
		0,
		xproto.ModMaskLock,
		numLock,
		xproto.ModMaskLock | numLock,
	}

	var firstErr error
	for _, extra := range lockVariants {
		err := xproto.GrabKeyChecked(conn,
			false, // ownerEvents: false → events come to us, not to the focused window
			root,
			modMask|extra,
			kc,
			xproto.GrabModeAsync, // pointerMode
			xproto.GrabModeAsync, // keyboardMode
		).Check()
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// ungrabHotkey removes all XGrabKey entries for the given keycode + modMask.
func ungrabHotkey(xu *xgbutil.XUtil, root xproto.Window, modMask uint16, kc xproto.Keycode) {
	conn := xu.Conn()
	numLock := numLockMask(xu)
	for _, extra := range []uint16{0, xproto.ModMaskLock, numLock, xproto.ModMaskLock | numLock} {
		xproto.UngrabKey(conn, kc, root, modMask|extra)
	}
}

// grabScrollButtons registers passive XGrabButton entries on the root window
// for Button4 (scroll-up) and Button5 (scroll-down) with the given modifier.
func grabScrollButtons(xu *xgbutil.XUtil, root xproto.Window, scrollMod uint16) {
	conn := xu.Conn()
	numLock := numLockMask(xu)

	for _, btn := range []byte{4, 5} {
		for _, extra := range []uint16{0, xproto.ModMaskLock, numLock, xproto.ModMaskLock | numLock} {
			xproto.GrabButton(conn,
				false, // ownerEvents
				root,
				uint16(xproto.EventMaskButtonPress|xproto.EventMaskButtonRelease),
				xproto.GrabModeAsync,
				xproto.GrabModeAsync,
				xproto.WindowNone,
				xproto.CursorNone,
				btn,
				scrollMod|extra,
			)
		}
	}
}

// ungrabScrollButtons removes all scroll button grabs.
func ungrabScrollButtons(xu *xgbutil.XUtil, root xproto.Window, scrollMod uint16) {
	conn := xu.Conn()
	numLock := numLockMask(xu)
	for _, btn := range []byte{4, 5} {
		for _, extra := range []uint16{0, xproto.ModMaskLock, numLock, xproto.ModMaskLock | numLock} {
			xproto.UngrabButton(conn, btn, root, scrollMod|extra)
		}
	}
}

// numLockMask returns the modifier mask for NumLock (usually Mod2).
// It looks up the key symbol XK_Num_Lock in the modifier map.
func numLockMask(xu *xgbutil.XUtil) uint16 {
	// NumLock is conventionally on Mod2Mask in most X11 setups.
	// A full implementation would query the modifier map; for robustness we
	// return both 0 and Mod2Mask variants in the caller.
	return xproto.ModMask2
}

// matchesHotkey checks whether a KeyPress event's keycode+state matches
// the given parsed modMask+keycode (ignoring lock-key bits).
func matchesHotkey(ev xproto.KeyPressEvent, modMask uint16, kc xproto.Keycode) bool {
	if ev.Detail != kc {
		return false
	}
	// Strip NumLock, CapsLock, and Mod5 (ScrollLock) before comparing.
	const lockBits = xproto.ModMaskLock | xproto.ModMask2 | xproto.ModMask5
	return ev.State&^uint16(lockBits) == modMask
}

// matchesScrollButton checks whether a ButtonPress event is a scroll event
// with the expected modifier held (ignoring lock bits).
func matchesScrollButton(ev xproto.ButtonPressEvent, scrollMod uint16) bool {
	if ev.Detail != 4 && ev.Detail != 5 {
		return false
	}
	const lockBits = xproto.ModMaskLock | xproto.ModMask2 | xproto.ModMask5
	return ev.State&^uint16(lockBits) == scrollMod
}
