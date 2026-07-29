package hotkey

import (
	"strings"
	"sync"
	"time"

	"github.com/jezek/xgbutil"
	"github.com/jezek/xgbutil/keybind"
	"github.com/jezek/xgbutil/xevent"
)

type bindingInfo struct {
	parts    []string
	callback func()
}

type ChordManager struct {
	X           *xgbutil.XUtil
	bindings    []bindingInfo
	lastPressed map[string]time.Time
	mu          sync.Mutex
}

func NewChordManager(X *xgbutil.XUtil) *ChordManager {
	return &ChordManager{
		X:           X,
		lastPressed: make(map[string]time.Time),
	}
}

func (cm *ChordManager) Register(hotkey string, callback func()) {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	hotkey = strings.TrimSpace(hotkey)
	parts := strings.Fields(hotkey)
	if len(parts) == 0 {
		return
	}

	cm.bindings = append(cm.bindings, bindingInfo{
		parts:    parts,
		callback: callback,
	})
}

func (cm *ChordManager) Start() {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	uniqueKeys := make(map[string]bool)
	for _, b := range cm.bindings {
		for _, part := range b.parts {
			uniqueKeys[part] = true
		}
	}

	for key := range uniqueKeys {
		k := key
		keybind.KeyPressFun(func(xu *xgbutil.XUtil, ev xevent.KeyPressEvent) {
			cm.handleKey(k)
		}).Connect(cm.X, cm.X.RootWin(), k, true)
	}
}

func (cm *ChordManager) handleKey(key string) {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	now := time.Now()
	prevTime := cm.lastPressed[key]
	triggered := false

	for _, b := range cm.bindings {
		if len(b.parts) == 1 {
			if b.parts[0] == key {
				go b.callback()
				triggered = true
			}
		} else if len(b.parts) == 2 {
			prefixKey := b.parts[0]
			triggerKey := b.parts[1]

			if prefixKey == triggerKey && key == prefixKey {
				if !prevTime.IsZero() && now.Sub(prevTime) < 800*time.Millisecond {
					go b.callback()
					triggered = true
					cm.lastPressed[key] = time.Time{}
					continue
				}
			} else {
				if key == triggerKey {
					prefixTime := cm.lastPressed[prefixKey]
					if !prefixTime.IsZero() && now.Sub(prefixTime) < 800*time.Millisecond {
						go b.callback()
						triggered = true
						cm.lastPressed[prefixKey] = time.Time{}
					}
				}
			}
		}
	}

	if !triggered || cm.lastPressed[key] != (time.Time{}) {
		cm.lastPressed[key] = now
	}
}
