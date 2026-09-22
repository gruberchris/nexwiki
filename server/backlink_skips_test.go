package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// newBacklinkSkipFixture builds a wiki whose hub has one readable referrer (spoke) plus one
// unreadable file and one misplaced document that each link to it — the state #162 describes,
// where a backlink answer is confidently incomplete unless the tool says what the scan skipped.
func newBacklinkSkipFixture(t *testing.T) *Server {
	t.Helper()
	srv := newMCPServer(t)

	for _, doc := range []struct{ title, body string }{
		{"Hub Page", "# Hub"},
		{"Spoke", "Points at [[Hub Page]]."},
	} {
		if _, err := srv.Storage.SaveArticle("", doc.title, doc.body, "", "", "", "", nil, ""); err != nil {
			t.Fatalf("SaveArticle(%q) failed: %v", doc.title, err)
		}
	}

	old := time.Now().Add(-time.Hour)
	// Unreadable: malformed front matter, so no scan can say whether it links to the hub.
	writeWithMtime(t, filepath.Join(srv.Storage.ArticleDir, "broken.md"),
		[]byte("---\ntitle: [unclosed\n---\nLinks [[Hub Page]]\n"), old)
	// Misplaced: parses and links to the hub, but its filename does not match its declared slug.
	writeArticleFile(t, srv.Storage, "linker-copy.md", "title: Hub Linker\nslug: hub-linker\n", "Links [[Hub Page]] too.\n", old)
	return srv
}

// TestReadArticleReportsSkippedEntries pins the #162 indicator on read_article, the tool that
// answers "what links here" since get_backlinks was retired: the unreadable and misplaced entries
// the scan could not count as backlinks are reported beside the list, in the structured output and
// the prose, with a count that matches the scan and entries that carry a reason.
func TestReadArticleReportsSkippedEntries(t *testing.T) {
	srv := newBacklinkSkipFixture(t)

	read := toolCall(t, srv, `{"name":"read_article","arguments":{"slug":"hub-page"}}`)
	if read.IsError {
		t.Fatalf("read_article failed: %s", read.Content[0].Text)
	}
	var out ArticleOutput
	decodeStructured(t, read, &out)

	// The readable referrer is still a backlink; the skips are reported beside it, not instead.
	if len(out.Backlinks) != 1 || out.Backlinks[0].Slug != "spoke" {
		t.Errorf("backlinks = %+v, want one from spoke", out.Backlinks)
	}

	// The count matches what the scan itself recorded, and the list is the helpers' own.
	scan, err := srv.Storage.scanBacklinks("hub-page")
	if err != nil {
		t.Fatalf("scanBacklinks failed: %v", err)
	}
	if len(scan.unreadable) != 1 || len(scan.misplaced) != 1 {
		t.Fatalf("fixture lost its skips: %+v", scan)
	}
	if want := skippedDocumentCount(scan); out.SkippedDocumentCount != want {
		t.Errorf("skipped_document_count = %d, want %d", out.SkippedDocumentCount, want)
	}
	if want := skippedDocuments(scan); !reflect.DeepEqual(out.SkippedDocuments, want) {
		t.Errorf("skipped_documents = %+v, the helper built %+v", out.SkippedDocuments, want)
	}

	// The entries carry their reason, sorted by path: broken.md before linker-copy.md.
	if len(out.SkippedDocuments) != 2 {
		t.Fatalf("skipped_documents = %+v, want the unreadable file and the misplaced document", out.SkippedDocuments)
	}
	if out.SkippedDocuments[0].Path != "broken.md" || out.SkippedDocuments[0].Reason != "unreadable" || out.SkippedDocuments[0].Slug != "" {
		t.Errorf("unreadable entry = %+v, want path broken.md, reason unreadable, no slug", out.SkippedDocuments[0])
	}
	if out.SkippedDocuments[1].Path != "linker-copy.md" || out.SkippedDocuments[1].Reason != "misplaced" || out.SkippedDocuments[1].Slug != "hub-linker" {
		t.Errorf("misplaced entry = %+v, want path linker-copy.md, reason misplaced, slug hub-linker", out.SkippedDocuments[1])
	}

	// The note rides in the Linked from block when there is one to ride in, and points where the
	// detail lives.
	if !strings.Contains(read.Content[0].Text, "Linked from: Spoke (spoke)") {
		t.Errorf("read_article missing Linked from section: %s", read.Content[0].Text)
	}
	if !strings.Contains(read.Content[0].Text, "Note: the backlink scan skipped 1 unreadable entry and 1 misplaced document") ||
		!strings.Contains(read.Content[0].Text, "wiki_health lists them in detail") {
		t.Errorf("read_article missing the skipped-entries note: %s", read.Content[0].Text)
	}

	// An article with no readable backlinks still gets the indicator — that is the case the
	// indicator exists for. The unreadable file is reported against every target (it may link to
	// anything); the misplaced document only against the slug it links to.
	spoke := toolCall(t, srv, `{"name":"read_article","arguments":{"slug":"spoke"}}`)
	if spoke.IsError {
		t.Fatalf("read_article(spoke) failed: %s", spoke.Content[0].Text)
	}
	var spokeOut ArticleOutput
	decodeStructured(t, spoke, &spokeOut)
	if spokeOut.SkippedDocumentCount != 1 || len(spokeOut.SkippedDocuments) != 1 ||
		spokeOut.SkippedDocuments[0].Path != "broken.md" || spokeOut.SkippedDocuments[0].Reason != "unreadable" {
		t.Errorf("spoke skipped indicator = %d %+v, want only the unreadable entry", spokeOut.SkippedDocumentCount, spokeOut.SkippedDocuments)
	}
	if strings.Contains(spoke.Content[0].Text, "Linked from:") {
		t.Errorf("spoke has no readable backlinks and should carry no Linked from section: %s", spoke.Content[0].Text)
	}
	if !strings.Contains(spoke.Content[0].Text, "Note: the backlink scan skipped 1 unreadable entry") {
		t.Errorf("spoke missing the skipped-entries note: %s", spoke.Content[0].Text)
	}
}

