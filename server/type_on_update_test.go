package server

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// These tests pin the fix for "save_article accepts `type` on update and silently discards it".
//
// Every type assertion goes through search_wiki(type: …), never through a direct read. A direct
// read passed throughout the original incident — the document read perfectly well by slug — and the
// listing was what was wrong, so the listing is what has to be looked at.

// slugsListedAs returns the slugs search_wiki's index reports for a type filter.
func slugsListedAs(t *testing.T, srv *Server, docType string) map[string]bool {
	t.Helper()
	resp := toolCall(t, srv, fmt.Sprintf(`{"name":"search_wiki","arguments":{"type":%q,"limit":1000}}`, docType))
	if resp.IsError {
		t.Fatalf("search_wiki(type: %q) failed: %s", docType, resp.Content[0].Text)
	}
	var out SearchOutput
	decodeStructured(t, resp, &out)
	slugs := map[string]bool{}
	for _, d := range out.Documents {
		slugs[d.Slug] = true
	}
	return slugs
}

// assertListedAs fails unless slug appears under exactly the listing for wantType.
func assertListedAs(t *testing.T, srv *Server, slug, wantType string) {
	t.Helper()
	for _, typ := range []string{ContentTypeWiki, ContentTypeMemory, ContentTypePlan, ContentTypeSkill} {
		listed := slugsListedAs(t, srv, typ)[slug]
		if typ == wantType && !listed {
			t.Errorf("%s is missing from search_wiki(type: %q)", slug, typ)
		}
		if typ != wantType && listed {
			t.Errorf("%s is listed under search_wiki(type: %q), want only %q", slug, typ, wantType)
		}
	}
}

// saveCall builds a save_article call from an argument map.
func saveCall(t *testing.T, srv *Server, args map[string]interface{}) ToolResponse {
	t.Helper()
	raw, err := json.Marshal(map[string]interface{}{"name": "save_article", "arguments": args})
	if err != nil {
		t.Fatal(err)
	}
	return toolCall(t, srv, string(raw))
}

// seedTyped creates one document of docType through save_article and returns its slug.
func seedTyped(t *testing.T, srv *Server, title, docType string) string {
	t.Helper()
	args := map[string]interface{}{"title": title, "content": "# " + title + "\n\nBody.", "type": docType}
	switch docType {
	case ContentTypeMemory:
		args["memory_kind"] = "project"
		args["memory_type"] = "scope-a"
		args["description"] = "fixture"
		args["source"] = "test"
	case ContentTypePlan:
		args["status"] = "implementing"
		args["project_context"] = "proj"
	case ContentTypeSkill:
		args["status"] = "ready"
	}
	resp := saveCall(t, srv, args)
	if resp.IsError {
		t.Fatalf("seeding %s %q failed: %s", docType, title, resp.Content[0].Text)
	}
	return Slugify(title)
}

func TestSaveArticleTypeTransitionMatrix(t *testing.T) {
	types := []string{ContentTypeWiki, ContentTypeMemory, ContentTypePlan, ContentTypeSkill}

	// wantStatus is the status after a transition with no explicit status; wantErr marks the
	// transitions that must be refused because a real lifecycle state would be coerced.
	type outcome struct {
		wantStatus string
		wantErr    bool
	}
	expect := map[[2]string]outcome{
		{ContentTypeWiki, ContentTypeMemory}:  {wantStatus: ""},
		{ContentTypeWiki, ContentTypePlan}:    {wantStatus: "draft"},
		{ContentTypeWiki, ContentTypeSkill}:   {wantStatus: ""},
		{ContentTypeMemory, ContentTypeWiki}:  {wantStatus: ""},
		{ContentTypeMemory, ContentTypePlan}:  {wantStatus: "draft"},
		{ContentTypeMemory, ContentTypeSkill}: {wantStatus: ""},
		{ContentTypePlan, ContentTypeWiki}:    {wantStatus: ""},
		{ContentTypePlan, ContentTypeMemory}:  {wantStatus: ""},
		{ContentTypePlan, ContentTypeSkill}:   {wantErr: true}, // implementing is no skill status
		{ContentTypeSkill, ContentTypeWiki}:   {wantStatus: ""},
		{ContentTypeSkill, ContentTypeMemory}: {wantStatus: ""},
		{ContentTypeSkill, ContentTypePlan}:   {wantErr: true}, // ready is no plan status
	}

	for _, from := range types {
		for _, to := range types {
			if from == to {
				continue
			}
			t.Run(from+"->"+to, func(t *testing.T) {
				srv := newMCPServer(t)
				slug := seedTyped(t, srv, "Doc "+from+" to "+to, from)
				assertListedAs(t, srv, slug, from)

				before, _ := srv.Storage.GetArticle(slug)
				args := map[string]interface{}{
					"slug": slug, "title": before.Title, "content": "# changed", "type": to,
					"loaded_version": before.Version,
				}
				if to == ContentTypeMemory {
					// Becoming a memory passes the memory write gate, as creating one does.
					args["memory_kind"] = "reference"
					args["description"] = "d"
					args["source"] = "s"
				}
				resp := saveCall(t, srv, args)
				want := expect[[2]string{from, to}]
				if want.wantErr {
					if !resp.IsError {
						t.Fatalf("expected the change to be refused, got: %s", resp.Content[0].Text)
					}
					if !strings.Contains(resp.Content[0].Text, "status") {
						t.Errorf("the refusal must say to pass a status: %s", resp.Content[0].Text)
					}
					// Refused means refused: the document stays where it was.
					assertListedAs(t, srv, slug, from)
					return
				}
				if resp.IsError {
					t.Fatalf("type change failed: %s", resp.Content[0].Text)
				}
				if !strings.Contains(resp.Content[0].Text, "Type: "+from+" → "+to) {
					t.Errorf("the response must echo the transition, got: %s", resp.Content[0].Text)
				}
				assertListedAs(t, srv, slug, to)

				after, _ := srv.Storage.GetArticle(slug)
				if after.Status != want.wantStatus {
					t.Errorf("status = %q, want %q", after.Status, want.wantStatus)
				}
				if to != ContentTypeMemory {
					if after.MemoryKind != "" {
						t.Errorf("memory_kind %q survived leaving the memory class", after.MemoryKind)
					}
					for _, tag := range after.Tags {
						if strings.HasPrefix(tag, MemoryScopeTagPrefix) {
							t.Errorf("memory scope tag %q survived leaving the memory class", tag)
						}
					}
				}
				if after.Version != before.Version+1 {
					t.Errorf("version = %d, want %d: a type change is an ordinary new revision", after.Version, before.Version+1)
				}
			})
		}
	}
}

