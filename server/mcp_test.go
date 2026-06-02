package server

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestMCPEditAgentPlan(t *testing.T) {
	tempDir := t.TempDir()

	storage, err := NewStorage(tempDir)
	if err != nil {
		t.Fatalf("Failed to initialize storage: %v", err)
	}

	eventBus := NewEventBus()
	srv := NewServer(storage, "Test Wiki", "light", false, eventBus, "1.0.0")

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
