package server

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Redaction without deletion.
//
// NexWiki keeps every revision of a document as a whole serialized file under
// data/history/<slug>/<N>.md.gz, and read_article(version: N) serves any of them. Rewriting a
// document's current text therefore does not remove anything from it: the old words stay one call
// away. Before purge_history the only operation that cleared history was delete_article, which
// also destroys the document, its slug and its backlinks — more than was asked for, and exactly
// the call an agent harness is right to refuse.
//
// A purge keeps the current revision and deletes the earlier ones. Because a snapshot is the whole
// file, its front matter goes with it: description, source, tags and title are part of a revision,
// and a purge that cleared only bodies would leave a leak in place while reporting success.

// HistoryPurge is what PurgeHistory removed.
type HistoryPurge struct {
	// Slug is the document whose history was purged.
	Slug string
	// Removed lists the deleted revision numbers, ascending.
	Removed []int
	// FormerSlugs are the other slugs the removed revisions were saved under — a document renamed
	// since then carried them — so activity-log entries recorded under a slug that no longer exists
	// can be found and retitled too. A slug is derived from a title, so it can carry the very text
	// being redacted.
	FormerSlugs []string
}

// PurgeHistory permanently deletes every stored revision of slug older than keepFrom, keeping
// keepFrom and anything newer. The document itself, its version counter, and every other
// document are untouched; versions are not renumbered, so optimistic locking stays monotonic.
//
// keepFrom is the version the caller just wrote, not "the current one": holding writeMu, a save
// that landed after the caller's is newer than keepFrom and is therefore never purged unseen.
func (s *Storage) PurgeHistory(slug string, keepFrom int) (*HistoryPurge, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if s.closed.Load() {
		return nil, ErrStorageClosed
	}
	if keepFrom < 1 {
		return nil, fmt.Errorf("invalid revision %d to keep", keepFrom)
	}

	current, err := s.GetArticle(slug)
	if err != nil {
		return nil, err
	}
	purge := &HistoryPurge{Slug: current.Slug, Removed: []int{}}

	histFolder := filepath.Join(s.HistoryDir, current.Slug)
	entries, err := os.ReadDir(histFolder)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return purge, nil
		}
		return nil, fmt.Errorf("failed to read history directory: %w", err)
	}

	formerSlugs := map[string]bool{}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() {
			continue
		}
		// A temp file left by an interrupted snapshot write may hold an old revision too.
		if isAtomicTempName(name) {
			_ = os.Remove(filepath.Join(histFolder, name))
			continue
		}
		version, ok := snapshotVersion(name)
		if !ok || version >= keepFrom {
			continue
		}
		path := filepath.Join(histFolder, name)
		if data, err := readGzippedFile(path); err == nil {
			if old, err := parseArticleFile(data, false); err == nil && old.Slug != "" && old.Slug != current.Slug {
				formerSlugs[old.Slug] = true
			}
		}
		if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("failed to remove revision %d: %w", version, err)
		}
		purge.Removed = append(purge.Removed, version)
	}
	sort.Ints(purge.Removed)
	for former := range formerSlugs {
		purge.FormerSlugs = append(purge.FormerSlugs, former)
	}
	sort.Strings(purge.FormerSlugs)
	return purge, nil
}

// snapshotVersion parses a history file name ("<N>.md.gz") into its revision number.
func snapshotVersion(name string) (int, bool) {
	digits, ok := strings.CutSuffix(name, ".md.gz")
	if !ok {
		return 0, false
	}
	v, err := strconv.Atoi(digits)
	if err != nil || v < 1 {
		return 0, false
	}
	return v, true
}

// HistoryMatch is one stored revision whose serialized file contains a search term.
type HistoryMatch struct {
	Slug    string `json:"slug"`
	Title   string `json:"title"`
	Version int    `json:"version"`
	// Current is true when this revision is the live document, false for a history snapshot.
	Current bool `json:"current"`
}

