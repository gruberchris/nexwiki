package server

import (
	"io/fs"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// stripCodeForLinkScan blanks out Markdown code so [[...]] inside fenced code blocks (``` / ~~~)
// and inline code spans (`...`) is NOT treated as a WikiLink. This keeps the broken-link scan and
// backlinks from flagging real code (e.g., C++ `[[nodiscard]]`, Lua `[[long strings]]`, syntax
// examples in docs). Genuine links in prose are untouched.
func stripCodeForLinkScan(content string) string {
	var b strings.Builder
	inFence := false
	fenceMarker := ""
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimLeft(line, " \t")
		if !inFence && (strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~")) {
			inFence = true
			fenceMarker = trimmed[:3]
			b.WriteByte('\n')
			continue
		}
		if inFence {
			if strings.HasPrefix(trimmed, fenceMarker) {
				inFence = false
			}
			b.WriteByte('\n') // drop fenced content (and the closing fence line)
			continue
		}
		// Drop inline code spans (backtick-delimited) on this line.
		inCode := false
		for i := 0; i < len(line); i++ {
			if line[i] == '`' {
				inCode = !inCode
				continue
			}
			if !inCode {
				b.WriteByte(line[i])
			}
		}
		b.WriteByte('\n')
	}
	return b.String()
}

// ExtractWikiLinkTargets returns the raw (pre-Slugify) targets of all [[Target]] and
// [[Target|display text]] WikiLinks found in a Markdown body, in order of appearance.
// Targets inside code blocks/spans are ignored (they are not navigational links).
func ExtractWikiLinkTargets(content string) []string {
	content = stripCodeForLinkScan(content)
	var targets []string
	for {
		startIdx := strings.Index(content, "[[")
		if startIdx == -1 {
			break
		}
		endIdx := strings.Index(content[startIdx:], "]]")
		if endIdx == -1 {
			break
		}
		endIdx += startIdx

		linkContent := content[startIdx+2 : endIdx]
		content = content[endIdx+2:]

		target := linkContent
		if pipeIdx := strings.Index(linkContent, "|"); pipeIdx != -1 {
			target = linkContent[:pipeIdx]
		}
		targets = append(targets, strings.TrimSpace(target))
	}
	return targets
}

// LinkForm distinguishes the two ways NexWiki authors write an internal link. Comparisons never
// need it — every consumer matches on the resolved slug — but a report does: telling an author to
// look for "[[rust]]" in a file that says "[Rust](/articles/rust)" sends them hunting for text
// that is not there.
type LinkForm string

const (
	// LinkFormWiki is the double-bracket form, [[Target]] or [[Target|display]].
	LinkFormWiki LinkForm = "wikilink"
	// LinkFormMarkdown is the absolute Markdown form, [display](/articles/<slug>). This is the
	// form nexwiki-agent-guidelines §5 tells agents to prefer in body prose, and it accounts for
	// the large majority of internal links in a real corpus.
	LinkFormMarkdown LinkForm = "markdown"
)

// LinkRef is one outbound internal link: the raw target as the author wrote it, the slug it
// resolves to, and which of the two link forms it was written in. All three matter — comparisons
// use the slug, but a report an author has to act on has to name the text they actually typed,
// in the syntax they typed it.
type LinkRef struct {
	// Target is what the author wrote: the text between the double brackets for a WikiLink, or
	// the destination path (/articles/<slug>) for a Markdown link. Not the link's display text —
	// the destination is the half a fix has to edit.
	Target string
	Slug   string
	Form   LinkForm
}

// Display renders a reference the way it appears in the source, so a broken-link report names
// something the author can search for. One helper rather than a format string at each call site:
// the health report and the statistics report must not describe the same link differently.
func (r LinkRef) Display() string {
	if r.Form == LinkFormMarkdown {
		return "(" + r.Target + ")"
	}
	return "[[" + r.Target + "]]"
}

// articlePathPrefix is the route every wiki article is served under, and the only destination
// prefix the link scanner treats as internal.
const articlePathPrefix = "/articles/"

