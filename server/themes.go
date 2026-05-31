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
	{
		Name:        "july-4th",
		DefaultMode: "light",
		Custom:      false,
		Light: ThemeColors{
			BgPrimary:       "#f8fafc", // soft slate white
			BgSecondary:     "#ffffff", // white
			TextPrimary:     "#0f172a", // navy
			TextSecondary:   "#1e293b", // copy slate
			TextMuted:       "#64748b", // muted
			BorderColor:     "#cbd5e1", // blue gray border
			AccentPrimary:   "#b91c1c", // patriotic red
			AccentSecondary: "#1d4ed8", // patriotic blue
			AccentHover:     "#991b1b",
			AccentBg:        "#fee2e2", // red accent bg
		},
		Dark: ThemeColors{
			BgPrimary:       "#0b0f19", // deep navy sky
			BgSecondary:     "#111827", // dark navy crypt
			TextPrimary:     "#f8fafc",
			TextSecondary:   "#cbd5e1",
			TextMuted:       "#6b7280",
			BorderColor:     "#1f2937",
			AccentPrimary:   "#ef4444", // bright holiday red
			AccentSecondary: "#3b82f6", // bright holiday blue
			AccentHover:     "#f87171",
			AccentBg:        "rgba(239, 68, 68, 0.15)",
		},
	},
	{
		Name:        "halloween",
		DefaultMode: "light",
		Custom:      false,
		Light: ThemeColors{
			BgPrimary:       "#fffbeb", // warm amber cream
			BgSecondary:     "#ffffff",
			TextPrimary:     "#1c1917", // charcoal black
			TextSecondary:   "#44403c",
			TextMuted:       "#78716c",
			BorderColor:     "#fed7aa", // pumpkin border
			AccentPrimary:   "#ea580c", // pumpkin orange
			AccentSecondary: "#7c3aed", // witch purple
			AccentHover:     "#c2410c",
			AccentBg:        "#ffedd5",
		},
		Dark: ThemeColors{
			BgPrimary:       "#0c0a09", // haunted stone black
			BgSecondary:     "#1c1917", // haunted stone gray
			TextPrimary:     "#fafaf9", // ghostly white
			TextSecondary:   "#d6d3d1", // skeleton gray
			TextMuted:       "#78716c",
			BorderColor:     "#44403c",
			AccentPrimary:   "#f97316", // neon orange
			AccentSecondary: "#a78bfa", // neon purple
			AccentHover:     "#fb923c",
			AccentBg:        "rgba(249, 115, 22, 0.15)",
		},
	},
	{
		Name:        "christmas",
		DefaultMode: "light",
		Custom:      false,
		Light: ThemeColors{
			BgPrimary:       "#f0fdf4", // minty white
			BgSecondary:     "#ffffff",
			TextPrimary:     "#14532d", // dark pine green
			TextSecondary:   "#166534",
			TextMuted:       "#71717a",
			BorderColor:     "#bbf7d0", // holly green border
			AccentPrimary:   "#b91c1c", // holiday crimson red
			AccentSecondary: "#eab308", // star gold
			AccentHover:     "#991b1b",
			AccentBg:        "#dcfce7",
		},
		Dark: ThemeColors{
			BgPrimary:       "#022c22", // deep evergreen pine
			BgSecondary:     "#064e3b",
			TextPrimary:     "#f0fdf4", // snow white
			TextSecondary:   "#a7f3d0", // minty text
			TextMuted:       "#6b7280",
			BorderColor:     "#0f766e",
			AccentPrimary:   "#ef4444", // bright holiday red
			AccentSecondary: "#facc15", // bright star gold
			AccentHover:     "#f87171",
			AccentBg:        "rgba(239, 68, 68, 0.15)",
		},
	},
	{
		Name:        "new-years",
		DefaultMode: "light",
		Custom:      false,
		Light: ThemeColors{
			BgPrimary:       "#fafaf9", // champagne white
			BgSecondary:     "#ffffff",
			TextPrimary:     "#1c1917", // warm stone black
			TextSecondary:   "#44403c",
			TextMuted:       "#78716c",
			BorderColor:     "#e7e5e4", // silver border
			AccentPrimary:   "#ca8a04", // metallic dark gold
			AccentSecondary: "#78716c", // slate silver
			AccentHover:     "#a16207",
			AccentBg:        "#fef9c3",
		},
		Dark: ThemeColors{
			BgPrimary:       "#09090b", // midnight sky black
			BgSecondary:     "#18181b",
			TextPrimary:     "#fafafa", // glittering white
			TextSecondary:   "#d4d4d8", // glittering silver
			TextMuted:       "#71717a",
			BorderColor:     "#27272a",
			AccentPrimary:   "#eab308", // celebration gold
			AccentSecondary: "#a1a1aa", // celebration silver
			AccentHover:     "#facc15",
			AccentBg:        "rgba(234, 179, 8, 0.15)",
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
