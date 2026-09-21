package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// newPurgeServer is an MCP server whose activity events persist to a real durable log, as they do
// in production, so a purge's rewrite of that log is exercised against the live writer.
func newPurgeServer(t *testing.T) *Server {
	t.Helper()
	srv := newMCPServer(t)
	al, err := OpenActivityLog(srv.Storage.DataDir)
	if err != nil {
		t.Fatalf("OpenActivityLog: %v", err)
	}
	t.Cleanup(func() { _ = al.Close() })
	srv.ActivityLog = al
	srv.EventBus.SetPersist(func(ev LogEvent) { _ = al.Append(ev) })
	return srv
}

// agentCall runs a tool call the way a connected agent does, activity logging included.
func agentCall(t *testing.T, srv *Server, name string, args map[string]interface{}) ToolResponse {
	t.Helper()
	raw, err := json.Marshal(map[string]interface{}{"name": name, "arguments": args})
	if err != nil {
		t.Fatal(err)
	}
	res, rpcErr := srv.executeToolCall(raw, "Test Agent")
	if rpcErr != nil {
		t.Fatalf("%s: RPC error %v", name, rpcErr)
	}
	resp, ok := res.(ToolResponse)
	if !ok {
		t.Fatalf("%s: got %T", name, res)
	}
	return resp
}

// historyScan runs search_wiki(include_history: true) and returns its history matches.
func historyScan(t *testing.T, srv *Server, query string, extra map[string]interface{}) ([]HistoryMatch, string) {
	t.Helper()
	args := map[string]interface{}{"query": query, "include_history": true}
	for k, v := range extra {
		args[k] = v
	}
	resp := agentCall(t, srv, "search_wiki", args)
	if resp.IsError {
		t.Fatalf("search_wiki failed: %s", resp.Content[0].Text)
	}
	var out SearchOutput
	decodeStructured(t, resp, &out)
	if out.HistoryMatches == nil {
		t.Fatal("include_history must always return history_matches, even when empty")
	}
	for _, problem := range validateAgainstSchema(mustRoundTrip(t, resp.StructuredContent), searchOutputSchema(), "$") {
		t.Error(problem)
	}
	return *out.HistoryMatches, resp.Content[0].Text
}

func mustRoundTrip(t *testing.T, v interface{}) interface{} {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var out interface{}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func historyFiles(t *testing.T, srv *Server, slug string) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(srv.Storage.HistoryDir, slug))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	return names
}

func activityText(t *testing.T, srv *Server) string {
	t.Helper()
	data, err := os.ReadFile(ActivityLogPath(srv.Storage.DataDir))
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	b.Write(data)
	for _, ev := range srv.EventBus.GetHistory() {
		b.WriteString(ev.Slug + " " + ev.Title + "\n")
	}
	return strings.ToLower(b.String())
}

