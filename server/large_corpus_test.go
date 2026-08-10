package server

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Large-corpus coverage. Everything else in this repo was exercised against the real 83-document
// wiki, which is small enough that an O(N^2) shape or an unbounded response is invisible. §0 of the
// code review listed "large-corpus behavior" as the last untested surface, and every other item
// closed off that list turned up a defect that reading the code had not.
//
// The expensive measurements are benchmarks, which `go test ./...` does not run, so the default CI
// path stays fast. The properties that must always hold run as ordinary tests, at a size chosen to
// be meaningful without dominating the suite.

// corpusMix describes the document blend to generate. The real wiki is mostly Wiki articles with a
// tail of agent documents (48 Wiki / 27 agent docs / home at the time of the review), and the
// health checks behave differently per type — orphan detection is Wiki-only, the unsourced check is
// Memory-only — so a single-type corpus would exercise none of that.
type corpusMix struct {
	wikiPct, memoryPct, planPct int // skills take the remainder
}

var realisticMix = corpusMix{wikiPct: 60, memoryPct: 20, planPct: 10}

func (m corpusMix) typeFor(i int) string {
	switch bucket := i % 100; {
	case bucket < m.wikiPct:
		return ContentTypeWiki
	case bucket < m.wikiPct+m.memoryPct:
		return ContentTypeMemory
	case bucket < m.wikiPct+m.memoryPct+m.planPct:
		return ContentTypePlan
	default:
		return ContentTypeSkill
	}
}

// corpusDoc builds one document with field lengths in the range real documents occupy. Titles and
// descriptions are deliberately not "Doc 12": they feed straight into the resources/list payload,
// and sizing that against toy strings would understate it by a factor of two.
func corpusDoc(i int, mix corpusMix) *Article {
	docType := mix.typeFor(i)
	title := fmt.Sprintf("Corpus Doc %d — Notes On A Subject With A Realistic Title Length", i)

	body := fmt.Sprintf("# %s\n\nDocument %d.\n\nBody prose long enough that metadata-only parsing "+
		"and full reads diverge, which is the distinction the article cache exists to exploit. "+
		"Real documents are paragraphs, not sentences, so a corpus of one-liners would make every "+
		"read look cheaper than it is.\n", title, i)
	// Every third document links out, to three targets scattered across the corpus rather than
	// adjacent, so inbound counts vary the way they do in a real wiki. A chain corpus where each
	// article links to its predecessor yields exactly one orphan and hides any per-edge cost.
	if i%3 == 0 && i > 7 {
		body += fmt.Sprintf("\nSee also [[Corpus Doc %d — Notes On A Subject With A Realistic Title Length]], "+
			"[[Corpus Doc %d — Notes On A Subject With A Realistic Title Length]], and "+
			"[[Corpus Doc %d — Notes On A Subject With A Realistic Title Length]].\n", i/2, i/3, i/7)
	}

	// Source is set on only half the memories, so the unsourced-memory check has both a population
	// and a control group rather than flagging every memory in the corpus.
	source := ""
	if docType == ContentTypeMemory && i%2 == 0 {
		source = "seeded corpus"
	}

	tags := []string{"corpus"}
	if docType == ContentTypeMemory {
		tags = append(tags, "memory-project")
	}

	now := time.Now()
	return &Article{
		Type:        docType,
		Title:       title,
		Slug:        Slugify(title),
		Content:     body,
		Description: fmt.Sprintf("A one-line description for document %d, of the length these actually run to in practice.", i),
		Source:      source,
		Tags:        tags,
		Version:     1,
		EditSummary: "seed",
		CreatedAt:   now,
		Timestamp:   now,
	}
}

// writeCorpusFiles writes n documents straight to disk, bypassing SaveArticle.
//
// Deliberate, for two reasons. SaveArticle costs ~36 ms per document (Bleve index write, history
// revision, front-matter round-trip), which puts a 10k corpus at six minutes and out of reach of a
// test. And a wiki that already holds 10k files is the situation being measured — arriving at it
// one API call at a time is a different scenario, covered by the write benchmarks.
//
// Files are written before NewStorage opens the directory, so boot synchronization has to index
// them: that makes cold-boot cost part of what this measures rather than something it skips.
func writeCorpusFiles(tb testing.TB, dataDir string, n int, mix corpusMix) {
	tb.Helper()
	articleDir := filepath.Join(dataDir, "articles")
	if err := os.MkdirAll(articleDir, 0755); err != nil {
		tb.Fatalf("MkdirAll failed: %v", err)
	}
	for i := 0; i < n; i++ {
		art := corpusDoc(i, mix)
		doc := serializeFrontMatter(art) + art.Content
		if err := os.WriteFile(filepath.Join(articleDir, art.Slug+".md"), []byte(doc), 0644); err != nil {
			tb.Fatalf("writing document %d failed: %v", i, err)
		}
	}
}

