package server

import (
	"errors"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

// writeTitleMismatchedDocument writes a canonical document at articles/my-slug.md whose title, as if
// edited outside NexWiki, slugifies to other-title. extra is front matter beyond title, slug, and
// version.
func writeTitleMismatchedDocument(t *testing.T, storage *Storage, version int, extra, body string) {
	t.Helper()
	writeArticleFile(t, storage, "my-slug.md",
		"title: Other Title\nslug: my-slug\nversion: "+strconv.Itoa(version)+"\n"+extra, body, time.Now().Add(-time.Hour))
}

// writeHistorySnapshot writes a history snapshot for slug directly, the way an earlier save would
// have.
func writeHistorySnapshot(t *testing.T, storage *Storage, slug string, version int, frontMatter, body string) {
	t.Helper()
	dir := filepath.Join(storage.HistoryDir, slug)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("MkdirAll failed: %v", err)
	}
	// Hold writeMu: the history temp-file sweep runs in the background alongside
	// live saves, and a temp file mid-write looks exactly like a leftover.
	storage.writeMu.Lock()
	defer storage.writeMu.Unlock()
	if err := writeGzippedFile(filepath.Join(dir, strconv.Itoa(version)+".md.gz"), []byte("---\n"+frontMatter+"---\n"+body)); err != nil {
		t.Fatalf("writeGzippedFile failed: %v", err)
	}
}

// expectNotAt fails the test if anything exists at a path relative to the data directory.
func expectNotAt(t *testing.T, storage *Storage, rel string) {
	t.Helper()
	if _, err := os.Lstat(filepath.Join(storage.DataDir, filepath.FromSlash(rel))); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("%s should not exist (stat error %v)", rel, err)
	}
}

// restCall runs a REST handler for a slug and returns the recorder.
func restCall(handle http.HandlerFunc, method, slug, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, "/api/articles/"+slug, strings.NewReader(body))
	if slug != "" {
		req.SetPathValue("slug", slug)
	}
	w := httptest.NewRecorder()
	handle(w, req)
	return w
}

