package server

import "testing"

func TestGetArticleDirectory(t *testing.T) {
	tests := []struct {
		name     string
		tags     []string
		expected string
	}{
		{"no tags", nil, "wiki"},
		{"empty tags", []string{}, "wiki"},
		{"regular tags", []string{"golang", "backend"}, "wiki"},
		{"aiagent-memory-rules", []string{"aiagent-memory-rules"}, "aimemories"},
		{"aiagent-memory-custom", []string{"aiagent-memory-custom"}, "aimemories"},
		{"aiagent-plan", []string{"aiagent-plan"}, "aiplans"},
		{"aiagent-skill", []string{"aiagent-skill"}, "aiskills"},
		{"mixed tags with memory first", []string{"aiagent-memory-rules", "aiagent-plan"}, "aimemories"},
		{"mixed tags with plan first", []string{"aiagent-plan", "aiagent-memory-rules"}, "aiplans"},
		{"uppercase tags normalized", []string{"AIAGENT-PLAN"}, "aiplans"},
		{"tag with regular prefix", []string{"someaiagent-plan", "notes"}, "wiki"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := getArticleDirectory(tc.tags)
			if got != tc.expected {
				t.Errorf("expected %q, got %q", tc.expected, got)
			}
		})
	}
}
