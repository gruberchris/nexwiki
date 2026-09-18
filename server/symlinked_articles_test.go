package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"
)

// symlinkOrSkip links link to target, skipping the test where symlinks cannot be created (Windows
// without the privilege, or a filesystem without them).
func symlinkOrSkip(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
}

// linkedDoc is a document declaring slug linked-doc, for a file symlinked into the article directory.
func linkedDoc(description, body string) []byte {
	return []byte("---\ntitle: Linked Doc\nslug: linked-doc\ndescription: " + description + "\n---\n" + body + "\n")
}

// listedSlugs returns the slugs ListArticles reports, sorted.
func listedSlugs(t *testing.T, storage *Storage) []string {
	t.Helper()
	articles, err := storage.ListArticles()
	if err != nil {
		t.Fatalf("ListArticles failed: %v", err)
	}
	slugs := make([]string, 0, len(articles))
	for _, a := range articles {
		slugs = append(slugs, a.Slug)
	}
	sort.Strings(slugs)
	return slugs
}

// hiddenForms returns dir and, where it differs, the form a symlink above it resolves to (/var on
// macOS), for checking that neither reaches a client.
func hiddenForms(t *testing.T, dir string) []string {
	t.Helper()
	forms := []string{dir}
	if resolved, err := filepath.EvalSymlinks(dir); err == nil && resolved != dir {
		forms = append(forms, resolved)
	}
	return forms
}

// searchHitsSlug reports whether a search result set names the slug.
func searchHitsSlug(hits []SearchResult, slug string) bool {
	for _, hit := range hits {
		if hit.Slug == slug {
			return true
		}
	}
	return false
}

