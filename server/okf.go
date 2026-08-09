package server

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"path"
	"sort"
	"strings"
	"time"
)

// OKFVersion is the Open Knowledge Format spec version NexWiki emits in the exported bundle root.
const OKFVersion = "0.1"

// okfDirLabels maps a bundle subdirectory to a human label for the synthesized index headings.
var okfDirLabels = map[string]string{
	"wiki":       "Wiki Articles",
	"aimemories": "Agent Memories",
	"aiplans":    "Agent Plans",
	"aiskills":   "Agent Skills",
}

// ExportOKFBundle serializes the entire knowledge base into a conformant OKF v0.1 bundle (a zip).
// Native files are already OKF YAML, so export mainly synthesizes the bundle hierarchy (by type),
// the reserved index.md files, a date-grouped log.md, and translates WikiLinks to bundle paths.
func (s *Storage) ExportOKFBundle() ([]byte, error) {
	// Export genuinely needs every body, so the GetArticle pass below is unavoidable. What is
	// avoidable is the metadata pass: ListArticles is cache-backed now, so on a warm cache it
	// costs a stat per file instead of a second full read and parse of the whole wiki.
	metas, err := s.ListArticles()
	if err != nil {
		return nil, err
	}

	var full []*Article
	pathForSlug := make(map[string]string)
	for _, m := range metas {
		a, err := s.GetArticle(m.Slug)
		if err != nil {
			continue
		}
		full = append(full, a)
		dir := getArticleDirectory(a.Type)
		pathForSlug[a.Slug] = "/" + dir + "/" + a.Slug + ".md"
	}

	grouped := make(map[string][]*Article)
	for _, a := range full {
		dir := getArticleDirectory(a.Type)
		grouped[dir] = append(grouped[dir], a)
	}

	buf := new(bytes.Buffer)
	zw := zip.NewWriter(buf)

	writeFile := func(name, content string) error {
		w, err := zw.Create(name)
		if err != nil {
			return err
		}
		_, err = io.WriteString(w, content)
		return err
	}

	// Concept files: existing OKF frontmatter as-is + body with links translated to bundle paths.
	for dir, arts := range grouped {
		for _, a := range arts {
			body := TranslateWikiLinksToBundlePaths(a.Content, pathForSlug)
			if err := writeFile(dir+"/"+a.Slug+".md", serializeFrontMatter(a)+body); err != nil {
				return nil, err
			}
		}
	}

	// Per-directory index.md (no frontmatter): "* [Title](path) - description".
	dirs := make([]string, 0, len(grouped))
	for dir := range grouped {
		dirs = append(dirs, dir)
	}
	sort.Strings(dirs)
	for _, dir := range dirs {
		arts := grouped[dir]
		sort.Slice(arts, func(i, j int) bool { return arts[i].Title < arts[j].Title })
		var b strings.Builder
		label := okfDirLabels[dir]
		if label == "" {
			label = dir
		}
		b.WriteString("# " + label + "\n\n")
		for _, a := range arts {
			summary := a.Description
			if summary == "" {
				summary = a.ContentPreview
			}
			_, _ = fmt.Fprintf(&b, "* [%s](/%s/%s.md)", a.Title, dir, a.Slug)
			if summary != "" {
				b.WriteString(" - " + summary)
			}
			b.WriteString("\n")
		}
		if err := writeFile(dir+"/index.md", b.String()); err != nil {
			return nil, err
		}
	}

	// Root index.md: spec-version declaration + home context + links into each section index.
	var root strings.Builder
	root.WriteString("---\nokf_version: \"" + OKFVersion + "\"\n---\n\n")
	root.WriteString("# Knowledge Base\n\n")
	if home, err := s.GetArticle("home"); err == nil {
		root.WriteString(TranslateWikiLinksToBundlePaths(home.Content, pathForSlug))
		root.WriteString("\n\n")
	}
	for _, dir := range dirs {
		label := okfDirLabels[dir]
		if label == "" {
			label = dir
		}
		_, _ = fmt.Fprintf(&root, "* [%s](/%s/index.md) (%d)\n", label, dir, len(grouped[dir]))
	}
	if err := writeFile("index.md", root.String()); err != nil {
		return nil, err
	}

	// log.md: date-grouped activity history, newest first.
	if err := writeFile("log.md", s.buildOKFLog()); err != nil {
		return nil, err
	}

	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// buildOKFLog renders the durable activity log as a date-grouped Markdown document (newest first).
func (s *Storage) buildOKFLog() string {
	events, err := ReadActivityLog(ActivityLogPath(s.DataDir), time.Time{}, 0, "", "")
	if err != nil || len(events) == 0 {
		return "# Activity Log\n\n(No recorded activity.)\n"
	}
	// ReadActivityLog returns oldest-first; iterate in reverse for newest-first grouping.
	var b strings.Builder
	b.WriteString("# Activity Log\n\n")
	lastDay := ""
	for i := len(events) - 1; i >= 0; i-- {
		ev := events[i]
		day := ev.Timestamp.Format("2006-01-02")
		if day != lastDay {
			b.WriteString("\n## " + day + "\n\n")
			lastDay = day
		}
		tool := ev.Tool
		if tool == "" {
			tool = "web-ui"
		}
		_, _ = fmt.Fprintf(&b, "- %s [%s/%s] %s", ev.Timestamp.Format("15:04:05"), ev.Source, ev.Action, tool)
		if ev.Title != "" || ev.Slug != "" {
			_, _ = fmt.Fprintf(&b, " → %s (%s)", ev.Title, ev.Slug)
		}
		b.WriteString("\n")
	}
	return b.String()
}

// Decompression limits for imported bundles. A zip archive can expand to orders of magnitude more
// than its compressed size, so the *decompressed* output must be bounded independently of the
// upload size cap — otherwise a few MB of crafted (or merely very compressible) input exhausts
// memory. Documents larger than the per-entry cap are skipped with a warning rather than aborting
// the whole import, matching the importer's permissive posture.
const (
	maxBundleEntries    = 10_000           // ceiling on concept documents in one bundle
	maxBundleEntryBytes = 8 << 20          // 8 MB per decompressed document
	maxBundleTotalBytes = int64(256 << 20) // 256 MB decompressed across the whole bundle
)

// OKFImportReport summarizes the outcome of importing a bundle, including a permissive conformance
// report (OKF §9): documents missing a type are defaulted to Wiki and flagged rather than rejected.
type OKFImportReport struct {
	Imported    int      `json:"imported"`
	Skipped     int      `json:"skipped"`
	MissingType []string `json:"missing_type"`
	Warnings    []string `json:"warnings"`
}

// ImportOKFBundle walks a zipped OKF bundle, parsing each non-reserved .md as an OKF concept
// document, mapping its type, translating bundle links back to WikiLinks, and creating/updating
// articles via SaveArticle. Reserved index.md/log.md files are consumed (skipped). The importer is
// permissive: malformed or typeless documents are flagged in the report rather than aborting.
func (s *Storage) ImportOKFBundle(data []byte) (*OKFImportReport, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("invalid bundle archive: %w", err)
	}

	report := &OKFImportReport{}
	var totalDecompressed int64
	entriesSeen := 0
	for _, f := range zr.File {
		if f.FileInfo().IsDir() || !strings.HasSuffix(f.Name, ".md") {
			continue
		}
		base := path.Base(f.Name)
		// Skip reserved synthesized files.
		if base == "index.md" || base == "log.md" {
			report.Skipped++
			continue
		}

		entriesSeen++
		if entriesSeen > maxBundleEntries {
			report.Warnings = append(report.Warnings,
				fmt.Sprintf("bundle exceeds the %d-document limit; remaining entries were not imported", maxBundleEntries))
			report.Skipped++
			break
		}
		if totalDecompressed >= maxBundleTotalBytes {
			report.Warnings = append(report.Warnings,
				fmt.Sprintf("bundle exceeds the %d MB decompressed limit; remaining entries were not imported", maxBundleTotalBytes>>20))
			report.Skipped++
			break
		}

		rc, err := f.Open()
		if err != nil {
			report.Warnings = append(report.Warnings, fmt.Sprintf("%s: %v", f.Name, err))
			continue
		}
		// Read at most one byte beyond the cap so an oversized entry is detectable, and bound the
		// read by the decompressed size rather than trusting the archive's declared sizes.
		raw, err := io.ReadAll(io.LimitReader(rc, maxBundleEntryBytes+1))
		_ = rc.Close()
		if err != nil {
			report.Warnings = append(report.Warnings, fmt.Sprintf("%s: %v", f.Name, err))
			continue
		}
		if len(raw) > maxBundleEntryBytes {
			report.Warnings = append(report.Warnings,
				fmt.Sprintf("%s: document exceeds the %d MB per-file limit; skipped", f.Name, maxBundleEntryBytes>>20))
			report.Skipped++
			continue
		}
		totalDecompressed += int64(len(raw))

		art, err := parseArticleFile(raw, true)
		if err != nil {
			report.Warnings = append(report.Warnings, fmt.Sprintf("%s: not a valid OKF concept document: %v", f.Name, err))
			report.Skipped++
			continue
		}

		// Permissive typing (OKF §9): an unrecognized or missing type is defaulted to Wiki and
		// flagged rather than rejected.
		//
		// This reads DeclaredType, the raw front-matter value, not Type. parseArticleFile
		// normalizes Type on the way out, so by the time the document arrives here Type is always
		// one of the four canonical values — the checks below used to compare against it and could
		// therefore never fire, leaving MissingType permanently empty. The coercion happened; only
		// the report of it was missing, which is the half that makes permissiveness auditable.
		if art.DeclaredType == "" || normalizeType(art.DeclaredType) != art.DeclaredType {
			report.MissingType = append(report.MissingType, art.Slug)
		}

		// Determine the existing slug to update (so re-imports do not duplicate).
		oldSlug := ""
		if _, err := s.GetArticle(art.Slug); err == nil {
			oldSlug = art.Slug
		}

		body := TranslateBundleLinksToWikiLinks(art.Content)
		summary := "Imported from OKF bundle"
		if _, err := s.SaveArticle(oldSlug, art.Title, body, art.Description, art.Source, art.Resource, summary, art.Tags, normalizeType(art.Type)); err != nil {
			report.Warnings = append(report.Warnings, fmt.Sprintf("%s: save failed: %v", f.Name, err))
			continue
		}
		report.Imported++
	}

	return report, nil
}