// openLargeCorpus writes n documents and then opens storage over them, returning the storage and
// how long the open (including index build) took.
func openLargeCorpus(tb testing.TB, n int) (*Storage, time.Duration) {
	tb.Helper()
	dataDir := tb.TempDir()
	writeCorpusFiles(tb, dataDir, n, realisticMix)

	start := time.Now()
	storage, err := NewStorage(dataDir)
	if err != nil {
		tb.Fatalf("NewStorage failed: %v", err)
	}
	boot := time.Since(start)
	tb.Cleanup(func() { _ = storage.Close() })
	return storage, boot
}

func largeCorpusServer(tb testing.TB, n int) (*Server, time.Duration) {
	tb.Helper()
	storage, boot := openLargeCorpus(tb, n)
	return NewServer(storage, "Test Wiki", "default", false, NewEventBus(), "0.0.1", "8080"), boot
}

// TestLargeCorpusSanity proves the generated corpus is the shape the other measurements assume:
// the files land, boot indexes them, and the type blend is what was asked for. A benchmark run
// against a corpus that silently failed to seed would report excellent numbers for no work.
func TestLargeCorpusSanity(t *testing.T) {
	const n = 300
	storage, boot := openLargeCorpus(t, n)

	arts, err := storage.ListArticles()
	if err != nil {
		t.Fatalf("ListArticles failed: %v", err)
	}
	if len(arts) != n {
		t.Fatalf("ListArticles returned %d documents, want %d", len(arts), n)
	}

	counts := map[string]int{}
	for _, a := range arts {
		counts[a.Type]++
	}
	if counts[ContentTypeWiki] == 0 || counts[ContentTypeMemory] == 0 ||
		counts[ContentTypePlan] == 0 || counts[ContentTypeSkill] == 0 {
		t.Fatalf("corpus is not mixed: %v", counts)
	}

	graph, err := storage.ScanLinkGraph()
	if err != nil {
		t.Fatalf("ScanLinkGraph failed: %v", err)
	}
	linked := 0
	for _, c := range graph.InboundCount {
		if c > 0 {
			linked++
		}
	}
	if linked == 0 {
		t.Fatal("corpus has no inbound links; the link-graph measurements would be meaningless")
	}

	t.Logf("n=%d  boot+index=%v  types=%v  documents with inbound links=%d  broken=%d",
		n, boot.Round(time.Millisecond), counts, linked, len(graph.Broken))
}

// TestResourcesListPayloadSize measures the serialized `resources/list` response and reports how
// large a wiki would have to be for it to stop fitting on a stdio line.
//
// listResources has no cursor and no limit: it projects *every* document, including home, into one
// response. On stdio that response has to be one line, and MaxStdioLineBytes caps a line at 8 MB
// (§3.10). This test does not assert a corpus size — it records bytes-per-document so the ceiling
// is a known number rather than a surprise, and fails only if the projection grows enough per
// document to move that ceiling materially.
func TestResourcesListPayloadSize(t *testing.T) {
	const n = 300
	srv, _ := largeCorpusServer(t, n)

	result, rpcErr := srv.listResources()
	if rpcErr != nil {
		t.Fatalf("listResources failed: %v", rpcErr)
	}
	encoded, err := json.Marshal(JSONRPCResponse{JSONRPC: "2.0", ID: json.RawMessage("1"), Result: result})
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	perDoc := float64(len(encoded)) / float64(n)
	docsToCap := float64(MaxStdioLineBytes) / perDoc
	t.Logf("resources/list at n=%d: %d bytes (%.0f bytes/document); the %d-byte stdio line cap is reached at ≈%.0f documents",
		n, len(encoded), perDoc, MaxStdioLineBytes, docsToCap)

	// A guard rather than a target. If a future change to the resource projection doubles the
	// per-document cost, the ceiling halves, and that should be a deliberate decision rather than
	// something discovered by a user whose wiki stopped answering resources/list.
	if perDoc > 1200 {
		t.Errorf("resources/list costs %.0f bytes/document, which brings the 8 MB stdio line cap "+
			"down to ≈%.0f documents; the projection grew and pagination is now overdue", perDoc, docsToCap)
	}
}

