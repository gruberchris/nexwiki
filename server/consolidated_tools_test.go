package server

import (
	"testing"
)

func TestConsolidatedToolsEndToEnd(t *testing.T) {
	srv := newMCPServer(t)

	// 1. save_article: create new Wiki article
	resp := toolCall(t, srv, `{"name":"save_article","arguments":{"title":"New Feature Design","content":"# Feature\n\nInitial content linking to [[Other Page]].","description":"Feature design doc","type":"Wiki","tags":["design"]}}`)
	if resp.IsError {
		t.Fatalf("save_article create failed: %s", resp.Content[0].Text)
	}

	// 2. read_article: read head version
	readResp := toolCall(t, srv, `{"name":"read_article","arguments":{"slug":"new-feature-design"}}`)
	if readResp.IsError {
		t.Fatalf("read_article failed: %s", readResp.Content[0].Text)
	}
	readOut, ok := readResp.StructuredContent.(ArticleOutput)
	if !ok {
		t.Fatalf("expected ArticleOutput, got %T", readResp.StructuredContent)
	}
	if readOut.Article.Version != 1 {
		t.Fatalf("expected version 1, got %d", readOut.Article.Version)
	}

	// 3. save_article: update existing Wiki article with loaded_version
	updateResp := toolCall(t, srv, `{"name":"save_article","arguments":{"slug":"new-feature-design","title":"New Feature Design","content":"# Feature\n\nUpdated content.","loaded_version":1,"edit_summary":"Updated body"}}`)
	if updateResp.IsError {
		t.Fatalf("save_article update failed: %s", updateResp.Content[0].Text)
	}

	// 4. save_article: optimistic locking conflict
	conflictResp := toolCall(t, srv, `{"name":"save_article","arguments":{"slug":"new-feature-design","title":"New Feature Design","content":"# Collision","loaded_version":1}}`)
	if !conflictResp.IsError {
		t.Fatalf("expected version conflict error, got success")
	}

	// 5. read_article: read historical version 1
	histResp := toolCall(t, srv, `{"name":"read_article","arguments":{"slug":"new-feature-design","version":1}}`)
	if histResp.IsError {
		t.Fatalf("read_article version 1 failed: %s", histResp.Content[0].Text)
	}
	histOut := histResp.StructuredContent.(ArticleOutput)
	if histOut.Article.Version != 1 {
		t.Fatalf("expected historical version 1, got %d", histOut.Article.Version)
	}

	// 6. append_article: append text
	appResp := toolCall(t, srv, `{"name":"append_article","arguments":{"slug":"new-feature-design","content":"## Notes\n\nAppended notes."}}`)
	if appResp.IsError {
		t.Fatalf("append_article failed: %s", appResp.Content[0].Text)
	}

	// 7. save_article: create Memory with memory_kind and memory_type
	memResp := toolCall(t, srv, `{"name":"save_article","arguments":{"title":"Docker Daemon Config","content":"# Memory\n\nConfigure daemon.json.","type":"AI-Agent-Memory","memory_kind":"reference","memory_type":"docker","description":"Docker config memory","source":"docker docs"}}`)
	if memResp.IsError {
		t.Fatalf("save_article memory failed: %s", memResp.Content[0].Text)
	}
	memArt, err := srv.Storage.GetArticle("docker-daemon-config")
	if err != nil {
		t.Fatalf("failed getting memory: %v", err)
	}
	if memArt.MemoryKind != "reference" {
		t.Fatalf("expected memory_kind 'reference', got %q", memArt.MemoryKind)
	}
	if !hasTag(memArt.Tags, "memory-docker") {
		t.Fatalf("expected memory-docker tag, got %v", memArt.Tags)
	}

	// 8. save_article: create Plan with status and project_context
	planResp := toolCall(t, srv, `{"name":"save_article","arguments":{"title":"Migration Plan","content":"# Plan\n\nSteps to migrate.","type":"AI-Agent-Plan","project_context":"nexwiki","status":"implementing"}}`)
	if planResp.IsError {
		t.Fatalf("save_article plan failed: %s", planResp.Content[0].Text)
	}
	planArt, err := srv.Storage.GetArticle("migration-plan")
	if err != nil {
		t.Fatalf("failed getting plan: %v", err)
	}
	if planArt.Status != "implementing" {
		t.Fatalf("expected status 'implementing', got %q", planArt.Status)
	}
	if !hasTag(planArt.Tags, "nexwiki") {
		t.Fatalf("expected nexwiki project tag, got %v", planArt.Tags)
	}

	// 9. list_articles: filter by type, status, tag
	listResp := toolCall(t, srv, `{"name":"list_articles","arguments":{"type":"plans"}}`)
	if listResp.IsError {
		t.Fatalf("list_articles failed: %s", listResp.Content[0].Text)
	}
	listOut := listResp.StructuredContent.(DocumentListOutput)
	if listOut.Count != 1 || listOut.Documents[0].Slug != "migration-plan" {
		t.Fatalf("expected 1 plan 'migration-plan', got %d docs", listOut.Count)
	}

	// 10. search_wiki: search with query, type, tag
	searchResp := toolCall(t, srv, `{"name":"search_wiki","arguments":{"query":"daemon","type":"memories"}}`)
	if searchResp.IsError {
		t.Fatalf("search_wiki failed: %s", searchResp.Content[0].Text)
	}
	searchOut := searchResp.StructuredContent.(SearchOutput)
	if searchOut.Count == 0 {
		t.Fatalf("expected search hits for 'daemon', got 0")
	}

	// 11. get_wiki_overview: overview with stats
	overResp := toolCall(t, srv, `{"name":"get_wiki_overview","arguments":{"since":"24h","include_stats":true}}`)
	if overResp.IsError {
		t.Fatalf("get_wiki_overview failed: %s", overResp.Content[0].Text)
	}
	overOut := overResp.StructuredContent.(OverviewOutput)
	if overOut.TotalArticles < 3 {
		t.Fatalf("expected at least 3 articles in overview, got %d", overOut.TotalArticles)
	}
	if overOut.Statistics == nil {
		t.Fatalf("expected statistics in overview")
	}

	// 12. get_backlinks
	_ = toolCall(t, srv, `{"name":"save_article","arguments":{"title":"Other Page","content":"# Other Page Body"}}`)
	blResp := toolCall(t, srv, `{"name":"get_backlinks","arguments":{"slug":"other-page"}}`)
	if blResp.IsError {
		t.Fatalf("get_backlinks failed: %s", blResp.Content[0].Text)
	}

	// 13. delete_article: delete article
	delResp := toolCall(t, srv, `{"name":"delete_article","arguments":{"slug":"docker-daemon-config"}}`)
	if delResp.IsError {
		t.Fatalf("delete_article failed: %s", delResp.Content[0].Text)
	}
	if _, err := srv.Storage.GetArticle("docker-daemon-config"); err == nil {
		t.Fatalf("expected docker-daemon-config to be deleted")
	}
}
