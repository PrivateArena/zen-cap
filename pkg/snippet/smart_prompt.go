package snippet

import (
	"strings"

	"zen-cap/pkg/prompt"
)

// loadPromptsDynamic loads prompts from the configured prompts directory.
// No built-in fallback — file is authoritative; returns nil on error/empty.
func loadPromptsDynamic(promptsPath string) []prompt.PromptDef {
	defs, err := prompt.LoadPrompts(promptsPath)
	if err != nil || len(defs) == 0 {
		return nil
	}
	return defs
}

func (s *SmartState) resolvePromptQuery() {
	q := strings.ToLower(strings.TrimSpace(s.query))
	if q == "" {
		s.promptIdx = 0
		return
	}

	var matches []prompt.PromptDef
	// 1. Exact/prefix match on name
	for _, p := range s.promptAll {
		nameLower := strings.ToLower(p.Name)
		if strings.HasPrefix(nameLower, q) || strings.ReplaceAll(nameLower, " ", "") == q {
			matches = append(matches, p)
		}
	}

	// Track matches we have already found
	seen := make(map[string]bool)
	for _, m := range matches {
		seen[m.Name] = true
	}

	// 2. Substring match on name, role, description, tags
	for _, p := range s.promptAll {
		if seen[p.Name] {
			continue
		}
		nameLower := strings.ToLower(p.Name)
		roleLower := strings.ToLower(p.Role)
		descLower := strings.ToLower(p.Description)
		templateLower := strings.ToLower(p.Template)
		if strings.Contains(nameLower, q) || strings.Contains(roleLower, q) || strings.Contains(descLower, q) || strings.Contains(templateLower, q) {
			matches = append(matches, p)
			seen[p.Name] = true
			continue
		}
		for _, tag := range p.Tags {
			if strings.HasPrefix(strings.ToLower(tag), q) || strings.ToLower(tag) == q {
				matches = append(matches, p)
				seen[p.Name] = true
				break
			}
		}
	}

	s.promptMatches = matches
	s.promptIdx = 0
}
