package server

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/blevesearch/bleve/v2"
	"gopkg.in/yaml.v3"
)

// OKFGenerated records who or what produced the content and when (§5.2).
type OKFGenerated struct {
	By string    `json:"by" yaml:"by"`
	At time.Time `json:"at" yaml:"at"`
}

func (g *OKFGenerated) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind == yaml.AliasNode {
		value = value.Alias
	}
	if value.Kind != yaml.MappingNode {
		return fmt.Errorf("generated must be a mapping")
	}
	for i := 0; i < len(value.Content); i += 2 {
		key := value.Content[i].Value
		valNode := value.Content[i+1]
		if valNode.Kind == yaml.AliasNode {
			valNode = valNode.Alias
		}
		switch key {
		case "by":
			g.By = strings.TrimSpace(valNode.Value)
		case "at":
			t, err := parseISO8601(valNode.Value)
			if err != nil {
				return fmt.Errorf("invalid generated 'at' timestamp: %w", err)
			}
			g.At = t
		}
	}
	return nil
}

func (g OKFGenerated) MarshalYAML() (interface{}, error) {
	type raw struct {
		By string `yaml:"by"`
		At string `yaml:"at,omitempty"`
	}
	r := raw{By: g.By}
	if !g.At.IsZero() {
		r.At = g.At.UTC().Format(time.RFC3339)
	}
	return r, nil
}

func (g *OKFGenerated) UnmarshalJSON(data []byte) error {
	var aux struct {
		By string `json:"by"`
		At string `json:"at"`
	}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	g.By = aux.By
	if aux.At != "" {
		t, err := parseISO8601(aux.At)
		if err != nil {
			return err
		}
		g.At = t
	}
	return nil
}

func (g OKFGenerated) MarshalJSON() ([]byte, error) {
	type raw struct {
		By string `json:"by"`
		At string `json:"at,omitempty"`
	}
	r := raw{By: g.By}
	if !g.At.IsZero() {
		r.At = g.At.UTC().Format(time.RFC3339)
	}
	return json.Marshal(r)
}

// OKFVerification records who or what confirmed the content against its sources or resource (§5.2).
type OKFVerification struct {
	By string    `json:"by" yaml:"by"`
	At time.Time `json:"at" yaml:"at"`
}

func (v *OKFVerification) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind == yaml.AliasNode {
		value = value.Alias
	}
	if value.Kind != yaml.MappingNode {
		return fmt.Errorf("verification must be a mapping")
	}
	for i := 0; i < len(value.Content); i += 2 {
		key := value.Content[i].Value
		valNode := value.Content[i+1]
		if valNode.Kind == yaml.AliasNode {
			valNode = valNode.Alias
		}
		switch key {
		case "by":
			v.By = strings.TrimSpace(valNode.Value)
		case "at":
			t, err := parseISO8601(valNode.Value)
			if err != nil {
				return fmt.Errorf("invalid verification 'at' timestamp: %w", err)
			}
			v.At = t
		}
	}
	return nil
}

func (v OKFVerification) MarshalYAML() (interface{}, error) {
	type raw struct {
		By string `yaml:"by"`
		At string `yaml:"at,omitempty"`
	}
	r := raw{By: v.By}
	if !v.At.IsZero() {
		r.At = v.At.UTC().Format(time.RFC3339)
	}
	return r, nil
}

func (v *OKFVerification) UnmarshalJSON(data []byte) error {
	var aux struct {
		By string `json:"by"`
		At string `json:"at"`
	}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	v.By = aux.By
	if aux.At != "" {
		t, err := parseISO8601(aux.At)
		if err != nil {
			return err
		}
		v.At = t
	}
	return nil
}

func (v OKFVerification) MarshalJSON() ([]byte, error) {
	type raw struct {
		By string `json:"by"`
		At string `json:"at,omitempty"`
	}
	r := raw{By: v.By}
	if !v.At.IsZero() {
		r.At = v.At.UTC().Format(time.RFC3339)
	}
	return json.Marshal(r)
}

// OKFVerificationList supports unmarshaling both a single bare mapping `{ by, at }`
// and a sequence of mappings `[{ by, at }]` per OKF spec §5.2 and §11.
type OKFVerificationList []OKFVerification

func (vl *OKFVerificationList) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind == yaml.AliasNode {
		value = value.Alias
	}
	if value.Kind == yaml.MappingNode {
		var single OKFVerification
		if err := single.UnmarshalYAML(value); err != nil {
			return err
		}
		*vl = []OKFVerification{single}
		return nil
	}
	if value.Kind == yaml.SequenceNode {
		var list []OKFVerification
		for _, item := range value.Content {
			var v OKFVerification
			if err := v.UnmarshalYAML(item); err != nil {
				return err
			}
			list = append(list, v)
		}
		*vl = list
		return nil
	}
	return nil
}

func (vl OKFVerificationList) MarshalYAML() (interface{}, error) {
	if len(vl) == 0 {
		return nil, nil
	}
	type raw struct {
		By string `yaml:"by"`
		At string `yaml:"at,omitempty"`
	}
	list := make([]raw, len(vl))
	for i, v := range vl {
		list[i] = raw{By: v.By}
		if !v.At.IsZero() {
			list[i].At = v.At.UTC().Format(time.RFC3339)
		}
	}
	return list, nil
}

func (vl *OKFVerificationList) UnmarshalJSON(data []byte) error {
	data = bytes.TrimSpace(data)
	if len(data) == 0 || string(data) == "null" {
		*vl = nil
		return nil
	}
	if data[0] == '{' {
		var single OKFVerification
		if err := json.Unmarshal(data, &single); err != nil {
			return err
		}
		*vl = []OKFVerification{single}
		return nil
	}
	var list []OKFVerification
	if err := json.Unmarshal(data, &list); err != nil {
		return err
	}
	*vl = list
	return nil
}

// OKFUsageWindow specifies a datetime range framing usage count signals (§5.1).
type OKFUsageWindow struct {
	From time.Time `json:"from,omitzero" yaml:"from,omitempty"`
	To   time.Time `json:"to,omitzero" yaml:"to,omitempty"`
}

func (w *OKFUsageWindow) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind == yaml.AliasNode {
		value = value.Alias
	}
	if value.Kind != yaml.MappingNode {
		return fmt.Errorf("usage_window must be a mapping")
	}
	for i := 0; i < len(value.Content); i += 2 {
		key := value.Content[i].Value
		valNode := value.Content[i+1]
		if valNode.Kind == yaml.AliasNode {
			valNode = valNode.Alias
		}
		switch key {
		case "from":
			t, err := parseISO8601(valNode.Value)
			if err != nil {
				return fmt.Errorf("invalid usage_window 'from' timestamp: %w", err)
			}
			w.From = t
		case "to":
			t, err := parseISO8601(valNode.Value)
			if err != nil {
				return fmt.Errorf("invalid usage_window 'to' timestamp: %w", err)
			}
			w.To = t
		}
	}
	return nil
}

func (w OKFUsageWindow) MarshalYAML() (interface{}, error) {
	type raw struct {
		From string `yaml:"from,omitempty"`
		To   string `yaml:"to,omitempty"`
	}
	var r raw
	if !w.From.IsZero() {
		r.From = w.From.UTC().Format(time.RFC3339)
	}
	if !w.To.IsZero() {
		r.To = w.To.UTC().Format(time.RFC3339)
	}
	return r, nil
}

func (w *OKFUsageWindow) UnmarshalJSON(data []byte) error {
	var aux struct {
		From string `json:"from"`
		To   string `json:"to"`
	}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	if aux.From != "" {
		t, err := parseISO8601(aux.From)
		if err != nil {
			return err
		}
		w.From = t
	}
	if aux.To != "" {
		t, err := parseISO8601(aux.To)
		if err != nil {
			return err
		}
		w.To = t
	}
	return nil
}

func (w OKFUsageWindow) MarshalJSON() ([]byte, error) {
	type raw struct {
		From string `json:"from,omitempty"`
		To   string `json:"to,omitempty"`
	}
	var r raw
	if !w.From.IsZero() {
		r.From = w.From.UTC().Format(time.RFC3339)
	}
	if !w.To.IsZero() {
		r.To = w.To.UTC().Format(time.RFC3339)
	}
	return json.Marshal(r)
}

// OKFSource represents a material a concept derives from, carrying optional credibility signals (§5.1).
type OKFSource struct {
	ID           string          `json:"id,omitempty" yaml:"id,omitempty"`
	Resource     string          `json:"resource" yaml:"resource"`
	Title        string          `json:"title,omitempty" yaml:"title,omitempty"`
	Author       string          `json:"author,omitempty" yaml:"author,omitempty"`
	UsageCount   *int            `json:"usage_count,omitempty" yaml:"usage_count,omitempty"`
	LastModified *time.Time      `json:"last_modified,omitzero" yaml:"last_modified,omitempty"`
	UsageWindow  *OKFUsageWindow `json:"usage_window,omitempty" yaml:"usage_window,omitempty"`
}

func (s *OKFSource) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind == yaml.AliasNode {
		value = value.Alias
	}
	if value.Kind != yaml.MappingNode {
		return fmt.Errorf("source must be a mapping")
	}
	for i := 0; i < len(value.Content); i += 2 {
		key := value.Content[i].Value
		valNode := value.Content[i+1]
		if valNode.Kind == yaml.AliasNode {
			valNode = valNode.Alias
		}
		switch key {
		case "id":
			s.ID = strings.TrimSpace(valNode.Value)
		case "resource":
			s.Resource = strings.TrimSpace(valNode.Value)
		case "title":
			s.Title = strings.TrimSpace(valNode.Value)
		case "author":
			s.Author = strings.TrimSpace(valNode.Value)
		case "usage_count":
			if valNode.Value != "" {
				count, err := strconv.Atoi(strings.TrimSpace(valNode.Value))
				if err != nil {
					return fmt.Errorf("invalid usage_count: %w", err)
				}
				s.UsageCount = &count
			}
		case "last_modified":
			if valNode.Value != "" {
				t, err := parseISO8601(valNode.Value)
				if err != nil {
					return fmt.Errorf("invalid last_modified: %w", err)
				}
				s.LastModified = &t
			}
		case "usage_window":
			var uw OKFUsageWindow
			if err := uw.UnmarshalYAML(valNode); err != nil {
				return err
			}
			s.UsageWindow = &uw
		}
	}
	return nil
}

func (s OKFSource) MarshalYAML() (interface{}, error) {
	type raw struct {
		ID           string          `yaml:"id,omitempty"`
		Resource     string          `yaml:"resource"`
		Title        string          `yaml:"title,omitempty"`
		Author       string          `yaml:"author,omitempty"`
		UsageCount   *int            `yaml:"usage_count,omitempty"`
		LastModified string          `yaml:"last_modified,omitempty"`
		UsageWindow  *OKFUsageWindow `yaml:"usage_window,omitempty"`
	}
	r := raw{
		ID:          s.ID,
		Resource:    s.Resource,
		Title:       s.Title,
		Author:      s.Author,
		UsageCount:  s.UsageCount,
		UsageWindow: s.UsageWindow,
	}
	if s.LastModified != nil && !s.LastModified.IsZero() {
		r.LastModified = s.LastModified.UTC().Format(time.RFC3339)
	}
	return r, nil
}

func (s *OKFSource) UnmarshalJSON(data []byte) error {
	var aux struct {
		ID           string          `json:"id,omitempty"`
		Resource     string          `json:"resource"`
		Title        string          `json:"title,omitempty"`
		Author       string          `json:"author,omitempty"`
		UsageCount   *int            `json:"usage_count,omitempty"`
		LastModified string          `json:"last_modified,omitempty"`
		UsageWindow  *OKFUsageWindow `json:"usage_window,omitempty"`
	}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	s.ID = aux.ID
	s.Resource = aux.Resource
	s.Title = aux.Title
	s.Author = aux.Author
	s.UsageCount = aux.UsageCount
	s.UsageWindow = aux.UsageWindow
	if aux.LastModified != "" {
		t, err := parseISO8601(aux.LastModified)
		if err != nil {
			return err
		}
		s.LastModified = &t
	}
	return nil
}

