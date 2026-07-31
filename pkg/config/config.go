package config

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

type TransformRule struct {
	Name        string `json:"name"`
	Type        string `json:"type"`        // "passthrough", "html2md", "regex"
	Pattern     string `json:"pattern"`     // regex pattern (for "regex" type)
	Replacement string `json:"replacement"` // replacement string (for "regex" type)
}

// MagnifierConfig holds configuration for the system magnifier service.
// It is embedded directly in Config under the "magnifier" JSON key.
type MagnifierConfig struct {
	Enabled          bool    `json:"enabled"`
	Display          string  `json:"display"`
	FullscreenHotkey string  `json:"fullscreen_hotkey"`
	LensHotkey       string  `json:"lens_hotkey"`
	ScrollModifier   string  `json:"scroll_modifier"`
	ZoomMin          float64 `json:"zoom_min"`
	ZoomMax          float64 `json:"zoom_max"`
	ZoomStep         float64 `json:"zoom_step"`
	InitialZoom      float64 `json:"initial_zoom"`
	LensSize         int     `json:"lens_size"`
	LensShape        string  `json:"lens_shape"`
	SmoothScaling    bool    `json:"smooth_scaling"`
}

// SnippetPickerConfig holds configuration for the snippet picker interface.
type SnippetPickerConfig struct {
	Width    int    `json:"width"`
	Height   int    `json:"height"`
	FontSize int    `json:"font_size"`
	FontFace string `json:"font_face"`
}

// EncoderSettings holds encoding parameters for screen recording.
type EncoderSettings struct {
	Encoder     string `json:"ffmpeg_encoder,omitempty"`
	ScaleAlgo   string `json:"ffmpeg_scale_algo,omitempty"`
	Preset      string `json:"ffmpeg_preset,omitempty"`
	CRF         string `json:"ffmpeg_crf,omitempty"`
	Tune        string `json:"ffmpeg_tune,omitempty"`
	Profile     string `json:"ffmpeg_profile,omitempty"`
	PixelFormat string `json:"ffmpeg_pixel_format,omitempty"`
	ExtraArgs   string `json:"ffmpeg_extra_args,omitempty"`
}

// AudioSettings holds audio capture and encoding parameters.
type AudioSettings struct {
	Enabled    bool   `json:"enabled"`
	Device     string `json:"device,omitempty"`
	SampleRate int    `json:"sample_rate,omitempty"`
	Channels   int    `json:"channels,omitempty"`
	Bitrate    int64  `json:"bitrate,omitempty"`
}

// RecorderSettings holds top-level recording configuration.
type RecorderSettings struct {
	Width          int             `json:"width,omitempty"` // output/encoded resolution
	Height         int             `json:"height,omitempty"`
	InternalWidth  int             `json:"internal_width,omitempty"` // capture resolution
	InternalHeight int             `json:"internal_height,omitempty"`
	FPS            int             `json:"fps,omitempty"`
	Bitrate        int64           `json:"bitrate,omitempty"`
	Encoder        EncoderSettings `json:"encoder"`
	Audio          AudioSettings   `json:"audio"`
}

// EditConfig controls the optional post-capture editing/annotation step.
type EditConfig struct {
	Enabled        bool   `json:"enabled"`         // Default: false
	Mode           string `json:"mode"`            // "builtin" or "external" (default: "builtin")
	ExternalCmd    string `json:"external_cmd"`    // e.g. "gimp {file}" — {file} is replaced with abs path
	BrushThickness uint32 `json:"brush_thickness"` // Default: 3
	FontScale      int    `json:"font_scale"`      // Default: 2
}

