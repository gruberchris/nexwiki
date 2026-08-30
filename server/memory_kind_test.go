package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The kind axis has one job: make "what sort of fact is this?" answerable by a filter rather than
// by reading every memory. These tests pin the two halves that make that work — the gate that
// stops an unclassified memory being created, and the preservation that stops an ordinary edit
// silently undoing the classification.

func TestMemoryKindIsRequiredAtCreation(t *testing.T) {
	srv := newMCPServer(t)

	t.Run("absent is rejected, and the error teaches the vocabulary", func(t *testing.T) {
		resp := toolCall(t, srv, `{"name":"create_agent_memory","arguments":{"title":"Unclassified","content":"# fact","description":"fixture memory","source":"test fixture"}}`)
		if !resp.IsError {
			t.Fatal("a memory with no kind must be rejected")
		}
		text := resp.Content[0].Text
		for _, kind := range MemoryKinds {
			if !strings.Contains(text, kind) {
				t.Errorf("rejection must list %q so the agent can retry without guessing: %s", kind, text)
			}
		}
	})

	t.Run("an invented kind is rejected rather than stored", func(t *testing.T) {
		resp := toolCall(t, srv, `{"name":"create_agent_memory","arguments":{"title":"Invented Kind","content":"# fact","memory_kind":"troubleshooting","description":"fixture memory","source":"test fixture"}}`)
		if !resp.IsError {
			t.Fatal("an out-of-vocabulary kind must be rejected")
		}
		if !strings.Contains(resp.Content[0].Text, "do not invent new ones") {
			t.Errorf("rejection should say the vocabulary is closed: %s", resp.Content[0].Text)
		}
		if _, err := srv.Storage.GetArticle("invented-kind"); err == nil {
			t.Error("a rejected create must not have written the document")
		}
	})

	t.Run("each vocabulary value is accepted and round-trips through disk", func(t *testing.T) {
		for _, kind := range MemoryKinds {
			title := "Kind " + kind
			resp := toolCall(t, srv, `{"name":"create_agent_memory","arguments":{"title":"`+title+`","content":"# fact","memory_kind":"`+kind+`","description":"fixture memory","source":"test fixture"}}`)
			if resp.IsError {
				t.Fatalf("kind %q rejected: %s", kind, resp.Content[0].Text)
			}
			// Read it back off disk rather than from the response, so this also covers the
			// front-matter serialize/parse pair. A field that only exists in memory is not stored.
			art, err := srv.Storage.GetArticle(Slugify(title))
			if err != nil {
				t.Fatalf("reading back %q: %v", title, err)
			}
			if art.MemoryKind != kind {
				t.Errorf("kind = %q after a disk round-trip, want %q", art.MemoryKind, kind)
			}
		}
	})
}