func (s OKFSource) MarshalJSON() ([]byte, error) {
	type raw struct {
		ID           string          `json:"id,omitempty"`
		Resource     string          `json:"resource"`
		Title        string          `json:"title,omitempty"`
		Author       string          `json:"author,omitempty"`
		UsageCount   *int            `json:"usage_count,omitempty"`
		LastModified string          `json:"last_modified,omitempty"`
		UsageWindow  *OKFUsageWindow `json:"usage_window,omitempty"`
	}
	r := raw{
		ID:          s.ID,
		Resource:    s.Resource,
		Title:       s.Title,
		Author:      s.Author,
		UsageCount:  s.UsageCount,
		UsageWindow: s.UsageWindow,
	}
	if s.LastModified != nil && !s.LastModified.IsZero() {
		r.LastModified = s.LastModified.UTC().Format(time.RFC3339)
	}
	return json.Marshal(r)
}

// OKFParameter represents a typed, named parameter for an Attested Computation (§10.2).
type OKFParameter struct {
	Name     string `json:"name" yaml:"name"`
	Type     string `json:"type" yaml:"type"`
	Required bool   `json:"required,omitempty" yaml:"required,omitempty"`
}

// OKFExecutor specifies how an Attested Computation is executed and its receipt contract (§10.2).
type OKFExecutor struct {
	Resource string   `json:"resource" yaml:"resource"`
	Receipt  []string `json:"receipt,omitempty" yaml:"receipt,omitempty"`
}

// OKFAttester specifies the deterministic attestation code for an Attested Computation (§10.2).
type OKFAttester struct {
	Resource string `json:"resource" yaml:"resource"`
}

// parseISO8601 parses standard ISO 8601 and RFC 3339 timestamp representations.
func parseISO8601(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, nil
	}
	formats := []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02T15:04:05-0700",
		"2006-01-02T15:04:05",
		"2006-01-02 15:04:05",
		"2006-01-02",
	}
	for _, f := range formats {
		if t, err := time.Parse(f, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("invalid ISO 8601 timestamp: %s", s)
}

// IsStale reports whether an article has exceeded its stale_after timestamp at the current time (§5.5).
func IsStale(art *Article) bool {
	return IsStaleAt(art, time.Now())
}

// IsStaleAt reports whether an article is stale at a specific instant (§5.5).
// A concept is stale when now >= stale_after.
func IsStaleAt(art *Article, now time.Time) bool {
	if art == nil || art.StaleAfter.IsZero() {
		return false
	}
	return !now.Before(art.StaleAfter)
}

// Article represents a wiki article page. On disk, it is a conformant OKF concept document
// with real YAML front-matter and a Markdown content body. In OKF v0.2, it supports provenance
// (sources), trust (generated, verified, trust_tier), freshness (stale_after, is_stale), and
// attested computations.
type Article struct {
	Type        string    `json:"type"` // OKF doc-class: Wiki, AI-Agent-Memory, AI-Agent-Plan, AI-Agent-Skill, Attested Computation
	Title       string    `json:"title"`
	Slug        string    `json:"slug"`
	CreatedAt   time.Time `json:"created_at"`
	Timestamp   time.Time `json:"timestamp"` // Canonical modified-time (OKF), synchronized with generated.at
	Content     string    `json:"content,omitempty"`
	Description string    `json:"description,omitempty"` // OKF one-line summary shown in indexes
	Resource    string    `json:"resource,omitempty"`    // OKF canonical URI of what the concept *is*
	Source      string    `json:"source,omitempty"`      // Provenance: legacy source, synchronized with sources[0].resource
	// Version is always reported, including 0. It used to carry `omitempty`, which dropped the
	// field entirely for articles written to disk before versioning existed — so read_article's
	// structured output had no `version` at all, and the documented loop of feeding it straight
	// back to edit_wiki_article as `loaded_version` dead-ended on exactly those articles.
	Version     int      `json:"version"`
	EditSummary string   `json:"edit_summary,omitempty"` // Summary of edits
	Tags        []string `json:"tags,omitempty"`         // Tags list (system and free user tags)
	// omitzero, not omitempty: encoding/json's omitempty does NOT drop a zero-valued struct, so
	// `omitempty` emitted "0001-01-01T00:00:00Z" on every unarchived document — a string every
	// JSON consumer reads as truthy. That hid every document from the dashboard and sidebar.
	ArchivedAt time.Time `json:"archived_at,omitzero"` // When the article was archived
	// Status is the document's lifecycle state — a single value, deliberately not a tag. Plans
	// and skills validate it against a closed vocabulary (see tags.go); wiki articles and
	// memories may use any value or none.
	Status string `json:"status,omitempty"`
	// StatusChangedAt records when a plan last changed lifecycle status. It exists because the
	// article Timestamp cannot drive the lifecycle timers — fixing a typo in a completed plan
	// would restart its archive clock. Only ever set on AI-Agent-Plan documents; the lifecycle
	// worker treats a missing value as "not yet eligible", never as "infinitely old".
	StatusChangedAt time.Time `json:"status_changed_at,omitzero"`
	// MemoryKind classifies what sort of fact a memory holds, against the closed four-value
	// vocabulary in tags.go. Only ever set on AI-Agent-Memory documents; the *scope* axis (how
	// far the fact reaches) stays on the memory-<scope> tag, because scope is free-form and kind
	// is not. See MemoryKinds for the vocabulary and for the two shapes to avoid when ownership
	// eventually becomes a third axis.
	MemoryKind string `json:"memory_kind,omitempty"`

	// LockedBy/LockedAt mark a skill as agent-read-only (wikiskill evolution, story 01).
	// Only ever set on AI-Agent-Skill documents; a locked skill rejects agent-facing edits
	// and advances only through the skill-candidate promote path. The *scope* axis stays on
	// tags; this is a separate evolution lock, not a lifecycle status.
	LockedBy string    `json:"locked_by,omitempty"`
	LockedAt time.Time `json:"locked_at,omitzero"`

	// Trained-marker metadata (wikiskill evolution, story 06). Only ever set on
	// AI-Agent-Skill documents, and only by the harness promote path
	// (PromoteSkillCandidate): the gate accept that promotes a candidate stamps
	// trained_at/version/parent_version/candidate/val_score/eval_hash at the same
	// instant it writes the new skill version, so the marker can never name a
	// version the body does not match. TrainedRevokedAt/Reason record an explicit
	// rollback or human unlock — the metadata itself is history and is never
	// silently deleted. The matching `trained-wikiskill` tag (TrainedMarkerTag,
	// server/skill_trained.go) is the tool-managed marker agents cannot forge or
	// strip; the front-matter fields carry the evidence.
	TrainedAt            time.Time `json:"trained_at,omitzero"`
	TrainedVersion       int       `json:"trained_version,omitempty"`
	TrainedParentVersion int       `json:"trained_parent_version,omitempty"`
	TrainedCandidate     string    `json:"trained_candidate,omitempty"`
	TrainedValScore      float64   `json:"trained_val_score,omitempty"`
	TrainedEvalHash      string    `json:"trained_eval_hash,omitempty"`
	TrainedRevokedAt     time.Time `json:"trained_revoked_at,omitzero"`
	TrainedRevokedReason string    `json:"trained_revoked_reason,omitempty"`

	// OKF v0.2 core models: provenance, trust, and lifecycle (§5)
	Generated   *OKFGenerated     `json:"generated,omitempty"`
	Verified    []OKFVerification `json:"verified,omitempty"`
	TrustTier   string            `json:"trust_tier,omitempty"`
	Sources     []OKFSource       `json:"sources,omitempty"`
	UsageWindow *OKFUsageWindow   `json:"usage_window,omitempty"`
	StaleAfter  time.Time         `json:"stale_after,omitzero"`
	IsStale     bool              `json:"is_stale,omitempty"`

	// Attested Computation (§10)
	Runtime     string         `json:"runtime,omitempty"`
	Parameters  []OKFParameter `json:"parameters,omitempty"`
	Computation string         `json:"computation,omitempty"`
	Executor    *OKFExecutor   `json:"executor,omitempty"`
	Attester    *OKFAttester   `json:"attester,omitempty"`

	// ContentPreview holds the first content line during metadata-only parses
	// (used as a description fallback in indexes); never serialized.
	ContentPreview string `json:"-"`

	// DeclaredType is the raw `type` exactly as it appeared in the front matter, before
	// normalization coerced it. Type above is always one of the four canonical values, so it
	// cannot tell an explicit "Wiki" from a missing or unrecognized type — which is precisely the
	// distinction the OKF import report has to make when it flags a coerced document. Never
	// serialized; empty for documents built in memory rather than parsed.
	DeclaredType string `json:"-"`
}

// UpdateFreshness recomputes IsStale against the provided instant.
func (a *Article) UpdateFreshness(now time.Time) {
	a.IsStale = IsStaleAt(a, now)
}

// DeriveTrustTier returns the trust tier for the article's current verifications.
func (a *Article) DeriveTrustTier() string {
	return DeriveTrustTier(a.Verified)
}

// Storage manages persistent article files and uploaded assets on disk.
type Storage struct {
	DataDir    string
	ArticleDir string
	AssetDir   string
	HistoryDir string
	// CandidateDir holds skill-candidate JSON records (wikiskill evolution, story 01).
	// Candidates are data files, not OKF articles: they never enter the search index.
	CandidateDir string
	// JobDir holds headless evolution job records (wikiskill evolution, story 04).
	// Jobs are data files, not OKF articles: they never enter the search index.
	JobDir      string
	SearchIndex bleve.Index
	ThemeStore  *ThemeStore
	closeOnce   sync.Once

	// cache memoizes parsed article metadata and link targets, validated by file mtime+size so
	// edits made outside NexWiki are still picked up. See article_cache.go.
	cache *articleCache

	// writeMu serializes every mutation of the article tree. Writers arrive concurrently from
	// the HTTP API, the in-process MCP goroutine, and the Streamable HTTP transport; without
	// this, two writers can both scan the history directory, compute the same next version
	// number, and have one silently overwrite the other's revision snapshot.
	//
	// Methods suffixed "Locked" assume the caller already holds it (Go mutexes are not
	// reentrant, and the write paths call into one another — e.g. SaveArticle → link healing
	// → SaveArticle). Read paths intentionally do not take it: a reader racing a writer sees
	// either the old or the new file, which is indistinguishable from reading a moment sooner.
	//
	// This guards a single process. A `-mcp-only` sidecar writing the same data directory is
	// still unsynchronized; that needs an on-disk lock file.
	writeMu sync.Mutex
}

// NewStorage initializes and returns a Storage manager, ensuring required subdirectories exist.
func NewStorage(dataDir string) (*Storage, error) {
	articleDir := filepath.Join(dataDir, "articles")
	assetDir := filepath.Join(dataDir, "assets")
	indexPath := filepath.Join(dataDir, "search.bleve")

	if err := os.MkdirAll(articleDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create article directory: %w", err)
	}
	if err := os.MkdirAll(assetDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create asset directory: %w", err)
	}
	historyDir := filepath.Join(dataDir, "history")
	if err := os.MkdirAll(historyDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create history directory: %w", err)
	}
	candidateDir := filepath.Join(dataDir, "skill_candidates")
	if err := os.MkdirAll(candidateDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create skill candidate directory: %w", err)
	}
	jobDir := filepath.Join(dataDir, "skill_jobs")
	if err := os.MkdirAll(jobDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create evolution job directory: %w", err)
	}

	// Open or create the Bleve index, under a deadline.
	//
	// The deadline covers this call and nothing else. bleve.Open takes an exclusive bbolt lock on
	// the index directory and blocks *forever* if another process holds it — no error, no timeout
	// of its own — so a bound is the only way to tell "another instance owns this directory" from
	// "still working".
	//
	// It used to bound the whole of NewStorage from main.go, which quietly put seeding, the
	// one-time migration, and the boot index sync on the same 15-second budget. On a NAS the
	// status-field migration exceeded it and the process was killed mid-migration, reporting a
	// lock conflict that had not happened. Only the lock wait is unbounded; the rest is work that
	// legitimately takes as long as the corpus requires.
	index, err := openSearchIndex(indexPath, IndexOpenTimeout)
	if err != nil {
		return nil, err
	}

	s := &Storage{
		DataDir:      dataDir,
		ArticleDir:   articleDir,
		AssetDir:     assetDir,
		HistoryDir:   historyDir,
		CandidateDir: candidateDir,
		JobDir:       jobDir,
		SearchIndex:  index,
		ThemeStore:   NewThemeStore(dataDir),
		cache:        newArticleCache(),
	}

	// Seed standard 'home' page if no articles exist
	if err := s.seedDefaultHome(); err != nil {
		_ = index.Close()
		return nil, err
	}

	// One-time sweep lifting lifecycle status out of tags into the status field (no-op once its
	// marker exists).
	if err := s.MigrateStatusToField(); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "Warning: status field migration failed: %v\n", err)
	}

	// Cleanup archived articles that have exceeded their retention period
	if err := s.CleanupArchivedArticles(); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "Warning: failed to cleanup archived articles: %v\n", err)
	}

	// Sync/populate search index
	if err := s.SyncSearchIndex(); err != nil {
		_ = index.Close()
		return nil, fmt.Errorf("failed to sync search index: %w", err)
	}

	return s, nil
}

