package server

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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

// activityLogRotateBytes triggers rotation (the active file is archived, a fresh one is started)
// when the log exceeds this size at open time. Variable for testability.
var activityLogRotateBytes int64 = 10 * 1024 * 1024 // 10 MB

// ActivityLog appends LogEvents durably as JSON Lines to the data directory.
type ActivityLog struct {
	mu   sync.Mutex
	file *os.File
	Path string
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

	return &ActivityLog{file: file, Path: path}, nil
}

// the nextArchivePath returns a non-colliding archive path: a UTC-timestamped name, falling back to a
// monotonic activity.jsonl.N suffix if a same-second archive already exists.
func nextArchivePath(dataDir string) string {
	// Colons are not filesystem-safe on all platforms, so use a dash-separated UTC stamp.
	stamp := time.Now().UTC().Format("2006-01-02T15-04-05Z")
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

// listActivityArchives returns the archive file paths in newest-first order.
func listActivityArchives(dataDir string) []string {
	entries, err := os.ReadDir(dataDir)
	if err != nil {
		return nil
	}
	var archives []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		isTimestamped := strings.HasPrefix(name, activityArchivePrefix) && strings.HasSuffix(name, ".jsonl")
		isMonotonic := strings.HasPrefix(name, ActivityLogFilename+".")
		if isTimestamped || isMonotonic {
			archives = append(archives, filepath.Join(dataDir, name))
		}
	}
	// Newest first: timestamped names sort lexically by time; this also keeps monotonic suffixes grouped.
	sort.Sort(sort.Reverse(sort.StringSlice(archives)))
	return archives
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
	_, err = al.file.Write(append(data, '\n'))
	return err
}

// Close releases the underlying file handle.
func (al *ActivityLog) Close() error {
	al.mu.Lock()
	defer al.mu.Unlock()
	return al.file.Close()
}

// ReadActivityLog reads persisted events spanning the active log plus rotated archives, applying
// filters, and returns at most limit of the newest matches in chronological order (oldest first).
// It walks files newest-first and stops as soon as the since/limit window is satisfied, so a
// "last 24h" query never scans years of archived history. Unparseable lines are skipped.
// Files are opened fresh on every call so mcp-only sidecar processes can read the shared log.
func ReadActivityLog(path string, since time.Time, limit int, actionFilter string, sourceFilter string) ([]LogEvent, error) {
	dataDir := filepath.Dir(path)

	// Newest-first reading order: the active file, then archives by descending name (time).
	files := append([]string{path}, listActivityArchives(dataDir)...)

	matches := func(ev LogEvent) bool {
		if !since.IsZero() && ev.Timestamp.Before(since) {
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
			if matches(ev) {
				collected = append(collected, ev)
			}
		}
		// Stop walking older archives once the window is fully covered:
		// - a since bound is satisfied once this file reaches before `since`, or
		// - without a since bound, once we already hold at least `limit` matches.
		if !since.IsZero() {
			if !fileEarliest.IsZero() && fileEarliest.Before(since) {
				break
			}
		} else if limit > 0 && len(collected) >= limit {
			break
		}
	}

	if !anyFileExists {
		return nil, nil
	}

	sort.Slice(collected, func(i, j int) bool {
		return collected[i].Timestamp.Before(collected[j].Timestamp)
	})

	if limit > 0 && len(collected) > limit {
		collected = collected[len(collected)-limit:]
	}
	return collected, nil
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
