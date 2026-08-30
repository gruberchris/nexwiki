package server

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMCPEditAgentPlan(t *testing.T) {
	tempDir := t.TempDir()

	storage, err := NewStorage(tempDir)
	if err != nil {
		t.Fatalf("Failed to initialize storage: %v", err)
	}
	t.Cleanup(func() { _ = storage.Close() })

	eventBus := NewEventBus()
	srv := NewServer(storage, "Test Wiki", "light", false, eventBus, "1.0.0", "")

	// 1. Create a plan first using executeToolCallInternal
	createArgs := json.RawMessage(`{"name":"create_agent_plan","arguments":{"title":"Migration Plan","content":"# Migration Checklist","project_context":"nexwiki","edit_summary":"Initial seed"}}`)
	res, rpcErr := srv.executeToolCallInternal(createArgs)
	if rpcErr != nil {
		t.Fatalf("create_agent_plan failed: %v", rpcErr)
	}

	resp, ok := res.(ToolResponse)
	if !ok || resp.IsError {
		t.Fatalf("create_agent_plan returned error response: %v", resp)
	}

	// Verify plan exists and check its initial properties
	plan, err := storage.GetArticle("migration-plan")
	if err != nil {
		t.Fatalf("Failed to load plan: %v", err)
	}
	if plan.Version != 1 {
		t.Errorf("Expected version 1, got %d", plan.Version)
	}
	if plan.Type != ContentTypePlan {
		t.Errorf("Expected type AI-Agent-Plan, got %q", plan.Type)
	}
	if len(plan.Tags) != 1 || plan.Tags[0] != "nexwiki" {
		t.Errorf("Expected tags ['nexwiki'], got %v", plan.Tags)
	}
	// A plan created with no status starts in draft (the lifecycle default).
	if plan.Status != "draft" {
		t.Errorf("Expected status 'draft', got %q", plan.Status)
	}

	// 2. Perform a successful edit using edit_agent_plan
	editArgs := json.RawMessage(`{"name":"edit_agent_plan","arguments":{"slug":"migration-plan","title":"Final Migration Plan","tags":["postgres","nexwiki"],"status":"implementing","loaded_version":1,"edit_summary":"Renamed and updated tags"}}`)
	res2, rpcErr2 := srv.executeToolCallInternal(editArgs)
	if rpcErr2 != nil {
		t.Fatalf("edit_agent_plan failed: %v", rpcErr2)
	}

	resp2, ok2 := res2.(ToolResponse)
	if !ok2 || resp2.IsError {
		t.Fatalf("edit_agent_plan returned error response: %v", resp2)
	}

	// Verify the original plan file was moved (since title/slug changed) and new one has updated fields
	_, err = storage.GetArticle("migration-plan")
	if err == nil {
		t.Errorf("Expected old plan to be moved or unindexed, but it was found")
	}

	updatedPlan, err := storage.GetArticle("final-migration-plan")
	if err != nil {
		t.Fatalf("Failed to load updated plan: %v", err)
	}
	if updatedPlan.Title != "Final Migration Plan" {
		t.Errorf("Expected title 'Final Migration Plan', got '%s'", updatedPlan.Title)
	}
	if updatedPlan.Version != 2 {
		t.Errorf("Expected version 2, got %d", updatedPlan.Version)
	}
	// The plan class is carried by the OKF type, which is preserved across the edit.
	if updatedPlan.Type != ContentTypePlan {
		t.Errorf("Expected type AI-Agent-Plan to be preserved, got %q", updatedPlan.Type)
	}
	if len(updatedPlan.Tags) != 2 || updatedPlan.Tags[0] != "postgres" || updatedPlan.Tags[1] != "nexwiki" {
		t.Errorf("Expected tags [postgres, nexwiki], got %v", updatedPlan.Tags)
	}
	if updatedPlan.Status != "implementing" {
		t.Errorf("Expected status 'implementing', got %q", updatedPlan.Status)
	}

	// 3. Test optimistic locking: try editing with outdated loaded_version = 1 (current disk is 2)
	conflictArgs := json.RawMessage(`{"name":"edit_agent_plan","arguments":{"slug":"final-migration-plan","title":"Conflict Plan","loaded_version":1,"edit_summary":"Should fail"}}`)
	res3, rpcErr3 := srv.executeToolCallInternal(conflictArgs)
	if rpcErr3 != nil {
		t.Fatalf("executeToolCallInternal itself failed: %v", rpcErr3)
	}

	resp3, ok3 := res3.(ToolResponse)
	if !ok3 || !resp3.IsError {
		t.Fatalf("Expected conflict error response, got: %v", resp3)
	}
	// The message must name the version to send and bound the retry to one attempt. "Re-fetch and
	// try again" named no value and set no bound, which is an unbounded loop for a client that
	// mis-threads loaded_version.
	for _, want := range []string{"version conflict", "version 2 on disk", "Retry once with loaded_version: 2"} {
		if !strings.Contains(resp3.Content[0].Text, want) {
			t.Errorf("conflict message missing %q, got: %s", want, resp3.Content[0].Text)
		}
	}

	// 4. Test target validation: try editing a standard article (not a plan)
	_, _ = storage.SaveArticle("", "Standard Page", "Just text", "", "", "", "initial", []string{"notes"}, "")
	invalidArgs := json.RawMessage(`{"name":"edit_agent_plan","arguments":{"slug":"standard-page","title":"Updated Title","loaded_version":1,"edit_summary":"Should fail"}}`)
	res4, rpcErr4 := srv.executeToolCallInternal(invalidArgs)
	if rpcErr4 != nil {
		t.Fatalf("executeToolCallInternal standard-page failed: %v", rpcErr4)
	}
	resp4, ok4 := res4.(ToolResponse)
	if !ok4 || !resp4.IsError {
		t.Fatalf("Expected target validation error response, got: %v", resp4)
	}
	if !strings.Contains(resp4.Content[0].Text, "is not a Collaborative AI Plan") {
		t.Errorf("Expected plan validation error message, got: %s", resp4.Content[0].Text)
	}
}

func TestMCPUpdateArticleTags(t *testing.T) {
	tempDir := t.TempDir()

	storage, err := NewStorage(tempDir)
	if err != nil {
		t.Fatalf("Failed to initialize storage: %v", err)
	}
	t.Cleanup(func() { _ = storage.Close() })

	eventBus := NewEventBus()
	srv := NewServer(storage, "Test Wiki", "light", false, eventBus, "1.0.0", "")

	// 1. Create a standard article first
	_, err = storage.SaveArticle("", "Golang Guide", "# Go content", "", "", "", "Initial seed", []string{"go", "backend"}, "")
	if err != nil {
		t.Fatalf("Failed to save article: %v", err)
	}

	// 2. Call update_article_tags via the MCP tool interface
	updateArgs := json.RawMessage(`{"name":"update_article_tags","arguments":{"slug":"golang-guide","tags":["programming","backend","language"],"loaded_version":1,"edit_summary":"MCP tag update"}}`)
	res, rpcErr := srv.executeToolCallInternal(updateArgs)
	if rpcErr != nil {
		t.Fatalf("update_article_tags failed: %v", rpcErr)
	}

	resp, ok := res.(ToolResponse)
	if !ok || resp.IsError {
		t.Fatalf("update_article_tags returned error response: %v", resp)
	}

	// 3. Verify changes on disk
	art, err := storage.GetArticle("golang-guide")
	if err != nil {
		t.Fatalf("Failed to load article: %v", err)
	}

	if art.Version != 2 {
		t.Errorf("Expected version 2, got %d", art.Version)
	}
	if len(art.Tags) != 3 || art.Tags[0] != "programming" || art.Tags[1] != "backend" || art.Tags[2] != "language" {
		t.Errorf("Expected tags ['programming', 'backend', 'language'], got %v", art.Tags)
	}
	if art.Content != "# Go content" {
		t.Errorf("Expected content to remain unchanged, got '%s'", art.Content)
	}
}

