package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/blevesearch/bleve/v2"
)

// misplacedBody is what every misplaced fixture file holds: a search term, a link to a real page, a
// link to nothing, and an embedded asset of a real page, so each scan has something it could wrongly
// pick up.
const misplacedBody = "zanzibarquux [[Good One]] [[Nowhere Page]] ![d](/api/assets/good-one/d.png)\n"

// writeArticleFile drops a document straight into the article directory at relPath, the way a hand
// edit, a copy, or a move into a folder would, bypassing the save path that only writes <slug>.md.
func writeArticleFile(t *testing.T, storage *Storage, relPath, frontMatter, body string, mtime time.Time) {
	t.Helper()
	writeWithMtime(t, filepath.Join(storage.ArticleDir, filepath.FromSlash(relPath)),
		[]byte("---\n"+frontMatter+"---\n"+body), mtime)
}

// misplacedWarnings returns the logged warnings that report relPath as misplaced.
func misplacedWarnings(t *testing.T, buf *logCaptureBuffer, relPath string) []string {
	t.Helper()
	return unreadableWarnings(t, buf, "misplaced article file "+relPath+":")
}

// wantMisplaced is what the fixture's misplaced files are reported as, sorted by path.
var wantMisplaced = []MisplacedDocument{
	{Path: "archive/sub-doc.md", Slug: "sub-doc"},
	{Path: "derived.md", Slug: "derived-title"},
	{Path: "file-name.md", Slug: "front-slug"},
	{Path: "zz/home.md", Slug: "home"},
}

// newMisplacedFixture builds a storage with canonical documents around the four shapes of misplaced
// document: one in a subdirectory, one whose declared slug differs from its filename, one whose slug
// is derived from its title and differs from its filename, and a copy of home in a subdirectory,
// which a walk reaches after the real home.md. derived-ok.md derives its slug from its title too, and
// matches, so it is canonical.
//
// It returns the link graph as it stood before the misplaced files were written. Nothing has scanned
// the misplaced files yet, so they are uncached.
func newMisplacedFixture(t *testing.T) (*Storage, *LinkGraph) {
	t.Helper()
	storage, err := NewStorage(t.TempDir())
	if err != nil {
		t.Fatalf("NewStorage failed: %v", err)
	}
	t.Cleanup(func() { _ = storage.Close() })

	for _, doc := range []struct{ title, body string }{
		{"Good One", "# Good One"},
		{"Good Two", "links [[Good One]]"},
		{"Embedder", "![d](/api/assets/good-one/d.png)"},
		// Links to the misplaced documents' slugs, which must stay broken: a misplaced document is no
		// link target.
		{"Pointer", "[[Front Slug]] and [[Sub Doc]]"},
	} {
		if _, err := storage.SaveArticle("", doc.title, doc.body, "", "", "", "seed", nil, ""); err != nil {
			t.Fatalf("SaveArticle(%q) failed: %v", doc.title, err)
		}
	}
	old := time.Now().Add(-time.Hour)
	writeArticleFile(t, storage, "derived-ok.md", "title: Derived Ok\n", "canonical\n", old)

	before, err := storage.ScanLinkGraph()
	if err != nil {
		t.Fatalf("ScanLinkGraph failed: %v", err)
	}

	writeArticleFile(t, storage, "archive/sub-doc.md", "title: Sub Doc\nslug: sub-doc\n", misplacedBody, old)
	writeArticleFile(t, storage, "file-name.md", "title: Front Slug\nslug: front-slug\n", misplacedBody, old)
	writeArticleFile(t, storage, "derived.md", "title: Derived Title\n", misplacedBody, old)
	writeArticleFile(t, storage, "zz/home.md", "title: Home Copy\nslug: home\n", misplacedBody, old)
	return storage, before
}

func articleSlugs(articles []Article) []string {
	slugs := make([]string, 0, len(articles))
	for _, a := range articles {
		slugs = append(slugs, a.Slug)
	}
	sort.Strings(slugs)
	return slugs
}

// TestMisplacedDocumentsAreExcludedFromEveryScan pins the canonical-document rule (#152): a document
// counts only when stored as <slug>.md directly in the article directory. Every other parseable file
// is left out of the listing, the link graph (as a document, a link source, and a link target),
// backlinks, the asset scan, and the search index, and the link graph reports it.
func TestMisplacedDocumentsAreExcludedFromEveryScan(t *testing.T) {
	storage, before := newMisplacedFixture(t)
	captureLog(t)

	t.Run("ListArticles", func(t *testing.T) {
		articles, err := storage.ListArticles()
		if err != nil {
			t.Fatalf("ListArticles failed: %v", err)
		}
		if got, want := articleSlugs(articles), []string{"derived-ok", "embedder", "good-one", "good-two", "pointer"}; !reflect.DeepEqual(got, want) {
			t.Errorf("ListArticles = %v, want %v", got, want)
		}
	})

	t.Run("ScanLinkGraph", func(t *testing.T) {
		graph, err := storage.ScanLinkGraph()
		if err != nil {
			t.Fatalf("ScanLinkGraph failed: %v", err)
		}
		if !reflect.DeepEqual(graph.Misplaced, wantMisplaced) {
			t.Errorf("Misplaced = %+v, want %+v", graph.Misplaced, wantMisplaced)
		}
		if len(graph.Unreadable) != 0 {
			t.Errorf("a misplaced document is not unreadable, got %+v", graph.Unreadable)
		}
		// The graph is exactly what it was without the misplaced files: they add no document, no
		// link, no inbound count, and repair no broken link.
		if got, want := sortedKeys(graph.Meta), sortedKeys(before.Meta); !reflect.DeepEqual(got, want) {
			t.Errorf("Meta slugs = %v, want %v", got, want)
		}
		if got, want := sortedKeys(graph.Outbound), sortedKeys(before.Outbound); !reflect.DeepEqual(got, want) {
			t.Errorf("Outbound sources = %v, want %v", got, want)
		}
		if graph.TotalLinks != before.TotalLinks {
			t.Errorf("TotalLinks = %d, want %d", graph.TotalLinks, before.TotalLinks)
		}
		if !reflect.DeepEqual(graph.InboundCount, before.InboundCount) {
			t.Errorf("InboundCount = %v, want %v", graph.InboundCount, before.InboundCount)
		}
		if !reflect.DeepEqual(graph.Broken, before.Broken) {
			t.Errorf("Broken = %+v, want %+v", graph.Broken, before.Broken)
		}
		if _, ok := graph.Meta["derived-ok"]; !ok {
			t.Error("a document whose derived slug matches its filename is canonical")
		}
		// The top-level home.md is canonical as usual, and its misplaced copy does not replace it.
		if home, ok := graph.Meta["home"]; !ok || home.Title != "Home" {
			t.Errorf("Meta[home] = %+v, want the real home page", home)
		}
	})

	t.Run("GetBacklinks", func(t *testing.T) {
		backlinks, err := storage.GetBacklinks("good-one")
		if err != nil {
			t.Fatalf("GetBacklinks failed: %v", err)
		}
		if got, want := articleSlugs(backlinks), []string{"good-two"}; !reflect.DeepEqual(got, want) {
			t.Errorf("GetBacklinks = %v, want %v", got, want)
		}
		// Every misplaced file links to good-one, so each is reported as a misplaced linker, not a
		// backlink and not an unreadable entry.
		scan, err := storage.scanBacklinks("good-one")
		if err != nil {
			t.Fatalf("scanBacklinks failed: %v", err)
		}
		if got, want := articleSlugs(scan.backlinks), []string{"good-two"}; !reflect.DeepEqual(got, want) {
			t.Errorf("scanBacklinks backlinks = %v, want %v", got, want)
		}
		if !reflect.DeepEqual(scan.misplaced, wantMisplaced) || len(scan.unreadable) != 0 {
			t.Errorf("scanBacklinks misplaced = %+v and unreadable = %+v, want %+v and none", scan.misplaced, scan.unreadable, wantMisplaced)
		}
	})

	t.Run("findAssetReferrers", func(t *testing.T) {
		referrers, _, err := storage.findAssetReferrers("good-one")
		if err != nil {
			t.Fatalf("findAssetReferrers failed: %v", err)
		}
		if want := []string{"embedder"}; !reflect.DeepEqual(referrers, want) {
			t.Errorf("findAssetReferrers = %v, want %v", referrers, want)
		}
	})

	t.Run("SyncSearchIndex", func(t *testing.T) {
		// Entries left from when these documents were stored where they belonged. A misplaced document
		// is no valid slug, so boot reconciliation drops them rather than keeping them searchable.
		for _, slug := range []string{"sub-doc", "front-slug"} {
			if err := storage.IndexArticle(&Article{Slug: slug, Title: slug, Content: "zanzibarquux"}); err != nil {
				t.Fatalf("IndexArticle failed: %v", err)
			}
		}
		if err := storage.SyncSearchIndex(); err != nil {
			t.Fatalf("SyncSearchIndex failed: %v", err)
		}
		for _, slug := range []string{"sub-doc", "front-slug", "derived-title"} {
			if inSearchIndex(t, storage, slug) {
				t.Errorf("misplaced document %s must not be in the search index", slug)
			}
		}
		res, err := storage.SearchIndex.Search(bleve.NewSearchRequest(bleve.NewQueryStringQuery("zanzibarquux")))
		if err != nil {
			t.Fatalf("search failed: %v", err)
		}
		if res.Total != 0 {
			t.Errorf("no indexed document should hold the misplaced documents' term, got %d hits", res.Total)
		}
		for _, slug := range []string{"home", "good-one", "derived-ok"} {
			if !inSearchIndex(t, storage, slug) {
				t.Errorf("canonical document %s should be indexed", slug)
			}
		}
	})
}