// TestPurgeHistoryRedactsEveryEarlierRevision is the perceptea case end to end: the provenance
// sat in revision 1's body, description, source, tags, and title, and no tool could reach it.
func TestPurgeHistoryRedactsEveryEarlierRevision(t *testing.T) {
	srv := newPurgeServer(t)

	resp := agentCall(t, srv, "save_article", map[string]interface{}{
		"title": "Perceptea JEV Port", "content": "# Perceptea\n\nPorted from the JEV project.",
		"description": "A port of JEV", "source": "JEV upstream repository", "tags": []string{"jev", "system-one"},
	})
	if resp.IsError {
		t.Fatal(resp.Content[0].Text)
	}
	agentCall(t, srv, "read_article", map[string]interface{}{"slug": "perceptea-jev-port"})
	if resp := agentCall(t, srv, "save_article", map[string]interface{}{
		"slug": "perceptea-jev-port", "title": "Perceptea JEV Port", "content": "# Perceptea\n\nStill mentions jev.", "loaded_version": 1,
	}); resp.IsError {
		t.Fatal(resp.Content[0].Text)
	}

	// Before the purge the term is everywhere: in history and in the activity log.
	before, _ := historyScan(t, srv, "JEV", nil)
	if len(before) != 2 {
		t.Fatalf("before the purge, history_matches = %+v, want versions 1 and 2", before)
	}
	if !strings.Contains(activityText(t, srv), "jev") {
		t.Fatal("fixture: the activity log should carry the old title before the purge")
	}

	// The redaction: new title (so a new slug), clean metadata, and the purge.
	resp = agentCall(t, srv, "save_article", map[string]interface{}{
		"slug": "perceptea-jev-port", "title": "Perceptea", "content": "# Perceptea\n\nA vision pipeline.",
		"description": "A vision pipeline", "source": "internal design", "tags": []string{"vision"},
		"loaded_version": 2, "purge_history": true,
	})
	if resp.IsError {
		t.Fatalf("purge save failed: %s", resp.Content[0].Text)
	}
	text := resp.Content[0].Text
	for _, want := range []string{"revisions_removed: [1, 2]", "activity_entries_retitled:", "only revision 3"} {
		if !strings.Contains(text, want) {
			t.Errorf("the response must report %q:\n%s", want, text)
		}
	}

	if got := historyFiles(t, srv, "perceptea"); len(got) != 1 || got[0] != "3.md.gz" {
		t.Errorf("history after the purge = %v, want only 3.md.gz", got)
	}
	for _, v := range []int{1, 2} {
		read := agentCall(t, srv, "read_article", map[string]interface{}{"slug": "perceptea", "version": v})
		if !read.IsError {
			t.Errorf("read_article(version: %d) must fail after the purge", v)
		}
	}
	if read := agentCall(t, srv, "read_article", map[string]interface{}{"slug": "perceptea", "version": 3}); read.IsError {
		t.Errorf("the kept revision must still be readable: %s", read.Content[0].Text)
	}
	if _, err := srv.Storage.RevertArticle("perceptea", 1); !errors.Is(err, errVersionNotFound) {
		t.Errorf("reverting to a purged version: err = %v, want version not found", err)
	}

	// The document itself is untouched apart from the save: same type, version counter intact.
	art, err := srv.Storage.GetArticle("perceptea")
	if err != nil || art.Version != 3 || art.Type != ContentTypeWiki {
		t.Fatalf("document after the purge: %+v, %v", art, err)
	}

	// Metadata is part of a revision: description, source, tags, and title are gone with it.
	after, scanText := historyScan(t, srv, "jev", nil)
	if len(after) != 0 {
		t.Errorf("history_matches after the purge = %+v, want none", after)
	}
	if !strings.Contains(scanText, "No revision of any document contains it") {
		t.Errorf("an empty scan must say so as a finding:\n%s", scanText)
	}
	for _, term := range []string{"system-one", "upstream repository"} {
		if m, _ := historyScan(t, srv, term, nil); len(m) != 0 {
			t.Errorf("%q survives in %+v", term, m)
		}
	}

	// The activity log — durable file and in-memory feed — no longer carries the old title or the
	// old slug derived from it.
	if log := activityText(t, srv); strings.Contains(log, "jev") {
		t.Errorf("the activity log still carries the redacted title or slug:\n%s", log)
	}
	events, _ := ReadActivityLogFiltered(ActivityLogPath(srv.Storage.DataDir), ActivityFilter{Slug: "perceptea"})
	if len(events) < 3 {
		t.Errorf("the earlier events must be kept, retitled, not dropped; got %d under the new slug", len(events))
	}

	// The writer's handle was reopened after the rewrite: new events still reach the file.
	agentCall(t, srv, "save_article", map[string]interface{}{"slug": "perceptea", "title": "Perceptea", "content": "# Perceptea\n\nv4", "loaded_version": 3})
	events2, _ := ReadActivityLogFiltered(ActivityLogPath(srv.Storage.DataDir), ActivityFilter{Slug: "perceptea"})
	if len(events2) != len(events)+1 {
		t.Errorf("an event after the purge did not reach the durable log: %d → %d", len(events), len(events2))
	}
}