// TestSearchLimitExactAtScale re-checks §3.8 on a corpus large enough that the over-fetch actually
// has to work. The original defect returned limit-1 because three filters run after Bleve applies
// Size; the fix over-fetches unconditionally. At 83 documents an over-fetch has plenty of slack —
// the question here is whether it still lands exactly when the corpus is much larger than limit.
func TestSearchLimitExactAtScale(t *testing.T) {
	const n = 300
	storage, _ := openLargeCorpus(t, n)

	for _, limit := range []int{4, 5, 8, 40} {
		results, err := storage.SearchArticlesWithOptions("document", SearchOptions{Limit: limit})
		if err != nil {
			t.Fatalf("SearchArticlesWithOptions(limit=%d) failed: %v", limit, err)
		}
		if len(results) != limit {
			t.Errorf("limit=%d returned %d results, want exactly %d (§3.8 regression at scale)",
				limit, len(results), limit)
		}
	}
}

// TestWikiHealthAtScale checks the report stays bounded and internally consistent on a corpus far
// larger than its limit: counts complete, lists capped, truncation flagged.
func TestWikiHealthAtScale(t *testing.T) {
	const n = 300
	srv, _ := largeCorpusServer(t, n)

	raw, rpcErr := srv.toolWikiHealth(json.RawMessage(`{"limit": 10}`))
	if rpcErr != nil {
		t.Fatalf("wiki_health failed: %v", rpcErr)
	}
	resp, ok := raw.(ToolResponse)
	if !ok {
		t.Fatalf("wiki_health returned %T, want ToolResponse", raw)
	}
	out, ok := resp.StructuredContent.(HealthOutput)
	if !ok {
		t.Fatalf("structured content is %T, want HealthOutput", resp.StructuredContent)
	}

	if out.TotalDocuments != n {
		t.Errorf("scanned %d documents, want %d", out.TotalDocuments, n)
	}
	if len(out.Orphans) > 10 || len(out.UnsourcedMemory) > 10 || len(out.StalePlans) > 10 {
		t.Errorf("a category exceeded the limit: orphans=%d unsourced=%d stale=%d",
			len(out.Orphans), len(out.UnsourcedMemory), len(out.StalePlans))
	}
	if out.OrphanCount > len(out.Orphans) && !out.Truncated {
		t.Error("orphan list was capped but Truncated was not set")
	}
	t.Logf("n=%d  orphans=%d unsourced=%d stale=%d broken=%d truncated=%v",
		n, out.OrphanCount, out.UnsourcedCount, out.StalePlanCount, out.BrokenLinkCount, out.Truncated)
}