// TestSymlinkedArticleDirectory pins that an article directory that is a symlink to a directory works
// like a real one. filepath.WalkDir does not follow a symlinked root, so every walk used to see only
// the link: listings, the link graph, and backlinks came back empty, and the boot index sync dropped
// every search entry as an orphan.
func TestSymlinkedArticleDirectory(t *testing.T) {
	dataDir := t.TempDir()
	storage, err := NewStorage(dataDir)
	if err != nil {
		t.Fatalf("NewStorage failed: %v", err)
	}
	if _, err := storage.SaveArticle("", "Target Page", "the target", "", "", "", "seed", nil, ""); err != nil {
		t.Fatalf("SaveArticle failed: %v", err)
	}
	if _, err := storage.SaveArticle("", "Linker", "See [[Target Page]] and ![d](/api/assets/target-page/d.png).", "", "", "", "seed", nil, ""); err != nil {
		t.Fatalf("SaveArticle failed: %v", err)
	}
	if err := storage.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	// Move the article directory elsewhere and link it back in its place.
	elsewhere := filepath.Join(t.TempDir(), "moved-articles")
	articleDir := filepath.Join(dataDir, "articles")
	if err := os.Rename(articleDir, elsewhere); err != nil {
		t.Fatalf("Rename failed: %v", err)
	}
	symlinkOrSkip(t, elsewhere, articleDir)

	buf := captureLog(t)
	storage, err = NewStorage(dataDir)
	if err != nil {
		t.Fatalf("NewStorage over a symlinked article directory failed: %v", err)
	}
	t.Cleanup(func() { _ = storage.Close() })

	for _, slug := range []string{"home", "target-page", "linker"} {
		if !inSearchIndex(t, storage, slug) {
			t.Errorf("the boot index sync dropped %s from the search index", slug)
		}
	}
	// A slug lookup always resolved through the root link; pin that serving a page keeps working
	// alongside the walks that now descend it.
	if art, err := storage.GetArticle("target-page"); err != nil || art.Title != "Target Page" {
		t.Errorf("GetArticle through the symlinked root = %+v (err %v)", art, err)
	}
	if got, want := listedSlugs(t, storage), []string{"linker", "target-page"}; !reflect.DeepEqual(got, want) {
		t.Errorf("ListArticles = %v, want %v", got, want)
	}
	graph, err := storage.ScanLinkGraph()
	if err != nil {
		t.Fatalf("ScanLinkGraph failed: %v", err)
	}
	if len(graph.Meta) != 3 || graph.InboundCount["target-page"] != 1 {
		t.Errorf("link graph over the symlinked directory: %d documents and %d inbound to target-page, want 3 and 1",
			len(graph.Meta), graph.InboundCount["target-page"])
	}
	if backlinks, err := storage.GetBacklinks("target-page"); err != nil || len(backlinks) != 1 || backlinks[0].Slug != "linker" {
		t.Errorf("GetBacklinks = %v (err %v), want linker", backlinks, err)
	}
	if referrers, _, err := storage.findAssetReferrers("target-page"); err != nil || !reflect.DeepEqual(referrers, []string{"linker"}) {
		t.Errorf("findAssetReferrers = %v (err %v), want linker", referrers, err)
	}

	// A save writes through the link, and the walks find what it wrote.
	if _, err := storage.SaveArticle("", "Saved Through Link", "body", "", "", "", "seed", nil, ""); err != nil {
		t.Fatalf("SaveArticle failed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(elsewhere, "saved-through-link.md")); err != nil {
		t.Errorf("the save did not land in the linked directory: %v", err)
	}
	if got, want := listedSlugs(t, storage), []string{"linker", "saved-through-link", "target-page"}; !reflect.DeepEqual(got, want) {
		t.Errorf("ListArticles after a save = %v, want %v", got, want)
	}

	t.Run("wiki_health names files relative to the article directory", func(t *testing.T) {
		writeWithMtime(t, filepath.Join(articleDir, "sub", "nested.md"),
			[]byte("---\ntitle: Nested\nslug: nested\n---\nbody\n"), time.Now().Add(-time.Hour))
		locked := filepath.Join(articleDir, "locked.md")
		writeWithMtime(t, locked, []byte("---\ntitle: Locked\nslug: locked\n---\nbody\n"), time.Now().Add(-time.Hour))
		makeUnreadable(t, locked)

		srv := NewServer(storage, "Test Wiki", "light", false, NewEventBus(), "1.0.0", "")
		resp := toolCall(t, srv, `{"name":"wiki_health","arguments":{}}`)
		var out HealthOutput
		decodeStructured(t, resp, &out)
		if want := []UnreadableFile{{Path: "locked.md", Error: "open locked.md: permission denied"}}; !reflect.DeepEqual(out.UnreadableFiles, want) {
			t.Errorf("UnreadableFiles = %+v, want %+v", out.UnreadableFiles, want)
		}
		if len(out.MisplacedDocuments) != 1 || out.MisplacedDocuments[0].Path != "sub/nested.md" {
			t.Errorf("MisplacedDocuments = %+v, want sub/nested.md", out.MisplacedDocuments)
		}
		encoded, err := json.Marshal(resp)
		if err != nil {
			t.Fatalf("marshal failed: %v", err)
		}
		for _, dir := range append(hiddenForms(t, dataDir), hiddenForms(t, elsewhere)...) {
			if strings.Contains(string(encoded), dir) {
				t.Errorf("wiki_health reveals %q:\n%s", dir, encoded)
			}
		}
	})

	// Entries directly under a symlinked root are stat'd with the separator the walk added doubled
	// (dir//home.md), and a client must still see the entry named as it would be in a real directory.
	t.Run("a symlinked directory that cannot be searched fails scans naming the entry", func(t *testing.T) {
		lockSearch(t, elsewhere)
		srv := NewServer(storage, "Test Wiki", "light", false, NewEventBus(), "1.0.0", "")
		resp := toolCall(t, srv, `{"name":"get_wiki_statistics","arguments":{}}`)
		want := "failed to list articles: article directory is not searchable: lstat home.md: permission denied"
		if !resp.IsError || len(resp.Content) != 1 || resp.Content[0].Text != want {
			t.Errorf("get_wiki_statistics: got %+v, want an error reading %q", resp, want)
		}
	})

	// A link whose directory is gone is a broken data directory, not an empty wiki: the lstat of the
	// root used to succeed on the link itself, and the scans listed nothing.
	t.Run("a dangling article directory link fails scans", func(t *testing.T) {
		if err := os.RemoveAll(elsewhere); err != nil {
			t.Fatalf("RemoveAll failed: %v", err)
		}
		if _, err := storage.ListArticles(); err == nil {
			t.Error("ListArticles: expected an error")
		}
		if _, err := storage.ScanLinkGraph(); err == nil {
			t.Error("ScanLinkGraph: expected an error")
		}
		if _, err := storage.GetBacklinks("target-page"); err == nil {
			t.Error("GetBacklinks: expected an error")
		}
		if err := storage.SyncSearchIndex(); err == nil {
			t.Error("SyncSearchIndex: expected an error")
		}
		if !inSearchIndex(t, storage, "target-page") {
			t.Error("a failed listing must leave the search index alone")
		}
		// The error names the directory as configured, not with the separator the walk added, so the
		// client sees it as "." rather than as an empty path.
		srv := NewServer(storage, "Test Wiki", "light", false, NewEventBus(), "1.0.0", "")
		resp := toolCall(t, srv, `{"name":"get_wiki_statistics","arguments":{}}`)
		if !resp.IsError || len(resp.Content) != 1 || !strings.HasPrefix(resp.Content[0].Text, "failed to list articles: ") ||
			!strings.Contains(resp.Content[0].Text, " .: ") {
			t.Errorf("get_wiki_statistics: got %+v, want an error naming the article directory as .", resp)
		}
	})

	if strings.Contains(buf.String(), "ignores case") {
		t.Errorf("case detection should probe through the link like any other directory:\n%s", buf)
	}
	if strings.Contains(buf.String(), "moved-articles") {
		t.Errorf("warnings should name article files relative to the article directory:\n%s", buf)
	}
}

// TestSymlinkedArticleFileFollowsItsTarget pins that an article file symlinked in from elsewhere is
// fingerprinted by its target. The link's own modification time and size never change when the
// target is edited, so a walk that fingerprinted the link kept serving the old metadata and links,
// whether the edit came from outside or from a NexWiki save, which writes through the link. Search
// follows with the boot index sync, which lists and re-reads what the walks fingerprint.
func TestSymlinkedArticleFileFollowsItsTarget(t *testing.T) {
	storage, err := NewStorage(t.TempDir())
	if err != nil {
		t.Fatalf("NewStorage failed: %v", err)
	}
	t.Cleanup(func() { _ = storage.Close() })
	if _, err := storage.SaveArticle("", "Target Page", "the target", "", "", "", "seed", nil, ""); err != nil {
		t.Fatalf("SaveArticle failed: %v", err)
	}

	// The target's own name does not matter: the link is what sits at articles/<slug>.md.
	target := filepath.Join(t.TempDir(), "notes.txt")
	link := filepath.Join(storage.ArticleDir, "linked-doc.md")
	writeWithMtime(t, target, linkedDoc("first", "orchid notes, no links yet"), time.Now().Add(-2*time.Hour))
	symlinkOrSkip(t, target, link)
	buf := captureLog(t)

	check := func(stage, wantDescription string, wantBacklink bool, freshTerm, staleTerm string) {
		t.Helper()
		articles, err := storage.ListArticles()
		if err != nil {
			t.Fatalf("%s: ListArticles failed: %v", stage, err)
		}
		found := false
		for _, a := range articles {
			if a.Slug == "linked-doc" {
				found = true
				if a.Description != wantDescription {
					t.Errorf("%s: listed description = %q, want %q", stage, a.Description, wantDescription)
				}
			}
		}
		if !found {
			t.Errorf("%s: linked-doc is not listed", stage)
		}

		backlinks, err := storage.GetBacklinks("target-page")
		if err != nil {
			t.Fatalf("%s: GetBacklinks failed: %v", stage, err)
		}
		if got := len(backlinks) == 1 && backlinks[0].Slug == "linked-doc"; got != wantBacklink || (!wantBacklink && len(backlinks) != 0) {
			t.Errorf("%s: backlinks to target-page = %v, want linked-doc: %v", stage, backlinks, wantBacklink)
		}

		graph, err := storage.ScanLinkGraph()
		if err != nil {
			t.Fatalf("%s: ScanLinkGraph failed: %v", stage, err)
		}
		wantInbound := 0
		if wantBacklink {
			wantInbound = 1
		}
		if got := graph.InboundCount["target-page"]; got != wantInbound {
			t.Errorf("%s: InboundCount[target-page] = %d, want %d", stage, got, wantInbound)
		}
		if got := graph.Meta["linked-doc"].Description; got != wantDescription {
			t.Errorf("%s: graph description = %q, want %q", stage, got, wantDescription)
		}
		if len(graph.Unreadable) != 0 || len(graph.Misplaced) != 0 {
			t.Errorf("%s: a symlink at articles/<slug>.md is canonical and readable, got unreadable %v and misplaced %v",
				stage, graph.Unreadable, graph.Misplaced)
		}

		// The boot index sync is the refresh search gets for an edit made outside the app, and it
		// starts from a walk: the listing must still name the symlinked document — or the sync
		// drops its index entry as an orphan — and the document it then re-reads is the edited
		// target, so search serves the version the walks now fingerprint.
		if err := storage.SyncSearchIndex(); err != nil {
			t.Fatalf("%s: SyncSearchIndex failed: %v", stage, err)
		}
		if hits, err := storage.SearchArticles(freshTerm); err != nil {
			t.Fatalf("%s: search for %q failed: %v", stage, freshTerm, err)
		} else if !searchHitsSlug(hits, "linked-doc") {
			t.Errorf("%s: search for %q does not find linked-doc: %+v", stage, freshTerm, hits)
		}
		if hits, err := storage.SearchArticles(staleTerm); err != nil {
			t.Fatalf("%s: search for %q failed: %v", stage, staleTerm, err)
		} else if searchHitsSlug(hits, "linked-doc") {
			t.Errorf("%s: search for %q still finds the version before the edit", stage, staleTerm)
		}
		if !inSearchIndex(t, storage, "linked-doc") {
			t.Errorf("%s: the index sync dropped linked-doc from the search index", stage)
		}
	}

	check("before any edit", "first", false, "orchid", "kestrel")

	writeWithMtime(t, target, linkedDoc("second", "meridian notes, now links to [[Target Page]]"), time.Now().Add(-time.Hour))
	check("after an external edit to the target", "second", true, "meridian", "orchid")

	if _, err := storage.SaveArticle("linked-doc", "Linked Doc", "kestrel notes, links nowhere now", "third", "", "", "edit", nil, ""); err != nil {
		t.Fatalf("SaveArticle through the link failed: %v", err)
	}
	if info, err := os.Lstat(link); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("the save should write through the link and leave it in place (lstat %v, err %v)", info, err)
	}
	check("after a NexWiki save through the link", "third", false, "kestrel", "meridian")

	if strings.Contains(buf.String(), "linked-doc.md") {
		t.Errorf("a readable, canonical symlinked document must not be warned about:\n%s", buf)
	}
}