// TestMisplacedHomeIsNotIndexed pins the one misplaced document the listing does not feed to boot
// indexing: home.md, which the sync reads directly. Declaring another slug makes it misplaced like
// any other file, and it must not be indexed under that slug.
func TestMisplacedHomeIsNotIndexed(t *testing.T) {
	storage, err := NewStorage(t.TempDir())
	if err != nil {
		t.Fatalf("NewStorage failed: %v", err)
	}
	t.Cleanup(func() { _ = storage.Close() })
	buf := captureLog(t)

	writeArticleFile(t, storage, "home.md", "title: Home\nslug: dashboard\n", "zanzibarquux\n", time.Now().Add(time.Hour))
	if err := storage.SyncSearchIndex(); err != nil {
		t.Fatalf("SyncSearchIndex failed: %v", err)
	}
	if inSearchIndex(t, storage, "dashboard") {
		t.Error("a misplaced home page must not be indexed under its declared slug")
	}
	if warnings := misplacedWarnings(t, buf, "home.md"); len(warnings) != 1 {
		t.Errorf("expected one warning for the misplaced home page, got %q", warnings)
	}
	graph, err := storage.ScanLinkGraph()
	if err != nil {
		t.Fatalf("ScanLinkGraph failed: %v", err)
	}
	if want := []MisplacedDocument{{Path: "home.md", Slug: "dashboard"}}; !reflect.DeepEqual(graph.Misplaced, want) {
		t.Errorf("Misplaced = %+v, want %+v", graph.Misplaced, want)
	}

	// Reconciliation drops an entry under a slug nothing lists, so the case that shows is home.md
	// declaring a listed document's slug while that document's own load fails: its existing entry is
	// kept, and must not be replaced by home's body.
	t.Run("under a listed document's slug", func(t *testing.T) {
		if _, err := storage.SaveArticle("", "Good One", "# Good One", "", "", "", "seed", nil, ""); err != nil {
			t.Fatalf("SaveArticle failed: %v", err)
		}
		if _, err := storage.ListArticles(); err != nil { // cached, so it is still listed once unreadable
			t.Fatalf("ListArticles failed: %v", err)
		}
		makeUnreadable(t, filepath.Join(storage.ArticleDir, "good-one.md"))
		writeArticleFile(t, storage, "home.md", "title: Home\nslug: good-one\n", "zanzibarquux\n", time.Now().Add(2*time.Hour))

		if err := storage.SyncSearchIndex(); err != nil {
			t.Fatalf("SyncSearchIndex failed: %v", err)
		}
		res, err := storage.SearchIndex.Search(bleve.NewSearchRequest(bleve.NewQueryStringQuery("zanzibarquux")))
		if err != nil {
			t.Fatalf("search failed: %v", err)
		}
		if res.Total != 0 {
			t.Errorf("the misplaced home page was indexed under good-one: %d hits", res.Total)
		}
		if !inSearchIndex(t, storage, "good-one") {
			t.Error("good-one should keep its existing entry")
		}
	})
}

// scanEverything runs every walk of the article directory, and boot indexing, failing on any error.
func scanEverything(t *testing.T, storage *Storage) {
	t.Helper()
	if _, err := storage.ListArticles(); err != nil {
		t.Fatalf("ListArticles failed: %v", err)
	}
	if _, err := storage.ScanLinkGraph(); err != nil {
		t.Fatalf("ScanLinkGraph failed: %v", err)
	}
	if _, err := storage.GetBacklinks("good-one"); err != nil {
		t.Fatalf("GetBacklinks failed: %v", err)
	}
	if _, _, err := storage.findAssetReferrers("good-one"); err != nil {
		t.Fatalf("findAssetReferrers failed: %v", err)
	}
	if err := storage.SyncSearchIndex(); err != nil {
		t.Fatalf("SyncSearchIndex failed: %v", err)
	}
}

// TestMisplacedDocumentWarnsOncePerVersion pins that a misplaced document is reported, not silently
// dropped, and with the same dedup as an unreadable file: once per file version across every scan,
// and again once the file changes.
func TestMisplacedDocumentWarnsOncePerVersion(t *testing.T) {
	storage, _ := newMisplacedFixture(t)
	buf := captureLog(t)

	for i := 0; i < 3; i++ {
		scanEverything(t, storage)
	}
	for _, doc := range wantMisplaced {
		warnings := misplacedWarnings(t, buf, doc.Path)
		if len(warnings) != 1 {
			t.Errorf("expected exactly one warning for %s across repeated scans, got %d: %q", doc.Path, len(warnings), warnings)
			continue
		}
		if strings.Contains(warnings[0], storage.ArticleDir) {
			t.Errorf("warning should name the path relative to the article directory: %q", warnings[0])
		}
		// The slug, and where a file with that slug has to live.
		if want := `its slug is "` + doc.Slug + `", so it belongs at ` + doc.Slug + `.md directly in the article directory`; !strings.Contains(warnings[0], want) {
			t.Errorf("warning %q should contain %q", warnings[0], want)
		}
	}

	// A new version of a file is news again; the others stay quiet.
	writeArticleFile(t, storage, "archive/sub-doc.md", "title: Sub Doc\nslug: sub-doc\n", misplacedBody+"edited\n", time.Now().Add(time.Hour))
	writeArticleFile(t, storage, "file-name.md", "title: Front Slug\nslug: front-slug\n", misplacedBody+"edited\n", time.Now().Add(time.Hour))
	for i := 0; i < 3; i++ {
		scanEverything(t, storage)
	}
	for path, want := range map[string]int{"archive/sub-doc.md": 2, "file-name.md": 2, "derived.md": 1, "zz/home.md": 1} {
		if warnings := misplacedWarnings(t, buf, path); len(warnings) != want {
			t.Errorf("expected %d warnings for %s after the edits, got %d: %q", want, path, len(warnings), warnings)
		}
	}

	// Fixed by giving it the slug its filename says, the file is canonical and quiet; misplaced again,
	// it warns again.
	writeArticleFile(t, storage, "file-name.md", "title: File Name\nslug: file-name\n", "fixed\n", time.Now().Add(2*time.Hour))
	articles, err := storage.ListArticles()
	if err != nil {
		t.Fatalf("ListArticles failed: %v", err)
	}
	if !contains(articleSlugs(articles), "file-name") {
		t.Errorf("a file whose slug matches its filename should be listed, got %v", articleSlugs(articles))
	}
	writeArticleFile(t, storage, "file-name.md", "title: Front Slug\nslug: front-slug\n", misplacedBody+"edited\n", time.Now().Add(time.Hour))
	scanEverything(t, storage)
	if warnings := misplacedWarnings(t, buf, "file-name.md"); len(warnings) != 3 {
		t.Errorf("expected a third warning once the fixed file was misplaced again, got %d: %q", len(warnings), warnings)
	}
}

// TestMisplacedWarningIsOnceUnderConcurrency pins that concurrent scans reaching an uncached
// misplaced file agree on a single warning. Each of them parses the file successfully, and a
// successful parse must not reset the misplaced record another scan has just made. Run with -race.
func TestMisplacedWarningIsOnceUnderConcurrency(t *testing.T) {
	storage, _ := newMisplacedFixture(t)
	buf := captureLog(t)

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				if _, err := storage.ListArticles(); err != nil {
					t.Errorf("ListArticles failed: %v", err)
					return
				}
				if _, err := storage.ScanLinkGraph(); err != nil {
					t.Errorf("ScanLinkGraph failed: %v", err)
					return
				}
				if _, err := storage.GetBacklinks("good-one"); err != nil {
					t.Errorf("GetBacklinks failed: %v", err)
					return
				}
			}
		}()
	}
	wg.Wait()

	for _, doc := range wantMisplaced {
		if warnings := misplacedWarnings(t, buf, doc.Path); len(warnings) != 1 {
			t.Errorf("expected exactly one warning for %s under concurrent scans, got %d: %q", doc.Path, len(warnings), warnings)
		}
	}
}

// TestMisplacedRecordSurvivesAParseOfTheSameVersion pins the race the concurrency test can only hope
// to hit, step by step: a scan records a file as misplaced, then another scan that missed the cache
// parses the same version and stores it. That must not forget the record, while a parse that recovers
// an unreadable file must still forget its failure.
func TestMisplacedRecordSurvivesAParseOfTheSameVersion(t *testing.T) {
	storage, _ := newMisplacedFixture(t)
	buf := captureLog(t)

	path := filepath.Join(storage.ArticleDir, "file-name.md")
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatalf("Lstat failed: %v", err)
	}
	if _, misplaced := storage.skipMisplaced(path, info, "front-slug"); !misplaced {
		t.Fatal("file-name.md declaring front-slug should be misplaced")
	}
	storage.cache.store(path, info, Article{Slug: "front-slug"})
	if _, misplaced := storage.skipMisplaced(path, info, "front-slug"); !misplaced {
		t.Fatal("file-name.md declaring front-slug should still be misplaced")
	}
	if warnings := misplacedWarnings(t, buf, "file-name.md"); len(warnings) != 1 {
		t.Errorf("a parse of the same version must not make the misplaced file news again, got %q", warnings)
	}

	if !storage.cache.noteFailure(filepath.Join(storage.ArticleDir, "good-one.md"), info) {
		t.Fatal("a first failure should be news")
	}
	storage.cache.store(filepath.Join(storage.ArticleDir, "good-one.md"), info, Article{Slug: "good-one"})
	if !storage.cache.noteFailure(filepath.Join(storage.ArticleDir, "good-one.md"), info) {
		t.Error("a parse that succeeds must forget an earlier read or parse failure, even of the same version")
	}
}