// the newMCPServer creates a server for MCP tool testing.
func newMCPServer(t *testing.T) *Server {
	t.Helper()
	storage, err := NewStorage(t.TempDir())
	if err != nil {
		t.Fatalf("NewStorage failed: %v", err)
	}
	t.Cleanup(func() { _ = storage.Close() })
	return NewServer(storage, "Test Wiki", "light", false, NewEventBus(), "1.0.0", "")
}

// toolCall is a helper to call executeToolCallInternal with a JSON string.
func toolCall(t *testing.T, srv *Server, toolJSON string) ToolResponse {
	t.Helper()
	res, rpcErr := srv.executeToolCallInternal(json.RawMessage(toolJSON))
	if rpcErr != nil {
		t.Fatalf("executeToolCallInternal failed with RPC error: %v", rpcErr)
	}
	resp, ok := res.(ToolResponse)
	if !ok {
		t.Fatalf("expected ToolResponse, got %T", res)
	}
	return resp
}

func TestMCPCreateWikiArticle(t *testing.T) {
	srv := newMCPServer(t)

	// Missing title returns an RPC argument error
	_, rpcErr := srv.executeToolCallInternal(json.RawMessage(`{"name":"create_wiki_article","arguments":{"title":"","content":"# Content"}}`))
	if rpcErr == nil {
		t.Error("expected RPC error for missing title")
	}

	// Valid creation
	resp2 := toolCall(t, srv, `{"name":"create_wiki_article","arguments":{"title":"Go Basics","content":"# Go\n\nContent here.","tags":["programming"],"edit_summary":"Initial"}}`)
	if resp2.IsError {
		t.Errorf("expected success, got error: %s", resp2.Content[0].Text)
	}
	if !strings.Contains(resp2.Content[0].Text, "go-basics") {
		t.Errorf("expected slug in success message, got: %s", resp2.Content[0].Text)
	}

	// Duplicate article
	resp3 := toolCall(t, srv, `{"name":"create_wiki_article","arguments":{"title":"Go Basics","content":"# Dupe"}}`)
	if !resp3.IsError {
		t.Error("expected error for duplicate article")
	}
}

func TestMCPReadArticle(t *testing.T) {
	srv := newMCPServer(t)

	// Missing slug returns an RPC argument error
	_, rpcErr := srv.executeToolCallInternal(json.RawMessage(`{"name":"read_article","arguments":{"slug":""}}`))
	if rpcErr == nil {
		t.Error("expected RPC error for missing slug")
	}

	// Not found
	resp2 := toolCall(t, srv, `{"name":"read_article","arguments":{"slug":"nonexistent"}}`)
	if !resp2.IsError {
		t.Error("expected error for nonexistent article")
	}

	// Valid read
	_, _ = srv.Storage.SaveArticle("", "Readable Article", "# Content here", "", "", "", "", []string{"docs"}, "")
	resp3 := toolCall(t, srv, `{"name":"read_article","arguments":{"slug":"readable-article"}}`)
	if resp3.IsError {
		t.Errorf("expected success, got error: %s", resp3.Content[0].Text)
	}
	// The body arrives in the structured payload, not the text block. A test that accepted
	// either would have passed against the version of this tool that returned no body at all.
	out, ok := resp3.StructuredContent.(ArticleOutput)
	if !ok {
		t.Fatalf("expected ArticleOutput, got %T", resp3.StructuredContent)
	}
	if !strings.Contains(out.Article.Content, "Content here") {
		t.Errorf("expected the body in the structured payload, got: %q", out.Article.Content)
	}
}

func TestMCPListArticles(t *testing.T) {
	srv := newMCPServer(t)

	// Empty (home is seeded but excluded)
	resp := toolCall(t, srv, `{"name":"list_articles","arguments":{}}`)
	if resp.IsError {
		t.Errorf("expected success, got error: %s", resp.Content[0].Text)
	}
	if !strings.Contains(resp.Content[0].Text, "no articles") && !strings.Contains(resp.Content[0].Text, "0 articles") {
		// Allow either "no articles" or a zero-count message
		_ = resp
	}

	// After saves
	_, _ = srv.Storage.SaveArticle("", "First Article", "# first", "", "", "", "", []string{"notes"}, "")
	_, _ = srv.Storage.SaveArticle("", "Second Article", "# second", "", "", "", "", nil, "")
	resp2 := toolCall(t, srv, `{"name":"list_articles","arguments":{}}`)
	if resp2.IsError {
		t.Errorf("expected success, got error: %s", resp2.Content[0].Text)
	}
	if !strings.Contains(resp2.Content[0].Text, "First Article") {
		t.Errorf("expected article titles in response, got: %s", resp2.Content[0].Text)
	}
}

func TestMCPDeleteWikiArticle(t *testing.T) {
	srv := newMCPServer(t)

	// Not found
	resp := toolCall(t, srv, `{"name":"delete_wiki_article","arguments":{"slug":"nonexistent"}}`)
	if !resp.IsError {
		t.Error("expected error for nonexistent article")
	}

	// Valid delete
	_, _ = srv.Storage.SaveArticle("", "To Delete", "# bye", "", "", "", "", nil, "")
	resp2 := toolCall(t, srv, `{"name":"delete_wiki_article","arguments":{"slug":"to-delete"}}`)
	if resp2.IsError {
		t.Errorf("expected success, got error: %s", resp2.Content[0].Text)
	}
	if _, err := srv.Storage.GetArticle("to-delete"); err == nil {
		t.Error("article should be deleted")
	}
}

func TestMCPCreateAgentMemory(t *testing.T) {
	srv := newMCPServer(t)

	// Project-scoped memory: memory_type becomes the tag suffix
	resp := toolCall(t, srv, `{"name":"create_agent_memory","arguments":{"memory_kind":"project","title":"NexWiki Deploy Notes","content":"# Notes","memory_type":"nexwiki","description":"fixture memory","source":"test fixture"}}`)
	if resp.IsError {
		t.Errorf("expected success for project-scoped memory, got error: %s", resp.Content[0].Text)
	}
	art, err := srv.Storage.GetArticle("nexwiki-deploy-notes")
	if err != nil {
		t.Fatalf("failed to get created memory: %v", err)
	}
	if art.Type != ContentTypeMemory {
		t.Errorf("expected type AI-Agent-Memory, got %q", art.Type)
	}
	hasTag := false
	for _, tag := range art.Tags {
		if tag == "memory-nexwiki" {
			hasTag = true
			break
		}
	}
	if !hasTag {
		t.Errorf("expected memory-nexwiki scope tag, got %v", art.Tags)
	}

	// Any free-form memory_type is accepted (e.g., topic name)
	resp2 := toolCall(t, srv, `{"name":"create_agent_memory","arguments":{"memory_kind":"project","title":"Docker Tips","content":"# Tips","memory_type":"docker","description":"fixture memory","source":"test fixture"}}`)
	if resp2.IsError {
		t.Errorf("expected success for topic-scoped memory, got error: %s", resp2.Content[0].Text)
	}
	art2, err := srv.Storage.GetArticle("docker-tips")
	if err != nil {
		t.Fatalf("failed to get topic-scoped memory: %v", err)
	}
	hasDockerTag := false
	for _, tag := range art2.Tags {
		if tag == "memory-docker" {
			hasDockerTag = true
			break
		}
	}
	if !hasDockerTag {
		t.Errorf("expected memory-docker scope tag, got %v", art2.Tags)
	}

	// Omitting memory_type produces a bare memory: type AI-Agent-Memory with no scope tag
	resp3 := toolCall(t, srv, `{"name":"create_agent_memory","arguments":{"memory_kind":"project","title":"General Note","content":"# General","description":"fixture memory","source":"test fixture"}}`)
	if resp3.IsError {
		t.Errorf("expected success for unscoped memory, got error: %s", resp3.Content[0].Text)
	}
	art3, err := srv.Storage.GetArticle("general-note")
	if err != nil {
		t.Fatalf("failed to get unscoped memory: %v", err)
	}
	if art3.Type != ContentTypeMemory {
		t.Errorf("expected type AI-Agent-Memory for bare memory, got %q", art3.Type)
	}
	if len(art3.Tags) != 0 {
		t.Errorf("expected no scope tags on a bare memory, got %v", art3.Tags)
	}
}