// TestMemoryKindSurvivesAnOrdinaryEdit is the pointer-semantics guarantee. It mirrors the reasoning
// that made `status` omitted-means-preserve: editing a plan's body must not reset it to draft, and
// editing a memory's body must not declassify it. Getting this wrong is invisible at the call site
// and only shows up later as a memory that kind-filtered recall can no longer find.
func TestMemoryKindSurvivesAnOrdinaryEdit(t *testing.T) {
	srv := newMCPServer(t)

	create := toolCall(t, srv, `{"name":"create_agent_memory","arguments":{"title":"Deploy Constraint","content":"# original","memory_kind":"project","memory_type":"nexwiki","description":"d","source":"s"}}`)
	if create.IsError {
		t.Fatalf("setup failed: %s", create.Content[0].Text)
	}

	t.Run("editing the body preserves the kind", func(t *testing.T) {
		resp := toolCall(t, srv, `{"name":"edit_agent_memory","arguments":{"slug":"deploy-constraint","content":"# corrected","loaded_version":1}}`)
		if resp.IsError {
			t.Fatalf("edit failed: %s", resp.Content[0].Text)
		}
		art, err := srv.Storage.GetArticle("deploy-constraint")
		if err != nil {
			t.Fatal(err)
		}
		if art.MemoryKind != "project" {
			t.Errorf("an ordinary edit declassified the memory: kind = %q", art.MemoryKind)
		}
		if !strings.Contains(art.Content, "corrected") {
			t.Error("the edit itself did not land")
		}
	})

	t.Run("replacing the tags preserves the kind", func(t *testing.T) {
		art, _ := srv.Storage.GetArticle("deploy-constraint")
		resp := toolCall(t, srv, `{"name":"edit_agent_memory","arguments":{"slug":"deploy-constraint","tags":["review"],"loaded_version":`+itoa(art.Version)+`}}`)
		if resp.IsError {
			t.Fatalf("edit failed: %s", resp.Content[0].Text)
		}
		after, _ := srv.Storage.GetArticle("deploy-constraint")
		if after.MemoryKind != "project" {
			t.Errorf("a tag edit declassified the memory: kind = %q", after.MemoryKind)
		}
	})

	t.Run("an explicit kind reclassifies", func(t *testing.T) {
		art, _ := srv.Storage.GetArticle("deploy-constraint")
		resp := toolCall(t, srv, `{"name":"edit_agent_memory","arguments":{"slug":"deploy-constraint","memory_kind":"reference","loaded_version":`+itoa(art.Version)+`}}`)
		if resp.IsError {
			t.Fatalf("edit failed: %s", resp.Content[0].Text)
		}
		after, _ := srv.Storage.GetArticle("deploy-constraint")
		if after.MemoryKind != "reference" {
			t.Errorf("kind = %q, want reference", after.MemoryKind)
		}
	})

	t.Run("an invented kind on edit is rejected and changes nothing", func(t *testing.T) {
		art, _ := srv.Storage.GetArticle("deploy-constraint")
		resp := toolCall(t, srv, `{"name":"edit_agent_memory","arguments":{"slug":"deploy-constraint","memory_kind":"nonsense","loaded_version":`+itoa(art.Version)+`}}`)
		if !resp.IsError {
			t.Fatal("an out-of-vocabulary kind must be rejected on edit too")
		}
		after, _ := srv.Storage.GetArticle("deploy-constraint")
		if after.MemoryKind != "reference" || after.Version != art.Version {
			t.Errorf("a rejected edit must leave the document untouched: kind=%q version=%d", after.MemoryKind, after.Version)
		}
	})
}

// TestMemoryKindIsIndependentOfScope pins the two-axis property. Scope is an open vocabulary on a
// tag; kind is a closed vocabulary in a field. The full cross-product has to be legal, and each
// filter has to narrow on its own axis without touching the other.
func TestMemoryKindIsIndependentOfScope(t *testing.T) {
	srv := newMCPServer(t)

	seed := []struct{ title, kind, scope string }{
		{"Swarm Deploy Rule", "project", "nexwiki"},
		{"Grafana Dashboard", "reference", "nexwiki"},
		{"How Chris Reviews", "feedback", ""},
		{"Operator Profile", "user", ""},
		{"Docker Socket Note", "reference", "docker"},
	}
	for _, s := range seed {
		args := `{"title":"` + s.title + `","content":"# fact","memory_kind":"` + s.kind + `"`
		if s.scope != "" {
			args += `,"memory_type":"` + s.scope + `"`
		}
		args += `,"description":"fixture memory","source":"test fixture"}`
		if resp := toolCall(t, srv, `{"name":"create_agent_memory","arguments":`+args+`}`); resp.IsError {
			t.Fatalf("seeding %q: %s", s.title, resp.Content[0].Text)
		}
	}

	listCount := func(t *testing.T, args string) int {
		t.Helper()
		resp := toolCall(t, srv, `{"name":"list_agent_memories","arguments":`+args+`}`)
		if resp.IsError {
			t.Fatalf("list failed: %s", resp.Content[0].Text)
		}
		out, ok := resp.StructuredContent.(DocumentListOutput)
		if !ok {
			t.Fatalf("expected DocumentListOutput, got %T", resp.StructuredContent)
		}
		return out.Count
	}

	if n := listCount(t, `{}`); n != len(seed) {
		t.Errorf("unfiltered = %d, want %d", n, len(seed))
	}
	if n := listCount(t, `{"memory_kind":"reference"}`); n != 2 {
		t.Errorf("kind=reference = %d, want 2 (across two different scopes)", n)
	}
	if n := listCount(t, `{"memory_type":"nexwiki"}`); n != 2 {
		t.Errorf("scope=nexwiki = %d, want 2 (of two different kinds)", n)
	}
	// The composition is the point: both filters applied means both, not either.
	if n := listCount(t, `{"memory_type":"nexwiki","memory_kind":"reference"}`); n != 1 {
		t.Errorf("scope=nexwiki + kind=reference = %d, want 1", n)
	}
	// The two kinds that had no home before this axis existed, and are unscoped by nature.
	if n := listCount(t, `{"memory_kind":"user"}`); n != 1 {
		t.Errorf("kind=user = %d, want 1", n)
	}
	if n := listCount(t, `{"memory_kind":"feedback"}`); n != 1 {
		t.Errorf("kind=feedback = %d, want 1", n)
	}

	t.Run("an unknown kind reports rather than returning nothing", func(t *testing.T) {
		// An empty result reads as "no such knowledge", which is the wrong conclusion to hand an
		// agent that typoed a filter — the same reasoning search_wiki applies to document types.
		resp := toolCall(t, srv, `{"name":"list_agent_memories","arguments":{"memory_kind":"projects"}}`)
		if !resp.IsError {
			t.Fatal("a typoed kind must be reported, not answered with an empty list")
		}
		if !strings.Contains(resp.Content[0].Text, "feedback") {
			t.Errorf("the error must list the vocabulary: %s", resp.Content[0].Text)
		}
	})
}