// SearchHistory answers "does any revision of any document still contain this term?" — the
// question that turns a redaction from a claim into a finding. It is a case-insensitive literal
// substring scan over every whole serialized file, front matter included: every history snapshot,
// and every live article file, so a document that predates version history is still covered.
//
// It is deliberately not narrowed by type, tag, or archived filters. An audit that silently
// skipped part of the corpus would report "not found" for text that is still there.
//
// Results are sorted by slug, then version, and deduplicated on (slug, version): the live file
// and the snapshot of the same revision are one revision.
func (s *Storage) SearchHistory(term string) ([]HistoryMatch, error) {
	needle := strings.ToLower(term)
	if strings.TrimSpace(needle) == "" {
		return []HistoryMatch{}, nil
	}

	type key struct {
		slug    string
		version int
	}
	found := map[key]HistoryMatch{}
	liveTitle := map[string]string{}
	liveVersion := map[string]int{}

	// Live files first, so the snapshot pass knows each document's current title and version.
	walkErr := filepath.WalkDir(s.articleWalkRoot(), func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if d != nil && d.IsDir() && !s.isArticleRoot(path) {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() || filepath.Ext(path) != ".md" {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		slug := strings.TrimSuffix(filepath.Base(path), ".md")
		title, version := slug, 0
		if art, err := parseArticleFile(data, false); err == nil {
			if art.Slug != "" {
				slug = art.Slug
			}
			title, version = art.Title, art.Version
		}
		liveTitle[slug] = title
		liveVersion[slug] = version
		if strings.Contains(strings.ToLower(string(data)), needle) {
			found[key{slug, version}] = HistoryMatch{Slug: slug, Title: title, Version: version, Current: true}
		}
		return nil
	})
	if walkErr != nil {
		return nil, walkErr
	}

	dirs, err := os.ReadDir(s.HistoryDir)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("failed to read history directory: %w", err)
	}
	for _, dir := range dirs {
		if !dir.IsDir() {
			continue
		}
		slug := dir.Name()
		files, err := os.ReadDir(filepath.Join(s.HistoryDir, slug))
		if err != nil {
			continue
		}
		for _, f := range files {
			version, ok := snapshotVersion(f.Name())
			if !ok || f.IsDir() {
				continue
			}
			data, err := readGzippedFile(filepath.Join(s.HistoryDir, slug, f.Name()))
			if err != nil || !strings.Contains(strings.ToLower(string(data)), needle) {
				continue
			}
			k := key{slug, version}
			if _, seen := found[k]; seen {
				continue
			}
			title, live := liveTitle[slug]
			if !live {
				// History with no live document: name it by the revision itself.
				if art, err := parseArticleFile(data, false); err == nil {
					title = art.Title
				}
			}
			found[k] = HistoryMatch{Slug: slug, Title: title, Version: version, Current: live && liveVersion[slug] == version}
		}
	}

	matches := make([]HistoryMatch, 0, len(found))
	for _, m := range found {
		matches = append(matches, m)
	}
	sort.Slice(matches, func(i, j int) bool {
		if matches[i].Slug != matches[j].Slug {
			return matches[i].Slug < matches[j].Slug
		}
		return matches[i].Version < matches[j].Version
	})
	return matches, nil
}

// HistoryPurgeReport is what a purge_history save reports back.
type HistoryPurgeReport struct {
	Slug string `json:"slug"`
	// KeptVersion is the one revision left in history: the one the save just wrote.
	KeptVersion int `json:"kept_version"`
	// RevisionsRemoved lists the deleted revision numbers, ascending.
	RevisionsRemoved []int `json:"revisions_removed"`
	// ActivityEntriesRetitled counts durable activity-log events rewritten to the current title.
	ActivityEntriesRetitled int `json:"activity_entries_retitled"`
}

// purgeDocumentHistory runs a purge after a successful save of art: every revision older than
// art.Version is deleted, and the document's earlier activity-log events — durable and in memory —
// are retitled to its current title (and current slug, for events recorded under a slug it had
// before a rename).
func (srv *Server) purgeDocumentHistory(art *Article) (*HistoryPurgeReport, error) {
	purge, err := srv.Storage.PurgeHistory(art.Slug, art.Version)
	if err != nil {
		return nil, err
	}

	slugs := map[string]bool{art.Slug: true}
	for _, former := range purge.FormerSlugs {
		// A former slug another live document has since taken is that document's now; its events
		// are not this document's to rewrite.
		if _, err := srv.Storage.GetArticle(former); err == nil {
			continue
		}
		slugs[former] = true
	}

	report := &HistoryPurgeReport{Slug: art.Slug, KeptVersion: art.Version, RevisionsRemoved: purge.Removed}

	var retitled int
	if srv.ActivityLog != nil {
		retitled, err = srv.ActivityLog.RetitleEvents(slugs, art.Slug, art.Title)
	} else {
		retitled, _, err = retitleActivityFiles(srv.Storage.DataDir, slugs, art.Slug, art.Title)
	}
	report.ActivityEntriesRetitled = retitled
	if srv.EventBus != nil {
		srv.EventBus.RetitleEvents(slugs, art.Slug, art.Title)
	}
	if err != nil {
		return report, fmt.Errorf("the revisions were removed, but rewriting the activity log failed: %w", err)
	}
	return report, nil
}

// text renders the report for a tool response: what was removed, and what survives.
func (r *HistoryPurgeReport) text() string {
	removed := "none (there were no earlier revisions)"
	if len(r.RevisionsRemoved) > 0 {
		parts := make([]string, len(r.RevisionsRemoved))
		for i, v := range r.RevisionsRemoved {
			parts[i] = strconv.Itoa(v)
		}
		removed = strings.Join(parts, ", ")
	}
	return fmt.Sprintf("\nHistory purged.\nrevisions_removed: [%s]\nactivity_entries_retitled: %d\n"+
		"What survives: only revision %d, the one this save wrote. The document, its slug, type, status, and backlinks are unchanged. "+
		"Verify with search_wiki(query: <term>, include_history: true).\n",
		removed, r.ActivityEntriesRetitled, r.KeptVersion)
}