// TestWikiHealthReportsMisplacedDocuments pins how misplaced documents reach a client: counted and
// listed by relative path and slug, left out of total_documents, placed right after the unreadable
// files, given a remedy that fits where the file is, and matching the published schema.
func TestWikiHealthReportsMisplacedDocuments(t *testing.T) {
	srv := newMCPServer(t)
	captureLog(t)

	for _, title := range []string{"Healthy Page", "Bar"} {
		if _, err := srv.Storage.SaveArticle("", title, "# "+title, "", "", "", "seed", nil, ""); err != nil {
			t.Fatalf("seeding failed: %v", err)
		}
	}
	before := healthReport(t, srv, `{}`)
	if before.MisplacedDocumentCount != 0 || before.MisplacedDocuments == nil || len(before.MisplacedDocuments) != 0 {
		t.Fatalf("a wiki with no misplaced documents should report 0 and an empty list, got %d and %#v",
			before.MisplacedDocumentCount, before.MisplacedDocuments)
	}

	bar, err := os.ReadFile(filepath.Join(srv.Storage.ArticleDir, "bar.md"))
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}
	old := time.Now().Add(-time.Hour)
	writeWithMtime(t, filepath.Join(srv.Storage.ArticleDir, "bar-copy.md"), bar, old)
	writeArticleFile(t, srv.Storage, "archive/bar.md", "title: Bar\nslug: bar\n", "body\n", old)
	writeArticleFile(t, srv.Storage, "notes/sub-doc.md", "title: Sub Doc\nslug: sub-doc\n", "body\n", old)
	writeArticleFile(t, srv.Storage, "drafts/broken.md", "title: Broken\nslug: broken\n", "body\n", old)
	writeBrokenArticle(t, srv, "broken.md")

	resp := toolCall(t, srv, `{"name":"wiki_health","arguments":{}}`)
	var out HealthOutput
	decodeStructured(t, resp, &out)

	want := []MisplacedDocumentFinding{
		// A copy of an existing article: moving it to where its slug says would overwrite bar.md.
		{Path: "archive/bar.md", Slug: "bar", Remedy: "articles/bar.md already exists, so do not move it there. Delete it if it's a copy, or give it an unused slug and store it as articles/<slug>.md."},
		{Path: "bar-copy.md", Slug: "bar", Remedy: "articles/bar.md already exists, so do not move it there. Change its slug to \"bar-copy\" to match its filename, or delete it if it's a copy."},
		// An unreadable file holds the destination just as surely as a document does.
		{Path: "drafts/broken.md", Slug: "broken", Remedy: "articles/broken.md already exists, so do not move it there. Delete it if it's a copy, or give it an unused slug and store it as articles/<slug>.md."},
		// Only a free destination is suggested.
		{Path: "notes/sub-doc.md", Slug: "sub-doc", Remedy: "Move it to articles/sub-doc.md, or delete it if it's a copy."},
	}
	if out.MisplacedDocumentCount != len(want) || !reflect.DeepEqual(out.MisplacedDocuments, want) {
		t.Fatalf("expected %+v, got count %d and %+v", want, out.MisplacedDocumentCount, out.MisplacedDocuments)
	}
	if out.Truncated {
		t.Error("four misplaced documents are well under the default limit")
	}
	if out.TotalDocuments != before.TotalDocuments {
		t.Errorf("total_documents changed from %d to %d; a misplaced document is not a document", before.TotalDocuments, out.TotalDocuments)
	}
	if out.UnreadableFileCount != 1 {
		t.Errorf("the unreadable file should still be reported on its own, got %d", out.UnreadableFileCount)
	}
	encoded, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	if strings.Contains(string(encoded), srv.Storage.DataDir) {
		t.Errorf("the response reveals the server's data directory %q:\n%s", srv.Storage.DataDir, encoded)
	}

	text := resp.Content[0].Text
	for _, want := range []string{
		"- Misplaced documents (not at articles/<slug>.md, skipped by every check): 4\n",
		"== Misplaced documents (4) ==\n",
		"- bar-copy.md (slug \"bar\") — not a document, so no listing, search, lookup, or check includes it. articles/bar.md already exists, so do not move it there. Change its slug to \"bar-copy\" to match its filename, or delete it if it's a copy.\n",
		"- notes/sub-doc.md (slug \"sub-doc\") — not a document, so no listing, search, lookup, or check includes it. Move it to articles/sub-doc.md, or delete it if it's a copy.\n",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("prose is missing %q:\n%s", want, text)
		}
	}
	unreadableAt := strings.Index(text, "== Unreadable article files and folders")
	misplacedAt := strings.Index(text, "== Misplaced documents")
	orphansAt := strings.Index(text, "== Orphan pages")
	if unreadableAt < 0 || orphansAt < 0 || unreadableAt >= misplacedAt || misplacedAt >= orphansAt {
		t.Errorf("misplaced documents should be listed after unreadable files and before the other categories:\n%s", text)
	}

	assertMatchesOutputSchema(t, resp, wikiHealthTool.Output)
}

// TestWikiHealthMisplacedDocumentAloneNeedsAttention pins that a misplaced document on its own stops
// the report calling the wiki healthy.
func TestWikiHealthMisplacedDocumentAloneNeedsAttention(t *testing.T) {
	srv := newHealthyWikiServer(t)
	captureLog(t)

	if text := toolCall(t, srv, `{"name":"wiki_health","arguments":{}}`).Content[0].Text; !strings.Contains(text, "the wiki is healthy") {
		t.Fatalf("the fixture must start with nothing to report, or this test proves nothing:\n%s", text)
	}

	writeArticleFile(t, srv.Storage, "notes/sub-doc.md", "title: Sub Doc\nslug: sub-doc\n", "body\n", time.Now().Add(-time.Hour))

	resp := toolCall(t, srv, `{"name":"wiki_health","arguments":{}}`)
	var out HealthOutput
	decodeStructured(t, resp, &out)
	if out.MisplacedDocumentCount != 1 {
		t.Fatalf("expected one misplaced document, got %d", out.MisplacedDocumentCount)
	}
	text := resp.Content[0].Text
	if strings.Contains(text, "the wiki is healthy") {
		t.Errorf("a wiki with a misplaced document is not healthy:\n%s", text)
	}
	if !strings.Contains(text, "- notes/sub-doc.md (slug \"sub-doc\") — ") {
		t.Errorf("the report should go on to list the document:\n%s", text)
	}
}

// TestWikiHealthCapsMisplacedDocuments pins that the category honours 'limit' like every other: the
// count stays complete, the list is cut, and the report says so.
func TestWikiHealthCapsMisplacedDocuments(t *testing.T) {
	srv := newMCPServer(t)
	captureLog(t)
	for _, name := range []string{"c", "a", "b"} {
		writeArticleFile(t, srv.Storage, name+".md", "title: Other "+name+"\nslug: other-"+name+"\n", "body\n", time.Now().Add(-time.Hour))
	}

	full := healthReport(t, srv, `{}`)
	if full.MisplacedDocumentCount != 3 || len(full.MisplacedDocuments) != 3 {
		t.Fatalf("expected all three misplaced documents, got count %d and %+v", full.MisplacedDocumentCount, full.MisplacedDocuments)
	}
	if full.Truncated {
		t.Error("three documents are well under the default limit and must not report truncation")
	}

	resp := toolCall(t, srv, `{"name":"wiki_health","arguments":{"limit":2}}`)
	var capped HealthOutput
	decodeStructured(t, resp, &capped)
	if capped.MisplacedDocumentCount != 3 {
		t.Errorf("count must stay complete under a limit, got %d", capped.MisplacedDocumentCount)
	}
	if want := []MisplacedDocument{{Path: "a.md", Slug: "other-a"}, {Path: "b.md", Slug: "other-b"}}; !reflect.DeepEqual(findingDocuments(capped.MisplacedDocuments), want) {
		t.Errorf("limit:2 should keep the first two paths in order, got %+v", capped.MisplacedDocuments)
	}
	if !capped.Truncated {
		t.Error("a capped misplaced list must set truncated")
	}
	text := resp.Content[0].Text
	if !strings.Contains(text, "- b.md (slug \"other-b\") — ") || strings.Contains(text, "- c.md ") ||
		!strings.Contains(text, "== Misplaced documents (3) ==\n") ||
		!strings.Contains(text, "  ... and 1 more; raise 'limit' to see them.\n") {
		t.Errorf("the prose should list the kept documents and say the list was cut short:\n%s", text)
	}
	assertMatchesOutputSchema(t, resp, wikiHealthTool.Output)
}

// findingDocuments strips the remedies off wiki_health's misplaced findings, for comparing with a
// link graph's.
func findingDocuments(findings []MisplacedDocumentFinding) []MisplacedDocument {
	docs := []MisplacedDocument{}
	for _, f := range findings {
		docs = append(docs, MisplacedDocument{Path: f.Path, Slug: f.Slug})
	}
	return docs
}

// articleTree lists every file under the article directory, relative and slash-separated.
func articleTree(t *testing.T, storage *Storage) []string {
	t.Helper()
	var files []string
	err := filepath.WalkDir(storage.ArticleDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			files = append(files, storage.articleRelPath(path))
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking the article directory failed: %v", err)
	}
	sort.Strings(files)
	return files
}

// healWarnings returns the logged warnings about not healing references to oldSlug in relPath.
func healWarnings(buf *logCaptureBuffer, oldSlug, relPath string) []string {
	var lines []string
	for _, line := range strings.Split(buf.String(), "\n") {
		if strings.HasPrefix(line, "Warning: not healing references to renamed article '"+oldSlug+"' in ") && strings.Contains(line, " "+relPath+":") {
			lines = append(lines, line)
		}
	}
	return lines
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}
	return string(data)
}

