package clipboard

import (
	"fmt"
	"regexp"

	md "github.com/JohannesKaufmann/html-to-markdown"
	"zen-cap/pkg/config"
)

// ApplyTransform applies a single transform rule to text content.
func ApplyTransform(text string, rule config.TransformRule) string {
	switch rule.Type {
	case "passthrough":
		return text

	case "html2md":
		converter := md.NewConverter("", true, nil)
		markdown, err := converter.ConvertString(text)
		if err != nil {
			fmt.Printf("[ClipboardManager] HTML to Markdown conversion failed: %v\n", err)
			return text
		}
		return markdown

	case "regex":
		if rule.Pattern == "" {
			return text
		}
		re, err := regexp.Compile(rule.Pattern)
		if err != nil {
			fmt.Printf("[ClipboardManager] Invalid regex pattern %q: %v\n", rule.Pattern, err)
			return text
		}
		return re.ReplaceAllString(text, rule.Replacement)

	default:
		fmt.Printf("[ClipboardManager] Unknown transform type: %s\n", rule.Type)
		return text
	}
}
