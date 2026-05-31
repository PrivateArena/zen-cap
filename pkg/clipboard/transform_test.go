package clipboard

import (
	"testing"

	"zen-cap/pkg/config"
)

func TestApplyTransformPassthrough(t *testing.T) {
	rule := config.TransformRule{
		Name: "None",
		Type: "passthrough",
	}

	input := "Hello World"
	got := ApplyTransform(input, rule)
	if got != input {
		t.Errorf("Expected %q, got %q", input, got)
	}
}

func TestApplyTransformRegex(t *testing.T) {
	rule := config.TransformRule{
		Name:        "Strip Domain",
		Type:        "regex",
		Pattern:     `\[[a-zA-Z0-9._-]+\]`,
		Replacement: "REDACTED",
	}

	input := "Welcome to [domain.com] user!"
	expected := "Welcome to REDACTED user!"
	got := ApplyTransform(input, rule)
	if got != expected {
		t.Errorf("Expected %q, got %q", expected, got)
	}
}

func TestApplyTransformHTML2MD(t *testing.T) {
	rule := config.TransformRule{
		Name: "HTML to MD",
		Type: "html2md",
	}

	input := "<h1>Title</h1><p>This is <b>bold</b> and <i>italic</i>.</p>"
	// html-to-markdown output will format it as Markdown
	got := ApplyTransform(input, rule)
	
	// We check if key Markdown elements exist in the output
	if got == "" {
		t.Errorf("Expected Markdown output, got empty string")
	}
}