// TestRenameNeverHealsIntoAnotherFile is the #151 scenario. bar-copy.md is a copy of bar.md, so it
// declares slug bar, and it embeds the renamed article's image. Healing used to load it by filename
// and save it under its slug, writing its body over bar.md. A document in a subfolder that links to
// the renamed article was skipped with nothing said. Now neither is written, nothing new is created,
// and each is named in a warning.
func TestRenameNeverHealsIntoAnotherFile(t *testing.T) {
	storage, err := NewStorage(t.TempDir())
	if err != nil {
		t.Fatalf("NewStorage failed: %v", err)
	}
	t.Cleanup(func() { _ = storage.Close() })
	buf := captureLog(t)

	if _, err := storage.SaveArticle("", "Diagram Source", "# Diagram", "", "", "", "seed", nil, ""); err != nil {
		t.Fatalf("SaveArticle failed: %v", err)
	}
	if _, err := storage.SaveAsset("diagram-source", "pic.png", []byte("PNG")); err != nil {
		t.Fatalf("SaveAsset failed: %v", err)
	}
	if _, err := storage.SaveArticle("", "Bar", "REAL BAR BODY", "", "", "", "seed", nil, ""); err != nil {
		t.Fatalf("SaveArticle failed: %v", err)
	}
	// A canonical referrer, which healing must still rewrite.
	if _, err := storage.SaveArticle("", "Good Linker", "See [[Diagram Source]].", "", "", "", "seed", nil, ""); err != nil {
		t.Fatalf("SaveArticle failed: %v", err)
	}

	barPath := filepath.Join(storage.ArticleDir, "bar.md")
	copyPath := filepath.Join(storage.ArticleDir, "bar-copy.md")
	nestedPath := filepath.Join(storage.ArticleDir, "archive", "linker.md")
	writeWithMtime(t, copyPath, []byte(readFile(t, barPath)+"\n![img](/api/assets/diagram-source/pic.png)\n"), time.Now().Add(-time.Hour))
	writeArticleFile(t, storage, "archive/linker.md", "title: Linker\nslug: linker\n", "See [[Diagram Source]].\n", time.Now().Add(-time.Hour))

	barBefore, copyBefore, nestedBefore := readFile(t, barPath), readFile(t, copyPath), readFile(t, nestedPath)
	historyBefore, err := storage.GetArticleHistory("bar")
	if err != nil {
		t.Fatalf("GetArticleHistory failed: %v", err)
	}
	treeBefore := articleTree(t, storage)

	if _, err := storage.SaveArticle("diagram-source", "Diagram Renamed", "# Diagram", "", "", "", "rename", nil, ""); err != nil {
		t.Fatalf("rename failed: %v", err)
	}

	if got := readFile(t, barPath); got != barBefore {
		t.Errorf("bar.md was rewritten by the rename:\n%s", got)
	}
	if got := readFile(t, copyPath); got != copyBefore {
		t.Errorf("bar-copy.md was rewritten by the rename:\n%s", got)
	}
	if got := readFile(t, nestedPath); got != nestedBefore {
		t.Errorf("archive/linker.md was rewritten by the rename:\n%s", got)
	}
	if history, err := storage.GetArticleHistory("bar"); err != nil || len(history) != len(historyBefore) {
		t.Errorf("bar gained history from the rename: %d versions before, %d after (%v)", len(historyBefore), len(history), err)
	}
	var wantTree []string
	for _, f := range treeBefore {
		if f != "diagram-source.md" {
			wantTree = append(wantTree, f)
		}
	}
	wantTree = append(wantTree, "diagram-renamed.md")
	sort.Strings(wantTree)
	if got := articleTree(t, storage); !reflect.DeepEqual(got, wantTree) {
		t.Errorf("the rename should only move the renamed article: files = %v, want %v", got, wantTree)
	}

	for _, rel := range []string{"bar-copy.md", "archive/linker.md"} {
		warnings := healWarnings(buf, "diagram-source", rel)
		if len(warnings) != 1 {
			t.Errorf("expected one warning naming %s and the old slug, got %q\nlog:\n%s", rel, warnings, buf)
		} else if !strings.Contains(warnings[0], "misplaced") {
			t.Errorf("the warning should say why %s was not healed: %q", rel, warnings[0])
		}
	}

	linker, err := storage.GetArticle("good-linker")
	if err != nil {
		t.Fatalf("GetArticle failed: %v", err)
	}
	if !strings.Contains(linker.Content, "[[Diagram Renamed]]") {
		t.Errorf("the canonical referrer should still be healed, got:\n%s", linker.Content)
	}
}

// TestRenameHealsReferrersInPlace pins how healing treats two awkward canonical referrers. One whose
// title does not yield its slug used to be renamed to <Slugify(title)>.md by the save, and then was
// skipped with a warning; it is now healed where it is. One whose read fails is left untouched and
// named.
func TestRenameHealsReferrersInPlace(t *testing.T) {
	storage, err := NewStorage(t.TempDir())
	if err != nil {
		t.Fatalf("NewStorage failed: %v", err)
	}
	t.Cleanup(func() { _ = storage.Close() })
	buf := captureLog(t)

	if _, err := storage.SaveArticle("", "Diagram Source", "# Diagram", "", "", "", "seed", nil, ""); err != nil {
		t.Fatalf("SaveArticle failed: %v", err)
	}
	// Edited outside NexWiki: the title changed and the slug did not.
	writeArticleFile(t, storage, "odd-title.md", "title: Something Else\nslug: odd-title\n", "See [[Diagram Source]].\n", time.Now().Add(-time.Hour))

	if _, err := storage.SaveArticle("diagram-source", "Diagram Renamed", "# Diagram", "", "", "", "rename", nil, ""); err != nil {
		t.Fatalf("rename failed: %v", err)
	}
	odd, err := storage.GetArticle("odd-title")
	if err != nil {
		t.Fatalf("the referrer should still be at odd-title: %v", err)
	}
	if !strings.Contains(odd.Content, "[[Diagram Renamed]]") || odd.Title != "Something Else" {
		t.Errorf("odd-title.md should be healed in place with its title kept, got title %q:\n%s", odd.Title, odd.Content)
	}
	if odd.EditSummary != "Auto-healed internal link: 'diagram-source' renamed to 'diagram-renamed'" {
		t.Errorf("unexpected edit summary %q", odd.EditSummary)
	}
	if history, err := storage.GetArticleHistory("odd-title"); err != nil || len(history) != 2 {
		t.Errorf("odd-title should have its original state and the heal in its history, got %d versions (%v)", len(history), err)
	}
	for _, p := range []string{filepath.Join(storage.ArticleDir, "something-else.md"), filepath.Join(storage.HistoryDir, "something-else")} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("healing must not move a referrer to the slug its title yields (stat error %v)", err)
		}
	}
	if warnings := healWarnings(buf, "diagram-source", "odd-title.md"); len(warnings) != 0 {
		t.Errorf("a referrer healed in place needs no warning, got %q", warnings)
	}

	t.Run("unreadable referrer", func(t *testing.T) {
		if _, err := storage.SaveArticle("", "Locked Linker", "See [[Diagram Renamed]].", "", "", "", "seed", nil, ""); err != nil {
			t.Fatalf("SaveArticle failed: %v", err)
		}
		// Cached while readable, so the backlink scan still finds it and only the heal's read fails.
		if _, err := storage.GetBacklinks("diagram-renamed"); err != nil {
			t.Fatalf("GetBacklinks failed: %v", err)
		}
		makeUnreadable(t, filepath.Join(storage.ArticleDir, "locked-linker.md"))

		if _, err := storage.SaveArticle("diagram-renamed", "Diagram Final", "# Diagram", "", "", "", "rename", nil, ""); err != nil {
			t.Fatalf("rename failed: %v", err)
		}
		warnings := healWarnings(buf, "diagram-renamed", "locked-linker.md")
		if len(warnings) != 1 || !strings.Contains(warnings[0], "permission denied") {
			t.Errorf("expected one warning naming locked-linker.md and carrying the read error, got %q\nlog:\n%s", warnings, buf)
		}
	})
}

// TestLifecycleWorkerKeepsPlanLinkedOnlyFromMisplacedDocument pins the deletion guard for misplaced
// documents: one that links to an eligible plan is no backlink, but deleting the plan would break
// its link, so the plan is kept as for any other skipped entry.
func TestLifecycleWorkerKeepsPlanLinkedOnlyFromMisplacedDocument(t *testing.T) {
	s := newLifecycleStorage(t)
	captureLog(t)
	savePlan(t, s, "Copied Linked Archived", StatusArchived)
	backdateStatusChange(t, s, "copied-linked-archived", 400)
	writeArticleFile(t, s, "notes/pointer.md", "title: Pointer\nslug: pointer\n", "See [[Copied Linked Archived]].\n", time.Now().Add(-time.Hour))

	bus := NewEventBus()
	w := &PlanLifecycleWorker{Storage: s, Bus: bus, Cfg: PlanLifecycleConfig{ArchiveAfterDays: 90, DeleteAfterDays: 365}}
	out := captureWorkerLog(w)
	w.Sweep()

	if _, err := s.GetArticle("copied-linked-archived"); err != nil {
		t.Fatal("a plan a misplaced document links to must not be deleted")
	}
	if !strings.Contains(out.String(), misplacedLinkerWarning("copied-linked-archived", 1)) {
		t.Errorf("the kept plan must be warned about with the misplaced linker count, got %q", out.String())
	}
	// The document certainly links to the plan, so the line must not hedge as for an unreadable one.
	if strings.Contains(out.String(), "unreadable") || strings.Contains(out.String(), "may link") {
		t.Errorf("a misplaced linker is not an unreadable entry that may link, got %q", out.String())
	}
	if n := deleteRefusals(bus, "copied-linked-archived"); n != 1 {
		t.Errorf("the refusal must be recorded as one delete-refused activity event, got %d", n)
	}
	if strings.Contains(out.String(), "PERMANENTLY DELETED") {
		t.Errorf("nothing may be deleted, got %q", out.String())
	}
}

// TestLifecycleWorkerDeletesPlanDespiteUnrelatedMisplacedDocuments pins the other side: a misplaced
// document that does not link to the plan is no reason to keep it.
func TestLifecycleWorkerDeletesPlanDespiteUnrelatedMisplacedDocuments(t *testing.T) {
	s := newLifecycleStorage(t)
	captureLog(t)
	savePlan(t, s, "Unlinked Archived", StatusArchived)
	backdateStatusChange(t, s, "unlinked-archived", 400)
	writeArticleFile(t, s, "notes/elsewhere.md", "title: Elsewhere\nslug: elsewhere\n", "No links here.\n", time.Now().Add(-time.Hour))
	// Even a copy of the plan itself, which declares its slug, links to nothing.
	plan := readFile(t, filepath.Join(s.ArticleDir, "unlinked-archived.md"))
	writeWithMtime(t, filepath.Join(s.ArticleDir, "unlinked-archived-copy.md"), []byte(plan), time.Now().Add(-time.Hour))

	w := &PlanLifecycleWorker{Storage: s, Cfg: PlanLifecycleConfig{ArchiveAfterDays: 90, DeleteAfterDays: 365}}
	out := captureWorkerLog(w)
	w.Sweep()

	if _, err := s.GetArticle("unlinked-archived"); err == nil {
		t.Errorf("a plan no document links to should be deleted, got %q", out.String())
	}
	if !strings.Contains(out.String(), "PERMANENTLY DELETED plan 'unlinked-archived'") {
		t.Errorf("the deletion must be logged, got %q", out.String())
	}
}

