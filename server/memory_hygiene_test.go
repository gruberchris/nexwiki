package server

import (
	"strings"
	"testing"
)

func TestMCPEditAgentMemory(t *testing.T) {
	srv := newMCPServer(t)

	create := toolCall(t, srv, `{"name":"create_agent_memory","arguments":{"title":"Build Quirk","content":"# Original fact","memory_type":"nexwiki","description":"original gist"}}`)
	if create.IsError {
		t.Fatalf("create failed: %s", create.Content[0].Text)
	}

	// Full replacement of content + description, version bump, scoped tag preserved
	edit := toolCall(t, srv, `{"name":"edit_agent_memory","arguments":{"slug":"build-quirk","content":"# Corrected fact","description":"corrected gist","loaded_version":1,"edit_summary":"Corrected stale fact"}}`)
	if edit.IsError {
		t.Fatalf("edit failed: %s", edit.Content[0].Text)
	}
	art, err := srv.Storage.GetArticle("build-quirk")
	if err != nil {
		t.Fatalf("GetArticle failed: %v", err)
	}
	if art.Content != "# Corrected fact" || art.Description != "corrected gist" {
		t.Errorf("edit did not replace content/description: %q / %q", art.Content, art.Description)
	}
	if art.Version != 2 {
		t.Errorf("expected version 2, got %d", art.Version)
	}
	if art.Type != ContentTypeMemory {
		t.Errorf("memory type lost after edit: %q", art.Type)
	}

	// Tags replacement preserves the tool-managed memory-scope tag
	edit2 := toolCall(t, srv, `{"name":"edit_agent_memory","arguments":{"slug":"build-quirk","tags":["extra-topic"],"loaded_version":2}}`)
	if edit2.IsError {
		t.Fatalf("edit2 failed: %s", edit2.Content[0].Text)
	}
	art2, _ := srv.Storage.GetArticle("build-quirk")
	foundScoped := false
	for _, tag := range art2.Tags {
		if tag == "memory-nexwiki" {
			foundScoped = true
		}
	}
	if !foundScoped {
		t.Errorf("expected scoped memory-nexwiki tag preserved, got %v", art2.Tags)
	}

	// Stale loaded_version yields a conflict telling the agent to re-read
	conflict := toolCall(t, srv, `{"name":"edit_agent_memory","arguments":{"slug":"build-quirk","content":"# Should fail","loaded_version":1}}`)
	if !conflict.IsError || !strings.Contains(conflict.Content[0].Text, "Re-read the memory") {
		t.Errorf("expected version conflict with re-read hint, got: %v", conflict)
	}

	// Non-memory target is rejected
	_, _ = srv.Storage.SaveArticle("", "Plain Article", "# plain", "", "", "", "", nil, "")
	notMem := toolCall(t, srv, `{"name":"edit_agent_memory","arguments":{"slug":"plain-article","content":"# nope","loaded_version":1}}`)
	if !notMem.IsError || !strings.Contains(notMem.Content[0].Text, "not a protected AI Agent Memory") {
		t.Errorf("expected memory validation error, got: %v", notMem)
	}

	// Missing slug is an RPC argument error
	_, rpcErr := srv.executeToolCallInternal([]byte(`{"name":"edit_agent_memory","arguments":{"loaded_version":1}}`))
	if rpcErr == nil {
		t.Error("expected RPC error for missing slug")
	}

	// Empty content replacement is rejected
	emptyContent := toolCall(t, srv, `{"name":"edit_agent_memory","arguments":{"slug":"build-quirk","content":"  ","loaded_version":3}}`)
	if !emptyContent.IsError {
		t.Error("expected error for empty content replacement")
	}
}

func TestMCPDeleteAgentMemory(t *testing.T) {
	srv := newMCPServer(t)

	_ = toolCall(t, srv, `{"name":"create_agent_memory","arguments":{"title":"Obsolete Memory","content":"# stale"}}`)
	_, _ = srv.Storage.SaveArticle("", "Plain Doc", "# doc", "", "", "", "", nil, "")

	// Refuses plain articles
	notMem := toolCall(t, srv, `{"name":"delete_agent_memory","arguments":{"slug":"plain-doc"}}`)
	if !notMem.IsError || !strings.Contains(notMem.Content[0].Text, "not a protected AI Agent Memory") {
		t.Errorf("expected refusal for plain article, got: %v", notMem)
	}

	// Deletes a memory
	del := toolCall(t, srv, `{"name":"delete_agent_memory","arguments":{"slug":"obsolete-memory"}}`)
	if del.IsError {
		t.Fatalf("delete failed: %s", del.Content[0].Text)
	}
	if _, err := srv.Storage.GetArticle("obsolete-memory"); err == nil {
		t.Error("expected memory to be deleted")
	}
}

func TestMCPDeleteWikiArticleRefusesMemories(t *testing.T) {
	srv := newMCPServer(t)

	_ = toolCall(t, srv, `{"name":"create_agent_memory","arguments":{"title":"Guarded Memory","content":"# keep"}}`)
	_, _ = srv.Storage.SaveArticle("", "Deletable Doc", "# doc", "", "", "", "", nil, "")

	// delete_wiki_article refuses the memory and points to delete_agent_memory
	refused := toolCall(t, srv, `{"name":"delete_wiki_article","arguments":{"slug":"guarded-memory"}}`)
	if !refused.IsError || !strings.Contains(refused.Content[0].Text, "delete_agent_memory") {
		t.Errorf("expected memory guard refusal, got: %v", refused)
	}
	if _, err := srv.Storage.GetArticle("guarded-memory"); err != nil {
		t.Error("memory should still exist after refused delete")
	}

	// Plain articles still are deleted fine
	ok := toolCall(t, srv, `{"name":"delete_wiki_article","arguments":{"slug":"deletable-doc"}}`)
	if ok.IsError {
		t.Fatalf("plain delete failed: %s", ok.Content[0].Text)
	}
	if _, err := srv.Storage.GetArticle("deletable-doc"); err == nil {
		t.Error("expected plain article to be deleted")
	}
}