// TestBootIndexesEveryDocument covers the boot synchronization path, which had no test at all
// before this — the likely reason §3.17 survived: SyncSearchIndex runs on every single start and
// nothing asserted anything about it.
func TestBootIndexesEveryDocument(t *testing.T) {
	const n = 300
	storage, _ := openLargeCorpus(t, n)

	count, err := storage.SearchIndex.DocCount()
	if err != nil {
		t.Fatalf("DocCount failed: %v", err)
	}
	if int(count) != n {
		t.Fatalf("index holds %d documents after boot, want %d — batching dropped documents", count, n)
	}

	// DocCount alone would pass on an index full of empty documents, so confirm the content is
	// really searchable rather than merely present.
	results, err := storage.SearchArticlesWithOptions("prose", SearchOptions{Limit: 10})
	if err != nil {
		t.Fatalf("search failed: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("boot-indexed documents are not searchable")
	}
}

// TestBootReconcilesDeletedDocuments covers the other half of boot synchronization: a document
// removed from disk while the server was down must not linger in the index. This is the path whose
// query size changed from a hardcoded 1,000,000 to the actual document count, and whose deletes
// were moved into a batch.
func TestBootReconcilesDeletedDocuments(t *testing.T) {
	const n = 50
	dataDir := t.TempDir()
	writeCorpusFiles(t, dataDir, n, realisticMix)

	storage, err := NewStorage(dataDir)
	if err != nil {
		t.Fatalf("NewStorage failed: %v", err)
	}
	if count, _ := storage.SearchIndex.DocCount(); int(count) != n {
		t.Fatalf("first boot indexed %d documents, want %d", count, n)
	}
	if err := storage.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	// Delete three documents behind the server's back, the way an external editor or a sync client
	// would, then boot again.
	removed := 0
	for i := 0; i < 3; i++ {
		slug := Slugify(corpusDoc(i, realisticMix).Title)
		if err := os.Remove(filepath.Join(dataDir, "articles", slug+".md")); err != nil {
			t.Fatalf("removing %s failed: %v", slug, err)
		}
		removed++
	}

	storage, err = NewStorage(dataDir)
	if err != nil {
		t.Fatalf("reopen failed: %v", err)
	}
	t.Cleanup(func() { _ = storage.Close() })

	count, err := storage.SearchIndex.DocCount()
	if err != nil {
		t.Fatalf("DocCount failed: %v", err)
	}
	if int(count) != n-removed {
		t.Errorf("index holds %d documents after reconciliation, want %d — deleted documents were "+
			"not removed from the index", count, n-removed)
	}
}

// TestBootStaysFastOnALargeCorpus is the regression guard for §3.17.
//
// A timing assertion is a blunt instrument, so the threshold is set where it cannot reasonably
// misfire: per-document indexing cost ~24 ms, which puts 2,000 documents at ~48 s, while batched
// indexing does the same corpus in well under a second. Ten seconds sits roughly an order of
// magnitude clear of both — far enough above the batched time to survive a slow or loaded CI
// machine, and far enough below the unbatched time that reverting the batch fails this test rather
// than merely slowing it.
func TestBootStaysFastOnALargeCorpus(t *testing.T) {
	if testing.Short() {
		t.Skip("large corpus boot timing")
	}
	const n = 2000
	const budget = 10 * time.Second

	_, boot := openLargeCorpus(t, n)
	if boot > budget {
		t.Errorf("booting a %d-document wiki took %v, over the %v budget — boot indexing is no "+
			"longer batched (§3.17); it was 24 ms/document before batching", n, boot.Round(time.Millisecond), budget)
	}
	t.Logf("boot+index of %d documents: %v", n, boot.Round(time.Millisecond))
}

// --- benchmarks -------------------------------------------------------------------------------
//
// Run: go test ./server/ -run XXX -bench BenchmarkLargeCorpus -benchtime 5x -timeout 30m
//
// Every benchmark rebuilds its corpus from scratch, so the full sweep is minutes, not seconds.
// Sizes live in one variable so a deeper one-off run means editing a single line.

var largeCorpusSizes = []int{1000, 5000, 10000}

func benchEachSize(b *testing.B, name string, fn func(b *testing.B, storage *Storage)) {
	for _, n := range largeCorpusSizes {
		b.Run(fmt.Sprintf("%s/n=%d", name, n), func(b *testing.B) {
			storage, boot := openLargeCorpus(b, n)
			b.ReportMetric(float64(boot.Milliseconds()), "boot-ms")
			b.ResetTimer()
			fn(b, storage)
		})
	}
}

// BenchmarkLargeCorpusBoot measures process start against an existing corpus: NewStorage plus the
// boot index synchronization. This is the number a user experiences as "how long until my wiki
// answers", and unlike the read paths it cannot be amortized by a cache.
func BenchmarkLargeCorpusBoot(b *testing.B) {
	for _, n := range largeCorpusSizes {
		b.Run(fmt.Sprintf("Boot/n=%d", n), func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				b.StopTimer()
				dataDir := b.TempDir()
				writeCorpusFiles(b, dataDir, n, realisticMix)
				b.StartTimer()

				storage, err := NewStorage(dataDir)
				if err != nil {
					b.Fatalf("NewStorage failed: %v", err)
				}

				b.StopTimer()
				_ = storage.Close()
				b.StartTimer()
			}
		})
	}
}

func BenchmarkLargeCorpusListArticles(b *testing.B) {
	benchEachSize(b, "ListArticles", func(b *testing.B, storage *Storage) {
		if _, err := storage.ListArticles(); err != nil { // warm the cache
			b.Fatalf("warmup failed: %v", err)
		}
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			if _, err := storage.ListArticles(); err != nil {
				b.Fatalf("ListArticles failed: %v", err)
			}
		}
	})
}

// There is deliberately no "cold ListArticles" benchmark.
//
// The first draft of this file had one, and it reported the same time as the warm case — which
// looked like the metadata cache doing nothing. It is a harness artifact, not a finding: NewStorage
// runs SyncSearchIndex, which calls ListArticles, so by the time any benchmark body runs the cache
// is already populated. A genuinely cold ListArticles cannot be observed after boot because boot is
// what warms it. The parse-everything cost it was trying to isolate is real, and it is already
// measured — inside BenchmarkLargeCorpusBoot.