// TestSearchNarrowsByMemoryKind covers the same facet on the retrieval path, including the
// property that makes it coherent: only memories carry a kind, so narrowing by one necessarily
// excludes every other document class rather than matching them on an empty field.
func TestSearchNarrowsByMemoryKind(t *testing.T) {
	srv := newMCPServer(t)

	must := func(call string) {
		t.Helper()
		if resp := toolCall(t, srv, call); resp.IsError {
			t.Fatalf("setup failed: %s", resp.Content[0].Text)
		}
	}
	must(`{"name":"create_wiki_article","arguments":{"title":"Bleve Indexing","content":"# bleve indexing notes"}}`)
	must(`{"name":"create_agent_memory","arguments":{"title":"Bleve Decision","content":"# bleve over elasticsearch","memory_kind":"project","description":"fixture memory","source":"test fixture"}}`)
	must(`{"name":"create_agent_memory","arguments":{"title":"Bleve Dashboard","content":"# bleve metrics dashboard","memory_kind":"reference","description":"fixture memory","source":"test fixture"}}`)

	search := func(t *testing.T, args string) SearchOutput {
		t.Helper()
		resp := toolCall(t, srv, `{"name":"search_wiki","arguments":`+args+`}`)
		if resp.IsError {
			t.Fatalf("search failed: %s", resp.Content[0].Text)
		}
		out, ok := resp.StructuredContent.(SearchOutput)
		if !ok {
			t.Fatalf("expected SearchOutput, got %T", resp.StructuredContent)
		}
		return out
	}

	all := search(t, `{"query":"bleve"}`)
	if all.Count < 3 {
		t.Fatalf("unfiltered search found %d, want at least 3", all.Count)
	}

	narrowed := search(t, `{"query":"bleve","memory_kind":"project"}`)
	if narrowed.Count != 1 {
		t.Errorf("kind-narrowed search = %d, want 1", narrowed.Count)
	}
	for _, hit := range narrowed.Results {
		if hit.Type != ContentTypeMemory {
			t.Errorf("a kind filter must exclude non-memories, got a %s", hit.Type)
		}
	}
	// The facet is echoed so an agent reading only the structured half can tell "no such
	// knowledge" from "my filter excluded it".
	if narrowed.MemoryKind != "project" {
		t.Errorf("the applied facet must be echoed, got %q", narrowed.MemoryKind)
	}
}