// TestSymlinkedArticleFileCacheDoesNotThrash pins that the lookups and the walks share one cache entry
// for a symlinked file. metaBySlug stats through the link and the walks used to stat the link itself,
// so each call found the other's fingerprint under the same key and re-parsed the file.
func TestSymlinkedArticleFileCacheDoesNotThrash(t *testing.T) {
	storage, err := NewStorage(t.TempDir())
	if err != nil {
		t.Fatalf("NewStorage failed: %v", err)
	}
	t.Cleanup(func() { _ = storage.Close() })
	if _, err := storage.SaveArticle("", "Target Page", "the target", "", "", "", "seed", nil, ""); err != nil {
		t.Fatalf("SaveArticle failed: %v", err)
	}
	target := filepath.Join(t.TempDir(), "linked-doc.md")
	link := filepath.Join(storage.ArticleDir, "linked-doc.md")
	writeWithMtime(t, target, linkedDoc("cached", "links to [[Target Page]]"), time.Now().Add(-time.Hour))
	symlinkOrSkip(t, target, link)

	parses := 0
	storage.cache.parseHook = func(path string) {
		if path == link {
			parses++
		}
	}
	for i := 0; i < 3; i++ {
		if meta, err := storage.metaBySlug("linked-doc"); err != nil || meta.Description != "cached" {
			t.Fatalf("metaBySlug = %+v (err %v)", meta, err)
		}
		if _, err := storage.ListArticles(); err != nil {
			t.Fatalf("ListArticles failed: %v", err)
		}
		if _, err := storage.GetBacklinks("target-page"); err != nil {
			t.Fatalf("GetBacklinks failed: %v", err)
		}
		if _, err := storage.ScanLinkGraph(); err != nil {
			t.Fatalf("ScanLinkGraph failed: %v", err)
		}
	}
	if parses != 1 {
		t.Errorf("the symlinked file was parsed %d times across alternating lookups and scans, want once", parses)
	}
}