// TestSavesThatAreNotTitleEditsKeepTheSlug pins that only an explicit title edit renames a document.
// my-slug.md is canonical, but its title yields other-title, so every save that derived the slug
// from the title moved it there: a status change, a re-tag, an append, a revert, a verification, the
// status migration, the plan lifecycle worker, and a bundle re-import.
func TestSavesThatAreNotTitleEditsKeepTheSlug(t *testing.T) {
	const (
		plan   = "type: AI-Agent-Plan\n"
		memory = "type: AI-Agent-Memory\n"
		skill  = "type: AI-Agent-Skill\n"
	)
	// doc is a setup writing the document at version 1 with extra front matter.
	doc := func(extra string) func(t *testing.T, s *Storage) {
		return func(t *testing.T, s *Storage) { writeTitleMismatchedDocument(t, s, 1, extra, "body\n") }
	}
	// A document at version 2 with an older snapshot, for the reverts.
	revertable := func(t *testing.T, s *Storage) {
		writeTitleMismatchedDocument(t, s, 2, "description: current\n", "current body\n")
		writeHistorySnapshot(t, s, "my-slug", 1, "title: Ancient Title\nslug: ancient-title\nversion: 1\ndescription: old\n", "old body\n")
	}
	mcp := func(call string) func(t *testing.T, srv *Server) {
		return func(t *testing.T, srv *Server) {
			t.Helper()
			if resp := toolCall(t, srv, call); resp.IsError {
				t.Fatalf("tool call failed: %s", resp.Content[0].Text)
			}
		}
	}
	rest := func(handle func(*Server, http.ResponseWriter, *http.Request), method, body string) func(t *testing.T, srv *Server) {
		return func(t *testing.T, srv *Server) {
			t.Helper()
			bound := func(w http.ResponseWriter, r *http.Request) { handle(srv, w, r) }
			if w := restCall(bound, method, "my-slug", body); w.Code != http.StatusOK {
				t.Fatalf("status %d: %s", w.Code, w.Body.String())
			}
		}
	}
	noErr := func(t *testing.T, err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("save failed: %v", err)
		}
	}

	for _, tc := range []struct {
		name  string
		setup func(t *testing.T, s *Storage)
		save  func(t *testing.T, srv *Server)
		check func(t *testing.T, art *Article)
	}{
		{
			name:  "SetStatus",
			setup: doc(plan + "status: implementing\n"),
			save: func(t *testing.T, srv *Server) {
				_, err := srv.Storage.SetStatus("my-slug", "completed", 1, "")
				noErr(t, err)
			},
			check: func(t *testing.T, art *Article) {
				if art.Status != "completed" {
					t.Errorf("status = %q, want completed", art.Status)
				}
			},
		},
		{
			name:  "plan lifecycle worker archive",
			setup: doc(plan + "status: completed\nstatus_changed_at: 2020-01-01T00:00:00Z\n"),
			save: func(t *testing.T, srv *Server) {
				w := &PlanLifecycleWorker{Storage: srv.Storage, Cfg: PlanLifecycleConfig{ArchiveAfterDays: 90, DeleteAfterDays: 365}}
				captureWorkerLog(w)
				w.Sweep()
			},
			check: func(t *testing.T, art *Article) {
				if art.Status != StatusArchived {
					t.Errorf("status = %q, want archived", art.Status)
				}
			},
		},
		{
			name:  "plan lifecycle worker backfill",
			setup: doc(plan),
			save: func(t *testing.T, srv *Server) {
				w := &PlanLifecycleWorker{Storage: srv.Storage, Cfg: PlanLifecycleConfig{ArchiveAfterDays: 90, DeleteAfterDays: 365}}
				captureWorkerLog(w)
				w.Sweep()
			},
			check: func(t *testing.T, art *Article) {
				if art.Status != DefaultPlanStatus {
					t.Errorf("status = %q, want %s", art.Status, DefaultPlanStatus)
				}
			},
		},
		{
			name:  "UpdateArticleTags",
			setup: doc(""),
			save: func(t *testing.T, srv *Server) {
				_, err := srv.Storage.UpdateArticleTags("my-slug", []string{"fresh"}, 1, "")
				noErr(t, err)
			},
			check: func(t *testing.T, art *Article) {
				if !reflect.DeepEqual(art.Tags, []string{"fresh"}) {
					t.Errorf("tags = %v, want [fresh]", art.Tags)
				}
			},
		},
		{
			name:  "DeleteTagGlobally",
			setup: doc("tags:\n  - doomed\n  - kept\n"),
			save: func(t *testing.T, srv *Server) {
				_, err := srv.Storage.DeleteTagGlobally("doomed")
				noErr(t, err)
			},
			check: func(t *testing.T, art *Article) {
				if !reflect.DeepEqual(art.Tags, []string{"kept"}) {
					t.Errorf("tags = %v, want [kept]", art.Tags)
				}
			},
		},
		{
			name:  "MigrateStatusToField",
			setup: doc(plan + "tags:\n  - nexwiki\n  - wip\n"),
			save: func(t *testing.T, srv *Server) {
				if err := os.Remove(filepath.Join(srv.Storage.DataDir, statusFieldMigrationMarker)); err != nil {
					t.Fatalf("remove marker: %v", err)
				}
				captureLog(t)
				noErr(t, srv.Storage.MigrateStatusToField())
			},
			check: func(t *testing.T, art *Article) {
				if art.Status != "implementing" || !reflect.DeepEqual(art.Tags, []string{"nexwiki"}) {
					t.Errorf("status %q, tags %v; want implementing, [nexwiki]", art.Status, art.Tags)
				}
			},
		},
		{
			name:  "RevertArticle",
			setup: revertable,
			save: func(t *testing.T, srv *Server) {
				_, err := srv.Storage.RevertArticle("my-slug", 1)
				noErr(t, err)
			},
			check: func(t *testing.T, art *Article) {
				if art.Content != "old body" || art.Description != "old" {
					t.Errorf("content %q, description %q; want the snapshot's", art.Content, art.Description)
				}
			},
		},
		{
			name:  "REST revert",
			setup: revertable,
			save:  rest((*Server).HandleRevertArticle, http.MethodPost, `{"version":1}`),
		},

		{
			name:  "REST verify",
			setup: doc(""),
			save:  rest((*Server).HandleVerifyArticle, http.MethodPost, ""),
			check: func(t *testing.T, art *Article) {
				if len(art.Verified) != 1 {
					t.Errorf("verified = %+v, want one record", art.Verified)
				}
			},
		},
		{
			name:  "REST tags",
			setup: doc(""),
			save:  rest((*Server).HandleUpdateArticleTags, http.MethodPut, `{"tags":["fresh"],"loaded_version":1}`),
		},
		{
			name:  "MCP save_article tags",
			setup: doc(""),
			save:  mcp(`{"name":"save_article","arguments":{"slug":"my-slug","title":"Other Title","content":"body","tags":["fresh"],"loaded_version":1}}`),
		},
		{
			name:  "MCP append_article for memory",
			setup: doc(memory),
			save:  mcp(`{"name":"append_article","arguments":{"slug":"my-slug","content":"more"}}`),
		},
		{
			name:  "MCP append_article for plan",
			setup: doc(plan + "status: draft\n"),
			save:  mcp(`{"name":"append_article","arguments":{"slug":"my-slug","content":"more"}}`),
		},
		{
			name:  "MCP save_article memory without a title change",
			setup: doc(memory),
			save:  mcp(`{"name":"save_article","arguments":{"slug":"my-slug","title":"Other Title","content":"body","loaded_version":1,"description":"clarified"}}`),
		},
		{
			name:  "MCP save_article plan without a title change",
			setup: doc(plan + "status: draft\n"),
			save:  mcp(`{"name":"save_article","arguments":{"slug":"my-slug","title":"Other Title","content":"body","loaded_version":1,"status":"implementing"}}`),
		},
		{
			name:  "MCP save_article skill without a title change",
			setup: doc(skill),
			save:  mcp(`{"name":"save_article","arguments":{"slug":"my-slug","title":"Other Title","content":"body","loaded_version":1,"status":"ready"}}`),
		},
		{
			name:  "OKF bundle re-import",
			setup: doc(""),
			save: func(t *testing.T, srv *Server) {
				bundle, err := srv.Storage.ExportOKFBundle()
				noErr(t, err)
				report, err := srv.Storage.ImportOKFBundle(bundle)
				noErr(t, err)
				if len(report.Warnings) != 0 {
					t.Fatalf("import warnings: %v", report.Warnings)
				}
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := newMCPServer(t)
			tc.setup(t, srv.Storage)
			before, err := srv.Storage.GetArticle("my-slug")
			if err != nil {
				t.Fatalf("the fixture should be a canonical document: %v", err)
			}

			tc.save(t, srv)

			art, err := srv.Storage.GetArticle("my-slug")
			if err != nil {
				t.Fatalf("the document should still be at my-slug: %v", err)
			}
			if art.Title != "Other Title" {
				t.Errorf("title = %q, want Other Title", art.Title)
			}
			if art.Version <= before.Version {
				t.Errorf("version = %d, want a new version after %d", art.Version, before.Version)
			}
			expectNotAt(t, srv.Storage, "articles/other-title.md")
			expectNotAt(t, srv.Storage, "history/other-title")
			if tc.check != nil {
				tc.check(t, art)
			}
		})
	}
}

