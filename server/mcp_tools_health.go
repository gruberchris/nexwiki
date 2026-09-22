package server

import (
	"encoding/json"
	"fmt"
	"path"
	"sort"
	"strings"
	"time"
)

// This file holds wiki_health, the maintenance tool. Everything it reports is something the wiki
// already knows but never volunteers: a page nothing links to, a link that goes nowhere, a memory
// with no provenance, a plan that has been "in progress" for a month.
//
// The checks are one tool rather than several because they answer one question — "what needs
// attention?" — and an agent that has to make six calls to ask it will make none. The memory
// hygiene checks (§6.8) live in mcp_tools_hygiene.go for the same reason they were added here
// rather than as a new tool.

// Default and ceiling values for the tool's arguments.
const (
	defaultStaleDays    = 30
	defaultHealthLimit  = 50
	maxHealthLimit      = 500
	minimumStaleDayspan = 1
)

// inFlightStatusTags are the lifecycle tags that say work is underway. Carrying one is not
// required for a plan to be stale — it only makes the report more specific about why.
// "implementing" is the closed-vocabulary state; the legacy synonyms stay listed so a corpus
// imported from an older bundle still reads correctly.
var inFlightStatusTags = []string{"implementing", "wip", "in-progress", "draft", "active", "todo", "pending", "review", "blocked"}

// finishedStatusTags are the tags that end a plan's life. A plan carrying one is never stale,
// however old: nagging about work the user has already marked done is worse than staying quiet.
// "superseded" counts as finished because a replaced plan is not waiting on anyone either.
var finishedStatusTags = []string{"completed", "done", "superseded"}

// parkedStatusTags mark work deliberately set aside or deliberately unending. A parked plan is
// not stale and is not finished, and until now the tool had no way to say so. "evergreen" —
// a running backlog with no finish line — is exempt for the same reason: it is a decision, and
// re-reporting a decision is noise.
//
// This gap was found by running the report against the real corpus: two of its five stale plans
// were `git-backed-storage-backend-for-nexwiki` and `hybrid-vector-semantic-search-for-nexwiki` —
// the two product bets this very code review deliberately deferred. They are not abandoned and
// they are not done, so they would have been reported as needing attention forever. A check that
// keeps naming things the user has already decided about is one they learn to ignore.
var parkedStatusTags = []string{"parked", "evergreen", "deferred", "tabled", "on-hold", "someday"}

// HealthFinding is one item needing attention, in whichever category found it.
type HealthFinding struct {
	Slug string `json:"slug"`
	// Title is the document's title, or for a broken link, the link text as written.
	Title string `json:"title"`
	// Type is the OKF document class of the document the finding is about.
	Type string `json:"type,omitempty"`
	// Detail explains the finding in a form an agent can act on directly.
	Detail string `json:"detail"`
}

// MisplacedDocumentFinding is a misplaced document as wiki_health reports it: where the file is, the
// slug it declares, and a fix that is safe to follow. See misplacedRemedy.
type MisplacedDocumentFinding struct {
	Path   string `json:"path"`
	Slug   string `json:"slug"`
	Remedy string `json:"remedy"`
}

// HealthOutput is the wiki_health payload. Counts are reported separately from the item lists
// because the lists are capped: a wiki with 400 orphans should say so without returning 400 items.
type HealthOutput struct {
	TotalDocuments int  `json:"total_documents"`
	StaleDays      int  `json:"stale_days"`
	Limit          int  `json:"limit"`
	Truncated      bool `json:"truncated"`

	// UnreadableFileCount and UnreadableFiles report article files the scan could not read or
	// parse, and directories it could not list. Such a file, or every article in such a directory,
	// is missing from total_documents and from every other category, so without this a document
	// with broken front matter would not exist as far as the report could tell.
	UnreadableFileCount int              `json:"unreadable_file_count"`
	UnreadableFiles     []UnreadableFile `json:"unreadable_files"`

	// MisplacedDocumentCount and MisplacedDocuments report documents that parse but are not stored
	// as <slug>.md directly in the article directory: one in a subfolder, a copy whose filename
	// differs from its slug, or one whose slug is not in slug form. Every scan leaves one out and no
	// lookup finds one, so like an unreadable file it is missing from listings, search, export,
	// total_documents, and every other category.
	MisplacedDocumentCount int                        `json:"misplaced_document_count"`
	MisplacedDocuments     []MisplacedDocumentFinding `json:"misplaced_documents"`

	OrphanCount     int             `json:"orphan_count"`
	Orphans         []HealthFinding `json:"orphans"`
	BrokenLinkCount int             `json:"broken_link_count"`
	BrokenLinks     []BrokenLinkRef `json:"broken_links"`
	UnsourcedCount  int             `json:"unsourced_memory_count"`
	UnsourcedMemory []HealthFinding `json:"unsourced_memories"`

	// UnkindedCount and UnkindedMemories report memories written before the kind axis existed.
	// This is the burn-down list for classifying them, and it is deliberately the whole
	// migration strategy: new writes require a kind, existing memories stay valid, and the
	// backlog is reported rather than guessed at by a script. Deciding project vs reference for
	// an existing memory is a judgment call per memory, not a mechanical rewrite.
	UnkindedCount    int             `json:"unkinded_memory_count"`
	UnkindedMemories []HealthFinding `json:"unkinded_memories"`

	// ContestedCount and ContestedMemories report memories holding an unresolved conflict — an
	// agent recorded evidence against the stored claim and could not adjudicate. They surface
	// here because this is where an agent already looks for what needs attention, and a
	// contradiction nobody surfaces is a contradiction nobody resolves.
	ContestedCount    int             `json:"contested_memory_count"`
	ContestedMemories []HealthFinding `json:"contested_memories"`
	StalePlanCount    int             `json:"stale_plan_count"`
	StalePlans        []HealthFinding `json:"stale_plans"`
	StaleConceptCount int             `json:"stale_concept_count"`
	StaleConcepts     []HealthFinding `json:"stale_concepts"`

	// UnreferencedSkillCount and UnreferencedSkills report skills nothing points an agent at.
	// Kept separate from Orphans because the remedy differs: an orphaned article wants a link
	// from a related page, whereas an unreferenced skill wants a read_article call in another
	// document — or retiring.
	UnreferencedSkillCount int             `json:"unreferenced_skill_count"`
	UnreferencedSkills     []HealthFinding `json:"unreferenced_skills"`

	// --- memory hygiene (§6.8) ---

	// ColdDays is the recency threshold applied to memories.
	ColdDays int `json:"cold_days"`
	// ColdMemoryScanRan is false when the activity log does not reach back ColdDays, in which case
	// the cold check is skipped rather than reporting every memory. See scanColdMemories.
	ColdMemoryScanRan bool                  `json:"cold_memory_scan_ran"`
	ColdMemorySkipped string                `json:"cold_memory_skipped_reason,omitempty"`
	ColdMemoryCount   int                   `json:"cold_memory_count"`
	ColdMemories      []HealthFinding       `json:"cold_memories"`
	DuplicateCount    int                   `json:"duplicate_memory_count"`
	DuplicateMemories []DuplicateMemoryPair `json:"duplicate_memories"`
	ParkedPlanCount   int                   `json:"parked_plan_count"`

	// PlanStatusCensus counts plans per lifecycle status (archived plans included, unlike every
	// other check), so the report answers "where does the plan corpus stand?" at a glance.
	PlanStatusCensus map[string]int `json:"plan_status_census,omitempty"`
}