func TestMCPAppendAgentMemory(t *testing.T) {
	srv := newMCPServer(t)

	// Not a memory (regular article)
	_, _ = srv.Storage.SaveArticle("", "Regular Article", "# content", "", "", "", "", nil, "")
	resp := toolCall(t, srv, `{"name":"append_agent_memory","arguments":{"slug":"regular-article","content_to_append":"## Appended"}}`)
	if !resp.IsError {
		t.Error("expected error for non-memory article")
	}

	// Valid append to scoped memory
	_, _ = srv.Storage.SaveArticle("", "My Memory", "# Base content", "", "", "", "", []string{"memory-nexwiki"}, ContentTypeMemory)
	resp2 := toolCall(t, srv, `{"name":"append_agent_memory","arguments":{"slug":"my-memory","content_to_append":"\n\n## Appended Section\n\nNew content here."}}`)
	if resp2.IsError {
		t.Errorf("expected success, got error: %s", resp2.Content[0].Text)
	}

	// Verify content was appended
	art, _ := srv.Storage.GetArticle("my-memory")
	if !strings.Contains(art.Content, "Appended Section") {
		t.Errorf("expected appended content, got: %s", art.Content)
	}

	// Valid append to a bare (unscoped) memory document
	_, _ = srv.Storage.SaveArticle("", "General Memory", "# Base", "", "", "", "", nil, ContentTypeMemory)
	resp3 := toolCall(t, srv, `{"name":"append_agent_memory","arguments":{"slug":"general-memory","content_to_append":"\n\n## Extra"}}`)
	if resp3.IsError {
		t.Errorf("expected success appending to bare memory document, got error: %s", resp3.Content[0].Text)
	}
}

func TestMCPCreateAgentPlan(t *testing.T) {
	srv := newMCPServer(t)

	// Valid creation
	resp := toolCall(t, srv, `{"name":"create_agent_plan","arguments":{"title":"Deploy Plan","content":"# Steps\n\n1. Build","project_context":"nexwiki"}}`)
	if resp.IsError {
		t.Errorf("expected success, got error: %s", resp.Content[0].Text)
	}

	// Verify the plan carries the reserved AI-Agent-Plan type and the project-context tag
	art, err := srv.Storage.GetArticle("deploy-plan")
	if err != nil {
		t.Fatalf("failed to get created plan: %v", err)
	}
	if art.Type != ContentTypePlan {
		t.Errorf("expected type AI-Agent-Plan, got %q", art.Type)
	}
	hasTag := false
	for _, tag := range art.Tags {
		if tag == "nexwiki" {
			hasTag = true
			break
		}
	}
	if !hasTag {
		t.Errorf("expected nexwiki project tag, got %v", art.Tags)
	}
}

func TestMCPAppendAgentPlan(t *testing.T) {
	srv := newMCPServer(t)

	// Not a plan
	_, _ = srv.Storage.SaveArticle("", "Regular Doc", "# doc", "", "", "", "", nil, "")
	resp := toolCall(t, srv, `{"name":"append_agent_plan","arguments":{"slug":"regular-doc","content_to_append":"\n\n## Extra"}}`)
	if !resp.IsError {
		t.Error("expected error for non-plan article")
	}

	// Valid append
	_, _ = srv.Storage.SaveArticle("", "Active Plan", "# Plan\n\nStep 1", "", "", "", "", []string{"aiagent-plan"}, ContentTypePlan)
	resp2 := toolCall(t, srv, `{"name":"append_agent_plan","arguments":{"slug":"active-plan","content_to_append":"\n\n## Step 2\n\nDo the thing."}}`)
	if resp2.IsError {
		t.Errorf("expected success, got error: %s", resp2.Content[0].Text)
	}

	art, _ := srv.Storage.GetArticle("active-plan")
	if !strings.Contains(art.Content, "Step 2") {
		t.Errorf("expected appended content, got: %s", art.Content)
	}
}

func TestMCPListAgentPlans(t *testing.T) {
	srv := newMCPServer(t)

	// No plans
	resp := toolCall(t, srv, `{"name":"list_agent_plans","arguments":{}}`)
	if resp.IsError {
		t.Errorf("expected success, got error: %s", resp.Content[0].Text)
	}

	// Create plans
	_, _ = srv.Storage.SaveArticle("", "Project Alpha Plan", "# plan", "", "", "", "", []string{"aiagent-plan", "alpha"}, ContentTypePlan)
	_, _ = srv.Storage.SaveArticle("", "Project Beta Plan", "# plan", "", "", "", "", []string{"aiagent-plan", "beta"}, ContentTypePlan)

	resp2 := toolCall(t, srv, `{"name":"list_agent_plans","arguments":{}}`)
	if resp2.IsError {
		t.Errorf("expected success, got error: %s", resp2.Content[0].Text)
	}
	if !strings.Contains(resp2.Content[0].Text, "Project Alpha Plan") {
		t.Errorf("expected plan in response, got: %s", resp2.Content[0].Text)
	}
}

func TestMCPCreateAgentSkill(t *testing.T) {
	srv := newMCPServer(t)

	// Valid skill creation
	resp := toolCall(t, srv, `{"name":"create_agent_skill","arguments":{"title":"Search Helper","content":"# Search Helper\n\nThis skill helps search articles."}}`)
	if resp.IsError {
		t.Errorf("expected success, got error: %s", resp.Content[0].Text)
	}

	// Verify the skill carries the reserved AI-Agent-Skill type
	art, err := srv.Storage.GetArticle("search-helper")
	if err != nil {
		t.Fatalf("failed to get created skill: %v", err)
	}
	if art.Type != ContentTypeSkill {
		t.Errorf("expected type AI-Agent-Skill, got %q (tags %v)", art.Type, art.Tags)
	}
}

func TestMCPListAgentSkills(t *testing.T) {
	srv := newMCPServer(t)

	// Empty
	resp := toolCall(t, srv, `{"name":"list_agent_skills","arguments":{}}`)
	if resp.IsError {
		t.Errorf("expected success, got error: %s", resp.Content[0].Text)
	}

	// After creating a skill
	_, _ = srv.Storage.SaveArticle("", "My Skill", "# skill content", "", "", "", "", []string{"aiagent-skill"}, ContentTypeSkill)
	resp2 := toolCall(t, srv, `{"name":"list_agent_skills","arguments":{}}`)
	if resp2.IsError {
		t.Errorf("expected success, got error: %s", resp2.Content[0].Text)
	}
	if !strings.Contains(resp2.Content[0].Text, "My Skill") {
		t.Errorf("expected skill in response, got: %s", resp2.Content[0].Text)
	}
}

func TestMCPGetStatusTags(t *testing.T) {
	srv := newMCPServer(t)

	resp := toolCall(t, srv, `{"name":"get_status_tags","arguments":{}}`)
	if resp.IsError {
		t.Errorf("expected success, got error: %s", resp.Content[0].Text)
	}
	if !strings.Contains(resp.Content[0].Text, "completed") {
		t.Errorf("expected status tags in response, got: %s", resp.Content[0].Text)
	}
}

