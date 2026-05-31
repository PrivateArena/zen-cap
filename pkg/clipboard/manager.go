package clipboard

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"zen-cap/pkg/capture"
	"zen-cap/pkg/config"
)

type ClipboardSlot struct {
	Index     int       `json:"index"`
	Format    string    `json:"format"`    // "text", "image", "empty"
	Content   []byte    `json:"content"`   // raw text or PNG bytes
	Preview   string    `json:"preview"`   // preview string
	SavedAt   time.Time `json:"saved_at"`
}

type ClipboardSession struct {
	Slots           [10]*ClipboardSlot `json:"slots"`
	ActiveTransform int                `json:"active_transform"`
}

type Manager struct {
	session              ClipboardSession
	sessionPath          string
	rules                []config.TransformRule
	lastPrimarySelection string
	mu                   sync.RWMutex
}

// NewManager creates a new Manager instance and loads or initializes the session.
func NewManager(cfg *config.Config) (*Manager, error) {
	mgr := &Manager{
		sessionPath: cfg.ClipboardSessionFile,
		rules:       cfg.TransformRules,
	}

	// Initialize empty slots
	for i := 0; i < 10; i++ {
		mgr.session.Slots[i] = &ClipboardSlot{
			Index:   i,
			Format:  "empty",
			Content: nil,
			Preview: "Empty slot",
			SavedAt: time.Time{},
		}
	}

	if err := mgr.loadSession(); err != nil {
		// Log error and start with clean session
		fmt.Fprintf(os.Stderr, "[ClipboardManager] Failed to load session: %v. Starting fresh.\n", err)
	}

	return mgr, nil
}

func (m *Manager) loadSession() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, err := os.Stat(m.sessionPath); os.IsNotExist(err) {
		return nil
	}

	data, err := os.ReadFile(m.sessionPath)
	if err != nil {
		return err
	}

	var loaded ClipboardSession
	if err := json.Unmarshal(data, &loaded); err != nil {
		return err
	}

	m.session.ActiveTransform = loaded.ActiveTransform
	for i := 0; i < 10; i++ {
		if loaded.Slots[i] != nil {
			m.session.Slots[i] = loaded.Slots[i]
		}
	}

	return nil
}