// planStatusCensusSchema declares one integer property per possible census key, since the schema
// walker used by the conformance tests has no additionalProperties support.
func planStatusCensusSchema() map[string]interface{} {
	props := map[string]interface{}{}
	for _, s := range PlanStatusTags {
		props[s] = schemaOf("integer", "Plans currently in '"+s+"'.")
	}
	props["(none)"] = schemaOf("integer", "Plans carrying no status (pre-migration stragglers).")
	census := schemaObject(props)
	census["description"] = "Plans per lifecycle status, archived included."
	return census
}

func healthOutputSchema() map[string]interface{} {
	finding := schemaObject(map[string]interface{}{
		"slug":   schemaOf("string", "Slug of the document the finding concerns."),
		"title":  schemaOf("string", "Document title, or for a broken link, the link text as written."),
		"type":   schemaOf("string", "OKF document class of the document."),
		"detail": schemaOf("string", "What is wrong, phrased as something to act on."),
	}, "slug", "title", "detail")

	broken := brokenLinkSchema()

	duplicate := schemaObject(map[string]interface{}{
		"slug":       schemaOf("string", "One memory of the pair."),
		"title":      schemaOf("string", "Title of that memory."),
		"other_slug": schemaOf("string", "The memory it resembles."),
		"scope":      schemaOf("string", "The memory-<scope> both share; absent for unscoped memories."),
		"detail":     schemaOf("string", "What to do about it."),
	}, "slug", "title", "other_slug", "detail")

	unreadable := schemaObject(map[string]interface{}{
		"path":  schemaOf("string", "Path relative to the article directory, slash-separated. A directory that could not be listed appears with a trailing /, standing for every article in it."),
		"error": schemaOf("string", "Why the file could not be read or parsed, or the directory could not be listed."),
	}, "path", "error")

	misplaced := schemaObject(map[string]interface{}{
		"path":   schemaOf("string", "Path relative to the article directory, slash-separated."),
		"slug":   schemaOf("string", "The document's slug, declared in its front matter or derived from its title. A document belongs at <slug>.md directly in the article directory, with a slug of lowercase letters, digits, and hyphens."),
		"remedy": schemaOf("string", "How to fix it without overwriting another file: it never suggests moving the document to a path another file already holds."),
	}, "path", "slug", "remedy")

	return schemaObject(map[string]interface{}{
		"total_documents":            schemaOf("integer", "Documents scanned, including the home dashboard."),
		"stale_days":                 schemaOf("integer", "Age threshold applied to in-flight plans."),
		"limit":                      schemaOf("integer", "Maximum items returned per category."),
		"truncated":                  schemaOf("boolean", "True when a category hit the limit and its list is shorter than its count."),
		"unreadable_file_count":      schemaOf("integer", "Entries in unreadable_files: article files that could not be read or parsed, plus directories that could not be listed, each directory counted once however many articles it holds. None of them are in total_documents or any other check."),
		"unreadable_files":           schemaArrayOf(unreadable, "Unreadable article files and unlistable directories (path ending in /), sorted by path, up to the limit."),
		"misplaced_document_count":   schemaOf("integer", "Documents not stored as <slug>.md directly in the article directory: in a subfolder, under a filename that differs from their slug (case included), or with a slug not in slug form. Listings, search, and export leave them out, they cannot be opened or edited by slug, and none of them are in total_documents or any other check."),
		"misplaced_documents":        schemaArrayOf(misplaced, "Misplaced documents, sorted by path, up to the limit."),
		"orphan_count":               schemaOf("integer", "Documents no other document links to."),
		"orphans":                    schemaArrayOf(finding, "Orphaned documents, up to the limit."),
		"broken_link_count":          schemaOf("integer", "Internal links with no destination, in either link form."),
		"broken_links":               schemaArrayOf(broken, "Broken internal links, up to the limit."),
		"unsourced_memory_count":     schemaOf("integer", "Agent memories recorded without a source."),
		"unsourced_memories":         schemaArrayOf(finding, "Memories missing provenance, up to the limit."),
		"unkinded_memory_count":      schemaOf("integer", "Agent memories carrying no memory_kind — written before the kind axis existed."),
		"unkinded_memories":          schemaArrayOf(finding, "Unclassified memories, up to the limit. This is the backfill worklist."),
		"contested_memory_count":     schemaOf("integer", "Agent memories holding an unresolved conflict: a contested claim awaiting a human decision."),
		"contested_memories":         schemaArrayOf(finding, "Contested memories awaiting adjudication, up to the limit."),
		"stale_plan_count":           schemaOf("integer", "In-flight plans untouched for longer than stale_days. Excludes plans tagged finished or parked."),
		"stale_plans":                schemaArrayOf(finding, "Stale plans, up to the limit."),
		"stale_concept_count":        schemaOf("integer", "Concepts whose freshness expiration (stale_after) has passed."),
		"stale_concepts":             schemaArrayOf(finding, "Concepts whose freshness expiration has passed, up to the limit."),
		"unreferenced_skill_count":   schemaOf("integer", "Skills no live document links or names in a read_article call."),
		"unreferenced_skills":        schemaArrayOf(finding, "Unreferenced skills, up to the limit."),
		"cold_days":                  schemaOf("integer", "Recency threshold applied to memories."),
		"cold_memory_scan_ran":       schemaOf("boolean", "False when the activity log does not reach back cold_days, in which case the cold-memory check was skipped rather than reporting every memory."),
		"cold_memory_skipped_reason": schemaOf("string", "Why the cold-memory check did not run, when it did not."),
		"cold_memory_count":          schemaOf("integer", "Memories neither read nor edited within cold_days."),
		"cold_memories":              schemaArrayOf(finding, "Cold memories, up to the limit."),
		"duplicate_memory_count":     schemaOf("integer", "Pairs of memories in the same scope with closely matching titles."),
		"duplicate_memories":         schemaArrayOf(duplicate, "Near-duplicate memory pairs, up to the limit."),
		"parked_plan_count":          schemaOf("integer", "Plans deliberately set aside; reported as a count only, since they need no action."),
		"plan_status_census":         planStatusCensusSchema(),
	}, "total_documents", "stale_days", "limit", "truncated",
		"unreadable_file_count", "unreadable_files",
		"misplaced_document_count", "misplaced_documents",
		"orphan_count", "orphans", "broken_link_count", "broken_links",
		"unsourced_memory_count", "unsourced_memories",
		"unkinded_memory_count", "unkinded_memories",
		"contested_memory_count", "contested_memories", "stale_plan_count", "stale_plans",
		"stale_concept_count", "stale_concepts",
		"unreferenced_skill_count", "unreferenced_skills",
		"cold_days", "cold_memory_scan_ran", "cold_memory_count", "cold_memories",
		"duplicate_memory_count", "duplicate_memories", "parked_plan_count")
}

