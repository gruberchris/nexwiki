package server

import "testing"

func TestGetArticleDirectory(t *testing.T) {
	tests := []struct {
		name        string
		articleType string
		expected    string
	}{
		{"empty type defaults to wiki", "", "wiki"},
		{"wiki", ContentTypeWiki, "wiki"},
		{"memory", ContentTypeMemory, "aimemories"},
		{"plan", ContentTypePlan, "aiplans"},
		{"skill", ContentTypeSkill, "aiskills"},
		{"lowercase memory normalized", "ai-agent-memory", "aimemories"},
		{"lowercase plan normalized", "ai-agent-plan", "aiplans"},
		{"unknown type defaults to wiki", "Something-Else", "wiki"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := getArticleDirectory(tc.articleType)
			if got != tc.expected {
				t.Errorf("expected %q, got %q", tc.expected, got)
			}
		})
	}
}