// articlePathLink matches an internal Markdown link — [text](/articles/<slug>) — in three parts:
// the character before the link, the opening through the /articles/ prefix, and the destination.
// Splitting it this way lets RewriteArticlePathLinks rebuild the link by swapping one group.
//
// The leading (^|[^!]) rejects the image form ![alt](/articles/…): an image is not a navigational
// link. The destination stops at whitespace, ')' or '"' so a titled link ([t](/articles/x "T"))
// still resolves. Only the absolute /articles/ prefix matches, which also excludes the
// /api/articles/… URLs that appear in API examples.
var articlePathLink = regexp.MustCompile(`(^|[^!])(\[[^]]*]\(/articles/)([^)\s"]+)`)

// ExtractArticlePathTargets returns the raw destinations of all absolute Markdown links to wiki
// articles — [text](/articles/<slug>) — found in a Markdown body, in order of appearance.
//
// This is the form nexwiki-agent-guidelines §5 tells agents to prefer, and until §3.21 it was
// invisible to the link graph: broken-link detection reported none, orphan detection reported
// pages the home page links to, and get_backlinks under-reported inbound references.
//
// Destinations inside code blocks/spans are ignored, exactly as WikiLinks are — a path in a
// syntax example is documentation, not a link. Any #fragment or ?query is trimmed, so a link into
// a section still resolves to the article that holds it.
func ExtractArticlePathTargets(content string) []string {
	content = stripCodeForLinkScan(content)
	var targets []string
	for _, m := range articlePathLink.FindAllStringSubmatch(content, -1) {
		dest := m[3]
		if idx := strings.IndexAny(dest, "#?"); idx != -1 {
			dest = dest[:idx]
		}
		dest = strings.TrimSuffix(dest, "/")
		if dest == "" {
			continue
		}
		targets = append(targets, articlePathPrefix+dest)
	}
	return targets
}

// ExtractLinkRefs returns every outbound internal link in a Markdown body, in both supported
// forms, with each target resolved to the slug it points at.
//
// The two forms are scanned separately and concatenated rather than interleaved by position.
// Nothing downstream depends on document order — the broken-link report is grouped and sorted by
// document — and keeping the hand-rolled WikiLink scanner intact preserves its pinned edge-case
// behavior (empty brackets, unterminated brackets) that a combined regex would silently change.
func ExtractLinkRefs(content string) []LinkRef {
	wiki := ExtractWikiLinkTargets(content)
	paths := ExtractArticlePathTargets(content)

	refs := make([]LinkRef, 0, len(wiki)+len(paths))
	for _, target := range wiki {
		refs = append(refs, LinkRef{Target: target, Slug: Slugify(target), Form: LinkFormWiki})
	}
	for _, target := range paths {
		// Slugify the path segment, not the whole destination: Slugify strips '/', so
		// Slugify("/articles/rust") would yield "articlesrust".
		slug := Slugify(strings.TrimPrefix(target, articlePathPrefix))
		refs = append(refs, LinkRef{Target: target, Slug: slug, Form: LinkFormMarkdown})
	}
	return refs
}

// LinkGraph is the whole wiki's internal-link structure from a single cached pass over the article
// directory: who links to whom, in both directions, plus the links that go nowhere. Both link
// forms are included — see LinkForm.
//
// One pass, one shape. Broken-link detection and orphan detection need the same traversal in
// opposite directions, and building it twice would double both the cost and the number of places
// a subtle rule (home is included, code fences are not links) has to be repeated.
type LinkGraph struct {
	// Meta is every document's metadata by slug, including home, which listings exclude.
	Meta map[string]Article
	// Outbound is each document's internal links, keyed by the slug of the document holding them.
	Outbound map[string][]LinkRef
	// InboundCount is how many *other* documents link to each slug. Self-links do not count:
	// a page that only links to itself is still an orphan.
	InboundCount map[string]int
	// Broken lists every internal link whose target does not exist, in document then document order.
	Broken []BrokenLinkRef
	// TotalLinks is every internal link scanned, broken or not, in either form.
	TotalLinks int
	// Mentions maps a document's slug to the slugs its body names in code — a
	// `read_article(slug: "…")` call or a backticked slug reference. This is how a *skill* is
	// referenced, and none of it is a link, so it is tracked separately from InboundCount and
	// deliberately kept out of it: counting these would change what get_backlinks and the orphan
	// check mean. See ExtractSlugMentions.
	//
	// Stored in the forward direction, as the walk produces it, because the only consumer
	// (liveReferencedSlugs) iterates documents anyway. An inverted mentioned-by map was tried
	// first and dropped as pointless indirection — measured, it made no difference either way.
	//
	// Collecting mentions at all costs ScanLinkGraph ~10% on the 1k/5k/10k benchmarks
	// (10000 docs: 127ms → 139ms), from retaining the per-document slices and one extra map
	// insert per file. Extraction itself is free, riding the body read cachedBodyRefs already
	// performs and caches by mtime. get_backlinks and get_wiki_statistics share this scan and pay
	// that overhead without reading the field; the absolute cost was judged small enough to
	// prefer one scan over two.
	Mentions map[string][]string
}