func TestPurgeHistoryRequiresAnUpdateWithLoadedVersion(t *testing.T) {
	srv := newPurgeServer(t)
	seedTyped(t, srv, "Guarded Doc", ContentTypeWiki)

	resp := saveCall(t, srv, map[string]interface{}{"slug": "guarded-doc", "title": "Guarded Doc", "content": "# v2", "purge_history": true})
	if !resp.IsError || !strings.Contains(resp.Content[0].Text, "loaded_version") {
		t.Errorf("purge without loaded_version must be refused naming it: %s", resp.Content[0].Text)
	}
	if art, _ := srv.Storage.GetArticle("guarded-doc"); art.Version != 1 {
		t.Error("a refused purge must not save either")
	}

	resp = saveCall(t, srv, map[string]interface{}{"title": "Brand New", "content": "# x", "purge_history": true})
	if !resp.IsError {
		t.Errorf("purge on a create must be refused: %s", resp.Content[0].Text)
	}
	if _, err := srv.Storage.GetArticle("brand-new"); err == nil {
		t.Error("a refused purge-on-create must not create the document")
	}

	// A stale loaded_version is still a conflict: you cannot purge revisions you have not seen.
	saveCall(t, srv, map[string]interface{}{"slug": "guarded-doc", "title": "Guarded Doc", "content": "# v2", "loaded_version": 1})
	resp = saveCall(t, srv, map[string]interface{}{"slug": "guarded-doc", "title": "Guarded Doc", "content": "# v3", "loaded_version": 1, "purge_history": true})
	if !resp.IsError {
		t.Error("a stale loaded_version must refuse the purge")
	}
	if got := historyFiles(t, srv, "guarded-doc"); len(got) != 2 {
		t.Errorf("history after a refused purge = %v, want both revisions", got)
	}
}

// TestPurgeHistoryKeepsNewerRevisions pins the concurrency contract: the purge keeps the version
// the caller wrote and anything newer, so a save that lands in between is never purged unseen.
func TestPurgeHistoryKeepsNewerRevisions(t *testing.T) {
	srv := newPurgeServer(t)
	seedTyped(t, srv, "Busy Doc", ContentTypeWiki)
	for v := 1; v <= 3; v++ {
		saveCall(t, srv, map[string]interface{}{"slug": "busy-doc", "title": "Busy Doc", "content": fmt.Sprintf("# v%d", v+1), "loaded_version": v})
	}
	purge, err := srv.Storage.PurgeHistory("busy-doc", 3)
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(purge.Removed) != "[1 2]" {
		t.Errorf("removed %v, want [1 2]", purge.Removed)
	}
	if got := historyFiles(t, srv, "busy-doc"); fmt.Sprint(got) != "[3.md.gz 4.md.gz]" {
		t.Errorf("history = %v, want versions 3 and 4 kept", got)
	}
	// The next save continues the counter; versions are never renumbered.
	resp := saveCall(t, srv, map[string]interface{}{"slug": "busy-doc", "title": "Busy Doc", "content": "# v5", "loaded_version": 4})
	if resp.IsError || !strings.Contains(resp.Content[0].Text, "New Version: 5") {
		t.Errorf("the version counter must continue at 5: %s", resp.Content[0].Text)
	}
}