// newCopiedArticleFixture builds the #151 copy: bar.md, and bar-copy.md holding bar.md's bytes, so it
// declares slug bar. bar-copy was a real document first, so it has history and a search index entry
// of its own, both of which a lookup by its old slug could still reach. It returns a check that
// nothing on disk has changed since.
func newCopiedArticleFixture(t *testing.T, storage *Storage) func() {
	t.Helper()
	if _, err := storage.SaveArticle("", "Bar", "REAL BAR BODY", "", "", "", "seed", nil, ""); err != nil {
		t.Fatalf("SaveArticle failed: %v", err)
	}
	if _, err := storage.SaveArticle("", "Bar Copy", "zanzibarquux", "", "", "", "seed", nil, ""); err != nil {
		t.Fatalf("SaveArticle failed: %v", err)
	}
	barPath := filepath.Join(storage.ArticleDir, "bar.md")
	copyPath := filepath.Join(storage.ArticleDir, "bar-copy.md")
	writeWithMtime(t, copyPath, []byte(readFile(t, barPath)), time.Now().Add(-time.Hour))

	bar, barCopy, tree := readFile(t, barPath), readFile(t, copyPath), articleTree(t, storage)
	history := func(slug string) int {
		t.Helper()
		versions, err := storage.GetArticleHistory(slug)
		if err != nil {
			t.Fatalf("GetArticleHistory failed: %v", err)
		}
		return len(versions)
	}
	barHistory, copyHistory := history("bar"), history("bar-copy")

	return func() {
		t.Helper()
		if got := readFile(t, barPath); got != bar {
			t.Errorf("bar.md changed:\n%s", got)
		}
		if got := readFile(t, copyPath); got != barCopy {
			t.Errorf("bar-copy.md changed:\n%s", got)
		}
		if got := articleTree(t, storage); !reflect.DeepEqual(got, tree) {
			t.Errorf("article files = %v, want %v", got, tree)
		}
		if got := history("bar"); got != barHistory {
			t.Errorf("bar has %d versions, want %d", got, barHistory)
		}
		// Not even a refused delete may take the history under the slug: it is what bar-copy was.
		if got := history("bar-copy"); got != copyHistory {
			t.Errorf("bar-copy has %d versions, want %d", got, copyHistory)
		}
	}
}

// expectNotFound checks err is the not-found result a missing slug gets.
func expectNotFound(t *testing.T, what string, err error) {
	t.Helper()
	if !errors.Is(err, errArticleNotFound) || err.Error() != "article not found: bar-copy" {
		t.Errorf("%s: got %v, want the not-found error a missing article gets", what, err)
	}
}