// ScanLinkGraph walks the article directory once and builds the whole link graph.
//
// It walks the directory directly rather than going through ListArticles and re-reading each file:
// metadata and link targets are both cached and validated by mtime, so an unchanged wiki costs one
// stat per file instead of a full read and Markdown scan — the same reasoning that made
// GetBacklinks 17.6× faster.
//
// home is included. It is excluded from listings, but it links to much of the wiki, so leaving it
// out would report most of the wiki as orphaned.
func (s *Storage) ScanLinkGraph() (*LinkGraph, error) {
	graph := &LinkGraph{
		Meta:         map[string]Article{},
		Outbound:     map[string][]LinkRef{},
		InboundCount: map[string]int{},
		Broken:       []BrokenLinkRef{},
		Mentions:     map[string][]string{},
	}

	var order []string
	err := filepath.WalkDir(s.ArticleDir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() || filepath.Ext(path) != ".md" {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil // unreadable file: skip rather than fail the whole scan
		}
		_, meta, err := s.cachedMeta(path, info)
		if err != nil {
			return nil
		}
		refs, mentions, err := s.cachedBodyRefs(path, info)
		if err != nil {
			return nil
		}
		graph.Meta[meta.Slug] = *meta
		graph.Outbound[meta.Slug] = refs
		graph.Mentions[meta.Slug] = mentions
		order = append(order, meta.Slug)
		return nil
	})
	if err != nil {
		return nil, err
	}

	// Sorting makes the report stable across runs. Directory walk order is filesystem-dependent,
	// and an agent diffing two health reports should see only real changes.
	sort.Strings(order)

	for _, slug := range order {
		for _, ref := range graph.Outbound[slug] {
			graph.TotalLinks++
			if _, exists := graph.Meta[ref.Slug]; !exists {
				graph.Broken = append(graph.Broken, BrokenLinkRef{
					FromSlug:   slug,
					Target:     ref.Target,
					TargetSlug: ref.Slug,
					Form:       ref.Form,
				})
				continue
			}
			if ref.Slug != slug {
				graph.InboundCount[ref.Slug]++
			}
		}
	}

	return graph, nil
}

