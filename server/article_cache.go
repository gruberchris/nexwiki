package server

import (
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strings"
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
//
// Every writer fingerprints a path the same way, by what reading it reads: a symlink's target, and a
// regular file itself. See walkFileInfo.
type articleCache struct {
	mu      sync.Mutex
	entries map[string]*articleCacheEntry
	// failures holds, per path, the file version that last failed to read or parse or was found
	// misplaced, so a broken or misplaced file is warned about once per version rather than on every
	// scan. A directory that could not be listed, or a file that could not be stat'd, has no version
	// and is recorded under the zero fileVersion. See Storage.skipUnreadable, Storage.skipWalkError,
	// and Storage.skipMisplaced.
	failures map[string]failureRecord

	// parseHook, when not nil, is called with the path of every file cachedMeta reads and parses
	// because it had no fresh entry. Only tests set it, before using the storage, to count the
	// parses the cache failed to save.
	parseHook func(path string)
}

// fileVersion identifies one version of a file on disk by the same modification time and size that
// fresh compares.
type fileVersion struct {
	modTime time.Time
	size    int64
}

func (v fileVersion) equal(other fileVersion) bool {
	return v.size == other.size && v.modTime.Equal(other.modTime)
}

// failureRecord is the version of a file that was last warned about.
type failureRecord struct {
	version fileVersion
	// misplaced marks a file that parsed but is not stored where its slug says it lives. store
	// forgets any other failure once the file parses, but a successful parse is how this one was
	// found, so a scan parsing the same version must not reset it and warn a second time.
	misplaced bool
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
		failures: make(map[string]failureRecord),
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
	// A successful parse forgets any earlier failure, so the file breaking again warns again. A
	// misplaced record of this same version stays: see failureRecord.
	if prev, ok := c.failures[path]; ok && !(prev.misplaced && prev.version.equal(fileVersion{modTime: entry.modTime, size: entry.size})) {
		delete(c.failures, path)
	}
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
	return c.note(path, info, false)
}

// noteMisplaced records that the current version of a file is misplaced, and reports whether that
// is news, under the same lock and version rule as noteFailure. info must not be nil: only a file
// that parsed can be misplaced.
func (c *articleCache) noteMisplaced(path string, info fs.FileInfo) bool {
	return c.note(path, info, true)
}

func (c *articleCache) note(path string, info fs.FileInfo, misplaced bool) bool {
	var version fileVersion
	if info != nil {
		version = fileVersion{modTime: info.ModTime(), size: info.Size()}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if info == nil {
		delete(c.entries, path)
	}
	// One record per version whatever its kind. A misplaced file whose body then fails to read in
	// another scan is not news, and letting the kinds replace each other would have the scans that
	// read bodies and the ones that do not take turns warning about the same version.
	if prev, ok := c.failures[path]; ok && prev.version.equal(version) {
		return false
	}
	c.failures[path] = failureRecord{version: version, misplaced: misplaced}
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
// skipWalkError instead, and one that parses but is stored in the wrong place through
// skipMisplaced.
//
// The warning is logged once per file version, not once per scan: the sidebar, the dashboard, and
// many MCP tools rescan the wiki, and a warning repeated on each of those would bury everything
// else on stderr.
//
// It reports false for a file that vanished mid-walk: a rename or delete racing the scan leaves a
// file that is gone, not broken, and reporting it would name a file that isn't there. The Lstat
// tells that apart from a dangling symlink, which fails the same way but is still in the directory.
// info is nil for a dangling symlink, which has no target to fingerprint (see walkFileInfo).
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

// getArticleForScan is the read the corpus-wide loops share: each enumerates the wiki — via
// ListArticles or another listing's cached metadata — and then opens every document it listed in
// full. The OKF export, the skills and plans listings, the status field migration, the plan
// lifecycle worker, global tag deletion, and the bulk-change announcements all have this shape,
// and every one skips a document that will not open rather than failing the whole operation.
//
// Skipping is right — one broken file must not cost an agent the whole listing, or the operator
// the whole export — but silent is not: a document that vanishes from an export, a listing, or a
// migration with nothing saying why is exactly the failure this read reports. A document that
// cannot be opened is warned about once per file version, the way the walks warn (see
// skipUnreadable), and skipped; one that is simply not there is skipped silently, for the reasons
// loadForIndex gives: a file deleted since the listing is gone, not broken, and one that has
// become misplaced since the listing is the walks' to report, as misplaced, for that version.
func (s *Storage) getArticleForScan(slug string) (*Article, bool) {
	art, err := s.GetArticle(slug)
	if err == nil {
		return art, true
	}
	s.reportScanReadFailure(slug, err)
	return nil, false
}

// reportScanReadFailure warns about one document a corpus-wide loop could not open, the way
// skipUnreadable warns for a walk: once per file version, naming the file and the error. Not
// found is not reported — see getArticleForScan — and neither is a slug that names no file.
func (s *Storage) reportScanReadFailure(slug string, err error) {
	if errors.Is(err, errArticleNotFound) {
		return
	}
	cleaned := Slugify(slug)
	if cleaned == "" {
		return
	}
	// os.Stat, which fingerprints a symlink by its target as the walks do (see walkFileInfo), so
	// the loops and the walks record the same version and do not take turns warning. A directory,
	// or a link to one, is no article to the walks, so it is not reported here either.
	path := filepath.Join(s.ArticleDir, cleaned+".md")
	if info, statErr := os.Stat(path); statErr == nil && !info.IsDir() {
		s.skipUnreadable(path, info, err)
	}
}

// skipMisplaced is the check every walk of the article directory makes once a file has parsed:
// ListArticles, ScanLinkGraph, GetBacklinks, and findAssetReferrers. See isCanonical for the rule.
// Listing a file that breaks it offered a document that could not be opened, searched, or exported,
// and saving it back under its slug wrote a different file: a duplicate, or over whatever article
// already held that slug. So every walk leaves it out, and it is reported for a person to move or
// delete rather than guessed at.
//
// It reports whether the file is misplaced, warning once per file version like skipUnreadable.
func (s *Storage) skipMisplaced(path string, info fs.FileInfo, slug string) (MisplacedDocument, bool) {
	if s.isCanonical(path, slug) {
		return MisplacedDocument{}, false
	}
	doc := MisplacedDocument{Path: s.articleRelPath(path), Slug: slug}
	if s.cache.noteMisplaced(path, info) {
		log.Printf("Warning: skipping misplaced article file %s: %s", doc.Path, doc.problem(s.caseInsensitive))
	}
	return doc, true
}

// isCanonical reports whether the parsed document a walk found at path is stored where its slug
// says it lives, which is what makes it a document at all. A document is stored as <slug>.md
// directly in the article directory, where slug is the one parseArticleFile returns, declared in its
// front matter or derived from its title, and is in slug form: what Slugify returns, so lowercase
// letters, digits, and hyphens. That is the only file a slug lookup (GetArticle, metaBySlug) reads
// and a save writes, and NexWiki never writes any other.
//
// Anything else that parses is misplaced: a file in a subdirectory, a copy whose filename differs
// from its slug (cp a.md b.md), or a slug no lookup can reach because lookups slugify what they are
// given. The filename is compared the way the filesystem compares names (see caseInsensitive):
// exactly where case matters, and ignoring ASCII case where it does not, since there a lookup of bar
// opens Bar.md. That keeps what the walks report as misplaced and what a lookup can open in
// agreement. Only ASCII case: a slug is ASCII, and a filesystem's folding beyond it varies (NTFS does
// not take the Kelvin sign for k, as Unicode folding does), so a non-ASCII name is not assumed to
// open by an ASCII slug.
//
// A lookup builds its path from the slug, so for one this reduces to the parsed slug being the one
// looked up, and it compares that directly.
func (s *Storage) isCanonical(path, slug string) bool {
	// A file directly in the article directory splits into that directory, a separator, and its
	// name: the walks join names onto ArticleDir, which NewStorage builds with filepath.Join and so
	// is already clean. This runs for every file on every scan, and cleaning both paths each time
	// was most of its cost.
	dir, file := filepath.Split(path)
	if len(dir) != len(s.ArticleDir)+1 || !strings.HasPrefix(dir, s.ArticleDir) {
		return false
	}
	stem, ok := strings.CutSuffix(file, ".md")
	if !ok {
		return false
	}
	if s.caseInsensitive {
		return asciiEqualFold(stem, slug) && isSlugForm(slug)
	}
	return stem == slug && isSlugForm(slug)
}

// asciiEqualFold reports whether a and b are equal ignoring the case of ASCII letters only. See
// isCanonical for why not strings.EqualFold.
func asciiEqualFold(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		if asciiLowerByte(a[i]) != asciiLowerByte(b[i]) {
			return false
		}
	}
	return true
}

// asciiLower lowercases the ASCII letters in s and leaves every other byte alone.
func asciiLower(s string) string {
	b := []byte(s)
	for i, c := range b {
		b[i] = asciiLowerByte(c)
	}
	return string(b)
}

func asciiLowerByte(c byte) byte {
	if 'A' <= c && c <= 'Z' {
		return c + 'a' - 'A'
	}
	return c
}

// caseProbeName is the file detectCaseInsensitive creates in the article directory for a moment. It
// is lowercase so its uppercase form is a different name, and neither an article (.md) nor a name
// the temp file sweep removes. It is fixed rather than random because detection runs while
// NewStorage holds the search index lock, so no other NexWiki process can be probing the same
// directory, and a fixed name means a probe a crash left behind is removed next start rather than
// accumulating.
const caseProbeName = ".nexwiki-case-probe"

// caseProbeLstat is os.Lstat as detectCaseInsensitive calls it, held in a variable only so tests can
// give either answer whatever the host filesystem does.
var caseProbeLstat = os.Lstat

// detectCaseInsensitive reports whether dir's filesystem treats names that differ only in case as
// the same entry: macOS and Windows by default, and SMB, FAT, and case-folding mounts anywhere. It
// creates caseProbeName, stats its uppercase form, and removes the probe. It runs once per Storage,
// so no lookup pays for it.
//
// Any failure answers false, case-sensitive, with a warning. That is also the conservative answer:
// on a filesystem that ignores case it reports a file whose name differs from its slug only in case
// as misplaced, while a lookup still opens it. That is the right document with the right slug, so the
// disagreement costs a warning, not data.
func detectCaseInsensitive(dir string) bool {
	warn := func(err error) bool {
		log.Printf("Warning: could not tell whether the article directory ignores case in file names, so treating it as case-sensitive: %v", err)
		return false
	}
	probe := filepath.Join(dir, caseProbeName)
	// Whatever holds the name goes first: a probe a crash left behind, or anything else. os.Remove
	// removes a symlink rather than what it points at, and O_EXCL will not follow one either, so the
	// probe can never truncate or create a file somewhere else.
	if err := os.Remove(probe); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return warn(err)
	}
	f, err := os.OpenFile(probe, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return warn(err)
	}
	_ = f.Close()
	defer func() {
		if err := os.Remove(probe); err != nil && !errors.Is(err, fs.ErrNotExist) {
			log.Printf("Warning: could not remove %s from the article directory: %v", caseProbeName, err)
		}
	}()

	probeInfo, err := caseProbeLstat(probe)
	if err != nil {
		return warn(err)
	}
	upperInfo, err := caseProbeLstat(filepath.Join(dir, strings.ToUpper(caseProbeName)))
	if errors.Is(err, fs.ErrNotExist) {
		return false // the usual answer on a filesystem where case matters
	}
	if err != nil {
		return warn(err)
	}
	return os.SameFile(probeInfo, upperInfo)
}

// skipWalkError is the per-entry error handling the article walks share for an entry they could
// not get as far as reading: a directory WalkDir could not list, or a file walkFileInfo could not
// stat. Call it with the WalkDir callback's path and entry, and return what it returns: nil to
// skip a file, fs.SkipDir to skip a directory's subtree, or an error to fail the scan.
//
// A missing or unreadable article root fails the scan, because listing a broken data directory as
// an empty wiki hides that, and callers act on the listing: SyncSearchIndex would drop every index
// entry as an orphan. So does a root that can be listed but not searched (read permission without
// execute), which lists every entry and then cannot stat or open any of them. That shows as a
// permission error stat'ing an entry directly under the root: a file's error here already is that
// stat, and a directory that cannot be listed is stat'd to tell. But one entry's stat can also be
// denied on its own, by an SELinux label, a macOS ACL, or a FUSE mount another user owns, and
// failing every scan for that would keep the server from even starting while blaming the wrong
// directory. So the root is blamed only when articleDirSearchable finds it at fault. Any other stat
// failure there, such as an I/O error or a stale network handle, may be that one entry's too.
//
// Anything else is skipped, so one bad entry never fails a scan. A file in a subdirectory that
// cannot be searched is skipped and reported on its own, like any other file, rather than as the
// subdirectory: the scan loses the same articles either way, and the report names each of them.
// An entry that no longer exists is skipped silently: a delete or rename racing the walk leaves it
// gone, not broken. Any other failure is warned about once while it lasts, like skipUnreadable, and
// passed to report, when that is not nil, with a directory's path ending in /.
func (s *Storage) skipWalkError(path string, d fs.DirEntry, err error, report func(UnreadableFile)) error {
	if s.isArticleRoot(path) {
		// For a symlinked directory the OS error names the root the walk was given, separator and all
		// (see articleWalkRoot). Name the directory as configured, so the error reads as a real
		// directory's does and client-facing path hiding turns it into "." rather than an empty path.
		if pathErr, ok := err.(*fs.PathError); ok && pathErr.Path != s.ArticleDir && s.isArticleRoot(pathErr.Path) {
			return &fs.PathError{Op: pathErr.Op, Path: s.ArticleDir, Err: pathErr.Err}
		}
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
			// Stat'd, not just probed: a directory whose listing failed (on a stale network handle,
			// say) and that is gone now was deleted, not broken, and is skipped silently like any other.
			_, statErr = walkLstat(path)
			if errors.Is(statErr, fs.ErrNotExist) {
				return skip
			}
		}
		if errors.Is(statErr, fs.ErrPermission) && !s.articleDirSearchable() {
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

// articleDirSearchProbe is the name articleDirSearchable stats directly under the article
// directory. Nothing creates it: it is neither an article (.md) nor an atomic write's temp file.
const articleDirSearchProbe = ".nexwiki-search-probe"

// walkLstat is os.Lstat as skipWalkError calls it, held in a variable only so tests can fail the
// call: a stat denied while the article directory is still searchable cannot be set up with
// permissions alone.
var walkLstat = os.Lstat

// articleDirSearchable reports whether the article directory can be searched, for skipWalkError to
// tell a root that denies every stat under it from one entry whose stat was denied. It stats a name
// that is not there, which a searchable root answers with not-exist, so only a permission error
// convicts the root. Any other error, or finding the name after all, is no evidence against it,
// and failing every scan on a guess would keep the server from starting.
//
// It costs a stat, possibly on a slow network mount, so it runs only once a stat has already been
// denied, and never under the cache lock.
func (s *Storage) articleDirSearchable() bool {
	_, err := walkLstat(filepath.Join(s.ArticleDir, articleDirSearchProbe))
	return !errors.Is(err, fs.ErrPermission)
}

// articleWalkRoot is the root every walk of the article directory hands filepath.WalkDir: ArticleDir,
// with a trailing separator when it is a symlink.
//
// WalkDir lstats its root, so given a symlinked article directory as it is, it visited the link and
// never descended, and the wiki looked empty: no listings or link graph, and a boot index sync that
// dropped every search entry as an orphan. A trailing separator has that lstat resolve the last
// element, as POSIX path resolution requires and Go's Lstat does on Windows too, and WalkDir drops the
// separator again joining each name onto the root. So the walk still produces exactly the paths a
// lookup builds with filepath.Join(ArticleDir, ...), and a link retargeted while the server runs is
// followed on the next walk. The separator costs each entry directly under the root two allocations,
// cleaning the joined path, so a real directory, which needs none, is walked as it always was, for
// the price of one lstat per walk.
//
// Resolving the directory once at startup would have had the walks produce other paths than the
// lookups, so cache keys, isCanonical, articleRelPath, and the root checks would all have had to
// switch to the resolved form, and errors would name wherever the link points, which the client-facing
// path hiding does not know when that is outside the data directory.
func (s *Storage) articleWalkRoot() string {
	// A root that cannot be lstat'd is walked as it is, and WalkDir fails on it the same way.
	if info, err := os.Lstat(s.ArticleDir); err != nil || info.Mode()&fs.ModeSymlink == 0 {
		return s.ArticleDir
	}
	return s.ArticleDir + string(filepath.Separator)
}

// isArticleRoot reports whether path names the article directory as a walk does: ArticleDir, or the
// root articleWalkRoot gives a symlinked one.
func (s *Storage) isArticleRoot(path string) bool {
	n := len(s.ArticleDir)
	return path == s.ArticleDir || (len(path) == n+1 && os.IsPathSeparator(path[n]) && path[:n] == s.ArticleDir)
}

// walkFileInfo is how every walk of the article directory stats a .md entry it reached. It returns the
// fingerprint the walk caches the file and records its warnings under, or false when the walk is to
// skip the entry and return the error.
//
// A symlink is fingerprinted by its target. The link's own modification time and size do not change
// when its target is edited, including by NexWiki's own saves, which writeFileAtomic makes through the
// link, so a walk that fingerprinted the link served the old metadata and links until a restart. The
// lookups that share the cache (metaBySlug, deleteArticleLocked, and loadForIndex's failure record)
// stat through the link, and a fingerprint that differed from theirs under the same key had each
// replace the other's entry, re-parsing the file on every call. For a regular file, DirEntry.Info
// answers as os.Stat does, so it keeps the one stat it always had.
//
// A link whose target is missing is still in the directory, so it goes through skipUnreadable, which
// reports it like any other file that cannot be read and tells it from a link deleted mid-walk. Any
// other failure to stat a target goes through skipWalkError, as a regular file's does. A link to a
// directory is not an article whatever its name, and WalkDir does not descend through it, so it is
// skipped without a word.
func (s *Storage) walkFileInfo(path string, d fs.DirEntry, report func(UnreadableFile)) (fs.FileInfo, bool, error) {
	if d.Type()&fs.ModeSymlink == 0 {
		info, err := d.Info()
		if err != nil {
			// On Unix, Info stats the directory the entry was listed from joined to its name, which
			// directly under a symlinked root doubles the separator articleWalkRoot added. Name the
			// entry by its walk path, so the error reads as a real directory's does and path hiding
			// recognizes it.
			if pathErr, ok := err.(*fs.PathError); ok && pathErr.Path != path {
				err = &fs.PathError{Op: pathErr.Op, Path: path, Err: pathErr.Err}
			}
			return nil, false, s.skipWalkError(path, d, err, report)
		}
		return info, true, nil
	}
	info, err := os.Stat(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		if file, reported := s.skipUnreadable(path, nil, err); reported && report != nil {
			report(file)
		}
		return nil, false, nil
	case err != nil:
		return nil, false, s.skipWalkError(path, d, err, report)
	case info.IsDir():
		return nil, false, nil
	}
	return info, true, nil
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

	if s.cache.parseHook != nil {
		s.cache.parseHook(path)
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
