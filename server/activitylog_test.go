package server

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

func seedEvent(id string, ts time.Time, source, action string) LogEvent {
	return LogEvent{ID: id, Timestamp: ts, Source: source, Action: action, Tool: "create_wiki_article", Slug: "some-slug", Title: "Some Title", Agent: "Test Agent"}
}

func TestActivityLogAppendReadRoundTrip(t *testing.T) {
	dataDir := t.TempDir()
	al, err := OpenActivityLog(dataDir)
	if err != nil {
		t.Fatalf("OpenActivityLog failed: %v", err)
	}
	t.Cleanup(func() { _ = al.Close() })

	now := time.Now()
	old := seedEvent("evt-old", now.Add(-2*time.Hour), "api", "edit")
	recent := seedEvent("evt-recent", now.Add(-5*time.Minute), "mcp", "create")
	if err := al.Append(old); err != nil {
		t.Fatalf("Append failed: %v", err)
	}
	if err := al.Append(recent); err != nil {
		t.Fatalf("Append failed: %v", err)
	}

	// Unfiltered read returns both in order
	events, err := ReadActivityLog(al.Path, time.Time{}, 0, "", "")
	if err != nil {
		t.Fatalf("ReadActivityLog failed: %v", err)
	}
	if len(events) != 2 || events[0].ID != "evt-old" || events[1].ID != "evt-recent" {
		t.Fatalf("unexpected events: %+v", events)
	}

	// since filter excludes older events
	sinceEvents, err := ReadActivityLog(al.Path, now.Add(-time.Hour), 0, "", "")
	if err != nil {
		t.Fatalf("ReadActivityLog since failed: %v", err)
	}
	if len(sinceEvents) != 1 || sinceEvents[0].ID != "evt-recent" {
		t.Errorf("expected only recent event, got %+v", sinceEvents)
	}

	// action and source filters
	apiOnly, _ := ReadActivityLog(al.Path, time.Time{}, 0, "", "api")
	if len(apiOnly) != 1 || apiOnly[0].ID != "evt-old" {
		t.Errorf("source filter failed: %+v", apiOnly)
	}
	createOnly, _ := ReadActivityLog(al.Path, time.Time{}, 0, "create", "")
	if len(createOnly) != 1 || createOnly[0].ID != "evt-recent" {
		t.Errorf("action filter failed: %+v", createOnly)
	}

	// limit keeps the newest matches
	limited, _ := ReadActivityLog(al.Path, time.Time{}, 1, "", "")
	if len(limited) != 1 || limited[0].ID != "evt-recent" {
		t.Errorf("limit failed: %+v", limited)
	}

	// Missing file returns nil events, no error
	noFile, err := ReadActivityLog(ActivityLogPath(t.TempDir()), time.Time{}, 0, "", "")
	if err != nil || noFile != nil {
		t.Errorf("expected nil events for missing file, got %v / %v", noFile, err)
	}
}

func TestActivityLogSkipsCorruptLines(t *testing.T) {
	dataDir := t.TempDir()
	path := ActivityLogPath(dataDir)

	good, _ := json.Marshal(seedEvent("evt-good", time.Now(), "mcp", "edit"))
	content := "not json at all\n" + string(good) + "\n{\"half\": \n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	events, err := ReadActivityLog(path, time.Time{}, 0, "", "")
	if err != nil {
		t.Fatalf("ReadActivityLog failed: %v", err)
	}
	if len(events) != 1 || events[0].ID != "evt-good" {
		t.Errorf("expected single good event, got %+v", events)
	}
}

func TestActivityLogRotation(t *testing.T) {
	dataDir := t.TempDir()

	origThreshold := activityLogRotateBytes
	activityLogRotateBytes = 100 // tiny threshold to trigger rotation
	t.Cleanup(func() { activityLogRotateBytes = origThreshold })

	// Round 1: write events, then reopen to trigger the first rotation.
	al, err := OpenActivityLog(dataDir)
	if err != nil {
		t.Fatalf("OpenActivityLog failed: %v", err)
	}
	for i := 0; i < 5; i++ {
		_ = al.Append(seedEvent("evt", time.Now(), "api", "edit"))
	}
	_ = al.Close()

	al2, err := OpenActivityLog(dataDir)
	if err != nil {
		t.Fatalf("reopen failed: %v", err)
	}
	if got := len(listActivityArchives(dataDir)); got != 1 {
		t.Errorf("expected 1 archive after first rotation, got %d", got)
	}
	if info, err := os.Stat(al2.Path); err != nil {
		t.Errorf("expected fresh empty log after rotation, got err=%v", err)
	} else if info.Size() != 0 {
		t.Errorf("expected fresh empty log after rotation, got size=%d", info.Size())
	}

	// Round 2: write more events and rotate again. A second rotation must NOT destroy the
	// first archive (the legacy one-deep rotation overwrote activity.jsonl.1 here).
	for i := 0; i < 5; i++ {
		_ = al2.Append(seedEvent("evt2", time.Now(), "api", "edit"))
	}
	_ = al2.Close()

	al3, err := OpenActivityLog(dataDir)
	if err != nil {
		t.Fatalf("second reopen failed: %v", err)
	}
	t.Cleanup(func() { _ = al3.Close() })

	if got := len(listActivityArchives(dataDir)); got != 2 {
		t.Errorf("expected 2 archives after second rotation (no history destroyed), got %d", got)
	}
}

