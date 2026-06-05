// [VERIFIED]
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type TransformRule struct {
	Name        string `json:"name"`
	Type        string `json:"type"`        // "passthrough", "html2md", "regex"
	Pattern     string `json:"pattern"`     // regex pattern (for "regex" type)
	Replacement string `json:"replacement"` // replacement string (for "regex" type)
}

type Config struct {
	OutputDir            string          `json:"output_dir"`
	Hotkeys              HotkeysConfig   `json:"hotkeys"`
	ClipboardMode        string          `json:"clipboard_mode"`     // "image", "path", "ocr", "translate", "none"
	OCRAddress           string          `json:"ocr_address"`        // Default: "http://localhost:8765"
	OCRLanguage          string          `json:"ocr_language"`       // Default: "ch"
	TranslationTarget    string          `json:"translation_target"` // Default: "en"
	TranslationEngine    string          `json:"translation_engine"` // "google" or "local" (default: "google")
	AutoTranslate        bool            `json:"auto_translate"`     // Default: false
	ClipboardSessionFile string          `json:"clipboard_session_file"`
	SnippetFile          string          `json:"snippet_file"`
	AutomationDir        string          `json:"automation_dir"`
	TransformRules       []TransformRule `json:"transform_rules"`
}

type HotkeysConfig struct {
	Screenshot          string `json:"screenshot"`
	RegionScreenshot    string `json:"region_screenshot"`
	WindowScreenshot    string `json:"window_screenshot"`
	OCRScreenshot       string `json:"ocr_screenshot"`
	OCRRegionScreenshot string `json:"ocr_region_screenshot"`
	OCRWindowScreenshot string `json:"ocr_window_screenshot"`
	RecordToggle           string `json:"record_toggle"`
	RecordMarkFullscreen   string `json:"record_mark_fullscreen"`
	RecordMarkRegion       string `json:"record_mark_region"`
	RecordMarkWindow       string `json:"record_mark_window"`
	RecordShowArea         string `json:"record_show_area"`
	ClipboardCopyMod       string `json:"clipboard_copy_mod"`   // e.g. "Control-Shift"
	ClipboardPasteMod      string `json:"clipboard_paste_mod"`  // e.g. "Mod1-Shift"
	ClipboardCycleRule     string `json:"clipboard_cycle_rule"` // e.g. "Control-grave"
	SnippetPicker          string `json:"snippet_picker"`       // e.g. "Mod1-grave" (Alt+`)
	AutomationPicker       string `json:"automation_picker"`    // e.g. "Mod1-a" (Alt+a)
	WindowClassGrab        string `json:"window_class_grab"`    // e.g. "Shift-F4"
}

func DefaultTransformRules() []TransformRule {
	return []TransformRule{
		{
			Name: "None",
			Type: "passthrough",
		},
		{
			Name: "HTML -> Markdown",
			Type: "html2md",
		},
		{
			Name:        "Strip [tokens]",
			Type:        "regex",
			Pattern:     `\[[a-zA-Z0-9._-]+\]`,
			Replacement: "",
		},
	}
}

// getBinaryDir returns the directory of the running executable.
// It detects 'go run' or temp builds and falls back to CWD in those cases.
func getBinaryDir() string {
	exe, err := os.Executable()
	if err != nil {
		dir, _ := os.Getwd()
		return dir
	}
	dir := filepath.Dir(exe)
	// Detect 'go run' or temp builds
	if strings.Contains(exe, "go-build") || strings.Contains(dir, "Temp") || strings.Contains(dir, "tmp") {
		dir, _ = os.Getwd()
	}
	return dir
}