// TestUnkindedMemoriesAreReportedNotRewritten pins the migration strategy. New writes require a
// kind; memories written before the axis existed stay valid, keep working, and show up as a
// burn-down list. Classifying them is a judgment call per memory, so nothing guesses on their
// behalf — which is the same shape as the unsourced_memories precedent.
func TestUnkindedMemoriesAreReportedNotRewritten(t *testing.T) {
	srv := newMCPServer(t)

	// A memory as it exists on disk today: no memory_kind key at all.
	legacy := "---\ntype: AI-Agent-Memory\ntitle: Legacy Fact\nslug: legacy-fact\nversion: 1\n" +
		"timestamp: \"2026-01-01T00:00:00Z\"\ncreated_at: \"2026-01-01T00:00:00Z\"\ntags:\n    - memory-nexwiki\n---\n# Legacy Fact\n\nWritten before the kind axis.\n"
	if err := os.WriteFile(filepath.Join(srv.Storage.ArticleDir, "legacy-fact.md"), []byte(legacy), 0644); err != nil {
		t.Fatalf("seeding the legacy file failed: %v", err)
	}
	if err := srv.Storage.SyncSearchIndex(); err != nil {
		t.Fatalf("reindex failed: %v", err)
	}

	t.Run("it is still readable and still editable", func(t *testing.T) {
		art, err := srv.Storage.GetArticle("legacy-fact")
		if err != nil {
			t.Fatalf("a legacy memory must stay readable: %v", err)
		}
		if art.MemoryKind != "" {
			t.Errorf("expected no kind, got %q", art.MemoryKind)
		}
		// The gate lives on create, not on save. A legacy memory that could not be edited until
		// somebody classified it would make correctness depend on migration order.
		resp := toolCall(t, srv, `{"name":"edit_agent_memory","arguments":{"slug":"legacy-fact","content":"# corrected","loaded_version":1}}`)
		if resp.IsError {
			t.Fatalf("an unclassified memory must remain editable: %s", resp.Content[0].Text)
		}
	})

	t.Run("wiki_health reports it as the backfill worklist", func(t *testing.T) {
		resp := toolCall(t, srv, `{"name":"wiki_health","arguments":{}}`)
		if resp.IsError {
			t.Fatalf("wiki_health failed: %s", resp.Content[0].Text)
		}
		out, ok := resp.StructuredContent.(HealthOutput)
		if !ok {
			t.Fatalf("expected HealthOutput, got %T", resp.StructuredContent)
		}
		if out.UnkindedCount != 1 {
			t.Fatalf("unkinded_memory_count = %d, want 1", out.UnkindedCount)
		}
		if len(out.UnkindedMemories) != 1 || out.UnkindedMemories[0].Slug != "legacy-fact" {
			t.Errorf("unkinded_memories = %+v, want the legacy memory", out.UnkindedMemories)
		}
		if !strings.Contains(out.UnkindedMemories[0].Detail, "edit_agent_memory") {
			t.Error("the finding must name the tool that fixes it")
		}
	})

	t.Run("classifying it clears the finding", func(t *testing.T) {
		art, _ := srv.Storage.GetArticle("legacy-fact")
		resp := toolCall(t, srv, `{"name":"edit_agent_memory","arguments":{"slug":"legacy-fact","memory_kind":"project","loaded_version":`+itoa(art.Version)+`}}`)
		if resp.IsError {
			t.Fatalf("classify failed: %s", resp.Content[0].Text)
		}
		health := toolCall(t, srv, `{"name":"wiki_health","arguments":{}}`)
		out := health.StructuredContent.(HealthOutput)
		if out.UnkindedCount != 0 {
			t.Errorf("unkinded_memory_count = %d after classifying, want 0", out.UnkindedCount)
		}
	})
}