// IndexOpenTimeout bounds how long startup waits for the search index's exclusive lock before
// concluding another process holds it. Generous enough for a cold open on slow disks, short
// enough that a stdio MCP client reports a failure instead of appearing to hang.
const IndexOpenTimeout = 15 * time.Second

// ErrSearchIndexLocked means the index could not be opened within IndexOpenTimeout, which in
// practice always means another NexWiki process owns the data directory.
var ErrSearchIndexLocked = errors.New("search index is locked by another process")

// openSearchIndex opens (or creates) the Bleve index under a deadline.
//
// The goroutine is deliberately abandoned on timeout rather than cancelled: bleve.Open offers no
// cancellation, and every caller treats this failure as fatal and exits the process.
func openSearchIndex(indexPath string, timeout time.Duration) (bleve.Index, error) {
	type result struct {
		index bleve.Index
		err   error
	}
	done := make(chan result, 1)

	go func() {
		if _, statErr := os.Stat(indexPath); os.IsNotExist(statErr) {
			idx, err := bleve.New(indexPath, bleve.NewIndexMapping())
			if err != nil {
				err = fmt.Errorf("failed to create search index: %w", err)
			}
			done <- result{idx, err}
			return
		}
		idx, err := bleve.Open(indexPath)
		if err != nil {
			err = fmt.Errorf("failed to open search index: %w", err)
		}
		done <- result{idx, err}
	}()

	select {
	case res := <-done:
		return res.index, res.err
	case <-time.After(timeout):
		return nil, ErrSearchIndexLocked
	}
}

// Slugify standardizes title strings into valid URL-safe and file-safe slug formats.
func Slugify(title string) string {
	slug := strings.ToLower(title)
	// Replace non-alphanumeric characters with spaces
	reg := regexp.MustCompile(`[^a-z0-9\s-_]`)
	slug = reg.ReplaceAllString(slug, "")
	// Replace spaces and underscores with hyphens
	slug = strings.ReplaceAll(slug, " ", "-")
	slug = strings.ReplaceAll(slug, "_", "-")
	// Replace multiple hyphens with a single hyphen
	regHyphen := regexp.MustCompile(`-+`)
	slug = regHyphen.ReplaceAllString(slug, "-")
	return strings.Trim(slug, "-")
}

// ListArticles reads all Markdown files and returns metadata sorted by updated time (newest first).
// The "home" article is excluded from listings (reserved for the Hero dashboard).
func (s *Storage) ListArticles() ([]Article, error) {
	var articles []Article

	seen := make(map[string]bool)
	err := filepath.WalkDir(s.ArticleDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || filepath.Ext(path) != ".md" {
			return nil
		}

		info, err := d.Info()
		if err != nil {
			return err
		}
		seen[path] = true

		// Served from cache when the file is unchanged; a stat beats an open + YAML parse.
		_, art, err := s.cachedMeta(path, info)
		if err != nil {
			// Skip malformed or unreadable files rather than failing the whole listing.
			return nil
		}

		// Exclude "home" from listings (reserved for Hero dashboard)
		if art.Slug == "home" {
			return nil
		}

		articles = append(articles, *art)
		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("failed to list articles: %w", err)
	}

	// Drop cache entries for files that have since been deleted or renamed.
	s.cache.prune(seen)

	// Sort articles: updated_at descending
	sort.Slice(articles, func(i, j int) bool {
		return articles[i].Timestamp.After(articles[j].Timestamp)
	})

	return articles, nil
}

// metaBySlug returns cached metadata for one slug, reading the file only when it has changed.
// Callers that need the Markdown body must use GetArticle.
func (s *Storage) metaBySlug(slug string) (*Article, error) {
	cleanedSlug := Slugify(slug)
	if cleanedSlug == "" {
		return nil, fmt.Errorf("invalid slug")
	}

	filePath := filepath.Join(s.ArticleDir, cleanedSlug+".md")
	info, err := os.Stat(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("article not found: %s", slug)
		}
		return nil, err
	}

	_, meta, err := s.cachedMeta(filePath, info)
	return meta, err
}

// GetArticle reads and parses a single article by slug.
func (s *Storage) GetArticle(slug string) (*Article, error) {
	cleanedSlug := Slugify(slug)
	if cleanedSlug == "" {
		return nil, fmt.Errorf("invalid slug")
	}

	filePath := filepath.Join(s.ArticleDir, cleanedSlug+".md")
	data, err := os.ReadFile(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("article not found: %s", slug)
		}
		return nil, err
	}

	return parseArticleFile(data, true)
}

// SaveArticle writes article Markdown to disk, handling potential slug changes and compressing a copy in gzip version history.
// Description, source, and resource are written as given (callers preserve existing values by passing them through).
// articleType sets the OKF document class; pass "" to preserve an existing article's type (or default a new one to Wiki).
func (s *Storage) SaveArticle(oldSlug string, title string, content string, description string, source string, resource string, editSummary string, tags []string, articleType string) (*Article, error) {
	return s.SaveArticleWithStatus(oldSlug, title, content, description, source, resource, editSummary, tags, articleType, nil)
}

// ArticleOverrides carries the write-time fields that are classifications rather than content.
//
// Every field is omitted-means-preserve: a nil pointer says "leave it alone", so an ordinary save
// — a body edit, a rename, a tag change, a link heal — cannot silently reclassify a document.
// Only a caller that genuinely means to change one passes a value.
//
// This is a struct rather than two more positional parameters because the list had already
// reached ten, and WS5 of the memory-enforcement plan adds a third classification (`change_kind`)
// on the same path. A struct absorbs that; an eleventh string parameter does not.
type ArticleOverrides struct {
	// Status moves a plan or skill through its lifecycle. A status change is a state transition,
	// not a content edit, which is why SaveArticle takes none at all.
	Status *string
	// MemoryKind classifies what sort of fact a memory holds. See MemoryKinds in tags.go.
	MemoryKind *string
	// LockedBy/LockedAt set or clear the skill evolution lock (story 01). Nil means preserve;
	// pass a pointer to "" to unlock. Only meaningful on AI-Agent-Skill documents.
	LockedBy *string
	LockedAt *time.Time
	// TrainedStamp, TrainedRevoke, and TrainedUnlock drive the story-06 trained marker
	// (server/skill_trained.go), again only on AI-Agent-Skill documents:
	//
	//   - TrainedStamp stamps a fresh marker (trained_at/version/parent/candidate/
	//     val_score/eval_hash, revocation cleared) as part of the version this save
	//     writes — the promote path and the bundle importer both use it. The stamp
	//     also asserts the `trained-wikiskill` marker tag.
	//   - TrainedRevoke records a revocation (rollback) against the metadata already
	//     on the document; every other trained field is left untouched, so the
	//     marker history survives the rollback.
	//   - TrainedUnlock clears the marker entirely (tag plus all metadata). It is the
	//     one write path allowed to remove the marker tag; the guard below refuses
	//     every other path that tries to strip it.
	TrainedStamp  *TrainedStamp
	TrainedRevoke *TrainedRevoke
	TrainedUnlock bool
	// OKF v0.2 overrides
	Sources     *[]OKFSource
	StaleAfter  *time.Time
	Generated   *OKFGenerated
	Verified    *[]OKFVerification
	UsageWindow *OKFUsageWindow

	// Attested Computation overrides (§10)
	Runtime     *string
	Parameters  *[]OKFParameter
	Computation *string
	Executor    *OKFExecutor
	Attester    *OKFAttester
}

// SaveArticleWithStatus is SaveArticle plus an explicit lifecycle status. Retained as the name
// every existing caller uses; it is a thin wrapper over SaveArticleWithOverrides.
func (s *Storage) SaveArticleWithStatus(oldSlug string, title string, content string, description string, source string, resource string, editSummary string, tags []string, articleType string, status *string) (*Article, error) {
	return s.SaveArticleWithOverrides(oldSlug, title, content, description, source, resource, editSummary, tags, articleType, ArticleOverrides{Status: status})
}

// SaveArticleWithOverrides is SaveArticle plus any of the classification fields in
// ArticleOverrides. Anything left nil is preserved from the existing document.
func (s *Storage) SaveArticleWithOverrides(oldSlug string, title string, content string, description string, source string, resource string, editSummary string, tags []string, articleType string, overrides ArticleOverrides) (*Article, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.saveArticleLocked(oldSlug, title, content, description, source, resource, editSummary, tags, articleType, overrides)
}

// SetStatus moves a document to a new lifecycle status, leaving its content, title, and tags
// untouched. This is the one write path that changes state.
func (s *Storage) SetStatus(slug string, status string, loadedVersion int, editSummary string) (*Article, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	art, err := s.GetArticle(slug)
	if err != nil {
		return nil, err
	}
	if loadedVersion > 0 && art.Version > 0 && art.Version != loadedVersion {
		return nil, fmt.Errorf("%w: loaded version %d, current version %d", ErrVersionConflict, loadedVersion, art.Version)
	}
	if editSummary == "" {
		editSummary = fmt.Sprintf("Status changed to '%s'", NormalizeStatus(status))
	}
	return s.saveArticleLocked(slug, art.Title, art.Content, art.Description, art.Source, art.Resource, editSummary, art.Tags, art.Type, ArticleOverrides{Status: &status})
}