func BenchmarkLargeCorpusScanLinkGraph(b *testing.B) {
	benchEachSize(b, "ScanLinkGraph", func(b *testing.B, storage *Storage) {
		for i := 0; i < b.N; i++ {
			if _, err := storage.ScanLinkGraph(); err != nil {
				b.Fatalf("ScanLinkGraph failed: %v", err)
			}
		}
	})
}

func BenchmarkLargeCorpusGetBacklinks(b *testing.B) {
	benchEachSize(b, "GetBacklinks", func(b *testing.B, storage *Storage) {
		target := Slugify(corpusDoc(1, realisticMix).Title)
		if _, err := storage.GetBacklinks(target); err != nil {
			b.Fatalf("warmup failed: %v", err)
		}
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			if _, err := storage.GetBacklinks(target); err != nil {
				b.Fatalf("GetBacklinks failed: %v", err)
			}
		}
	})
}

func BenchmarkLargeCorpusSearch(b *testing.B) {
	benchEachSize(b, "Search", func(b *testing.B, storage *Storage) {
		for i := 0; i < b.N; i++ {
			if _, err := storage.SearchArticlesWithOptions("document prose", SearchOptions{Limit: 40}); err != nil {
				b.Fatalf("search failed: %v", err)
			}
		}
	})
}

func BenchmarkLargeCorpusSearchFaceted(b *testing.B) {
	benchEachSize(b, "SearchFaceted", func(b *testing.B, storage *Storage) {
		opts := SearchOptions{Limit: 40, Types: []string{ContentTypeMemory}, Tags: []string{"corpus"}}
		for i := 0; i < b.N; i++ {
			if _, err := storage.SearchArticlesWithOptions("document prose", opts); err != nil {
				b.Fatalf("faceted search failed: %v", err)
			}
		}
	})
}

func BenchmarkLargeCorpusWikiHealth(b *testing.B) {
	for _, n := range largeCorpusSizes {
		b.Run(fmt.Sprintf("WikiHealth/n=%d", n), func(b *testing.B) {
			storage, _ := openLargeCorpus(b, n)
			srv := NewServer(storage, "Test Wiki", "default", false, NewEventBus(), "0.0.1", "8080")
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := srv.toolWikiHealth(json.RawMessage(`{}`)); err != nil {
					b.Fatalf("wiki_health failed: %v", err)
				}
			}
		})
	}
}

func BenchmarkLargeCorpusListResources(b *testing.B) {
	for _, n := range largeCorpusSizes {
		b.Run(fmt.Sprintf("ListResources/n=%d", n), func(b *testing.B) {
			storage, _ := openLargeCorpus(b, n)
			srv := NewServer(storage, "Test Wiki", "default", false, NewEventBus(), "0.0.1", "8080")

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := srv.listResources(); err != nil {
					b.Fatalf("listResources failed: %v", err)
				}
			}
			b.StopTimer()

			// Report the serialized size alongside the latency: for this endpoint the payload is
			// the risk, not the time. Reported after the loop — ResetTimer discards metrics
			// recorded before it, which silently swallowed these on the first attempt.
			result, _ := srv.listResources()
			encoded, _ := json.Marshal(JSONRPCResponse{JSONRPC: "2.0", ID: json.RawMessage("1"), Result: result})
			b.ReportMetric(float64(len(encoded)), "payload-bytes")
			b.ReportMetric(float64(len(encoded))/float64(MaxStdioLineBytes)*100, "pct-of-stdio-cap")
		})
	}
}

func BenchmarkLargeCorpusExportOKF(b *testing.B) {
	benchEachSize(b, "ExportOKF", func(b *testing.B, storage *Storage) {
		for i := 0; i < b.N; i++ {
			bundle, err := storage.ExportOKFBundle()
			if err != nil {
				b.Fatalf("ExportOKFBundle failed: %v", err)
			}
			b.ReportMetric(float64(len(bundle)), "bundle-bytes")
		}
	})
}

// BenchmarkLargeCorpusSaveArticle measures the write path on an already-large wiki — the cost an
// agent pays per create once the corpus is big, as opposed to the cost of reading it.
func BenchmarkLargeCorpusSaveArticle(b *testing.B) {
	benchEachSize(b, "SaveArticle", func(b *testing.B, storage *Storage) {
		for i := 0; i < b.N; i++ {
			title := fmt.Sprintf("Written During Benchmark %d %d", b.N, i)
			if _, err := storage.SaveArticle("", title, "body prose for the write benchmark",
				"desc", "", "", "bench", []string{"corpus"}, ContentTypeWiki); err != nil {
				b.Fatalf("SaveArticle failed: %v", err)
			}
		}
	})
}