// TestReadArticleOmitsSkippedIndicatorOnCleanCorpus pins the other half of the contract: on a
// corpus with nothing skipped, the indicator is absent, not zero-filled, so a clean wiki's
// responses are byte-for-byte what they always were.
func TestReadArticleOmitsSkippedIndicatorOnCleanCorpus(t *testing.T) {
	srv := newMCPServer(t)
	for _, doc := range []struct{ title, body string }{
		{"Hub Page", "# Hub"},
		{"Spoke", "Points at [[Hub Page]]."},
	} {
		if _, err := srv.Storage.SaveArticle("", doc.title, doc.body, "", "", "", "", nil, ""); err != nil {
			t.Fatalf("SaveArticle(%q) failed: %v", doc.title, err)
		}
	}

	for _, call := range []string{
		`{"name":"read_article","arguments":{"slug":"hub-page"}}`,
		`{"name":"read_article","arguments":{"slug":"spoke"}}`,
	} {
		resp := toolCall(t, srv, call)
		if resp.IsError {
			t.Fatalf("%s failed: %s", call, resp.Content[0].Text)
		}
		encoded, err := json.Marshal(resp.StructuredContent)
		if err != nil {
			t.Fatalf("%s: structuredContent does not serialize: %v", call, err)
		}
		if strings.Contains(string(encoded), "skipped_document") {
			t.Errorf("%s reported skipped entries on a clean corpus: %s", call, encoded)
		}
		if strings.Contains(resp.Content[0].Text, "Note: the backlink scan skipped") {
			t.Errorf("%s wrote a skipped-entries note on a clean corpus: %s", call, resp.Content[0].Text)
		}
	}
}

