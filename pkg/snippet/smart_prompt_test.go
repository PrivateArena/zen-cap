package snippet

import (
	"strings"
	"testing"

	"zen-cap/pkg/prompt"
)

func TestSmartPromptQueryResolution(t *testing.T) {
	testPrompts := []prompt.PromptDef{
		{
			Name: "Viral Content Creator",
			Role: "a viral content creator who has generated 100M+ impressions across Twitter/X, TikTok, and LinkedIn",
			Job:  "analyze the provided topic or draft and rewrite it into highly engaging, hook-driven content optimized for virality and high shareability. Maintain a clean, punchy format.",
			Tags: []string{"viral", "marketing", "social media", "twitter", "linkedin", "tiktok", "copywriting"},
		},
		{
			Name: "Ruthless Editor",
			Role: "a ruthless, world-class editor with a focus on simplicity, readability, and high-impact communication",
			Job:  "edit the provided text to cut filler words, remove passive voice, simplify complex sentences, and enhance clarity while preserving the author's original voice.",
			Tags: []string{"editor", "writing", "editing", "readability", "clear"},
		},
		{
			Name: "SEO Specialist",
			Role: "an SEO Strategist and technical content optimizer who consistently ranks articles in Google's top 3 positions",
			Job:  "optimize the provided text or outline for the target keywords. Improve heading hierarchy, metadata, search intent alignment, and keyword density naturally.",
			Tags: []string{"seo", "marketing", "keywords", "search", "google"},
		},
	}

	s := &SmartState{
		kind:          SmartTypePrompt,
		promptAll:     testPrompts,
		promptMatches: testPrompts,
	}

	// 1. Resolve empty query (should return all prompts)
	s.resolvePromptQuery()
	if len(s.promptMatches) == 0 {
		t.Fatalf("expected prompts for empty query, got 0 matches")
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

	// 4. Content formatting with ResolveContent
	formatted := s.Content("", "")
	if !strings.Contains(formatted, "SEO Strategist") {
		t.Errorf("expected formatted prompt to contain 'SEO Strategist', got: %s", formatted)
	}
	if !strings.Contains(formatted, "optimize the provided text") {
		t.Errorf("expected formatted prompt to contain job details, got: %s", formatted)
	}
}