var wikiHealthTool = toolDef{
	Schema: map[string]interface{}{
		"name":        "wiki_health",
		"description": "Audit the knowledge base for maintenance work: article files that cannot be read or parsed (such as malformed front matter) and article folders that cannot be listed, documents stored somewhere other than articles/<slug>.md (in a subfolder, copied under another filename, or with a slug not in slug form), which cannot be opened and which listings, search, and every other check leave out, orphan pages nothing links to, broken internal links (both [[WikiLinks]] and absolute [text](/articles/<slug>) Markdown links), agent memories recorded without a 'source' or without a 'memory_kind', in-flight plans that have gone stale, skills nothing points an agent at, memories nothing has read or edited in months, and near-duplicate memories in the same scope that may have drifted apart. Use it at the start of a maintenance session, or before a big reorganization, to find what needs attention without reading every document.",
		"inputSchema": map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"stale_days": map[string]interface{}{
					"type":        "integer",
					"description": "How many days an in-flight plan may go untouched before it counts as stale (default 30).",
				},
				"cold_days": map[string]interface{}{
					"type":        "integer",
					"description": "How many days a memory may go unread and unedited before it counts as cold (default 90). Skipped entirely when the activity log does not reach back that far.",
				},
				"limit": map[string]interface{}{
					"type":        "integer",
					"description": "Maximum items reported per category (default 50, maximum 500). Counts are always complete even when the lists are capped.",
				},
			},
		},
	},
	Output:   healthOutputSchema(),
	Handler:  (*Server).toolWikiHealth,
	Behavior: toolBehavior{Title: "Wiki Health", ReadOnly: true},
}

// HealthArgs holds optional filters/caps for the wiki_health tool.
type HealthArgs struct {
	StaleDays int `json:"stale_days"`
	ColdDays  int `json:"cold_days"`
	Limit     int `json:"limit"`
}