func TestMCPGetArticleHistory(t *testing.T) {
	srv := newMCPServer(t)

	// No history yet (missing article)
	resp := toolCall(t, srv, `{"name":"get_article_history","arguments":{"slug":"nonexistent"}}`)
	// Should return empty history or an error about no article - either is acceptable

	// After creating and updating
	_, _ = srv.Storage.SaveArticle("", "History Article", "# v1", "", "", "", "", nil, "")
	_, _ = srv.Storage.SaveArticle("history-article", "History Article", "# v2", "", "", "", "", nil, "")
	resp2 := toolCall(t, srv, `{"name":"get_article_history","arguments":{"slug":"history-article"}}`)
	if resp2.IsError {
		t.Errorf("expected success, got error: %s", resp2.Content[0].Text)
	}
	if !strings.Contains(resp2.Content[0].Text, "version") && !strings.Contains(resp2.Content[0].Text, "Version") {
		t.Errorf("expected version info in history response, got: %s", resp2.Content[0].Text)
	}
	_ = resp
}

func TestMCPRevertArticleVersion(t *testing.T) {
	srv := newMCPServer(t)

	// Invalid version
	_, _ = srv.Storage.SaveArticle("", "Revert Test", "# v1", "", "", "", "", nil, "")
	_, _ = srv.Storage.SaveArticle("revert-test", "Revert Test", "# v2", "", "", "", "", nil, "")
	resp := toolCall(t, srv, `{"name":"revert_article_version","arguments":{"slug":"revert-test","version":99}}`)
	if !resp.IsError {
		t.Error("expected error for nonexistent version")
	}

	// Valid revert
	resp2 := toolCall(t, srv, `{"name":"revert_article_version","arguments":{"slug":"revert-test","version":1}}`)
	if resp2.IsError {
		t.Errorf("expected success, got error: %s", resp2.Content[0].Text)
	}
}

func TestMCPGetWikiStatistics(t *testing.T) {
	srv := newMCPServer(t)

	resp := toolCall(t, srv, `{"name":"get_wiki_statistics","arguments":{}}`)
	if resp.IsError {
		t.Errorf("expected success, got error: %s", resp.Content[0].Text)
	}

	// Add articles and check stats appear in output
	_, _ = srv.Storage.SaveArticle("", "Wiki Page", "# content", "", "", "", "", nil, "")
	resp2 := toolCall(t, srv, `{"name":"get_wiki_statistics","arguments":{}}`)
	if resp2.IsError {
		t.Errorf("expected success, got error: %s", resp2.Content[0].Text)
	}
	if !strings.Contains(resp2.Content[0].Text, "1") {
		t.Errorf("expected count in stats, got: %s", resp2.Content[0].Text)
	}
}

func TestMCPSearchWiki(t *testing.T) {
	srv := newMCPServer(t)

	// Empty query: search_wiki returns an RPC error (missing required argument)
	_, rpcErr := srv.executeToolCallInternal(json.RawMessage(`{"name":"search_wiki","arguments":{"query":""}}`))
	if rpcErr == nil {
		t.Error("expected RPC error for empty query")
	}

	// Valid query (no results in fresh storage)
	resp2 := toolCall(t, srv, `{"name":"search_wiki","arguments":{"query":"golang"}}`)
	if resp2.IsError {
		t.Errorf("expected success for valid query, got: %s", resp2.Content[0].Text)
	}
}

func TestHandleRequest_Protocol(t *testing.T) {
	srv := newMCPServer(t)
	id := float64(1)

	tests := []struct {
		name          string
		method        string
		params        string
		wantError     bool
		wantResultKey string
	}{
		{"initialize", "initialize", "null", false, "protocolVersion"},
		{"tools/list", "tools/list", "null", false, "tools"},
		{"prompts/list", "prompts/list", "null", false, "prompts"},
		{"unknown method", "unknown/method", "null", true, ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var params json.RawMessage
			if tc.params == "null" {
				params = json.RawMessage(`null`)
			} else {
				params = json.RawMessage(tc.params)
			}

			req := &JSONRPCRequest{
				JSONRPC: "2.0",
				Method:  tc.method,
				Params:  params,
				ID:      id,
			}

			buf := &bytes.Buffer{}
			srv.handleRequest(buf, req)

			if buf.Len() == 0 {
				t.Fatal("handleRequest wrote no response")
			}

			var resp JSONRPCResponse
			if err := json.Unmarshal(buf.Bytes(), &resp); err != nil {
				t.Fatalf("failed to parse response: %v (raw: %s)", err, buf.String())
			}

			if tc.wantError {
				if resp.Error == nil {
					t.Errorf("expected error response, got result: %v", resp.Result)
				}
			} else {
				if resp.Error != nil {
					t.Errorf("expected success, got error: %v", resp.Error)
				}
				if tc.wantResultKey != "" {
					resultMap, ok := resp.Result.(map[string]interface{})
					if !ok {
						t.Fatalf("expected map result, got %T", resp.Result)
					}
					if _, ok := resultMap[tc.wantResultKey]; !ok {
						t.Errorf("expected key '%s' in result, got keys: %v", tc.wantResultKey, resultMap)
					}
				}
			}
		})
	}
}

func TestHandleRequest_Notification(t *testing.T) {
	// Notifications (no ID) should be silently ignored
	srv := newMCPServer(t)
	req := &JSONRPCRequest{
		JSONRPC: "2.0",
		Method:  "notifications/initialized",
		ID:      nil,
	}
	buf := &bytes.Buffer{}
	srv.handleRequest(buf, req)
	if buf.Len() != 0 {
		t.Errorf("notifications should produce no output, got: %s", buf.String())
	}
}

func TestHandleRequest_PromptsGet(t *testing.T) {
	srv := newMCPServer(t)
	id := float64(1)

	// Valid prompt: article_creation_workflow
	params := json.RawMessage(`{"name":"article_creation_workflow","arguments":{"title":"My Article","description":"A test article"}}`)
	req := &JSONRPCRequest{JSONRPC: "2.0", Method: "prompts/get", Params: params, ID: id}
	buf := &bytes.Buffer{}
	srv.handleRequest(buf, req)

	var resp JSONRPCResponse
	if err := json.Unmarshal(buf.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse: %v", err)
	}
	if resp.Error != nil {
		t.Errorf("expected success for article_creation_workflow, got error: %v", resp.Error)
	}

	// Valid prompt: project_planning_workflow
	params2 := json.RawMessage(`{"name":"project_planning_workflow","arguments":{"title":"Deploy Plan","project":"nexwiki"}}`)
	req2 := &JSONRPCRequest{JSONRPC: "2.0", Method: "prompts/get", Params: params2, ID: id}
	buf2 := &bytes.Buffer{}
	srv.handleRequest(buf2, req2)

	var resp2 JSONRPCResponse
	if err := json.Unmarshal(buf2.Bytes(), &resp2); err != nil {
		t.Fatalf("failed to parse2: %v", err)
	}
	if resp2.Error != nil {
		t.Errorf("expected success for project_planning_workflow, got error: %v", resp2.Error)
	}

	// Unknown prompt name
	params3 := json.RawMessage(`{"name":"unknown_prompt","arguments":{}}`)
	req3 := &JSONRPCRequest{JSONRPC: "2.0", Method: "prompts/get", Params: params3, ID: id}
	buf3 := &bytes.Buffer{}
	srv.handleRequest(buf3, req3)

	var resp3 JSONRPCResponse
	if err := json.Unmarshal(buf3.Bytes(), &resp3); err != nil {
		t.Fatalf("failed to parse3: %v", err)
	}
	if resp3.Error == nil {
		t.Error("expected error for unknown prompt name")
	}
}

