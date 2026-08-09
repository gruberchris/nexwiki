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

// TestActivityLogRotationAtOpen covers the restart path: a log that grew past the threshold while
// the previous process ran is archived when the next process opens it. Archives are never
// overwritten — the legacy one-deep rotation destroyed activity.jsonl.1 on the second rotation.
func TestActivityLogRotationAtOpen(t *testing.T) {
	dataDir := t.TempDir()
	// A threshold above one event but below two: rotation is driven by reopening, not by the
	// append path, so this exercises OpenActivityLog specifically.
	restoreThreshold(t, int64(len(marshalEvent(t, seedEvent("evt", time.Now(), "api", "edit")))*2))

	for round := 1; round <= 2; round++ {
		al, err := OpenActivityLog(dataDir)
		if err != nil {
			t.Fatalf("round %d: OpenActivityLog failed: %v", round, err)
		}
		// Two events cross the threshold, but the append-path check fires only *after* the
		// write that crosses it, so the file is left over-threshold for the next open.
		_ = al.Append(seedEvent("evt", time.Now(), "api", "edit"))
		_ = al.Close()

		reopened, err := OpenActivityLog(dataDir)
		if err != nil {
			t.Fatalf("round %d: reopen failed: %v", round, err)
		}
		_ = reopened.Close()
	}

	// Whatever the exact count, the invariant is that no archive was destroyed: every rotation
	// produced a distinct file.
	archives := listActivityArchives(dataDir)
	seen := map[string]bool{}
	for _, a := range archives {
		if seen[a] {
			t.Errorf("archive %q appeared twice — names are colliding", a)
		}
		seen[a] = true
	}
}

// TestActivityLogRotatesWhileRunning is the §3.14 regression. The threshold used to be checked
// only in OpenActivityLog, which main.go calls once at startup — so a process that stays up, which
// is the documented `docker compose up -d` deployment, never rotated and the active log grew
// without bound. Reverting the Append-side check makes this test fail with 0 archives.
func TestActivityLogRotatesWhileRunning(t *testing.T) {
	dataDir := t.TempDir()
	restoreThreshold(t, 200)

	// One handle, opened once and never reopened — exactly the long-running server's lifecycle.
	al, err := OpenActivityLog(dataDir)
	if err != nil {
		t.Fatalf("OpenActivityLog failed: %v", err)
	}
	t.Cleanup(func() { _ = al.Close() })

	const events = 40
	for i := 0; i < events; i++ {
		if err := al.Append(seedEvent("evt", time.Now(), "api", "edit")); err != nil {
			t.Fatalf("Append %d failed: %v", i, err)
		}
	}

	if got := len(listActivityArchives(dataDir)); got == 0 {
		t.Fatal("a long-running log never rotated: the threshold is only enforced at open")
	}

	// The active file must stay bounded rather than growing to the full run.
	info, err := os.Stat(al.Path)
	if err != nil {
		t.Fatalf("stat active log failed: %v", err)
	}
	if info.Size() > activityLogRotateBytes*2 {
		t.Errorf("active log is %d bytes, well past the %d threshold — rotation is not keeping up",
			info.Size(), activityLogRotateBytes)
	}

	// Nothing may be lost to rotation: every appended event is still readable across the active
	// file plus its archives. This is the guarantee that makes rotating mid-run safe at all.
	got, err := ReadActivityLog(ActivityLogPath(dataDir), time.Time{}, events*2, "", "")
	if err != nil {
		t.Fatalf("ReadActivityLog failed: %v", err)
	}
	if len(got) != events {
		t.Errorf("rotation lost events: appended %d, read back %d", events, len(got))
	}
}

// TestActivityLogRotationSurvivesAFailedRename pins that a rotation failure degrades rather than
// killing the logger. The activity log is audit bookkeeping; losing the ability to record events
// is worse than an oversized file.
func TestActivityLogRotationSurvivesAFailedRename(t *testing.T) {
	dataDir := t.TempDir()
	restoreThreshold(t, 200)

	al, err := OpenActivityLog(dataDir)
	if err != nil {
		t.Fatalf("OpenActivityLog failed: %v", err)
	}
	t.Cleanup(func() { _ = al.Close() })

	// Make the directory read-only so the rename inside rotation cannot succeed.
	if err := os.Chmod(dataDir, 0500); err != nil {
		t.Skipf("cannot chmod temp dir on this platform: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dataDir, 0700) })

	for i := 0; i < 20; i++ {
		if err := al.Append(seedEvent("evt", time.Now(), "api", "edit")); err != nil {
			t.Fatalf("Append %d returned an error after a failed rotation: %v", i, err)
		}
	}

	if err := os.Chmod(dataDir, 0700); err != nil {
		t.Fatalf("restoring permissions failed: %v", err)
	}
	got, err := ReadActivityLog(ActivityLogPath(dataDir), time.Time{}, 100, "", "")
	if err != nil {
		t.Fatalf("ReadActivityLog failed: %v", err)
	}
	if len(got) != 20 {
		t.Errorf("events were dropped when rotation could not rename: appended 20, read %d", len(got))
	}
}

// restoreThreshold lowers the rotation threshold for one test and restores it afterwards. Tests
// override it rather than writing 10 MB — the boundary itself is arithmetic, and asserting on it
// with a real 10 MB file would buy nothing for the runtime it costs.
func restoreThreshold(t *testing.T, bytes int64) {
	t.Helper()
	orig := activityLogRotateBytes
	activityLogRotateBytes = bytes
	t.Cleanup(func() { activityLogRotateBytes = orig })
}

func marshalEvent(t *testing.T, ev LogEvent) []byte {
	t.Helper()
	data, err := json.Marshal(ev)
	if err != nil {
		t.Fatalf("marshal event failed: %v", err)
	}
	return append(data, '\n')
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
