package server

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// saveLinkedFixture saves a target and n documents linking to it, alternating the two internal
// link forms, and returns the linkers' slugs.
func saveLinkedFixture(t *testing.T, srv *Server, target string, n int) []string {
	t.Helper()
	if _, err := srv.Storage.SaveArticle("", target, "# "+target, "", "", "", "", nil, ""); err != nil {
		t.Fatalf("SaveArticle(%q) failed: %v", target, err)
	}
	slugs := make([]string, 0, n)
	for i := 1; i <= n; i++ {
		body := fmt.Sprintf("See [[%s]].", target)
		if i%2 == 0 {
			body = fmt.Sprintf("See [it](/articles/%s).", Slugify(target))
		}
		art, err := srv.Storage.SaveArticle("", fmt.Sprintf("Referrer %02d", i), body, "", "", "", "", nil, "")
		if err != nil {
			t.Fatalf("SaveArticle failed: %v", err)
		}
		slugs = append(slugs, art.Slug)
	}
	return slugs
}

// TestDeleteArticleRefusesWhileLinked pins the guard that replaced get_backlinks as the pre-delete
// check: an agent no longer has to remember to look, because the delete looks for it. The refusal
// deletes nothing and names every linker — past read_article's old 15-entry prose cap — so the
// agent can fix them without a second call.
func TestDeleteArticleRefusesWhileLinked(t *testing.T) {
	srv := newMCPServer(t)
	linkers := saveLinkedFixture(t, srv, "Doomed Page", 18)
	before, err := srv.Storage.GetArticle("doomed-page")
	if err != nil {
		t.Fatalf("GetArticle failed: %v", err)
	}

	for _, call := range []string{
		`{"name":"delete_article","arguments":{"slug":"doomed-page"}}`,
		`{"name":"delete_article","arguments":{"slug":"doomed-page","break_links":false}}`,
	} {
		resp := toolCall(t, srv, call)
		if !resp.IsError {
			t.Fatalf("%s: expected a refusal, got %+v", call, resp)
		}
		text := resp.Content[0].Text
		for i, slug := range linkers {
			if want := fmt.Sprintf("Referrer %02d (%s)", i+1, slug); !strings.Contains(text, want) {
				t.Errorf("%s: refusal does not name %q: %s", call, want, text)
			}
		}
		for _, want := range []string{"nothing was deleted", "18 documents link to 'doomed-page'", "break_links: true"} {
			if !strings.Contains(text, want) {
				t.Errorf("%s: refusal does not say %q: %s", call, want, text)
			}
		}
	}

	after, err := srv.Storage.GetArticle("doomed-page")
	if err != nil {
		t.Fatalf("a refused delete removed the document: %v", err)
	}
	if after.Version != before.Version || after.Content != before.Content {
		t.Errorf("a refused delete changed the document: %+v -> %+v", before, after)
	}
}

// TestDeleteArticleBreakLinks pins the override: with break_links: true the delete goes ahead,
// and the response names the documents it left holding broken links, so the agent knows what to
// repair.
func TestDeleteArticleBreakLinks(t *testing.T) {
	srv := newMCPServer(t)
	linkers := saveLinkedFixture(t, srv, "Doomed Page", 3)

	resp := toolCall(t, srv, `{"name":"delete_article","arguments":{"slug":"doomed-page","break_links":true}}`)
	if resp.IsError {
		t.Fatalf("delete_article with break_links failed: %s", resp.Content[0].Text)
	}
	text := resp.Content[0].Text
	if !strings.Contains(text, "has been permanently deleted") || !strings.Contains(text, "These 3 documents now hold broken links to 'doomed-page'") {
		t.Errorf("success response does not report the broken links: %s", text)
	}
	for i, slug := range linkers {
		if want := fmt.Sprintf("Referrer %02d (%s)", i+1, slug); !strings.Contains(text, want) {
			t.Errorf("success response does not name %q: %s", want, text)
		}
	}
	if _, err := srv.Storage.GetArticle("doomed-page"); err == nil {
		t.Error("break_links: true should have deleted the document")
	}
}