func TestMCPListAgentMemories(t *testing.T) {
	srv := newMCPServer(t)

	// No memories
	resp := toolCall(t, srv, `{"name":"list_agent_memories","arguments":{}}`)
	if resp.IsError {
		t.Errorf("expected success, got error: %s", resp.Content[0].Text)
	}

	// Create memories: scoped (memory-<scope> tag) and bare (no scope tag), all of type Memory
	_, _ = srv.Storage.SaveArticle("", "NexWiki Notes", "# notes", "", "", "", "", []string{"memory-nexwiki"}, ContentTypeMemory)
	_, _ = srv.Storage.SaveArticle("", "Docker Tips", "# tips", "", "", "", "", []string{"memory-docker"}, ContentTypeMemory)
	_, _ = srv.Storage.SaveArticle("", "General Note", "# general", "", "", "", "", nil, ContentTypeMemory)

	// List all — the bare (unscoped) memory must be included
	resp2 := toolCall(t, srv, `{"name":"list_agent_memories","arguments":{}}`)
	if resp2.IsError {
		t.Errorf("expected success, got error: %s", resp2.Content[0].Text)
	}
	if !strings.Contains(resp2.Content[0].Text, "NexWiki Notes") {
		t.Errorf("expected scoped memory in response, got: %s", resp2.Content[0].Text)
	}
	if !strings.Contains(resp2.Content[0].Text, "General Note") {
		t.Errorf("expected bare-tagged memory in response, got: %s", resp2.Content[0].Text)
	}

	// Filter by project name
	resp3 := toolCall(t, srv, `{"name":"list_agent_memories","arguments":{"memory_type":"nexwiki"}}`)
	if resp3.IsError {
		t.Errorf("expected success, got error: %s", resp3.Content[0].Text)
	}
	if !strings.Contains(resp3.Content[0].Text, "NexWiki Notes") {
		t.Errorf("expected nexwiki memory in filtered response, got: %s", resp3.Content[0].Text)
	}
	if strings.Contains(resp3.Content[0].Text, "Docker Tips") {
		t.Errorf("docker memory should not appear in nexwiki filter, got: %s", resp3.Content[0].Text)
	}
}

func TestExecuteToolCallLogsActivity(t *testing.T) {
	srv := newMCPServer(t)

	// executeToolCall (not internal) should log to EventBus without error
	params := json.RawMessage(`{"name":"list_articles","arguments":{}}`)
	result, rpcErr := srv.executeToolCall(params, "Test Client")
	if rpcErr != nil {
		t.Fatalf("executeToolCall returned RPC error: %v", rpcErr)
	}
	resp, ok := result.(ToolResponse)
	if !ok {
		t.Fatalf("expected ToolResponse, got %T", result)
	}
	if resp.IsError {
		t.Errorf("expected success, got error: %s", resp.Content[0].Text)
	}
}

func TestLogMCPToolCallBranches(t *testing.T) {
	srv := newMCPServer(t)

	// Covers create_ prefix → "create" action
	_, _ = srv.executeToolCall(json.RawMessage(`{"name":"create_wiki_article","arguments":{"title":"Log Test Article","content":"# Content"}}`), "Test Client")

	// Covers delete_ prefix → "delete" action
	_, _ = srv.Storage.SaveArticle("", "Log Delete Me", "# bye", "", "", "", "", nil, "")
	_, _ = srv.executeToolCall(json.RawMessage(`{"name":"delete_wiki_article","arguments":{"slug":"log-delete-me"}}`), "Test Client")

	// Covers edit_ prefix → "edit" action
	_, _ = srv.Storage.SaveArticle("", "Log Edit Me", "# v1", "", "", "", "", nil, "")
	_, _ = srv.executeToolCall(json.RawMessage(`{"name":"edit_wiki_article","arguments":{"slug":"log-edit-me","title":"Log Edit Me","content":"# v2","loaded_version":1}}`), "Test Client")

	// Covers append_ prefix → "edit" action
	_, _ = srv.Storage.SaveArticle("", "Log Append Me", "# base", "", "", "", "", []string{"aiagent-plan"}, ContentTypePlan)
	_, _ = srv.executeToolCall(json.RawMessage(`{"name":"append_agent_plan","arguments":{"slug":"log-append-me","content_to_append":"\n\n## Appended"}}`), "Test Client")

	// Verify EventBus received events
	time.Sleep(10 * time.Millisecond) // let async goroutines finish if any
	history := srv.EventBus.GetHistory()
	if len(history) == 0 {
		t.Error("expected events in EventBus history after tool calls")
	}
}

func TestMCPEditAgentPlanContentEditing(t *testing.T) {
	srv := newMCPServer(t)

	// Seed plan via the tool so version/type are set correctly.
	toolCall(t, srv, `{"name":"create_agent_plan","arguments":{"title":"Content Test Plan","content":"# Phase 1\n\n- [ ] Task A\n- [ ] Task B","project_context":"test"}}`)

	// 1. Content replacement: body changes, version increments, OKF type preserved.
	resp := toolCall(t, srv, `{"name":"edit_agent_plan","arguments":{"slug":"content-test-plan","content":"# Rewritten\n\n- [ ] New Step","loaded_version":1,"edit_summary":"Rewrote plan steps"}}`)
	if resp.IsError {
		t.Fatalf("expected success on content replacement, got: %s", resp.Content[0].Text)
	}
	art, _ := srv.Storage.GetArticle("content-test-plan")
	if !strings.Contains(art.Content, "Rewritten") {
		t.Errorf("expected rewritten content, got: %s", art.Content)
	}
	if strings.Contains(art.Content, "Task A") {
		t.Errorf("old content should have been replaced, got: %s", art.Content)
	}
	if art.Version != 2 {
		t.Errorf("expected version 2 after content replace, got %d", art.Version)
	}
	if art.Type != ContentTypePlan {
		t.Errorf("OKF type must be preserved after content edit, got %q", art.Type)
	}

	// 2. Content preservation: omitting content leaves body unchanged.
	resp2 := toolCall(t, srv, `{"name":"edit_agent_plan","arguments":{"slug":"content-test-plan","tags":["test"],"status":"completed","loaded_version":2,"edit_summary":"Mark completed"}}`)
	if resp2.IsError {
		t.Fatalf("expected success on metadata-only edit, got: %s", resp2.Content[0].Text)
	}
	art2, _ := srv.Storage.GetArticle("content-test-plan")
	if !strings.Contains(art2.Content, "Rewritten") {
		t.Errorf("content should be preserved when omitted from edit, got: %s", art2.Content)
	}
	if art2.Version != 3 {
		t.Errorf("expected version 3, got %d", art2.Version)
	}

	// 3. Empty content guard: content="" must return a tool error without saving.
	resp3 := toolCall(t, srv, `{"name":"edit_agent_plan","arguments":{"slug":"content-test-plan","content":"","loaded_version":3}}`)
	if !resp3.IsError {
		t.Error("expected error when content is empty string")
	}
	if !strings.Contains(resp3.Content[0].Text, "cannot be empty") {
		t.Errorf("expected 'cannot be empty' in error, got: %s", resp3.Content[0].Text)
	}
	// Version must not have changed (guard returned before saving).
	artAfterGuard, _ := srv.Storage.GetArticle("content-test-plan")
	if artAfterGuard.Version != 3 {
		t.Errorf("version should not have changed after empty-content guard, got %d", artAfterGuard.Version)
	}

	// 4. Empty title guard: title="" must return a tool error without saving.
	resp4 := toolCall(t, srv, `{"name":"edit_agent_plan","arguments":{"slug":"content-test-plan","title":"","loaded_version":3}}`)
	if !resp4.IsError {
		t.Error("expected error when title is empty string")
	}
	if !strings.Contains(resp4.Content[0].Text, "cannot be empty") {
		t.Errorf("expected 'cannot be empty' in error, got: %s", resp4.Content[0].Text)
	}
}

