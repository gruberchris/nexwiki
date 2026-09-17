package server

import (
	"bufio"
	"cmp"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ActivityLogFilename is the durable activity log file inside the data directory.
const ActivityLogFilename = "activity.jsonl"

// activityArchivePrefix names rotated archive files: activity-<UTC>.jsonl (timestamped) or
// activity.jsonl.N (monotonic fallback). Archives are never overwritten, so history is retained.
const activityArchivePrefix = "activity-"

// activityLogRotateBytes triggers rotation: the active file is archived and a fresh one started
// once the log exceeds this size. Checked both at open and after every append. Variable for
// testability — tests lower it rather than writing 10 MB.
var activityLogRotateBytes int64 = 10 * 1024 * 1024 // 10 MB

// ActivityLog appends LogEvents durably as JSON Lines to the data directory.
type ActivityLog struct {
	mu   sync.Mutex
	file *os.File
	Path string

	// dataDir is retained so Append can rotate without re-deriving it from Path.
	dataDir string
	// size tracks the active file's byte count so the rotation check costs no syscall. Seeded
	// from the file at open, incremented on every append, reset to zero on rotation.
	size int64
}

// ActivityLogPath returns the canonical activity log location for a data directory.
func ActivityLogPath(dataDir string) string {
	return filepath.Join(dataDir, ActivityLogFilename)
}

// OpenActivityLog opens (creating if needed) the append-only activity log. If the current file
// has grown past the size threshold, it is archived under a non-destructive timestamped name so no
// history is lost (the legacy one-deep rotation overwrote and destroyed older archives).
func OpenActivityLog(dataDir string) (*ActivityLog, error) {
	path := ActivityLogPath(dataDir)

	if info, err := os.Stat(path); err == nil && info.Size() > activityLogRotateBytes {
		if err := os.Rename(path, nextArchivePath(dataDir)); err != nil {
			return nil, fmt.Errorf("failed to rotate activity log: %w", err)
		}
		pruneActivityArchives(dataDir)
	}

	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return nil, fmt.Errorf("failed to open activity log: %w", err)
	}

	// Seed the running size from whatever survived the rotation check above, so appends pick up
	// where the previous process left off rather than restarting the count at zero.
	var size int64
	if info, err := file.Stat(); err == nil {
		size = info.Size()
	}

	return &ActivityLog{file: file, Path: path, dataDir: dataDir, size: size}, nil
}

// rotateLocked archives the active file and starts a fresh one. The caller must hold al.mu.
//
// A rotation failure must not take the logger down with it: the activity log is audit
// bookkeeping, and losing the ability to record events is worse than an oversized file. Every
// failure path therefore keeps the current handle and reports, so appends continue.
func (al *ActivityLog) rotateLocked() {
	if err := al.file.Close(); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "Warning: closing activity log for rotation failed: %v\n", err)
	}

	reopen := func() {
		file, err := os.OpenFile(al.Path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
		if err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "Warning: reopening activity log after rotation failed: %v\n", err)
			return
		}
		al.file = file
		var size int64
		if info, err := file.Stat(); err == nil {
			size = info.Size()
		}
		al.size = size
	}

	if err := os.Rename(al.Path, nextArchivePath(al.dataDir)); err != nil {
		// The rename failed, so the active file is still in place; reopening it resumes
		// appending to the same (oversized) file rather than dropping events on the floor.
		_, _ = fmt.Fprintf(os.Stderr, "Warning: rotating activity log failed: %v\n", err)
		reopen()
		return
	}

	pruneActivityArchives(al.dataDir)
	reopen()
}

// activityArchiveStampLayout is the UTC stamp in a timestamped archive name. Colons are not
// filesystem-safe on all platforms, so the time fields are dash-separated.
const activityArchiveStampLayout = "2006-01-02T15-04-05Z"

