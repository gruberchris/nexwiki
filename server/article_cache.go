package server

import (
	"io/fs"
	"os"
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
}

// articleCacheEntry is one file's parsed form plus the stat fingerprint it was parsed from.
type articleCacheEntry struct {
	modTime time.Time
	size    int64

	// meta is the metadata-only parse (no body), matching what ListArticles returns.
	meta Article
	// links are the outbound WikiLinks in the body, each carrying both the raw target as
	// written and the slug it resolves to. Populated lazily: only link scans need them, and
	// they require reading the body.
	links       []WikiLinkRef
	linksLoaded bool
}

func newArticleCache() *articleCache {
	return &articleCache{entries: make(map[string]*articleCacheEntry)}
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
	c.mu.Unlock()
	return entry
}

// setLinks attaches the outbound links to an entry, under the cache lock so a concurrent reader
// never observes a half-populated slice.
func (c *articleCache) setLinks(entry *articleCacheEntry, refs []WikiLinkRef) {
	c.mu.Lock()
	entry.links = refs
	entry.linksLoaded = true
	c.mu.Unlock()
}

// links reads an entry's cached outbound links, reporting whether they have been populated.
func (c *articleCache) links(entry *articleCacheEntry) ([]WikiLinkRef, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return entry.links, entry.linksLoaded
}

// prune drops entries for files that no longer exist, so a long-lived process does not accumulate
// metadata for deleted or renamed articles.
func (c *articleCache) prune(seen map[string]bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for path := range c.entries {
		if !seen[path] {
			delete(c.entries, path)
		}
	}
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

// cachedLinkTargets returns the outbound WikiLinks for one article, reading the body only when the
// file changed or the links have not been scanned yet. Link scans are O(articles) by nature;
// caching the parse keeps repeated lookups from re-reading the whole wiki.
func (s *Storage) cachedLinkTargets(path string, info fs.FileInfo) ([]WikiLinkRef, error) {
	entry, _, err := s.cachedMeta(path, info)
	if err != nil {
		return nil, err
	}
	if refs, ok := s.cache.links(entry); ok {
		return refs, nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	full, err := parseArticleFile(data, true)
	if err != nil {
		return nil, err
	}

	// Resolve each target once here, so Slugify stays off the hot path of every lookup. The raw
	// target is kept alongside it because a broken-link report has to name the link as the author
	// wrote it — "[[Search Design]]" is what they have to find in the file to fix it.
	raw := ExtractWikiLinkTargets(full.Content)
	refs := make([]WikiLinkRef, 0, len(raw))
	for _, target := range raw {
		refs = append(refs, WikiLinkRef{Target: target, Slug: Slugify(target)})
	}

	s.cache.setLinks(entry, refs)
	return refs, nil
}