// TestMisplacedDocumentCannotBeOpenedOrWrittenBySlug pins the lookup half of #152 and #151: a
// misplaced file at <slug>.md is not found by any read or write that starts from that slug. Opened
// by its filename, bar-copy.md said it was bar, so saving it wrote bar.md.
func TestMisplacedDocumentCannotBeOpenedOrWrittenBySlug(t *testing.T) {
	storage, err := NewStorage(t.TempDir())
	if err != nil {
		t.Fatalf("NewStorage failed: %v", err)
	}
	t.Cleanup(func() { _ = storage.Close() })
	captureLog(t)
	unchanged := newCopiedArticleFixture(t, storage)

	_, err = storage.GetArticle("no-such-article")
	if !errors.Is(err, errArticleNotFound) || err.Error() != "article not found: no-such-article" {
		t.Fatalf("a missing article should be not found, got %v", err)
	}

	_, err = storage.GetArticle("bar-copy")
	expectNotFound(t, "GetArticle", err)
	_, err = storage.metaBySlug("bar-copy")
	expectNotFound(t, "metaBySlug", err)
	_, err = storage.ApplyArticleEdit("bar-copy", ArticleEdit{Title: "Bar", Content: "HIJACKED", LoadedVersion: 1})
	expectNotFound(t, "ApplyArticleEdit", err)
	_, err = storage.UpdateArticleTags("bar-copy", []string{"hijacked"}, 0, "")
	expectNotFound(t, "UpdateArticleTags", err)
	_, err = storage.SetStatus("bar-copy", "draft", 0, "")
	expectNotFound(t, "SetStatus", err)
	_, err = storage.RevertArticle("bar-copy", 1)
	expectNotFound(t, "RevertArticle", err)
	// The save refuses the file it would start from, for a caller that does not ask GetArticle first.
	_, err = storage.SaveArticle("bar-copy", "Bar", "HIJACKED", "", "", "", "", nil, "")
	expectNotFound(t, "SaveArticle from the slug", err)
	_, err = storage.SaveArticleWithOverrides("bar-copy", "Bar", "HIJACKED", "", "", "", "", nil, "", ArticleOverrides{KeepSlug: true})
	expectNotFound(t, "SaveArticleWithOverrides in place", err)
	expectNotFound(t, "DeleteArticle", storage.DeleteArticle("bar-copy"))

	// Nor may a create take the path the misplaced file holds.
	if _, err := storage.SaveArticle("", "Bar Copy", "HIJACKED", "", "", "", "", nil, ""); err == nil || !strings.Contains(err.Error(), "a misplaced file already occupies articles/bar-copy.md") {
		t.Errorf("creating over a misplaced file should be refused, got %v", err)
	}

	// bar-copy's index entry is still there, but a hit on it resolves to nothing.
	if !inSearchIndex(t, storage, "bar-copy") {
		t.Fatal("the fixture should leave bar-copy's old index entry, or the search check proves nothing")
	}
	results, err := storage.SearchArticles("zanzibarquux")
	if err != nil {
		t.Fatalf("SearchArticles failed: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("search should not resolve a hit to a misplaced file, got %+v", results)
	}

	unchanged()
	if art, err := storage.GetArticle("bar"); err != nil || art.Content != "REAL BAR BODY" {
		t.Errorf("the real bar should still open, got %+v, %v", art, err)
	}
}

// TestMisplacedDocumentIsNotFoundOverMCPAndREST drives the same refusals through the entry points a
// client uses.
func TestMisplacedDocumentIsNotFoundOverMCPAndREST(t *testing.T) {
	srv := newMCPServer(t)
	captureLog(t)
	unchanged := newCopiedArticleFixture(t, srv.Storage)

	for name, call := range map[string]string{
		"read_article":   `{"name":"read_article","arguments":{"slug":"bar-copy"}}`,
		"save_article":   `{"name":"save_article","arguments":{"slug":"bar-copy","title":"Bar","content":"HIJACKED","loaded_version":1}}`,
		"delete_article": `{"name":"delete_article","arguments":{"slug":"bar-copy"}}`,
		"append_article": `{"name":"append_article","arguments":{"slug":"bar-copy","content":"HIJACKED"}}`,
	} {
		resp := toolCall(t, srv, call)
		if !resp.IsError || !strings.Contains(strings.ToLower(resp.Content[0].Text), "not found") {
			t.Errorf("%s: expected a not-found error, got %+v", name, resp)
		}
	}

	for _, tc := range []struct {
		name, method, body string
		handle             func(http.ResponseWriter, *http.Request)
	}{
		{"GET", http.MethodGet, "", srv.HandleGetArticle},
		{"PUT", http.MethodPut, `{"title":"Bar","content":"HIJACKED","loaded_version":1}`, srv.HandleUpdateArticle},
		{"PUT tags", http.MethodPut, `{"tags":["hijacked"]}`, srv.HandleUpdateArticleTags},
		{"POST verify", http.MethodPost, "", srv.HandleVerifyArticle},
		{"DELETE", http.MethodDelete, "", srv.HandleDeleteArticle},
		{"POST revert", http.MethodPost, `{"version":1}`, srv.HandleRevertArticle},
	} {
		req := httptest.NewRequest(tc.method, "/api/articles/bar-copy", strings.NewReader(tc.body))
		req.SetPathValue("slug", "bar-copy")
		w := httptest.NewRecorder()
		tc.handle(w, req)
		if w.Code != http.StatusNotFound {
			t.Errorf("%s /api/articles/bar-copy: status %d, want 404 (%s)", tc.name, w.Code, w.Body.String())
		}
	}

	unchanged()
}

// TestMisplacedDocumentsOutOfSlugForm pins the slug-form half of the rule: a slug no lookup can
// reach, because lookups slugify what they are given, makes a document misplaced even where its
// filename matches it byte for byte, and a filename that differs from the slug only in case makes
// it misplaced on any filesystem. Each gets a remedy that fits it.
func TestMisplacedDocumentsOutOfSlugForm(t *testing.T) {
	srv := newMCPServer(t)
	buf := captureLog(t)
	// Bar.md is only misplaced where case matters, so that is the mode tested, whatever the host.
	srv.Storage.caseInsensitive = false

	old := time.Now().Add(-time.Hour)
	writeArticleFile(t, srv.Storage, "Foo.md", "title: Foo\nslug: Foo\n", "body\n", old)
	writeArticleFile(t, srv.Storage, "my-notes.md", "title: My Notes\nslug: My Notes\n", "body\n", old)
	writeArticleFile(t, srv.Storage, "Bar.md", "title: Bar\nslug: bar\n", "body\n", old)
	writeArticleFile(t, srv.Storage, "bang.md", "title: Bang\nslug: \"!!!\"\n", "body\n", old)
	writeArticleFile(t, srv.Storage, "notes/Deep.md", "title: Deep\nslug: Deep\n", "body\n", old)

	articles, err := srv.Storage.ListArticles()
	if err != nil {
		t.Fatalf("ListArticles failed: %v", err)
	}
	if got := articleSlugs(articles); len(got) != 0 {
		t.Errorf("none of these is a document, got %v", got)
	}

	resp := toolCall(t, srv, `{"name":"wiki_health","arguments":{}}`)
	var out HealthOutput
	decodeStructured(t, resp, &out)
	want := []MisplacedDocument{
		{Path: "Bar.md", Slug: "bar"},
		{Path: "Foo.md", Slug: "Foo"},
		{Path: "bang.md", Slug: "!!!"},
		{Path: "my-notes.md", Slug: "My Notes"},
		{Path: "notes/Deep.md", Slug: "Deep"},
	}
	if got := findingDocuments(out.MisplacedDocuments); !reflect.DeepEqual(got, want) {
		t.Fatalf("misplaced_documents = %+v, want %+v", got, want)
	}
	text := resp.Content[0].Text
	for _, remedy := range []string{
		"- Bar.md (slug \"bar\") — not a document, so no listing, search, lookup, or check includes it. Rename it to articles/bar.md, in lowercase, or delete it if it's a copy.\n",
		"- Foo.md (slug \"Foo\") — not a document, so no listing, search, lookup, or check includes it. Change its slug to \"foo\" and move it to articles/foo.md, or delete it if it's a copy.\n",
		"- bang.md (slug \"!!!\") — not a document, so no listing, search, lookup, or check includes it. Change its slug to \"bang\" to match its filename, or delete it if it's a copy.\n",
		"- my-notes.md (slug \"My Notes\") — not a document, so no listing, search, lookup, or check includes it. Change its slug to \"my-notes\" to match its filename, or delete it if it's a copy.\n",
		"- notes/Deep.md (slug \"Deep\") — not a document, so no listing, search, lookup, or check includes it. Change its slug to \"deep\" and move it to articles/deep.md, or delete it if it's a copy.\n",
	} {
		if !strings.Contains(text, remedy) {
			t.Errorf("prose is missing %q:\n%s", remedy, text)
		}
	}
	assertMatchesOutputSchema(t, resp, wikiHealthTool.Output)

	for path, want := range map[string]string{
		"Foo.md":      `its slug "Foo" is not in slug form (lowercase letters, digits, and hyphens)`,
		"my-notes.md": `its slug "My Notes" is not in slug form`,
		"Bar.md":      `its slug is "bar", so it belongs at bar.md directly in the article directory, and file names are compared exactly, case included`,
	} {
		if warnings := misplacedWarnings(t, buf, path); len(warnings) != 1 || !strings.Contains(warnings[0], want) {
			t.Errorf("expected one warning for %s containing %q, got %q", path, want, warnings)
		}
	}

	// my-notes.md is exactly where the slug it would take lives, and still not found by it.
	if _, err := srv.Storage.GetArticle("my-notes"); !errors.Is(err, errArticleNotFound) {
		t.Errorf("a file whose slug is not in slug form must not be found, got %v", err)
	}
}

// TestWikiStatisticsCountsMisplacedDocuments pins that get_wiki_statistics counts misplaced
// documents as wiki_health does, and points to it for the list.
func TestWikiStatisticsCountsMisplacedDocuments(t *testing.T) {
	srv := newMCPServer(t)
	captureLog(t)

	stats := func() (StatisticsOutput, ToolResponse) {
		t.Helper()
		resp := toolCall(t, srv, `{"name":"get_wiki_overview","arguments":{"include_stats":true}}`)
		var overview OverviewOutput
		decodeStructured(t, resp, &overview)
		var out StatisticsOutput
		if overview.Statistics != nil {
			out = *overview.Statistics
		}
		return out, resp
	}

	clean, resp := stats()
	if clean.MisplacedDocumentCount != 0 {
		t.Errorf("a clean wiki should report 0 misplaced documents, got %d:\n%s", clean.MisplacedDocumentCount, resp.Content[0].Text)
	}

	if _, err := srv.Storage.SaveArticle("", "Bar", "# Bar", "", "", "", "seed", nil, ""); err != nil {
		t.Fatalf("SaveArticle failed: %v", err)
	}
	before, _ := stats()
	writeWithMtime(t, filepath.Join(srv.Storage.ArticleDir, "bar-copy.md"),
		[]byte(readFile(t, filepath.Join(srv.Storage.ArticleDir, "bar.md"))), time.Now().Add(-time.Hour))
	writeArticleFile(t, srv.Storage, "notes/sub-doc.md", "title: Sub Doc\nslug: sub-doc\n", "body\n", time.Now().Add(-time.Hour))

	got, resp := stats()
	health := healthReport(t, srv, `{}`)
	if got.MisplacedDocumentCount != 2 || got.MisplacedDocumentCount != health.MisplacedDocumentCount {
		t.Errorf("get_wiki_overview reports %d misplaced documents, wiki_health %d; want 2 from both",
			got.MisplacedDocumentCount, health.MisplacedDocumentCount)
	}
	if got.TotalArticles != before.TotalArticles || got.UnreadableFileCount != 0 {
		t.Errorf("misplaced documents must not count as articles or unreadable files, got %+v (before %+v)", got, before)
	}
	text := resp.Content[0].Text
	if !strings.Contains(text, "2 misplaced docs") {
		t.Errorf("prose is missing '2 misplaced docs':\n%s", text)
	}

	assertMatchesOutputSchema(t, resp, getWikiOverviewTool.Output)
}

// TestIsSlugFormAgreesWithSlugify pins the regex-free check the walks run on every file against the
// function it stands in for, over every short string of characters Slugify treats specially and a
// sample of longer ones.
func TestIsSlugFormAgreesWithSlugify(t *testing.T) {
	// Kept letters and digits, the separators it rewrites or collapses, the whitespace \s keeps and
	// the one it does not (\v), an uppercase and a non-ASCII letter, the Kelvin sign (which lowercases
	// to ASCII k), and a byte that is not UTF-8.
	alphabet := []string{"a", "z", "0", "-", "_", " ", "\t", "\n", "\f", "\r", "\v", "A", "é", "K", "\xff"}
	check := func(s string) {
		if got, want := isSlugForm(s), Slugify(s) == s; got != want {
			t.Errorf("isSlugForm(%q) = %v, but Slugify gives %q", s, got, Slugify(s))
		}
	}
	var build func(prefix string, depth int)
	build = func(prefix string, depth int) {
		check(prefix)
		if depth == 0 {
			return
		}
		for _, c := range alphabet {
			build(prefix+c, depth-1)
		}
	}
	build("", 4)
	for _, s := range []string{"good-one", "bench-article-100", "a-b-c-d-e", "a--b", "-a", "a-", "My Notes", "my_notes", "2026-09-17", "x\ty"} {
		check(s)
	}
}

// TestCanonicalFilenameFollowsFilesystemCaseSensitivity pins that a filename is compared with its
// slug the way the article directory's filesystem compares names. Where case is ignored, a lookup of
// bar opens Bar.md, so the walks must count it as bar too; where case matters, no lookup can reach
// it, so it is misplaced, with a remedy to rename it. Each mode is set explicitly, and only the walks
// are checked, so this holds whatever the host filesystem does. A slug out of slug form is misplaced
// in both.
func TestCanonicalFilenameFollowsFilesystemCaseSensitivity(t *testing.T) {
	for _, tc := range []struct {
		name            string
		caseInsensitive bool
		listed          []string
		misplaced       []MisplacedDocumentFinding
	}{
		{
			name:   "case-sensitive",
			listed: []string{},
			misplaced: []MisplacedDocumentFinding{
				{Path: "Bar.md", Slug: "bar", Remedy: "Rename it to articles/bar.md, in lowercase, or delete it if it's a copy."},
				{Path: "Foo.md", Slug: "Foo", Remedy: "Change its slug to \"foo\" and move it to articles/foo.md, or delete it if it's a copy."},
				{Path: "Notes.md", Slug: "notes-copy", Remedy: "Move it to articles/notes-copy.md, or delete it if it's a copy."},
			},
		},
		{
			name:            "case-insensitive",
			caseInsensitive: true,
			listed:          []string{"bar"},
			misplaced: []MisplacedDocumentFinding{
				{Path: "Foo.md", Slug: "Foo", Remedy: "Change its slug to \"foo\" to match its filename, or delete it if it's a copy."},
				{Path: "Notes.md", Slug: "notes-copy", Remedy: "Move it to articles/notes-copy.md (or change its slug to \"notes\" to match its filename), or delete it if it's a copy."},
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := newMCPServer(t)
			captureLog(t)
			srv.Storage.caseInsensitive = tc.caseInsensitive

			old := time.Now().Add(-time.Hour)
			writeArticleFile(t, srv.Storage, "Bar.md", "title: Bar\nslug: bar\n", "body\n", old)
			writeArticleFile(t, srv.Storage, "Foo.md", "title: Foo\nslug: Foo\n", "body\n", old)
			writeArticleFile(t, srv.Storage, "Notes.md", "title: Notes Copy\nslug: notes-copy\n", "body\n", old)

			articles, err := srv.Storage.ListArticles()
			if err != nil {
				t.Fatalf("ListArticles failed: %v", err)
			}
			if got := articleSlugs(articles); !reflect.DeepEqual(got, tc.listed) {
				t.Errorf("ListArticles = %v, want %v", got, tc.listed)
			}
			out := healthReport(t, srv, `{}`)
			if !reflect.DeepEqual(out.MisplacedDocuments, tc.misplaced) {
				t.Errorf("misplaced_documents = %+v, want %+v", out.MisplacedDocuments, tc.misplaced)
			}
		})
	}
}

// TestDetectCaseInsensitive runs the detector on the host's real filesystem. Which answer is right
// depends on the host, so it only checks that there is a consistent one, that nothing is left in the
// directory, including a probe a crash left behind, and that NewStorage uses it.
func TestDetectCaseInsensitive(t *testing.T) {
	captureLog(t)
	dir := t.TempDir()
	emptyAfter := func(when string) {
		t.Helper()
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatalf("ReadDir failed: %v", err)
		}
		if len(entries) != 0 {
			t.Errorf("%s: detection left %v behind", when, entries)
		}
	}

	first := detectCaseInsensitive(dir)
	t.Logf("the host's temp directory ignores case: %v", first)
	emptyAfter("a clean run")

	if err := os.WriteFile(filepath.Join(dir, caseProbeName), []byte("left by a crash"), 0o600); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}
	if got := detectCaseInsensitive(dir); got != first {
		t.Errorf("a leftover probe changed the answer from %v to %v", first, got)
	}
	emptyAfter("a run over a leftover probe")

	// A directory it cannot write to is treated as case-sensitive rather than failing.
	if detectCaseInsensitive(filepath.Join(dir, "missing")) {
		t.Error("a directory that cannot be probed should be treated as case-sensitive")
	}
	emptyAfter("a failed run")

	storage, err := NewStorage(t.TempDir())
	if err != nil {
		t.Fatalf("NewStorage failed: %v", err)
	}
	t.Cleanup(func() { _ = storage.Close() })
	if storage.caseInsensitive != first {
		t.Errorf("NewStorage detected %v, the detector %v", storage.caseInsensitive, first)
	}
	entries, err := os.ReadDir(storage.ArticleDir)
	if err != nil {
		t.Fatalf("ReadDir failed: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != "home.md" {
		t.Errorf("a new wiki should hold only its seeded home page, got %v", entries)
	}
}

// stubCaseProbeLstat routes the detector's stat of the probe's uppercase name through upper for the
// rest of the test. upper gets the probe's own path and returns what the stat of the uppercase name
// should: the probe's info where case is ignored, an error where it is not, and so on.
func stubCaseProbeLstat(t *testing.T, upper func(probe string) (fs.FileInfo, error)) {
	t.Helper()
	prev := caseProbeLstat
	caseProbeLstat = func(path string) (fs.FileInfo, error) {
		if filepath.Base(path) == strings.ToUpper(caseProbeName) {
			return upper(filepath.Join(filepath.Dir(path), caseProbeName))
		}
		return prev(path)
	}
	t.Cleanup(func() { caseProbeLstat = prev })
}