// TestSymlinkedArticleFileWarnsOncePerTargetVersion pins that every place that records a failure
// fingerprints a symlinked file by its target: the walks and the boot index sync agree on one warning
// for a broken target, and an edit to the target is a new version that warns again.
func TestSymlinkedArticleFileWarnsOncePerTargetVersion(t *testing.T) {
	storage, err := NewStorage(t.TempDir())
	if err != nil {
		t.Fatalf("NewStorage failed: %v", err)
	}
	t.Cleanup(func() { _ = storage.Close() })

	// home is the one document the boot index sync loads without a listing having vouched for it, so
	// the sync reaches a broken home.md on its own.
	homePath := filepath.Join(storage.ArticleDir, "home.md")
	if err := os.Remove(homePath); err != nil {
		t.Fatalf("Remove failed: %v", err)
	}
	target := filepath.Join(t.TempDir(), "home-target.md")
	writeWithMtime(t, target, []byte("---\ntitle: [unclosed\n---\nbody\n"), time.Now().Add(-2*time.Hour))
	symlinkOrSkip(t, target, homePath)
	buf := captureLog(t)

	scanAll := func() {
		t.Helper()
		graph, err := storage.ScanLinkGraph()
		if err != nil {
			t.Fatalf("ScanLinkGraph failed: %v", err)
		}
		if len(graph.Unreadable) != 1 || graph.Unreadable[0].Path != "home.md" {
			t.Errorf("Unreadable = %+v, want home.md", graph.Unreadable)
		}
		if err := storage.SyncSearchIndex(); err != nil {
			t.Fatalf("SyncSearchIndex failed: %v", err)
		}
		if _, err := storage.ListArticles(); err != nil {
			t.Fatalf("ListArticles failed: %v", err)
		}
	}
	scanAll()
	scanAll()
	if warnings := unreadableWarnings(t, buf, "home.md"); len(warnings) != 1 {
		t.Errorf("expected one warning for the broken target, got %d: %q", len(warnings), warnings)
	}

	writeWithMtime(t, target, []byte("---\ntitle: [still unclosed\n---\nbody\n"), time.Now().Add(-time.Hour))
	scanAll()
	scanAll()
	if warnings := unreadableWarnings(t, buf, "home.md"); len(warnings) != 2 {
		t.Errorf("expected a second warning once the target changed, got %d: %q", len(warnings), warnings)
	}
}

