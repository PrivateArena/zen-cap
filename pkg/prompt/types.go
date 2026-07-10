package prompt

// PromptDef is the new prompt definition that replaces the old PromptRole.
// It is deserialised from individual YAML files in the prompts directory.
type PromptDef struct {
	Name          string   `yaml:"name"`
	Description   string   `yaml:"description"`
	Role          string   `yaml:"role"`
	Job           string   `yaml:"job"`
	Template      string   `yaml:"template"`
	Tags          []string `yaml:"tags"`
	EnabledSkills []string `yaml:"enabledSkills"`
}
