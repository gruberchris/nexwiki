package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestThemeActiveToday(t *testing.T) {
	tests := []struct {
		name     string
		schedule *ThemeSchedule
		date     time.Time
		expected bool
	}{
		{"nil schedule", nil, time.Now(), false},
		{"normal range: inside", &ThemeSchedule{10, 15, 11, 1}, date(10, 20), true},
		{"normal range: on start", &ThemeSchedule{10, 15, 11, 1}, date(10, 15), true},
		{"normal range: on end", &ThemeSchedule{10, 15, 11, 1}, date(11, 1), true},
		{"normal range: before", &ThemeSchedule{10, 15, 11, 1}, date(9, 1), false},
		{"normal range: after", &ThemeSchedule{10, 15, 11, 1}, date(11, 2), false},
		{"wrapping range: in December", &ThemeSchedule{12, 26, 1, 7}, date(12, 31), true},
		{"wrapping range: in January", &ThemeSchedule{12, 26, 1, 7}, date(1, 3), true},
		{"wrapping range: on start", &ThemeSchedule{12, 26, 1, 7}, date(12, 26), true},
		{"wrapping range: on end", &ThemeSchedule{12, 26, 1, 7}, date(1, 7), true},
		{"wrapping range: outside", &ThemeSchedule{12, 26, 1, 7}, date(6, 15), false},
		{"christmas: Dec 15 inside", &ThemeSchedule{12, 1, 12, 25}, date(12, 15), true},
		{"christmas: Dec 26 outside", &ThemeSchedule{12, 1, 12, 25}, date(12, 26), false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := themeActiveToday(tc.schedule, tc.date)
			if got != tc.expected {
				t.Errorf("expected %v, got %v", tc.expected, got)
			}
		})
	}
}

func TestResolveScheduledTheme(t *testing.T) {
	// No themes with schedules
	noScheduled := []Theme{
		{Name: "default", Schedule: nil},
	}
	if got := ResolveScheduledTheme(noScheduled, date(12, 15)); got != "" {
		t.Errorf("no scheduled themes: expected '', got '%s'", got)
	}

	// One active theme
	oneActive := []Theme{
		{Name: "christmas", Schedule: &ThemeSchedule{12, 1, 12, 25}},
		{Name: "halloween", Schedule: &ThemeSchedule{10, 15, 11, 1}},
	}
	if got := ResolveScheduledTheme(oneActive, date(12, 15)); got != "christmas" {
		t.Errorf("one active: expected 'christmas', got '%s'", got)
	}
	if got := ResolveScheduledTheme(oneActive, date(10, 20)); got != "halloween" {
		t.Errorf("one active: expected 'halloween', got '%s'", got)
	}

	// No active themes
	if got := ResolveScheduledTheme(oneActive, date(6, 15)); got != "" {
		t.Errorf("none active: expected '', got '%s'", got)
	}

	// Multiple overlapping candidates: deterministic selection
	bothActive := []Theme{
		{Name: "alpha", Schedule: &ThemeSchedule{6, 1, 8, 31}},
		{Name: "beta", Schedule: &ThemeSchedule{6, 1, 8, 31}},
	}
	result1 := ResolveScheduledTheme(bothActive, date(7, 15))
	result2 := ResolveScheduledTheme(bothActive, date(7, 15))
	if result1 == "" {
		t.Error("multiple candidates: expected a theme name, got empty string")
	}
	if result1 != result2 {
		t.Errorf("multiple candidates: expected deterministic result, got %q and %q", result1, result2)
	}

	// DefaultThemes: christmas schedule starts Dec 1
	christmasDate := date(12, 15)
	result := ResolveScheduledTheme(DefaultThemes, christmasDate)
	if result != "christmas" {
		t.Errorf("default themes Dec 15: expected 'christmas', got '%s'", result)
	}
}

func TestThemeStoreSaveLoad(t *testing.T) {
	dir := t.TempDir()
	ts := NewThemeStore(dir)

	// Load from non-existent file returns empty slice
	themes, err := ts.LoadCustomThemes()
	if err != nil {
		t.Fatalf("LoadCustomThemes on missing file failed: %v", err)
	}
	if len(themes) != 0 {
		t.Errorf("expected empty slice, got %d themes", len(themes))
	}

	// Save custom themes
	customThemes := []Theme{
		{
			Name:        "test-theme",
			DefaultMode: "light",
			Custom:      true,
			Light:       ThemeColors{BgPrimary: "#ffffff"},
			Dark:        ThemeColors{BgPrimary: "#000000"},
		},
	}
	if err := ts.SaveCustomThemes(customThemes); err != nil {
		t.Fatalf("SaveCustomThemes failed: %v", err)
	}

	// File should exist
	if _, err := os.Stat(filepath.Join(dir, "custom_themes.json")); os.IsNotExist(err) {
		t.Error("custom_themes.json not created")
	}

	// Load returns the saved themes
	loaded, err := ts.LoadCustomThemes()
	if err != nil {
		t.Fatalf("LoadCustomThemes after save failed: %v", err)
	}
	if len(loaded) != 1 {
		t.Fatalf("expected 1 theme, got %d", len(loaded))
	}
	if loaded[0].Name != "test-theme" {
		t.Errorf("expected name 'test-theme', got '%s'", loaded[0].Name)
	}
	if loaded[0].Light.BgPrimary != "#ffffff" {
		t.Errorf("expected BgPrimary '#ffffff', got '%s'", loaded[0].Light.BgPrimary)
	}

	// Save empty slice clears the file
	if err := ts.SaveCustomThemes([]Theme{}); err != nil {
		t.Fatalf("SaveCustomThemes empty failed: %v", err)
	}
	loaded2, _ := ts.LoadCustomThemes()
	if len(loaded2) != 0 {
		t.Errorf("expected 0 themes after clearing, got %d", len(loaded2))
	}
}

func TestThemeStoreSaveLoadRoundtrip(t *testing.T) {
	dir := t.TempDir()
	ts := NewThemeStore(dir)

	// Save multiple themes and verify JSON round-trip fidelity
	themes := []Theme{
		{Name: "theme-a", DefaultMode: "light", Custom: true, Schedule: &ThemeSchedule{6, 28, 7, 6}},
		{Name: "theme-b", DefaultMode: "dark", Custom: true},
	}
	if err := ts.SaveCustomThemes(themes); err != nil {
		t.Fatalf("save failed: %v", err)
	}

	// Verify file is valid JSON
	data, _ := os.ReadFile(filepath.Join(dir, "custom_themes.json"))
	var raw []map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("saved file is not valid JSON: %v", err)
	}

	loaded, _ := ts.LoadCustomThemes()
	if len(loaded) != 2 {
		t.Fatalf("expected 2 themes, got %d", len(loaded))
	}
	if loaded[0].Name != "theme-a" || loaded[1].Name != "theme-b" {
		t.Errorf("theme names mismatch: %v", loaded)
	}
	if loaded[0].Schedule == nil {
		t.Error("expected schedule for theme-a to be preserved")
	}
}

// date builds a time.Time for a given month and day in year 2024.
func date(month, day int) time.Time {
	return time.Date(2024, time.Month(month), day, 12, 0, 0, 0, time.UTC)
}