// TestSymlinkedArticleFileIsCanonicalByLinkName pins that the canonical rule reads the name in the
// article directory, not the target's: a link named for another slug is misplaced wherever it points,
// and warned about once while its target is unchanged.
func TestSymlinkedArticleFileIsCanonicalByLinkName(t *testing.T) {
	storage, err := NewStorage(t.TempDir())
	if err != nil {
		t.Fatalf("NewStorage failed: %v", err)
	}
	t.Cleanup(func() { _ = storage.Close() })

	// The target is named for its slug, which must not make the link canonical.
	target := filepath.Join(t.TempDir(), "linked-doc.md")
	writeWithMtime(t, target, linkedDoc("aliased", "body"), time.Now().Add(-time.Hour))
	symlinkOrSkip(t, target, filepath.Join(storage.ArticleDir, "alias.md"))
	buf := captureLog(t)

	for i := 0; i < 2; i++ {
		if slugs := listedSlugs(t, storage); len(slugs) != 0 {
			t.Errorf("a link named for another slug must not be listed, got %v", slugs)
		}
		graph, err := storage.ScanLinkGraph()
		if err != nil {
			t.Fatalf("ScanLinkGraph failed: %v", err)
		}
		if want := []MisplacedDocument{{Path: "alias.md", Slug: "linked-doc"}}; !reflect.DeepEqual(graph.Misplaced, want) {
			t.Errorf("Misplaced = %+v, want %+v", graph.Misplaced, want)
		}
		if _, err := storage.metaBySlug("linked-doc"); err == nil {
			t.Error("metaBySlug found a document that is only reachable through a misplaced link")
		}
	}
	if warnings := misplacedWarnings(t, buf, "alias.md"); len(warnings) != 1 {
		t.Errorf("expected one misplaced warning, got %d: %q", len(warnings), warnings)
	}
}