// TestSaveArticleTypeChangeAppliesClassificationArgs is the knock-on of the original bug: with the
// old type, memory_type, project_context, memory_kind, and status were all validated and applied
// against the class the document was leaving, so they vanished too.
func TestSaveArticleTypeChangeAppliesClassificationArgs(t *testing.T) {
	srv := newMCPServer(t)

	plan := seedTyped(t, srv, "Cold Start Handoff", ContentTypeWiki)
	resp := saveCall(t, srv, map[string]interface{}{
		"slug": plan, "title": "Cold Start Handoff", "content": "# handoff", "loaded_version": 1,
		"type": "AI-Agent-Plan", "project_context": "kimmydb", "status": "implementing",
	})
	if resp.IsError {
		t.Fatalf("relabel failed: %s", resp.Content[0].Text)
	}
	if !slugsListedAs(t, srv, "plans")[plan] {
		t.Fatal("the relabelled plan is missing from search_wiki(type: \"plans\")")
	}
	art, _ := srv.Storage.GetArticle(plan)
	if art.Status != "implementing" || !hasTag(art.Tags, "kimmydb") {
		t.Errorf("status %q tags %v: want implementing with the project tag", art.Status, art.Tags)
	}

	mem := seedTyped(t, srv, "Operator Prefers Tables", ContentTypeWiki)
	resp = saveCall(t, srv, map[string]interface{}{
		"slug": mem, "title": "Operator Prefers Tables", "content": "# pref", "loaded_version": 1,
		"type": "memories", "memory_type": "style", "memory_kind": "feedback",
		"description": "table preference", "source": "operator, in session",
	})
	if resp.IsError {
		t.Fatalf("relabel failed: %s", resp.Content[0].Text)
	}
	art, _ = srv.Storage.GetArticle(mem)
	if art.Type != ContentTypeMemory || art.MemoryKind != "feedback" || !hasTag(art.Tags, "memory-style") {
		t.Errorf("type %q kind %q tags %v: want a feedback memory scoped to style", art.Type, art.MemoryKind, art.Tags)
	}

	// An explicit status is validated against the new type: a skill status is no plan status.
	skill := seedTyped(t, srv, "Deploy Runbook", ContentTypeSkill)
	resp = saveCall(t, srv, map[string]interface{}{
		"slug": skill, "title": "Deploy Runbook", "content": "# runbook", "loaded_version": 1,
		"type": "AI-Agent-Plan", "status": "ready",
	})
	if !resp.IsError {
		t.Fatalf("a skill status must not be accepted onto a plan: %s", resp.Content[0].Text)
	}
	resp = saveCall(t, srv, map[string]interface{}{
		"slug": skill, "title": "Deploy Runbook", "content": "# runbook", "loaded_version": 1,
		"type": "AI-Agent-Plan", "status": "blocked",
	})
	if resp.IsError {
		t.Fatalf("an explicit valid status must make the change possible: %s", resp.Content[0].Text)
	}
	assertListedAs(t, srv, skill, ContentTypePlan)

	// A plan status the skill class shares carries over.
	draftPlan := seedTyped(t, srv, "Draft To Skill", ContentTypeWiki)
	saveCall(t, srv, map[string]interface{}{"slug": draftPlan, "title": "Draft To Skill", "content": "# x", "loaded_version": 1, "type": "AI-Agent-Plan"})
	resp = saveCall(t, srv, map[string]interface{}{"slug": draftPlan, "title": "Draft To Skill", "content": "# x", "loaded_version": 2, "type": "AI-Agent-Skill"})
	if resp.IsError {
		t.Fatalf("draft is a skill status too: %s", resp.Content[0].Text)
	}
	if art, _ := srv.Storage.GetArticle(draftPlan); art.Status != "draft" {
		t.Errorf("status = %q, want draft carried over", art.Status)
	}
}

