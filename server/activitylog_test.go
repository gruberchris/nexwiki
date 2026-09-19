package server

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
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

	// Rotations this close together collide on their UTC second, so this run writes .N fallbacks
	// too. Every name it wrote must pass the exact archive matcher, or reads and retention would
	// never see that archive.
	entries, err := os.ReadDir(dataDir)
	if err != nil {
		t.Fatalf("ReadDir failed: %v", err)
	}
	for _, e := range entries {
		if e.Name() == ActivityLogFilename {
			continue
		}
		if _, ok := parseActivityArchive(dataDir, e); !ok {
			t.Errorf("rotation wrote %s, which the archive matcher rejects", e.Name())
		}
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

// archiveRotation is one archive in a fixture history: the name nextArchivePath gave it and when
// the rotation that produced it ran.
type archiveRotation struct {
	name    string
	rotated time.Time
}

// realisticArchiveHistory is a rotation history exactly as nextArchivePath produces it, oldest
// first. A .N fallback only exists because a rotation found the timestamped name for its UTC second
// already taken, so each one sits just after the timestamped archive of that second. And because
// nextArchivePath takes the lowest free N, the numbers stop meaning creation order as soon as any
// fallback is deleted: here .9 and .10 came from two more rotations inside one second while .1–.8
// existed, and .1 and .2 were handed out again after those were removed. Lexically, every .N name
// also sorts after every activity-<UTC> name, so neither the names nor their reverse order is
// chronological.
func realisticArchiveHistory() []archiveRotation {
	at := func(day, ms int) time.Time {
		return time.Date(2026, time.March, day, 9, 30, 15, 0, time.UTC).Add(time.Duration(ms) * time.Millisecond)
	}
	return []archiveRotation{
		{"activity-2026-03-01T09-30-15Z.jsonl", at(1, 300)},
		{"activity.jsonl.9", at(1, 600)},
		{"activity.jsonl.10", at(1, 900)},
		{"activity-2026-03-02T09-30-15Z.jsonl", at(2, 300)},
		{"activity.jsonl.1", at(2, 700)},
		{"activity-2026-03-03T09-30-15Z.jsonl", at(3, 300)},
		{"activity.jsonl.2", at(3, 700)},
		{"activity-2026-03-04T09-30-15Z.jsonl", at(4, 300)},
		{"activity-2026-03-05T09-30-15Z.jsonl", at(5, 300)},
	}
}

// writeArchiveHistory lays the history out on disk, plus an active log newer than all of it, and
// returns every event oldest first. Each archive holds two events written shortly before its
// rotation, and its modification time is the last of those writes, which a rename preserves.
// coarseMtimes truncates modification times to whole seconds, as filesystems with one-second
// timestamp resolution record them.
func writeArchiveHistory(t *testing.T, dataDir string, history []archiveRotation, coarseMtimes bool) []LogEvent {
	t.Helper()
	var all []LogEvent
	write := func(name string, events []LogEvent) {
		var content []byte
		for _, ev := range events {
			content = append(content, marshalEvent(t, ev)...)
		}
		if err := os.WriteFile(filepath.Join(dataDir, name), content, 0644); err != nil {
			t.Fatalf("writing %s failed: %v", name, err)
		}
		all = append(all, events...)
	}

	for _, r := range history {
		lastWrite := r.rotated.Add(-100 * time.Millisecond)
		write(r.name, []LogEvent{
			seedEvent(r.name+"#1", r.rotated.Add(-150*time.Millisecond), "api", "edit"),
			seedEvent(r.name+"#2", lastWrite, "api", "edit"),
		})
		mtime := lastWrite
		if coarseMtimes {
			mtime = mtime.Truncate(time.Second)
		}
		if err := os.Chtimes(filepath.Join(dataDir, r.name), mtime, mtime); err != nil {
			t.Fatalf("setting mtime on %s failed: %v", r.name, err)
		}
	}
	write(ActivityLogFilename, []LogEvent{
		seedEvent("active#1", history[len(history)-1].rotated.Add(time.Hour), "api", "edit"),
	})
	return all
}

func eventIDs(events []LogEvent) []string {
	ids := make([]string, 0, len(events))
	for _, ev := range events {
		ids = append(ids, ev.ID)
	}
	return ids
}

// newestFirstNames is the archive order listActivityArchives must produce for a history.
func newestFirstNames(history []archiveRotation) []string {
	var names []string
	for i := len(history) - 1; i >= 0; i-- {
		names = append(names, history[i].name)
	}
	return names
}

// listedArchiveNames is listActivityArchives reduced to base names, for comparing against fixtures.
func listedArchiveNames(dataDir string) []string {
	var names []string
	for _, p := range listActivityArchives(dataDir) {
		names = append(names, filepath.Base(p))
	}
	return names
}

// TestActivityArchivePruningKeepsNewestByChronology pins that the retention cap removes the
// chronologically oldest archives. Ordering archives by reversed name put every .N fallback ahead
// of every timestamped archive (and .9 ahead of .10), so a cap kept stale fallbacks and deleted the
// newest timestamped history instead.
func TestActivityArchivePruningKeepsNewestByChronology(t *testing.T) {
	history := realisticArchiveHistory()
	newestFirst := newestFirstNames(history)

	for _, coarse := range []bool{false, true} {
		t.Run(fmt.Sprintf("coarseMtimes=%v", coarse), func(t *testing.T) {
			listed := listedArchiveNames

			dataDir := t.TempDir()
			writeArchiveHistory(t, dataDir, history, coarse)
			if got := listed(dataDir); !slices.Equal(got, newestFirst) {
				t.Errorf("archives are not listed newest first:\n got  %v\n want %v", got, newestFirst)
			}

			for keep := 0; keep <= len(history)+1; keep++ {
				dataDir := t.TempDir()
				writeArchiveHistory(t, dataDir, history, coarse)
				t.Setenv("NEXWIKI_ACTIVITY_MAX_ARCHIVES", strconv.Itoa(keep))

				pruneActivityArchives(dataDir)

				want := newestFirst[:min(keep, len(newestFirst))]
				if got := listed(dataDir); !slices.Equal(got, want) {
					t.Errorf("cap %d kept the wrong archives:\n got  %v\n want %v", keep, got, want)
				}
				if _, err := os.Stat(ActivityLogPath(dataDir)); err != nil {
					t.Errorf("cap %d touched the active log: %v", keep, err)
				}
			}
		})
	}
}

// TestActivityLogReadsArchivesInChronologicalOrder pins the read side of the same ordering. Both
// readers walk archives newest first and stop early — at the first file reaching before `since`,
// or once `limit` matches are held — so visiting a stale fallback too soon ends the walk before the
// newer archives are read, and those events silently vanish from the result.
func TestActivityLogReadsArchivesInChronologicalOrder(t *testing.T) {
	history := realisticArchiveHistory()
	// The start of the second in which the day-3 timestamped rotation ran: the since bound and the
	// older-history cursor alike.
	bound := time.Date(2026, time.March, 3, 9, 30, 15, 0, time.UTC)

	for _, coarse := range []bool{false, true} {
		t.Run(fmt.Sprintf("coarseMtimes=%v", coarse), func(t *testing.T) {
			dataDir := t.TempDir()
			all := writeArchiveHistory(t, dataDir, history, coarse)
			path := ActivityLogPath(dataDir)

			// The oracle for each query is computed from the full event list, independent of files.
			var wantSince []LogEvent
			for _, ev := range all {
				if !ev.Timestamp.Before(bound) {
					wantSince = append(wantSince, ev)
				}
			}
			gotSince, err := ReadActivityLog(path, bound, 0, "", "")
			if err != nil {
				t.Fatalf("ReadActivityLog since failed: %v", err)
			}
			if !slices.Equal(eventIDs(gotSince), eventIDs(wantSince)) {
				t.Errorf("since read returned the wrong events:\n got  %v\n want %v", eventIDs(gotSince), eventIDs(wantSince))
			}

			const limit = 4
			wantLimit := all[len(all)-limit:]
			gotLimit, err := ReadActivityLog(path, time.Time{}, limit, "", "")
			if err != nil {
				t.Fatalf("ReadActivityLog limit failed: %v", err)
			}
			if !slices.Equal(eventIDs(gotLimit), eventIDs(wantLimit)) {
				t.Errorf("limit read did not return the newest events:\n got  %v\n want %v", eventIDs(gotLimit), eventIDs(wantLimit))
			}

			// The Activity Drawer's "Load older history" page: the next events older than a cursor.
			var wantBefore []LogEvent
			for i := len(all) - 1; i >= 0 && len(wantBefore) < 3; i-- {
				if all[i].Timestamp.Before(bound) {
					wantBefore = append(wantBefore, all[i])
				}
			}
			gotBefore, err := ReadActivityLogBefore(path, bound, 3, "", "")
			if err != nil {
				t.Fatalf("ReadActivityLogBefore failed: %v", err)
			}
			if !slices.Equal(eventIDs(gotBefore), eventIDs(wantBefore)) {
				t.Errorf("older-history page skipped events:\n got  %v\n want %v", eventIDs(gotBefore), eventIDs(wantBefore))
			}
		})
	}
}

// parseArchiveFile runs parseActivityArchive on one file, as listActivityArchives would see it.
func parseArchiveFile(t *testing.T, path string) (activityArchive, bool) {
	t.Helper()
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatalf("Lstat %s failed: %v", path, err)
	}
	return parseActivityArchive(filepath.Dir(path), fs.FileInfoToDirEntry(info))
}