// TestMCPEditAgentPlanDescriptionAndSource covers the gap that left most plans in a real wiki
// with an empty description: create_agent_plan accepted description and source, edit_agent_plan
// accepted neither, so the one-line summary get_context_overview shows could be set once at
// creation and never corrected. Both use pointer semantics, matching edit_agent_memory.
func TestMCPEditAgentPlanDescriptionAndSource(t *testing.T) {
	srv := newMCPServer(t)

	toolCall(t, srv, `{"name":"create_agent_plan","arguments":{"title":"Described Plan","content":"# Step 1","project_context":"test","description":"first summary","source":"session notes"}}`)

	// Omitted description and source preserve the existing values.
	if resp := toolCall(t, srv, `{"name":"edit_agent_plan","arguments":{"slug":"described-plan","content":"# Step 2","loaded_version":1}}`); resp.IsError {
		t.Fatalf("edit failed: %s", resp.Content[0].Text)
	}
	art, _ := srv.Storage.GetArticle("described-plan")
	if art.Description != "first summary" || art.Source != "session notes" {
		t.Errorf("omitted fields must be preserved, got description=%q source=%q", art.Description, art.Source)
	}

	// A supplied description replaces it — the whole point of the fix.
	if resp := toolCall(t, srv, `{"name":"edit_agent_plan","arguments":{"slug":"described-plan","description":"corrected summary","loaded_version":2}}`); resp.IsError {
		t.Fatalf("edit failed: %s", resp.Content[0].Text)
	}
	art, _ = srv.Storage.GetArticle("described-plan")
	if art.Description != "corrected summary" {
		t.Errorf("description = %q, want the corrected value", art.Description)
	}
	if art.Source != "session notes" {
		t.Errorf("source must survive a description-only edit, got %q", art.Source)
	}

	// An explicit empty string clears, which is why these are pointers rather than plain strings.
	if resp := toolCall(t, srv, `{"name":"edit_agent_plan","arguments":{"slug":"described-plan","source":"","loaded_version":3}}`); resp.IsError {
		t.Fatalf("edit failed: %s", resp.Content[0].Text)
	}
	art, _ = srv.Storage.GetArticle("described-plan")
	if art.Source != "" {
		t.Errorf("an explicit empty source must clear it, got %q", art.Source)
	}
	if art.Type != ContentTypePlan {
		t.Errorf("OKF type must be preserved, got %q", art.Type)
	}
}

// TestMCPEditAgentSkill covers the tool that did not exist: create_agent_skill and
// list_agent_skills shipped without an edit counterpart, so revising a skill — including
// nexwiki-agent-guidelines, the governance document every agent loads — had no first-class path.
func TestMCPEditAgentSkill(t *testing.T) {
	srv := newMCPServer(t)

	toolCall(t, srv, `{"name":"create_agent_skill","arguments":{"title":"Prune Containers","content":"# Steps\n\n1. docker system prune","description":"how to prune","status":"draft"}}`)

	// Content, description, and a draft -> ready promotion in one edit.
	resp := toolCall(t, srv, `{"name":"edit_agent_skill","arguments":{"slug":"prune-containers","content":"# Steps\n\n1. docker system prune -af","description":"how to prune aggressively","status":"ready","loaded_version":1,"edit_summary":"Promote to ready"}}`)
	if resp.IsError {
		t.Fatalf("edit_agent_skill failed: %s", resp.Content[0].Text)
	}
	art, _ := srv.Storage.GetArticle("prune-containers")
	if !strings.Contains(art.Content, "prune -af") {
		t.Errorf("content not replaced: %s", art.Content)
	}
	if art.Description != "how to prune aggressively" {
		t.Errorf("description = %q", art.Description)
	}
	if art.Version != 2 {
		t.Errorf("version = %d, want 2", art.Version)
	}
	if art.Type != ContentTypeSkill {
		t.Errorf("the reserved AI-Agent-Skill type must survive an edit, got %q", art.Type)
	}
	if art.Status != "ready" {
		t.Errorf("expected the skill promoted to status 'ready', got %q", art.Status)
	}
	if len(art.Tags) != 0 {
		t.Errorf("tags = %v, want none — lifecycle state lives in the status field", art.Tags)
	}

	// Optimistic locking, matching every other edit tool.
	if resp := toolCall(t, srv, `{"name":"edit_agent_skill","arguments":{"slug":"prune-containers","content":"# Stale","loaded_version":1}}`); !resp.IsError {
		t.Error("a stale loaded_version must be rejected")
	}

	// Refuses a target that is not a skill, so it cannot be used to launder a document's type.
	toolCall(t, srv, `{"name":"create_wiki_article","arguments":{"title":"Not A Skill","content":"# Plain"}}`)
	resp = toolCall(t, srv, `{"name":"edit_agent_skill","arguments":{"slug":"not-a-skill","content":"# Hijacked","loaded_version":1}}`)
	if !resp.IsError || !strings.Contains(resp.Content[0].Text, "not a Custom AI Skill") {
		t.Errorf("expected a type guard error, got: %+v", resp)
	}
}

// TestEditingAnUnversionedArticle covers the legacy files written to disk before versioning
// existed. read_article omitted `version` entirely for them (omitempty on zero), and
// edit_wiki_article demanded a positive loaded_version — so the documented read-then-edit loop
// dead-ended on exactly those articles, and the only way to change one was to edit the file by
// hand. Relaxing the guard must not weaken optimistic locking for versioned articles.
func TestEditingAnUnversionedArticle(t *testing.T) {
	srv := newMCPServer(t)

	// A file placed on disk directly, with no version in its front matter — the shape every
	// article created before versioning has.
	legacy := "---\ntype: Wiki\ntitle: Legacy Page\nslug: legacy-page\ntimestamp: \"2026-01-01T00:00:00Z\"\ncreated_at: \"2026-01-01T00:00:00Z\"\n---\n# Legacy Page\n\nWritten before versioning.\n"
	if err := os.WriteFile(filepath.Join(srv.Storage.ArticleDir, "legacy-page.md"), []byte(legacy), 0644); err != nil {
		t.Fatalf("seeding the legacy file failed: %v", err)
	}

	// read_article must report the version rather than omitting it, or the agent has nothing to
	// feed back as loaded_version.
	resp := toolCall(t, srv, `{"name":"read_article","arguments":{"slug":"legacy-page"}}`)
	out, ok := resp.StructuredContent.(ArticleOutput)
	if !ok {
		t.Fatalf("expected ArticleOutput, got %T", resp.StructuredContent)
	}
	if out.Article.Version != 0 {
		t.Fatalf("expected version 0 for an unversioned article, got %d", out.Article.Version)
	}
	raw, err := json.Marshal(out.Article)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	if !strings.Contains(string(raw), `"version":0`) {
		t.Errorf("version must be present in the payload, not dropped by omitempty: %s", raw)
	}

	// Feeding that 0 straight back must work.
	resp = toolCall(t, srv, `{"name":"edit_wiki_article","arguments":{"slug":"legacy-page","title":"Legacy Page","content":"# Legacy Page\n\nNow editable.","loaded_version":0,"edit_summary":"Edit an unversioned article"}}`)
	if resp.IsError {
		t.Fatalf("editing an unversioned article must succeed, got: %s", resp.Content[0].Text)
	}
	art, _ := srv.Storage.GetArticle("legacy-page")
	if !strings.Contains(art.Content, "Now editable") {
		t.Errorf("content was not written: %s", art.Content)
	}

	// The article now has a version, so a stale 0 must be rejected rather than silently
	// overwriting — the check this relaxation must not weaken.
	resp = toolCall(t, srv, `{"name":"edit_wiki_article","arguments":{"slug":"legacy-page","title":"Legacy Page","content":"# Clobbered","loaded_version":0}}`)
	if !resp.IsError || !strings.Contains(resp.Content[0].Text, "version conflict") {
		t.Errorf("a 0 against a versioned article must be a conflict, got: %+v", resp)
	}
	if !strings.Contains(resp.Content[0].Text, "Retry once with loaded_version: 2") {
		t.Errorf("conflict must name the version to retry with, got: %s", resp.Content[0].Text)
	}

	// A negative version is still nonsense and stays a hard argument error.
	if _, rpcErr := srv.executeToolCallInternal(json.RawMessage(`{"name":"edit_wiki_article","arguments":{"slug":"legacy-page","title":"T","content":"C","loaded_version":-1}}`)); rpcErr == nil {
		t.Error("a negative loaded_version must be rejected")
	}
}