func TestEventBusPersistHook(t *testing.T) {
	eb := NewEventBus()

	var persisted []LogEvent
	eb.SetPersist(func(ev LogEvent) { persisted = append(persisted, ev) })

	eb.PublishActivity("mcp", "create", "create_wiki_article", "slug-a", "Title A", "Agent")
	// Identical event inside the 2-second dedup window must not persist twice
	eb.PublishActivity("mcp", "create", "create_wiki_article", "slug-a", "Title A", "Agent")
	// A different event persists
	eb.PublishActivity("api", "edit", "", "slug-b", "Title B", "User")

	if len(persisted) != 2 {
		t.Fatalf("expected 2 persisted events (dedup suppresses the duplicate), got %d", len(persisted))
	}
	if persisted[0].Slug != "slug-a" || persisted[1].Slug != "slug-b" {
		t.Errorf("unexpected persisted events: %+v", persisted)
	}
}

func TestMCPGetRecentActivity(t *testing.T) {
	srv := newMCPServer(t)

	// Wire persistence like main.go does (primary process)
	al, err := OpenActivityLog(srv.Storage.DataDir)
	if err != nil {
		t.Fatalf("OpenActivityLog failed: %v", err)
	}
	t.Cleanup(func() { _ = al.Close() })
	srv.EventBus.SetPersist(func(ev LogEvent) { _ = al.Append(ev) })

	srv.EventBus.PublishActivity("mcp", "create", "create_wiki_article", "new-doc", "New Doc", "Claude")
	srv.EventBus.PublishActivity("api", "edit", "", "new-doc", "New Doc", "User")

	resp := toolCall(t, srv, `{"name":"get_recent_activity","arguments":{"since":"1h"}}`)
	if resp.IsError {
		t.Fatalf("get_recent_activity failed: %s", resp.Content[0].Text)
	}
	text := resp.Content[0].Text
	if !strings.Contains(text, "[mcp/create] create_wiki_article → 'New Doc' (new-doc) by Claude") {
		t.Errorf("missing mcp event line: %s", text)
	}
	if !strings.Contains(text, "[api/edit] web-ui → 'New Doc' (new-doc) by User") {
		t.Errorf("missing api event line: %s", text)
	}

	// Source filter
	apiOnly := toolCall(t, srv, `{"name":"get_recent_activity","arguments":{"source":"api"}}`)
	if strings.Contains(apiOnly.Content[0].Text, "[mcp/") {
		t.Errorf("source filter leaked mcp events: %s", apiOnly.Content[0].Text)
	}

	// Invalid since value
	bad := toolCall(t, srv, `{"name":"get_recent_activity","arguments":{"since":"yesterday"}}`)
	if !bad.IsError {
		t.Error("expected error for invalid since value")
	}

	// RFC3339 since accepted
	rfc := toolCall(t, srv, `{"name":"get_recent_activity","arguments":{"since":"2020-01-01T00:00:00Z"}}`)
	if rfc.IsError {
		t.Errorf("RFC3339 since rejected: %s", rfc.Content[0].Text)
	}
}

func TestMCPGetRecentActivityFallsBackToRing(t *testing.T) {
	srv := newMCPServer(t)

	// No persistence wired and no file on disk: tool falls back to the in-memory ring
	srv.EventBus.PublishActivity("mcp", "read", "read_article", "doc", "Doc", "Claude")

	resp := toolCall(t, srv, `{"name":"get_recent_activity","arguments":{}}`)
	if resp.IsError {
		t.Fatalf("fallback failed: %s", resp.Content[0].Text)
	}
	if !strings.Contains(resp.Content[0].Text, "read_article → 'Doc' (doc)") {
		t.Errorf("expected ring-buffer event in fallback output: %s", resp.Content[0].Text)
	}
}
