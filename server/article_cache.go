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