// saveArticleLocked is SaveArticle's body. The caller must hold writeMu.
func (s *Storage) saveArticleLocked(oldSlug string, title string, content string, description string, source string, resource string, editSummary string, tags []string, articleType string, overrides ArticleOverrides) (*Article, error) {
	newSlug := Slugify(title)
	if newSlug == "" {
		return nil, fmt.Errorf("article title must contain valid characters to generate a slug")
	}

	var art *Article
	now := time.Now()
	resolvedType := normalizeType(articleType) // empty/unknown → Wiki
	renamedFromSlug := ""                      // set when a slug rename occurs, to heal inbound WikiLinks
	prevStatus := ""                           // the status before this save, for change stamping
	prevMemoryKind := ""                       // the memory kind before this save, preserved unless overridden
	prevLockedBy := ""                         // the skill lock holder before this save, preserved unless overridden
	var prevLockedAt time.Time
	prevHadTrainedMarker := false // whether the on-disk skill carried the trained marker tag
	prevTrainedAt, prevTrainedVersion := time.Time{}, 0
	prevTrainedParentVersion := 0
	prevTrainedCandidate, prevTrainedEvalHash, prevTrainedRevokedReason := "", "", ""
	prevTrainedValScore := 0.0
	var prevTrainedRevokedAt time.Time

	// If updating an existing article
	if oldSlug != "" {
		oldSlug = Slugify(oldSlug)
		oldPath := filepath.Join(s.ArticleDir, oldSlug+".md")

		existingData, err := os.ReadFile(oldPath)
		if err == nil {
			existingArt, parseErr := parseArticleFile(existingData, false)
			if parseErr == nil {
				// Preserve the existing document class unless the caller explicitly supplied one.
				if articleType == "" {
					resolvedType = existingArt.Type
				}
				prevStatus = existingArt.Status
				prevMemoryKind = existingArt.MemoryKind
				prevLockedBy = existingArt.LockedBy
				prevLockedAt = existingArt.LockedAt
				prevHadTrainedMarker = hasTag(existingArt.Tags, TrainedMarkerTag)
				prevTrainedAt = existingArt.TrainedAt
				prevTrainedVersion = existingArt.TrainedVersion
				prevTrainedParentVersion = existingArt.TrainedParentVersion
				prevTrainedCandidate = existingArt.TrainedCandidate
				prevTrainedValScore = existingArt.TrainedValScore
				prevTrainedEvalHash = existingArt.TrainedEvalHash
				prevTrainedRevokedAt = existingArt.TrainedRevokedAt
				prevTrainedRevokedReason = existingArt.TrainedRevokedReason
				art = &Article{
					Type:            resolvedType,
					Title:           title,
					Slug:            newSlug,
					CreatedAt:       existingArt.CreatedAt,
					ArchivedAt:      existingArt.ArchivedAt,
					Status:          existingArt.Status,
					StatusChangedAt: existingArt.StatusChangedAt,
					MemoryKind:      existingArt.MemoryKind,
					Timestamp:       now,
					Content:         content,
					Tags:            tags,
					Generated:       existingArt.Generated,
					Verified:        existingArt.Verified,
					Sources:         existingArt.Sources,
					UsageWindow:     existingArt.UsageWindow,
					StaleAfter:      existingArt.StaleAfter,
					Runtime:         existingArt.Runtime,
					Parameters:      existingArt.Parameters,
					Computation:     existingArt.Computation,
					Executor:        existingArt.Executor,
					Attester:        existingArt.Attester,
				}
			}
		}
	}

	// Resolve the status this save lands on: an explicit override wins, otherwise the document
	// keeps the state it already had, and a brand-new plan enters the lifecycle at draft.
	resolvedStatus := prevStatus
	if overrides.Status != nil {
		resolvedStatus = NormalizeStatus(*overrides.Status)
	}
	// A plan always ends up with a status: a new one enters at draft, and one written before the
	// field existed is defaulted here rather than rejected — otherwise a legacy plan would be
	// un-editable until a migration had swept it, which makes correctness depend on boot order.
	if resolvedType == ContentTypePlan && resolvedStatus == "" {
		resolvedStatus = DefaultPlanStatus
	}

	// The status contract holds at every write path — REST, MCP, revert, import — and this is the
	// one choke point they all pass through. Validated before the rename below so a rejected save
	// cannot leave a half-moved article behind. Only plans and skills have a contract; wiki
	// articles and memories may use any status and any tags.
	if err := ValidateStatus(resolvedType, resolvedStatus); err != nil {
		return nil, err
	}
	if err := ValidateStatusFreeTags(resolvedType, tags); err != nil {
		return nil, err
	}

	// The memory kind resolves the same way and is validated at the same choke point. Absent is
	// permitted here: requiring one is the *create tool's* gate, not storage's, because a memory
	// written before the field existed must stay editable rather than becoming un-saveable until
	// somebody classifies it. wiki_health reports those as unkinded_memories instead.
	resolvedMemoryKind := prevMemoryKind
	if overrides.MemoryKind != nil {
		resolvedMemoryKind = NormalizeMemoryKind(*overrides.MemoryKind)
	}
	if err := ValidateMemoryKind(resolvedType, resolvedMemoryKind, false); err != nil {
		return nil, err
	}
	// A kind on anything but a memory is meaningless, and letting one persist would put a value
	// in the corpus that no reader can interpret and no filter will ever match.
	if resolvedType != ContentTypeMemory {
		resolvedMemoryKind = ""
	}

	// The skill lock resolves the same way: preserved unless overridden, and meaningless
	// anywhere but a skill. A bare locker with no timestamp is treated as unlocked so a
	// half-written lock can never claim a document forever.
	resolvedLockedBy := prevLockedBy
	resolvedLockedAt := prevLockedAt
	if overrides.LockedBy != nil {
		resolvedLockedBy = strings.TrimSpace(*overrides.LockedBy)
	}
	if overrides.LockedAt != nil {
		resolvedLockedAt = *overrides.LockedAt
	}
	if resolvedType != ContentTypeSkill {
		resolvedLockedBy = ""
		resolvedLockedAt = time.Time{}
	}
	if resolvedLockedBy == "" {
		resolvedLockedAt = time.Time{}
	}

	if oldSlug != "" {
		oldPath := filepath.Join(s.ArticleDir, oldSlug+".md")

		// If the slug has changed, rename files and move assets
		if oldSlug != newSlug {
			renamedFromSlug = oldSlug
			newPath := filepath.Join(s.ArticleDir, newSlug+".md")
			// Check if target slug already exists
			if _, err := os.Stat(newPath); err == nil {
				return nil, fmt.Errorf("an article with slug '%s' already exists", newSlug)
			}

			// Rename physical Markdown file
			if err := os.Rename(oldPath, newPath); err != nil && !os.IsNotExist(err) {
				return nil, fmt.Errorf("failed to rename article file: %w", err)
			}

			// Remove old slug from search index
			_ = s.UnindexArticle(oldSlug)

			// Rename corresponding asset directory if it exists
			oldAssetDir := filepath.Join(s.AssetDir, oldSlug)
			newAssetDir := filepath.Join(s.AssetDir, newSlug)
			if _, err := os.Stat(oldAssetDir); err == nil {
				if err := os.Rename(oldAssetDir, newAssetDir); err != nil {
					return nil, fmt.Errorf("failed to move assets: %w", err)
				}
			}

			// Rename corresponding history directory if it exists
			oldHistDir := filepath.Join(s.HistoryDir, oldSlug)
			newHistDir := filepath.Join(s.HistoryDir, newSlug)
			if _, err := os.Stat(oldHistDir); err == nil {
				if err := os.Rename(oldHistDir, newHistDir); err != nil {
					return nil, fmt.Errorf("failed to move history: %w", err)
				}
			}
		}
	}

	histFolder := filepath.Join(s.HistoryDir, newSlug)
	_ = os.MkdirAll(histFolder, 0755)

	// The version this save supersedes, read from the document's own front matter. After the rename
	// block above, newSlug is where the previous state lives whichever slug it arrived under —
	// including the case where a create collides with an existing title and oldSlug was never set.
	activePath := filepath.Join(s.ArticleDir, newSlug+".md")
	prevVersion := 0
	prevData, prevErr := os.ReadFile(activePath)
	if prevErr == nil {
		if prevArt, err := parseArticleFile(prevData, false); err == nil {
			prevVersion = prevArt.Version
		}
	}

	// Front matter is the authoritative version counter: `loaded_version` optimistic locking, the
	// REST API, and the MCP output schemas all speak this integer, and it travels with the document.
	// Deriving it from the history directory instead made the counter a property of a local cache,
	// so pruning data/history, restoring from a partial backup, or importing a document without its
	// snapshots silently restarted a long-lived article at version 1 — taking optimistic locking
	// with it, since a reset counter compares equal to a stale loaded_version.
	nextVersion := 1
	if prevVersion > 0 {
		nextVersion = prevVersion + 1
	} else if prevErr == nil {
		// The document exists but predates the version field. Continue its history's numbering
		// rather than restarting, which is what the directory scan was always really for.
		if files, err := os.ReadDir(histFolder); err == nil {
			for _, f := range files {
				if f.IsDir() || !strings.HasSuffix(f.Name(), ".md.gz") {
					continue
				}
				if v, err := strconv.Atoi(strings.TrimSuffix(f.Name(), ".md.gz")); err == nil && v >= nextVersion {
					nextVersion = v + 1
				}
			}
		}
		if nextVersion == 1 {
			// Unversioned and unsnapshotted: the state on disk is version 1, and this save is 2.
			nextVersion = 2
		}
	}

	// Archive the state being superseded if no snapshot of it exists, so the timeline the version
	// numbers promise has an entry to revert to. This covers a document whose history was pruned as
	// well as the original case it was written for: an article on disk with no history at all.
	if prevErr == nil && nextVersion > 1 {
		histFilePath := filepath.Join(histFolder, fmt.Sprintf("%d.md.gz", nextVersion-1))
		if _, err := os.Stat(histFilePath); os.IsNotExist(err) {
			_ = writeGzippedFile(histFilePath, prevData)
		}
	}

	// YAML front matter handles escaping/multi-line values natively, so no value flattening is needed.
	editSummary = strings.TrimSpace(editSummary)

	if editSummary == "" {
		if nextVersion == 1 {
			editSummary = "Initial version"
		} else {
			editSummary = "Updated article"
		}
	}

	// Create new article object if not populated above
	if art == nil {
		art = &Article{
			Type:      resolvedType,
			Title:     title,
			Slug:      newSlug,
			CreatedAt: now,
			Timestamp: now,
			Content:   content,
			Tags:      tags,
		}
	} else {
		art.Tags = tags
	}
	art.Description = description
	art.Source = source
	art.Resource = resource

	art.Status = resolvedStatus
	art.MemoryKind = resolvedMemoryKind
	art.LockedBy = resolvedLockedBy
	art.LockedAt = resolvedLockedAt

	// The trained marker resolves like the lock: preserved unless a stamp, revoke,
	// or unlock override says otherwise, and meaningless anywhere but a skill. This
	// runs after nextVersion is computed because a stamp always names the version
	// this save writes — the marker can never point at a version that does not
	// carry the stamped body.
	art.TrainedAt = prevTrainedAt
	art.TrainedVersion = prevTrainedVersion
	art.TrainedParentVersion = prevTrainedParentVersion
	art.TrainedCandidate = prevTrainedCandidate
	art.TrainedValScore = prevTrainedValScore
	art.TrainedEvalHash = prevTrainedEvalHash
	art.TrainedRevokedAt = prevTrainedRevokedAt
	art.TrainedRevokedReason = prevTrainedRevokedReason
	if overrides.TrainedStamp != nil {
		stamp := overrides.TrainedStamp
		trainedVersion := stamp.VersionOverride
		if trainedVersion == 0 {
			trainedVersion = nextVersion
		}
		art.TrainedAt = stamp.At
		art.TrainedVersion = trainedVersion
		art.TrainedParentVersion = stamp.ParentVersion
		art.TrainedCandidate = stamp.CandidateID
		art.TrainedValScore = stamp.ValScore
		art.TrainedEvalHash = stamp.EvalHash
		// A stamp is a (re-)training: any earlier revocation is superseded, so the
		// marker goes back to its active form.
		art.TrainedRevokedAt = time.Time{}
		art.TrainedRevokedReason = ""
	}
	if overrides.TrainedRevoke != nil {
		// A revocation only writes the revocation fields: trained_at/version/score/
		// eval_hash stay exactly as they were, which is the marker history a rollback
		// must preserve.
		art.TrainedRevokedAt = overrides.TrainedRevoke.At
		art.TrainedRevokedReason = overrides.TrainedRevoke.Reason
	}
	if overrides.TrainedUnlock || resolvedType != ContentTypeSkill {
		art.TrainedAt = time.Time{}
		art.TrainedVersion = 0
		art.TrainedParentVersion = 0
		art.TrainedCandidate = ""
		art.TrainedValScore = 0
		art.TrainedEvalHash = ""
		art.TrainedRevokedAt = time.Time{}
		art.TrainedRevokedReason = ""
	}
	// The stamp asserts the marker tag alongside the metadata; every other write
	// path keeps an existing marker (the tool-managed-tag rule), and only an
	// explicit unlock may clear it.
	if overrides.TrainedStamp != nil && resolvedType == ContentTypeSkill {
		art.Tags = ensureTrainedMarkerTag(art.Tags)
	}
	if resolvedType == ContentTypeSkill && !overrides.TrainedUnlock && overrides.TrainedStamp == nil &&
		prevHadTrainedMarker && !hasTag(art.Tags, TrainedMarkerTag) {
		// Tool-managed marker: a write path that arrives without it (an agent tag
		// edit that dropped it, a revert restoring pre-marker tags, an import of a
		// bundle without it) cannot silently strip it. Re-assert it so the marker
		// is removable only through TrainedUnlock — the explicit human path that
		// appends its own audit record.
		art.Tags = ensureTrainedMarkerTag(art.Tags)
	}

	if overrides.Sources != nil {
		art.Sources = *overrides.Sources
		if len(art.Sources) > 0 && art.Source == "" {
			art.Source = art.Sources[0].Resource
		} else if len(art.Sources) == 0 {
			art.Source = ""
		}
	} else if source != "" {
		if len(art.Sources) == 0 {
			art.Sources = []OKFSource{{Resource: source}}
		} else {
			art.Sources[0].Resource = source
		}
	} else if source == "" && overrides.Sources == nil {
		art.Sources = nil
	}
	if overrides.StaleAfter != nil {
		art.StaleAfter = *overrides.StaleAfter
	}
	if overrides.Verified != nil {
		art.Verified = *overrides.Verified
	}
	if overrides.UsageWindow != nil {
		art.UsageWindow = overrides.UsageWindow
	}
	if overrides.Runtime != nil {
		art.Runtime = *overrides.Runtime
	}
	if overrides.Parameters != nil {
		art.Parameters = *overrides.Parameters
	}
	if overrides.Computation != nil {
		art.Computation = *overrides.Computation
	}
	if overrides.Executor != nil {
		art.Executor = overrides.Executor
	}
	if overrides.Attester != nil {
		art.Attester = overrides.Attester
	}

	if overrides.Generated != nil {
		art.Generated = overrides.Generated
		if art.Generated.At.IsZero() {
			art.Generated.At = now
		}
		art.Timestamp = art.Generated.At
	} else {
		if art.Generated == nil {
			art.Generated = &OKFGenerated{By: "nexwiki", At: now}
		} else {
			art.Generated.At = now
		}
		art.Timestamp = now
	}
	art.TrustTier = DeriveTrustTier(art.Verified)
	art.IsStale = IsStale(art)

	switch art.Type {
	case ContentTypePlan, ContentTypeSkill:
		if resolvedStatus != prevStatus {
			// Log-and-allow: an unusual transition is worth a trace, but a human correcting a
			// mis-set plan in the editor must always win over the state machine.
			if art.Type == ContentTypePlan && prevStatus != "" && !isLegalPlanTransition(prevStatus, resolvedStatus) {
				_, _ = fmt.Fprintf(os.Stderr, "Note: plan '%s' made an unusual status transition %s → %s (allowed)\n",
					art.Slug, prevStatus, resolvedStatus)
			}
			art.StatusChangedAt = now
		}
		// A plan predating the lifecycle (or written by an external tool) gets its clock started
		// now rather than being treated as infinitely old. Skills have no timers and no clock.
		if art.Type == ContentTypePlan && art.StatusChangedAt.IsZero() {
			art.StatusChangedAt = now
		}
		// archived_at exactly mirrors the archived status here: set on entry so the deletion
		// clock starts, cleared on revival so IsArchived cannot keep hiding a document that is
		// nominally back in flight (the one-way-door asymmetry the lifecycle design flagged).
		if resolvedStatus == StatusArchived {
			if art.ArchivedAt.IsZero() {
				art.ArchivedAt = now
			}
		} else if !art.ArchivedAt.IsZero() {
			art.ArchivedAt = time.Time{}
		}
	default:
		// Wiki articles and memories keep the long-standing tag semantics: archiving is manual,
		// the stamp is written once, and removing the tag deliberately does not clear it.
		if art.ArchivedAt.IsZero() {
			for _, tag := range art.Tags {
				if strings.EqualFold(tag, StatusArchived) {
					art.ArchivedAt = now
					break
				}
			}
		}
	}

	// A rename moved this document's asset directory (above), so its own body is the first thing
	// that has to follow the media. This is the common case by far — assets are uploaded under the
	// slug of the page that embeds them — and it is not reachable from healRenamedLinks, which
	// visits other documents. Done here, before serialization, so the move and the references to it
	// land in the same saved state.
	if renamedFromSlug != "" {
		if rewritten, changed := RewriteAssetPathLinks(art.Content, renamedFromSlug, newSlug); changed {
			art.Content = rewritten
		}
	}

	// Set version and edit summary
	art.Version = nextVersion
	art.EditSummary = editSummary

	// Serialize front matter and content
	serialized := serializeFrontMatter(art) + art.Content

	// Write uncompressed active version to data/articles/
	filePath := filepath.Join(s.ArticleDir, newSlug+".md")
	if err := os.WriteFile(filePath, []byte(serialized), 0644); err != nil {
		return nil, fmt.Errorf("failed to write active article file: %w", err)
	}

	// Write compressed history version to data/history/
	histFilePath := filepath.Join(histFolder, fmt.Sprintf("%d.md.gz", nextVersion))
	if err := writeGzippedFile(histFilePath, []byte(serialized)); err != nil {
		return nil, fmt.Errorf("failed to write compressed version history file: %w", err)
	}

	// Add updated/new article to search index
	if err := s.IndexArticle(art); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "Warning: failed to index article '%s' in search engine: %v\n", newSlug, err)
	}

	// Best-effort link healing: when a slug changed, rewrite inbound WikiLinks so they keep
	// resolving. Failures here are logged and never block the rename that already succeeded.
	if renamedFromSlug != "" {
		s.healRenamedLinks(renamedFromSlug, newSlug, art.Title)
	}

	return art, nil
}

