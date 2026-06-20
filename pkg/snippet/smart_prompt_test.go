package snippet

import (
	"strings"
	"testing"
)

func TestSmartPromptQueryResolution(t *testing.T) {
	s := &SmartState{
		kind: SmartTypePrompt,
	}

	// 1. Resolve empty query (should return popular templates)
	s.resolvePromptQuery()
	if len(s.promptMatches) == 0 {
		t.Fatalf("expected popular prompts for empty query, got 0 matches")
	}
	if s.promptMatches[0].Name != "Viral Content Creator" {
		t.Errorf("expected first match to be Viral Content Creator, got: %s", s.promptMatches[0].Name)
	}

	// 2. Query search by name prefix/substring
	s.query = "ruthless"
	s.resolvePromptQuery()
	if len(s.promptMatches) != 1 {
		t.Fatalf("expected 1 match for 'ruthless', got %d", len(s.promptMatches))
	}
	if s.promptMatches[0].Name != "Ruthless Editor" {
		t.Errorf("expected Ruthless Editor, got: %s", s.promptMatches[0].Name)
	}

	// 3. Query search by tag
	s.query = "seo"
	s.resolvePromptQuery()
	if len(s.promptMatches) != 1 {
		t.Fatalf("expected 1 match for tag 'seo', got %d", len(s.promptMatches))
	}
	if s.promptMatches[0].Name != "SEO Specialist" {
		t.Errorf("expected SEO Specialist, got: %s", s.promptMatches[0].Name)
	}

	// 4. Content formatting replacement
	formatted := s.Content("")
	if !strings.Contains(formatted, "SEO Strategist") {
		t.Errorf("expected formatted prompt to contain 'SEO Strategist', got: %s", formatted)
	}
	if !strings.Contains(formatted, "optimize the provided text") {
		t.Errorf("expected formatted prompt to contain job details, got: %s", formatted)
	}

	// 5. Custom formatting
	customFormat := "Role: {role}\nJob: {job}"
	customFormatted := s.Content(customFormat)
	expectedPrefix := "Role: an SEO Strategist"
	if !strings.HasPrefix(customFormatted, expectedPrefix) {
		t.Errorf("expected prefix %q, got: %s", expectedPrefix, customFormatted)
	}
}