// TestDetectCaseInsensitiveAnswers pins each answer the detector can give, whatever the host
// filesystem does, by standing in for the stat of the probe's uppercase name.
func TestDetectCaseInsensitiveAnswers(t *testing.T) {
	for _, tc := range []struct {
		name  string
		upper func(probe string) (fs.FileInfo, error)
		want  bool
		warns bool
	}{
		{name: "the uppercase name is the probe", upper: os.Lstat, want: true},
		{name: "no file has the uppercase name", upper: func(string) (fs.FileInfo, error) {
			return nil, &os.PathError{Op: "lstat", Path: "upper", Err: fs.ErrNotExist}
		}},
		{name: "the uppercase name is another file", upper: func(probe string) (fs.FileInfo, error) {
			return os.Lstat(filepath.Dir(probe))
		}},
		{name: "the stat fails", warns: true, upper: func(string) (fs.FileInfo, error) {
			return nil, &os.PathError{Op: "lstat", Path: "upper", Err: syscall.EIO}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			buf := captureLog(t)
			stubCaseProbeLstat(t, tc.upper)
			dir := t.TempDir()
			if got := detectCaseInsensitive(dir); got != tc.want {
				t.Errorf("detectCaseInsensitive = %v, want %v", got, tc.want)
			}
			if warned := strings.Contains(buf.String(), "Warning: could not tell whether the article directory ignores case"); warned != tc.warns {
				t.Errorf("warned = %v, want %v; log:\n%s", warned, tc.warns, buf)
			}
			if entries, _ := os.ReadDir(dir); len(entries) != 0 {
				t.Errorf("detection left %v behind", entries)
			}
		})
	}
}

// TestDetectCaseInsensitiveDoesNotFollowSymlinks pins that whatever holds the probe's name is
// replaced, never written through: a symlink by that name must leave its target as it was, and a
// dangling one must not create its target.
func TestDetectCaseInsensitiveDoesNotFollowSymlinks(t *testing.T) {
	captureLog(t)
	dir, elsewhere := t.TempDir(), t.TempDir()
	target := filepath.Join(elsewhere, "precious.txt")
	if err := os.WriteFile(target, []byte("do not truncate"), 0o644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}
	link := filepath.Join(dir, caseProbeName)
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	detectCaseInsensitive(dir)
	if got := readFile(t, target); got != "do not truncate" {
		t.Errorf("the symlink's target was written through: %q", got)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("detection left %v behind", entries)
	}

	dangling := filepath.Join(elsewhere, "not-there.txt")
	if err := os.Symlink(dangling, link); err != nil {
		t.Fatalf("Symlink failed: %v", err)
	}
	detectCaseInsensitive(dir)
	if _, err := os.Lstat(dangling); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("a dangling symlink's target was created (stat error %v)", err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("detection left %v behind", entries)
	}
}

// TestCanonicalFilenameFoldsASCIIOnly pins that ignoring case in a filename means ASCII case only.
// Unicode folding takes the Kelvin sign (U+212A) for k, which NTFS does not, so a file named with one
// must not count as the document a lookup of its ASCII slug would open.
func TestCanonicalFilenameFoldsASCIIOnly(t *testing.T) {
	storage := &Storage{ArticleDir: filepath.Join(t.TempDir(), "articles"), caseInsensitive: true}
	for name, want := range map[string]bool{
		"key.md":      true,
		"KEY.md":      true,
		"Key.md":      true,
		"\u212Aey.md": false, // KELVIN SIGN
		"ke\u0130.md": false, // LATIN CAPITAL LETTER I WITH DOT ABOVE
	} {
		if got := storage.isCanonical(filepath.Join(storage.ArticleDir, name), "key"); got != want {
			t.Errorf("isCanonical(%q, key) = %v, want %v", name, got, want)
		}
	}
	if !asciiEqualFold("Bar-Copy", "bar-copy") || asciiEqualFold("\u212A", "k") || asciiLower("\u212AEY") != "\u212Aey" {
		t.Error("asciiEqualFold and asciiLower must fold ASCII letters and nothing else")
	}
}

// TestMisplacedRemedyNeverOverwrites pins the remedy rules case by case: moving a document where its
// slug says is suggested only when nothing is there and no other misplaced document is told the
// same, and failing that, taking its filename's slug is suggested only when that is in slug form and
// free. Each case is a scan's worth of documents, unreadable files, and misplaced documents.
func TestMisplacedRemedyNeverOverwrites(t *testing.T) {
	const (
		deleteIt = "or delete it if it's a copy."
		taken    = "already exists, so do not move it there. Delete it if it's a copy, or give it an unused slug and store it as articles/<slug>.md."
	)
	claimed := func(n int, slug string) string {
		return fmt.Sprintf("%d misplaced documents claim articles/%s.md: move at most one there, and delete or re-slug the others.", n, slug)
	}
	doc := func(path, slug string) MisplacedDocument { return MisplacedDocument{Path: path, Slug: slug} }

	for _, tc := range []struct {
		name            string
		caseInsensitive bool
		documents       []string // slugs of the wiki's documents
		unreadable      []string // paths of unreadable files
		misplaced       []MisplacedDocument
		want            []string // one remedy per misplaced document, in order
	}{
		// One misplaced document at a time.
		{name: "a copy whose slug is taken", documents: []string{"bar"},
			misplaced: []MisplacedDocument{doc("bar-copy.md", "bar")},
			want:      []string{`articles/bar.md already exists, so do not move it there. Change its slug to "bar-copy" to match its filename, ` + deleteIt}},
		{name: "a copy whose filename's slug is taken too", documents: []string{"bar", "bar-copy"},
			misplaced: []MisplacedDocument{doc("bar-copy.md", "bar")},
			want:      []string{"articles/bar.md " + taken}},
		{name: "a copy in a folder whose slug is taken", documents: []string{"bar"},
			misplaced: []MisplacedDocument{doc("archive/bar.md", "bar")},
			want:      []string{"articles/bar.md " + taken}},
		{name: "a copy whose filename is not a slug", documents: []string{"bar"},
			misplaced: []MisplacedDocument{doc("Bar Copy.md", "bar")},
			want:      []string{"articles/bar.md " + taken}},
		{name: "a destination held by an unreadable file", unreadable: []string{"bar.md"},
			misplaced: []MisplacedDocument{doc("notes/bar.md", "bar")},
			want:      []string{"articles/bar.md " + taken}},
		{name: "a free slug",
			misplaced: []MisplacedDocument{doc("bar-copy.md", "bar")},
			want:      []string{`Move it to articles/bar.md (or change its slug to "bar-copy" to match its filename), ` + deleteIt}},
		{name: "a free slug in a folder",
			misplaced: []MisplacedDocument{doc("notes/bar.md", "bar")},
			want:      []string{"Move it to articles/bar.md, " + deleteIt}},
		{name: "a case-only mismatch where case matters",
			misplaced: []MisplacedDocument{doc("Bar.md", "bar")},
			want:      []string{"Rename it to articles/bar.md, in lowercase, " + deleteIt}},
		{name: "a case-only mismatch whose lowercase name is taken", documents: []string{"bar"},
			misplaced: []MisplacedDocument{doc("Bar.md", "bar")},
			want:      []string{"articles/bar.md " + taken}},
		{name: "a slug out of slug form, filename matching once corrected",
			misplaced: []MisplacedDocument{doc("my-notes.md", "My Notes")},
			want:      []string{`Change its slug to "my-notes" to match its filename, ` + deleteIt}},
		{name: "a slug out of slug form, corrected slug taken", documents: []string{"foo"},
			misplaced: []MisplacedDocument{doc("Foo.md", "Foo")},
			want:      []string{"articles/foo.md " + taken}},
		{name: "a slug with no slug form",
			misplaced: []MisplacedDocument{doc("notes/bang.md", "!!!")},
			want:      []string{"Give it an unused slug of lowercase letters, digits, and hyphens and store it as articles/<slug>.md, " + deleteIt}},
		{name: "a filename's slug offered where case is ignored", caseInsensitive: true, documents: []string{"bar"},
			misplaced: []MisplacedDocument{doc("Bar-Copy.md", "bar")},
			want:      []string{`articles/bar.md already exists, so do not move it there. Change its slug to "bar-copy" to match its filename, ` + deleteIt}},

		// Several misplaced documents in one scan.
		{name: "two folders claiming one free destination",
			misplaced: []MisplacedDocument{doc("archive/bar.md", "bar"), doc("backup/bar.md", "bar")},
			want:      []string{claimed(2, "bar"), claimed(2, "bar")}},
		{name: "an OKF bundle unzipped into the article directory twice",
			misplaced: []MisplacedDocument{
				doc("export 2/aimemories/fact.md", "fact"), doc("export 2/wiki/bar.md", "bar"),
				doc("export/aimemories/fact.md", "fact"), doc("export/wiki/bar.md", "bar"),
			},
			want: []string{claimed(2, "fact"), claimed(2, "bar"), claimed(2, "fact"), claimed(2, "bar")}},
		{name: "two slugs that become the same slug",
			misplaced: []MisplacedDocument{doc("drafts/bar.md", "bar"), doc("notes/Bar.md", "Bar")},
			want:      []string{claimed(2, "bar"), claimed(2, "bar")}},
		{name: "a lowercase rename and a move to one destination",
			misplaced: []MisplacedDocument{doc("Bar.md", "bar"), doc("notes/bar.md", "bar")},
			want:      []string{claimed(2, "bar"), claimed(2, "bar")}},
		{name: "three claims, one with a filename's slug to take instead",
			misplaced: []MisplacedDocument{doc("a/bar.md", "bar"), doc("bar-copy.md", "bar"), doc("b/bar.md", "bar")},
			want: []string{
				claimed(3, "bar"),
				strings.TrimSuffix(claimed(3, "bar"), ".") + ` (this one could take the slug "bar-copy" to match its filename).`,
				claimed(3, "bar"),
			}},
		{name: "a destination another misplaced file already holds",
			misplaced: []MisplacedDocument{doc("drafts/my-notes.md", "my-notes"), doc("my-notes.md", "My Notes")},
			want: []string{
				"articles/my-notes.md " + taken,
				`Change its slug to "my-notes" to match its filename, ` + deleteIt,
			}},
		{name: "separate destinations",
			misplaced: []MisplacedDocument{doc("a/one.md", "one"), doc("b/two.md", "two")},
			want:      []string{"Move it to articles/one.md, " + deleteIt, "Move it to articles/two.md, " + deleteIt}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			graph := &LinkGraph{Meta: map[string]Article{}, Misplaced: tc.misplaced}
			for _, slug := range tc.documents {
				graph.Meta[slug] = Article{Slug: slug}
			}
			for _, p := range tc.unreadable {
				graph.Unreadable = append(graph.Unreadable, UnreadableFile{Path: p})
			}
			findings := misplacedFindings(graph, len(tc.misplaced), tc.caseInsensitive)
			var got []string
			for _, f := range findings {
				got = append(got, f.Remedy)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("remedies =\n  %q\nwant\n  %q", got, tc.want)
			}

			// A report cut short by its limit still counts every claim.
			if len(tc.misplaced) > 1 {
				capped := misplacedFindings(graph, 1, tc.caseInsensitive)
				if len(capped) != 1 || capped[0].Remedy != tc.want[0] {
					t.Errorf("with limit 1, remedy = %+v, want %q", capped, tc.want[0])
				}
			}
		})
	}
}