// healRenamedLinks (caller must hold writeMu) rewrites every article that references oldSlug so it
// points at the renamed article. Three reference forms are healed: a [[WikiLink]] is retargeted to
// the new title, an absolute [text](/articles/<slug>) destination is retargeted to the new slug with
// its link text untouched, and an /api/assets/<slug>/<file> URL is retargeted to follow the asset
// directory the rename moved. Best-effort: any per-article failure is logged and skipped.
//
// The candidate set is the union of two scans because the link graph answers only half the
// question. GetBacklinks reports documents that *link* to oldSlug; a document that merely embeds
// one of its images has no link and no backlink, so findAssetReferrers finds it separately. The
// renamed document's own body is handled in saveArticleLocked, not here.
func (s *Storage) healRenamedLinks(oldSlug, newSlug, newTitle string) {
	candidates := map[string]bool{}
	backlinks, err := s.GetBacklinks(oldSlug)
	if err != nil {
		// A failed backlink scan is not a reason to skip asset healing too: the two scans are
		// independent, and half the healing beats none.
		_, _ = fmt.Fprintf(os.Stderr, "Warning: link-heal scan failed after renaming '%s'→'%s': %v\n", oldSlug, newSlug, err)
	}
	for _, bl := range backlinks {
		candidates[bl.Slug] = true
	}
	for _, slug := range s.findAssetReferrers(oldSlug) {
		// The renamed document is its own asset referrer and was already healed in place.
		if slug != newSlug {
			candidates[slug] = true
		}
	}

	slugs := make([]string, 0, len(candidates))
	for slug := range candidates {
		slugs = append(slugs, slug)
	}
	sort.Strings(slugs) // stable order, so a rename's healing commits/logs are reproducible

	for _, slug := range slugs {
		linker, err := s.GetArticle(slug)
		if err != nil {
			continue
		}
		rewritten, wikiChanged := RewriteWikiLinks(linker.Content, oldSlug, newTitle)
		rewritten, pathChanged := RewriteArticlePathLinks(rewritten, oldSlug, newSlug)
		rewritten, assetChanged := RewriteAssetPathLinks(rewritten, oldSlug, newSlug)
		if !wikiChanged && !pathChanged && !assetChanged {
			continue
		}
		summary := fmt.Sprintf("Auto-healed internal link: '%s' renamed to '%s'", oldSlug, newSlug)
		if _, err := s.saveArticleLocked(linker.Slug, linker.Title, rewritten, linker.Description, linker.Source, linker.Resource, summary, linker.Tags, linker.Type, ArticleOverrides{}); err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "Warning: failed to heal links in '%s' after rename: %v\n", linker.Slug, err)
		}
	}
}

// DeleteArticle deletes the article's Markdown file, all its assets, and all version history on disk.
func (s *Storage) DeleteArticle(slug string) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.deleteArticleLocked(slug)
}

// deleteArticleLocked is DeleteArticle's body. The caller must hold writeMu.
func (s *Storage) deleteArticleLocked(slug string) error {
	cleanedSlug := Slugify(slug)
	if cleanedSlug == "" {
		return fmt.Errorf("invalid slug")
	}

	// 1. Delete the Markdown file
	filePath := filepath.Join(s.ArticleDir, cleanedSlug+".md")
	if err := os.Remove(filePath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to delete article file: %w", err)
	}

	// Remove from search index
	_ = s.UnindexArticle(cleanedSlug)

	// 2. Recursively delete asset folder
	assetPath := filepath.Join(s.AssetDir, cleanedSlug)
	if err := os.RemoveAll(assetPath); err != nil {
		return fmt.Errorf("failed to delete asset directory: %w", err)
	}

	// 3. Recursively delete history folder
	historyPath := filepath.Join(s.HistoryDir, cleanedSlug)
	if err := os.RemoveAll(historyPath); err != nil {
		return fmt.Errorf("failed to delete history directory: %w", err)
	}

	return nil
}

// SaveAsset saves an uploaded file into data/assets/{slug}/{filename}.
func (s *Storage) SaveAsset(slug string, filename string, fileData []byte) (string, error) {
	// Shares writeMu with SaveArticle, which renames asset directories on slug changes.
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	cleanedSlug := Slugify(slug)
	if cleanedSlug == "" {
		return "", fmt.Errorf("invalid slug")
	}

	// Sanitize filename to prevent directory traversal
	safeFilename := filepath.Base(filename)
	safeFilename = strings.ReplaceAll(safeFilename, " ", "-")

	// Ensure the specific folder for this article exists
	articleAssetDir := filepath.Join(s.AssetDir, cleanedSlug)
	if err := os.MkdirAll(articleAssetDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create article asset folder: %w", err)
	}

	filePath := filepath.Join(articleAssetDir, safeFilename)
	if err := os.WriteFile(filePath, fileData, 0644); err != nil {
		return "", fmt.Errorf("failed to write asset file: %w", err)
	}

	// Return URL path for the client
	return fmt.Sprintf("/api/assets/%s/%s", cleanedSlug, safeFilename), nil
}

// GetAssetPath returns the absolute path to an asset, validating to prevent directory traversal.
func (s *Storage) GetAssetPath(slug, filename string) (string, error) {
	cleanedSlug := Slugify(slug)
	safeFilename := filepath.Base(filename)

	if cleanedSlug == "" || safeFilename == "" || safeFilename == "." || safeFilename == ".." {
		return "", fmt.Errorf("invalid path parameters")
	}

	filePath := filepath.Clean(filepath.Join(s.AssetDir, cleanedSlug, safeFilename))

	// Confirm the resolved path really is inside the asset directory. A string-prefix comparison
	// is not sufficient here: "/data/assets-evil/x" has "/data/assets" as a prefix but escapes the
	// directory. filepath.Rel answers containment properly — anything outside yields a path
	// starting with "..".
	if err := ensureWithin(s.AssetDir, filePath); err != nil {
		return "", err
	}

	return filePath, nil
}

// ensureWithin reports an error unless target resolves to a path inside dir. Both are made
// absolute first so a relative DataDir cannot produce a false negative.
func ensureWithin(dir, target string) error {
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	absTarget, err := filepath.Abs(target)
	if err != nil {
		return err
	}

	rel, err := filepath.Rel(absDir, absTarget)
	if err != nil {
		return fmt.Errorf("unauthorized file access path traversal attempt")
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return fmt.Errorf("unauthorized file access path traversal attempt")
	}

	return nil
}

// seedDefaultHome creates a default welcoming home wiki page if the articles folder is completely empty.
func (s *Storage) seedDefaultHome() error {
	files, err := os.ReadDir(s.ArticleDir)
	if err != nil {
		return err
	}
	if len(files) > 0 {
		return nil
	}

	defaultHomeContent := `# Welcome to NexWiki 🚀

Welcome to your brand-new, self-hosted personal wiki application! 

This wiki is built using **Go** for the backend server and **React + TypeScript + Tailwind CSS** for the frontend interface. It is fully containerized with **Docker** and runs out of a single, optimized binary.

### 🌟 Features Ready To Use:
*   **Slug-Based Clean Routing:** Dynamic URLs mapping directly to your markdown files.
*   **Split-Pane Editor:** Enjoy editing raw markdown on the left with instant live visual rendering on the right.
*   **Drag-and-Drop Image Uploader:** Paste or drag images straight into the editor to upload them.
*   **WikiLinks:** Write double-bracket links like [[Guides]] or [[Markdown Playground]] to easily connect pages.
*   **Table of Contents (TOC):** A dynamic, scroll-observed floating outline generated from article headers.
*   **Dark Mode Toggle:** Easy reading day or night with a gorgeous dark aesthetic.
*   **Asset Lifecycle Management:** When you delete a wiki article, all uploaded images embedded in that article are instantly and securely removed from disk.

### 📝 Get Started
*   Click the **Edit** button in the top right to modify this page.
*   Click the **New Page** button in the sidebar or search index to create a new page.
*   Try inserting a link to a new page using the double-bracket syntax: ` + "`" + `[[My Draft Page]]` + "`" + `. Click it to create the article on the fly!
`
	_, err = s.SaveArticle("", "Home", defaultHomeContent, "", "", "", "Initial version", nil, ContentTypeWiki)
	return err
}