// GetBacklinks scans all articles (including the home dashboard, which listings exclude) and
// returns metadata for every article whose body links to the target slug, in either internal link
// form. Self-links are skipped. Results are sorted by UpdatedAt descending.
func (s *Storage) GetBacklinks(targetSlug string) ([]Article, error) {
	cleanedTarget := Slugify(targetSlug)
	if cleanedTarget == "" {
		return nil, nil
	}

	// Walk the article directory directly rather than going through ListArticles and then
	// re-reading each file: link targets are cached alongside metadata, so an unchanged wiki
	// costs one stat per file instead of a full read and Markdown scan.
	var backlinks []Article
	err := filepath.WalkDir(s.ArticleDir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() || filepath.Ext(path) != ".md" {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil // unreadable file: skip rather than fail the whole scan
		}

		_, meta, err := s.cachedMeta(path, info)
		if err != nil {
			return nil
		}
		// "home" is excluded from listings but may still hold links, so it is included here.
		if meta.Slug == cleanedTarget {
			return nil // self-links are not backlinks
		}

		refs, err := s.cachedLinkTargets(path, info)
		if err != nil {
			return nil
		}
		for _, ref := range refs {
			if ref.Slug == cleanedTarget {
				backlinks = append(backlinks, *meta)
				break
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	sort.Slice(backlinks, func(i, j int) bool {
		return backlinks[i].Timestamp.After(backlinks[j].Timestamp)
	})

	return backlinks, nil
}

// RewriteWikiLinks rewrites every [[Target]] / [[Target|display]] WikiLink, whose target
// resolves (via Slugify) to oldSlug, so it points at the newTitle instead, preserving any
// display-text alias. It returns the rewritten content and whether any link changed.
// Both [[Title]] and [[slug]] forms are matched because both Slugify to the same value.
func RewriteWikiLinks(content, oldSlug, newTitle string) (string, bool) {
	cleanedOld := Slugify(oldSlug)
	if cleanedOld == "" {
		return content, false
	}

	var b strings.Builder
	changed := false
	rest := content
	for {
		startIdx := strings.Index(rest, "[[")
		if startIdx == -1 {
			b.WriteString(rest)
			break
		}
		endRel := strings.Index(rest[startIdx:], "]]")
		if endRel == -1 {
			b.WriteString(rest)
			break
		}
		endIdx := startIdx + endRel

		// Emit everything up to and including the opening brackets.
		b.WriteString(rest[:startIdx+2])
		linkContent := rest[startIdx+2 : endIdx]

		target := linkContent
		alias := ""
		if pipeIdx := strings.Index(linkContent, "|"); pipeIdx != -1 {
			target = linkContent[:pipeIdx]
			alias = linkContent[pipeIdx:] // includes the leading '|'
		}

		if Slugify(strings.TrimSpace(target)) == cleanedOld {
			b.WriteString(newTitle + alias)
			changed = true
		} else {
			b.WriteString(linkContent)
		}
		b.WriteString("]]")
		rest = rest[endIdx+2:]
	}

	return b.String(), changed
}

// RewriteArticlePathLinks rewrites every absolute Markdown link whose destination resolves to
// oldSlug — [text](/articles/<oldSlug>) — so it points at newSlug instead. It returns the
// rewritten content and whether any link changed.
//
// Only the destination changes; the link text is left exactly as written. That is the same
// guarantee [[old|display]] already gets from RewriteWikiLinks, and it is what makes healing this
// form safe: a Markdown destination is a path, not prose. Note the asymmetry with
// RewriteWikiLinks, which substitutes the new *title* — a WikiLink target is a title, a Markdown
// destination is a slug.
//
// Any #fragment or ?query on the destination is preserved: a link into a section of the renamed
// article should still land on that section.
func RewriteArticlePathLinks(content, oldSlug, newSlug string) (string, bool) {
	cleanedOld := Slugify(oldSlug)
	cleanedNew := Slugify(newSlug)
	if cleanedOld == "" || cleanedNew == "" || cleanedOld == cleanedNew {
		return content, false
	}

	changed := false
	rewritten := articlePathLink.ReplaceAllStringFunc(content, func(m string) string {
		groups := articlePathLink.FindStringSubmatch(m)
		if len(groups) != 4 {
			return m
		}
		dest, suffix := groups[3], ""
		if idx := strings.IndexAny(dest, "#?"); idx != -1 {
			dest, suffix = dest[:idx], dest[idx:]
		}
		if Slugify(strings.TrimSuffix(dest, "/")) != cleanedOld {
			return m
		}
		changed = true
		// groups[1] is the character the pattern had to consume to reject the image form and
		// groups[2] is "[text](/articles/"; both go back verbatim.
		return groups[1] + groups[2] + cleanedNew + suffix
	})

	return rewritten, changed
}

// TranslateWikiLinksToBundlePaths rewrites [[Target]] / [[Target|alias]] WikiLinks into
// bundle-relative Markdown links ([alias](/dir/slug.md)) for OKF export (§5.1). The slug→path
// map provides each concept's location in the emitted tree. Links whose target is not in the
// map are left as WikiLinks (broken-link tolerance, OKF §5).
func TranslateWikiLinksToBundlePaths(content string, pathForSlug map[string]string) string {
	var b strings.Builder
	rest := content
	for {
		start := strings.Index(rest, "[[")
		if start == -1 {
			b.WriteString(rest)
			break
		}
		endRel := strings.Index(rest[start:], "]]")
		if endRel == -1 {
			b.WriteString(rest)
			break
		}
		end := start + endRel
		b.WriteString(rest[:start])
		inner := rest[start+2 : end]

		target := inner
		alias := inner
		if pipe := strings.Index(inner, "|"); pipe != -1 {
			target = inner[:pipe]
			alias = inner[pipe+1:]
		}
		slug := Slugify(strings.TrimSpace(target))
		if p, ok := pathForSlug[slug]; ok {
			b.WriteString("[" + strings.TrimSpace(alias) + "](" + p + ")")
		} else {
			// Unknown target: keep the original WikiLink so broken links survive the round-trip.
			b.WriteString(rest[start : end+2])
		}
		rest = rest[end+2:]
	}
	return b.String()
}

// bundleMarkdownLink matches a Markdown link whose destination is a (possibly bundle-relative)
// path ending in .md — e.g. [text](/wiki/foo.md) or [text](../aiplans/bar.md).
var bundleMarkdownLink = regexp.MustCompile(`\[([^]]*)]\(([^)]+\.md)\)`)

// TranslateBundleLinksToWikiLinks rewrites bundle-relative Markdown links back into [[slug]]
// WikiLinks for OKF import. The slug is derived from the link's filename (Slugify of the basename
// without .md). The alias is preserved as [[slug|text]] when it differs from the slug.
func TranslateBundleLinksToWikiLinks(content string) string {
	return bundleMarkdownLink.ReplaceAllStringFunc(content, func(m string) string {
		groups := bundleMarkdownLink.FindStringSubmatch(m)
		if len(groups) != 3 {
			return m
		}
		text := strings.TrimSpace(groups[1])
		dest := groups[2]
		base := strings.TrimSuffix(path.Base(dest), ".md")
		slug := Slugify(base)
		if slug == "" {
			return m
		}
		if text != "" && Slugify(text) != slug {
			return "[[" + slug + "|" + text + "]]"
		}
		return "[[" + slug + "]]"
	})
}

// slugMentionCandidate matches a slug-shaped token: lowercase alphanumerics in two or more
// hyphen-separated parts. Requiring a hyphen keeps the extracted set small — a single-word slug
// is not detected, which is a deliberate trade. Skills are slugified from their titles, and a
// one-word skill title is rare; the cost of missing one is a hint that says "nothing references
// this" about something that is referenced, which the reader resolves by looking.
var slugMentionCandidate = regexp.MustCompile(`[a-z0-9]+(?:-[a-z0-9]+)+`)

// markdownCodeRegions matches fenced code blocks and inline code spans — the inverse of what
// stripCodeForLinkScan blanks out.
var markdownCodeRegions = regexp.MustCompile("(?s)```.*?```|~~~.*?~~~|`[^`\n]+`")

// ExtractSlugMentions returns the slug-shaped tokens appearing inside Markdown code — fenced
// blocks and inline spans — deduplicated and sorted.
//
// This exists because a skill is not referenced the way an article is. Articles are linked;
// skills are *invoked*, by a `read_article(slug: "…")` call written into another document's
// prose, or named in a backticked slug reference. Neither form is a link, so ScanLinkGraph never
// sees them and get_backlinks reports zero.
//
// Measured on the real corpus: nexwiki-agent-core-guidelines referenced
// enhanced-memory-decision-making-skill four times in exactly these forms, and
// get_backlinks(enhanced-memory-decision-making-skill) still returned 0. create-plan-skill, a
// live and wanted skill, also reports 0 inbound links. An unreferenced-skill check built on the
// link graph alone would therefore flag the healthy skill and stay silent on the dead one.
//
// Code regions are scanned rather than avoided, and fenced blocks are included rather than
// skipped, because being generous about what counts as a reference errs toward silence — the
// safe direction for a hint whose remedy is "reference this or retire it".
func ExtractSlugMentions(content string) []string {
	seen := map[string]bool{}
	var mentions []string
	for _, region := range markdownCodeRegions.FindAllString(content, -1) {
		for _, token := range slugMentionCandidate.FindAllString(region, -1) {
			if seen[token] {
				continue
			}
			seen[token] = true
			mentions = append(mentions, token)
		}
	}
	sort.Strings(mentions)
	return mentions
}
