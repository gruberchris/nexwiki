package server

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// ActivityLogFilename is the durable activity log file inside the data directory.
const ActivityLogFilename = "activity.jsonl"

// activityLogRotateBytes triggers a one-deep rotation (activity.jsonl -> activity.jsonl.1)
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

// OpenActivityLog opens (creating if needed) the append-only activity log,
// rotating the previous file aside once if it has grown past the size threshold.
func OpenActivityLog(dataDir string) (*ActivityLog, error) {
	path := ActivityLogPath(dataDir)

	if info, err := os.Stat(path); err == nil && info.Size() > activityLogRotateBytes {
		// One-deep rotation: overwrite any previous .1 file
		if err := os.Rename(path, path+".1"); err != nil {
			return nil, fmt.Errorf("failed to rotate activity log: %w", err)
		}
	}

	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return nil, fmt.Errorf("failed to open activity log: %w", err)
	}

	return &ActivityLog{file: file, Path: path}, nil
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

// ReadActivityLog reads persisted events from an activity log file, applying filters,
// and returns at most limit of the newest matches in chronological order.
// It opens the file fresh on every call so secondary stdio processes can read the
// shared log written by the primary web server process. Unparseable lines are skipped.
func ReadActivityLog(path string, since time.Time, limit int, actionFilter string, sourceFilter string) ([]LogEvent, error) {
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer func() { _ = file.Close() }()

	var events []LogEvent
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
		if !since.IsZero() && ev.Timestamp.Before(since) {
			continue
		}
		if actionFilter != "" && ev.Action != actionFilter {
			continue
		}
		if sourceFilter != "" && ev.Source != sourceFilter {
			continue
		}
		events = append(events, ev)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}

	if limit > 0 && len(events) > limit {
		events = events[len(events)-limit:]
	}
	return events, nil
}
