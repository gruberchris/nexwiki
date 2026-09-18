package server

import (
	"os"
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
		// The bare directory only where it is recognizably a path: a single relative name such as
		// "data" is as likely to be an ordinary word in the error text.
		if filepath.IsAbs(form) || strings.ContainsRune(form, filepath.Separator) {
			reps = append(reps, dataDirReplacement{old: form, new: ".", bare: true})
		}
	}
	// hide takes the first replacement that matches. Where two match at one position one starts the
	// other, and the longer names the fuller path, so it goes first. The order they were built in
	// does not ensure that: a bare form ends wherever pathEndsAt says a path does, including at
	// characters a directory name may hold, so for a data directory /srv/wiki linked to "/srv/wiki 2"
	// the bare /srv/wiki, built first, would make "/srv/wiki 2/articles" into ". 2/articles".
	sort.SliceStable(reps, func(i, j int) bool { return len(reps[i].old) > len(reps[j].old) })
	return dataDirHider{reps: reps}
}

// hide returns text with the data directory hidden.
func (h dataDirHider) hide(text string) string {
	// Most errors name no path under the data directory, and those go back as they are, uncopied.
	if !h.mentionedIn(text) {
		return text
	}

	// One left-to-right pass that never rescans what it has already replaced. Text is copied only
	// up to each replacement, so a form that never sits at path boundaries costs no copy either.
	var b strings.Builder
	done := 0 // text[:done] has been written to b
	replaced := false
	for i := 0; i < len(text); {
		r, ok := h.replacementAt(text, i)
		if !ok {
			i++
			continue
		}
		b.WriteString(text[done:i])
		b.WriteString(r.new)
		i += len(r.old)
		done = i
		replaced = true
	}
	if !replaced {
		return text
	}
	b.WriteString(text[done:])
	return b.String()
}

// replacementAt returns the replacement that applies to the path starting at text[i], if any.
func (h dataDirHider) replacementAt(text string, i int) (dataDirReplacement, bool) {
	if !pathStartsAt(text, i) {
		return dataDirReplacement{}, false
	}
	for _, r := range h.reps {
		if strings.HasPrefix(text[i:], r.old) && (!r.bare || pathEndsAt(text, i+len(r.old))) {
			return r, true
		}
	}
	return dataDirReplacement{}, false
}

// mentionedIn reports whether text contains any string h replaces, wherever it sits.
func (h dataDirHider) mentionedIn(text string) bool {
	for _, r := range h.reps {
		if strings.Contains(text, r.old) {
			return true
		}
	}
	return false
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

// pathEndsAt reports whether a path running up to text[i] ends there. A separator, whitespace, or
// the punctuation that closes a path in error text ends it. Anything else is taken to continue the
// name, since a file name may legally hold it.
func pathEndsAt(text string, i int) bool {
	if i == len(text) {
		return true
	}
	r, _ := utf8.DecodeRuneInString(text[i:])
	return (r < utf8.RuneSelf && os.IsPathSeparator(uint8(r))) || unicode.IsSpace(r) || strings.ContainsRune(":;,\"'`)]}", r)
}