// articleFrontMatter is the on-disk OKF YAML front-matter schema. The field order here
// determines the emitted key order: OKF canonical keys first, then NexWiki custom keys.
// Times are stored as RFC3339 strings for stable, controlled formatting.
type articleFrontMatter struct {
	Type        string   `yaml:"type"`
	Title       string   `yaml:"title"`
	Slug        string   `yaml:"slug,omitempty"`
	Description string   `yaml:"description,omitempty"`
	Resource    string   `yaml:"resource,omitempty"`
	Tags        []string `yaml:"tags,omitempty"`
	Status      string   `yaml:"status,omitempty"`

	// OKF v0.2 provenance, trust, and lifecycle families
	Generated   *OKFGenerated       `yaml:"generated,omitempty"`
	Verified    OKFVerificationList `yaml:"verified,omitempty"`
	StaleAfter  string              `yaml:"stale_after,omitempty"`
	Sources     []OKFSource         `yaml:"sources,omitempty"`
	UsageWindow *OKFUsageWindow     `yaml:"usage_window,omitempty"`

	// Attested Computation keys (§10)
	Runtime     string         `yaml:"runtime,omitempty"`
	Parameters  []OKFParameter `yaml:"parameters,omitempty"`
	Computation string         `yaml:"computation,omitempty"`
	Executor    *OKFExecutor   `yaml:"executor,omitempty"`
	Attester    *OKFAttester   `yaml:"attester,omitempty"`

	// Legacy & NexWiki custom keys
	Timestamp   string `yaml:"timestamp,omitempty"`
	CreatedAt   string `yaml:"created_at,omitempty"`
	Version     int    `yaml:"version,omitempty"`
	EditSummary string `yaml:"edit_summary,omitempty"`
	Source      string `yaml:"source,omitempty"`
	ArchivedAt  string `yaml:"archived_at,omitempty"`
	// StatusChangedAt is a NexWiki custom key carried only by AI-Agent-Plan documents; it feeds
	// the plan lifecycle timers (see server/plan_lifecycle.go).
	StatusChangedAt string `yaml:"status_changed_at,omitempty"`
	// MemoryKind is a NexWiki custom key carried only by AI-Agent-Memory documents (see tags.go).
	MemoryKind string `yaml:"memory_kind,omitempty"`
	// LockedBy/LockedAt are NexWiki custom keys carried only by AI-Agent-Skill documents
	// (wikiskill evolution, story 01). Like status_changed_at and memory_kind they are
	// namespaced custom keys, not OKF v0.2 core — never relabelled, never stripped.
	LockedBy string `yaml:"locked_by,omitempty"`
	LockedAt string `yaml:"locked_at,omitempty"`
	// Trained-marker keys are NexWiki custom keys carried only by AI-Agent-Skill
	// documents (wikiskill evolution, story 06). They are written only by the
	// harness promote path and ride OKF export/import like locked_by.
	TrainedAt            string  `yaml:"trained_at,omitempty"`
	TrainedVersion       int     `yaml:"trained_version,omitempty"`
	TrainedParentVersion int     `yaml:"trained_parent_version,omitempty"`
	TrainedCandidate     string  `yaml:"trained_candidate,omitempty"`
	TrainedValScore      float64 `yaml:"trained_val_score,omitempty"`
	TrainedEvalHash      string  `yaml:"trained_eval_hash,omitempty"`
	TrainedRevokedAt     string  `yaml:"trained_revoked_at,omitempty"`
	TrainedRevokedReason string  `yaml:"trained_revoked_reason,omitempty"`
}

// extractCitationsFromBody parses a legacy v0.1 Markdown '# Citations' section (§13.1).
func extractCitationsFromBody(body string) []OKFSource {
	lines := strings.Split(body, "\n")
	inCitations := false
	var sources []OKFSource
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "# Citations") {
			inCitations = true
			continue
		}
		if inCitations {
			if strings.HasPrefix(trimmed, "#") {
				break
			}
			if strings.HasPrefix(trimmed, "- ") || strings.HasPrefix(trimmed, "* ") {
				urlOrResource := strings.TrimSpace(trimmed[2:])
				if urlOrResource != "" {
					sources = append(sources, OKFSource{Resource: urlOrResource})
				}
			}
		}
	}
	return sources
}

// parseArticleFile parses the OKF YAML front-matter block and Markdown body.
func parseArticleFile(fileContent []byte, loadContent bool) (*Article, error) {
	// Normalize Windows line endings
	str := strings.ReplaceAll(string(fileContent), "\r\n", "\n")

	if !strings.HasPrefix(str, "---\n") {
		return nil, fmt.Errorf("invalid format: missing front matter header marker")
	}

	parts := strings.SplitN(str, "---\n", 3)
	if len(parts) < 3 {
		return nil, fmt.Errorf("invalid format: malformed front matter delimiters")
	}

	metaSection := parts[1]
	bodySection := strings.TrimSpace(parts[2])

	var fm articleFrontMatter
	if err := yaml.Unmarshal([]byte(metaSection), &fm); err != nil {
		return nil, fmt.Errorf("invalid format: front matter is not valid YAML: %w", err)
	}

	art := &Article{
		Type:         normalizeType(fm.Type),
		DeclaredType: strings.TrimSpace(fm.Type),
		Title:        fm.Title,
		Slug:         fm.Slug,
		Description:  fm.Description,
		Resource:     fm.Resource,
		Source:       fm.Source,
		Version:      fm.Version,
		EditSummary:  fm.EditSummary,
		Status:       NormalizeStatus(fm.Status),
		MemoryKind:   NormalizeMemoryKind(fm.MemoryKind),
		Runtime:      fm.Runtime,
		Parameters:   fm.Parameters,
		Computation:  fm.Computation,
		Executor:     fm.Executor,
		Attester:     fm.Attester,
		UsageWindow:  fm.UsageWindow,
	}
	// Clean and copy the tag list (drop blanks).
	for _, t := range fm.Tags {
		t = strings.TrimSpace(t)
		if t != "" {
			art.Tags = append(art.Tags, t)
		}
	}
	if t, err := parseISO8601(fm.CreatedAt); err == nil {
		art.CreatedAt = t
	}

	// OKF v0.2 Rule 1: timestamp is superseded by generated: { by, at }.
	// On parse, if generated is absent, fall back to legacy timestamp.
	// Keep art.Timestamp synchronized with generated.at.
	if fm.Generated != nil && (!fm.Generated.At.IsZero() || fm.Generated.By != "") {
		art.Generated = fm.Generated
		if !art.Generated.At.IsZero() {
			art.Timestamp = art.Generated.At
		}
	} else if fm.Timestamp != "" {
		if t, err := parseISO8601(fm.Timestamp); err == nil {
			art.Timestamp = t
			art.Generated = &OKFGenerated{At: t}
		}
	}
	if art.Timestamp.IsZero() && fm.Timestamp != "" {
		if t, err := parseISO8601(fm.Timestamp); err == nil {
			art.Timestamp = t
			if art.Generated != nil && art.Generated.At.IsZero() {
				art.Generated.At = t
			}
		}
	}
	if art.Generated != nil && art.Generated.At.IsZero() && !art.Timestamp.IsZero() {
		art.Generated.At = art.Timestamp
	}

	// OKF v0.2 Rule 3 & 4: verified list & trust tier derivation
	if len(fm.Verified) > 0 {
		art.Verified = make([]OKFVerification, len(fm.Verified))
		copy(art.Verified, fm.Verified)
	}
	art.TrustTier = DeriveTrustTier(art.Verified)

	// OKF v0.2 Rule 5: stale_after & freshness check
	if fm.StaleAfter != "" {
		if t, err := parseISO8601(fm.StaleAfter); err == nil {
			art.StaleAfter = t
			art.IsStale = IsStale(art)
		}
	}

	// OKF v0.2 Rule 2: source is superseded by sources array in frontmatter.
	// On parse, if sources is absent and legacy source is present, populate art.Sources with a single entry { Resource: legacySource }.
	// Fallback to body # Citations if both are absent (§13.1).
	if len(fm.Sources) > 0 {
		art.Sources = fm.Sources
		if art.Source == "" {
			art.Source = art.Sources[0].Resource
		}
	} else if fm.Source != "" {
		art.Source = fm.Source
		art.Sources = []OKFSource{{Resource: fm.Source}}
	} else {
		extracted := extractCitationsFromBody(bodySection)
		if len(extracted) > 0 {
			art.Sources = extracted
			art.Source = extracted[0].Resource
		}
	}

	if fm.ArchivedAt != "" {
		if t, err := parseISO8601(fm.ArchivedAt); err == nil {
			art.ArchivedAt = t
		}
	}
	if fm.StatusChangedAt != "" {
		if t, err := parseISO8601(fm.StatusChangedAt); err == nil {
			art.StatusChangedAt = t
		}
	}
	if strings.TrimSpace(fm.LockedBy) != "" {
		art.LockedBy = strings.TrimSpace(fm.LockedBy)
		if fm.LockedAt != "" {
			if t, err := parseISO8601(fm.LockedAt); err == nil {
				art.LockedAt = t
			}
		}
	}
	if fm.TrainedAt != "" {
		if t, err := parseISO8601(fm.TrainedAt); err == nil {
			art.TrainedAt = t
		}
	}
	art.TrainedVersion = fm.TrainedVersion
	art.TrainedParentVersion = fm.TrainedParentVersion
	art.TrainedCandidate = strings.TrimSpace(fm.TrainedCandidate)
	art.TrainedValScore = fm.TrainedValScore
	art.TrainedEvalHash = strings.ToLower(strings.TrimSpace(fm.TrainedEvalHash))
	if fm.TrainedRevokedAt != "" {
		if t, err := parseISO8601(fm.TrainedRevokedAt); err == nil {
			art.TrainedRevokedAt = t
		}
	}
	art.TrainedRevokedReason = strings.TrimSpace(fm.TrainedRevokedReason)

	// `slug` derivation
	if art.Slug == "" {
		art.Slug = Slugify(art.Title)
	}

	// Basic check. `title` stays required
	if art.Title == "" || art.Slug == "" {
		return nil, fmt.Errorf("invalid format: front matter must carry a title (and a slug, or a title that yields one)")
	}

	if loadContent {
		art.Content = bodySection
	} else {
		art.ContentPreview = extractContentPreview(bodySection)
	}

	return art, nil
}

// extractContentPreview returns the first meaningful content line of a Markdown body,
// stripped of heading markers and WikiLink brackets, truncated to 120 runes.
func extractContentPreview(body string) string {
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(line), "#"))
		if line == "" || line == "---" {
			continue
		}
		line = strings.ReplaceAll(line, "[[", "")
		line = strings.ReplaceAll(line, "]]", "")
		runes := []rune(line)
		if len(runes) > 120 {
			return string(runes[:120]) + "..."
		}
		return line
	}
	return ""
}