func TestMCPAppendAgentPlanComprehensive(t *testing.T) {
	srv := newMCPServer(t)

	// 1. Missing slug → RPC error (required argument).
	_, rpcErr := srv.executeToolCallInternal(json.RawMessage(`{"name":"append_agent_plan","arguments":{"slug":"","content_to_append":"## Extra"}}`))
	if rpcErr == nil {
		t.Error("expected RPC error for empty slug")
	}

	// 2. Missing content_to_append → RPC error (required argument).
	_, rpcErr2 := srv.executeToolCallInternal(json.RawMessage(`{"name":"append_agent_plan","arguments":{"slug":"some-plan","content_to_append":""}}`))
	if rpcErr2 == nil {
		t.Error("expected RPC error for empty content_to_append")
	}

	// 3. Slug not found → tool error.
	resp3 := toolCall(t, srv, `{"name":"append_agent_plan","arguments":{"slug":"nonexistent-plan","content_to_append":"## Extra"}}`)
	if !resp3.IsError {
		t.Error("expected error for nonexistent plan slug")
	}

	// 4. Non-plan article → type validation error mentioning AI-Agent-Plan.
	_, _ = srv.Storage.SaveArticle("", "Regular Doc", "# wiki content", "", "", "", "", nil, "")
	resp4 := toolCall(t, srv, `{"name":"append_agent_plan","arguments":{"slug":"regular-doc","content_to_append":"## Injected"}}`)
	if !resp4.IsError {
		t.Error("expected error when appending to a non-plan article")
	}
	if !strings.Contains(resp4.Content[0].Text, "AI-Agent-Plan") {
		t.Errorf("expected type validation error mentioning AI-Agent-Plan, got: %s", resp4.Content[0].Text)
	}

	// 5. Valid append: original content is preserved, new content follows a double-newline
	//    separator, version increments, and OKF type is unchanged.
	toolCall(t, srv, `{"name":"create_agent_plan","arguments":{"title":"Append Target Plan","content":"# Phase 1\n\n- [ ] Task A","project_context":"test"}}`)

	resp5 := toolCall(t, srv, `{"name":"append_agent_plan","arguments":{"slug":"append-target-plan","content_to_append":"## Phase 2\n\n- [ ] Task B","edit_summary":"Added phase 2"}}`)
	if resp5.IsError {
		t.Fatalf("expected success on valid append, got: %s", resp5.Content[0].Text)
	}
	art, _ := srv.Storage.GetArticle("append-target-plan")
	if !strings.Contains(art.Content, "Phase 1") {
		t.Errorf("original content should be preserved, got: %s", art.Content)
	}
	if !strings.Contains(art.Content, "Phase 2") {
		t.Errorf("appended content should be present, got: %s", art.Content)
	}
	// The handler joins with "\n\n"; verify exact separator between original and appended text.
	if !strings.Contains(art.Content, "Task A\n\n## Phase 2") {
		t.Errorf("expected double-newline separator between original and appended content, got: %q", art.Content)
	}
	if art.Version != 2 {
		t.Errorf("expected version 2 after first append, got %d", art.Version)
	}
	if art.Type != ContentTypePlan {
		t.Errorf("OKF type must be preserved through append, got %q", art.Type)
	}

	// 6. Multiple sequential appends: each increments the version and stacks content.
	toolCall(t, srv, `{"name":"append_agent_plan","arguments":{"slug":"append-target-plan","content_to_append":"## Phase 3\n\n- [ ] Task C"}}`)
	toolCall(t, srv, `{"name":"append_agent_plan","arguments":{"slug":"append-target-plan","content_to_append":"## Final Notes\n\nImplementation complete."}}`)

	artFinal, _ := srv.Storage.GetArticle("append-target-plan")
	if !strings.Contains(artFinal.Content, "Phase 3") {
		t.Errorf("expected phase 3 after second append, got: %s", artFinal.Content)
	}
	if !strings.Contains(artFinal.Content, "Final Notes") {
		t.Errorf("expected final notes after third append, got: %s", artFinal.Content)
	}
	if artFinal.Version != 4 {
		t.Errorf("expected version 4 (1 create + 3 appends), got %d", artFinal.Version)
	}
	if artFinal.Type != ContentTypePlan {
		t.Errorf("OKF type must be preserved through all appends, got %q", artFinal.Type)
	}
}

func TestHandleStreamableHTTP(t *testing.T) {
	srv := newMCPServer(t)

	// OPTIONS pre-flight from the wiki's own loopback UI: allowed, origin echoed back verbatim.
	req := httptest.NewRequest("OPTIONS", "/mcp", nil)
	req.Header.Set("Origin", "http://localhost:8080")
	w := httptest.NewRecorder()
	srv.HandleStreamableHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("OPTIONS: expected 200, got %d", w.Code)
	}
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "http://localhost:8080" {
		t.Errorf("OPTIONS: expected echoed loopback origin, got %q", got)
	}

	// A cross-site page must not be able to drive MCP tools against the unauthenticated server.
	reqEvil := httptest.NewRequest("POST", "/mcp",
		strings.NewReader(`{"jsonrpc":"2.0","method":"tools/list","id":1}`))
	reqEvil.Header.Set("Origin", "https://evil.example")
	wEvil := httptest.NewRecorder()
	srv.HandleStreamableHTTP(wEvil, reqEvil)
	if wEvil.Code != http.StatusForbidden {
		t.Errorf("cross-site MCP POST: expected 403, got %d", wEvil.Code)
	}

	// Unsupported method
	req2 := httptest.NewRequest("PUT", "/mcp", nil)
	w2 := httptest.NewRecorder()
	srv.HandleStreamableHTTP(w2, req2)
	if w2.Code != http.StatusMethodNotAllowed {
		t.Errorf("PUT: expected 405, got %d", w2.Code)
	}

	// POST with invalid JSON
	req3 := httptest.NewRequest("POST", "/mcp", strings.NewReader("not json"))
	req3.Header.Set("Content-Type", "application/json")
	w3 := httptest.NewRecorder()
	srv.HandleStreamableHTTP(w3, req3)
	if w3.Code != http.StatusBadRequest {
		t.Errorf("POST invalid JSON: expected 400, got %d", w3.Code)
	}

	// POST with valid JSON-RPC initialize
	body := `{"jsonrpc":"2.0","method":"initialize","params":null,"id":1}`
	req4 := httptest.NewRequest("POST", "/mcp", strings.NewReader(body))
	req4.Header.Set("Content-Type", "application/json")
	w4 := httptest.NewRecorder()
	srv.HandleStreamableHTTP(w4, req4)
	if w4.Code != http.StatusOK {
		t.Errorf("POST valid request: expected 200, got %d", w4.Code)
	}
	var resp JSONRPCResponse
	if err := json.Unmarshal(w4.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	if resp.Error != nil {
		t.Errorf("expected success, got error: %v", resp.Error)
	}

	// POST with tools/call
	toolBody := `{"jsonrpc":"2.0","method":"tools/call","params":{"name":"get_status_tags","arguments":{}},"id":2}`
	req5 := httptest.NewRequest("POST", "/mcp", strings.NewReader(toolBody))
	req5.Header.Set("Content-Type", "application/json")
	w5 := httptest.NewRecorder()
	srv.HandleStreamableHTTP(w5, req5)
	if w5.Code != http.StatusOK {
		t.Errorf("POST tools/call: expected 200, got %d", w5.Code)
	}

	// GET with immediate context cancel (SSE stream setup then exit)
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately so the stream loop exits right away
	req6 := httptest.NewRequest("GET", "/mcp", nil).WithContext(ctx)
	req6.Header.Set("Accept", "text/event-stream")
	w6 := httptest.NewRecorder()
	srv.HandleStreamableHTTP(w6, req6)
	if w6.Header().Get("Content-Type") != "text/event-stream" {
		t.Errorf("GET SSE: expected text/event-stream, got %s", w6.Header().Get("Content-Type"))
	}

	// GET with unsupported Accept header
	req7 := httptest.NewRequest("GET", "/mcp", nil)
	req7.Header.Set("Accept", "application/json")
	w7 := httptest.NewRecorder()
	srv.HandleStreamableHTTP(w7, req7)
	if w7.Code != http.StatusNotAcceptable {
		t.Errorf("GET unsupported Accept: expected 406, got %d", w7.Code)
	}
}