func (srv *Server) toolWikiHealth(args json.RawMessage) (interface{}, *JSONRPCError) {
	var hArgs HealthArgs
	if e := decodeToolArgs(args, &hArgs); e != nil {
		return nil, e
	}

	staleDays := hArgs.StaleDays
	if staleDays <= 0 {
		staleDays = defaultStaleDays
	}
	if staleDays < minimumStaleDayspan {
		staleDays = minimumStaleDayspan
	}
	limit := hArgs.Limit
	if limit <= 0 {
		limit = defaultHealthLimit
	}
	if limit > maxHealthLimit {
		limit = maxHealthLimit
	}

	graph, err := srv.Storage.ScanLinkGraph()
	if err != nil {
		return ToolResponse{IsError: true, Content: []ToolContent{{Type: "text", Text: "Error scanning the wiki: " + srv.clientError(err)}}}, nil
	}

	slugs := make([]string, 0, len(graph.Meta))
	for slug := range graph.Meta {
		slugs = append(slugs, slug)
	}
	sort.Strings(slugs)

	coldDays := hArgs.ColdDays
	if coldDays <= 0 {
		coldDays = defaultColdMemoryDays
	}
	if coldDays < minimumStaleDayspan {
		coldDays = minimumStaleDayspan
	}

	now := time.Now()
	// Not AddDate, which wraps for a day count past about 1e14 and can put the cutoff in the future,
	// where every unfinished plan is stale. A capped count puts it some 292 years back.
	staleBefore := now.Add(-daysToDuration(staleDays))
	orphans := []HealthFinding{}
	unsourced := []HealthFinding{}
	unkinded := []HealthFinding{}
	contested := []HealthFinding{}
	stalePlans := []HealthFinding{}
	staleConcepts := []HealthFinding{}
	unreferencedSkills := []HealthFinding{}
	memories := []Article{}
	parkedPlans := 0

	liveRefs := liveReferencedSlugs(graph)

	planCensus := make(map[string]int)

	for _, slug := range slugs {
		doc := graph.Meta[slug]

		// The census counts every plan, archived included, before the archived skip below —
		// "archived: 12" is exactly the kind of standing the report exists to surface.
		if doc.Type == ContentTypePlan {
			if doc.Status == "" {
				planCensus["(none)"]++
			} else {
				planCensus[doc.Status]++
			}
		}

		// Archived documents are deliberately out of scope for every check. Archiving is the
		// user saying "this is done"; reporting it as needing attention inverts that.
		if IsArchived(&doc) {
			continue
		}

		// Check for any document where doc.IsStale (i.e. !doc.StaleAfter.IsZero() && now.After(doc.StaleAfter))
		if doc.IsStale || (!doc.StaleAfter.IsZero() && !now.Before(doc.StaleAfter)) {
			tier := doc.TrustTier
			if tier == "" {
				tier = doc.DeriveTrustTier()
			}
			staleConcepts = append(staleConcepts, HealthFinding{
				Slug:   slug,
				Title:  doc.Title,
				Type:   doc.Type,
				Detail: fmt.Sprintf("Expired on %s (trust tier: %s)", doc.StaleAfter.Format("2006-01-02"), tier),
			})
		}

		// Orphan detection applies to wiki articles only. Memories, plans, and skills are reached
		// through their own list tools, the search facets, and the context overview — nobody links
		// to a memory, so calling every one of them an orphan is noise. Measured on the real
		// 83-document corpus: reporting every type flagged 70 documents, 27 of which were agent
		// documents behaving exactly as designed.
		//
		// The graph counts both internal link forms (§3.21). While it counted only [[WikiLinks]]
		// this check still fired on 44 of 84 real documents, almost all of them linked from the
		// home page in Markdown syntax; counting both took it to 2.
		//
		// home is the dashboard, not a leaf page: nothing links to a front page.
		if doc.Type == ContentTypeWiki && slug != "home" && graph.InboundCount[slug] == 0 {
			orphans = append(orphans, HealthFinding{
				Slug: slug, Title: doc.Title, Type: doc.Type,
				Detail: "No article links here. Link it from a related page, or archive it if it is finished.",
			})
		}

		// A skill is not reached the way an article is. Articles are linked; skills are invoked by
		// name, through a read_article(slug: "…") call written into another document. So this
		// check counts both links and in-code slug mentions, and reusing the orphan check here
		// would be wrong in both directions — measured on the real corpus, create-plan-skill (live
		// and wanted) has 0 inbound links, while enhanced-memory-decision-making-skill (dead) also
		// had 0 despite being named four times by the document that referenced it.
		//
		// No skill is exempt: nothing in code names a skill's slug, so every skill is reached
		// through a document or not at all.
		if doc.Type == ContentTypeSkill && !liveRefs[slug] {
			unreferencedSkills = append(unreferencedSkills, HealthFinding{
				Slug: slug, Title: doc.Title, Type: doc.Type,
				Detail: "Nothing points agents at this skill — no document links it or names its slug in a read_article call. Reference it from another document or skill, or archive it if it is retired.",
			})
		}

		if doc.Type == ContentTypeMemory {
			memories = append(memories, doc)
			if strings.TrimSpace(doc.Source) == "" {
				unsourced = append(unsourced, HealthFinding{
					Slug: slug, Title: doc.Title, Type: doc.Type,
					Detail: "Memory has no 'source'. A fact with no provenance cannot be re-verified later; set source with save_article.",
				})
			}
			if doc.MemoryKind == "" {
				unkinded = append(unkinded, HealthFinding{
					Slug: slug, Title: doc.Title, Type: doc.Type,
					Detail: "Memory has no 'memory_kind', so kind-filtered recall cannot find it. Classify it with save_article (memory_kind): 'project', 'reference', 'user', or 'feedback'.",
				})
			}
			if hasTag(doc.Tags, ContestedTag) {
				contested = append(contested, HealthFinding{
					Slug: slug, Title: doc.Title, Type: doc.Type,
					Detail: "Memory holds an unresolved conflict: an agent recorded evidence against the stored claim and could not adjudicate. Both claims are preserved in the body. Decide which is right, edit with change_intent 'correct', and remove the 'contested' tag.",
				})
			}
		}

		if doc.Type == ContentTypePlan && isParked(doc.Status) && !isFinished(doc.Status) {
			parkedPlans++
		}

		if doc.Type == ContentTypePlan && doc.Timestamp.Before(staleBefore) && !isFinished(doc.Status) && !isParked(doc.Status) {
			// An in-flight tag is not required. On the real corpus almost no plan carries one —
			// they hold a project tag and nothing else — so requiring "wip" made the check
			// incapable of ever firing. What actually matters is that the plan was never marked
			// finished and nobody has touched it since.
			since := doc.Timestamp.Format("2006-01-02")
			detail := fmt.Sprintf("Untouched since %s and never marked finished. Finish it, tag it 'completed', or archive it.", since)
			if status, inFlight := inFlightStatus(doc.Status); inFlight {
				detail = fmt.Sprintf("Tagged '%s' but untouched since %s. Finish it, tag it 'completed', or archive it.", status, since)
			}
			stalePlans = append(stalePlans, HealthFinding{
				Slug: slug, Title: doc.Title, Type: doc.Type, Detail: detail,
			})
		}
	}

	// Memory hygiene (§6.8). Both checks look only at memories, which the loop above collected.
	cold := scanColdMemories(ActivityLogPath(srv.Storage.DataDir), memories, coldDays)
	duplicates := findDuplicateMemories(memories, graph.Outbound)

	out := HealthOutput{
		TotalDocuments:         len(graph.Meta),
		StaleDays:              staleDays,
		Limit:                  limit,
		UnreadableFileCount:    len(graph.Unreadable),
		MisplacedDocumentCount: len(graph.Misplaced),
		UnreferencedSkillCount: len(unreferencedSkills),
		OrphanCount:            len(orphans),
		BrokenLinkCount:        len(graph.Broken),
		UnsourcedCount:         len(unsourced),
		UnkindedCount:          len(unkinded),
		ContestedCount:         len(contested),
		StalePlanCount:         len(stalePlans),
		StaleConceptCount:      len(staleConcepts),
		ColdDays:               coldDays,
		ColdMemoryScanRan:      cold.Ran,
		ColdMemoryCount:        len(cold.Findings),
		DuplicateCount:         len(duplicates),
		ParkedPlanCount:        parkedPlans,
		PlanStatusCensus:       planCensus,
	}
	if !cold.Ran {
		out.ColdMemorySkipped = fmt.Sprintf("the activity log only reaches back %d days, less than the %d-day "+
			"threshold, so every memory would look cold", cold.LogSpanDays, coldDays)
	}

	// Counts stay complete while the lists are capped, so a wiki with 400 orphans reports 400
	// without returning 400 items and burying every other category.
	out.Orphans, out.Truncated = capFindings(orphans, limit, out.Truncated)
	out.UnsourcedMemory, out.Truncated = capFindings(unsourced, limit, out.Truncated)
	out.UnkindedMemories, out.Truncated = capFindings(unkinded, limit, out.Truncated)
	out.ContestedMemories, out.Truncated = capFindings(contested, limit, out.Truncated)
	out.StalePlans, out.Truncated = capFindings(stalePlans, limit, out.Truncated)
	out.StaleConcepts, out.Truncated = capFindings(staleConcepts, limit, out.Truncated)
	out.UnreferencedSkills, out.Truncated = capFindings(unreferencedSkills, limit, out.Truncated)
	out.ColdMemories, out.Truncated = capFindings(cold.Findings, limit, out.Truncated)
	out.DuplicateMemories = duplicates
	if len(out.DuplicateMemories) > limit {
		out.DuplicateMemories = out.DuplicateMemories[:limit]
		out.Truncated = true
	}
	out.BrokenLinks = graph.Broken
	if len(out.BrokenLinks) > limit {
		out.BrokenLinks = out.BrokenLinks[:limit]
		out.Truncated = true
	}
	out.UnreadableFiles = graph.Unreadable
	if len(out.UnreadableFiles) > limit {
		out.UnreadableFiles = out.UnreadableFiles[:limit]
		out.Truncated = true
	}
	out.UnreadableFiles = unreadableForClient(srv.Storage.DataDir, out.UnreadableFiles)
	// Paths are relative by construction, so unlike an unreadable file's OS error there is nothing
	// here that could name the server's directory.
	out.MisplacedDocuments = misplacedFindings(graph, limit, srv.Storage.caseInsensitive)
	if len(graph.Misplaced) > limit {
		out.Truncated = true
	}

	return ToolResponse{
		Content:           []ToolContent{{Type: "text", Text: renderHealthReport(out)}},
		StructuredContent: out,
	}, nil
}