// TestDeleteArticleRefusesOnIncompleteScan is #162 applied to the one irreversible caller an agent
// drives: an entry the scan could not read may link to the target, so an empty backlink list is
// not "nothing links here" and the delete refuses, naming what it skipped. break_links: true
// accepts the risk.
func TestDeleteArticleRefusesOnIncompleteScan(t *testing.T) {
	srv := newBacklinkSkipFixture(t)

	// spoke has no readable backlinks; only the unreadable broken.md stands in the way.
	resp := toolCall(t, srv, `{"name":"delete_article","arguments":{"slug":"spoke"}}`)
	if !resp.IsError {
		t.Fatalf("expected a refusal on an incomplete scan, got %+v", resp)
	}
	text := resp.Content[0].Text
	for _, want := range []string{"nothing was deleted", "inbound-link check was incomplete", "- broken.md (unreadable)", "break_links: true"} {
		if !strings.Contains(text, want) {
			t.Errorf("refusal does not say %q: %s", want, text)
		}
	}
	if strings.Contains(text, "link to 'spoke', and deleting") {
		t.Errorf("refusal claims readable linkers that do not exist: %s", text)
	}
	if _, err := srv.Storage.GetArticle("spoke"); err != nil {
		t.Fatalf("a refused delete removed the document: %v", err)
	}

	// hub-page has a readable linker and both kinds of skipped entry: all three are named.
	hub := toolCall(t, srv, `{"name":"delete_article","arguments":{"slug":"hub-page"}}`)
	if !hub.IsError {
		t.Fatalf("expected a refusal, got %+v", hub)
	}
	for _, want := range []string{"Spoke (spoke)", "- broken.md (unreadable)", "- linker-copy.md (misplaced, slug hub-linker)"} {
		if !strings.Contains(hub.Content[0].Text, want) {
			t.Errorf("refusal does not name %q: %s", want, hub.Content[0].Text)
		}
	}

	forced := toolCall(t, srv, `{"name":"delete_article","arguments":{"slug":"spoke","break_links":true}}`)
	if forced.IsError {
		t.Fatalf("break_links: true should proceed past an incomplete scan: %s", forced.Content[0].Text)
	}
	if !strings.Contains(forced.Content[0].Text, "inbound-link check was incomplete") {
		t.Errorf("success response should still say the check was incomplete: %s", forced.Content[0].Text)
	}
	if _, err := srv.Storage.GetArticle("spoke"); err == nil {
		t.Error("break_links: true should have deleted the document")
	}
}

// TestDeleteArticleUnlinkedAndSelfLinked pins the unchanged path: a document nothing else links to
// deletes exactly as before, and a link to itself does not count — scanBacklinks never reads a
// target's own file, so a self-link cannot hold its own deletion hostage.
func TestDeleteArticleUnlinkedAndSelfLinked(t *testing.T) {
	srv := newMCPServer(t)
	for _, doc := range []struct{ title, body string }{
		{"Lonely Page", "# Nobody links here."},
		{"Narcissus Page", "I link to [[Narcissus Page]] and [myself](/articles/narcissus-page)."},
	} {
		if _, err := srv.Storage.SaveArticle("", doc.title, doc.body, "", "", "", "", nil, ""); err != nil {
			t.Fatalf("SaveArticle(%q) failed: %v", doc.title, err)
		}
	}

	for _, slug := range []string{"lonely-page", "narcissus-page"} {
		resp := toolCall(t, srv, fmt.Sprintf(`{"name":"delete_article","arguments":{"slug":%q}}`, slug))
		if resp.IsError {
			t.Fatalf("delete_article(%s) failed: %s", slug, resp.Content[0].Text)
		}
		want := fmt.Sprintf("Success! Document with slug '%s' has been permanently deleted from disk along with all history backups and media assets.\n", slug)
		if resp.Content[0].Text != want {
			t.Errorf("delete_article(%s) = %q, want the unchanged success text %q", slug, resp.Content[0].Text, want)
		}
		if _, err := srv.Storage.GetArticle(slug); err == nil {
			t.Errorf("%s was not deleted", slug)
		}
	}
}

// TestDeleteArticleBreakLinksAfterScanError pins the override on the other incomplete case: a
// walk that fails outright refuses without break_links (as does the data-dir check in
// TestUnlistableArticleDirErrorsHideDataDir), and with it the delete proceeds and still says the
// check failed.
func TestDeleteArticleBreakLinksAfterScanError(t *testing.T) {
	srv := newMCPServer(t)
	captureLog(t)
	if _, err := srv.Storage.SaveArticle("", "Doomed Page", "# Doomed", "", "", "", "", nil, ""); err != nil {
		t.Fatalf("SaveArticle failed: %v", err)
	}
	lockListing(t, srv.Storage.ArticleDir, "doomed-page.md")

	refused := toolCall(t, srv, `{"name":"delete_article","arguments":{"slug":"doomed-page"}}`)
	if !refused.IsError || !strings.Contains(refused.Content[0].Text, "inbound-link check failed") {
		t.Fatalf("expected a refusal naming the failed check, got %+v", refused)
	}

	resp := toolCall(t, srv, `{"name":"delete_article","arguments":{"slug":"doomed-page","break_links":true}}`)
	if resp.IsError {
		t.Fatalf("break_links: true should proceed past a failed scan: %s", resp.Content[0].Text)
	}
	text := resp.Content[0].Text
	if !strings.Contains(text, "has been permanently deleted") || !strings.Contains(text, "inbound-link check failed") {
		t.Errorf("success response should report the deletion and the failed check: %s", text)
	}
	if strings.Contains(text, "Any of those may now hold a broken link") {
		t.Errorf("a failed scan has no list for \"those\" to refer to: %s", text)
	}
	if _, err := os.Stat(filepath.Join(srv.Storage.ArticleDir, "doomed-page.md")); !os.IsNotExist(err) {
		t.Errorf("doomed-page.md should be gone, stat err = %v", err)
	}
}