// serializeFrontMatter converts article metadata into a conformant OKF YAML front-matter block.
func serializeFrontMatter(art *Article) string {
	fm := articleFrontMatter{
		Type:        normalizeType(art.Type),
		Title:       art.Title,
		Slug:        art.Slug,
		Description: art.Description,
		Resource:    art.Resource,
		Tags:        art.Tags,
		Status:      art.Status,
		MemoryKind:  art.MemoryKind,
		Version:     art.Version,
		EditSummary: art.EditSummary,
		Runtime:     art.Runtime,
		Parameters:  art.Parameters,
		Computation: art.Computation,
		Executor:    art.Executor,
		Attester:    art.Attester,
		UsageWindow: art.UsageWindow,
	}

	// OKF v0.2 Rule 1: timestamp superseded by generated: { by, at }.
	// Keep art.Timestamp synchronized with generated.at.
	if art.Generated != nil {
		gen := *art.Generated
		if gen.By == "" {
			gen.By = "nexwiki"
		}
		if gen.At.IsZero() && !art.Timestamp.IsZero() {
			gen.At = art.Timestamp
		}
		fm.Generated = &gen
		if !gen.At.IsZero() {
			fm.Timestamp = gen.At.UTC().Format(time.RFC3339)
		}
	} else if !art.Timestamp.IsZero() {
		fm.Generated = &OKFGenerated{
			By: "nexwiki",
			At: art.Timestamp,
		}
		fm.Timestamp = art.Timestamp.UTC().Format(time.RFC3339)
	}

	// Verified
	if len(art.Verified) > 0 {
		fm.Verified = OKFVerificationList(art.Verified)
	}

	// StaleAfter
	if !art.StaleAfter.IsZero() {
		fm.StaleAfter = art.StaleAfter.UTC().Format(time.RFC3339)
	}

	// OKF v0.2 Rule 2: sources array in frontmatter.
	if len(art.Sources) > 0 {
		fm.Sources = art.Sources
	} else if art.Source != "" {
		fm.Sources = []OKFSource{{Resource: art.Source}}
	}

	if !art.CreatedAt.IsZero() {
		fm.CreatedAt = art.CreatedAt.UTC().Format(time.RFC3339)
	}
	if !art.ArchivedAt.IsZero() {
		fm.ArchivedAt = art.ArchivedAt.UTC().Format(time.RFC3339)
	}
	if !art.StatusChangedAt.IsZero() {
		fm.StatusChangedAt = art.StatusChangedAt.UTC().Format(time.RFC3339)
	}
	if art.LockedBy != "" {
		fm.LockedBy = art.LockedBy
		if !art.LockedAt.IsZero() {
			fm.LockedAt = art.LockedAt.UTC().Format(time.RFC3339)
		}
	}
	if !art.TrainedAt.IsZero() {
		fm.TrainedAt = art.TrainedAt.UTC().Format(time.RFC3339)
	}
	fm.TrainedVersion = art.TrainedVersion
	fm.TrainedParentVersion = art.TrainedParentVersion
	fm.TrainedCandidate = art.TrainedCandidate
	fm.TrainedValScore = art.TrainedValScore
	fm.TrainedEvalHash = art.TrainedEvalHash
	if !art.TrainedRevokedAt.IsZero() {
		fm.TrainedRevokedAt = art.TrainedRevokedAt.UTC().Format(time.RFC3339)
	}
	fm.TrainedRevokedReason = art.TrainedRevokedReason

	out, err := yaml.Marshal(&fm)
	if err != nil {
		// yaml.Marshal of a plain struct does not realistically fail; fall back to a minimal block.
		return fmt.Sprintf("---\ntype: %s\ntitle: %s\nslug: %s\n---\n", fm.Type, fm.Title, fm.Slug)
	}
	return "---\n" + string(out) + "---\n"
}

// IndexArticle adds or updates a single article inside the Bleve search index.
func (s *Storage) IndexArticle(art *Article) error {
	return s.SearchIndex.Index(art.Slug, art)
}

// UnindexArticle deletes a single article from the Bleve search index.
func (s *Storage) UnindexArticle(slug string) error {
	return s.SearchIndex.Delete(slug)
}

// bootIndexBatchSize is how many documents are indexed per Bleve batch during boot
// synchronization.
//
// Batching is the whole cost of this function. Bleve's Index() is one transaction per call — a
// segment write and a store commit each time — so indexing document-by-document made startup
// linear in the corpus at roughly 24 ms per document: 26 s at 1,000 documents, four minutes at
// 10,000, with the server not answering for any of it. Batching amortizes the commit across a
// chunk instead.
//
// The chunk is bounded rather than "one batch for everything" because a batch is held in memory
// until it is executed, and a single batch over an entire large wiki would trade a startup delay
// for a startup allocation spike. 500 is comfortably past the point where per-commit overhead
// stops dominating.
const bootIndexBatchSize = 500

// SyncSearchIndex populates the Bleve index with all existing Markdown articles on startup and reconciles discrepancies.
func (s *Storage) SyncSearchIndex() error {
	_, _ = fmt.Fprintf(os.Stderr, "Commencing search index boot synchronization and reconciliation...\n")
	articles, err := s.ListArticles()
	if err != nil {
		return fmt.Errorf("failed to list articles for indexing: %w", err)
	}

	validSlugs := make(map[string]bool)
	// Always index the home page (since it is excluded from ListArticles)
	validSlugs["home"] = true

	batch := s.SearchIndex.NewBatch()
	// flush executes the pending batch and starts a fresh one. A batch failure is reported and
	// discarded rather than returned: boot indexing is best-effort reconciliation, and refusing to
	// start the server because one chunk of documents would not index is a worse outcome than
	// starting with an incomplete index that the next write repairs.
	flush := func() {
		if batch.Size() == 0 {
			return
		}
		if err := s.SearchIndex.Batch(batch); err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "Warning: failed to index a batch of %d articles: %v\n", batch.Size(), err)
		}
		batch = s.SearchIndex.NewBatch()
	}

	if homeArt, err := s.GetArticle("home"); err == nil {
		if err := batch.Index(homeArt.Slug, homeArt); err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "Warning: failed to index 'home' article: %v\n", err)
		}
	}

	for _, item := range articles {
		validSlugs[item.Slug] = true
		art, err := s.GetArticle(item.Slug)
		if err != nil {
			continue
		}
		if err := batch.Index(art.Slug, art); err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "Warning: failed to index article '%s': %v\n", item.Slug, err)
			continue
		}
		if batch.Size() >= bootIndexBatchSize {
			flush()
		}
	}
	flush()

	// Clean up any orphaned documents in the index that no longer exist on disk.
	//
	// DocCount bounds the request instead of a hardcoded ceiling: the previous Size of 1,000,000
	// asked Bleve to size a top-N collector for a million hits regardless of how many documents
	// existed, which is an allocation that scales with the constant rather than the corpus.
	docCount, err := s.SearchIndex.DocCount()
	if err != nil {
		docCount = 0
	}
	if docCount > 0 {
		q := bleve.NewMatchAllQuery()
		searchRequest := bleve.NewSearchRequest(q)
		searchRequest.Size = int(docCount)
		results, err := s.SearchIndex.Search(searchRequest)
		if err == nil {
			deletions := s.SearchIndex.NewBatch()
			for _, hit := range results.Hits {
				if !validSlugs[hit.ID] {
					_, _ = fmt.Fprintf(os.Stderr, "Removing orphaned article '%s' from search index...\n", hit.ID)
					deletions.Delete(hit.ID)
				}
			}
			if deletions.Size() > 0 {
				if err := s.SearchIndex.Batch(deletions); err != nil {
					_, _ = fmt.Fprintf(os.Stderr, "Warning: failed to remove orphaned index entries: %v\n", err)
				}
			}
		}
	}

	newCount, _ := s.SearchIndex.DocCount()
	_, _ = fmt.Fprintf(os.Stderr, "Boot synchronization complete. Search index contains %d articles.\n", newCount)
	return nil
}

// SearchResult represents a single full-text query match.
type SearchResult struct {
	Title string `json:"title"`
	Slug  string `json:"slug"`
	// Type is the OKF document class of the hit. Faceted searches span types, so a caller has to
	// be able to tell a wiki article from an agent memory in the results.
	Type      string    `json:"type,omitempty"`
	Score     float64   `json:"score"`
	Timestamp time.Time `json:"timestamp"`
	// Snippets are HTML fragments: article text is entity-escaped and matched terms are
	// wrapped in <mark>. The frontend renders them as HTML, so every producer of a snippet
	// MUST escape the article text it embeds — see the fallback path in SearchArticles.
	Snippets []string `json:"snippets"`
	Tags     []string `json:"tags,omitempty"`
}

// SearchArticles is the human/browser convenience wrapper: whole-corpus search across wiki
// articles, agent memories, plans, and skills (same default as the search_wiki MCP tool).
// Archived documents stay excluded unless the query itself names them — the browser UI has no
// archived toggle, so typing "archived" is the only way a human can surface them.
func (s *Storage) SearchArticles(queryStr string) ([]SearchResult, error) {
	return s.SearchArticlesWithOptions(queryStr, SearchOptions{
		IncludeArchived: strings.Contains(strings.ToLower(queryStr), "archived"),
	})
}

// SearchArticlesWithOptions runs a full-text search with explicit facets.
//
// The default (zero value) spans every document type: memories and plans are the point of the
// second brain, and hiding them means re-deriving knowledge already recorded. Facets make
// narrowing an explicit choice by the caller rather than a property of the query text.
func (s *Storage) SearchArticlesWithOptions(queryStr string, opts SearchOptions) ([]SearchResult, error) {
	if queryStr == "" {
		return []SearchResult{}, nil
	}

	limit := opts.Limit
	if limit <= 0 {
		limit = defaultSearchLimit
	}
	if limit > maxSearchLimit {
		limit = maxSearchLimit
	}

	// An explicit "archived" entry in the tags facet implies IncludeArchived, mirroring the
	// browser's query-text heuristic. Without this, asking for archived documents the obvious way
	// — search_wiki(tags: ["archived"]) — returned zero results with no explanation: the archived
	// filter ran before the tag filter and discarded every hit the caller was asking for.
	for _, t := range opts.Tags {
		if strings.EqualFold(strings.TrimSpace(t), "archived") {
			opts.IncludeArchived = true
			break
		}
	}

	// Create a query matching terms (supports boolean logic, wildcards, fuzzy matching natively!)
	q := bleve.NewQueryStringQuery(queryStr)
	searchRequest := bleve.NewSearchRequest(q)

	// Configure Bleve highlighter style to wrap matched words in HTML <mark> tags
	searchRequest.Highlight = bleve.NewHighlightWithStyle("html")
	searchRequest.Highlight.AddField("content")
	searchRequest.Highlight.AddField("title")

	// Over-fetch: every filter below runs *after* Bleve has scored and truncated, so asking for
	// exactly `limit` hits silently returns fewer than requested whenever anything is dropped.
	//
	// This must not be conditional on opts.hasFilters(). Three filters apply to every search
	// regardless of the caller's facets: "home" is always excluded, archived documents are
	// excluded by default, and a hit whose file has been deleted is skipped. The home page in
	// particular mentions the wiki's own name constantly, so it scores highly on the most
	// ordinary queries — which made an unfaceted `limit: N` reliably return N-1.
	searchRequest.Size = maxSearchLimit

	searchResults, err := s.SearchIndex.Search(searchRequest)
	if err != nil {
		return nil, fmt.Errorf("bleve search failed: %w", err)
	}

	typeFilter := normalizeTypeFilter(opts.Types)
	tagFilter := lowercaseSet(opts.Tags)
	queryLower := strings.ToLower(queryStr)

	var results []SearchResult
	for _, hit := range searchResults.Hits {
		if len(results) >= limit {
			break
		}

		// Metadata is enough to decide whether a hit survives filtering; only the fallback
		// snippet below needs the body, and Bleve supplies fragments for most hits. Reading the
		// full file for every hit up front was pure waste.
		art, err := s.metaBySlug(hit.ID)
		if err != nil {
			// Skip if the physical Markdown file was deleted on disk but search index is slightly out of sync
			continue
		}

		// Exclude "home" from search results (reserved for Hero dashboard)
		if art.Slug == "home" {
			continue
		}

		if !opts.allowsArchived(art, queryLower) {
			continue
		}
		if !opts.allowsType(art, typeFilter, queryStr, queryLower) {
			continue
		}
		if !matchesAllTags(art.Tags, tagFilter) {
			continue
		}
		// Kind narrows to memories of that kind, and to nothing else: no other class carries the
		// field, so a match on "" would silently widen the facet to the whole corpus.
		if opts.MemoryKind != "" && art.MemoryKind != opts.MemoryKind {
			continue
		}

		var snippets []string
		if frags, ok := hit.Fragments["content"]; ok {
			snippets = frags
		} else if frags, ok := hit.Fragments["title"]; ok {
			snippets = frags
		}

		// Fallback snippet if Bleve returns empty fragments (extract first 150 characters).
		// Bleve escapes the fragments it produces; this path must escape too, because the
		// frontend renders snippets as raw HTML. Without it, an article body starting with
		// <img src=x onerror=...> becomes stored XSS in the search dropdown.
		if len(snippets) == 0 {
			body := art.ContentPreview
			if full, err := s.GetArticle(art.Slug); err == nil {
				body = full.Content
			}
			runes := []rune(body)
			limit := 150
			if len(runes) < limit {
				limit = len(runes)
			}
			snippets = []string{html.EscapeString(string(runes[:limit])) + "..."}
		}

		results = append(results, SearchResult{
			Title:     art.Title,
			Slug:      art.Slug,
			Type:      art.Type,
			Score:     hit.Score,
			Timestamp: art.Timestamp,
			Snippets:  snippets,
			Tags:      art.Tags,
		})
	}

	return results, nil
}