// UploaderConfig controls the optional post-capture upload step.
type UploaderConfig struct {
	Enabled        bool              `json:"enabled"`         // Default: false
	Endpoint       string            `json:"endpoint"`        // e.g. "https://api.imgur.com/3/image"
	FieldName      string            `json:"field_name"`      // multipart field name for the file, default "file"
	AuthHeader     string            `json:"auth_header"`     // e.g. "Authorization" or "Client-ID"
	AuthToken      string            `json:"auth_token"`      // plaintext token (prefer AuthTokenEnv)
	AuthTokenEnv   string            `json:"auth_token_env"`  // env var name to read token from instead
	ExtraFields    map[string]string `json:"extra_fields"`    // additional multipart form fields
	URLJSONPath    string            `json:"url_json_path"`   // dot-path into JSON response, e.g. "data.link"
	TimeoutSeconds int               `json:"timeout_seconds"` // Default: 30
}

// VisionConfig controls the optional post-capture LLM vision-explanation step.
type VisionConfig struct {
	Enabled        bool   `json:"enabled"`         // Default: false
	Provider       string `json:"provider"`        // "anthropic" or "openai" (default: "anthropic")
	Model          string `json:"model"`           // e.g. "claude-sonnet-4-6" or "gpt-4o"
	Prompt         string `json:"prompt"`          // instruction sent with the image
	APIKeyEnv      string `json:"api_key_env"`     // env var holding the API key
	SaveSidecar    bool   `json:"save_sidecar"`    // write result to <screenshot>.txt, default true
	TimeoutSeconds int    `json:"timeout_seconds"` // Default: 60
}

type TaskProfile struct {
	Name      string   `json:"name"`
	Tasks     []string `json:"tasks"`
	AppliesTo []string `json:"applies_to"` // "capture","ocr","ocr_auto","record"; empty -> ["capture"]
}

type BrowserBridgeConfig struct {
	Address  string `json:"address"`
	Port     int    `json:"port"`
	Provider string `json:"provider"`
	Prompt   string `json:"prompt"`
}

type Config struct {
	OutputDir            string              `json:"output_dir"`
	Hotkeys              HotkeysConfig       `json:"hotkeys"`
	SnippetPicker        SnippetPickerConfig `json:"snippet_picker"`
	OCRAddress           string              `json:"ocr_address"`           // Default: "http://localhost:8765"
	APIAddress           string              `json:"api_address"`           // Default: "localhost:4444"
	OCRLanguage          string              `json:"ocr_language"`          // Default: "ch"
	OCRLanguages         []string            `json:"ocr_languages"`         // Default: ["en", "ja", "ko", "ch"]
	OCRAutoFPS           float64             `json:"ocr_auto_fps"`          // Default: 1.0
	TranslationTarget    string              `json:"translation_target"`    // Default: "en"
	TranslationEngine    string              `json:"translation_engine"`    // "google" or "local" (default: "google")
	AutoTranslate        bool                `json:"auto_translate"`        // Default: false
	ColorPickerFormat    string              `json:"color_picker_format"`   // "hex", "rgb", "rgba", "hsl" (default: "hex")
	DisableNotifications bool                `json:"disable_notifications"` // Default: false
	ClipboardSessionFile string              `json:"clipboard_session_file"`
	SnippetFile          string              `json:"snippet_file"`
	AutomationDir        string              `json:"automation_dir"`
	TransformRules       []TransformRule     `json:"transform_rules"`
	Magnifier            MagnifierConfig     `json:"magnifier"`
	Recorder             RecorderSettings    `json:"recorder"`
	SnippetMode          string              `json:"snippet_mode"` // "paste" or "type" (default: "paste")
	PromptsPath          string              `json:"prompts_path"` // abs path to prompts directory
	SkillsPath           string              `json:"skills_path"`  // abs path to skills directory

	// --- new: post-capture task pipeline ---
	AfterCaptureTasks []string       `json:"after_capture_tasks"`     // ordered task names, e.g. ["edit","upload","vision","copy_image"]
	AfterOCRTasks     []string       `json:"after_ocr_tasks"`         // ordered task names for one-shot OCR
	AfterOCRAutoTasks []string       `json:"after_ocr_auto_tasks"`    // ordered task names for the realtime OCR loop
	AfterRecordTasks  []string       `json:"after_record_tasks"`      // ordered task names after a recording stops
	OCRAutoCopy       bool           `json:"ocr_auto_copy"`           // append copy_text to the ocr_auto chain
	Edit              EditConfig     `json:"edit"`
	Uploader          UploaderConfig `json:"uploader"`
	Vision            VisionConfig   `json:"vision"`

	// --- task profile system ---
	TaskProfiles       []TaskProfile `json:"task_profiles"`
	CurrentTaskProfile string        `json:"current_task_profile"`

	// --- browser bridge configuration ---
	BrowserBridge BrowserBridgeConfig `json:"browser_bridge"`
}