// TestBacklinksSkippedEntriesAreCapped pins the cap: past maxSkippedDocuments the list stops but
// the count stays complete, so truncation costs no information.
func TestBacklinksSkippedEntriesAreCapped(t *testing.T) {
	srv := newMCPServer(t)
	if _, err := srv.Storage.SaveArticle("", "Hub Page", "# Hub", "", "", "", "", nil, ""); err != nil {
		t.Fatalf("SaveArticle failed: %v", err)
	}

	old := time.Now().Add(-time.Hour)
	for i := 0; i < maxSkippedDocuments+5; i++ {
		writeWithMtime(t, filepath.Join(srv.Storage.ArticleDir, fmt.Sprintf("broken-%02d.md", i)),
			[]byte("---\ntitle: [unclosed\n---\nLinks [[Hub Page]]\n"), old)
	}

	resp := toolCall(t, srv, `{"name":"read_article","arguments":{"slug":"hub-page"}}`)
	if resp.IsError {
		t.Fatalf("read_article failed: %s", resp.Content[0].Text)
	}
	var out ArticleOutput
	decodeStructured(t, resp, &out)

	if want := maxSkippedDocuments + 5; out.SkippedDocumentCount != want {
		t.Errorf("skipped_document_count = %d, want %d", out.SkippedDocumentCount, want)
	}
	if len(out.SkippedDocuments) != maxSkippedDocuments {
		t.Fatalf("skipped_documents holds %d entries, want the cap of %d", len(out.SkippedDocuments), maxSkippedDocuments)
	}
	// The cap keeps the first entries by path, so the report is stable across runs.
	if out.SkippedDocuments[0].Path != "broken-00.md" || out.SkippedDocuments[maxSkippedDocuments-1].Path != "broken-19.md" {
		t.Errorf("cap did not keep the first entries by path: %s … %s", out.SkippedDocuments[0].Path, out.SkippedDocuments[maxSkippedDocuments-1].Path)
	}
	// The count is the total, so the truncation is derivable, and the prose names it too.
	if !strings.Contains(resp.Content[0].Text, fmt.Sprintf("skipped %d unreadable", maxSkippedDocuments+5)) {
		t.Errorf("prose count disagrees with the total: %s", resp.Content[0].Text)
	}
}

// TestRESTBacklinksResponseUnchangedWithSkips pins the design decision: #162 touches the MCP
// tools only, and the REST backlinks handler answers exactly what it always did even while the
// scan is skipping entries — the plain article array, no skipped-entries fields.
func TestRESTBacklinksResponseUnchangedWithSkips(t *testing.T) {
	srv := newBacklinkSkipFixture(t)

	req := httptest.NewRequest("GET", "/api/articles/hub-page/backlinks", nil)
	req.SetPathValue("slug", "hub-page")
	w := httptest.NewRecorder()
	srv.HandleGetBacklinks(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	body := strings.TrimSpace(w.Body.String())
	if !strings.HasPrefix(body, "[") || !strings.Contains(body, `"slug":"spoke"`) {
		t.Errorf("expected the plain article array with spoke, got: %s", body)
	}
	var articles []map[string]interface{}
	if err := json.Unmarshal([]byte(body), &articles); err != nil {
		t.Fatalf("backlinks response is not a JSON array of articles: %v", err)
	}
	if len(articles) != 1 {
		t.Errorf("expected only spoke, got %d articles: %s", len(articles), body)
	}
	for _, key := range []string{"skipped_document_count", "skipped_documents", "unreadable", "misplaced"} {
		if strings.Contains(body, key) {
			t.Errorf("REST backlinks response gained %q — the skipped-entries indicator is MCP-only: %s", key, body)
		}
		for _, field := range articles[0] {
			if s, ok := field.(string); ok && (s == key) {
				t.Errorf("REST backlinks response carries %q as a value", key)
			}
		}
	}
}