// nextArchivePath returns a non-colliding archive path: a UTC-timestamped name, falling back to a
// monotonic activity.jsonl.N suffix if a same-second archive already exists.
func nextArchivePath(dataDir string) string {
	stamp := time.Now().UTC().Format(activityArchiveStampLayout)
	candidate := filepath.Join(dataDir, activityArchivePrefix+stamp+".jsonl")
	if _, err := os.Stat(candidate); os.IsNotExist(err) {
		return candidate
	}
	for n := 1; ; n++ {
		fallback := filepath.Join(dataDir, ActivityLogFilename+"."+strconv.Itoa(n))
		if _, err := os.Stat(fallback); os.IsNotExist(err) {
			return fallback
		}
	}
}

// activityArchive is one rotated archive and its place in the rotation history.
type activityArchive struct {
	path string
	// at places the archive in time. A timestamped name records its rotation second, so that is
	// used. An activity.jsonl.N name records no time, and N is no guide either: nextArchivePath
	// takes the lowest free N, so after any archive is deleted a newer fallback can take a lower
	// number than an older one. The modification time stands in instead. It is the last append
	// before rotation, which the rename preserves, and that append came after the same-second
	// archive it collided with was rotated out, so it lands between the right neighbours. A copy
	// that rewrote mtimes can misplace a fallback, but never a timestamped archive.
	at time.Time
	// n is the N of an activity.jsonl.N name, zero otherwise.
	n int
}

// compare orders archives oldest first.
func (a activityArchive) compare(b activityArchive) int {
	if c := a.at.Compare(b.at); c != 0 {
		return c
	}
	// Equal times happen when modification times are whole seconds. A fallback exists only
	// because the timestamped name for its second was already taken, so the timestamped archive
	// (n zero; fallbacks start at 1) is the older. Between fallbacks, with no deletion in between,
	// the lowest free N was taken first. Names are matched exactly, so no two archives share both
	// a stamp and an N, and this decides every pair.
	return cmp.Compare(a.n, b.n)
}

// listActivityArchives returns the archive file paths in newest-first order. Pruning and both
// readers depend on this being chronological: pruning deletes from the tail, and the readers stop
// walking once the files they have read cover the query. Name order is not chronological —
// every activity.jsonl.N sorts after every activity-<UTC>.jsonl, and .10 before .9.
func listActivityArchives(dataDir string) []string {
	entries, err := os.ReadDir(dataDir)
	if err != nil {
		return nil
	}
	var archives []activityArchive
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if a, ok := parseActivityArchive(dataDir, e); ok {
			archives = append(archives, a)
		}
	}
	slices.SortFunc(archives, func(a, b activityArchive) int { return b.compare(a) })

	paths := make([]string, 0, len(archives))
	for _, a := range archives {
		paths = append(paths, a.path)
	}
	return paths
}

// parseActivityArchive reports whether a directory entry is a rotated archive and where it falls
// in time. Only names nextArchivePath can write qualify, exactly: pruning deletes whatever this
// accepts, so a prefix match would let the retention cap delete a user's own activity-backup.jsonl
// and let reads mix its events into the history. The one-deep rotation that predated timestamped
// names only ever wrote activity.jsonl.1, which still qualifies. Only fallback names cost a stat;
// timestamped names carry their own time.
func parseActivityArchive(dataDir string, e os.DirEntry) (activityArchive, bool) {
	name := e.Name()
	path := filepath.Join(dataDir, name)

	if rest, ok := strings.CutPrefix(name, activityArchivePrefix); ok {
		stamp, isJSONL := strings.CutSuffix(rest, ".jsonl")
		if !isJSONL {
			return activityArchive{}, false
		}
		// Parse alone also accepts stamps Format never writes, such as a one-digit hour or
		// fractional seconds, so the stamp must survive the round trip unchanged.
		at, err := time.Parse(activityArchiveStampLayout, stamp)
		if err != nil || at.Format(activityArchiveStampLayout) != stamp {
			return activityArchive{}, false
		}
		return activityArchive{path: path, at: at}, true
	}

	if suffix, ok := strings.CutPrefix(name, ActivityLogFilename+"."); ok {
		// Likewise Atoi accepts a sign, leading zeros and zero; Itoa of a count from 1 writes none.
		n, err := strconv.Atoi(suffix)
		if err != nil || n < 1 || strconv.Itoa(n) != suffix {
			return activityArchive{}, false
		}
		info, err := e.Info()
		if err != nil {
			return activityArchive{}, false // removed since the directory was read
		}
		return activityArchive{path: path, at: info.ModTime(), n: n}, true
	}

	return activityArchive{}, false
}