func TestSaveArticleSameTypeIsIdempotent(t *testing.T) {
	srv := newMCPServer(t)
	slug := seedTyped(t, srv, "Stable Plan", ContentTypePlan)

	for i, typ := range []string{"AI-Agent-Plan", "plans", "ai-agent-plan", ""} {
		resp := saveCall(t, srv, map[string]interface{}{
			"slug": slug, "title": "Stable Plan", "content": fmt.Sprintf("# rev %d", i), "loaded_version": i + 1, "type": typ,
		})
		if resp.IsError {
			t.Fatalf("re-save with type %q failed: %s", typ, resp.Content[0].Text)
		}
		if !strings.Contains(resp.Content[0].Text, "Type: AI-Agent-Plan\n") || strings.Contains(resp.Content[0].Text, "→") {
			t.Errorf("a same-type save must echo the type and no transition: %s", resp.Content[0].Text)
		}
	}
	art, _ := srv.Storage.GetArticle(slug)
	if art.Status != "implementing" {
		t.Errorf("a same-type re-save must keep the status, got %q", art.Status)
	}
	assertListedAs(t, srv, slug, ContentTypePlan)
}

// TestUnknownTypeIsAnError is F3: an unrecognized type used to become Wiki silently, in
// save_article create and list_articles (now search_wiki's index) alike. A typo that behaves exactly like the bug is
// indistinguishable from it.
func TestUnknownTypeIsAnError(t *testing.T) {
	srv := newMCPServer(t)

	resp := saveCall(t, srv, map[string]interface{}{"title": "Typo Plan", "content": "# x", "type": "Not-A-Real-Type"})
	if !resp.IsError || !strings.Contains(resp.Content[0].Text, "AI-Agent-Plan") {
		t.Errorf("create with an unknown type must fail naming the valid types: %+v", resp)
	}
	if _, err := srv.Storage.GetArticle("typo-plan"); err == nil {
		t.Error("a refused create must write nothing")
	}

	slug := seedTyped(t, srv, "Existing Doc", ContentTypeWiki)
	resp = saveCall(t, srv, map[string]interface{}{"slug": slug, "title": "Existing Doc", "content": "# changed", "loaded_version": 1, "type": "memorys"})
	if !resp.IsError {
		t.Errorf("update with an unknown type must fail: %s", resp.Content[0].Text)
	}
	if art, _ := srv.Storage.GetArticle(slug); art.Version != 1 || art.Content == "# changed" {
		t.Error("a refused update must write nothing")
	}

	list := toolCall(t, srv, `{"name":"search_wiki","arguments":{"type":"memorys"}}`)
	if !list.IsError || !strings.Contains(list.Content[0].Text, "memorys") {
		t.Errorf("search_wiki with an unknown type must fail naming it, not list wiki articles: %s", list.Content[0].Text)
	}

	// Attested Computation is a real type; spelled canonically, it is accepted for listing.
	if list := toolCall(t, srv, `{"name":"search_wiki","arguments":{"type":"Attested Computation"}}`); list.IsError {
		t.Errorf("Attested Computation must be a valid list filter: %s", list.Content[0].Text)
	}
}