// misplacedFindings returns the first limit misplaced documents in graph, each with its remedy.
//
// Whether a remedy's destination is free is decided against the whole scan, not just the documents
// reported, and so are competing claims on one. articles/<slug>.md is taken by a document with that
// slug, and also by an unreadable or misplaced file stored under that name, none of which a
// suggestion may overwrite. And a destination that is free now is only safe to suggest to one
// document: two copies of an unzipped bundle each told to move to articles/bar.md would, followed
// together, leave one of them overwritten.
func misplacedFindings(graph *LinkGraph, limit int, caseInsensitive bool) []MisplacedDocumentFinding {
	key := func(name string) string {
		if caseInsensitive {
			return asciiLower(name)
		}
		return name
	}
	nonDocuments := make(map[string]string, len(graph.Unreadable)+len(graph.Misplaced))
	for _, f := range graph.Unreadable {
		nonDocuments[key(f.Path)] = f.Path
	}
	for _, d := range graph.Misplaced {
		nonDocuments[key(d.Path)] = d.Path
	}
	// A document's own file does not take the slug its filename offers: it is already there.
	occupiedFor := func(d MisplacedDocument) func(slug string) bool {
		return func(slug string) bool {
			if _, ok := graph.Meta[slug]; ok {
				return true
			}
			holder, ok := nonDocuments[key(slug+".md")]
			return ok && holder != d.Path
		}
	}

	// Destinations are slugs, which are already lowercase, so they need no key of their own.
	claims := map[string]int{}
	for _, d := range graph.Misplaced {
		if dest, ok := misplacedDestination(d, caseInsensitive, occupiedFor(d)); ok {
			claims[dest]++
		}
	}

	docs := graph.Misplaced
	if len(docs) > limit {
		docs = docs[:limit]
	}
	findings := make([]MisplacedDocumentFinding, 0, len(docs))
	for _, d := range docs {
		findings = append(findings, MisplacedDocumentFinding{
			Path:   d.Path,
			Slug:   d.Slug,
			Remedy: misplacedRemedy(d, caseInsensitive, occupiedFor(d), func(slug string) int { return claims[slug] }),
		})
	}
	return findings
}