// DefaultConfig returns a Config struct initialized with default values.
func DefaultConfig() *Config {
	home, err := os.UserHomeDir()
	var defaultOutputDir string
	if err == nil {
		defaultOutputDir = filepath.Join(home, "zen-cap-outputs")
	} else {
		defaultOutputDir = "."
	}

	defaultSessionFile := filepath.Join(filepath.Dir(defaultOutputDir), ".config", "zen-cap", "clipboard_session.json")
	defaultSnippetFile, _ := filepath.Abs("snippets.yaml")
	defaultAutomationDir, _ := filepath.Abs("automations")
	if home, err := os.UserHomeDir(); err == nil {
		defaultSessionFile = filepath.Join(home, ".config", "zen-cap", "clipboard_session.json")
	}

	return &Config{
		OutputDir: defaultOutputDir,
		Hotkeys: HotkeysConfig{
			Screenshot:          "Control-Shift-s",
			RegionScreenshot:    "Control-Shift-a",
			WindowScreenshot:    "Shift-F2",
			OCRScreenshot:       "Control-Shift-o",
			OCRRegionScreenshot: "Control-Shift-p",
			OCRWindowScreenshot: "Shift-F3",
			RecordToggle:         "Control-Shift-r",
			RecordMarkFullscreen: "Control-Mod1-f",
			RecordMarkRegion:     "Control-Mod1-r",
			RecordMarkWindow:     "Control-Mod1-w",
			RecordShowArea:       "Mod1-Shift-F4",
			ClipboardCopyMod:     "Control-Shift",
			ClipboardPasteMod:    "Mod1-Shift",
			ClipboardCycleRule:   "Control-grave",
			SnippetPicker:        "Mod1-grave",
			AutomationPicker:     "Mod1-a",
			WindowClassGrab:      "Shift-F4",
		},
		ClipboardMode:        "image",
		OCRAddress:           "http://localhost:8765",
		OCRLanguage:          "ch",
		TranslationTarget:    "en",
		TranslationEngine:    "google",
		AutoTranslate:        false,
		ClipboardSessionFile: defaultSessionFile,
		SnippetFile:          defaultSnippetFile,
		AutomationDir:         defaultAutomationDir,
		TransformRules:       DefaultTransformRules(),
	}
}

// DefaultPortableConfig returns a Config struct with default output path inside the binary's folder.
func DefaultPortableConfig(binDir string) *Config {
	return &Config{
		OutputDir: filepath.Join(binDir, "zen-cap-outputs"),
		Hotkeys: HotkeysConfig{
			Screenshot:          "Control-Shift-s",
			RegionScreenshot:    "Control-Shift-a",
			WindowScreenshot:    "Shift-F2",
			OCRScreenshot:       "Control-Shift-o",
			OCRRegionScreenshot: "Control-Shift-p",
			OCRWindowScreenshot: "Shift-F3",
			RecordToggle:         "Control-Shift-r",
			RecordMarkFullscreen: "Control-Mod1-f",
			RecordMarkRegion:     "Control-Mod1-r",
			RecordMarkWindow:     "Control-Mod1-w",
			RecordShowArea:       "Mod1-Shift-F4",
			ClipboardCopyMod:     "Control-Shift",
			ClipboardPasteMod:    "Mod1-Shift",
			ClipboardCycleRule:   "Control-grave",
			SnippetPicker:        "Mod1-grave",
			AutomationPicker:     "Mod1-a",
			WindowClassGrab:      "Shift-F4",
		},
		ClipboardMode:        "image",
		OCRAddress:           "http://localhost:8765",
		OCRLanguage:          "ch",
		TranslationTarget:    "en",
		TranslationEngine:    "google",
		AutoTranslate:        false,
		ClipboardSessionFile: filepath.Join(binDir, "clipboard_session.json"),
		SnippetFile:          filepath.Join(binDir, "snippets.yaml"),
		AutomationDir:         filepath.Join(binDir, "automations"),
		TransformRules:       DefaultTransformRules(),
	}
}

