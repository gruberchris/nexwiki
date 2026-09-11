package server

import (
	"archive/zip"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAllMCPToolsComprehensive(t *testing.T) {
	srv := newMCPServer(t)

	// Helper for executing a tool call and returning ToolResponse or JSONRPCError
	call := func(toolName string, args map[string]interface{}) (ToolResponse, *JSONRPCError) {
		t.Helper()
		payload := map[string]interface{}{
			"name":      toolName,
			"arguments": args,
		}
		raw, err := json.Marshal(payload)
		if err != nil {
			t.Fatalf("marshal error for %s: %v", toolName, err)
		}
		res, rpcErr := srv.executeToolCall(raw, "test-agent")
		if rpcErr != nil {
			return ToolResponse{IsError: true}, rpcErr
		}
		resp, ok := res.(ToolResponse)
		if !ok {
			t.Fatalf("unexpected response type for %s: %T", toolName, res)
		}
		return resp, nil
	}

	mustCall := func(toolName string, args map[string]interface{}) ToolResponse {
		t.Helper()
		resp, rpcErr := call(toolName, args)
		if rpcErr != nil {
			t.Fatalf("protocol error for %s: %v", toolName, rpcErr)
		}
		if resp.IsError {
			t.Fatalf("tool error for %s: %s", toolName, resp.Content[0].Text)
		}
		return resp
	}

	// 1. Test get_wiki_statistics
	t.Run("get_wiki_statistics", func(t *testing.T) {
		resp := mustCall("get_wiki_statistics", map[string]interface{}{})
		stats, ok := resp.StructuredContent.(StatisticsOutput)
		if !ok {
			t.Fatalf("expected StatisticsOutput, got %T", resp.StructuredContent)
		}
		if stats.TotalArticles < 0 {
			t.Errorf("invalid TotalArticles: %d", stats.TotalArticles)
		}
	})

	// 2. Test get_status_tags
	t.Run("get_status_tags", func(t *testing.T) {
		resp := mustCall("get_status_tags", map[string]interface{}{})
		tags, ok := resp.StructuredContent.(StatusTagsOutput)
		if !ok {
			t.Fatalf("expected StatusTagsOutput, got %T", resp.StructuredContent)
		}
		if len(tags.PlanStatusTags) == 0 || len(tags.SkillStatusTags) == 0 {
			t.Errorf("empty status tags: %+v", tags)
		}
	})

	// 3. Test list_articles (initial)
	t.Run("list_articles_initial", func(t *testing.T) {
		resp := mustCall("list_articles", map[string]interface{}{})
		list, ok := resp.StructuredContent.(DocumentListOutput)
		if !ok {
			t.Fatalf("expected DocumentListOutput, got %T", resp.StructuredContent)
		}
		if list.Documents == nil {
			t.Errorf("documents slice should not be nil")
		}
	})

	// 4. Test create_wiki_article
	t.Run("create_wiki_article", func(t *testing.T) {
		// Bare verb rejection
		bad, _ := call("create_wiki_article", map[string]interface{}{
			"title":   "create",
			"content": "# Test",
		})
		if !bad.IsError {
			t.Errorf("expected error for bare tool verb title 'create'")
		}

		// Missing content (returns protocol error -32602)
		badContent, rpcErr := call("create_wiki_article", map[string]interface{}{
			"title": "Valid Title",
		})
		if rpcErr == nil && !badContent.IsError {
			t.Errorf("expected error for missing content")
		}

		// Valid creation
		resp := mustCall("create_wiki_article", map[string]interface{}{
			"title":        "Alpha Article",
			"content":      "# Alpha Article\n\nThis is alpha content.",
			"description":  "Alpha description",
			"source":       "https://example.com/alpha",
			"tags":         []string{"topic-a", "shared"},
			"edit_summary": "Initial alpha",
		})
		if !strings.Contains(resp.Content[0].Text, "created successfully") {
			t.Errorf("expected success text, got: %s", resp.Content[0].Text)
		}

		// Duplicate title rejection
		dup, _ := call("create_wiki_article", map[string]interface{}{
			"title":   "Alpha Article",
			"content": "# Duplicate Alpha",
		})
		if !dup.IsError {
			t.Errorf("expected error for duplicate title")
		}
	})

	// 5. Test read_article
	var alphaVersion int
	t.Run("read_article", func(t *testing.T) {
		resp := mustCall("read_article", map[string]interface{}{
			"slug": "alpha-article",
		})
		out, ok := resp.StructuredContent.(ArticleOutput)
		if !ok {
			t.Fatalf("expected ArticleOutput, got %T", resp.StructuredContent)
		}
		if out.Article.Slug != "alpha-article" {
			t.Errorf("expected slug 'alpha-article', got %q", out.Article.Slug)
		}
		if out.Article.Description != "Alpha description" {
			t.Errorf("expected description 'Alpha description', got %q", out.Article.Description)
		}
		if !strings.Contains(resp.Content[0].Text, "Alpha description") {
			t.Errorf("text content does not include description")
		}
		if !strings.Contains(resp.Content[0].Text, "This is alpha content.") {
			t.Errorf("text content does not include body")
		}
		alphaVersion = out.Article.Version
		if alphaVersion <= 0 {
			t.Errorf("expected positive version, got %d", alphaVersion)
		}

		// Read missing article
		missing, _ := call("read_article", map[string]interface{}{
			"slug": "does-not-exist",
		})
		if !missing.IsError {
			t.Errorf("expected error reading missing article")
		}
	})

	// 6. Test edit_wiki_article
	t.Run("edit_wiki_article", func(t *testing.T) {
		// Version conflict
		conflict, _ := call("edit_wiki_article", map[string]interface{}{
			"slug":           "alpha-article",
			"title":          "Alpha Article",
			"content":        "# Alpha Article\n\nConflict edit.",
			"loaded_version": alphaVersion + 99,
		})
		if !conflict.IsError {
			t.Errorf("expected version conflict error")
		}

		// Valid edit
		mustCall("edit_wiki_article", map[string]interface{}{
			"slug":           "alpha-article",
			"title":          "Alpha Article",
			"content":        "# Alpha Article\n\nUpdated alpha content.",
			"description":    "Updated alpha description",
			"loaded_version": alphaVersion,
			"edit_summary":   "Updated content",
		})

		// Read back
		readBack := mustCall("read_article", map[string]interface{}{"slug": "alpha-article"})
		out := readBack.StructuredContent.(ArticleOutput)
		if out.Article.Version != alphaVersion+1 {
			t.Errorf("expected version %d, got %d", alphaVersion+1, out.Article.Version)
		}
		alphaVersion = out.Article.Version
	})

	// 7. Test update_article_tags
	t.Run("update_article_tags", func(t *testing.T) {
		mustCall("update_article_tags", map[string]interface{}{
			"slug":           "alpha-article",
			"tags":           []string{"new-tag", "topic-a"},
			"loaded_version": alphaVersion,
			"edit_summary":   "Updated tags",
		})
		readBack := mustCall("read_article", map[string]interface{}{"slug": "alpha-article"})
		out := readBack.StructuredContent.(ArticleOutput)
		if out.Article.Version != alphaVersion+1 {
			t.Errorf("expected version %d, got %d", alphaVersion+1, out.Article.Version)
		}
		alphaVersion = out.Article.Version
	})

	// 8. Test get_article_history
	t.Run("get_article_history", func(t *testing.T) {
		resp := mustCall("get_article_history", map[string]interface{}{
			"slug": "alpha-article",
		})
		hist, ok := resp.StructuredContent.(HistoryOutput)
		if !ok {
			t.Fatalf("expected HistoryOutput, got %T", resp.StructuredContent)
		}
		if hist.Count < 3 {
			t.Errorf("expected at least 3 versions in history, got %d", hist.Count)
		}
	})

	// 9. Test revert_article_version
	t.Run("revert_article_version", func(t *testing.T) {
		mustCall("revert_article_version", map[string]interface{}{
			"slug":    "alpha-article",
			"version": 1,
		})
		readBack := mustCall("read_article", map[string]interface{}{"slug": "alpha-article"})
		out := readBack.StructuredContent.(ArticleOutput)
		if !strings.Contains(out.Article.Content, "This is alpha content.") {
			t.Errorf("revert did not restore version 1 content: %s", out.Article.Content)
		}
		alphaVersion = out.Article.Version
	})

	// 10. Test get_backlinks
	t.Run("get_backlinks", func(t *testing.T) {
		// Create Beta Article linking to Alpha Article via [[Alpha Article]]
		mustCall("create_wiki_article", map[string]interface{}{
			"title":        "Beta Article",
			"content":      "# Beta\n\nLinks to [[Alpha Article]] and [direct](/articles/alpha-article).",
			"description":  "Beta page",
			"edit_summary": "Initial beta",
		})

		resp := mustCall("get_backlinks", map[string]interface{}{
			"slug": "alpha-article",
		})
		bl, ok := resp.StructuredContent.(BacklinksOutput)
		if !ok {
			t.Fatalf("expected BacklinksOutput, got %T", resp.StructuredContent)
		}
		if bl.Count == 0 {
			t.Errorf("expected backlinks from Beta Article")
		}
		foundBeta := false
		for _, b := range bl.Backlinks {
			if b.Slug == "beta-article" {
				foundBeta = true
				break
			}
		}
		if !foundBeta {
			t.Errorf("beta-article not found in backlinks: %+v", bl.Backlinks)
		}
	})

	// 11. Test search_wiki
	t.Run("search_wiki", func(t *testing.T) {
		resp := mustCall("search_wiki", map[string]interface{}{
			"query": "alpha",
		})
		out, ok := resp.StructuredContent.(SearchOutput)
		if !ok {
			t.Fatalf("expected SearchOutput, got %T", resp.StructuredContent)
		}
		if out.Count == 0 {
			t.Errorf("search for 'alpha' returned no hits")
		}

		// Invalid type error
		badType, _ := call("search_wiki", map[string]interface{}{
			"query": "alpha",
			"type":  []string{"invalid_type"},
		})
		if !badType.IsError {
			t.Errorf("expected error for invalid search type")
		}

		// Invalid memory_kind error
		badKind, _ := call("search_wiki", map[string]interface{}{
			"query":       "alpha",
			"memory_kind": "not_a_kind",
		})
		if !badKind.IsError {
			t.Errorf("expected error for invalid search memory_kind")
		}
	})

	// 12. Test create_agent_plan
	var planSlug string
	var planVersion int
	t.Run("create_agent_plan", func(t *testing.T) {
		// Missing project_context (returns protocol error -32602)
		bad, rpcErr := call("create_agent_plan", map[string]interface{}{
			"title":   "My Plan",
			"content": "# Plan",
		})
		if rpcErr == nil && !bad.IsError {
			t.Errorf("expected error for missing project_context")
		}

		// Valid creation
		mustCall("create_agent_plan", map[string]interface{}{
			"title":           "Migration Plan",
			"content":         "# Migration Plan\n\nSteps to migrate.",
			"project_context": "project-x",
			"description":     "Migration roadmap",
			"status":          "draft",
			"tags":            []string{"migration", "infra"},
			"edit_summary":    "Initial plan",
		})

		readBack := mustCall("read_article", map[string]interface{}{"slug": "migration-plan"})
		out := readBack.StructuredContent.(ArticleOutput)
		planSlug = out.Article.Slug
		planVersion = out.Article.Version
		if out.Article.Type != ContentTypePlan {
			t.Errorf("expected ContentTypePlan, got %q", out.Article.Type)
		}
		if out.Article.Status != "draft" {
			t.Errorf("expected status 'draft', got %q", out.Article.Status)
		}
	})

	// 13. Test list_agent_plans
	t.Run("list_agent_plans", func(t *testing.T) {
		resp := mustCall("list_agent_plans", map[string]interface{}{
			"project_context": "project-x",
		})
		out, ok := resp.StructuredContent.(DocumentListOutput)
		if !ok {
			t.Fatalf("expected DocumentListOutput, got %T", resp.StructuredContent)
		}
		if out.Count == 0 {
			t.Errorf("expected at least 1 plan")
		}
	})

	// 14. Test append_agent_plan
	t.Run("append_agent_plan", func(t *testing.T) {
		mustCall("append_agent_plan", map[string]interface{}{
			"slug":              planSlug,
			"content_to_append": "## Progress Update\n\nPhase 1 completed.",
			"edit_summary":      "Appended phase 1 progress",
		})

		readBack := mustCall("read_article", map[string]interface{}{"slug": planSlug})
		out := readBack.StructuredContent.(ArticleOutput)
		if !strings.Contains(out.Article.Content, "Phase 1 completed.") {
			t.Errorf("appended content not found: %s", out.Article.Content)
		}
		planVersion = out.Article.Version
	})

	// 15. Test edit_agent_plan
	t.Run("edit_agent_plan", func(t *testing.T) {
		newContent := "# Migration Plan\n\nSteps revised.\n\n## Progress Update\n\nPhase 1 completed."
		mustCall("edit_agent_plan", map[string]interface{}{
			"slug":           planSlug,
			"content":        newContent,
			"status":         "implementing",
			"loaded_version": planVersion,
			"edit_summary":   "Moved to implementing",
		})

		readBack := mustCall("read_article", map[string]interface{}{"slug": planSlug})
		out := readBack.StructuredContent.(ArticleOutput)
		if out.Article.Status != "implementing" {
			t.Errorf("expected status 'implementing', got %q", out.Article.Status)
		}
		planVersion = out.Article.Version
	})

	// 16. Test create_agent_skill
	var skillSlug string
	var skillVersion int
	t.Run("create_agent_skill", func(t *testing.T) {
		mustCall("create_agent_skill", map[string]interface{}{
			"title":        "Deploy Procedure",
			"content":      "# Deploy Procedure\n\n1. Run tests\n2. Ship.",
			"description":  "Safe deploy instructions",
			"source":       "ops handbook",
			"status":       "ready",
			"tags":         []string{"ops", "deploy"},
			"edit_summary": "Initial deploy skill",
		})

		readBack := mustCall("read_article", map[string]interface{}{"slug": "deploy-procedure"})
		out := readBack.StructuredContent.(ArticleOutput)
		skillSlug = out.Article.Slug
		skillVersion = out.Article.Version
		if out.Article.Type != ContentTypeSkill {
			t.Errorf("expected ContentTypeSkill, got %q", out.Article.Type)
		}
	})

	// 17. Test list_agent_skills
	t.Run("list_agent_skills", func(t *testing.T) {
		resp := mustCall("list_agent_skills", map[string]interface{}{})
		out, ok := resp.StructuredContent.(DocumentListOutput)
		if !ok {
			t.Fatalf("expected DocumentListOutput, got %T", resp.StructuredContent)
		}
		if out.Count == 0 {
			t.Errorf("expected skills in listing")
		}
	})

	// 18. Test edit_agent_skill
	t.Run("edit_agent_skill", func(t *testing.T) {
		mustCall("edit_agent_skill", map[string]interface{}{
			"slug":           skillSlug,
			"description":    "Updated deploy instructions",
			"loaded_version": skillVersion,
			"edit_summary":   "Refined description",
		})
		readBack := mustCall("read_article", map[string]interface{}{"slug": skillSlug})
		out := readBack.StructuredContent.(ArticleOutput)
		if out.Article.Description != "Updated deploy instructions" {
			t.Errorf("description update failed: %s", out.Article.Description)
		}
		skillVersion = out.Article.Version
	})

	// 19. Test create_agent_memory
	var memSlug string
	var memVersion int
	t.Run("create_agent_memory", func(t *testing.T) {
		// Missing memory_kind (protocol or tool error)
		badKind, rpcErr := call("create_agent_memory", map[string]interface{}{
			"title":       "DB Memory",
			"content":     "Fact",
			"description": "desc",
			"source":      "src",
		})
		if rpcErr == nil && !badKind.IsError {
			t.Errorf("expected error for missing memory_kind")
		}

		// Missing description
		badDesc, rpcErr := call("create_agent_memory", map[string]interface{}{
			"title":       "DB Memory",
			"content":     "Fact",
			"memory_kind": "project",
			"source":      "src",
		})
		if rpcErr == nil && !badDesc.IsError {
			t.Errorf("expected error for missing description")
		}

		// Missing source
		badSrc, rpcErr := call("create_agent_memory", map[string]interface{}{
			"title":       "DB Memory",
			"content":     "Fact",
			"memory_kind": "project",
			"description": "desc",
		})
		if rpcErr == nil && !badSrc.IsError {
			t.Errorf("expected error for missing source")
		}

		// Valid creation
		mustCall("create_agent_memory", map[string]interface{}{
			"title":        "Database Port Decision",
			"content":      "# Database Port\n\nPostgres runs on 5433 to avoid local collision.",
			"memory_kind":  "project",
			"memory_type":  "postgres",
			"description":  "Postgres port setting",
			"source":       "incident note 2026-07",
			"edit_summary": "Initial memory",
		})

		readBack := mustCall("read_article", map[string]interface{}{"slug": "database-port-decision"})
		out := readBack.StructuredContent.(ArticleOutput)
		memSlug = out.Article.Slug
		memVersion = out.Article.Version
		if out.Article.Type != ContentTypeMemory {
			t.Errorf("expected ContentTypeMemory, got %q", out.Article.Type)
		}
		if out.Article.MemoryKind != "project" {
			t.Errorf("expected MemoryKind 'project', got %q", out.Article.MemoryKind)
		}
		if !hasTagFold(out.Article.Tags, "memory-postgres") {
			t.Errorf("expected tag 'memory-postgres', got %v", out.Article.Tags)
		}
	})

	// 20. Test list_agent_memories
	t.Run("list_agent_memories", func(t *testing.T) {
		resp := mustCall("list_agent_memories", map[string]interface{}{
			"memory_kind": "project",
			"memory_type": "postgres",
		})
		out, ok := resp.StructuredContent.(DocumentListOutput)
		if !ok {
			t.Fatalf("expected DocumentListOutput, got %T", resp.StructuredContent)
		}
		if out.Count == 0 {
			t.Errorf("expected matching memory")
		}

		// Invalid kind error
		bad, _ := call("list_agent_memories", map[string]interface{}{
			"memory_kind": "bogus",
		})
		if !bad.IsError {
			t.Errorf("expected error for invalid memory_kind")
		}
	})

	// 21. Test append_agent_memory
	t.Run("append_agent_memory", func(t *testing.T) {
		mustCall("append_agent_memory", map[string]interface{}{
			"slug":              memSlug,
			"content_to_append": "Verified again on 2026-09.",
			"edit_summary":      "Re-verified port",
		})
		readBack := mustCall("read_article", map[string]interface{}{"slug": memSlug})
		out := readBack.StructuredContent.(ArticleOutput)
		if !strings.Contains(out.Article.Content, "Re-verified") && !strings.Contains(out.Article.Content, "Verified again") {
			t.Errorf("appended content not found: %s", out.Article.Content)
		}
		memVersion = out.Article.Version
	})

	// 22. Test edit_agent_memory
	t.Run("edit_agent_memory", func(t *testing.T) {
		// Missing change_intent when content is passed
		badIntent, _ := call("edit_agent_memory", map[string]interface{}{
			"slug":           memSlug,
			"content":        "New content",
			"loaded_version": memVersion,
		})
		if !badIntent.IsError {
			t.Errorf("expected error when content is passed without change_intent")
		}

		// change_intent 'correct' requires edit_summary
		badSummary, _ := call("edit_agent_memory", map[string]interface{}{
			"slug":           memSlug,
			"content":        "New content",
			"change_intent":  "correct",
			"loaded_version": memVersion,
		})
		if !badSummary.IsError {
			t.Errorf("expected error when change_intent 'correct' has empty edit_summary")
		}

		// Valid refine
		mustCall("edit_agent_memory", map[string]interface{}{
			"slug":           memSlug,
			"content":        "# Database Port\n\nPostgres runs on 5433 to avoid local host collision.",
			"change_intent":  "refine",
			"loaded_version": memVersion,
			"edit_summary":   "Refined wording",
		})
		readBack := mustCall("read_article", map[string]interface{}{"slug": memSlug})
		out := readBack.StructuredContent.(ArticleOutput)
		memVersion = out.Article.Version

		// Contradict intent
		mustCall("edit_agent_memory", map[string]interface{}{
			"slug":           memSlug,
			"content":        "Observer claims Postgres runs on 5432.",
			"change_intent":  "contradict",
			"loaded_version": memVersion,
			"edit_summary":   "Conflicting observation",
		})
		readContradict := mustCall("read_article", map[string]interface{}{"slug": memSlug})
		outContradict := readContradict.StructuredContent.(ArticleOutput)
		if !hasTagFold(outContradict.Article.Tags, "contested") {
			t.Errorf("expected 'contested' tag on contradictory edit: %v", outContradict.Article.Tags)
		}
		memVersion = outContradict.Article.Version
	})

	// 23. Test get_context_overview
	t.Run("get_context_overview", func(t *testing.T) {
		resp := mustCall("get_context_overview", map[string]interface{}{})
		txt := resp.Content[0].Text
		for _, sec := range []string{"Wiki Articles", "Agent Memories", "Agent Plans", "Agent Skills"} {
			if !strings.Contains(txt, sec) {
				t.Errorf("context overview missing section %s", sec)
			}
		}

		// Filter
		mustCall("get_context_overview", map[string]interface{}{
			"type": "memories",
		})

		// Invalid filter
		bad, _ := call("get_context_overview", map[string]interface{}{
			"type": "invalid_filter",
		})
		if !bad.IsError {
			t.Errorf("expected error for invalid type filter")
		}
	})

	// 24. Test get_recent_activity
	t.Run("get_recent_activity", func(t *testing.T) {
		resp := mustCall("get_recent_activity", map[string]interface{}{
			"limit": 10,
		})
		act, ok := resp.StructuredContent.(ActivityOutput)
		if !ok {
			t.Fatalf("expected ActivityOutput, got %T", resp.StructuredContent)
		}
		if act.Count == 0 {
			t.Errorf("expected logged activities, got 0")
		}
	})

	// 25. Test wiki_health
	t.Run("wiki_health", func(t *testing.T) {
		resp := mustCall("wiki_health", map[string]interface{}{})
		health, ok := resp.StructuredContent.(HealthOutput)
		if !ok {
			t.Fatalf("expected HealthOutput, got %T", resp.StructuredContent)
		}
		if health.TotalDocuments == 0 {
			t.Errorf("expected non-zero TotalDocuments")
		}
		// Since we added a contested memory earlier, contested_memory_count should be at least 1
		if health.ContestedCount == 0 {
			t.Errorf("expected at least 1 contested memory from contradict edit")
		}
	})

	// 26. Test export_okf_bundle
	var exportedPath string
	t.Run("export_okf_bundle", func(t *testing.T) {
		resp := mustCall("export_okf_bundle", map[string]interface{}{})
		lines := strings.Split(resp.Content[0].Text, "\n")
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if strings.HasSuffix(line, ".zip") {
				exportedPath = line
				break
			}
		}
		if exportedPath == "" {
			t.Fatalf("could not extract exported zip path from response: %s", resp.Content[0].Text)
		}
		// Verify file exists
		if _, err := os.Stat(exportedPath); err != nil {
			t.Fatalf("exported file %q does not exist: %v", exportedPath, err)
		}
		// Verify zip is readable
		zr, err := zip.OpenReader(exportedPath)
		if err != nil {
			t.Fatalf("exported zip is not valid: %v", err)
		}
		_ = zr.Close()
	})

	// 27. Test import_okf_bundle
	t.Run("import_okf_bundle", func(t *testing.T) {
		// Import using the exact path exported by export_okf_bundle
		resp, rpcErr := call("import_okf_bundle", map[string]interface{}{
			"path": exportedPath,
		})
		if rpcErr != nil {
			t.Fatalf("protocol error on import_okf_bundle: %v", rpcErr)
		}
		if resp.IsError {
			t.Fatalf("import_okf_bundle failed using path %q returned by export_okf_bundle: %s", exportedPath, resp.Content[0].Text)
		}
		if !strings.Contains(resp.Content[0].Text, "OKF import complete") {
			t.Errorf("expected import confirmation message, got: %s", resp.Content[0].Text)
		}

		// Also test import with filename relative to DataDir
		filenameOnly := filepath.Base(exportedPath)
		respRel, rpcErrRel := call("import_okf_bundle", map[string]interface{}{
			"path": filenameOnly,
		})
		if rpcErrRel != nil {
			t.Fatalf("protocol error on import_okf_bundle relative: %v", rpcErrRel)
		}
		if respRel.IsError {
			t.Fatalf("import_okf_bundle failed with relative filename %q: %s", filenameOnly, respRel.Content[0].Text)
		}
	})

	// 28. Test delete_wiki_article
	t.Run("delete_wiki_article", func(t *testing.T) {
		// Attempt to delete agent memory using delete_wiki_article -> must refuse
		refuseMem, _ := call("delete_wiki_article", map[string]interface{}{
			"slug": memSlug,
		})
		if !refuseMem.IsError || !strings.Contains(refuseMem.Content[0].Text, "delete_agent_memory") {
			t.Errorf("expected refusal to delete memory via delete_wiki_article: %s", refuseMem.Content[0].Text)
		}

		// Delete beta article
		mustCall("delete_wiki_article", map[string]interface{}{
			"slug": "beta-article",
		})

		// Read back should fail
		readBack, _ := call("read_article", map[string]interface{}{"slug": "beta-article"})
		if !readBack.IsError {
			t.Errorf("beta-article should be deleted")
		}
	})

	// 29. Test delete_agent_memory
	t.Run("delete_agent_memory", func(t *testing.T) {
		// Attempt to delete standard article using delete_agent_memory -> must refuse
		refuseArt, _ := call("delete_agent_memory", map[string]interface{}{
			"slug": "alpha-article",
		})
		if !refuseArt.IsError || !strings.Contains(refuseArt.Content[0].Text, "delete_wiki_article") {
			t.Errorf("expected refusal to delete wiki article via delete_agent_memory: %s", refuseArt.Content[0].Text)
		}

		// Delete memory
		mustCall("delete_agent_memory", map[string]interface{}{
			"slug": memSlug,
		})

		// Read back should fail
		readBack, _ := call("read_article", map[string]interface{}{"slug": memSlug})
		if !readBack.IsError {
			t.Errorf("memory should be deleted")
		}
	})
}

