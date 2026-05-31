package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

// ThemeColors defines the color palette mapping for a single mode (light or dark).
type ThemeColors struct {
	BgPrimary       string `json:"bg_primary"`
	BgSecondary     string `json:"bg_secondary"`
	TextPrimary     string `json:"text_primary"`
	TextSecondary   string `json:"text_secondary"`
	TextMuted       string `json:"text_muted"`
	BorderColor     string `json:"border_color"`
	AccentPrimary   string `json:"accent_primary"`
	AccentSecondary string `json:"accent_secondary"`
	AccentHover     string `json:"accent_hover"`
	AccentBg        string `json:"accent_bg"`
}

// Theme represents a custom or predefined theme, containing light and dark variants.
type Theme struct {
	Name        string      `json:"name"`
	DefaultMode string      `json:"default_mode"` // "light" or "dark"
	Light       ThemeColors `json:"light"`
	Dark        ThemeColors `json:"dark"`
	Custom      bool        `json:"custom"` // true if user-created
}

// DefaultThemes defines the predefined themes available in NexWiki.
var DefaultThemes = []Theme{
	{
		Name:        "default",
		DefaultMode: "light",
		Custom:      false,
		Light: ThemeColors{
			BgPrimary:       "#f8fafc", // slate-50
			BgSecondary:     "#ffffff", // white
			TextPrimary:     "#0f172a", // slate-900
			TextSecondary:   "#334155", // slate-700
			TextMuted:       "#64748b", // slate-500
			BorderColor:     "#e2e8f0", // slate-200
			AccentPrimary:   "#4f46e5", // indigo-600
			AccentSecondary: "#7c3aed", // violet-600
			AccentHover:     "#3730a3", // indigo-800
			AccentBg:        "#e0e7ff", // indigo-100
		},
		Dark: ThemeColors{
			BgPrimary:       "#020617", // slate-955
			BgSecondary:     "#0f172a", // slate-900
			TextPrimary:     "#f8fafc", // slate-50
			TextSecondary:   "#cbd5e1", // slate-300
			TextMuted:       "#64748b", // slate-500
			BorderColor:     "#1e293b", // slate-800
			AccentPrimary:   "#4f46e5", // indigo-600
			AccentSecondary: "#10b981", // emerald-500
			AccentHover:     "#6366f1", // indigo-500
			AccentBg:        "rgba(49, 46, 129, 0.2)",
		},
	},
}

// ThemeStore handles persistent custom theme files in the wiki's data directory.
type ThemeStore struct {
	filePath string
	mu       sync.RWMutex
}

// NewThemeStore builds a new ThemeStore.
func NewThemeStore(dataDir string) *ThemeStore {
	return &ThemeStore{
		filePath: filepath.Join(dataDir, "custom_themes.json"),
	}
}

// LoadCustomThemes reads user-created themes from custom_themes.json.
func (ts *ThemeStore) LoadCustomThemes() ([]Theme, error) {
	ts.mu.RLock()
	defer ts.mu.RUnlock()

	if _, err := os.Stat(ts.filePath); os.IsNotExist(err) {
		return []Theme{}, nil
	}

	data, err := os.ReadFile(ts.filePath)
	if err != nil {
		return nil, err
	}

	var themes []Theme
	if err := json.Unmarshal(data, &themes); err != nil {
		return nil, err
	}

	return themes, nil
}

// SaveCustomThemes persists user-created themes to custom_themes.json.
func (ts *ThemeStore) SaveCustomThemes(themes []Theme) error {
	ts.mu.Lock()
	defer ts.mu.Unlock()

	data, err := json.MarshalIndent(themes, "", "  ")
	if err != nil {
		return err
	}

	// Ensure target directory exists
	dir := filepath.Dir(ts.filePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	return os.WriteFile(ts.filePath, data, 0644)
}