type HotkeysConfig struct {
	Screenshot           string `json:"screenshot"`
	RegionScreenshot     string `json:"region_screenshot"`
	WindowScreenshot     string `json:"window_screenshot"`
	OCRScreenshot        string `json:"ocr_screenshot"`
	OCRRegionScreenshot  string `json:"ocr_region_screenshot"`
	OCRWindowScreenshot  string `json:"ocr_window_screenshot"`
	RecordToggle         string `json:"record_toggle"`
	RecordAnnotate       string `json:"record_annotate"`
	RecordMarkFullscreen string `json:"record_mark_fullscreen"`
	RecordMarkRegion     string `json:"record_mark_region"`
	RecordMarkWindow     string `json:"record_mark_window"`
	RecordShowArea       string `json:"record_show_area"`
	RecordAudioOnly      string `json:"record_audio_only"`
	ClipboardCopyMod     string `json:"clipboard_copy_mod"`   // e.g. "Control-Shift"
	ClipboardPasteMod    string `json:"clipboard_paste_mod"`  // e.g. "Mod1-Shift"
	ClipboardCycleRule   string `json:"clipboard_cycle_rule"` // e.g. "Control-grave"
	OcrCycleModel        string `json:"ocr_cycle_model"`      // e.g. "Control-Mod1-grave"
	OCRAutoToggle        string `json:"ocr_auto_toggle"`      // e.g. "Control-Mod1-F1"
	OCRAutoFPS           string `json:"ocr_auto_fps"`         // e.g. "Control-Mod1-F2"
	SnippetPicker        string `json:"snippet_picker"`       // e.g. "Mod1-grave" (Alt+`)
	AutomationPicker     string `json:"automation_picker"`    // e.g. "Mod1-a" (Alt+a)
	WindowClassGrab      string `json:"window_class_grab"`    // e.g. "Shift-F4"
	ColorPicker          string `json:"color_picker"`         // e.g. "Shift-F5"
	SnippetCycleMode     string `json:"snippet_cycle_mode"`   // e.g. "Mod4-w"
	CycleTaskProfile     string `json:"cycle_task_profile"`   // e.g. "Control-Mod1-p"
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
			Screenshot:           "Control-Shift-s",
			RegionScreenshot:     "Control-Shift-a",
			WindowScreenshot:     "Shift-F2",
			OCRScreenshot:        "Control-Shift-o",
			OCRRegionScreenshot:  "Control-Shift-p",
			OCRWindowScreenshot:  "Shift-F3",
			RecordToggle:         "Control-Shift-r",
			RecordAnnotate:       "Control-Shift-Mod1-a",
			RecordMarkFullscreen: "Control-Mod1-f",
			RecordMarkRegion:     "Control-Mod1-r",
			RecordMarkWindow:     "Control-Mod1-w",
			RecordShowArea:       "Mod1-Shift-F4",
			RecordAudioOnly:      "Control-Shift-Mod1-F5",
			ClipboardCopyMod:     "Control-Shift",
			ClipboardPasteMod:    "Mod1-Shift",
			ClipboardCycleRule:   "Control-grave",
			OcrCycleModel:        "Control-Mod1-grave",
			OCRAutoToggle:        "Control-Mod1-F1",
			OCRAutoFPS:           "Control-Mod1-F2",
			SnippetPicker:        "Mod1-grave",
			AutomationPicker:     "Mod1-a",
			WindowClassGrab:      "Shift-F4",
			ColorPicker:          "Shift-F5",
			SnippetCycleMode:     "Mod4-w",
			CycleTaskProfile:     "Control-Mod1-p",
		},
		SnippetMode:          "paste",
		ColorPickerFormat:    "hex",
		OCRAddress:           "http://localhost:8765",
		APIAddress:           "localhost:4444",
		OCRLanguage:          "ch",
		OCRLanguages:         []string{"en", "ja", "ko", "ch"},
		OCRAutoFPS:           1.0,
		TranslationTarget:    "en",
		TranslationEngine:    "google",
		AutoTranslate:        false,
		DisableNotifications: false,
		ClipboardSessionFile: defaultSessionFile,
		SnippetFile:          defaultSnippetFile,
		AutomationDir:        defaultAutomationDir,
		TransformRules:       DefaultTransformRules(),
		Magnifier: MagnifierConfig{
			Enabled:          false,
			Display:          ":0.0",
			FullscreenHotkey: "super-f",
			LensHotkey:       "super-l",
			ScrollModifier:   "super",
			ZoomMin:          1.5,
			ZoomMax:          10.0,
			ZoomStep:         0.5,
			InitialZoom:      2.0,
			LensSize:         420,
			LensShape:        "circle",
			SmoothScaling:    true,
		},
		Recorder: RecorderSettings{
			Width:          0,
			Height:         0,
			InternalWidth:  0,
			InternalHeight: 0,
			FPS:            30,
			Bitrate:        4000000,
			Encoder: EncoderSettings{
				Encoder:     "libx264",
				ScaleAlgo:   "lanczos",
				Preset:      "ultrafast",
				CRF:         "28",
				Tune:        "animation",
				Profile:     "",
				PixelFormat: "yuv420p",
				ExtraArgs:   "",
			},
			Audio: AudioSettings{
				Enabled:    false,
				Device:     "default",
				SampleRate: 48000,
				Channels:   2,
				Bitrate:    128000,
			},
		},
		SnippetPicker: SnippetPickerConfig{
			Width:    550,
			Height:   390,
			FontSize: 14,
			FontFace: "",
		},
		PromptsPath: "/media/jang/home/Deve/web-reader-mcp-master/src/resources/prompts",
		SkillsPath:  "/media/jang/home/Deve/web-reader-mcp-master/src/resources/skills",
		AfterCaptureTasks:  []string{"edit", "upload", "vision", "copy_image"},
		AfterOCRTasks:      []string{"ocr", "translate", "display"},
		AfterOCRAutoTasks:  []string{"ocr", "translate", "display_live"},
		AfterRecordTasks:   []string{"copy_path"},
		OCRAutoCopy:        false,
		Edit: EditConfig{
			Enabled:        false,
			Mode:           "builtin",
			ExternalCmd:    "",
			BrushThickness: 3,
			FontScale:      2,
		},
		Uploader: UploaderConfig{
			Enabled:        false,
			Endpoint:       "",
			FieldName:      "file",
			AuthHeader:     "Authorization",
			AuthToken:      "",
			AuthTokenEnv:   "",
			ExtraFields:    map[string]string{},
			URLJSONPath:    "data.link",
			TimeoutSeconds: 30,
		},
		Vision: VisionConfig{
			Enabled:        false,
			Provider:       "anthropic",
			Model:          "claude-sonnet-4-6",
			Prompt:         "Describe what is shown in this screenshot in 2-3 concise sentences.",
			APIKeyEnv:      "ANTHROPIC_API_KEY",
			SaveSidecar:    true,
			TimeoutSeconds: 60,
		},
		TaskProfiles: []TaskProfile{
			{
				Name:      "LLM Vision",
				Tasks:     []string{"vision", "copy_llm"},
				AppliesTo: []string{"capture"},
			},
			{
				Name:      "Copy Path",
				Tasks:     []string{"copy_path"},
				AppliesTo: []string{"capture"},
			},
			{
				Name:      "Copy Image",
				Tasks:     []string{"copy_image"},
				AppliesTo: []string{"capture"},
			},
			{
				Name:      "OCR",
				Tasks:     []string{"ocr", "copy_text"},
				AppliesTo: []string{"capture"},
			},
			{
				Name:      "Translate",
				Tasks:     []string{"ocr", "translate", "copy_text"},
				AppliesTo: []string{"capture"},
			},
			{
				Name:      "Realtime Translate",
				Tasks:     []string{"ocr", "translate", "copy_text", "display_live"},
				AppliesTo: []string{"ocr_auto"},
			},
		},
		CurrentTaskProfile: "Copy Image",
		BrowserBridge: BrowserBridgeConfig{
			Address:  "127.0.0.1",
			Port:     9999,
			Provider: "gemini",
			Prompt:   "Describe what is shown in this screenshot in 2-3 concise sentences.",
		},
	}
}

