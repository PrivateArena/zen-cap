package snippet

import (
	"fmt"
	"strings"
	"time"
)

// smartType enumerates all supported smart snippet kinds.
type smartType string

var SnippetFilePath string

const (
	SmartTypeTime  smartType = "time"
	SmartTypeIP    smartType = "ip"
	SmartTypeEmoji smartType = "emoji"
	SmartTypePrompt smartType = "prompt"
)

type EmojiInfo struct {
	Char string
	Name string
	Tags []string
}

// SmartState holds the runtime state for a smart snippet while it is selected
// in the picker. It lives inside pickerState, not in the Snippet itself, so
// that Snippet can stay a plain serialisable struct.
type SmartState struct {
	// kind mirrors the originating Snippet.Smart field.
	kind smartType

	// --- For SmartTypeTime ---
	// Location cycle list (IANA names), len >= 1 (index 0 = local "system").
	locations []string
	locLabels []string // human-readable label for each location
	locIdx    int      // currently active index

	// Freeform query the user typed while this item is selected.
	// When non-empty, we try to resolve it to a timezone on the fly.
	query string

	// Resolved location (non-nil). Follows locIdx unless query overrides it.
	resolved *time.Location

	// --- For SmartTypeIP ---
	ipAddress string
	ipErr     error
	ipLoading bool
	ipFetched bool

	// --- For SmartTypeEmoji ---
	emojiMatches []EmojiInfo
	emojiIdx     int

	// --- For SmartTypePrompt ---
	promptMatches []PromptRole
	promptIdx     int
}

// Content returns the current resolved snippet text for pasting.
func (s *SmartState) Content(format string) string {
	if s.kind == SmartTypeTime {
		if format == "" {
			format = "{time}"
		}
		now := time.Now().In(s.resolved)

		// Resolve {place}
		place := s.LocationLabel()
		res := strings.ReplaceAll(format, "{place}", place)

		// Resolve {iana}
		res = strings.ReplaceAll(res, "{iana}", s.resolved.String())

		// Resolve {time:LAYOUT}
		for {
			start := strings.Index(res, "{time:")
			if start == -1 {
				break
			}
			end := strings.Index(res[start:], "}")
			if end == -1 {
				break
			}
			end = start + end
			layout := res[start+6 : end]
			formatted := now.Format(layout)
			res = res[:start] + formatted + res[end+1:]
		}

		// Resolve default {time}
		defaultTime := now.Format("2006-01-02 15:04:05 MST")
		res = strings.ReplaceAll(res, "{time}", defaultTime)

		return res
	} else if s.kind == SmartTypeIP {
		if format == "" {
			format = "{ip}"
		}
		ipVal := s.ipAddress
		if s.ipLoading {
			ipVal = "Fetching IP..."
		} else if s.ipErr != nil {
			ipVal = fmt.Sprintf("Error: %v", s.ipErr)
		}
		return strings.ReplaceAll(format, "{ip}", ipVal)
	} else if s.kind == SmartTypeEmoji {
		if format == "" {
			format = "{emoji}"
		}
		emojiVal := ""
		nameVal := ""
		if len(s.emojiMatches) > 0 && s.emojiIdx >= 0 && s.emojiIdx < len(s.emojiMatches) {
			emojiVal = s.emojiMatches[s.emojiIdx].Char
			nameVal = s.emojiMatches[s.emojiIdx].Name
		} else {
			emojiVal = "❓"
			nameVal = "No match"
		}
		res := strings.ReplaceAll(format, "{emoji}", emojiVal)
		res = strings.ReplaceAll(res, "{name}", nameVal)
		return res
	} else if s.kind == SmartTypePrompt {
		if format == "" {
			format = "You are a {role}.\nYour JOB is to {job}."
		}
		roleVal := ""
		jobVal := ""
		if len(s.promptMatches) > 0 && s.promptIdx >= 0 && s.promptIdx < len(s.promptMatches) {
			roleVal = s.promptMatches[s.promptIdx].Role
			jobVal = s.promptMatches[s.promptIdx].Job
		} else {
			roleVal = "AI assistant"
			jobVal = "help the user with their request"
		}
		res := strings.ReplaceAll(format, "{role}", roleVal)
		res = strings.ReplaceAll(res, "{job}", jobVal)
		return res
	}
	return ""
}

// CycleNext advances to the next preset / prediction.
func (s *SmartState) CycleNext() {
	if s.kind == SmartTypeTime {
		s.query = ""
		s.locIdx = (s.locIdx + 1) % len(s.locations)
		s.resolved = s.loadPreset(s.locIdx)
	} else if s.kind == SmartTypeEmoji {
		if len(s.emojiMatches) > 0 {
			s.emojiIdx = (s.emojiIdx + 1) % len(s.emojiMatches)
		}
	} else if s.kind == SmartTypePrompt {
		if len(s.promptMatches) > 0 {
			s.promptIdx = (s.promptIdx + 1) % len(s.promptMatches)
		}
	}
}

// CyclePrev goes back to the previous preset / prediction.
func (s *SmartState) CyclePrev() {
	if s.kind == SmartTypeTime {
		s.query = ""
		s.locIdx = (s.locIdx - 1 + len(s.locations)) % len(s.locations)
		s.resolved = s.loadPreset(s.locIdx)
	} else if s.kind == SmartTypeEmoji {
		if len(s.emojiMatches) > 0 {
			s.emojiIdx = (s.emojiIdx - 1 + len(s.emojiMatches)) % len(s.emojiMatches)
		}
	} else if s.kind == SmartTypePrompt {
		if len(s.promptMatches) > 0 {
			s.promptIdx = (s.promptIdx - 1 + len(s.promptMatches)) % len(s.promptMatches)
		}
	}
}

// AppendQuery appends a rune to the freeform query and attempts resolution.
func (s *SmartState) AppendQuery(r rune) {
	s.query += string(r)
	if s.kind == SmartTypeTime {
		s.tryResolveQuery()
	} else if s.kind == SmartTypeEmoji {
		s.resolveEmojiQuery()
	} else if s.kind == SmartTypePrompt {
		s.resolvePromptQuery()
	}
}

// BackspaceQuery removes the last rune from the query.
func (s *SmartState) BackspaceQuery() {
	runes := []rune(s.query)
	if len(runes) == 0 {
		return
	}
	s.query = string(runes[:len(runes)-1])
	if s.kind == SmartTypeTime {
		if s.query == "" {
			// Reset to current preset
			s.resolved = s.loadPreset(s.locIdx)
		} else {
			s.tryResolveQuery()
		}
	} else if s.kind == SmartTypeEmoji {
		s.resolveEmojiQuery()
	} else if s.kind == SmartTypePrompt {
		s.resolvePromptQuery()
	}
}

// ClearQuery resets the query.
func (s *SmartState) ClearQuery() {
	s.query = ""
	if s.kind == SmartTypeTime {
		s.resolved = s.loadPreset(s.locIdx)
	} else if s.kind == SmartTypeEmoji {
		s.resolveEmojiQuery()
	} else if s.kind == SmartTypePrompt {
		s.resolvePromptQuery()
	}
}
