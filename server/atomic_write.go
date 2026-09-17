package server

import (
	"errors"
	"io/fs"
	"log"
	"math/rand/v2"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// A temp file is named atomicTempPrefix + a random uint64 in base 36 + atomicTempSuffix.
// writeFileAtomic names its files with atomicTempName and the startup sweep matches with
// isAtomicTempName, which round-trips through it, so the two cannot drift apart.
const (
	atomicTempPrefix = ".nexwiki-"
	atomicTempSuffix = ".tmp"
)

func atomicTempName(n uint64) string {
	return atomicTempPrefix + strconv.FormatUint(n, 36) + atomicTempSuffix
}

// isAtomicTempName reports whether name is exactly one atomicTempName produces. The sweep deletes
// what this accepts, so near misses a looser match would take (upper case, leading zeros, extra
// suffixes) must be rejected.
func isAtomicTempName(name string) bool {
	digits, ok := strings.CutPrefix(name, atomicTempPrefix)
	if !ok {
		return false
	}
	if digits, ok = strings.CutSuffix(digits, atomicTempSuffix); !ok {
		return false
	}
	n, err := strconv.ParseUint(digits, 36, 64)
	return err == nil && atomicTempName(n) == name
}

// chmodFile is (*os.File).Chmod, held in a variable only so tests can observe or fail the call.
var chmodFile = (*os.File).Chmod

// writeFileAtomic replaces path with data so that a concurrent reader sees either the old file or
// the new one, never an empty or half-written one. os.WriteFile truncates the live file and then
// writes into it, and the directory scans (ListArticles, ScanLinkGraph, GetBacklinks) read without
// writeMu, so a scan racing a save could fail to parse the article and drop it from its results.
//
// The temp file is created beside the destination so the rename never crosses a filesystem, and
// its name does not end in ".md" (or ".md.gz") so the scans, which filter on those extensions,
// never see it. A crash before the rename leaves it behind; NewStorage sweeps those at startup.
//
// It otherwise behaves like os.WriteFile where that is cheap to keep: it writes through a symlink
// instead of replacing the link, an existing file keeps its mode (where the filesystem allows the
// chmod), and a new file gets perm less the umask. Where it differs: a dangling symlink is replaced
// by a regular file, where os.WriteFile would have created the link's target; on Unix a read-only
// (e.g. 0444) destination is replaced rather than rejected, because a rename needs write permission
// only on the directory. And it cannot keep the inode: hard links to the old file keep the old
// content, and the replacement is owned by this process's user.
func writeFileAtomic(path string, data []byte, perm os.FileMode) (err error) {
	if resolved, evalErr := filepath.EvalSymlinks(path); evalErr == nil {
		path = resolved
	}
	existing, statErr := os.Stat(path)

	// Dot-prefixed so ls and most file managers hide it, and ".tmp" keeps it off ".md". That does
	// not hide it from sync clients (Syncthing and Dropbox both sync dotfiles by default), which may
	// briefly pick it up before the rename. The name is bounded in length rather than derived from
	// the destination's: a slug near the 255-byte name limit fits as "<slug>.md" but would not with
	// a prefix and suffix added.
	tmpPath := filepath.Join(filepath.Dir(path), atomicTempName(rand.Uint64()))
	// O_EXCL, not os.CreateTemp: CreateTemp forces mode 0600, and chmodding a new file to perm
	// afterwards would bypass the umask that os.WriteFile applied.
	tmp, err := os.OpenFile(tmpPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = tmp.Close() // harmless if already closed
			_ = os.Remove(tmpPath)
		}
	}()

	if _, err = tmp.Write(data); err != nil {
		return err
	}
	if statErr == nil {
		// Only chmod when the modes actually differ, and never fail the save over it: some CIFS/SMB
		// and FUSE mounts (common for a NAS data directory) reject chmod outright, and an
		// unconditional, fatal chmod would fail every edit there, even 0644 over 0644. The content
		// swap is what matters.
		want := existing.Mode().Perm()
		if info, tmpStatErr := tmp.Stat(); tmpStatErr != nil || info.Mode().Perm() != want {
			if chmodErr := chmodFile(tmp, want); chmodErr != nil {
				log.Printf("Warning: saved %s without keeping its mode %v: %v", path, want, chmodErr)
			}
		}
	}
	if err = tmp.Sync(); err != nil {
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}

	// Windows refuses to replace a file while another handle has it open, and Go opens files
	// without FILE_SHARE_DELETE, so a save can collide with this process's own lock-free readers
	// (or a virus scanner) for a moment. Truncating in place never failed that way, so retry
	// briefly rather than fail the save. Elsewhere a rename error is not transient.
	for delay := time.Millisecond; ; delay *= 2 {
		err = os.Rename(tmpPath, path)
		if err == nil || runtime.GOOS != "windows" || delay > 256*time.Millisecond || !isTransientWindowsRenameError(err) {
			return err
		}
		time.Sleep(delay)
	}
}

// isTransientWindowsRenameError reports whether a Windows rename failed because some other handle
// held the source or destination open. Only meaningful on Windows: the errno values differ elsewhere.
func isTransientWindowsRenameError(err error) bool {
	const errorAccessDenied, errorSharingViolation = 5, 32
	var errno syscall.Errno
	if !errors.As(err, &errno) {
		return false
	}
	return errno == errorAccessDenied || errno == errorSharingViolation
}

// removeLeftoverTempFiles deletes the temp files writeFileAtomic leaves behind when the process
// dies between creating one and renaming it into place; nothing else would ever remove them. It
// covers every tree writeFileAtomic writes into, touches only regular files whose names
// isAtomicTempName accepts, and logs rather than returns failures so one stubborn file cannot stop
// startup.
//
// The caller must guarantee no writer is active in these trees, in this process or another: a
// temp file mid-write looks exactly like a leftover. NewStorage calls it while holding the search
// index's exclusive lock and before anything writes.
func (s *Storage) removeLeftoverTempFiles() {
	removed := 0
	for _, root := range []string{s.ArticleDir, s.HistoryDir, s.AssetDir} {
		_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				log.Printf("Warning: temp file sweep could not read %s: %v", s.dataRelPath(path), walkErr)
				return nil
			}
			if !d.Type().IsRegular() || !isAtomicTempName(d.Name()) {
				return nil
			}
			if err := os.Remove(path); err != nil {
				if !errors.Is(err, fs.ErrNotExist) {
					log.Printf("Warning: could not remove leftover temp file %s: %v", s.dataRelPath(path), err)
				}
				return nil
			}
			removed++
			return nil
		})
	}
	if removed > 0 {
		log.Printf("Removed %d leftover temp file(s) from interrupted writes", removed)
	}
}

// dataRelPath shortens path to its slash-separated form relative to the data directory, for logs.
func (s *Storage) dataRelPath(path string) string {
	if rel, err := filepath.Rel(s.DataDir, path); err == nil {
		return filepath.ToSlash(rel)
	}
	return path
}