// TestMisplacedAndUnreadableRecordsDoNotAlternate pins that one file version has one failure record,
// whichever kind came first. A misplaced file whose body then cannot be read is found misplaced by
// scans that only need its cached metadata and unreadable by scans that read the body, and if each
// kind replaced the other the two would take turns warning on every scan.
func TestMisplacedAndUnreadableRecordsDoNotAlternate(t *testing.T) {
	storage, _ := newMisplacedFixture(t)
	buf := captureLog(t)

	t.Run("records", func(t *testing.T) {
		path := filepath.Join(storage.ArticleDir, "records.md")
		writeArticleFile(t, storage, "records.md", "title: Records\nslug: other\n", "body\n", time.Now().Add(-time.Hour))
		info, err := os.Lstat(path)
		if err != nil {
			t.Fatalf("Lstat failed: %v", err)
		}
		if !storage.cache.noteMisplaced(path, info) {
			t.Fatal("the first record of a version should be news")
		}
		for i := 0; i < 3; i++ {
			if storage.cache.noteFailure(path, info) {
				t.Errorf("round %d: an unreadable record replaced the misplaced one for the same version", i)
			}
			if storage.cache.noteMisplaced(path, info) {
				t.Errorf("round %d: a misplaced record replaced the unreadable one for the same version", i)
			}
		}
	})

	t.Run("scans", func(t *testing.T) {
		// Cached while readable, so the listing still finds it misplaced once it cannot be read.
		if _, err := storage.ListArticles(); err != nil {
			t.Fatalf("ListArticles failed: %v", err)
		}
		makeUnreadable(t, filepath.Join(storage.ArticleDir, "archive", "sub-doc.md"))
		for i := 0; i < 3; i++ {
			scanEverything(t, storage)
		}
		if warnings := unreadableWarnings(t, buf, "archive/sub-doc.md"); len(warnings) != 1 {
			t.Errorf("expected only the first warning for the file, got %d: %q", len(warnings), warnings)
		}
	})
}

// TestRefusedCreateLeavesNothingBehind pins that a create refused because a misplaced file holds its
// path is refused before it touches anything, including the history directory it would have used.
func TestRefusedCreateLeavesNothingBehind(t *testing.T) {
	storage, err := NewStorage(t.TempDir())
	if err != nil {
		t.Fatalf("NewStorage failed: %v", err)
	}
	t.Cleanup(func() { _ = storage.Close() })
	captureLog(t)
	if _, err := storage.SaveArticle("", "Bar", "REAL BAR BODY", "", "", "", "seed", nil, ""); err != nil {
		t.Fatalf("SaveArticle failed: %v", err)
	}
	copyPath := filepath.Join(storage.ArticleDir, "fresh-copy.md")
	writeWithMtime(t, copyPath, []byte(readFile(t, filepath.Join(storage.ArticleDir, "bar.md"))), time.Now().Add(-time.Hour))
	before := readFile(t, copyPath)

	if _, err := storage.SaveArticle("", "Fresh Copy", "HIJACKED", "", "", "", "", nil, ""); err == nil || !strings.Contains(err.Error(), "a misplaced file already occupies articles/fresh-copy.md") {
		t.Fatalf("creating over a misplaced file should be refused, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(storage.HistoryDir, "fresh-copy")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("a refused create must not leave a history directory (stat error %v)", err)
	}
	if got := readFile(t, copyPath); got != before {
		t.Errorf("fresh-copy.md changed:\n%s", got)
	}
	if inSearchIndex(t, storage, "fresh-copy") {
		t.Error("a refused create must not index anything")
	}
}

// TestLoadForIndexSkipsMisplacedSilently pins that boot indexing does not report a misplaced file as
// unreadable. The walks normally record it first, which hides the difference, so the load is called
// with no scan before it: a file that became misplaced after the listing is in exactly that state, and
// an unreadable record would then stand for its version and keep the misplaced warning from ever
// appearing.
func TestLoadForIndexSkipsMisplacedSilently(t *testing.T) {
	storage, err := NewStorage(t.TempDir())
	if err != nil {
		t.Fatalf("NewStorage failed: %v", err)
	}
	t.Cleanup(func() { _ = storage.Close() })
	buf := captureLog(t)

	writeArticleFile(t, storage, "home.md", "title: Home\nslug: dashboard\n", "body\n", time.Now().Add(time.Hour))
	if art, ok := storage.loadForIndex("home"); ok {
		t.Fatalf("a misplaced home page must not load for indexing, got %+v", art)
	}
	if strings.Contains(buf.String(), "home.md") {
		t.Errorf("loading a misplaced file for indexing must not report it:\n%s", buf)
	}
	if _, err := storage.ListArticles(); err != nil {
		t.Fatalf("ListArticles failed: %v", err)
	}
	if warnings := misplacedWarnings(t, buf, "home.md"); len(warnings) != 1 {
		t.Errorf("the listing should then report it as misplaced, got %q\nlog:\n%s", warnings, buf)
	}
}

// TestListArticlesReportsMisplacedHomeCopies pins that the listing on its own reports a misplaced
// file declaring slug home, rather than dropping it with the real home page it resembles. The other
// scans report it too, so only a listing is run here.
func TestListArticlesReportsMisplacedHomeCopies(t *testing.T) {
	storage, err := NewStorage(t.TempDir())
	if err != nil {
		t.Fatalf("NewStorage failed: %v", err)
	}
	t.Cleanup(func() { _ = storage.Close() })
	buf := captureLog(t)

	home := readFile(t, filepath.Join(storage.ArticleDir, "home.md"))
	for _, rel := range []string{"home-copy.md", "archive/home.md"} {
		writeWithMtime(t, filepath.Join(storage.ArticleDir, filepath.FromSlash(rel)), []byte(home), time.Now().Add(-time.Hour))
	}
	if _, err := storage.ListArticles(); err != nil {
		t.Fatalf("ListArticles failed: %v", err)
	}
	for _, rel := range []string{"home-copy.md", "archive/home.md"} {
		if warnings := misplacedWarnings(t, buf, rel); len(warnings) != 1 {
			t.Errorf("expected the listing to report %s once, got %q", rel, warnings)
		}
	}
	if warnings := misplacedWarnings(t, buf, "home.md"); len(warnings) != 0 {
		t.Errorf("the real home page is not misplaced, got %q", warnings)
	}
}

// TestMisplacedCopyOfTargetCountsAsLinker pins the conservative reading for plan deletion: a misplaced
// copy of a plan declares the plan's slug, but it is not the plan, so a link in it to the plan is not a
// self-link to skip. It counts, and keeps the plan.
func TestMisplacedCopyOfTargetCountsAsLinker(t *testing.T) {
	s := newLifecycleStorage(t)
	captureLog(t)
	savePlan(t, s, "Copied Plan", StatusArchived)
	backdateStatusChange(t, s, "copied-plan", 400)
	plan := readFile(t, filepath.Join(s.ArticleDir, "copied-plan.md"))
	writeWithMtime(t, filepath.Join(s.ArticleDir, "copied-plan-copy.md"), []byte(plan+"\nSee [[Copied Plan]].\n"), time.Now().Add(-time.Hour))

	scan, err := s.scanBacklinks("copied-plan")
	if err != nil {
		t.Fatalf("scanBacklinks failed: %v", err)
	}
	if want := []MisplacedDocument{{Path: "copied-plan-copy.md", Slug: "copied-plan"}}; !reflect.DeepEqual(scan.misplaced, want) {
		t.Errorf("misplaced linkers = %+v, want %+v", scan.misplaced, want)
	}

	w := &PlanLifecycleWorker{Storage: s, Cfg: PlanLifecycleConfig{ArchiveAfterDays: 90, DeleteAfterDays: 365}}
	out := captureWorkerLog(w)
	w.Sweep()
	if _, err := s.GetArticle("copied-plan"); err != nil {
		t.Errorf("a plan its misplaced copy links to must be kept, got %q", out.String())
	}
	if !strings.Contains(out.String(), misplacedLinkerWarning("copied-plan", 1)) {
		t.Errorf("the refusal should count the copy as a misplaced linker, got %q", out.String())
	}
}

// TestLifecycleWorkerNamesEachKindOfSkippedLinker pins the refusal's wording when both kinds keep a
// plan: an unreadable entry may link to it, while a misplaced document does.
func TestLifecycleWorkerNamesEachKindOfSkippedLinker(t *testing.T) {
	s := newLifecycleStorage(t)
	captureLog(t)
	savePlan(t, s, "Doubly Kept", StatusArchived)
	backdateStatusChange(t, s, "doubly-kept", 400)
	writeWithMtime(t, filepath.Join(s.ArticleDir, "broken.md"), []byte("---\ntitle: [unclosed\n---\nbody\n"), time.Now().Add(-time.Hour))
	writeArticleFile(t, s, "notes/one.md", "title: One\nslug: one\n", "See [[Doubly Kept]].\n", time.Now().Add(-time.Hour))
	writeArticleFile(t, s, "notes/two.md", "title: Two\nslug: two\n", "See [[Doubly Kept]].\n", time.Now().Add(-time.Hour))

	w := &PlanLifecycleWorker{Storage: s, Cfg: PlanLifecycleConfig{ArchiveAfterDays: 90, DeleteAfterDays: 365}}
	out := captureWorkerLog(w)
	w.Sweep()
	want := "refusing to delete plan 'doubly-kept' — the backlink scan skipped 1 unreadable entry that may link to it, and 2 misplaced documents link to it. Later sweeps check again; wiki_health lists what to fix."
	if !strings.Contains(out.String(), want) {
		t.Errorf("worker log is missing %q, got %q", want, out.String())
	}
}