func TestOKFExportAndImportWithRelativeDataDir(t *testing.T) {
	// Create a temp dir and use a relative path to it
	tmp := t.TempDir()
	origWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("os.Getwd failed: %v", err)
	}
	if err := os.Chdir(tmp); err != nil {
		t.Fatalf("os.Chdir failed: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(origWd) })

	relDataDir := "my-wiki-data"
	storage, err := NewStorage(relDataDir)
	if err != nil {
		t.Fatalf("NewStorage failed with relative data dir %q: %v", relDataDir, err)
	}
	t.Cleanup(func() { _ = storage.Close() })

	srv := NewServer(storage, "Relative Wiki", "light", false, NewEventBus(), "1.0.0", "")

	// 1. Export bundle
	exportRaw, err := json.Marshal(map[string]interface{}{
		"name":      "export_okf_bundle",
		"arguments": map[string]interface{}{},
	})
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}
	exportRes, rpcErr := srv.executeToolCall(exportRaw, "test-agent")
	if rpcErr != nil {
		t.Fatalf("export protocol error: %v", rpcErr)
	}
	exportResp := exportRes.(ToolResponse)
	if exportResp.IsError {
		t.Fatalf("export error: %s", exportResp.Content[0].Text)
	}

	var exportedPath string
	for _, line := range strings.Split(exportResp.Content[0].Text, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasSuffix(line, ".zip") {
			exportedPath = line
			break
		}
	}
	if exportedPath == "" {
		t.Fatalf("could not find exported path in: %s", exportResp.Content[0].Text)
	}

	t.Logf("Exported path returned by export_okf_bundle: %q", exportedPath)

	// 2. Import using the path returned by export_okf_bundle
	importRaw, err := json.Marshal(map[string]interface{}{
		"name": "import_okf_bundle",
		"arguments": map[string]interface{}{
			"path": exportedPath,
		},
	})
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}
	importRes, rpcErr := srv.executeToolCall(importRaw, "test-agent")
	if rpcErr != nil {
		t.Fatalf("import protocol error: %v", rpcErr)
	}
	importResp := importRes.(ToolResponse)
	if importResp.IsError {
		t.Fatalf("import_okf_bundle failed on path returned by export_okf_bundle: %s", importResp.Content[0].Text)
	}
}
