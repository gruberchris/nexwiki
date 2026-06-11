package server

import (
	"sort"
	"strings"
)

// ExtractWikiLinkTargets returns the raw (pre-Slugify) targets of all [[Target]] and
// [[Target|display text]] WikiLinks found in a Markdown body, in order of appearance.
func ExtractWikiLinkTargets(content string) []string {
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

	metas, err := s.ListArticles()
	if err != nil {
		return nil, err
	}

	slugs := make([]string, 0, len(metas)+1)
	slugs = append(slugs, "home") // excluded from ListArticles but may hold links
	for _, m := range metas {
		slugs = append(slugs, m.Slug)
	}

	var backlinks []Article
	for _, slug := range slugs {
		if slug == cleanedTarget {
			continue
		}
		art, err := s.GetArticle(slug)
		if err != nil {
			continue
		}
		for _, target := range ExtractWikiLinkTargets(art.Content) {
			if Slugify(target) == cleanedTarget {
				meta := *art
				meta.Content = "" // metadata only
				backlinks = append(backlinks, meta)
				break
			}
		}
	}

	sort.Slice(backlinks, func(i, j int) bool {
		return backlinks[i].UpdatedAt.After(backlinks[j].UpdatedAt)
	})

	return backlinks, nil
}
