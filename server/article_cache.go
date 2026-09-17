package server

import (
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// articleCache memoizes parsed article metadata and outbound WikiLink targets, keyed by file path.
//
// ListArticles backs the sidebar, the dashboard, list_articles, get_context_overview, the OKF
// export, and every backlink scan — and it re-read and re-parsed *every* Markdown file on disk on
// each of those calls. On a wiki of any size that is the dominant cost of an ordinary page load.
//
// Validation is by modification time and size rather than by invalidating on NexWiki's own
// writes. That is deliberate: NexWiki's whole storage pitch is that the files stay yours and stay
// editable by anything — vim, Obsidian, a sync client, a shell script. A cache that only noticed
// NexWiki's own writes would serve stale content the moment someone edited a file outside the app,
// which is a worse failure than the cost it saves. A stat is far cheaper than an open, read, and
// YAML parse, so unchanged files still cost almost nothing.
type articleCache struct {
	mu      sync.Mutex
	entries map[string]*articleCacheEntry
	// failures holds, per path, the stat fingerprint of the file version that last failed to read
	// or parse, so a broken file is warned about once per version rather than on every scan. A
	// directory that could not be listed, or a file that could not be stat'd, has no version and is
	// recorded under the zero fileVersion. See Storage.skipUnreadable and Storage.skipWalkError.
	failures map[string]fileVersion
}

// fileVersion identifies one version of a file on disk by the same modification time and size that
// fresh compares.
type fileVersion struct {
	modTime time.Time
	size    int64
}

// articleCacheEntry is one file's parsed form plus the stat fingerprint it was parsed from.
type articleCacheEntry struct {
	modTime time.Time
	size    int64

	// meta is the metadata-only parse (no body), matching what ListArticles returns.
	meta Article
	// links are the outbound internal links in the body, each carrying the raw target as
	// written, the slug it resolves to, and which form it was written in. Populated lazily:
	// only link scans need them, and they require reading the body.
	links       []LinkRef
	linksLoaded bool
	// slugMentions are the slug-shaped tokens found in the body's code spans and fenced blocks.
	// Populated in the same read as links, since both need the body and a skill reference is
	// written as code (`read_article(slug: "…")`) rather than as a link. See ExtractSlugMentions.
	slugMentions []string
}

func newArticleCache() *articleCache {
	return &articleCache{
		entries:  make(map[string]*articleCacheEntry),
		failures: make(map[string]fileVersion),
	}
}

// fresh reports whether a cached entry still matches what is on disk.
func (e *articleCacheEntry) fresh(info fs.FileInfo) bool {
	return e != nil && e.size == info.Size() && e.modTime.Equal(info.ModTime())
}

// lookup returns the cached entry for a path when it still matches the file on disk.
func (c *articleCache) lookup(path string, info fs.FileInfo) (*articleCacheEntry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	entry, ok := c.entries[path]
	if !ok || !entry.fresh(info) {
		return nil, false
	}
	return entry, true
}

// store records a freshly parsed entry.
func (c *articleCache) store(path string, info fs.FileInfo, meta Article) *articleCacheEntry {
	entry := &articleCacheEntry{
		modTime: info.ModTime(),
		size:    info.Size(),
		meta:    meta,
	}
	c.mu.Lock()
	c.entries[path] = entry
	// A successful parse forgets any earlier failure, so the file breaking again warns again.
	delete(c.failures, path)
	c.mu.Unlock()
	return entry
}

// noteFailure records that the current version of a file failed to read or parse, and reports
// whether that is news. The check and the record happen under one lock, so concurrent scans
// hitting the same broken file agree on exactly one of them reporting it first.
//
// info is nil for a path that could not be stat'd or listed at all, which is news once for as long
// as that lasts. Any cached parse of the path is dropped too: it can no longer be validated, and
// dropping it means a file that recovers is parsed afresh, so store forgets the failure and a
// later one warns again.
func (c *articleCache) noteFailure(path string, info fs.FileInfo) bool {
	var version fileVersion
	if info != nil {
		version = fileVersion{modTime: info.ModTime(), size: info.Size()}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if info == nil {
		delete(c.entries, path)
	}
	if prev, ok := c.failures[path]; ok && prev.size == version.size && prev.modTime.Equal(version.modTime) {
		return false
	}
	c.failures[path] = version
	return true
}

// setLinks attaches the outbound links and slug mentions to an entry, under the cache lock so a
// concurrent reader never observes a half-populated slice. Both are set together because both
// come from the one body read that populated them.
func (c *articleCache) setLinks(entry *articleCacheEntry, refs []LinkRef, mentions []string) {
	c.mu.Lock()
	entry.links = refs
	entry.slugMentions = mentions
	entry.linksLoaded = true
	c.mu.Unlock()
}

// links reads an entry's cached outbound links and slug mentions, reporting whether they have
// been populated.
func (c *articleCache) links(entry *articleCacheEntry) ([]LinkRef, []string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return entry.links, entry.slugMentions, entry.linksLoaded
}

// prune drops entries and recorded failures for paths the listing did not mark seen, so a
// long-lived process does not accumulate state for deleted or renamed articles. The listing marks
// a directory seen only while it cannot be listed, so a directory that recovers is forgotten here
// and warns again if it breaks again.
func (c *articleCache) prune(seen map[string]bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for path := range c.entries {
		if !seen[path] {
			delete(c.entries, path)
		}
	}
	for path := range c.failures {
		if !seen[path] {
			delete(c.failures, path)
		}
	}
}

// skipUnreadable is the path a file takes when a scan of the article directory drops it because it
// could not be read or parsed: the walks in ListArticles, ScanLinkGraph, GetBacklinks, and
// findAssetReferrers, and SyncSearchIndex's per-document read. Skipping keeps one bad file from
// failing a whole listing or scan, but a silent skip makes the file vanish from listings, search,
// and health reports with nothing saying why. A file that cannot even be stat'd goes through
// skipWalkError instead.
//
// The warning is logged once per file version, not once per scan: the sidebar, the dashboard, and
// many MCP tools rescan the wiki, and a warning repeated on each of those would bury everything
// else on stderr.
//
// It reports false for a file that vanished mid-walk: a rename or delete racing the scan leaves a
// file that is gone, not broken, and reporting it would name a file that isn't there. The Lstat
// tells that apart from a dangling symlink, which fails the same way but is still in the directory.
func (s *Storage) skipUnreadable(path string, info fs.FileInfo, err error) (UnreadableFile, bool) {
	if errors.Is(err, fs.ErrNotExist) {
		if _, statErr := os.Lstat(path); errors.Is(statErr, fs.ErrNotExist) {
			return UnreadableFile{}, false
		}
	}
	rel := s.articleRelPath(path)
	if s.cache.noteFailure(path, info) {
		log.Printf("Warning: skipping unreadable article file %s: %v", rel, err)
	}
	return UnreadableFile{Path: rel, Error: err.Error()}, true
}

// skipWalkError is the per-entry error handling the article walks share for an entry they could
// not get as far as reading: a directory WalkDir could not list, or a file DirEntry.Info could not
// stat. Call it with the WalkDir callback's path and entry, and return what it returns: nil to
// skip a file, fs.SkipDir to skip a directory's subtree, or an error to fail the scan.
//
// A missing or unreadable article root fails the scan, because listing a broken data directory as
// an empty wiki hides that, and callers act on the listing: SyncSearchIndex would drop every index
// entry as an orphan. So does a root that can be listed but not searched (read permission without
// execute), which lists every entry and then cannot stat or open any of them. That shows as a
// permission error stat'ing an entry directly under the root, which the entry's own permissions
// never cause: a file's error here already is that stat, and a directory that cannot be listed is
// stat'd to tell. Any other stat failure there, such as an I/O error or a stale network handle, may
// be that one entry's, and failing every scan for it would keep the server from even starting.
//
// Anything else is skipped, so one bad entry never fails a scan. A file in a subdirectory that
// cannot be searched is skipped and reported on its own, like any other file, rather than as the
// subdirectory: the scan loses the same articles either way, and the report names each of them.
// An entry that no longer exists is skipped silently: a delete or rename racing the walk leaves it
// gone, not broken. Any other failure is warned about once while it lasts, like skipUnreadable, and
// passed to report, when that is not nil, with a directory's path ending in /.
func (s *Storage) skipWalkError(path string, d fs.DirEntry, err error, report func(UnreadableFile)) error {
	if path == s.ArticleDir {
		return err
	}
	// WalkDir passes a nil entry only for the root, so d is set from here on.
	isDir := d.IsDir()
	var skip error
	if isDir {
		skip = fs.SkipDir
	}
	if errors.Is(err, fs.ErrNotExist) {
		return skip
	}
	if filepath.Dir(path) == filepath.Clean(s.ArticleDir) {
		statErr := err
		if isDir {
			_, statErr = os.Lstat(path)
			if errors.Is(statErr, fs.ErrNotExist) {
				return skip
			}
		}
		if errors.Is(statErr, fs.ErrPermission) {
			// The stat error names an entry, but the entry is not what needs fixing.
			return fmt.Errorf("article directory is not searchable: %w", statErr)
		}
	}
	rel, kind := s.articleRelPath(path), "file"
	if isDir {
		rel, kind = rel+"/", "directory"
	}
	if s.cache.noteFailure(path, nil) {
		log.Printf("Warning: skipping unreadable article %s %s: %v", kind, rel, err)
	}
	if report != nil {
		report(UnreadableFile{Path: rel, Error: err.Error()})
	}
	return skip
}

// articleRelPath names a path under the article directory the way warnings and reports do:
// relative and slash-separated.
func (s *Storage) articleRelPath(path string) string {
	if r, err := filepath.Rel(s.ArticleDir, path); err == nil {
		return filepath.ToSlash(r)
	}
	return path
}

// cachedMeta returns the parsed metadata for one article file, reading and parsing only when the
// file has changed since it was last seen. The returned Article is a copy safe for the caller to
// mutate; the cached original is never handed out.
func (s *Storage) cachedMeta(path string, info fs.FileInfo) (*articleCacheEntry, *Article, error) {
	if entry, ok := s.cache.lookup(path, info); ok {
		meta := entry.meta.clone()
		return entry, &meta, nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	parsed, err := parseArticleFile(data, false)
	if err != nil {
		return nil, nil, err
	}

	entry := s.cache.store(path, info, *parsed)
	meta := parsed.clone()
	return entry, &meta, nil
}

// clone deep-copies the slice fields so a caller mutating tags cannot corrupt the shared cache
// entry. Everything else in Article is a value type.
func (a Article) clone() Article {
	dup := a
	if a.Tags != nil {
		dup.Tags = append([]string(nil), a.Tags...)
	}
	return dup
}

// cachedLinkTargets returns the outbound internal links for one article — both [[WikiLinks]] and
// absolute /articles/ Markdown links — reading the body only when the file changed or the links
// have not been scanned yet. Link scans are O(articles) by nature; caching the parse keeps
// repeated lookups from re-reading the whole wiki.
func (s *Storage) cachedLinkTargets(path string, info fs.FileInfo) ([]LinkRef, error) {
	refs, _, err := s.cachedBodyRefs(path, info)
	return refs, err
}

// cachedBodyRefs returns both the outbound internal links and the slug mentions for one article,
// from a single body read. ScanLinkGraph needs both, and reading the file twice to get them would
// undo the point of the cache.
func (s *Storage) cachedBodyRefs(path string, info fs.FileInfo) ([]LinkRef, []string, error) {
	entry, _, err := s.cachedMeta(path, info)
	if err != nil {
		return nil, nil, err
	}
	if refs, mentions, ok := s.cache.links(entry); ok {
		return refs, mentions, nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	full, err := parseArticleFile(data, true)
	if err != nil {
		return nil, nil, err
	}

	// ExtractLinkRefs resolves each target once here, so Slugify stays off the hot path of every
	// lookup. The raw target is kept alongside it because a broken-link report has to name the
	// link as the author wrote it — "[[Search Design]]" or "(/articles/search-design)" is what
	// they have to find in the file to fix it.
	refs := ExtractLinkRefs(full.Content)
	mentions := ExtractSlugMentions(full.Content)

	s.cache.setLinks(entry, refs, mentions)
	return refs, mentions, nil
}
