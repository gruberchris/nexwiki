package server

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"
)

// The bulk writes — web global tag deletion, web OKF import, and the MCP import — used to change
// documents without a proper trace. The web ones published no activity event, so the audit trail
// missed them, and no live update, so other open tabs went stale; the MCP import was logged as a
// single slug-less "read". These tests observe them through a real EventBus, the way the other
// handlers are observed.

// busObserver captures what a bulk write announces: the activity events it adds to the bus history,
// the structured updates MCP subscribers receive, and the SSE frames browsers receive.
type busObserver struct {
	srv          *Server
	historyStart int
	updates      chan WikiUpdate
	browser      chan string
}

func observeBus(t *testing.T, srv *Server) *busObserver {
	t.Helper()
	obs := &busObserver{
		srv:          srv,
		historyStart: len(srv.EventBus.GetHistory()),
		updates:      srv.EventBus.SubscribeWikiUpdates(),
		browser:      srv.EventBus.Subscribe(),
	}
	t.Cleanup(func() {
		srv.EventBus.UnsubscribeWikiUpdates(obs.updates)
		srv.EventBus.Unsubscribe(obs.browser)
	})
	return obs
}

// activity returns the events published since observation began, keyed by slug.
func (o *busObserver) activity(t *testing.T) map[string]LogEvent {
	t.Helper()
	out := map[string]LogEvent{}
	for _, ev := range o.srv.EventBus.GetHistory()[o.historyStart:] {
		if _, dup := out[ev.Slug]; dup {
			t.Errorf("more than one activity event for %q", ev.Slug)
		}
		out[ev.Slug] = ev
	}
	return out
}

// wikiUpdates collects exactly n structured updates, keyed by slug, failing on a timeout or an extra
// one. Publishing is synchronous, so once the handler has returned everything is already buffered.
func (o *busObserver) wikiUpdates(t *testing.T, n int) map[string]WikiUpdate {
	t.Helper()
	out := map[string]WikiUpdate{}
	for i := 0; i < n; i++ {
		select {
		case u := <-o.updates:
			out[u.Slug] = u
		case <-time.After(3 * time.Second):
			t.Fatalf("timed out after %d of %d wiki updates", i, n)
		}
	}
	select {
	case u := <-o.updates:
		t.Errorf("unexpected extra wiki update: %+v", u)
	default:
	}
	return out
}