// TestExplicitTitleEditStillRenames pins the other side: an edit that supplies a new title moves the
// document to the slug that title yields.
func TestExplicitTitleEditStillRenames(t *testing.T) {
	for _, tc := range []struct {
		name  string
		extra string
		save  func(t *testing.T, srv *Server)
	}{
		{"ApplyArticleEdit", "", func(t *testing.T, srv *Server) {
			if _, err := srv.Storage.ApplyArticleEdit("my-slug", ArticleEdit{Title: "Brand New Title", Content: "body", LoadedVersion: 1}); err != nil {
				t.Fatalf("ApplyArticleEdit failed: %v", err)
			}
		}},
		{"REST edit", "", func(t *testing.T, srv *Server) {
			if w := restCall(srv.HandleUpdateArticle, http.MethodPut, "my-slug", `{"title":"Brand New Title","content":"body","loaded_version":1}`); w.Code != http.StatusOK {
				t.Fatalf("status %d: %s", w.Code, w.Body.String())
			}
		}},
		{"MCP save_article", "", func(t *testing.T, srv *Server) {
			if resp := toolCall(t, srv, `{"name":"save_article","arguments":{"slug":"my-slug","title":"Brand New Title","content":"body","loaded_version":1}}`); resp.IsError {
				t.Fatalf("tool call failed: %s", resp.Content[0].Text)
			}
		}},
		{"MCP save_article plan with a title", "type: AI-Agent-Plan\nstatus: draft\n", func(t *testing.T, srv *Server) {
			if resp := toolCall(t, srv, `{"name":"save_article","arguments":{"slug":"my-slug","type":"AI-Agent-Plan","title":"Brand New Title","content":"body","loaded_version":1}}`); resp.IsError {
				t.Fatalf("tool call failed: %s", resp.Content[0].Text)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := newMCPServer(t)
			captureLog(t)
			writeTitleMismatchedDocument(t, srv.Storage, 1, tc.extra, "body\n")

			tc.save(t, srv)

			art, err := srv.Storage.GetArticle("brand-new-title")
			if err != nil {
				t.Fatalf("the edit should have renamed the document to brand-new-title: %v", err)
			}
			if art.Title != "Brand New Title" {
				t.Errorf("title = %q, want Brand New Title", art.Title)
			}
			expectNotAt(t, srv.Storage, "articles/my-slug.md")
		})
	}
}

// TestKeepSlugSaveNeedsAnExistingDocument pins that a save in place updates a document and never
// creates one: with nothing at the slug it is not found, and with a misplaced file there it is not
// found either.
func TestKeepSlugSaveNeedsAnExistingDocument(t *testing.T) {
	storage, err := NewStorage(t.TempDir())
	if err != nil {
		t.Fatalf("NewStorage failed: %v", err)
	}
	t.Cleanup(func() { _ = storage.Close() })
	captureLog(t)

	_, err = storage.SaveArticleWithOverrides("gone", "Gone", "body", "", "", "", "", nil, "", ArticleOverrides{KeepSlug: true})
	if !errors.Is(err, errArticleNotFound) {
		t.Errorf("a save in place with no document should be not found, got %v", err)
	}
	expectNotAt(t, storage, "articles/gone.md")
	expectNotAt(t, storage, "history/gone")

	unchanged := newCopiedArticleFixture(t, storage)
	_, err = storage.SaveArticleWithOverrides("bar-copy", "Bar", "HIJACKED", "", "", "", "", nil, "", ArticleOverrides{KeepSlug: true})
	expectNotFound(t, "SaveArticleWithOverrides in place", err)
	unchanged()

	// A create has no slug to keep and derives one from its title as always.
	art, err := storage.SaveArticleWithOverrides("", "Fresh Page", "body", "", "", "", "", nil, "", ArticleOverrides{KeepSlug: true})
	if err != nil || art.Slug != "fresh-page" {
		t.Errorf("a create should derive its slug from its title, got %+v, %v", art, err)
	}
}

// TestRevertAcrossTitleChangeKeepsTitleAndSlug is #156: reverting to a version with another title
// moved the article to that version's slug. It now restores the version's content and metadata as a
// new version under the current title and slug.
func TestRevertAcrossTitleChangeKeepsTitleAndSlug(t *testing.T) {
	storage, err := NewStorage(t.TempDir())
	if err != nil {
		t.Fatalf("NewStorage failed: %v", err)
	}
	t.Cleanup(func() { _ = storage.Close() })

	stale := time.Date(2030, 1, 2, 0, 0, 0, 0, time.UTC)
	verified := []OKFVerification{{By: "human:first", At: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}}
	if _, err := storage.SaveArticleWithOverrides("", "First Title", "# first body", "first description", "https://first.example", "first-resource", "v1", []string{"alpha"}, "",
		ArticleOverrides{StaleAfter: &stale, Verified: &verified}); err != nil {
		t.Fatalf("SaveArticleWithOverrides failed: %v", err)
	}
	empty := []OKFVerification{}
	renamed, err := storage.SaveArticleWithOverrides("first-title", "Second Title", "# second body", "second description", "https://second.example", "second-resource", "rename", []string{"beta"}, "",
		ArticleOverrides{StaleAfter: &time.Time{}, Verified: &empty})
	if err != nil || renamed.Slug != "second-title" {
		t.Fatalf("rename failed: %+v, %v", renamed, err)
	}

	reverted, err := storage.RevertArticle("second-title", 1)
	if err != nil {
		t.Fatalf("RevertArticle failed: %v", err)
	}

	got, err := storage.GetArticle("second-title")
	if err != nil {
		t.Fatalf("the article should still be at second-title: %v", err)
	}
	for _, art := range []*Article{reverted, got} {
		if art.Slug != "second-title" || art.Title != "Second Title" {
			t.Errorf("slug %q, title %q; want the current second-title, Second Title", art.Slug, art.Title)
		}
		if art.Content != "# first body" || art.Description != "first description" || art.Source != "https://first.example" || art.Resource != "first-resource" {
			t.Errorf("content %q, description %q, source %q, resource %q; want version 1's", art.Content, art.Description, art.Source, art.Resource)
		}
		if !reflect.DeepEqual(art.Tags, []string{"alpha"}) || !art.StaleAfter.Equal(stale) || len(art.Verified) != 1 {
			t.Errorf("tags %v, stale_after %v, verified %+v; want version 1's", art.Tags, art.StaleAfter, art.Verified)
		}
		if art.Version != 3 || art.EditSummary != "Reverted to version 1" {
			t.Errorf("version %d, summary %q; want a new version 3, \"Reverted to version 1\"", art.Version, art.EditSummary)
		}
	}
	if _, err := storage.GetArticle("first-title"); !errors.Is(err, errArticleNotFound) {
		t.Errorf("nothing should be at first-title, got %v", err)
	}
	expectNotAt(t, storage, "articles/first-title.md")
	expectNotAt(t, storage, "history/first-title")
	if history, err := storage.GetArticleHistory("second-title"); err != nil || len(history) != 3 {
		t.Errorf("second-title should have 3 versions, got %d (%v)", len(history), err)
	}
}

// TestRevertKeepsTypeAndLifecycleStatus pins what a revert leaves alone besides the title: the type,
// and a plan's lifecycle status and its clock. Undoing a body edit is not a state transition.
func TestRevertKeepsTypeAndLifecycleStatus(t *testing.T) {
	storage, err := NewStorage(t.TempDir())
	if err != nil {
		t.Fatalf("NewStorage failed: %v", err)
	}
	t.Cleanup(func() { _ = storage.Close() })
	captureLog(t)

	t.Run("plan status", func(t *testing.T) {
		draft, completed := "draft", "completed"
		if _, err := storage.SaveArticleWithStatus("", "Lifecycle Plan", "# draft body", "", "", "", "", nil, ContentTypePlan, &draft); err != nil {
			t.Fatalf("SaveArticleWithStatus failed: %v", err)
		}
		if _, err := storage.SaveArticleWithStatus("lifecycle-plan", "Lifecycle Plan", "# shipped body", "", "", "", "", nil, ContentTypePlan, &completed); err != nil {
			t.Fatalf("SaveArticleWithStatus failed: %v", err)
		}
		// A snapshot from before status was a field, carrying it as a tag.
		writeHistorySnapshot(t, storage, "lifecycle-plan", 1, "type: AI-Agent-Plan\ntitle: Lifecycle Plan\nslug: lifecycle-plan\nversion: 1\ntags:\n  - nexwiki\n  - wip\n", "# legacy body\n")
		before, err := storage.GetArticle("lifecycle-plan")
		if err != nil {
			t.Fatalf("GetArticle failed: %v", err)
		}

		reverted, err := storage.RevertArticle("lifecycle-plan", 1)
		if err != nil {
			t.Fatalf("RevertArticle failed: %v", err)
		}
		if reverted.Content != "# legacy body" || !reflect.DeepEqual(reverted.Tags, []string{"nexwiki"}) {
			t.Errorf("content %q, tags %v; want the snapshot's body and its tags less the status word", reverted.Content, reverted.Tags)
		}
		if reverted.Type != ContentTypePlan || reverted.Status != "completed" || !reverted.StatusChangedAt.Equal(before.StatusChangedAt) {
			t.Errorf("type %q, status %q, status_changed_at %v; want the current plan, completed, %v",
				reverted.Type, reverted.Status, reverted.StatusChangedAt, before.StatusChangedAt)
		}
	})

	t.Run("type", func(t *testing.T) {
		if _, err := storage.SaveArticle("", "Relabelled", "# wiki body", "", "", "", "", nil, ContentTypeWiki); err != nil {
			t.Fatalf("SaveArticle failed: %v", err)
		}
		if _, err := storage.SaveArticle("relabelled", "Relabelled", "# memory body", "", "", "", "", nil, ContentTypeMemory); err != nil {
			t.Fatalf("SaveArticle failed: %v", err)
		}
		reverted, err := storage.RevertArticle("relabelled", 1)
		if err != nil {
			t.Fatalf("RevertArticle failed: %v", err)
		}
		if reverted.Content != "# wiki body" || reverted.Type != ContentTypeMemory {
			t.Errorf("content %q, type %q; want version 1's body and the current type", reverted.Content, reverted.Type)
		}
	})
}

// TestHandleRevertArticleStatusCodes pins the revert handler's answers: not found for a missing
// article, a misplaced file, or a missing version, and a server error for anything else, with the
// data directory hidden.
func TestHandleRevertArticleStatusCodes(t *testing.T) {
	srv := newTestServer(t)
	captureLog(t)
	for _, title := range []string{"Revertable", "Removed By Hand"} {
		if _, err := srv.Storage.SaveArticle("", title, "# v1", "", "", "", "", nil, ""); err != nil {
			t.Fatalf("SaveArticle failed: %v", err)
		}
	}
	// History left behind by a file removed outside NexWiki must not let a revert recreate it.
	if err := os.Remove(filepath.Join(srv.Storage.ArticleDir, "removed-by-hand.md")); err != nil {
		t.Fatalf("Remove failed: %v", err)
	}
	unchanged := newCopiedArticleFixture(t, srv.Storage)

	for _, tc := range []struct {
		name, slug, body string
		want             int
	}{
		{"missing article", "no-such-article", `{"version":1}`, http.StatusNotFound},
		{"removed article with history", "removed-by-hand", `{"version":1}`, http.StatusNotFound},
		{"misplaced file", "bar-copy", `{"version":1}`, http.StatusNotFound},
		{"missing version", "revertable", `{"version":7}`, http.StatusNotFound},
	} {
		w := restCall(srv.HandleRevertArticle, http.MethodPost, tc.slug, tc.body)
		if w.Code != tc.want {
			t.Errorf("%s: status %d, want %d (%s)", tc.name, w.Code, tc.want, w.Body.String())
		}
	}

	// A snapshot that cannot be read is the server's problem, not a missing version.
	if err := os.WriteFile(filepath.Join(srv.Storage.HistoryDir, "revertable", "1.md.gz"), []byte("not gzip"), 0644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}
	w := restCall(srv.HandleRevertArticle, http.MethodPost, "revertable", `{"version":1}`)
	if w.Code != http.StatusInternalServerError {
		t.Errorf("corrupt snapshot: status %d, want 500 (%s)", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), srv.Storage.DataDir) {
		t.Errorf("the error should not name the data directory: %s", w.Body.String())
	}

	unchanged()
}

// TestCreateOverMisplacedFileIsAConflict pins how a create refused because a misplaced file holds its
// path reaches a client: 409 over REST, and the refusal's own message over MCP.
func TestCreateOverMisplacedFileIsAConflict(t *testing.T) {
	srv := newMCPServer(t)
	captureLog(t)
	unchanged := newCopiedArticleFixture(t, srv.Storage)
	const refusal = "a misplaced file already occupies articles/bar-copy.md; move or delete it first (wiki_health lists it)"

	for name, handle := range map[string]http.HandlerFunc{
		"HandleCreateArticle": srv.HandleCreateArticle,
		"HandleSaveArticle":   srv.HandleSaveArticle,
	} {
		w := restCall(handle, http.MethodPost, "", `{"title":"Bar Copy","content":"HIJACKED"}`)
		if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), refusal) {
			t.Errorf("%s: status %d, want 409 with the refusal (%s)", name, w.Code, w.Body.String())
		}
	}

	resp := toolCall(t, srv, `{"name":"save_article","arguments":{"title":"Bar Copy","content":"HIJACKED"}}`)
	if !resp.IsError || !strings.Contains(resp.Content[0].Text, refusal) {
		t.Errorf("save_article: expected the refusal, got %+v", resp)
	}

	unchanged()
}