// DefaultPortableConfig returns a Config struct with default output path inside the binary's folder.
func DefaultPortableConfig(binDir string) *Config {
	return &Config{
		OutputDir: filepath.Join(binDir, "zen-cap-outputs"),
		Hotkeys: HotkeysConfig{
			Screenshot:           "Control-Shift-s",
			RegionScreenshot:     "Control-Shift-a",
			WindowScreenshot:     "Shift-F2",
			OCRScreenshot:        "Control-Shift-o",
			OCRRegionScreenshot:  "Control-Shift-p",
			OCRWindowScreenshot:  "Shift-F3",
			RecordToggle:         "Control-Shift-r",
			RecordAnnotate:       "Control-Shift-Mod1-a",
			RecordMarkFullscreen: "Control-Mod1-f",
			RecordMarkRegion:     "Control-Mod1-r",
			RecordMarkWindow:     "Control-Mod1-w",
			RecordShowArea:       "Mod1-Shift-F4",
			RecordAudioOnly:      "Control-Shift-Mod1-F5",
			ClipboardCopyMod:     "Control-Shift",
			ClipboardPasteMod:    "Mod1-Shift",
			ClipboardCycleRule:   "Control-grave",
			OcrCycleModel:        "Control-Mod1-grave",
			OCRAutoToggle:        "Control-Mod1-F1",
			OCRAutoFPS:           "Control-Mod1-F2",
			SnippetPicker:        "Mod1-grave",
			AutomationPicker:     "Mod1-a",
			WindowClassGrab:      "Shift-F4",
			ColorPicker:          "Shift-F5",
			SnippetCycleMode:     "Mod4-w",
			CycleTaskProfile:     "Control-Mod1-p",
		},
		SnippetMode:          "paste",
		ColorPickerFormat:    "hex",
		OCRAddress:           "http://localhost:8765",
		APIAddress:           "localhost:4444",
		OCRLanguage:          "ch",
		OCRLanguages:         []string{"en", "ja", "ko", "ch"},
		OCRAutoFPS:           1.0,
		TranslationTarget:    "en",
		TranslationEngine:    "google",
		AutoTranslate:        false,
		DisableNotifications: false,
		ClipboardSessionFile: filepath.Join(binDir, "clipboard_session.json"),
		SnippetFile:          filepath.Join(binDir, "snippets.yaml"),
		AutomationDir:        filepath.Join(binDir, "automations"),
		TransformRules:       DefaultTransformRules(),
		Magnifier: MagnifierConfig{
			Enabled:          false,
			Display:          ":0.0",
			FullscreenHotkey: "super-f",
			LensHotkey:       "super-l",
			ScrollModifier:   "super",
			ZoomMin:          1.5,
			ZoomMax:          10.0,
			ZoomStep:         0.5,
			InitialZoom:      2.0,
			LensSize:         420,
			LensShape:        "circle",
			SmoothScaling:    true,
		},
		SnippetPicker: SnippetPickerConfig{
			Width:    550,
			Height:   390,
			FontSize: 14,
			FontFace: "",
		},
		Recorder: RecorderSettings{
			Width:          0,
			Height:         0,
			InternalWidth:  0,
			InternalHeight: 0,
			FPS:            30,
			Bitrate:        4000000,
			Encoder: EncoderSettings{
				Encoder:     "libx264",
				ScaleAlgo:   "lanczos",
				Preset:      "ultrafast",
				CRF:         "28",
				Tune:        "animation",
				Profile:     "",
				PixelFormat: "yuv420p",
				ExtraArgs:   "",
			},
			Audio: AudioSettings{
				Enabled:    false,
				Device:     "default",
				SampleRate: 48000,
				Channels:   2,
				Bitrate:    128000,
			},
		},
		PromptsPath: "/media/jang/home/Deve/web-reader-mcp-master/src/resources/prompts",
		SkillsPath:  "/media/jang/home/Deve/web-reader-mcp-master/src/resources/skills",
		AfterCaptureTasks:  []string{"edit", "upload", "vision", "copy_image"},
		AfterOCRTasks:      []string{"ocr", "translate", "display"},
		AfterOCRAutoTasks:  []string{"ocr", "translate", "display_live"},
		AfterRecordTasks:   []string{"copy_path"},
		OCRAutoCopy:        false,
		Edit: EditConfig{
			Enabled:        false,
			Mode:           "builtin",
			ExternalCmd:    "",
			BrushThickness: 3,
			FontScale:      2,
		},
		Uploader: UploaderConfig{
			Enabled:        false,
			Endpoint:       "",
			FieldName:      "file",
			AuthHeader:     "Authorization",
			AuthToken:      "",
			AuthTokenEnv:   "",
			ExtraFields:    map[string]string{},
			URLJSONPath:    "data.link",
			TimeoutSeconds: 30,
		},
		Vision: VisionConfig{
			Enabled:        false,
			Provider:       "anthropic",
			Model:          "claude-sonnet-4-6",
			Prompt:         "Describe what is shown in this screenshot in 2-3 concise sentences.",
			APIKeyEnv:      "ANTHROPIC_API_KEY",
			SaveSidecar:    true,
			TimeoutSeconds: 60,
		},
		TaskProfiles: []TaskProfile{
			{
				Name:      "LLM Vision",
				Tasks:     []string{"vision", "copy_llm"},
				AppliesTo: []string{"capture"},
			},
			{
				Name:      "Copy Path",
				Tasks:     []string{"copy_path"},
				AppliesTo: []string{"capture"},
			},
			{
				Name:      "Copy Image",
				Tasks:     []string{"copy_image"},
				AppliesTo: []string{"capture"},
			},
			{
				Name:      "OCR",
				Tasks:     []string{"ocr", "copy_text"},
				AppliesTo: []string{"capture"},
			},
			{
				Name:      "Translate",
				Tasks:     []string{"ocr", "translate", "copy_text"},
				AppliesTo: []string{"capture"},
			},
			{
				Name:      "Realtime Translate",
				Tasks:     []string{"ocr", "translate", "copy_text", "display_live"},
				AppliesTo: []string{"ocr_auto"},
			},
		},
		CurrentTaskProfile: "Copy Image",
		BrowserBridge: BrowserBridgeConfig{
			Address:  "127.0.0.1",
			Port:     9999,
			Provider: "gemini",
			Prompt:   "Describe what is shown in this screenshot in 2-3 concise sentences.",
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
				NotificationsDisabled = cfg.DisableNotifications
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
			NotificationsDisabled = defaultCfg.DisableNotifications
			return defaultCfg, createPath, nil
		}
	}

	NotificationsDisabled = defaultCfg.DisableNotifications
	return defaultCfg, "", nil
}