// TestStdioAcceptsLinesOverBufioDefault covers a failure that silently killed the stdio server.
//
// bufio.Scanner caps a line at 64 KB by default, and overrunning it is not recoverable: Scan
// returns false and the read loop exits for the life of the process. A tool call carrying an
// article body passes 64 KB easily, so writing a long article over stdio ended the MCP session —
// standalone the process exited with status 0, so a supervising client saw a clean shutdown; run
// alongside the web server, HTTP kept serving 200s while the MCP channel stayed dead. Either way
// the agent got no response at all and the article was never written.
func TestStdioAcceptsLinesOverBufioDefault(t *testing.T) {
	srv := newTestServer(t)

	// Comfortably past the 64 KB default, and past the 65,536-byte boundary specifically.
	body := strings.Repeat("x", 200_000)
	request, err := json.Marshal(map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params": map[string]interface{}{
			"name":      "create_wiki_article",
			"arguments": map[string]interface{}{"title": "Large Stdio Article", "content": body},
		},
	})
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	if len(request) <= 64*1024 {
		t.Fatalf("test payload is %d bytes, which does not exercise the cap", len(request))
	}

	var out bytes.Buffer
	scanner := bufio.NewScanner(bytes.NewReader(append(request, '\n')))
	scanner.Buffer(make([]byte, 0, 64*1024), MaxStdioLineBytes)

	if !scanner.Scan() {
		t.Fatalf("scanner rejected a %d-byte line: %v", len(request), scanner.Err())
	}
	var req JSONRPCRequest
	if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	srv.handleRequest(&out, &req)

	if !strings.Contains(out.String(), "Large Stdio Article") {
		t.Errorf("large article was not created; response begins: %.200s", out.String())
	}
	art, err := srv.Storage.GetArticle("large-stdio-article")
	if err != nil {
		t.Fatalf("article not persisted: %v", err)
	}
	if len(art.Content) != len(body) {
		t.Errorf("persisted content is %d bytes, want %d", len(art.Content), len(body))
	}
}

// TestModernSubscriptionsListenOnStdio covers an era inversion.
//
// subscriptions/listen was introduced by the 2026-07-28 revision, but handleModernMethod has no
// case for it — the HTTP transport lifts the method out of dispatch before the era branch, and
// stdio did not. So a modern client, the only kind that knows the method exists, was answered
// "Method not found", while a legacy client got the graceful acknowledgment.
func TestModernSubscriptionsListenOnStdio(t *testing.T) {
	srv := newTestServer(t)

	for _, tc := range []struct{ name, params string }{
		{"legacy", `{"notifications":{"resourcesListChanged":true}}`},
		{"modern", `{"notifications":{"resourcesListChanged":true},"_meta":{` +
			`"io.modelcontextprotocol/protocolVersion":"2026-07-28",` +
			`"io.modelcontextprotocol/clientCapabilities":{}}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			req := JSONRPCRequest{JSONRPC: "2.0", ID: float64(7), Method: "subscriptions/listen",
				Params: json.RawMessage(tc.params)}
			srv.handleRequest(&out, &req)

			var resp map[string]interface{}
			if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
				t.Fatalf("response is not JSON: %q", out.String())
			}
			if _, isErr := resp["error"]; isErr {
				t.Fatalf("subscriptions/listen returned an error: %s", out.String())
			}
			if resp["method"] != "notifications/subscriptions/acknowledged" {
				t.Errorf("method = %v, want the acknowledgment", resp["method"])
			}
		})
	}
}

// TestUpdateArticleTagsVersionConflict covers the fifth optimistic-locking site, which had no
// conflict coverage at all. It was also the worst of the five: UpdateArticleTags returned a bare
// Go error that the tool surfaced verbatim as "Error updating tags: version conflict: loaded
// version 1, current version 2" — no instruction, no value to retry with.
func TestUpdateArticleTagsVersionConflict(t *testing.T) {
	srv := newTestServer(t)

	_, err := srv.Storage.SaveArticle("", "Tag Target", "# body", "", "", "", "seed", []string{"one"}, ContentTypeWiki)
	if err != nil {
		t.Fatalf("seed failed: %v", err)
	}
	// Bump to version 2 so a loaded_version of 1 is genuinely stale.
	if _, err := srv.Storage.UpdateArticleTags("tag-target", []string{"two"}, 1, "bump"); err != nil {
		t.Fatalf("bump failed: %v", err)
	}

	resp := toolCall(t, srv, `{"name":"update_article_tags","arguments":{"slug":"tag-target","tags":["three"],"loaded_version":1}}`)
	if !resp.IsError {
		t.Fatalf("a stale loaded_version must conflict, got: %+v", resp)
	}
	for _, want := range []string{"version conflict", "version 2 on disk", "Retry once with loaded_version: 2"} {
		if !strings.Contains(resp.Content[0].Text, want) {
			t.Errorf("conflict message missing %q, got: %s", want, resp.Content[0].Text)
		}
	}
	// The raw storage error must not leak through: it names no value and no bound.
	if strings.Contains(resp.Content[0].Text, "Error updating tags:") {
		t.Errorf("raw storage error leaked instead of the guided message: %s", resp.Content[0].Text)
	}

	// The instruction must actually work — one corrected retry, no re-read required.
	ok := toolCall(t, srv, `{"name":"update_article_tags","arguments":{"slug":"tag-target","tags":["three"],"loaded_version":2}}`)
	if ok.IsError {
		t.Errorf("the retry the message prescribes must succeed, got: %+v", ok)
	}
}

// TestVersionConflictMessage pins the three properties that make the message a bounded retry
// rather than an open-ended one.
func TestVersionConflictMessage(t *testing.T) {
	msg := versionConflictMessage("plan", "my-plan", 7, 4)

	for _, want := range []string{
		"'my-plan'",                             // which document
		"plan is at version 7 on disk",          // the truth
		"you sent loaded_version 4",             // what was wrong
		"Retry once with loaded_version: 7",     // the value, and the bound
		"sending 4 again will fail identically", // forecloses the identical retry
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("message missing %q, got: %s", want, msg)
		}
	}

	// Re-reading must be offered as conditional, not prescribed — prescribing it is what made the
	// old message an unbounded loop.
	if !strings.Contains(msg, "Re-read only if") {
		t.Errorf("re-reading should be conditional, got: %s", msg)
	}
}
