package prompt

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// LoadPrompts reads all *.yaml / *.yml from promptsPath.
// Returns deduplicated slice keyed by Name (last write wins).
func LoadPrompts(promptsPath string) ([]PromptDef, error) {
	entries, err := os.ReadDir(promptsPath)
	if err != nil {
		return nil, fmt.Errorf("reading prompts dir %s: %w", promptsPath, err)
	}

	var all []PromptDef
	seen := make(map[string]int) // name → index in all; last write wins

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		ext := filepath.Ext(entry.Name())
		if ext != ".yaml" && ext != ".yml" {
			continue
		}

		defs, err := loadYAML(filepath.Join(promptsPath, entry.Name()))
		if err != nil {
			continue // skip malformed files silently
		}
		for _, d := range defs {
			if idx, ok := seen[d.Name]; ok {
				all[idx] = d
			} else {
				seen[d.Name] = len(all)
				all = append(all, d)
			}
		}
	}

	return all, nil
}

// loadYAML parses a single YAML file; accepts array or single object.
func loadYAML(path string) ([]PromptDef, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	// Try array first
	var arr []PromptDef
	if err := yaml.Unmarshal(data, &arr); err == nil {
		return arr, nil
	}

	// Fall back to single object
	var single PromptDef
	if err := yaml.Unmarshal(data, &single); err != nil {
		return nil, err
	}
	return []PromptDef{single}, nil
}
