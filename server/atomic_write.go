package server

import (
	"errors"
	"math/rand/v2"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"syscall"
	"time"
)

// writeFileAtomic replaces path with data so that a concurrent reader sees either the old file or
// the new one, never an empty or half-written one. os.WriteFile truncates the live file and then
// writes into it, and the directory scans (ListArticles, ScanLinkGraph, GetBacklinks) read without
// writeMu, so a scan racing a save could fail to parse the article and drop it from its results.
//
// The temp file is created beside the destination so the rename never crosses a filesystem, and
// its name does not end in ".md" (or ".md.gz") so the scans, which filter on those extensions,
// never see it.
//
// It otherwise behaves like os.WriteFile where that is cheap to keep: it writes through a symlink
// instead of replacing the link, an existing file keeps its mode, and a new file gets perm less the
// umask. What it cannot keep is the inode: hard links to the old file keep the old content, and the
// replacement is owned by this process's user.
func writeFileAtomic(path string, data []byte, perm os.FileMode) (err error) {
	if resolved, evalErr := filepath.EvalSymlinks(path); evalErr == nil {
		path = resolved
	}
	existing, statErr := os.Stat(path)

	// Dot-prefixed so editors and sync clients ignore it, and ".tmp" keeps it off ".md". The name
	// is fixed-length rather than derived from the destination's: a slug near the 255-byte name
	// limit fits as "<slug>.md" but would not with a prefix and suffix added.
	tmpPath := filepath.Join(filepath.Dir(path), ".nexwiki-"+strconv.FormatUint(rand.Uint64(), 36)+".tmp")
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
		if err = tmp.Chmod(existing.Mode().Perm()); err != nil {
			return err
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