// misplacedShape is what a remedy for a misplaced document is worked out from.
type misplacedShape struct {
	stem     string // the filename without .md
	topLevel bool   // directly in the article directory, not in a subdirectory
	target   string // the slug it can have: Slugify of its slug, empty when there is none
	// inPlace is true when the file is already stored as articles/<target>.md, so only its slug is
	// wrong and fixing it moves nothing.
	inPlace bool
}

func shapeOf(d MisplacedDocument, caseInsensitive bool) misplacedShape {
	dir, file := path.Split(d.Path)
	sh := misplacedShape{
		stem:     strings.TrimSuffix(file, ".md"),
		topLevel: dir == "",
		target:   Slugify(d.Slug),
	}
	if sh.topLevel && sh.target != "" {
		if caseInsensitive {
			sh.inPlace = asciiEqualFold(sh.stem, sh.target)
		} else {
			sh.inPlace = sh.stem == sh.target
		}
	}
	return sh
}

// misplacedDestination reports the slug whose articles/<slug>.md a remedy for d would put it at, by
// moving it, renaming it, or changing its slug and moving it, if its remedy does any of those. One
// whose destination is taken is told not to move, and one already there moves nothing.
func misplacedDestination(d MisplacedDocument, caseInsensitive bool, occupied func(slug string) bool) (string, bool) {
	sh := shapeOf(d, caseInsensitive)
	if sh.target == "" || sh.inPlace || occupied(sh.target) {
		return "", false
	}
	return sh.target, true
}

// misplacedRemedy says how to fix a misplaced document, as a sentence for the report. occupied
// reports whether articles/<slug>.md already holds some other file, claims how many misplaced
// documents' remedies would otherwise put a file there, and caseInsensitive is how the article
// directory compares names (see Storage.isCanonical).
//
// Moving a document to where its slug says it belongs is only suggested when nothing is there and
// no other misplaced document is told the same. The usual misplaced file is a copy, cp bar.md
// bar-copy.md, still declaring slug bar, and moving it to articles/bar.md would overwrite the real
// document: the data loss the misplaced rule exists to prevent. What is left then is giving it a slug
// of its own, its filename's where that is free and in slug form, or deleting it.
func misplacedRemedy(d MisplacedDocument, caseInsensitive bool, occupied func(slug string) bool, claims func(slug string) int) string {
	sh := shapeOf(d, caseInsensitive)
	target := sh.target

	// ownSlug is a slug the document could take without moving: its filename, where that is one.
	ownSlug := ""
	if sh.topLevel {
		candidate := sh.stem
		if caseInsensitive {
			candidate = asciiLower(sh.stem)
		}
		if candidate != "" && candidate != target && isSlugForm(candidate) && !occupied(candidate) {
			ownSlug = candidate
		}
	}
	const deleteIt = "or delete it if it's a copy."

	var fix string
	switch {
	case target == "":
		if ownSlug != "" {
			return fmt.Sprintf("Change its slug to %q to match its filename, %s", ownSlug, deleteIt)
		}
		return "Give it an unused slug of lowercase letters, digits, and hyphens and store it as articles/<slug>.md, " + deleteIt
	case sh.inPlace:
		// Only the slug is out of slug form; the file is already where the corrected one lives.
		fix = fmt.Sprintf("Change its slug to %q to match its filename", target)
	case occupied(target):
		if ownSlug != "" {
			return fmt.Sprintf("articles/%s.md already exists, so do not move it there. Change its slug to %q to match its filename, %s", target, ownSlug, deleteIt)
		}
		return fmt.Sprintf("articles/%s.md already exists, so do not move it there. Delete it if it's a copy, or give it an unused slug and store it as articles/<slug>.md.", target)
	case claims(target) > 1:
		contested := fmt.Sprintf("%d misplaced documents claim articles/%s.md: move at most one there, and delete or re-slug the others", claims(target), target)
		if ownSlug != "" {
			contested += fmt.Sprintf(" (this one could take the slug %q to match its filename)", ownSlug)
		}
		return contested + "."
	case target != d.Slug:
		fix = fmt.Sprintf("Change its slug to %q and move it to articles/%s.md", target, target)
	case sh.topLevel && asciiEqualFold(sh.stem, target):
		// Reachable only where case matters: elsewhere this file would be in place.
		fix = fmt.Sprintf("Rename it to articles/%s.md, in lowercase", target)
	default:
		fix = fmt.Sprintf("Move it to articles/%s.md", target)
	}
	if ownSlug != "" {
		fix += fmt.Sprintf(" (or change its slug to %q to match its filename)", ownSlug)
	}
	return fix + ", " + deleteIt
}

