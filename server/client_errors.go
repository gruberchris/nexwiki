package server

import (
	"path/filepath"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Errors from the OS, and from the libraries storage is built on, name the path they failed on,
// and every path storage touches sits under the data directory: articles, history, assets, the
// search index, the activity log and its archives, custom themes, and OKF exports. Handing such an
// error to a client verbatim tells it where the wiki lives on the server's disk, which is the
// server's business. The path relative to the data directory says just as well what is wrong, so
// that is what a client gets, while the server log keeps the full path.

// clientError is the text of an error from storage or the OS as an MCP or REST client may see it,
// with the data directory hidden. Validation messages a handler composes itself name no server
// paths and need not go through it.
func (srv *Server) clientError(err error) string {
	if err == nil {
		return ""
	}
	return newDataDirHider(srv.Storage.DataDir).hide(err.Error())
}

// dataDirHider rewrites text so it names paths relative to the data directory rather than where
// that directory sits on the server's disk: /srv/nexwiki/data/articles/sub becomes articles/sub,
// and the directory itself becomes ".".
type dataDirHider struct {
	reps []dataDirReplacement
}

// dataDirReplacement is one string dataDirHider swaps out. Every one must start where a path does;
// a bare one names the directory itself, so it must also end where a path does.
type dataDirReplacement struct {
	old, new string
	bare     bool
}

func newDataDirHider(dataDir string) dataDirHider {
	// Storage joins paths onto the data directory as configured, and Join cleans them, so a
	// relative -data shows up in errors in its cleaned form, and some callers make paths absolute
	// first. The resolved form covers a data directory reached through a symlink, when a path has
	// been resolved before the OS reports it.
	cleaned := filepath.Clean(dataDir)
	forms := []string{cleaned}
	if abs, err := filepath.Abs(cleaned); err == nil {
		forms = append(forms, abs)
		if resolved, err := filepath.EvalSymlinks(abs); err == nil {
			forms = append(forms, resolved)
		}
	}

	var reps []dataDirReplacement
	seen := make(map[string]bool)
	for _, form := range forms {
		// A root (or ".") contains every path, so hiding it would mangle paths that are not the
		// wiki's, and "." is already relative.
		if seen[form] || filepath.Dir(form) == form {
			continue
		}
		seen[form] = true
		reps = append(reps, dataDirReplacement{old: form + string(filepath.Separator)})
		// The bare directory only in absolute form: a short relative one such as "data" is as
		// likely to be an ordinary word in the error text.
		if filepath.IsAbs(form) {
			reps = append(reps, dataDirReplacement{old: form, new: ".", bare: true})
		}
	}
	// Longest first, so where two forms match at one position the fuller path wins.
	sort.SliceStable(reps, func(i, j int) bool { return len(reps[i].old) > len(reps[j].old) })
	return dataDirHider{reps: reps}
}

// hide returns text with the data directory hidden.
func (h dataDirHider) hide(text string) string {
	if len(h.reps) == 0 {
		return text
	}

	// One left-to-right pass that never rescans what it has already replaced.
	var b strings.Builder
	for i := 0; i < len(text); {
		matched := false
		if pathStartsAt(text, i) {
			for _, r := range h.reps {
				if strings.HasPrefix(text[i:], r.old) && (!r.bare || pathEndsAt(text, i+len(r.old))) {
					b.WriteString(r.new)
					i += len(r.old)
					matched = true
					break
				}
			}
		}
		if !matched {
			b.WriteByte(text[i])
			i++
		}
	}
	return b.String()
}

// pathStartsAt reports whether a path could start at text[i]: at the start of the text, or after
// whitespace or the punctuation that opens a path in error text. Anything else, a separator
// included, means text[i] is inside a longer name, so /mnt/srv/data is never taken for /srv/data
// and a relative data directory is never matched inside some other path.
func pathStartsAt(text string, i int) bool {
	if i == 0 {
		return true
	}
	r, _ := utf8.DecodeLastRuneInString(text[:i])
	return unicode.IsSpace(r) || strings.ContainsRune("\"'`([{<=:,;", r)
}
