package prompt

// ResolveContent returns the final pasted text for a PromptDef.
// If def.Template != "": uses template as-is, appends skill blocks.
// Else: builds "You are a {role}.\nYour JOB is to {job}." + skill blocks.
func ResolveContent(def PromptDef, skillsPath string) string {
	var text string
	if def.Template != "" {
		text = def.Template
	} else {
		text = "You are a " + def.Role + ".\nYour JOB is to " + def.Job + "."
	}

	// Append enabled skill blocks
	for _, skillID := range def.EnabledSkills {
		content, err := LoadSkillContent(skillsPath, skillID)
		if err != nil || content == "" {
			continue
		}
		text += "\n\n" + formatSkillBlock(skillID, content)
	}

	return text
}

// tokenCount returns a rough estimate of the token count for a text string.
// Uses ~4 characters per token as a simple heuristic.
func tokenCount(text string) int {
	return len([]rune(text)) / 4
}