// browserUpdateFrames counts the wiki-update SSE frames a browser tab was sent.
func (o *busObserver) browserUpdateFrames() int {
	n := 0
	for {
		select {
		case frame := <-o.browser:
			if strings.HasPrefix(frame, "event: wiki-update\n") {
				n++
			}
		default:
			return n
		}
	}
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func TestHandleDeleteTagGloballyPublishesEachChangedDocument(t *testing.T) {
	srv := newTestServer(t)
	seed := []struct {
		title string
		tags  []string
	}{
		{"Tagged One", []string{"removable", "keep"}},
		{"Tagged Two", []string{"Removable"}}, // the sweep matches case-insensitively
		{"Untagged", []string{"keep"}},
	}
	for _, s := range seed {
		if _, err := srv.Storage.SaveArticle("", s.title, "# body", "", "", "", "", s.tags, ContentTypeWiki); err != nil {
			t.Fatalf("seed %q: %v", s.title, err)
		}
	}
	obs := observeBus(t, srv)

	req := httptest.NewRequest("DELETE", "/api/tags/removable", nil)
	req.SetPathValue("tag", "removable")
	w := httptest.NewRecorder()
	srv.HandleDeleteTagGlobally(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	// The counts report what the sweep did: two rewritten, nothing skipped, nothing failed.
	var body struct {
		Message   string `json:"message"`
		Rewritten int    `json:"rewritten"`
		Skipped   int    `json:"skipped"`
		Failed    int    `json:"failed"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || body.Message != "tag deleted globally successfully" {
		t.Errorf("response shape changed: %s", w.Body.String())
	}
	if body.Rewritten != 2 || body.Skipped != 0 || body.Failed != 0 {
		t.Errorf("counts do not match the sweep: %+v", body)
	}

	want := []string{"tagged-one", "tagged-two"}
	events := obs.activity(t)
	if got := sortedKeys(events); !slices.Equal(got, want) {
		t.Fatalf("activity events for %v, want exactly the rewritten documents %v", got, want)
	}
	for slug, ev := range events {
		if ev.Source != "api" || ev.Action != "edit" || ev.Tool != "delete_tag" || ev.Agent != "User" || ev.Title == "" {
			t.Errorf("%s: unexpected event %+v", slug, ev)
		}
	}

	updates := obs.wikiUpdates(t, len(want))
	for _, slug := range want {
		u, ok := updates[slug]
		if !ok {
			t.Errorf("no wiki update for %s", slug)
			continue
		}
		if u.Type != "article-edited" || u.Directory != "wiki" || u.TotalCount != 3 || u.DirectoryCount != 3 {
			t.Errorf("%s: unexpected wiki update %+v", slug, u)
		}
		if slices.ContainsFunc(u.Tags, func(tag string) bool { return strings.EqualFold(tag, "removable") }) {
			t.Errorf("%s: update still carries the deleted tag: %v", slug, u.Tags)
		}
	}
	if n := obs.browserUpdateFrames(); n != len(want) {
		t.Errorf("browser tabs were sent %d wiki-update frames, want %d", n, len(want))
	}

	// Deleting a tag nothing carries changes nothing, so it announces nothing.
	obs2 := observeBus(t, srv)
	req2 := httptest.NewRequest("DELETE", "/api/tags/removable", nil)
	req2.SetPathValue("tag", "removable")
	srv.HandleDeleteTagGlobally(httptest.NewRecorder(), req2)
	if events := obs2.activity(t); len(events) != 0 {
		t.Errorf("a no-op deletion published activity: %v", events)
	}
	obs2.wikiUpdates(t, 0)
}

// TestHandleDeleteTagGloballyAnnouncesAPartialSweep covers a sweep that fails partway. The
// documents it rewrote before failing stay rewritten, so they are announced despite the error; the
// ones it never reached still carry the tag and changed nothing, so they are not.
func TestHandleDeleteTagGloballyAnnouncesAPartialSweep(t *testing.T) {
	srv := newTestServer(t)
	// Written directly so the timestamps fix the sweep order (newest first), and so the plan can
	// carry a status that no longer validates, which makes its save fail.
	for slug, doc := range map[string]string{
		"newer":       "---\ntype: Wiki\ntitle: Newer\nslug: newer\ntags:\n  - removable\ntimestamp: \"2022-01-01T00:00:00Z\"\nversion: 1\n---\n# c\n",
		"broken-plan": "---\ntype: AI-Agent-Plan\ntitle: Broken Plan\nslug: broken-plan\nstatus: bogus\ntags:\n  - removable\ntimestamp: \"2021-01-01T00:00:00Z\"\nversion: 1\n---\n# b\n",
		"older":       "---\ntype: Wiki\ntitle: Older\nslug: older\ntags:\n  - removable\ntimestamp: \"2020-01-01T00:00:00Z\"\nversion: 1\n---\n# a\n",
	} {
		if err := os.WriteFile(filepath.Join(srv.Storage.ArticleDir, slug+".md"), []byte(doc), 0644); err != nil {
			t.Fatalf("seed %s: %v", slug, err)
		}
	}
	obs := observeBus(t, srv)

	req := httptest.NewRequest("DELETE", "/api/tags/removable", nil)
	req.SetPathValue("tag", "removable")
	w := httptest.NewRecorder()
	srv.HandleDeleteTagGlobally(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected the failed sweep to report 500, got %d: %s", w.Code, w.Body.String())
	}
	// The failure exit carries the partial state: one document rewritten before the save failed,
	// one failure, and the 500 names what stopped the sweep.
	var body struct {
		Error     string `json:"error"`
		Rewritten int    `json:"rewritten"`
		Skipped   int    `json:"skipped"`
		Failed    int    `json:"failed"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid error body: %s", w.Body.String())
	}
	if body.Rewritten != 1 || body.Failed != 1 || body.Skipped != 0 {
		t.Errorf("the partial state is not reported: %+v", body)
	}
	if !strings.Contains(body.Error, "broken-plan") {
		t.Errorf("the error does not name the document that stopped the sweep: %q", body.Error)
	}
	if older, err := srv.Storage.GetArticle("older"); err != nil || !slices.Contains(older.Tags, "removable") {
		t.Fatalf("the sweep was expected to stop before reaching 'older': %+v, %v", older, err)
	}
	events := obs.activity(t)
	if got := sortedKeys(events); !slices.Equal(got, []string{"newer"}) {
		t.Errorf("activity events for %v, want only the document rewritten before the failure", got)
	}
	if updates := obs.wikiUpdates(t, 1); updates["newer"].Type != "article-edited" {
		t.Errorf("unexpected wiki updates %+v", updates)
	}
}

// TestHandleDeleteTagGloballyRemovesEveryCaseVariant covers a document carrying the tag in two
// cases. The sweep removes every case-insensitive variant in the one rewrite, so the document is
// announced — it changed — and no variant survives it.
func TestHandleDeleteTagGloballyRemovesEveryCaseVariant(t *testing.T) {
	srv := newTestServer(t)
	// Written directly, so the duplicate survives regardless of how a save normalizes tags.
	doc := "---\ntype: Wiki\ntitle: Both Cases\nslug: both-cases\ntags:\n  - removable\n  - Removable\nversion: 1\n---\n# body\n"
	if err := os.WriteFile(filepath.Join(srv.Storage.ArticleDir, "both-cases.md"), []byte(doc), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	obs := observeBus(t, srv)

	req := httptest.NewRequest("DELETE", "/api/tags/removable", nil)
	req.SetPathValue("tag", "removable")
	w := httptest.NewRecorder()
	srv.HandleDeleteTagGlobally(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	art, err := srv.Storage.GetArticle("both-cases")
	if err != nil || art.Version != 2 || slices.ContainsFunc(art.Tags, func(tag string) bool { return strings.EqualFold(tag, "removable") }) {
		t.Fatalf("expected one rewrite with no case variant left, got %+v (%v)", art, err)
	}
	if ev := obs.activity(t)["both-cases"]; ev.Action != "edit" || ev.Tool != "delete_tag" {
		t.Errorf("the rewritten document was not announced: %+v", ev)
	}
	if u := obs.wikiUpdates(t, 1)["both-cases"]; u.Type != "article-edited" {
		t.Errorf("unexpected wiki update %+v", u)
	}
}

// okfBundle zips the given files into an OKF bundle.
func okfBundle(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, name := range sortedKeys(files) {
		fw, err := zw.Create(name)
		if err != nil {
			t.Fatalf("zip create %s: %v", name, err)
		}
		if _, err := fw.Write([]byte(files[name])); err != nil {
			t.Fatalf("zip write %s: %v", name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}
	return buf.Bytes()
}

func TestHandleImportOKFBundlePublishesEachSavedDocument(t *testing.T) {
	srv := newTestServer(t)
	if _, err := srv.Storage.SaveArticle("", "Existing Article", "# old", "", "", "", "", nil, ContentTypeWiki); err != nil {
		t.Fatalf("seed: %v", err)
	}
	obs := observeBus(t, srv)

	bundle := okfBundle(t, map[string]string{
		"wiki/existing-article.md": "---\ntype: Wiki\ntitle: Existing Article\nslug: existing-article\n---\n# replaced\n",
		"aiplans/new-plan.md":      "---\ntype: AI-Agent-Plan\ntitle: New Plan\nslug: new-plan\nstatus: draft\n---\n# plan\n",
		// Neither of these changes anything: a reserved file is consumed, and a document whose
		// title yields no slug fails to save. Neither may be announced.
		"index.md":         "# Knowledge Base\n",
		"wiki/untitled.md": "---\ntype: Wiki\ntitle: \"!!!\"\nslug: untitled\n---\n# nothing\n",
	})

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, _ := mw.CreateFormFile("file", "bundle.zip")
	_, _ = fw.Write(bundle)
	_ = mw.Close()
	req := httptest.NewRequest("POST", "/api/okf/import", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	w := httptest.NewRecorder()
	srv.HandleImportOKFBundle(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	// The saved-document list rides on the report internally; it must not leak into the response.
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(w.Body.Bytes(), &raw); err != nil {
		t.Fatalf("invalid report: %v", err)
	}
	if got := sortedKeys(raw); !slices.Equal(got, []string{"imported", "missing_type", "skipped", "warnings"}) {
		t.Errorf("response shape changed: keys %v", got)
	}
	var report OKFImportReport
	_ = json.Unmarshal(w.Body.Bytes(), &report)
	if report.Imported != 2 || len(report.Warnings) != 1 {
		t.Fatalf("bundle did not import as the test assumes: %+v", report)
	}

	events := obs.activity(t)
	if got := sortedKeys(events); !slices.Equal(got, []string{"existing-article", "new-plan"}) {
		t.Fatalf("activity events for %v, want exactly the imported documents", got)
	}
	for slug, wantAction := range map[string]string{"existing-article": "edit", "new-plan": "create"} {
		ev := events[slug]
		if ev.Source != "api" || ev.Action != wantAction || ev.Tool != "okf_import" || ev.Agent != "User" {
			t.Errorf("%s: event %+v, want source api, action %s, tool okf_import, agent User", slug, ev, wantAction)
		}
	}

	updates := obs.wikiUpdates(t, 2)
	if u := updates["existing-article"]; u.Type != "article-edited" || u.Directory != "wiki" || u.TotalCount != 2 || u.DirectoryCount != 1 {
		t.Errorf("existing-article: unexpected wiki update %+v", u)
	}
	if u := updates["new-plan"]; u.Type != "article-added" || u.Directory != "aiplans" || u.TotalCount != 2 || u.DirectoryCount != 1 {
		t.Errorf("new-plan: unexpected wiki update %+v", u)
	}
	if n := obs.browserUpdateFrames(); n != 2 {
		t.Errorf("browser tabs were sent %d wiki-update frames, want 2", n)
	}
}

// TestMCPImportOKFBundleLogsEachSavedDocument covers the MCP import, which used to be logged as one
// "read" with no slug and no live update. It goes through the HTTP endpoint so the agent is resolved
// exactly as for any other call: here the forwarded client name beats the primary's fallback.
func TestMCPImportOKFBundleLogsEachSavedDocument(t *testing.T) {
	srv := newTestServer(t)
	srv.AgentName = "Primary Fallback"
	if _, err := srv.Storage.SaveArticle("", "Existing Article", "# old", "", "", "", "", nil, ContentTypeWiki); err != nil {
		t.Fatalf("seed: %v", err)
	}

	importBundle := func(t *testing.T, files map[string]string) string {
		t.Helper()
		path := filepath.Join(srv.Storage.DataDir, "bundle.zip")
		if err := os.WriteFile(path, okfBundle(t, files), 0644); err != nil {
			t.Fatalf("write bundle: %v", err)
		}
		args, _ := json.Marshal(map[string]string{"path": path})
		body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"import_okf_bundle","arguments":` + string(args) + `}}`
		req := httptest.NewRequest("POST", "/api/mcp", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set(clientNameHeader, "importer")
		w := httptest.NewRecorder()
		srv.HandleStreamableHTTP(w, req)

		var resp struct {
			Error  interface{}  `json:"error"`
			Result ToolResponse `json:"result"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil || resp.Error != nil || resp.Result.IsError {
			t.Fatalf("import call failed (%v): %s", err, w.Body.String())
		}
		return resp.Result.Content[0].Text
	}

	obs := observeBus(t, srv)
	text := importBundle(t, map[string]string{
		"wiki/existing-article.md": "---\ntype: Wiki\ntitle: Existing Article\nslug: existing-article\n---\n# replaced\n",
		"aiplans/new-plan.md":      "---\ntype: AI-Agent-Plan\ntitle: New Plan\nslug: new-plan\nstatus: draft\n---\n# plan\n",
		"index.md":                 "# Knowledge Base\n",
		"wiki/untitled.md":         "---\ntype: Wiki\ntitle: \"!!!\"\nslug: untitled\n---\n# nothing\n",
	})
	if !strings.Contains(text, "2 imported") {
		t.Fatalf("bundle did not import as the test assumes: %s", text)
	}

	events := obs.activity(t)
	if got := sortedKeys(events); !slices.Equal(got, []string{"existing-article", "new-plan"}) {
		t.Fatalf("activity events for %v, want one per imported document and no per-call entry", got)
	}
	for slug, wantAction := range map[string]string{"existing-article": "edit", "new-plan": "create"} {
		ev := events[slug]
		if ev.Source != "mcp" || ev.Action != wantAction || ev.Tool != "import_okf_bundle" || ev.Agent != "importer" {
			t.Errorf("%s: event %+v, want source mcp, action %s, tool import_okf_bundle, agent importer", slug, ev, wantAction)
		}
	}
	updates := obs.wikiUpdates(t, 2)
	if updates["existing-article"].Type != "article-edited" || updates["new-plan"].Type != "article-added" {
		t.Errorf("unexpected wiki updates %+v", updates)
	}

	// An import that saves nothing changed nothing: no events at all, and in particular no read.
	obs2 := observeBus(t, srv)
	importBundle(t, map[string]string{"index.md": "# Knowledge Base\n"})
	if events := obs2.activity(t); len(events) != 0 {
		t.Errorf("an import that saved nothing published activity: %v", events)
	}
	obs2.wikiUpdates(t, 0)
}

// TestHandleVerifyArticleIsBroadcastAndAttributed covers the web verify. It saves a new revision,
// so it must refresh open tabs like any other write, and get_article_history must credit that
// revision to it — which it could not while "verify" was missing from writeActions.
func TestHandleVerifyArticleIsBroadcastAndAttributed(t *testing.T) {
	srv := newTestServer(t)
	persistPrimaryActivity(t, srv)

	create := httptest.NewRequest("POST", "/api/articles", strings.NewReader(`{"title": "Verify Me", "content": "# body"}`))
	cw := httptest.NewRecorder()
	srv.HandleCreateArticle(cw, create)
	if cw.Code != http.StatusCreated {
		t.Fatalf("create: expected 201, got %d: %s", cw.Code, cw.Body.String())
	}

	obs := observeBus(t, srv)
	req := httptest.NewRequest("POST", "/api/articles/verify-me/verify", nil)
	req.SetPathValue("slug", "verify-me")
	w := httptest.NewRecorder()
	srv.HandleVerifyArticle(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("verify: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var saved Article
	if err := json.Unmarshal(w.Body.Bytes(), &saved); err != nil || saved.Version != 2 {
		t.Fatalf("verify was expected to save revision 2, got %+v (%v)", saved, err)
	}

	if ev := obs.activity(t)["verify-me"]; ev.Source != "api" || ev.Action != "verify" {
		t.Errorf("unexpected verify event %+v", ev)
	}
	if u := obs.wikiUpdates(t, 1)["verify-me"]; u.Type != "article-edited" || u.Directory != "wiki" || u.TotalCount != 1 {
		t.Errorf("unexpected wiki update %+v", u)
	}

	hreq := httptest.NewRequest("GET", "/api/articles/verify-me/history", nil)
	hreq.SetPathValue("slug", "verify-me")
	hw := httptest.NewRecorder()
	srv.HandleGetArticleHistory(hw, hreq)
	var history []historyEntryResponse
	if err := json.Unmarshal(hw.Body.Bytes(), &history); err != nil {
		t.Fatalf("history: %v: %s", err, hw.Body.String())
	}
	byVersion := map[int]historyEntryResponse{}
	for _, entry := range history {
		byVersion[entry.Version] = entry
	}
	if v2 := byVersion[2]; v2.Via != "api" || v2.Agent != "User" {
		t.Errorf("the verify revision is unattributed: %+v", v2)
	}
	if v1 := byVersion[1]; v1.Via != "api" || v1.Agent != "User" {
		t.Errorf("the create revision is unattributed: %+v", v1)
	}
}
