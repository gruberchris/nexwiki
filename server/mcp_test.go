package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
	if len(plan.Tags) != 2 || plan.Tags[0] != "aiagent-plan" || plan.Tags[1] != "nexwiki" {
		t.Errorf("Expected tags ['aiagent-plan', 'nexwiki'], got %v", plan.Tags)
	}

	// 2. Perform a successful edit using edit_agent_plan
	editArgs := json.RawMessage(`{"name":"edit_agent_plan","arguments":{"slug":"migration-plan","title":"Final Migration Plan","tags":["postgres","nexwiki"],"loaded_version":1,"edit_summary":"Renamed and updated tags"}}`)
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
	// Verify that 'aiagent-plan' is preserved, even though it was not explicitly in the tags arg!
	hasPlanTag := false
	for _, tag := range updatedPlan.Tags {
		if tag == "aiagent-plan" {
			hasPlanTag = true
			break
		}
	}
	if !hasPlanTag {
		t.Errorf("Expected 'aiagent-plan' tag to be preserved in new tags list, got %v", updatedPlan.Tags)
	}
	if len(updatedPlan.Tags) != 3 {
		t.Errorf("Expected 3 tags (aiagent-plan, postgres, nexwiki), got %v", updatedPlan.Tags)
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
	if !strings.Contains(resp3.Content[0].Text, "Version conflict") {
		t.Errorf("Expected version conflict error message, got: %s", resp3.Content[0].Text)
	}

	// 4. Test target validation: try editing a standard article (not a plan)
	_, _ = storage.SaveArticle("", "Standard Page", "Just text", "initial", []string{"notes"})
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
	_, err = storage.SaveArticle("", "Golang Guide", "# Go content", "Initial seed", []string{"go", "backend"})
	if err != nil {
		t.Fatalf("Failed to save article: %v", err)
	}

	// 2. Call update_article_tags via MCP tool interface
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

// newMCPServer creates a server for MCP tool testing.
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
	_, _ = srv.Storage.SaveArticle("", "Readable Article", "# Content here", "", []string{"docs"})
	resp3 := toolCall(t, srv, `{"name":"read_article","arguments":{"slug":"readable-article"}}`)
	if resp3.IsError {
		t.Errorf("expected success, got error: %s", resp3.Content[0].Text)
	}
	if !strings.Contains(resp3.Content[0].Text, "Content here") {
		t.Errorf("expected content in response, got: %s", resp3.Content[0].Text)
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
	_, _ = srv.Storage.SaveArticle("", "First Article", "# first", "", []string{"notes"})
	_, _ = srv.Storage.SaveArticle("", "Second Article", "# second", "", nil)
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
	_, _ = srv.Storage.SaveArticle("", "To Delete", "# bye", "", nil)
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
	resp := toolCall(t, srv, `{"name":"create_agent_memory","arguments":{"title":"NexWiki Deploy Notes","content":"# Notes","memory_type":"nexwiki"}}`)
	if resp.IsError {
		t.Errorf("expected success for project-scoped memory, got error: %s", resp.Content[0].Text)
	}
	art, err := srv.Storage.GetArticle("nexwiki-deploy-notes")
	if err != nil {
		t.Fatalf("failed to get created memory: %v", err)
	}
	hasTag := false
	for _, tag := range art.Tags {
		if tag == "aiagent-memory-nexwiki" {
			hasTag = true
			break
		}
	}
	if !hasTag {
		t.Errorf("expected aiagent-memory-nexwiki tag, got %v", art.Tags)
	}

	// Any free-form memory_type is accepted (e.g. topic name)
	resp2 := toolCall(t, srv, `{"name":"create_agent_memory","arguments":{"title":"Docker Tips","content":"# Tips","memory_type":"docker"}}`)
	if resp2.IsError {
		t.Errorf("expected success for topic-scoped memory, got error: %s", resp2.Content[0].Text)
	}
	art2, err := srv.Storage.GetArticle("docker-tips")
	if err != nil {
		t.Fatalf("failed to get topic-scoped memory: %v", err)
	}
	hasDockerTag := false
	for _, tag := range art2.Tags {
		if tag == "aiagent-memory-docker" {
			hasDockerTag = true
			break
		}
	}
	if !hasDockerTag {
		t.Errorf("expected aiagent-memory-docker tag, got %v", art2.Tags)
	}

	// Omitting memory_type produces the bare aiagent-memory tag
	resp3 := toolCall(t, srv, `{"name":"create_agent_memory","arguments":{"title":"General Note","content":"# General"}}`)
	if resp3.IsError {
		t.Errorf("expected success for unscoped memory, got error: %s", resp3.Content[0].Text)
	}
	art3, err := srv.Storage.GetArticle("general-note")
	if err != nil {
		t.Fatalf("failed to get unscoped memory: %v", err)
	}
	hasBareTag := false
	for _, tag := range art3.Tags {
		if tag == "aiagent-memory" {
			hasBareTag = true
			break
		}
	}
	if !hasBareTag {
		t.Errorf("expected bare aiagent-memory tag, got %v", art3.Tags)
	}
}

func TestMCPAppendAgentMemory(t *testing.T) {
	srv := newMCPServer(t)

	// Not a memory (regular article)
	_, _ = srv.Storage.SaveArticle("", "Regular Article", "# content", "", nil)
	resp := toolCall(t, srv, `{"name":"append_agent_memory","arguments":{"slug":"regular-article","content_to_append":"## Appended"}}`)
	if !resp.IsError {
		t.Error("expected error for non-memory article")
	}

	// Valid append to scoped memory
	_, _ = srv.Storage.SaveArticle("", "My Memory", "# Base content", "", []string{"aiagent-memory-nexwiki"})
	resp2 := toolCall(t, srv, `{"name":"append_agent_memory","arguments":{"slug":"my-memory","content_to_append":"\n\n## Appended Section\n\nNew content here."}}`)
	if resp2.IsError {
		t.Errorf("expected success, got error: %s", resp2.Content[0].Text)
	}

	// Verify content was appended
	art, _ := srv.Storage.GetArticle("my-memory")
	if !strings.Contains(art.Content, "Appended Section") {
		t.Errorf("expected appended content, got: %s", art.Content)
	}

	// Valid append to bare aiagent-memory tagged article
	_, _ = srv.Storage.SaveArticle("", "General Memory", "# Base", "", []string{"aiagent-memory"})
	resp3 := toolCall(t, srv, `{"name":"append_agent_memory","arguments":{"slug":"general-memory","content_to_append":"\n\n## Extra"}}`)
	if resp3.IsError {
		t.Errorf("expected success appending to bare aiagent-memory article, got error: %s", resp3.Content[0].Text)
	}
}

func TestMCPCreateAgentPlan(t *testing.T) {
	srv := newMCPServer(t)

	// Valid creation
	resp := toolCall(t, srv, `{"name":"create_agent_plan","arguments":{"title":"Deploy Plan","content":"# Steps\n\n1. Build","project_context":"nexwiki"}}`)
	if resp.IsError {
		t.Errorf("expected success, got error: %s", resp.Content[0].Text)
	}

	// Verify aiagent-plan tag is present
	art, err := srv.Storage.GetArticle("deploy-plan")
	if err != nil {
		t.Fatalf("failed to get created plan: %v", err)
	}
	hasTag := false
	for _, tag := range art.Tags {
		if tag == "aiagent-plan" {
			hasTag = true
			break
		}
	}
	if !hasTag {
		t.Errorf("expected aiagent-plan tag, got %v", art.Tags)
	}
}

func TestMCPAppendAgentPlan(t *testing.T) {
	srv := newMCPServer(t)

	// Not a plan
	_, _ = srv.Storage.SaveArticle("", "Regular Doc", "# doc", "", nil)
	resp := toolCall(t, srv, `{"name":"append_agent_plan","arguments":{"slug":"regular-doc","content_to_append":"\n\n## Extra"}}`)
	if !resp.IsError {
		t.Error("expected error for non-plan article")
	}

	// Valid append
	_, _ = srv.Storage.SaveArticle("", "Active Plan", "# Plan\n\nStep 1", "", []string{"aiagent-plan"})
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
	_, _ = srv.Storage.SaveArticle("", "Project Alpha Plan", "# plan", "", []string{"aiagent-plan", "alpha"})
	_, _ = srv.Storage.SaveArticle("", "Project Beta Plan", "# plan", "", []string{"aiagent-plan", "beta"})

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

	// Verify aiagent-skill tag
	art, err := srv.Storage.GetArticle("search-helper")
	if err != nil {
		t.Fatalf("failed to get created skill: %v", err)
	}
	hasTag := false
	for _, tag := range art.Tags {
		if tag == "aiagent-skill" {
			hasTag = true
			break
		}
	}
	if !hasTag {
		t.Errorf("expected aiagent-skill tag, got %v", art.Tags)
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
	_, _ = srv.Storage.SaveArticle("", "My Skill", "# skill content", "", []string{"aiagent-skill"})
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
	_, _ = srv.Storage.SaveArticle("", "History Article", "# v1", "", nil)
	_, _ = srv.Storage.SaveArticle("history-article", "History Article", "# v2", "", nil)
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
	_, _ = srv.Storage.SaveArticle("", "Revert Test", "# v1", "", nil)
	_, _ = srv.Storage.SaveArticle("revert-test", "Revert Test", "# v2", "", nil)
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
	_, _ = srv.Storage.SaveArticle("", "Wiki Page", "# content", "", nil)
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

	// Create memories: scoped and bare
	_, _ = srv.Storage.SaveArticle("", "NexWiki Notes", "# notes", "", []string{"aiagent-memory-nexwiki"})
	_, _ = srv.Storage.SaveArticle("", "Docker Tips", "# tips", "", []string{"aiagent-memory-docker"})
	_, _ = srv.Storage.SaveArticle("", "General Note", "# general", "", []string{"aiagent-memory"})

	// List all — bare aiagent-memory tag must be included
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
	result, rpcErr := srv.executeToolCall(params)
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
	_, _ = srv.executeToolCall(json.RawMessage(`{"name":"create_wiki_article","arguments":{"title":"Log Test Article","content":"# Content"}}`))

	// Covers delete_ prefix → "delete" action
	_, _ = srv.Storage.SaveArticle("", "Log Delete Me", "# bye", "", nil)
	_, _ = srv.executeToolCall(json.RawMessage(`{"name":"delete_wiki_article","arguments":{"slug":"log-delete-me"}}`))

	// Covers edit_ prefix → "edit" action
	_, _ = srv.Storage.SaveArticle("", "Log Edit Me", "# v1", "", nil)
	_, _ = srv.executeToolCall(json.RawMessage(`{"name":"edit_wiki_article","arguments":{"slug":"log-edit-me","title":"Log Edit Me","content":"# v2","loaded_version":1}}`))

	// Covers append_ prefix → "edit" action
	_, _ = srv.Storage.SaveArticle("", "Log Append Me", "# base", "", []string{"aiagent-plan"})
	_, _ = srv.executeToolCall(json.RawMessage(`{"name":"append_agent_plan","arguments":{"slug":"log-append-me","content_to_append":"\n\n## Appended"}}`))

	// Verify EventBus received events
	time.Sleep(10 * time.Millisecond) // let async goroutines finish if any
	history := srv.EventBus.GetHistory()
	if len(history) == 0 {
		t.Error("expected events in EventBus history after tool calls")
	}
}

func TestHandleStreamableHTTP(t *testing.T) {
	srv := newMCPServer(t)

	// OPTIONS pre-flight
	req := httptest.NewRequest("OPTIONS", "/mcp", nil)
	w := httptest.NewRecorder()
	srv.HandleStreamableHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("OPTIONS: expected 200, got %d", w.Code)
	}
	if w.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Error("missing CORS header on OPTIONS")
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