// TestMemoryKindOnlyEverLandsOnMemories guards the field against classes that cannot interpret it.
// A kind on a wiki article would be a value no reader can act on and no filter will ever match.
func TestMemoryKindOnlyEverLandsOnMemories(t *testing.T) {
	srv := newMCPServer(t)

	kind := "project"
	art, err := srv.Storage.SaveArticleWithOverrides("", "Ordinary Page", "# body", "", "", "", "", nil,
		ContentTypeWiki, ArticleOverrides{MemoryKind: &kind})
	if err != nil {
		t.Fatalf("save failed: %v", err)
	}
	if art.MemoryKind != "" {
		t.Errorf("a wiki article must not carry a memory kind, got %q", art.MemoryKind)
	}

	raw, err := os.ReadFile(filepath.Join(srv.Storage.ArticleDir, "ordinary-page.md"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "memory_kind") {
		t.Errorf("memory_kind must not be serialized onto a non-memory:\n%s", raw)
	}
}

// TestMemoryKindSurvivesAnOKFRoundTrip covers the export/import pair. A bundle that drops the
// field would silently declassify the whole corpus on restore, which is precisely the data loss a
// bundle exists to prevent.
func TestMemoryKindSurvivesAnOKFRoundTrip(t *testing.T) {
	srv := newMCPServer(t)

	if resp := toolCall(t, srv, `{"name":"create_agent_memory","arguments":{"title":"Bundled Fact","content":"# fact","memory_kind":"feedback","memory_type":"nexwiki","description":"d","source":"s"}}`); resp.IsError {
		t.Fatalf("setup failed: %s", resp.Content[0].Text)
	}

	bundle, err := srv.Storage.ExportOKFBundle()
	if err != nil {
		t.Fatalf("export failed: %v", err)
	}

	// Import into a second, empty wiki so this measures the bundle's contents rather than what
	// happened to already be on disk.
	dest := newMCPServer(t)
	report, err := dest.Storage.ImportOKFBundle(bundle)
	if err != nil {
		t.Fatalf("import failed: %v", err)
	}
	if report.Imported == 0 {
		t.Fatalf("nothing imported: %+v", report)
	}

	restored, err := dest.Storage.GetArticle("bundled-fact")
	if err != nil {
		t.Fatalf("the memory did not survive the round-trip: %v", err)
	}
	if restored.MemoryKind != "feedback" {
		t.Errorf("kind = %q after an OKF round-trip, want feedback", restored.MemoryKind)
	}
}

// TestMemoryKindReachesStructuredOutput checks the field is actually visible to an agent that
// reads structured payloads, on both the read and the list paths. A stored field no tool surfaces
// is a field nothing can filter on.
func TestMemoryKindReachesStructuredOutput(t *testing.T) {
	srv := newMCPServer(t)

	if resp := toolCall(t, srv, `{"name":"create_agent_memory","arguments":{"title":"Surfaced Fact","content":"# fact","memory_kind":"user","description":"fixture memory","source":"test fixture"}}`); resp.IsError {
		t.Fatalf("setup failed: %s", resp.Content[0].Text)
	}

	read := toolCall(t, srv, `{"name":"read_article","arguments":{"slug":"surfaced-fact"}}`)
	out, ok := read.StructuredContent.(ArticleOutput)
	if !ok {
		t.Fatalf("expected ArticleOutput, got %T", read.StructuredContent)
	}
	if out.Article.MemoryKind != "user" {
		t.Errorf("read_article structured payload has kind %q, want user", out.Article.MemoryKind)
	}

	// And the published schema has to advertise it, or a client trusting outputSchema never looks.
	props, _ := readArticleTool.Output["properties"].(map[string]interface{})
	article, _ := props["article"].(map[string]interface{})
	articleProps, _ := article["properties"].(map[string]interface{})
	if _, declared := articleProps["memory_kind"]; !declared {
		t.Error("articleSchema must declare memory_kind")
	}

	list := toolCall(t, srv, `{"name":"list_agent_memories","arguments":{}}`)
	listOut := list.StructuredContent.(DocumentListOutput)
	if len(listOut.Documents) != 1 || listOut.Documents[0].MemoryKind != "user" {
		t.Errorf("list_agent_memories must carry the kind, got %+v", listOut.Documents)
	}
	// The prose half has to agree with the structured half, since some clients render only one.
	if !strings.Contains(list.Content[0].Text, "Kind: user") {
		t.Errorf("the listing prose must show the kind: %s", list.Content[0].Text)
	}
}
