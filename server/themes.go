package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
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

// ThemeSchedule defines an annual recurrence window (month/day, inclusive).
type ThemeSchedule struct {
	StartMonth int `json:"start_month"`
	StartDay   int `json:"start_day"`
	EndMonth   int `json:"end_month"`
	EndDay     int `json:"end_day"`
}

// Theme represents a custom or predefined theme, containing light and dark variants.
type Theme struct {
	Name        string         `json:"name"`
	DefaultMode string         `json:"default_mode"` // "light" or "dark"
	Light       ThemeColors    `json:"light"`
	Dark        ThemeColors    `json:"dark"`
	Custom      bool           `json:"custom"`             // true if user-created
	Schedule    *ThemeSchedule `json:"schedule,omitempty"` // nil = no schedule
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
		Name:        "valentine-day",
		DefaultMode: "light",
		Custom:      false,
		Schedule:    &ThemeSchedule{StartMonth: 2, StartDay: 10, EndMonth: 2, EndDay: 18},
		Light: ThemeColors{
			BgPrimary:       "#fff5f7", // soft blush pink
			BgSecondary:     "#ffffff", // white
			TextPrimary:     "#4c0519", // deep rose
			TextSecondary:   "#be185d", // pinkish berry
			TextMuted:       "#fb7185", // light pink
			BorderColor:     "#fbcfe8", // very light pink
			AccentPrimary:   "#e11d48", // vibrant rose red
			AccentSecondary: "#db2777", // hot pink
			AccentHover:     "#be185d",
			AccentBg:        "rgba(244, 114, 160, 0.15)",
		},
		Dark: ThemeColors{
			BgPrimary:       "#2d0a13", // deep wine black
			BgSecondary:     "#4c0519", // dark rose brown
			TextPrimary:     "#fff1f2", // pale blush white
			TextSecondary:   "#fdaeec", // soft pink
			TextMuted:       "#fb7185",
			BorderColor:     "#881337", // deep wine border
			AccentPrimary:   "#fb7185", // bright rose pink
			AccentSecondary: "#f43f5e", // neon red-pink
			AccentHover:     "#ea580c",
			AccentBg:        "rgba(239, 68, 68, 0.2)",
		},
	},
	{
		Name:        "st-patricks-day",
		DefaultMode: "light",
		Custom:      false,
		Schedule:    &ThemeSchedule{StartMonth: 3, StartDay: 10, EndMonth: 4, EndDay: 2},
		Light: ThemeColors{
			BgPrimary:       "#f0fdf4", // minty white
			BgSecondary:     "#ffffff",
			TextPrimary:     "#064e3b", // dark emerald green
			TextSecondary:   "#15803d",
			TextMuted:       "#9caab2", // silver/gray
			BorderColor:     "#bbf7d0", // holly green border
			AccentPrimary:   "#16a34a", // bright grass green
			AccentSecondary: "#ca8a04", // metallic gold
			AccentHover:     "#15803d",
			AccentBg:        "rgba(22, 163, 74, 0.1)",
		},
		Dark: ThemeColors{
			BgPrimary:       "#064e3b", // deep emerald green
			BgSecondary:     "#065f46",
			TextPrimary:     "#ecfdf5", // snow-white mint
			TextSecondary:   "#a7f3d0",
			TextMuted:       "#6b7280",
			BorderColor:     "#047857",
			AccentPrimary:   "#fbbf24", // bright gold
			AccentSecondary: "#10b981", // emerald green
			AccentHover:     "#facc15",
			AccentBg:        "rgba(16, 185, 129, 0.2)",
		},
	},
	{
		Name:        "memorial-day",
		DefaultMode: "light",
		Custom:      false,
		Schedule:    &ThemeSchedule{StartMonth: 5, StartDay: 20, EndMonth: 6, EndDay: 10},
		Light: ThemeColors{
			BgPrimary:       "#f8fafc", // slate white
			BgSecondary:     "#ffffff",
			TextPrimary:     "#1e3a8a", // navy blue
			TextSecondary:   "#1d4ed8",
			TextMuted:       "#64748b",
			BorderColor:     "#cbd5e1",
			AccentPrimary:   "#ef4444", // patriotic red
			AccentSecondary: "#1e40af", // deep blue
			AccentHover:     "#dc2626",
			AccentBg:        "rgba(30, 73, 182, 0.1)",
		},
		Dark: ThemeColors{
			BgPrimary:       "#0f172a", // dark navy sky
			BgSecondary:     "#1e293b",
			TextPrimary:     "#f8fafc",
			TextSecondary:   "#cbd5e1",
			TextMuted:       "#64748b",
			BorderColor:     "#334155",
			AccentPrimary:   "#ef4444", // bright holiday red
			AccentSecondary: "#60a5fa", // sky blue
			AccentHover:     "#f87171",
			AccentBg:        "rgba(239, 68, 68, 0.15)",
		},
	},
	{
		Name:        "patriots-day",
		DefaultMode: "light",
		Custom:      false,
		Schedule:    &ThemeSchedule{StartMonth: 9, StartDay: 8, EndMonth: 9, EndDay: 12},
		Light: ThemeColors{
			BgPrimary:       "#f8fafc", // neutral white
			BgSecondary:     "#ffffff",
			TextPrimary:     "#0f172a", // slate navy
			TextSecondary:   "#334155",
			TextMuted:       "#64748b",
			BorderColor:     "#e2e8f0",
			AccentPrimary:   "#1e293b", // deep solemn blue
			AccentSecondary: "#64748b", // muted slate
			AccentHover:     "#334155",
			AccentBg:        "rgba(30, 41, 59, 0.1)",
		},
		Dark: ThemeColors{
			BgPrimary:       "#0f172a", // dark navy
			BgSecondary:     "#1e293b",
			TextPrimary:     "#f8fafc",
			TextSecondary:   "#cbd5e1",
			TextMuted:       "#64748b",
			BorderColor:     "#334155",
			AccentPrimary:   "#94a3b8", // muted slate blue
			AccentSecondary: "#64748b",
			AccentHover:     "#cbd5e1",
			AccentBg:        "rgba(100, 116, 139, 0.2)",
		},
	},
	{
		Name:        "pearl-harbor-remembrance-day",
		DefaultMode: "light",
		Custom:      false,
		Schedule:    &ThemeSchedule{StartMonth: 12, StartDay: 7, EndMonth: 12, EndDay: 10},
		Light: ThemeColors{
			BgPrimary:       "#f8fafc",
			BgSecondary:     "#ffffff",
			TextPrimary:     "#0f172a",
			TextSecondary:   "#334155",
			TextMuted:       "#64748b",
			BorderColor:     "#e2e8f0",
			AccentPrimary:   "#1e293b", // solemn navy
			AccentSecondary: "#475569",
			AccentHover:     "#334155",
			AccentBg:        "rgba(30, 41, 59, 0.1)",
		},
		Dark: ThemeColors{
			BgPrimary:       "#0f172a",
			BgSecondary:     "#1e293b",
			TextPrimary:     "#f8fafc",
			TextSecondary:   "#cbd5e1",
			TextMuted:       "#64748b",
			BorderColor:     "#334155",
			AccentPrimary:   "#94a3b8",
			AccentSecondary: "#64748b",
			AccentHover:     "#cbd5e1",
			AccentBg:        "rgba(100, 116, 139, 0.2)",
		},
	},
	{
		Name:        "allied-invasion-of-normandy-remembrance-day",
		DefaultMode: "light",
		Custom:      false,
		Schedule:    &ThemeSchedule{StartMonth: 6, StartDay: 5, EndMonth: 6, EndDay: 10},
		Light: ThemeColors{
			BgPrimary:       "#f8fafc",
			BgSecondary:     "#ffffff",
			TextPrimary:     "#0f172a",
			TextSecondary:   "#334155",
			TextMuted:       "#64748b",
			BorderColor:     "#e2e8f0",
			AccentPrimary:   "#1e3a8a", // deep patriotic blue
			AccentSecondary: "#94a3b8",
			AccentHover:     "#1d4ed8",
			AccentBg:        "rgba(30, 58, 138, 0.1)",
		},
		Dark: ThemeColors{
			BgPrimary:       "#0f172a",
			BgSecondary:     "#1e293b",
			TextPrimary:     "#f8fafc",
			TextSecondary:   "#cbd5e1",
			TextMuted:       "#64748b",
			BorderColor:     "#334155",
			AccentPrimary:   "#3b82f6", // bright blue
			AccentSecondary: "#94a3b8",
			AccentHover:     "#60a5fa",
			AccentBg:        "rgba(59, 130, 246, 0.2)",
		},
	},
	{
		Name:        "thanksgiving",
		DefaultMode: "light",
		Custom:      false,
		Schedule:    &ThemeSchedule{StartMonth: 11, StartDay: 20, EndMonth: 12, EndDay: 5},
		Light: ThemeColors{
			BgPrimary:       "#fffbeb", // warm cream
			BgSecondary:     "#ffffff",
			TextPrimary:     "#451a03", // deep brown
			TextSecondary:   "#78350f",
			TextMuted:       "#92400e",
			BorderColor:     "#fed7aa",
			AccentPrimary:   "#b45309", // burnt orange
			AccentSecondary: "#d97706", // gold
			AccentHover:     "#92400e",
			AccentBg:        "rgba(217, 119, 6, 0.1)",
		},
		Dark: ThemeColors{
			BgPrimary:       "#2d1a0e", // dark chocolate brown
			BgSecondary:     "#451a03",
			TextPrimary:     "#fafaf9",
			TextSecondary:   "#d6d3d1",
			TextMuted:       "#a8a29e",
			BorderColor:     "#44403c",
			AccentPrimary:   "#f59e0b", // amber orange
			AccentSecondary: "#ca8a04", // metallic gold
			AccentHover:     "#fbbf24",
			AccentBg:        "rgba(217, 119, 6, 0.2)",
		},
	}, {
		Name:        "halloween",
		DefaultMode: "light",
		Custom:      false,
		Schedule:    &ThemeSchedule{StartMonth: 10, StartDay: 15, EndMonth: 11, EndDay: 1},
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
		Schedule:    &ThemeSchedule{StartMonth: 12, StartDay: 1, EndMonth: 12, EndDay: 25},
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
			TextPrimary:     "#f0fdf4", // snow-white
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
		Schedule:    &ThemeSchedule{StartMonth: 12, StartDay: 26, EndMonth: 1, EndDay: 7},
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

// themeActiveToday checks if the given time falls inside the theme's annual date range.
func themeActiveToday(schedule *ThemeSchedule, now time.Time) bool {
	if schedule == nil {
		return false
	}

	month := int(now.Month())
	day := now.Day()

	current := month*100 + day
	start := schedule.StartMonth*100 + schedule.StartDay
	end := schedule.EndMonth*100 + schedule.EndDay

	if start <= end {
		return current >= start && current <= end
	}

	// Wrapping range (wraps over Dec 31 / Jan 1)
	return current >= start || current <= end
}

// ResolveScheduledTheme deterministically selects the active theme from the candidates.
func ResolveScheduledTheme(themes []Theme, now time.Time) string {
	var candidates []Theme
	for _, theme := range themes {
		if theme.Schedule != nil && themeActiveToday(theme.Schedule, now) {
			candidates = append(candidates, theme)
		}
	}

	if len(candidates) == 0 {
		return ""
	}
	if len(candidates) == 1 {
		return candidates[0].Name
	}

	// Overlap: deterministic date-based seed
	dateStr := now.Format("20060102")
	var hash int64
	for _, c := range dateStr {
		hash = 31*hash + int64(c)
	}

	index := hash % int64(len(candidates))
	if index < 0 {
		index = -index
	}
	return candidates[index].Name
}