// pruneActivityArchives enforces an optional retention cap (NEXWIKI_ACTIVITY_MAX_ARCHIVES).
// Default is unlimited (keep all history). The oldest archives are removed first.
func pruneActivityArchives(dataDir string) {
	capStr := strings.TrimSpace(os.Getenv("NEXWIKI_ACTIVITY_MAX_ARCHIVES"))
	if capStr == "" {
		return
	}
	maxArchives, err := strconv.Atoi(capStr)
	if err != nil || maxArchives < 0 {
		return
	}
	archives := listActivityArchives(dataDir) // newest first
	for i := maxArchives; i < len(archives); i++ {
		_ = os.Remove(archives[i])
	}
}

// Append durably writes one event as a single JSON line.
func (al *ActivityLog) Append(ev LogEvent) error {
	data, err := json.Marshal(ev)
	if err != nil {
		return err
	}

	al.mu.Lock()
	defer al.mu.Unlock()

	n, err := al.file.Write(append(data, '\n'))
	al.size += int64(n)
	if err != nil {
		return err
	}

	// Rotate here, not only at open. The threshold used to be checked exclusively in
	// OpenActivityLog, which main.go calls once at startup — so a process that stays up (the
	// documented `docker compose up -d` deployment) never rotated at all and the log grew without
	// bound until the next restart. That also fed back into reads: ReadActivityLog stops early
	// across *archives*, but always parses the active file end to end, so an unbounded active log
	// made get_recent_activity progressively slower on every call.
	if al.size > activityLogRotateBytes {
		al.rotateLocked()
	}
	return nil
}

// Close releases the underlying file handle.
func (al *ActivityLog) Close() error {
	al.mu.Lock()
	defer al.mu.Unlock()
	return al.file.Close()
}

// ActivityFilter selects which events a read returns.
//
// This exists because ReadActivityLog and ReadActivityLogBefore already took five positional
// filter arguments each, and the provenance join needed a sixth (slug). A struct keeps the
// existing call sites untouched and stops the next filter from making the signature worse.
type ActivityFilter struct {
	// Since bounds how far back to walk. Zero means unbounded.
	Since time.Time
	// Limit caps the returned events. Zero or negative means unlimited.
	Limit int
	// Action, Source and Slug are exact-match filters; empty means "any".
	Action string
	Source string
	Slug   string
}

func (f ActivityFilter) matches(ev LogEvent) bool {
	if !f.Since.IsZero() && ev.Timestamp.Before(f.Since) {
		return false
	}
	if f.Action != "" && ev.Action != f.Action {
		return false
	}
	if f.Source != "" && ev.Source != f.Source {
		return false
	}
	if f.Slug != "" && ev.Slug != f.Slug {
		return false
	}
	return true
}

