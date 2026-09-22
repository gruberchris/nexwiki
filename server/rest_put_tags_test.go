package server

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
)

// TestHandleUpdateArticleTagsOmittedMeansPreserve pins the REST PUT tag rule: an omitted or null
// "tags" key keeps the current set, [] clears it, and a list replaces it. A status-only PUT used
// to wipe every tag because the handler always passed the decoded (nil) slice to the edit.
func TestHandleUpdateArticleTagsOmittedMeansPreserve(t *testing.T) {
	srv := newTestServer(t)
	if _, err := srv.Storage.SaveArticle("", "Tagged Page", "# body", "", "", "", "", []string{"alpha", "beta"}, ""); err != nil {
		t.Fatalf("seed: %v", err)
	}

	steps := []struct {
		name string
		tags string // the "tags" member of the body, or "" to omit the key entirely
		want []string
	}{
		{"omitted key preserves", "", []string{"alpha", "beta"}},
		{"null preserves", `"tags": null,`, []string{"alpha", "beta"}},
		{"list replaces", `"tags": ["gamma"],`, []string{"gamma"}},
		{"empty array clears", `"tags": [],`, nil},
	}
	for _, step := range steps {
		cur, err := srv.Storage.GetArticle("tagged-page")
		if err != nil {
			t.Fatalf("%s: read before: %v", step.name, err)
		}
		body := `{"title": "Tagged Page", "content": "# body", ` + step.tags + ` "loaded_version": ` + itoa(cur.Version) + `}`
		req := httptest.NewRequest("PUT", "/api/articles/tagged-page", strings.NewReader(body))
		req.SetPathValue("slug", "tagged-page")
		w := httptest.NewRecorder()
		srv.HandleUpdateArticle(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("%s: expected 200, got %d: %s", step.name, w.Code, w.Body.String())
		}
		after, err := srv.Storage.GetArticle("tagged-page")
		if err != nil {
			t.Fatalf("%s: read after: %v", step.name, err)
		}
		if after.Version != cur.Version+1 {
			t.Fatalf("%s: the PUT did not write (version %d → %d)", step.name, cur.Version, after.Version)
		}
		got := slices.Clone(after.Tags)
		slices.Sort(got)
		if len(got) == 0 {
			got = nil
		}
		if !slices.Equal(got, step.want) {
			t.Errorf("%s: tags = %v, want %v", step.name, after.Tags, step.want)
		}
	}
}