// TestActivityArchiveNamesRoundTrip pins that every name nextArchivePath writes is one the archive
// matcher accepts, and parses back to what it encodes. The matcher is exact, so a name rotation
// wrote but the matcher rejected would be invisible to reads and retention alike.
func TestActivityArchiveNamesRoundTrip(t *testing.T) {
	t.Run("timestamped", func(t *testing.T) {
		// The ordering tests would still pass if the stamp did not parse, because their mtimes agree
		// with their names. So this archive gets a misleading mtime, as a copy or restore can leave,
		// and only its name can put it in the right second.
		dataDir := t.TempDir()
		notBefore := time.Now().UTC().Truncate(time.Second)
		path := nextArchivePath(dataDir)
		notAfter := time.Now().UTC()
		if err := os.WriteFile(path, nil, 0644); err != nil {
			t.Fatalf("WriteFile failed: %v", err)
		}
		copied := time.Date(2001, time.January, 1, 0, 0, 0, 0, time.UTC)
		if err := os.Chtimes(path, copied, copied); err != nil {
			t.Fatalf("Chtimes failed: %v", err)
		}

		a, ok := parseArchiveFile(t, path)
		if !ok || a.n != 0 {
			t.Fatalf("%s was not recognised as a timestamped archive: %+v", filepath.Base(path), a)
		}
		if a.at.Before(notBefore) || a.at.After(notAfter) {
			t.Errorf("%s parsed to %v, outside the rotation window [%v, %v]", filepath.Base(path), a.at, notBefore, notAfter)
		}
	})

	t.Run("fallbacks", func(t *testing.T) {
		// Occupy the timestamped name for every second the test could plausibly run in, so each
		// call has to fall back. Twelve rotations reach the multi-digit numbers.
		dataDir := t.TempDir()
		now := time.Now().UTC()
		for s := -1; s <= 60; s++ {
			stamp := now.Add(time.Duration(s) * time.Second).Format(activityArchiveStampLayout)
			if err := os.WriteFile(filepath.Join(dataDir, activityArchivePrefix+stamp+".jsonl"), nil, 0644); err != nil {
				t.Fatalf("WriteFile failed: %v", err)
			}
		}
		for want := 1; want <= 12; want++ {
			path := nextArchivePath(dataDir)
			if got := filepath.Base(path); got != ActivityLogFilename+"."+strconv.Itoa(want) {
				t.Fatalf("rotation %d wrote %s, want %s.%d", want, got, ActivityLogFilename, want)
			}
			if err := os.WriteFile(path, nil, 0644); err != nil {
				t.Fatalf("WriteFile failed: %v", err)
			}
			if a, ok := parseArchiveFile(t, path); !ok || a.n != want {
				t.Errorf("%s was not recognised as fallback %d: ok=%v %+v", filepath.Base(path), want, ok, a)
			}
		}
	})
}

