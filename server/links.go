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

// GetBacklinks scans all articles (including the home dashboard, which listings exclude)
// and returns metadata for every article whose body links to the target slug via a WikiLink.
// Self-links are skipped. Results are sorted by UpdatedAt descending.
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

		targets, err := s.cachedLinkTargets(path, info)
		if err != nil {
			return nil
		}
		for _, target := range targets {
			if target == cleanedTarget {
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
