package macro

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"

	"zen-cap/pkg/automation"
	"zen-cap/pkg/capture"
	"zen-cap/pkg/config"
)

// Task names recognized in task-profile Tasks lists for macro output. A task
// profile whose AppliesTo contains AppliesToMacro drives the macro output
// chain; cycling the current task profile cycles the output type.
const (
	TaskSaveScript = "save_script"
	TaskCopyScript = "copy_script"
	// AppliesToMacro is the AppliesTo marker for task profiles that drive
	// macro output instead of a post-capture pipeline.
	AppliesToMacro = "macro"
)

// ScriptYAML serializes a script to the same YAML format used by the
// automation manager, so saved macros are directly editable and loadable.
func ScriptYAML(script automation.Script) ([]byte, error) {
	return yaml.Marshal(script)
}

// SaveScript writes script as <name>.yaml inside dir (creating it if needed)
// and returns the absolute path written.
func SaveScript(script automation.Script, dir string) (string, error) {
	data, err := ScriptYAML(script)
	if err != nil {
		return "", fmt.Errorf("macro: marshal script: %w", err)
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", fmt.Errorf("macro: create output dir: %w", err)
	}
	path := filepath.Join(dir, sanitizeName(script.Name)+".yaml")
	if err := os.WriteFile(path, data, 0644); err != nil {
		return "", fmt.Errorf("macro: write script: %w", err)
	}
	return path, nil
}

// CopyScript copies the YAML rendering of script to the system clipboard.
func CopyScript(script automation.Script) error {
	data, err := ScriptYAML(script)
	if err != nil {
		return fmt.Errorf("macro: marshal script: %w", err)
	}
	if err := capture.CopyTextToClipboard(string(data)); err != nil {
		return fmt.Errorf("macro: copy to clipboard: %w", err)
	}
	return nil
}

// OutputChain returns the ordered macro output task names honored for cfg.
// If the current task profile applies to AppliesToMacro, its Tasks are used;
// otherwise the default chain (save_script) applies, which keeps macro output
// stable for configurations that never opt into macro profiles.
func OutputChain(cfg *config.Config) []string {
	if cfg != nil {
		for _, p := range cfg.TaskProfiles {
			if p.Name != cfg.CurrentTaskProfile {
				continue
			}
			for _, a := range p.AppliesTo {
				if a == AppliesToMacro && len(p.Tasks) > 0 {
					return append([]string(nil), p.Tasks...)
				}
			}
			break
		}
	}
	return []string{TaskSaveScript}
}

// Render executes each output task in chain against script, returning a
// human-readable result per task. An unrecognized task aborts the chain loudly
// so a misconfigured profile cannot silently drop output.
func Render(chain []string, script automation.Script, dir string) ([]string, error) {
	var results []string
	for _, task := range chain {
		switch task {
		case TaskSaveScript:
			path, err := SaveScript(script, dir)
			if err != nil {
				return results, err
			}
			results = append(results, "saved to "+path)
		case TaskCopyScript:
			if err := CopyScript(script); err != nil {
				return results, err
			}
			results = append(results, "copied to clipboard")
		default:
			return results, fmt.Errorf("macro: unknown output task %q", task)
		}
	}
	return results, nil
}

func sanitizeName(name string) string {
	var res []rune
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-':
			res = append(res, r)
		case r == ' ':
			res = append(res, '_')
		}
	}
	if len(res) == 0 {
		return "macro"
	}
	return string(res)
}