// TestDanglingArticleSymlinkIsUnreadable pins that a symlink whose target is missing is still
// reported, now that a walk stats the target rather than the link: it sits in the article directory,
// so it is not a file deleted mid-walk.
func TestDanglingArticleSymlinkIsUnreadable(t *testing.T) {
	storage, err := NewStorage(t.TempDir())
	if err != nil {
		t.Fatalf("NewStorage failed: %v", err)
	}
	t.Cleanup(func() { _ = storage.Close() })

	target := filepath.Join(t.TempDir(), "missing.md")
	symlinkOrSkip(t, target, filepath.Join(storage.ArticleDir, "linked-doc.md"))
	buf := captureLog(t)

	for i := 0; i < 2; i++ {
		graph, err := storage.ScanLinkGraph()
		if err != nil {
			t.Fatalf("ScanLinkGraph failed: %v", err)
		}
		if len(graph.Unreadable) != 1 || graph.Unreadable[0].Path != "linked-doc.md" {
			t.Errorf("Unreadable = %+v, want linked-doc.md", graph.Unreadable)
		}
		scan, err := storage.scanBacklinks("home")
		if err != nil {
			t.Fatalf("scanBacklinks failed: %v", err)
		}
		if len(scan.unreadable) != 1 || scan.unreadable[0].Path != "linked-doc.md" {
			t.Errorf("backlink scan unreadable = %+v, want linked-doc.md", scan.unreadable)
		}
		if slugs := listedSlugs(t, storage); len(slugs) != 0 {
			t.Errorf("ListArticles = %v, want nothing", slugs)
		}
	}
	if warnings := unreadableWarnings(t, buf, "linked-doc.md"); len(warnings) != 1 {
		t.Errorf("expected one warning for the dangling link, got %d: %q", len(warnings), warnings)
	}

	// Once the target exists the link is an article, and losing the target again warns again.
	writeWithMtime(t, target, linkedDoc("found", "body"), time.Now().Add(-time.Hour))
	if got, want := listedSlugs(t, storage), []string{"linked-doc"}; !reflect.DeepEqual(got, want) {
		t.Errorf("ListArticles = %v, want %v", got, want)
	}
	if err := os.Remove(target); err != nil {
		t.Fatalf("Remove failed: %v", err)
	}
	if slugs := listedSlugs(t, storage); len(slugs) != 0 {
		t.Errorf("ListArticles = %v, want nothing once the target is gone again", slugs)
	}
	if warnings := unreadableWarnings(t, buf, "linked-doc.md"); len(warnings) != 2 {
		t.Errorf("expected a second warning once the target went missing again, got %d: %q", len(warnings), warnings)
	}
}

// TestSymlinkToDirectoryIsNotAnArticle pins that a symlink named like an article file but pointing at
// a directory is skipped without a report. It is no more an article than a directory would be, and
// the walks do not descend through it, so a document inside is not reported either.
func TestSymlinkToDirectoryIsNotAnArticle(t *testing.T) {
	storage, err := NewStorage(t.TempDir())
	if err != nil {
		t.Fatalf("NewStorage failed: %v", err)
	}
	t.Cleanup(func() { _ = storage.Close() })

	dir := filepath.Join(t.TempDir(), "folder.md")
	writeWithMtime(t, filepath.Join(dir, "inner.md"), []byte("---\ntitle: Inner\nslug: inner\n---\n[[Home]]\n"), time.Now().Add(-time.Hour))
	symlinkOrSkip(t, dir, filepath.Join(storage.ArticleDir, "folder.md"))
	buf := captureLog(t)

	if slugs := listedSlugs(t, storage); len(slugs) != 0 {
		t.Errorf("ListArticles = %v, want nothing", slugs)
	}
	graph, err := storage.ScanLinkGraph()
	if err != nil {
		t.Fatalf("ScanLinkGraph failed: %v", err)
	}
	if len(graph.Unreadable) != 0 || len(graph.Misplaced) != 0 || len(graph.Meta) != 1 {
		t.Errorf("a link to a directory is not an article: got unreadable %+v, misplaced %+v, and %d documents",
			graph.Unreadable, graph.Misplaced, len(graph.Meta))
	}
	scan, err := storage.scanBacklinks("home")
	if err != nil {
		t.Fatalf("scanBacklinks failed: %v", err)
	}
	if len(scan.backlinks) != 0 || len(scan.misplaced) != 0 || len(scan.unreadable) != 0 {
		t.Errorf("backlink scan = %+v, want nothing", scan)
	}
	if _, _, err := storage.findAssetReferrers("home"); err != nil {
		t.Errorf("findAssetReferrers failed: %v", err)
	}
	if err := storage.SyncSearchIndex(); err != nil {
		t.Fatalf("SyncSearchIndex failed: %v", err)
	}
	if strings.Contains(buf.String(), "folder.md") {
		t.Errorf("a link to a directory must not be warned about:\n%s", buf)
	}
}