// TestActivityArchiveMatchingIgnoresNearMisses pins that only the exact names rotation writes are
// archives. Pruning deletes whatever the matcher accepts, so a prefix match let
// NEXWIKI_ACTIVITY_MAX_ARCHIVES delete a user's own activity-backup.jsonl, and let reads mix its
// events into the activity history.
func TestActivityArchiveMatchingIgnoresNearMisses(t *testing.T) {
	history := realisticArchiveHistory()
	newestFirst := newestFirstNames(history)
	nearMisses := []string{
		"activity-backup.jsonl",
		"activity-.jsonl",
		"activity-2026-03-06T9-30-15Z.jsonl",    // one-digit hour: time.Parse accepts it, Format never writes it
		"activity-2026-03-06T09-30-15.5Z.jsonl", // fractional seconds: likewise
		"activity-2026-02-30T09-30-15Z.jsonl",   // no such date
		"activity-2026-03-06T09-30-15Z.jsonl.gz",
		"activity.jsonl.bak",
		"activity.jsonl.01", // strconv.Atoi accepts leading zeros, a sign, and zero; Itoa writes none
		"activity.jsonl.+3",
		"activity.jsonl.-3",
		"activity.jsonl.0",
		"activity.jsonl.3.gz",
		"activity.jsonl.99999999999999999999",
	}

	// setup lays out the real history plus every near-miss. The near-misses hold the newest events
	// of all, so a matcher that let one through would surface it in every read below.
	setup := func(t *testing.T) (string, []LogEvent) {
		t.Helper()
		dataDir := t.TempDir()
		all := writeArchiveHistory(t, dataDir, history, false)
		newest := history[len(history)-1].rotated.Add(2 * time.Hour)
		for i, name := range nearMisses {
			ev := seedEvent("near-miss:"+name, newest.Add(time.Duration(i)*time.Millisecond), "api", "edit")
			if err := os.WriteFile(filepath.Join(dataDir, name), marshalEvent(t, ev), 0644); err != nil {
				t.Fatalf("writing %s failed: %v", name, err)
			}
		}
		return dataDir, all
	}

	t.Run("listing", func(t *testing.T) {
		dataDir, _ := setup(t)
		if got := listedArchiveNames(dataDir); !slices.Equal(got, newestFirst) {
			t.Errorf("near-misses were listed as archives:\n got  %v\n want %v", got, newestFirst)
		}
	})

	for _, keep := range []int{0, 2} {
		t.Run(fmt.Sprintf("pruneCap=%d", keep), func(t *testing.T) {
			dataDir, _ := setup(t)
			t.Setenv("NEXWIKI_ACTIVITY_MAX_ARCHIVES", strconv.Itoa(keep))

			pruneActivityArchives(dataDir)

			for _, name := range nearMisses {
				if _, err := os.Stat(filepath.Join(dataDir, name)); err != nil {
					t.Errorf("cap %d deleted %s, which is not an archive: %v", keep, name, err)
				}
			}
			if got, want := listedArchiveNames(dataDir), newestFirst[:keep]; !slices.Equal(got, want) {
				t.Errorf("cap %d kept the wrong archives:\n got  %v\n want %v", keep, got, want)
			}
		})
	}

	t.Run("reads", func(t *testing.T) {
		dataDir, all := setup(t)
		path := ActivityLogPath(dataDir)

		everything, err := ReadActivityLog(path, time.Time{}, 0, "", "")
		if err != nil {
			t.Fatalf("ReadActivityLog failed: %v", err)
		}
		if !slices.Equal(eventIDs(everything), eventIDs(all)) {
			t.Errorf("unbounded read returned events from near-misses:\n got  %v\n want %v", eventIDs(everything), eventIDs(all))
		}

		since := history[len(history)-1].rotated.Add(-time.Second)
		recent, err := ReadActivityLog(path, since, 4, "", "")
		if err != nil {
			t.Fatalf("ReadActivityLog since failed: %v", err)
		}
		if want := all[len(all)-3:]; !slices.Equal(eventIDs(recent), eventIDs(want)) {
			t.Errorf("since read returned events from near-misses:\n got  %v\n want %v", eventIDs(recent), eventIDs(want))
		}

		page, err := ReadActivityLogBefore(path, time.Time{}, 3, "", "")
		if err != nil {
			t.Fatalf("ReadActivityLogBefore failed: %v", err)
		}
		want := slices.Clone(all[len(all)-3:])
		slices.Reverse(want)
		if !slices.Equal(eventIDs(page), eventIDs(want)) {
			t.Errorf("older-history page returned events from near-misses:\n got  %v\n want %v", eventIDs(page), eventIDs(want))
		}
	})
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

	resp := toolCall(t, srv, `{"name":"get_wiki_overview","arguments":{"since":"1h"}}`)
	if resp.IsError {
		t.Fatalf("get_wiki_overview failed: %s", resp.Content[0].Text)
	}
	text := resp.Content[0].Text
	if !strings.Contains(text, "[mcp/create] create_wiki_article → 'New Doc' (new-doc)") {
		t.Errorf("missing mcp event line: %s", text)
	}
	if !strings.Contains(text, "[api/edit] web-ui → 'New Doc' (new-doc)") {
		t.Errorf("missing api event line: %s", text)
	}

	// Invalid since value
	bad := toolCall(t, srv, `{"name":"get_wiki_overview","arguments":{"since":"yesterday"}}`)
	if !bad.IsError {
		t.Error("expected error for invalid since value")
	}

	// RFC3339 since accepted
	rfc := toolCall(t, srv, `{"name":"get_wiki_overview","arguments":{"since":"2020-01-01T00:00:00Z"}}`)
	if rfc.IsError {
		t.Errorf("RFC3339 since rejected: %s", rfc.Content[0].Text)
	}
}

func TestMCPGetRecentActivityFallsBackToRing(t *testing.T) {
	srv := newMCPServer(t)

	// No persistence wired and no file on disk: tool falls back to the in-memory ring
	srv.EventBus.PublishActivity("mcp", "read", "read_article", "doc", "Doc", "Claude")

	resp := toolCall(t, srv, `{"name":"get_wiki_overview","arguments":{}}`)
	if resp.IsError {
		t.Fatalf("fallback failed: %s", resp.Content[0].Text)
	}
	if !strings.Contains(resp.Content[0].Text, "read_article → 'Doc' (doc)") {
		t.Errorf("expected ring-buffer event in fallback output: %s", resp.Content[0].Text)
	}
}