// isFinished reports whether a plan's status says its life is over.
func isFinished(status string) bool {
	return hasAnyTag([]string{status}, finishedStatusTags)
}

// isParked reports whether a plan has been deliberately set aside. Parked is not finished — the
// work may still happen — but it is a decision, and re-reporting a decision is noise.
func isParked(status string) bool {
	return hasAnyTag([]string{status}, parkedStatusTags)
}

func hasAnyTag(tags []string, candidates []string) bool {
	for _, tag := range tags {
		for _, candidate := range candidates {
			if strings.EqualFold(tag, candidate) {
				return true
			}
		}
	}
	return false
}

// inFlightStatus reports the first in-flight status tag a document carries, so the report can name
// it. Absence is not exoneration — see the stale-plan check.
func inFlightStatus(status string) (string, bool) {
	for _, inFlight := range inFlightStatusTags {
		if strings.EqualFold(status, inFlight) {
			return strings.ToLower(status), true
		}
	}
	return "", false
}

// capFindings truncates a category to the limit, reporting whether anything was dropped.
func capFindings(findings []HealthFinding, limit int, truncated bool) ([]HealthFinding, bool) {
	if len(findings) > limit {
		return findings[:limit], true
	}
	return findings, truncated
}

// unreadableForClient returns a copy of a scan's unreadable files whose errors no longer reveal
// where the wiki lives on the server. An OS error names the path it failed on, and that comes out
// relative to the data directory, as in every other tool's errors, so a file that cannot be read
// says the same here as from read_article. Each entry's path stays relative to the article
// directory, as documented.
func unreadableForClient(dataDir string, files []UnreadableFile) []UnreadableFile {
	hider := newDataDirHider(dataDir)
	out := make([]UnreadableFile, 0, len(files))
	for _, f := range files {
		out = append(out, UnreadableFile{Path: f.Path, Error: hider.hide(f.Error)})
	}
	return out
}