// NotificationsDisabled indicates if desktop notifications are globally disabled.
var NotificationsDisabled bool

var (
	lastNotificationID string
	lastNotificationMu sync.Mutex
)

// SendNotification displays a desktop notification via notify-send unless disabled.
// It uses notify-send's -p (print-id) and -r (replace-id) to update the previous notification in-place.
func SendNotification(title, message string) {
	if NotificationsDisabled {
		return
	}
	lastNotificationMu.Lock()
	defer lastNotificationMu.Unlock()

	args := []string{"-a", "Zen-Cap", "-p"}
	if lastNotificationID != "" {
		args = append(args, "-r", lastNotificationID)
	}
	args = append(args, title, message)

	cmd := exec.Command("notify-send", args...)
	output, err := cmd.Output()
	if err == nil {
		idStr := strings.TrimSpace(string(output))
		if _, errConv := strconv.Atoi(idStr); errConv == nil {
			lastNotificationID = idStr
		}
	}
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
	if cfg.Hotkeys.RecordAnnotate == "" {
		cfg.Hotkeys.RecordAnnotate = defaults.Hotkeys.RecordAnnotate
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
	if cfg.Hotkeys.RecordAudioOnly == "" {
		cfg.Hotkeys.RecordAudioOnly = defaults.Hotkeys.RecordAudioOnly
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
	if cfg.Hotkeys.OcrCycleModel == "" {
		cfg.Hotkeys.OcrCycleModel = defaults.Hotkeys.OcrCycleModel
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
	if cfg.Hotkeys.ColorPicker == "" {
		cfg.Hotkeys.ColorPicker = defaults.Hotkeys.ColorPicker
	}
	if cfg.Hotkeys.SnippetCycleMode == "" {
		cfg.Hotkeys.SnippetCycleMode = defaults.Hotkeys.SnippetCycleMode
	}
	if cfg.SnippetMode == "" {
		cfg.SnippetMode = defaults.SnippetMode
	}
	if cfg.ColorPickerFormat == "" {
		cfg.ColorPickerFormat = defaults.ColorPickerFormat
	}
	if cfg.OCRAddress == "" {
		cfg.OCRAddress = defaults.OCRAddress
	}
	if cfg.APIAddress == "" {
		cfg.APIAddress = defaults.APIAddress
	}
	if cfg.OCRLanguage == "" {
		cfg.OCRLanguage = defaults.OCRLanguage
	}
	if len(cfg.OCRLanguages) == 0 {
		cfg.OCRLanguages = defaults.OCRLanguages
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

	if cfg.SnippetPicker.Width <= 0 {
		cfg.SnippetPicker.Width = defaults.SnippetPicker.Width
	}
	if cfg.SnippetPicker.Height <= 0 {
		cfg.SnippetPicker.Height = defaults.SnippetPicker.Height
	}
	if cfg.SnippetPicker.FontSize <= 0 {
		cfg.SnippetPicker.FontSize = defaults.SnippetPicker.FontSize
	}
	if cfg.SnippetPicker.FontFace == "" {
		cfg.SnippetPicker.FontFace = defaults.SnippetPicker.FontFace
	}

	if cfg.PromptsPath == "" {
		cfg.PromptsPath = defaults.PromptsPath
	}
	if cfg.SkillsPath == "" {
		cfg.SkillsPath = defaults.SkillsPath
	}

	if len(cfg.AfterCaptureTasks) == 0 {
		cfg.AfterCaptureTasks = defaults.AfterCaptureTasks
	}
	if len(cfg.AfterOCRTasks) == 0 {
		cfg.AfterOCRTasks = defaults.AfterOCRTasks
	}
	if len(cfg.AfterOCRAutoTasks) == 0 {
		cfg.AfterOCRAutoTasks = defaults.AfterOCRAutoTasks
	}
	if len(cfg.AfterRecordTasks) == 0 {
		cfg.AfterRecordTasks = defaults.AfterRecordTasks
	}
	for i := range cfg.TaskProfiles {
		if len(cfg.TaskProfiles[i].AppliesTo) == 0 {
			cfg.TaskProfiles[i].AppliesTo = []string{"capture"}
		}
	}
	if cfg.Edit.Mode == "" {
		cfg.Edit.Mode = defaults.Edit.Mode
	}
	if cfg.Edit.BrushThickness == 0 {
		cfg.Edit.BrushThickness = defaults.Edit.BrushThickness
	}
	if cfg.Edit.FontScale == 0 {
		cfg.Edit.FontScale = defaults.Edit.FontScale
	}
	if cfg.Uploader.FieldName == "" {
		cfg.Uploader.FieldName = defaults.Uploader.FieldName
	}
	if cfg.Uploader.AuthHeader == "" {
		cfg.Uploader.AuthHeader = defaults.Uploader.AuthHeader
	}
	if cfg.Uploader.URLJSONPath == "" {
		cfg.Uploader.URLJSONPath = defaults.Uploader.URLJSONPath
	}
	if cfg.Uploader.TimeoutSeconds <= 0 {
		cfg.Uploader.TimeoutSeconds = defaults.Uploader.TimeoutSeconds
	}
	if cfg.Vision.Provider == "" {
		cfg.Vision.Provider = defaults.Vision.Provider
	}
	if cfg.Vision.Model == "" {
		cfg.Vision.Model = defaults.Vision.Model
	}
	if cfg.Vision.Prompt == "" {
		cfg.Vision.Prompt = defaults.Vision.Prompt
	}
	if cfg.Vision.APIKeyEnv == "" {
		cfg.Vision.APIKeyEnv = defaults.Vision.APIKeyEnv
	}
	if cfg.Vision.TimeoutSeconds <= 0 {
		cfg.Vision.TimeoutSeconds = defaults.Vision.TimeoutSeconds
	}

	if cfg.Recorder.Width <= 0 {
		cfg.Recorder.Width = defaults.Recorder.Width
	}
	if cfg.Recorder.Height <= 0 {
		cfg.Recorder.Height = defaults.Recorder.Height
	}
	if cfg.Recorder.FPS <= 0 {
		cfg.Recorder.FPS = defaults.Recorder.FPS
	}
	if cfg.Recorder.Bitrate <= 0 {
		cfg.Recorder.Bitrate = defaults.Recorder.Bitrate
	}
	if cfg.Recorder.Encoder.Encoder == "" {
		cfg.Recorder.Encoder.Encoder = defaults.Recorder.Encoder.Encoder
	}
	if cfg.Recorder.Encoder.ScaleAlgo == "" {
		cfg.Recorder.Encoder.ScaleAlgo = defaults.Recorder.Encoder.ScaleAlgo
	}
	if cfg.Recorder.Encoder.Preset == "" {
		cfg.Recorder.Encoder.Preset = defaults.Recorder.Encoder.Preset
	}
	if cfg.Recorder.Encoder.CRF == "" {
		cfg.Recorder.Encoder.CRF = defaults.Recorder.Encoder.CRF
	}
	if cfg.Recorder.Encoder.Tune == "" {
		cfg.Recorder.Encoder.Tune = defaults.Recorder.Encoder.Tune
	}
	if cfg.Recorder.Encoder.PixelFormat == "" {
		cfg.Recorder.Encoder.PixelFormat = defaults.Recorder.Encoder.PixelFormat
	}
	if cfg.Recorder.Audio.Device == "" {
		cfg.Recorder.Audio.Device = defaults.Recorder.Audio.Device
	}
	if cfg.Recorder.Audio.SampleRate <= 0 {
		cfg.Recorder.Audio.SampleRate = defaults.Recorder.Audio.SampleRate
	}
	if cfg.Recorder.Audio.Channels <= 0 {
		cfg.Recorder.Audio.Channels = defaults.Recorder.Audio.Channels
	}
	if cfg.Recorder.Audio.Bitrate <= 0 {
		cfg.Recorder.Audio.Bitrate = defaults.Recorder.Audio.Bitrate
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