// LoadConfig attempts to load the config from:
// 1. Binary Directory: <binDir>/config.json
// 2. Working Directory: ./config.json
// 3. User Config Directory (unless portable.dat is present)
// If none exists, it creates a default configuration file.
func LoadConfig() (*Config, string, error) {
	binDir := getBinaryDir()

	// Check if we have a portable.dat lock in the binary directory
	portableLockPath := filepath.Join(binDir, "portable.dat")
	_, errPortableLock := os.Stat(portableLockPath)
	isPortable := errPortableLock == nil

	// Compile the list of config paths in order of priority
	var configPaths []string

	// 1. Binary directory path (highest priority)
	configPaths = append(configPaths, filepath.Join(binDir, "config.json"))

	// 2. CWD path
	configPaths = append(configPaths, "config.json")

	// 3. User Config Directory (fallback, only if not strictly locked to portable)
	var userConfigPath string
	userConfigDir, err := os.UserConfigDir()
	if err == nil && !isPortable {
		userConfigPath = filepath.Join(userConfigDir, "zen-cap", "config.json")
		configPaths = append(configPaths, userConfigPath)
	}

	// Search for config file
	for _, path := range configPaths {
		absPath, _ := filepath.Abs(path)
		if _, err := os.Stat(absPath); err == nil {
			cfg, err := readConfig(absPath, binDir, isPortable)
			if err == nil {
				// Log loaded config path to stderr for discovery transparency
				fmt.Fprintf(os.Stderr, "[Config] Loaded from: %s\n", absPath)
				return cfg, absPath, nil
			}
		}
	}

	// If no config file exists, create a default one
	var defaultCfg *Config
	var createPath string

	if isPortable {
		defaultCfg = DefaultPortableConfig(binDir)
		createPath = filepath.Join(binDir, "config.json")
	} else {
		defaultCfg = DefaultConfig()
		if userConfigPath != "" {
			createPath = userConfigPath
		} else {
			createPath = filepath.Join(binDir, "config.json")
		}
	}

	createDir := filepath.Dir(createPath)
	if err := os.MkdirAll(createDir, 0755); err == nil {
		if err := SaveConfig(defaultCfg, createPath); err == nil {
			fmt.Fprintf(os.Stderr, "[Config] Created default configuration file at: %s\n", createPath)
			return defaultCfg, createPath, nil
		}
	}

	return defaultCfg, "", nil
}

