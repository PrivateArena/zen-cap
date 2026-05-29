// [VERIFIED]
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type Config struct {
	OutputDir string        `json:"output_dir"`
	Hotkeys   HotkeysConfig `json:"hotkeys"`
}

type HotkeysConfig struct {
	Screenshot   string `json:"screenshot"`
	RecordToggle string `json:"record_toggle"`
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

	return &Config{
		OutputDir: defaultOutputDir,
		Hotkeys: HotkeysConfig{
			Screenshot:   "Control-Shift-s",
			RecordToggle: "Control-Shift-r",
		},
	}
}

// DefaultPortableConfig returns a Config struct with default output path inside the binary's folder.
func DefaultPortableConfig(binDir string) *Config {
	return &Config{
		OutputDir: filepath.Join(binDir, "zen-cap-outputs"),
		Hotkeys: HotkeysConfig{
			Screenshot:   "Control-Shift-s",
			RecordToggle: "Control-Shift-r",
		},
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
	if cfg.Hotkeys.RecordToggle == "" {
		cfg.Hotkeys.RecordToggle = defaults.Hotkeys.RecordToggle
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