// ReadActivityLogFiltered is the general form of ReadActivityLog, taking a filter struct.
func ReadActivityLogFiltered(path string, filter ActivityFilter) ([]LogEvent, error) {
	dataDir := filepath.Dir(path)
	files := append([]string{path}, listActivityArchives(dataDir)...)

	var collected []LogEvent
	anyFileExists := false
	for _, fp := range files {
		evs, fileEarliest, ok, err := readEventFile(fp)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue // file does not exist
		}
		anyFileExists = true
		for _, ev := range evs {
			if filter.matches(ev) {
				collected = append(collected, ev)
			}
		}
		// Stop walking older archives once the window is fully covered:
		// - a since bound is satisfied once this file reaches before `since`, or
		// - without a since bound, once we already hold at least `limit` matches.
		if !filter.Since.IsZero() {
			if !fileEarliest.IsZero() && fileEarliest.Before(filter.Since) {
				break
			}
		} else if filter.Limit > 0 && len(collected) >= filter.Limit {
			break
		}
	}

	if !anyFileExists {
		return nil, nil
	}

	sort.Slice(collected, func(i, j int) bool {
		return collected[i].Timestamp.Before(collected[j].Timestamp)
	})

	if filter.Limit > 0 && len(collected) > filter.Limit {
		collected = collected[len(collected)-filter.Limit:]
	}
	return collected, nil
}

// ReadActivityLog reads persisted events spanning the active log plus rotated archives, applying
// filters, and returns at most limit of the newest matches in chronological order (oldest first).
// It walks files newest-first and stops as soon as the since/limit window is satisfied, so a
// "last 24h" query never scans years of archived history. Unparseable lines are skipped.
// Files are opened by path on every call rather than through the writer's handle, which is
// write-only and replaced on rotation, so each read sees the current active file and archives.
// Only the process that owns the data directory reads them (the primary, or a standalone
// -mcp-only process); an -mcp-only sidecar that finds a running primary proxies to it instead.
func ReadActivityLog(path string, since time.Time, limit int, actionFilter string, sourceFilter string) ([]LogEvent, error) {
	return ReadActivityLogFiltered(path, ActivityFilter{
		Since:  since,
		Limit:  limit,
		Action: actionFilter,
		Source: sourceFilter,
	})
}

// ReadActivityLogBefore returns up to limit matching events strictly older than `before`
// (or the newest events when before is zero), in newest-first order. It powers the
// "Load older history" pagination cursor in the Activity Drawer, spanning archives as needed.
func ReadActivityLogBefore(path string, before time.Time, limit int, actionFilter string, sourceFilter string) ([]LogEvent, error) {
	dataDir := filepath.Dir(path)
	files := append([]string{path}, listActivityArchives(dataDir)...)

	matches := func(ev LogEvent) bool {
		if !before.IsZero() && !ev.Timestamp.Before(before) {
			return false
		}
		if actionFilter != "" && ev.Action != actionFilter {
			return false
		}
		if sourceFilter != "" && ev.Source != sourceFilter {
			return false
		}
		return true
	}

	var collected []LogEvent
	for _, fp := range files {
		evs, _, ok, err := readEventFile(fp)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		for _, ev := range evs {
			if matches(ev) {
				collected = append(collected, ev)
			}
		}
		if limit > 0 && len(collected) >= limit {
			break
		}
	}

	// Newest first for drawer rendering.
	sort.Slice(collected, func(i, j int) bool {
		return collected[i].Timestamp.After(collected[j].Timestamp)
	})
	if limit > 0 && len(collected) > limit {
		collected = collected[:limit]
	}
	return collected, nil
}

// readEventFile reads and parses all events from one JSON-Lines file. It returns the parsed events,
// the earliest timestamp seen, whether the file existed, and any read error.
func readEventFile(path string) (events []LogEvent, earliest time.Time, exists bool, err error) {
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, time.Time{}, false, nil
		}
		return nil, time.Time{}, false, err
	}
	defer func() { _ = file.Close() }()

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var ev LogEvent
		if err := json.Unmarshal(line, &ev); err != nil {
			continue // skip corrupt lines
		}
		events = append(events, ev)
		if earliest.IsZero() || ev.Timestamp.Before(earliest) {
			earliest = ev.Timestamp
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, time.Time{}, true, err
	}
	return events, earliest, true, nil
}