// renderHealthReport writes the prose from the same value the structured payload carries, so the
// two halves cannot disagree. Clean categories are still listed: "0 broken links" is information,
// and omitting it makes an agent wonder whether the check ran.
func renderHealthReport(out HealthOutput) string {
	var b strings.Builder
	fmt.Fprintf(&b, "NexWiki Health Report (%d documents scanned)\n\n", out.TotalDocuments)
	fmt.Fprintf(&b, "- Unreadable article files and folders (skipped by every check): %d\n", out.UnreadableFileCount)
	fmt.Fprintf(&b, "- Misplaced documents (not at articles/<slug>.md, skipped by every check): %d\n", out.MisplacedDocumentCount)
	fmt.Fprintf(&b, "- Orphan pages: %d\n", out.OrphanCount)
	fmt.Fprintf(&b, "- Broken internal links: %d\n", out.BrokenLinkCount)
	fmt.Fprintf(&b, "- Memories with no source: %d\n", out.UnsourcedCount)
	fmt.Fprintf(&b, "- Memories with no kind: %d\n", out.UnkindedCount)
	fmt.Fprintf(&b, "- Contested memories: %d\n", out.ContestedCount)
	fmt.Fprintf(&b, "- Stale plans (unfinished, untouched for %d+ days): %d\n", out.StaleDays, out.StalePlanCount)
	fmt.Fprintf(&b, "- Stale concepts (past freshness expiration): %d\n", out.StaleConceptCount)
	fmt.Fprintf(&b, "- Skills nothing references: %d\n", out.UnreferencedSkillCount)
	if out.ColdMemoryScanRan {
		fmt.Fprintf(&b, "- Cold memories (not read or edited in %d+ days): %d\n", out.ColdDays, out.ColdMemoryCount)
	} else {
		fmt.Fprintf(&b, "- Cold memories: not checked — %s\n", out.ColdMemorySkipped)
	}
	fmt.Fprintf(&b, "- Possible duplicate memories: %d\n", out.DuplicateCount)
	if out.ParkedPlanCount > 0 {
		// Reported so the number is not mistaken for plans that vanished from the stale list by
		// accident. Parked is a decision, so there is nothing to act on and no list to print.
		fmt.Fprintf(&b, "- Parked plans (deliberately set aside, not reported as stale): %d\n", out.ParkedPlanCount)
	}
	if len(out.PlanStatusCensus) > 0 {
		// Rendered in the canonical lifecycle order rather than map order, so the report reads
		// as a pipeline: draft → implementing → blocked → completed → superseded → parked →
		// evergreen → archived.
		b.WriteString("- Plan status census:")
		for _, s := range PlanStatusTags {
			if n := out.PlanStatusCensus[s]; n > 0 {
				fmt.Fprintf(&b, " %s=%d", s, n)
			}
		}
		for _, s := range []string{"(none)"} {
			if n := out.PlanStatusCensus[s]; n > 0 {
				fmt.Fprintf(&b, " %s=%d", s, n)
			}
		}
		b.WriteString("\n")
	}

	needsAttention := out.UnreadableFileCount + out.MisplacedDocumentCount + out.OrphanCount + out.BrokenLinkCount + out.UnsourcedCount + out.UnkindedCount + out.ContestedCount +
		out.StalePlanCount + out.StaleConceptCount + out.ColdMemoryCount + out.DuplicateCount + out.UnreferencedSkillCount
	if needsAttention == 0 {
		b.WriteString("\nNothing needs attention — the wiki is healthy. 🎉\n")
		return b.String()
	}

	writeFindings := func(label string, count int, findings []HealthFinding) {
		if count == 0 {
			return
		}
		fmt.Fprintf(&b, "\n== %s (%d) ==\n", label, count)
		for _, f := range findings {
			fmt.Fprintf(&b, "- %s (%s) — %s\n", f.Title, f.Slug, f.Detail)
		}
		if len(findings) < count {
			fmt.Fprintf(&b, "  ... and %d more; raise 'limit' to see them.\n", count-len(findings))
		}
	}

	// Unreadable files and misplaced documents come first: every other category is blind to them,
	// so fixing one can change what the rest of the report says.
	if out.UnreadableFileCount > 0 {
		fmt.Fprintf(&b, "\n== Unreadable article files and folders (%d) ==\n", out.UnreadableFileCount)
		for _, f := range out.UnreadableFiles {
			remedy := "Fix its front matter or file permissions, or delete the file."
			if strings.HasSuffix(f.Path, "/") {
				// A directory that could not be listed has no front matter, and deleting it would take
				// the articles inside with it. Permissions are the usual cause but not the only one: an
				// I/O error or a stale or disconnected network mount fails the listing too.
				remedy = "Fix what keeps the directory from being listed (usually its permissions) so the articles in it are scanned."
			}
			// YAML errors can span lines; flattened so each file stays one list item. A closing period
			// is dropped because the sentence adds its own, and Windows ends OS errors with one
			// ("Access is denied.").
			fmt.Fprintf(&b, "- %s — %s. %s\n",
				f.Path, strings.TrimRight(strings.Join(strings.Fields(f.Error), " "), ". "), remedy)
		}
		if len(out.UnreadableFiles) < out.UnreadableFileCount {
			fmt.Fprintf(&b, "  ... and %d more; raise 'limit' to see them.\n", out.UnreadableFileCount-len(out.UnreadableFiles))
		}
	}

	if out.MisplacedDocumentCount > 0 {
		fmt.Fprintf(&b, "\n== Misplaced documents (%d) ==\n", out.MisplacedDocumentCount)
		for _, d := range out.MisplacedDocuments {
			fmt.Fprintf(&b, "- %s (slug %q) — not a document, so no listing, search, lookup, or check includes it. %s\n",
				d.Path, d.Slug, d.Remedy)
		}
		if len(out.MisplacedDocuments) < out.MisplacedDocumentCount {
			fmt.Fprintf(&b, "  ... and %d more; raise 'limit' to see them.\n", out.MisplacedDocumentCount-len(out.MisplacedDocuments))
		}
	}

	writeFindings("Orphan pages", out.OrphanCount, out.Orphans)
	writeFindings("Skills nothing references", out.UnreferencedSkillCount, out.UnreferencedSkills)

	if out.BrokenLinkCount > 0 {
		fmt.Fprintf(&b, "\n== Broken internal links (%d) ==\n", out.BrokenLinkCount)
		for _, bl := range out.BrokenLinks {
			// Display() prints the link in the syntax it was written in, so the agent searches the
			// file for text that is actually there.
			fmt.Fprintf(&b, "- '%s' in '%s' — target '%s' does not exist. Create it or fix the link.\n",
				bl.Display(), bl.FromSlug, bl.TargetSlug)
		}
		if len(out.BrokenLinks) < out.BrokenLinkCount {
			fmt.Fprintf(&b, "  ... and %d more; raise 'limit' to see them.\n", out.BrokenLinkCount-len(out.BrokenLinks))
		}
	}

	writeFindings("Memories with no source", out.UnsourcedCount, out.UnsourcedMemory)
	writeFindings("Memories with no kind", out.UnkindedCount, out.UnkindedMemories)
	writeFindings("Contested memories", out.ContestedCount, out.ContestedMemories)
	writeFindings("Stale plans", out.StalePlanCount, out.StalePlans)
	writeFindings("Stale concepts", out.StaleConceptCount, out.StaleConcepts)
	writeFindings("Cold memories", out.ColdMemoryCount, out.ColdMemories)

	if out.DuplicateCount > 0 {
		fmt.Fprintf(&b, "\n== Possible duplicate memories (%d) ==\n", out.DuplicateCount)
		for _, d := range out.DuplicateMemories {
			scope := d.Scope
			if scope == "" {
				scope = "unscoped"
			}
			fmt.Fprintf(&b, "- '%s' (%s) and '%s' — same scope '%s'. %s\n",
				d.Title, d.Slug, d.OtherSlug, scope, d.Detail)
		}
		if len(out.DuplicateMemories) < out.DuplicateCount {
			fmt.Fprintf(&b, "  ... and %d more; raise 'limit' to see them.\n", out.DuplicateCount-len(out.DuplicateMemories))
		}
	}

	return b.String()
}

// liveReferencedSlugs returns every slug reachable from a *non-archived* document, by either an
// internal link or an in-code slug mention.
//
// References from archived documents deliberately do not count. Archiving is the user saying "this
// is done", and a skill whose only mention lives in a retired document is as unreachable as one
// with no mention at all. This is not a hypothetical refinement: nexwiki-agent-core-guidelines was
// the only document naming enhanced-memory-decision-making-skill, and the skill became genuinely
// dead the moment that document was archived. Without this rule the check would have stayed quiet.
func liveReferencedSlugs(graph *LinkGraph) map[string]bool {
	live := map[string]bool{}

	for from, refs := range graph.Outbound {
		source := graph.Meta[from]
		if IsArchived(&source) {
			continue
		}
		for _, ref := range refs {
			if ref.Slug != from {
				live[ref.Slug] = true
			}
		}
	}

	for from, mentions := range graph.Mentions {
		source := graph.Meta[from]
		if IsArchived(&source) {
			continue
		}
		for _, mentioned := range mentions {
			if mentioned != from {
				live[mentioned] = true
			}
		}
	}

	return live
}