// TestRESTTypeOnCreateAndUpdate is F1 and F2 on the REST side: ArticleRequest had no type field,
// so the key was dropped by the decoder, and POST hardcoded Wiki.
func TestRESTTypeOnCreateAndUpdate(t *testing.T) {
	srv := newMCPServer(t)

	do := func(method, path, slug, body string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		if slug != "" {
			req.SetPathValue("slug", slug)
		}
		w := httptest.NewRecorder()
		if method == http.MethodPost {
			srv.HandleCreateArticle(w, req)
		} else {
			srv.HandleUpdateArticle(w, req)
		}
		return w
	}

	if w := do("POST", "/api/articles", "", `{"title":"Rest Plan","content":"x","type":"AI-Agent-Plan"}`); w.Code != http.StatusCreated {
		t.Fatalf("POST with a type: %d %s", w.Code, w.Body.String())
	}
	assertListedAs(t, srv, "rest-plan", ContentTypePlan)
	if art, _ := srv.Storage.GetArticle("rest-plan"); art.Status != "draft" {
		t.Errorf("a plan created over REST enters at draft, got %q", art.Status)
	}
	if w := do("POST", "/api/articles", "", `{"title":"Rest Default","content":"x"}`); w.Code != http.StatusCreated {
		t.Fatalf("POST without a type: %d", w.Code)
	}
	assertListedAs(t, srv, "rest-default", ContentTypeWiki)
	if w := do("POST", "/api/articles", "", `{"title":"Rest Typo","content":"x","type":"Plan-ish"}`); w.Code != http.StatusBadRequest {
		t.Errorf("POST with an unknown type: %d, want 400", w.Code)
	}
	if w := do("POST", "/api/articles", "", `{"title":"Rest Bad Status","content":"x","type":"AI-Agent-Plan","status":"ready"}`); w.Code != http.StatusBadRequest {
		t.Errorf("POST with a status the type rejects: %d, want 400", w.Code)
	}

	// PUT: the reproduction from the report — asked AI-Agent-Plan, got Wiki.
	if w := do("PUT", "/api/articles/rest-default", "rest-default", `{"title":"Rest Default","content":"x","type":"AI-Agent-Plan","status":"implementing","loaded_version":1}`); w.Code != http.StatusOK {
		t.Fatalf("PUT with a type: %d %s", w.Code, w.Body.String())
	}
	assertListedAs(t, srv, "rest-default", ContentTypePlan)
	if art, _ := srv.Storage.GetArticle("rest-default"); art.Status != "implementing" {
		t.Errorf("status = %q, want implementing", art.Status)
	}

	if w := do("PUT", "/api/articles/rest-default", "rest-default", `{"title":"Rest Default","content":"x","type":"Not-A-Real-Type","status":"blocked","loaded_version":2}`); w.Code != http.StatusBadRequest {
		t.Errorf("PUT with an unknown type: %d, want 400", w.Code)
	}
	if w := do("PUT", "/api/articles/rest-default", "rest-default", `{"title":"Rest Default","content":"x","type":"AI-Agent-Skill","loaded_version":2}`); w.Code != http.StatusBadRequest {
		t.Errorf("PUT moving an implementing plan to a skill with no status: %d, want 400", w.Code)
	}
	art, _ := srv.Storage.GetArticle("rest-default")
	if art.Version != 2 || art.Status != "implementing" {
		t.Errorf("a refused PUT must change nothing: version %d status %q", art.Version, art.Status)
	}
	assertListedAs(t, srv, "rest-default", ContentTypePlan)

	// Omitted type on PUT keeps the current one, so the web editor can never relabel by accident.
	if w := do("PUT", "/api/articles/rest-default", "rest-default", `{"title":"Rest Default","content":"y","loaded_version":2}`); w.Code != http.StatusOK {
		t.Fatalf("PUT without a type: %d", w.Code)
	}
	assertListedAs(t, srv, "rest-default", ContentTypePlan)
}

// TestOKFImportTypeOnExistingSlug is F4: the importer relabelled an existing document with
// normalizeType, so an unknown declared type turned an existing plan into a Wiki.
func TestOKFImportTypeOnExistingSlug(t *testing.T) {
	srv := newMCPServer(t)
	seedTyped(t, srv, "Imported Plan", ContentTypePlan)
	seedTyped(t, srv, "Imported Memory", ContentTypeMemory)
	seedTyped(t, srv, "Imported Wiki", ContentTypeWiki)

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	add := func(name, body string) {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte(body))
	}
	add("a.md", "---\ntitle: Imported Plan\ntype: BlogPost\n---\n# overwritten\n")
	add("b.md", "---\ntitle: Imported Memory\n---\n# updated, no type declared\n")
	add("c.md", "---\ntitle: Imported Wiki\ntype: AI-Agent-Skill\nstatus: ready\n---\n# relabelled\n")
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}

	report, err := srv.Storage.ImportOKFBundle(buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(report.Warnings, "\n")
	if !strings.Contains(joined, "BlogPost") {
		t.Errorf("an unknown declared type on an existing slug must be reported: %v", report.Warnings)
	}
	assertListedAs(t, srv, "imported-plan", ContentTypePlan)
	if art, _ := srv.Storage.GetArticle("imported-plan"); strings.Contains(art.Content, "overwritten") {
		t.Error("the refused entry must not have been written")
	}
	assertListedAs(t, srv, "imported-memory", ContentTypeMemory)
	assertListedAs(t, srv, "imported-wiki", ContentTypeSkill)
}