func TestRESTPurgeHistory(t *testing.T) {
	srv := newPurgeServer(t)
	seedTyped(t, srv, "Rest Redact", ContentTypeWiki)
	saveCall(t, srv, map[string]interface{}{"slug": "rest-redact", "title": "Rest Redact", "content": "# secret-name", "loaded_version": 1})

	put := func(body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("PUT", "/api/articles/rest-redact", strings.NewReader(body))
		req.SetPathValue("slug", "rest-redact")
		w := httptest.NewRecorder()
		srv.HandleUpdateArticle(w, req)
		return w
	}
	if w := put(`{"title":"Rest Redact","content":"# clean","purge_history":true}`); w.Code != http.StatusBadRequest {
		t.Errorf("PUT purge without loaded_version: %d, want 400", w.Code)
	}
	w := put(`{"title":"Rest Redact","content":"# clean","purge_history":true,"loaded_version":2}`)
	if w.Code != http.StatusOK {
		t.Fatalf("PUT purge: %d %s", w.Code, w.Body.String())
	}
	var body struct {
		Slug                    string `json:"slug"`
		Version                 int    `json:"version"`
		RevisionsRemoved        []int  `json:"revisions_removed"`
		ActivityEntriesRetitled *int   `json:"activity_entries_retitled"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Slug != "rest-redact" || body.Version != 3 || fmt.Sprint(body.RevisionsRemoved) != "[1 2]" || body.ActivityEntriesRetitled == nil {
		t.Errorf("PUT purge response = %+v (%s)", body, w.Body.String())
	}
	if m, _ := historyScan(t, srv, "secret-name", nil); len(m) != 0 {
		t.Errorf("the purged text is still stored: %+v", m)
	}

	// Without purge_history the PUT response shape is unchanged.
	w = put(`{"title":"Rest Redact","content":"# again","loaded_version":3}`)
	if strings.Contains(w.Body.String(), "revisions_removed") {
		t.Errorf("an ordinary PUT must not report a purge: %s", w.Body.String())
	}

	req := httptest.NewRequest("POST", "/api/articles", strings.NewReader(`{"title":"Rest New","content":"x","purge_history":true}`))
	w = httptest.NewRecorder()
	srv.HandleCreateArticle(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("POST with purge_history: %d, want 400", w.Code)
	}
}

// TestSearchIncludeHistory covers the audit half: a term that survives only in an old revision is
// found, filters do not narrow the scan, and a live file with no history at all is still covered.
func TestSearchIncludeHistory(t *testing.T) {
	srv := newPurgeServer(t)
	seedTyped(t, srv, "Audit Target", ContentTypeWiki)
	saveCall(t, srv, map[string]interface{}{"slug": "audit-target", "title": "Audit Target", "content": "# rewritten", "loaded_version": 1})

	// "audit target" is in both revisions' front matter; "Body." only in revision 1's body.
	m, text := historyScan(t, srv, "body.", nil)
	if len(m) != 1 || m[0].Slug != "audit-target" || m[0].Version != 1 || m[0].Current {
		t.Errorf("history_matches = %+v, want only revision 1, not current", m)
	}

	// Bleve cannot see it (current text only), the history scan can.
	var out SearchOutput
	decodeStructured(t, agentCall(t, srv, "search_wiki", map[string]interface{}{"query": "rewritten", "include_history": true}), &out)
	if len(*out.HistoryMatches) != 1 || !(*out.HistoryMatches)[0].Current || (*out.HistoryMatches)[0].Version != 2 {
		t.Errorf("the current revision must be reported once, as current: %+v", *out.HistoryMatches)
	}
	if !strings.Contains(text, "version 1 — earlier revision") {
		t.Errorf("prose must list the match:\n%s", text)
	}

	// Filters narrow Bleve results, never the audit.
	m, text = historyScan(t, srv, "body.", map[string]interface{}{"type": "skills", "tag": "nothing-has-this"})
	if len(m) != 1 {
		t.Errorf("a type/tag filter narrowed the history scan: %+v", m)
	}
	if !strings.Contains(text, "do not narrow this scan") {
		t.Errorf("the response must say the filters do not narrow the scan:\n%s", text)
	}

	// A document written before version history existed has only its live file.
	legacy := "---\ntitle: Legacy Page\nslug: legacy-page\ntype: Wiki\n---\n# Legacy\n\nMentions an EmbargoedName.\n"
	if err := os.WriteFile(filepath.Join(srv.Storage.ArticleDir, "legacy-page.md"), []byte(legacy), 0644); err != nil {
		t.Fatal(err)
	}
	m, _ = historyScan(t, srv, "embargoedname", nil)
	if len(m) != 1 || m[0].Slug != "legacy-page" || !m[0].Current {
		t.Errorf("a live file with no history must be scanned: %+v", m)
	}

	// Quoted as a Bleve phrase, the literal is still found.
	if m, _ = historyScan(t, srv, `"EmbargoedName"`, nil); len(m) != 1 {
		t.Errorf("surrounding quotes must be stripped: %+v", m)
	}

	// Without include_history nothing about history is emitted.
	plain := agentCall(t, srv, "search_wiki", map[string]interface{}{"query": "rewritten"})
	encoded, _ := json.Marshal(plain.StructuredContent)
	if strings.Contains(string(encoded), "history") || strings.Contains(plain.Content[0].Text, "Version history scan") {
		t.Errorf("an ordinary search must not scan history: %s", encoded)
	}
}

// TestRetitleActivityFilesPreservesArchivesAndOtherEvents covers the durable rewrite in isolation:
// rotated archives are rewritten too, unrelated events and unparseable lines are untouched, and an
// archive keeps the modification time its ordering depends on.
func TestRetitleActivityFilesPreservesArchivesAndOtherEvents(t *testing.T) {
	dir := t.TempDir()
	line := func(slug, title string) string {
		raw, _ := json.Marshal(LogEvent{ID: "evt", Slug: slug, Title: title, Action: "edit"})
		return string(raw) + "\n"
	}
	active := line("doc", "Old Title") + "not json\n" + line("other", "Old Title")
	archive := filepath.Join(dir, ActivityLogFilename+".1")
	if err := os.WriteFile(ActivityLogPath(dir), []byte(active), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(archive, []byte(line("old-doc-slug", "Older Title")+line("doc", "New Title")), 0644); err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(archive)

	n, activeRewritten, err := retitleActivityFiles(dir, map[string]bool{"doc": true, "old-doc-slug": true}, "doc", "New Title")
	if err != nil || n != 2 || !activeRewritten {
		t.Fatalf("retitled %d (active rewritten %v), err %v; want 2, true", n, activeRewritten, err)
	}
	got, _ := os.ReadFile(ActivityLogPath(dir))
	if !strings.Contains(string(got), "not json\n") || !strings.Contains(string(got), `"slug":"other","title":"Old Title"`) {
		t.Errorf("unrelated lines must be untouched:\n%s", got)
	}
	arch, _ := os.ReadFile(archive)
	if strings.Contains(string(arch), "Older Title") || strings.Contains(string(arch), "old-doc-slug") {
		t.Errorf("the archive was not rewritten:\n%s", arch)
	}
	if after, _ := os.Stat(archive); !after.ModTime().Equal(info.ModTime()) {
		t.Errorf("archive mtime changed %v → %v; fallback archives are ordered by it", info.ModTime(), after.ModTime())
	}
}

// TestSearchIncludeHistoryAcceptsQueriesBleveRejects: the history scan takes the query literally,
// so an audit term that happens to be invalid Bleve syntax (an IP range, a regex fragment) must
// still be scanned rather than failing the whole call.
func TestSearchIncludeHistoryAcceptsQueriesBleveRejects(t *testing.T) {
	srv := newPurgeServer(t)
	saveCall(t, srv, map[string]interface{}{"title": "Regex Notes", "content": "# Notes\n\nThe pattern /[/ breaks parsers."})

	if plain := agentCall(t, srv, "search_wiki", map[string]interface{}{"query": "/[/"}); !plain.IsError {
		t.Skip("Bleve now accepts this query; pick another invalid one")
	}
	m, text := historyScan(t, srv, "/[/", nil)
	if len(m) != 1 || m[0].Slug != "regex-notes" {
		t.Errorf("history_matches = %+v, want the note", m)
	}
	if !strings.Contains(text, "Index search failed") {
		t.Errorf("the failed index half must be reported:\n%s", text)
	}
}

// TestSearchWikiDeclaresEveryFilterItHonours: memory_kind and include_archived were decoded and
// applied by the handler but missing from the input schema, so no agent reading tools/list could
// find them.
func TestSearchWikiDeclaresEveryFilterItHonours(t *testing.T) {
	props := searchWikiTool.Schema["inputSchema"].(map[string]interface{})["properties"].(map[string]interface{})
	for _, name := range []string{"query", "type", "tag", "limit", "memory_kind", "include_archived", "include_history"} {
		if _, ok := props[name]; !ok {
			t.Errorf("search_wiki honours %q but its input schema does not declare it", name)
		}
	}
}