func (m *Manager) saveSession() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	dir := filepath.Dir(m.sessionPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	data, err := json.MarshalIndent(m.session, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(m.sessionPath, data, 0644)
}

// CopyToSlot reads from system clipboard and stores in slot N.
func (m *Manager) CopyToSlot(n int) {
	if n < 0 || n > 9 {
		return
	}

	// 1. Try reading the active highlighted text (PRIMARY selection) first
	primaryText, errPrimary := capture.ReadTextFromSelection("primary")

	m.mu.Lock()
	lastPrimary := m.lastPrimarySelection
	m.mu.Unlock()

	// If there is a fresh highlight, prioritize it
	if errPrimary == nil && len(primaryText) > 0 && primaryText != lastPrimary {
		m.mu.Lock()
		m.lastPrimarySelection = primaryText
		preview := primaryText
		if len(preview) > 60 {
			preview = preview[:57] + "..."
		}
		m.session.Slots[n] = &ClipboardSlot{
			Index:   n,
			Format:  "text",
			Content: []byte(primaryText),
			Preview: preview,
			SavedAt: time.Now(),
		}
		m.mu.Unlock()
		_ = m.saveSession()

		msg := fmt.Sprintf("Saved text (highlighted) to slot %d:\n%s", n, preview)
		sendNotification(fmt.Sprintf("Slot %d Saved", n), msg)
		fmt.Printf("[ClipboardManager] %s\n", msg)
		return
	}

	// 2. Fallback to standard CLIPBOARD selection (text first, then image)
	clipText, errText := capture.ReadTextFromSelection("clipboard")
	if errText == nil && len(clipText) > 0 {
		m.mu.Lock()
		m.lastPrimarySelection = clipText
		preview := clipText
		if len(preview) > 60 {
			preview = preview[:57] + "..."
		}
		m.session.Slots[n] = &ClipboardSlot{
			Index:   n,
			Format:  "text",
			Content: []byte(clipText),
			Preview: preview,
			SavedAt: time.Now(),
		}
		m.mu.Unlock()
		_ = m.saveSession()

		msg := fmt.Sprintf("Saved text (clipboard) to slot %d:\n%s", n, preview)
		sendNotification(fmt.Sprintf("Slot %d Saved", n), msg)
		fmt.Printf("[ClipboardManager] %s\n", msg)
		return
	}

	// Try image fallback
	imgData, errImg := capture.ReadImageFromSelection()
	if errImg == nil && len(imgData) > 0 {
		m.mu.Lock()
		m.session.Slots[n] = &ClipboardSlot{
			Index:   n,
			Format:  "image",
			Content: imgData,
			Preview: fmt.Sprintf("[PNG Image: %d bytes]", len(imgData)),
			SavedAt: time.Now(),
		}
		m.mu.Unlock()
		_ = m.saveSession()

		msg := fmt.Sprintf("Saved image to slot %d (%d bytes)", n, len(imgData))
		sendNotification(fmt.Sprintf("Slot %d Saved", n), msg)
		fmt.Printf("[ClipboardManager] %s\n", msg)
		return
	}

	// If everything failed
	sendNotification("Clipboard Copy Failed", "System clipboard and selections are empty.")
	fmt.Printf("[ClipboardManager] Copy to slot %d failed: primary err=%v, text err=%v, image err=%v\n", n, errPrimary, errText, errImg)
}

// PasteFromSlot sets the system clipboard with slot N's content and performs auto-paste.
func (m *Manager) PasteFromSlot(n int) {
	if n < 0 || n > 9 {
		return
	}

	m.mu.RLock()
	slot := m.session.Slots[n]
	activeRuleIdx := m.session.ActiveTransform
	m.mu.RUnlock()

	if slot == nil || slot.Format == "empty" {
		sendNotification("Paste Failed", fmt.Sprintf("Slot %d is empty.", n))
		return
	}

	if slot.Format == "text" {
		text := string(slot.Content)
		ruleName := "None"
		if activeRuleIdx >= 0 && activeRuleIdx < len(m.rules) {
			rule := m.rules[activeRuleIdx]
			ruleName = rule.Name
			text = ApplyTransform(text, rule)
		}

		if err := capture.CopyTextToClipboard(text); err != nil {
			fmt.Printf("[ClipboardManager] Copy text to system clipboard failed: %v\n", err)
			return
		}
		fmt.Printf("[ClipboardManager] Restored slot %d (transform: %s) to system clipboard.\n", n, ruleName)
	} else if slot.Format == "image" {
		if err := capture.CopyImageToClipboard(slot.Content); err != nil {
			fmt.Printf("[ClipboardManager] Copy image to system clipboard failed: %v\n", err)
			return
		}
		fmt.Printf("[ClipboardManager] Restored image from slot %d to system clipboard.\n", n)
	}

	// Perform auto-paste
	go func() {
		// Small delay to allow the OS to register the new clipboard content
		time.Sleep(100 * time.Millisecond)
		cmd := exec.Command("xdotool", "key", "--clearmodifiers", "ctrl+v")
		if err := cmd.Run(); err != nil {
			fmt.Printf("[ClipboardManager] Auto-paste via xdotool failed: %v\n", err)
		}
	}()
}

// CycleTransform cycles to the next transform rule.
func (m *Manager) CycleTransform() {
	if len(m.rules) == 0 {
		return
	}

	m.mu.Lock()
	m.session.ActiveTransform = (m.session.ActiveTransform + 1) % len(m.rules)
	rule := m.rules[m.session.ActiveTransform]
	m.mu.Unlock()

	_ = m.saveSession()

	msg := fmt.Sprintf("Active transform set to: %s", rule.Name)
	sendNotification("Transform Cycled", msg)
	fmt.Printf("[ClipboardManager] %s\n", msg)
}

func sendNotification(title, message string) {
	_ = exec.Command("notify-send", "-a", "Zen-Cap", title, message).Run()
}
