package prompt

import (
	"fmt"
	"os"
	"path/filepath"
)

// LoadSkillContent returns the raw markdown for a skill ID.
// Checks: skillsPath/<id>.md, then skillsPath/<id>/SKILL.md.
// Returns ("", nil) if not found.
func LoadSkillContent(skillsPath, id string) (string, error) {
	// Try <id>.md
	path := filepath.Join(skillsPath, id+".md")
	if data, err := os.ReadFile(path); err == nil {
		return string(data), nil
	}

	// Try <id>/SKILL.md
	path = filepath.Join(skillsPath, id, "SKILL.md")
	if data, err := os.ReadFile(path); err == nil {
		return string(data), nil
	}

	return "", nil
}

// formatSkillBlock wraps raw markdown content in the standard skill block
// format used by the prompt system.
func formatSkillBlock(id, content string) string {
	return fmt.Sprintf("---\n<!-- SKILL: %s -->\n%s", id, content)
}