func readConfig(path string, binDir string, isPortable bool) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("invalid config JSON in %s: %w", path, err)
	}

	// Fallback to default values for empty fields
	var defaults *Config
	if isPortable {
		defaults = DefaultPortableConfig(binDir)
	} else {
		defaults = DefaultConfig()
	}

	if cfg.OutputDir == "" {
		cfg.OutputDir = defaults.OutputDir
	}
	if cfg.Hotkeys.Screenshot == "" {
		cfg.Hotkeys.Screenshot = defaults.Hotkeys.Screenshot
	}
	if cfg.Hotkeys.RegionScreenshot == "" {
		cfg.Hotkeys.RegionScreenshot = defaults.Hotkeys.RegionScreenshot
	}
	if cfg.Hotkeys.WindowScreenshot == "" {
		cfg.Hotkeys.WindowScreenshot = defaults.Hotkeys.WindowScreenshot
	}
	if cfg.Hotkeys.OCRScreenshot == "" {
		cfg.Hotkeys.OCRScreenshot = defaults.Hotkeys.OCRScreenshot
	}
	if cfg.Hotkeys.OCRRegionScreenshot == "" {
		cfg.Hotkeys.OCRRegionScreenshot = defaults.Hotkeys.OCRRegionScreenshot
	}
	if cfg.Hotkeys.OCRWindowScreenshot == "" {
		cfg.Hotkeys.OCRWindowScreenshot = defaults.Hotkeys.OCRWindowScreenshot
	}
	if cfg.Hotkeys.RecordToggle == "" {
		cfg.Hotkeys.RecordToggle = defaults.Hotkeys.RecordToggle
	}
	if cfg.Hotkeys.RecordMarkFullscreen == "" {
		cfg.Hotkeys.RecordMarkFullscreen = defaults.Hotkeys.RecordMarkFullscreen
	}
	if cfg.Hotkeys.RecordMarkRegion == "" {
		cfg.Hotkeys.RecordMarkRegion = defaults.Hotkeys.RecordMarkRegion
	}
	if cfg.Hotkeys.RecordMarkWindow == "" {
		cfg.Hotkeys.RecordMarkWindow = defaults.Hotkeys.RecordMarkWindow
	}
	if cfg.Hotkeys.RecordShowArea == "" {
		cfg.Hotkeys.RecordShowArea = defaults.Hotkeys.RecordShowArea
	}
	if cfg.Hotkeys.ClipboardCopyMod == "" {
		cfg.Hotkeys.ClipboardCopyMod = defaults.Hotkeys.ClipboardCopyMod
	}
	if cfg.Hotkeys.ClipboardPasteMod == "" {
		cfg.Hotkeys.ClipboardPasteMod = defaults.Hotkeys.ClipboardPasteMod
	}
	if cfg.Hotkeys.ClipboardCycleRule == "" {
		cfg.Hotkeys.ClipboardCycleRule = defaults.Hotkeys.ClipboardCycleRule
	}
	if cfg.Hotkeys.SnippetPicker == "" {
		cfg.Hotkeys.SnippetPicker = defaults.Hotkeys.SnippetPicker
	}
	if cfg.Hotkeys.AutomationPicker == "" {
		cfg.Hotkeys.AutomationPicker = defaults.Hotkeys.AutomationPicker
	}
	if cfg.Hotkeys.WindowClassGrab == "" {
		cfg.Hotkeys.WindowClassGrab = defaults.Hotkeys.WindowClassGrab
	}
	if cfg.ClipboardMode == "" {
		cfg.ClipboardMode = defaults.ClipboardMode
	}
	if cfg.OCRAddress == "" {
		cfg.OCRAddress = defaults.OCRAddress
	}
	if cfg.OCRLanguage == "" {
		cfg.OCRLanguage = defaults.OCRLanguage
	}
	if cfg.TranslationTarget == "" {
		cfg.TranslationTarget = defaults.TranslationTarget
	}
	if cfg.TranslationEngine == "" {
		cfg.TranslationEngine = defaults.TranslationEngine
	}
	if cfg.ClipboardSessionFile == "" {
		cfg.ClipboardSessionFile = defaults.ClipboardSessionFile
	}
	if cfg.SnippetFile == "" || cfg.SnippetFile == defaults.SnippetFile {
		binSnippet := filepath.Join(binDir, "snippets.yaml")
		cwdSnippet := "snippets.yaml"
		if _, err := os.Stat(binSnippet); err == nil {
			cfg.SnippetFile = binSnippet
		} else if _, err := os.Stat(cwdSnippet); err == nil {
			cfg.SnippetFile, _ = filepath.Abs(cwdSnippet)
		} else {
			if isPortable {
				cfg.SnippetFile = binSnippet
			} else {
				cfg.SnippetFile, _ = filepath.Abs("snippets.yaml")
			}
		}
	}
	if cfg.AutomationDir == "" || cfg.AutomationDir == defaults.AutomationDir {
		binAuto := filepath.Join(binDir, "automations")
		cwdAuto := "automations"
		if _, err := os.Stat(binAuto); err == nil {
			cfg.AutomationDir = binAuto
		} else if _, err := os.Stat(cwdAuto); err == nil {
			cfg.AutomationDir, _ = filepath.Abs(cwdAuto)
		} else {
			if isPortable {
				cfg.AutomationDir = binAuto
			} else {
				cfg.AutomationDir, _ = filepath.Abs("automations")
			}
		}
	}
	if len(cfg.TransformRules) == 0 {
		cfg.TransformRules = defaults.TransformRules
	}

	return &cfg, nil
}

// SaveConfig saves the configuration to the specified path.
func SaveConfig(cfg *Config, path string) error {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}