// TestSymlinkedCorpusKeepsAutoDeleteBacklinkGuard pins that the backlink scan behind startup
// auto-delete follows symlinks like every other walk. scanBacklinksToEach was the one walk still
// handed the article directory raw: through a symlinked root its corpus looked empty, so every
// archived document came back unlinked and the guard would have deleted a linked one, and a
// symlinked article file it fingerprinted by the link, so an edit to the target never reached the
// scan and a document that had just been linked still looked unlinked. The unlinked archived
// document beside the linked one is the control that the guard still deletes, so a kept document
// is the backlink's doing and not a broken scan.
func TestSymlinkedCorpusKeepsAutoDeleteBacklinkGuard(t *testing.T) {
	t.Run("symlinked article directory", func(t *testing.T) {
		t.Setenv(envAutoDeleteArchived, "")
		dataDir := t.TempDir()
		seed, err := NewStorage(dataDir)
		if err != nil {
			t.Fatalf("NewStorage failed: %v", err)
		}
		saveArchived(t, seed, "Linked Archive", "# kept")
		saveArchived(t, seed, "Free Archive", "# gone")
		if _, err := seed.SaveArticle("", "Wiki Pointer", "See [[Linked Archive]].", "", "", "", "", nil, ContentTypeWiki); err != nil {
			t.Fatalf("seed linker: %v", err)
		}
		for _, slug := range []string{"linked-archive", "free-archive"} {
			backdateArchived(t, seed.ArticleDir, slug, 31, time.Now())
		}
		if err := seed.Close(); err != nil {
			t.Fatalf("Close failed: %v", err)
		}

		// Move the article directory elsewhere and link it back in its place, then start the wiki
		// again: NewStorage runs the guard, and through the link it must see the corpus the
		// lookups do.
		articleDir := filepath.Join(dataDir, "articles")
		elsewhere := filepath.Join(t.TempDir(), "moved-articles")
		if err := os.Rename(articleDir, elsewhere); err != nil {
			t.Fatalf("Rename failed: %v", err)
		}
		symlinkOrSkip(t, elsewhere, articleDir)

		t.Setenv(envAutoDeleteArchived, "30")
		buf := captureLog(t)
		s, err := NewStorage(dataDir)
		if err != nil {
			t.Fatalf("NewStorage over a symlinked article directory failed: %v", err)
		}
		t.Cleanup(func() { _ = s.Close() })

		if !exists(t, s, "linked-archive") {
			t.Error("an archived article linked through the symlinked article directory must not be auto-deleted")
		}
		if len(logLines(buf, "Warning: not auto-deleting archived article 'linked-archive'", "still linked from 1 document: wiki-pointer")) != 1 {
			t.Errorf("the kept article must be warned about naming its linker, got %q", buf.String())
		}
		if exists(t, s, "free-archive") {
			t.Error("an unlinked archived article must still be deleted through the symlinked article directory")
		}
		if len(logLines(buf, "Deleted archived article: free-archive (archived at: ")) != 1 {
			t.Errorf("the deletion must leave its audit line, got %q", buf.String())
		}

		// The batched scan itself, not only the guard built on it, must see the backlink — and
		// nothing else: a scan that reported the corpus unreadable would keep documents for the
		// wrong reason.
		scans, err := s.scanBacklinksToEach([]string{"linked-archive"})
		if err != nil {
			t.Fatalf("scanBacklinksToEach failed: %v", err)
		}
		scan := scans["linked-archive"]
		if len(scan.backlinks) != 1 || scan.backlinks[0].Slug != "wiki-pointer" {
			t.Errorf("scan for linked-archive = %+v, want wiki-pointer as its one backlink", scan)
		}
		if reasons := scan.incompleteReasons(); len(reasons) != 0 {
			t.Errorf("a readable corpus through a symlinked root must scan completely, got %v", reasons)
		}
	})

	t.Run("symlinked article file", func(t *testing.T) {
		t.Setenv(envAutoDeleteArchived, "")
		s, err := NewStorage(t.TempDir())
		if err != nil {
			t.Fatalf("NewStorage failed: %v", err)
		}
		t.Cleanup(func() { _ = s.Close() })
		saveArchived(t, s, "Linked Archive", "# kept")
		saveArchived(t, s, "Free Archive", "# gone")
		if _, err := s.SaveArticle("", "Wiki Pointer", "No links yet.", "", "", "", "", nil, ContentTypeWiki); err != nil {
			t.Fatalf("seed linker: %v", err)
		}

		// Move the linker out and link it back in its place. Nothing is due yet.
		target := filepath.Join(t.TempDir(), "wiki-pointer.md")
		link := filepath.Join(s.ArticleDir, "wiki-pointer.md")
		if err := os.Rename(link, target); err != nil {
			t.Fatalf("Rename failed: %v", err)
		}
		symlinkOrSkip(t, target, link)

		t.Setenv(envAutoDeleteArchived, "30")
		scans, err := s.scanBacklinksToEach([]string{"linked-archive", "free-archive"})
		if err != nil {
			t.Fatalf("scanBacklinksToEach failed: %v", err)
		}
		for slug, scan := range scans {
			if len(scan.backlinks) != 0 || len(scan.incompleteReasons()) != 0 {
				t.Fatalf("fixture: scan for %s = %+v, want nothing yet", slug, scan)
			}
		}

		// An edit to the target is the version the scan must read: the link's own fingerprint
		// never changes, and a scan that cached by it would keep serving the body before the edit
		// and let the guard delete a document that has just been linked.
		writeWithMtime(t, target, []byte("---\ntitle: Wiki Pointer\nslug: wiki-pointer\n---\nSee [[Linked Archive]].\n"), time.Now())
		scans, err = s.scanBacklinksToEach([]string{"linked-archive", "free-archive"})
		if err != nil {
			t.Fatalf("scanBacklinksToEach failed: %v", err)
		}
		if scan := scans["linked-archive"]; len(scan.backlinks) != 1 || scan.backlinks[0].Slug != "wiki-pointer" {
			t.Errorf("scan for linked-archive after the target gained its link = %+v, want wiki-pointer as its one backlink", scan)
		}
		if scan := scans["free-archive"]; len(scan.backlinks) != 0 || len(scan.incompleteReasons()) != 0 {
			t.Errorf("scan for free-archive = %+v, want nothing", scan)
		}

		// The guard decides on that scan: the linked document is kept, the unlinked one deleted.
		backdateArchived(t, s.ArticleDir, "linked-archive", 31, time.Now())
		backdateArchived(t, s.ArticleDir, "free-archive", 31, time.Now().Add(-time.Minute))
		buf := captureLog(t)
		if err := s.CleanupArchivedArticles(); err != nil {
			t.Fatalf("CleanupArchivedArticles failed: %v", err)
		}
		if !exists(t, s, "linked-archive") {
			t.Error("an archived article linked through a symlinked article file must not be auto-deleted")
		}
		if len(logLines(buf, "Warning: not auto-deleting archived article 'linked-archive'", "still linked from 1 document: wiki-pointer")) != 1 {
			t.Errorf("the kept article must be warned about naming its linker, got %q", buf.String())
		}
		if exists(t, s, "free-archive") {
			t.Error("an unlinked archived article must still be deleted")
		}
		if len(logLines(buf, "Deleted archived article: free-archive (archived at: ")) != 1 {
			t.Errorf("the deletion must leave its audit line, got %q", buf.String())
		}
	})
}