// Helpers for reading/writing Gzip files

func writeGzippedFile(filePath string, data []byte) error {
	file, err := os.Create(filePath)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()

	gw := gzip.NewWriter(file)
	defer func() { _ = gw.Close() }()

	_, err = gw.Write(data)
	return err
}

func readGzippedFile(filePath string) ([]byte, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()

	gr, err := gzip.NewReader(file)
	if err != nil {
		return nil, err
	}
	defer func() { _ = gr.Close() }()

	return io.ReadAll(gr)
}

// GetArticleHistory returns metadata for all historical versions of an article (newest first).
func (s *Storage) GetArticleHistory(slug string) ([]Article, error) {
	cleanedSlug := Slugify(slug)
	if cleanedSlug == "" {
		return nil, fmt.Errorf("invalid slug")
	}

	histFolder := filepath.Join(s.HistoryDir, cleanedSlug)
	files, err := os.ReadDir(histFolder)
	if err != nil {
		if os.IsNotExist(err) {
			return []Article{}, nil
		}
		return nil, fmt.Errorf("failed to read history directory: %w", err)
	}

	var history []Article
	for _, file := range files {
		if file.IsDir() || !strings.HasSuffix(file.Name(), ".md.gz") {
			continue
		}

		filePath := filepath.Join(histFolder, file.Name())
		data, err := readGzippedFile(filePath)
		if err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "Warning: failed to read history version file %s: %v\n", file.Name(), err)
			continue
		}

		art, err := parseArticleFile(data, false)
		if err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "Warning: failed to parse history version file %s: %v\n", file.Name(), err)
			continue
		}

		history = append(history, *art)
	}

	// Sort history by version descending
	sort.Slice(history, func(i, j int) bool {
		return history[i].Version > history[j].Version
	})

	return history, nil
}

// GetArticleVersion reads a single historical version of an article.
func (s *Storage) GetArticleVersion(slug string, version int) (*Article, error) {
	cleanedSlug := Slugify(slug)
	if cleanedSlug == "" {
		return nil, fmt.Errorf("invalid slug")
	}

	filePath := filepath.Join(s.HistoryDir, cleanedSlug, fmt.Sprintf("%d.md.gz", version))
	data, err := readGzippedFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("version %d not found for article %s: %w", version, slug, err)
	}

	return parseArticleFile(data, true)
}

// ErrVersionConflict reports that an article changed since the client loaded it. Callers map
// this to HTTP 409.
var ErrVersionConflict = errors.New("article has been updated in another session")

// ArticleEdit describes a user-initiated edit of an existing article. Nil pointer fields
// preserve the stored value; an explicit empty string clears it. LoadedVersion enables the
// optimistic-locking guard (0 disables it).
type ArticleEdit struct {
	Title       string
	Content     string
	Description *string
	Source      *string
	Resource    *string
	EditSummary string
	// Tags is a pointer so "omitted" and "explicitly empty" stay distinguishable: nil keeps the
	// article's current tags untouched, while a non-nil (even empty) slice replaces them after
	// memory-scope validation. Collapsing the two would make a caller that simply doesn't manage
	// tags silently strip every free tag off the document.
	Tags *[]string
	// Status is a pointer for the same reason: nil preserves the document's current lifecycle
	// state, so an editor that does not manage status cannot silently reset a completed plan.
	Status *string
	// MemoryKind is a pointer for the same reason again: nil preserves a memory's existing
	// classification, so editing a memory's body cannot silently blank the axis that decides
	// how it is recalled.
	MemoryKind    *string
	LoadedVersion int

	// OKF v0.2 optional edit fields
	Sources    *[]OKFSource
	StaleAfter *time.Time
	Generated  *OKFGenerated
	Verified   *[]OKFVerification
}

// ApplyArticleEdit loads the article, verifies LoadedVersion, merges the optional fields, and
// writes — all under a single lock. Doing the check and the write atomically is the point:
// performing them separately lets a concurrent writer land in between, so the guard passes and
// the other session's edit is silently overwritten anyway.
//
// The document type is immutable here; regular edits never relabel a reserved OKF class.
func (s *Storage) ApplyArticleEdit(slug string, edit ArticleEdit) (*Article, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	existing, err := s.GetArticle(slug)
	if err != nil {
		return nil, err
	}

	// A locked skill is agent-read-only: it advances only through the skill-candidate
	// promote path, never through direct edits.
	if err := checkSkillLock(existing); err != nil {
		return nil, err
	}

	// A stored version of 0 means the file predates versioning — there is nothing to compare
	// against, so any loaded_version is accepted. Once an article has a version, the caller must
	// supply the matching one: a 0 against a versioned article is a stale read, not a waiver of
	// the check. (This condition used to also require edit.LoadedVersion > 0, which turned an
	// omitted version into a silent bypass of optimistic locking on every versioned article.)
	if existing.Version > 0 && existing.Version != edit.LoadedVersion {
		return nil, ErrVersionConflict
	}

	description := existing.Description
	if edit.Description != nil {
		description = *edit.Description
	}
	source := existing.Source
	if edit.Source != nil {
		source = *edit.Source
	}
	resource := existing.Resource
	if edit.Resource != nil {
		resource = *edit.Resource
	}

	// Preserve tool-managed memory-scope tags a user edit must not be able to drop or forge.
	cleanedTags := existing.Tags
	if edit.Tags != nil {
		cleanedTags = validateAndCleanUserTags(*edit.Tags, existing.Tags, existing.Type)
	}

	return s.saveArticleLocked(slug, edit.Title, edit.Content, description, source, resource,
		edit.EditSummary, cleanedTags, existing.Type, ArticleOverrides{
			Status:     edit.Status,
			MemoryKind: edit.MemoryKind,
			Sources:    edit.Sources,
			StaleAfter: edit.StaleAfter,
			Generated:  edit.Generated,
			Verified:   edit.Verified,
		})
}

// RevertArticle rolls the current active document back to the content of a historical version.
func (s *Storage) RevertArticle(slug string, version int) (*Article, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	histArt, err := s.GetArticleVersion(slug, version)
	if err != nil {
		return nil, err
	}

	// Reverting a locked skill would rewrite its body outside the candidate flow.
	if live, err := s.GetArticle(slug); err == nil {
		if err := checkSkillLock(live); err != nil {
			return nil, err
		}
	}

	summary := fmt.Sprintf("Reverted to version %d", version)
	// A revision written before status became a field carries it as a tag, so lift it out rather
	// than let a revert resurrect a tag set that validation now rejects. A revision that already
	// has the field keeps it.
	status, tags := ExtractLegacyStatus(histArt.Type, histArt.Tags)
	if histArt.Status != "" {
		status = histArt.Status
	}
	return s.saveArticleLocked(slug, histArt.Title, histArt.Content, histArt.Description, histArt.Source, histArt.Resource, summary, tags, histArt.Type, ArticleOverrides{
		Status:     &status,
		Sources:    &histArt.Sources,
		StaleAfter: &histArt.StaleAfter,
		Verified:   &histArt.Verified,
	})
}

// UpdateArticleTags updates only the tag array for an article without modifying the title or content.
// The version check and the write happen under one lock, so the optimistic guard cannot be
// defeated by a concurrent writer slipping in between them.
func (s *Storage) UpdateArticleTags(slug string, tags []string, loadedVersion int, editSummary string) (*Article, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	art, err := s.GetArticle(slug)
	if err != nil {
		return nil, err
	}

	// Tag writes are writes: a locked skill accepts none from agent paths.
	if err := checkSkillLock(art); err != nil {
		return nil, err
	}

	if loadedVersion > 0 && art.Version > 0 && art.Version != loadedVersion {
		// Wraps the sentinel so the MCP layer can errors.Is it and answer with the version to
		// retry on. The REST handler never reaches this: HandleUpdateArticleTags does its own
		// check and returns 409 before calling in.
		return nil, fmt.Errorf("%w: loaded version %d, current version %d", ErrVersionConflict, loadedVersion, art.Version)
	}

	if editSummary == "" {
		editSummary = "Updated article tags"
	}

	return s.saveArticleLocked(slug, art.Title, art.Content, art.Description, art.Source, art.Resource, editSummary, tags, art.Type, ArticleOverrides{
		Sources:    &art.Sources,
		StaleAfter: &art.StaleAfter,
		Verified:   &art.Verified,
		Generated:  art.Generated,
	})
}

// Close releases resources held by the Storage, including the Bleve search index.
// It is safe to call multiple times; only the first invocation performs the close.
func (s *Storage) Close() error {
	var err error
	s.closeOnce.Do(func() {
		if s.SearchIndex != nil {
			err = s.SearchIndex.Close()
		}
	})
	return err
}

// CleanupArchivedArticles removes articles that have been tagged as archived
// and whose archive time has elapsed based on the configured delay.
func (s *Storage) CleanupArchivedArticles() error {
	// Get the configured delay from environment variable
	delayStr := os.Getenv("NEXWIKI_AUTO_DELETE_ARCHIVED_AFTER_DAYS")
	if delayStr == "" {
		delayStr = "0" // Default to disabled
	}

	delay, err := strconv.Atoi(delayStr)
	if err != nil {
		return fmt.Errorf("invalid NEXWIKI_AUTO_DELETE_ARCHIVED_AFTER_DAYS value: %w", err)
	}

	// If delay is 0, auto-deletion is disabled
	if delay <= 0 {
		return nil
	}

	// List all articles
	articles, err := s.ListArticles()
	if err != nil {
		return fmt.Errorf("failed to list articles for cleanup: %w", err)
	}

	// Check each article
	for _, art := range articles {
		// Skip if not archived
		if art.ArchivedAt.IsZero() {
			continue
		}

		// Calculate if the delay has elapsed
		elapsed := time.Since(art.ArchivedAt)
		if elapsed >= time.Duration(delay)*24*time.Hour {
			// Delete the article
			err = s.DeleteArticle(art.Slug)
			if err != nil {
				return fmt.Errorf("failed to delete archived article %s: %w", art.Slug, err)
			}
			_, _ = fmt.Fprintf(os.Stderr, "Deleted archived article: %s (archived at: %s)\n", art.Slug, art.ArchivedAt.Format(time.RFC3339))
		}
	}

	return nil
}

// DeleteTagGlobally removes a tag from all articles in the wiki.
// Enforces validation: it returns an error if the tag is a tool-managed memory-scope tag,
// or the tool-managed trained marker tag (story 06), whose removal belongs to the
// explicit unlock path (UnlockTrainedMarker) with its audit record.
func (s *Storage) DeleteTagGlobally(tag string) error {
	tagLower := strings.ToLower(tag)
	if strings.HasPrefix(tagLower, MemoryScopeTagPrefix) {
		return fmt.Errorf("cannot delete protected memory-scope tag: %s", tag)
	}
	if tagLower == TrainedMarkerTag {
		return fmt.Errorf("cannot delete the trained marker tag '%s': it is tool-managed — re-train the skill or use the explicit unlock path (UnlockTrainedMarker), which records the removal with an audit entry", tag)
	}

	// Held across the whole sweep so the operation is all-or-nothing with respect to other
	// writers, rather than interleaving per-article saves with concurrent edits.
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	articles, err := s.ListArticles()
	if err != nil {
		return err
	}

	for _, artMeta := range articles {
		art, err := s.GetArticle(artMeta.Slug)
		if err != nil {
			continue
		}

		// Check if tag is present
		tagIndex := -1
		for i, t := range art.Tags {
			if strings.ToLower(t) == tagLower {
				tagIndex = i
				break
			}
		}

		if tagIndex != -1 {
			// Remove the tag
			newTags := append(art.Tags[:tagIndex], art.Tags[tagIndex+1:]...)
			// Save the updated article
			_, err = s.saveArticleLocked(art.Slug, art.Title, art.Content, art.Description, art.Source, art.Resource, fmt.Sprintf("Removed tag '%s' globally", tag), newTags, art.Type, ArticleOverrides{})
			if err != nil {
				return fmt.Errorf("failed to update article %s during global tag deletion: %w", art.Slug, err)
			}
		}
	}

	return nil
}
