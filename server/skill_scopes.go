package server

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// This file holds story 02 of the wikiskill evolution support plan: per-phase tool
// scopes (least-privilege roles) enforced at the MCP layer.
//
// Three harness phases run with different needs, so each gets only the tools its job
// requires:
//
//   - inference: reads locked skills and appends raw observations. It must never see
//     the wiki layer (pattern/log/impact reads), publish or promote skills, or write
//     audit entries.
//   - maintainer: curates raw memories and wiki articles, and reads skills. It must
//     never publish or promote skills, or write audit entries.
//   - proposer: reads the wiki layer and proposes skill candidates. It must never
//     publish or promote skills, write audit entries, or edit skills directly (the
//     story 01 lock already refuses direct edits to locked skills; this role rule
//     covers even unlocked ones).
//
// The role is server-side configuration (Server.WikiskillRole, from -wikiskill-role /
// NEXWIKI_WIKISKILL_ROLE), exactly like AgentName is. There is deliberately NO
// client-supplied role field: a caller that could declare its own role could escalate
// to it, so self-declared identity (MCP clientInfo, the candidate `proposer` string)
// stays what it has always been — a provenance hint, never an authorization claim.
//
// TRUST ASSUMPTION: the harness runs one process per phase with the role fixed at
// startup. Every phase of one wiki must therefore run in its own process (or the
// operator runs with no role set, which is unrestricted).
//
// Story 04 binds the headless job lifecycle to per-harness tokens instead of the
// process role: checkWikiskillScope consults validJobTokenForArgs first, so a call
// carrying the job's own token authorizes that job's lifecycle tools (claim,
// heartbeat, artifact, complete) regardless of the process role. Creation mints
// the token and stays on the process path. Anything without a valid token falls
// through to the scope map below, where jobs.manage is granted to no phase.

// WikiSkill evolution roles.
const (
	WikiskillRoleInference  = "inference"
	WikiskillRoleMaintainer = "maintainer"
	WikiskillRoleProposer   = "proposer"
)

// WikiskillRoleEnv is the environment variable fixing the process-wide evolution role.
// Env takes precedence over the flag, matching NEXWIKI_NAME / NEXWIKI_AGENT_NAME.
const WikiskillRoleEnv = "NEXWIKI_WIKISKILL_ROLE"

// Least-privilege scopes. Each MCP tool requires one or more; each role grants a set.
// Tools the brief never assigns (plans, direct audit writes) are granted to no role.
const (
	ScopeWikiRead         = "wiki.read"
	ScopeWikiWrite        = "wiki.write"
	ScopeSkillsRead       = "skills.read"
	ScopeSkillsPublish    = "skills.publish" // create/edit/delete a skill outside candidates
	ScopeSkillsPromote    = "skills.promote" // promote_skill_candidate (operator-only)
	ScopeCandidatePropose = "candidates.propose"
	ScopeMemoryRead       = "memory.read"
	ScopeMemoryAppend     = "memory.append"
	ScopeMemoryWrite      = "memory.write"
	ScopePlansRead        = "plans.read"
	ScopePlansWrite       = "plans.write"
	ScopeAuditRead        = "audit.read"  // get_recent_activity (the log read path)
	ScopeAuditWrite       = "audit.write" // skill-impact/log audit writes (story 03 owns the writes; the scope is enforced here first)
	ScopeSystemRead       = "system.read" // get_status_tags: lifecycle vocabulary, harmless to every phase
	ScopeJobsManage       = "jobs.manage" // headless evolution job lifecycle (story 04): no phase holds it
)

// wikiskillRoleGrants is the least-privilege matrix: exactly what each phase needs.
var wikiskillRoleGrants = map[string]map[string]bool{
	WikiskillRoleInference: {
		ScopeSkillsRead: true, ScopeMemoryAppend: true, ScopeSystemRead: true,
	},
	WikiskillRoleMaintainer: {
		ScopeSkillsRead: true,
		ScopeMemoryRead: true, ScopeMemoryAppend: true, ScopeMemoryWrite: true,
		ScopeWikiRead: true, ScopeWikiWrite: true,
		ScopeAuditRead: true, ScopeSystemRead: true,
	},
	WikiskillRoleProposer: {
		ScopeSkillsRead: true, ScopeWikiRead: true,
		ScopeCandidatePropose: true,
		ScopeAuditRead:        true, ScopeSystemRead: true,
	},
}

// wikiskillRoleSummaries renders what a role may do, for denial messages.
func wikiskillRoleSummaries(role string) string {
	switch role {
	case WikiskillRoleInference:
		return "inference may read skills and append raw memories only"
	case WikiskillRoleMaintainer:
		return "maintainer may read/write raw memories and wiki articles, and read skills"
	case WikiskillRoleProposer:
		return "proposer may read wiki articles and skills, and propose skill candidates"
	default:
		return "unknown role"
	}
}

// NormalizeWikiskillRole folds a configured role to its canonical form. Empty (and
// anything unrecognized) means unrestricted: the server behaves exactly as before
// story 02, so every existing deployment and test keeps working.
func NormalizeWikiskillRole(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case WikiskillRoleInference:
		return WikiskillRoleInference
	case WikiskillRoleMaintainer:
		return WikiskillRoleMaintainer
	case WikiskillRoleProposer:
		return WikiskillRoleProposer
	default:
		return ""
	}
}

// ResolveWikiskillRole returns the configured evolution role, with the environment
// variable taking precedence over the flag — matching NEXWIKI_NAME/NEXWIKI_AGENT_NAME.
func ResolveWikiskillRole(flagValue string) string {
	if env := strings.TrimSpace(os.Getenv(WikiskillRoleEnv)); env != "" {
		return NormalizeWikiskillRole(env)
	}
	return NormalizeWikiskillRole(flagValue)
}

// SkillScopeError reports a refused tool call under least-privilege roles. Like
// SkillLockedError it names what the agent needs to know: the role it ran as, the
// scope it is missing, and the tool that was refused.
type SkillScopeError struct {
	Role    string
	Tool    string
	Missing []string
}

func (e *SkillScopeError) Error() string {
	missing := strings.Join(e.Missing, "', '")
	return fmt.Sprintf("access denied: role '%s' lacks scope '%s' for tool '%s' (wikiskill least-privilege denial: %s)",
		e.Role, missing, e.Tool, wikiskillRoleSummaries(e.Role))
}

// roleForRequest returns the evolution role governing this request. Process-wide today;
// story 04 plugs per-harness token validation in here (see the file header).
func (srv *Server) roleForRequest() string {
	if srv == nil {
		return ""
	}
	return NormalizeWikiskillRole(srv.WikiskillRole)
}

// slugOf extracts a best-effort identifier from tool arguments for denial audit lines.
func slugOf(args json.RawMessage) string {
	var probe struct {
		Slug       string `json:"slug"`
		ParentSlug string `json:"parent_slug"`
		ID         string `json:"id"`
		Title      string `json:"title"`
	}
	if err := json.Unmarshal(args, &probe); err != nil {
		return ""
	}
	for _, s := range []string{probe.Slug, probe.ParentSlug, probe.ID} {
		if strings.TrimSpace(s) != "" {
			return strings.TrimSpace(s)
		}
	}
	if strings.TrimSpace(probe.Title) != "" {
		return Slugify(probe.Title)
	}
	return ""
}

// requiredWikiskillScopes maps a tool call to the scopes it needs. Generic wiki tools
// that accept any document type resolve against the target's actual type, so a read of
// a skill needs skills.read while a read of a wiki article needs wiki.read. An
// unresolvable target (missing slug, unknown article) resolves to nil: the handler's
// own not-found/validation error is the honest answer there, not a scope denial.
func (srv *Server) requiredWikiskillScopes(toolName string, args json.RawMessage) []string {
	switch toolName {
	case "search_wiki", "list_articles", "get_article_history", "get_backlinks",
		"get_context_overview", "get_wiki_statistics", "wiki_health", "export_okf_bundle":
		// The wiki layer, whole-corpus reads included: search spans every type and the
		// overview/stats/health/export all expose wiki content, so the wiki.read scope
		// governs them even when skills ride along. (Facet-aware narrowing — e.g.
		// letting inference search type:["skills"] — is a future seam, not this story.)
		return []string{ScopeWikiRead}
	case "read_article":
		return srv.scopesForSlug(args, false)
	case "create_wiki_article":
		return []string{ScopeWikiWrite}
	case "edit_wiki_article", "update_article_tags", "delete_wiki_article", "revert_article_version":
		return srv.scopesForSlug(args, true)
	case "create_agent_memory", "edit_agent_memory", "delete_agent_memory":
		return []string{ScopeMemoryWrite}
	case "append_agent_memory":
		return []string{ScopeMemoryAppend}
	case "list_agent_memories":
		return []string{ScopeMemoryRead}
	case "create_agent_plan", "append_agent_plan", "edit_agent_plan":
		return []string{ScopePlansWrite}
	case "list_agent_plans":
		return []string{ScopePlansRead}
	case "create_agent_skill", "edit_agent_skill":
		// Publishing a skill outside the candidate flow: no evolution phase may do it.
		return []string{ScopeSkillsPublish}
	case "list_agent_skills", "get_skill_trained_state":
		return []string{ScopeSkillsRead}
	case "propose_skill_candidate":
		return []string{ScopeCandidatePropose}
	case "promote_skill_candidate":
		// Promotion writes a new live skill version AND the skill-impact/log audit
		// trail (story 03 owns those writes), so it needs both scopes. No phase
		// holds either: promotion is an operator action until the story 04 runner.
		return []string{ScopeSkillsPromote, ScopeAuditWrite}
	case "get_recent_activity":
		return []string{ScopeAuditRead}
	case "create_evolution_job", "claim_evolution_job", "heartbeat_evolution_job",
		"upload_job_artifact", "upload_evolution_eval_set", "complete_evolution_job":
		// Headless job lifecycle (stories 04–05): operator/harness only. A call carrying
		// the job's own per-harness token bypasses the process role via
		// validJobTokenForArgs in checkWikiskillScope; everything else needs a
		// scope no phase holds, so agent roles are denied like promotion.
		return []string{ScopeJobsManage}
	case "get_status_tags":
		return []string{ScopeSystemRead}
	case "import_okf_bundle":
		// An import can create or overwrite documents of any class, including
		// skills, so it needs the wiki write scope and the skill publish scope.
		return []string{ScopeWikiWrite, ScopeSkillsPublish}
	default:
		// Unknown tools (and any future tool this map predates) fall through to the
		// handler, which reports them. Denying by default would turn a typo into a
		// misleading scope error instead of "Tool not found".
		return nil
	}
}

// scopesForSlug resolves a document-targeted generic tool (read_article, or the wiki
// write family) to the scope its target's class demands. Returns nil when the target
// cannot be resolved, letting the handler answer.
func (srv *Server) scopesForSlug(args json.RawMessage, write bool) []string {
	var probe struct {
		Slug string `json:"slug"`
	}
	if err := json.Unmarshal(args, &probe); err != nil || strings.TrimSpace(probe.Slug) == "" {
		return nil
	}
	if srv == nil || srv.Storage == nil {
		return nil
	}
	art, err := srv.Storage.GetArticle(probe.Slug)
	if err != nil {
		return nil
	}
	switch art.Type {
	case ContentTypeSkill:
		if write {
			// A direct write to a skill outside the candidate flow — publish, not wiki.
			return []string{ScopeSkillsPublish}
		}
		return []string{ScopeSkillsRead}
	case ContentTypeMemory:
		if write {
			// Unreachable in practice: the wiki write handlers refuse memories with
			// their own "use the memory tools" error, which is more helpful. The
			// scope just has to be one the phase plausibly lacks or holds honestly.
			return []string{ScopeWikiWrite}
		}
		return []string{ScopeMemoryRead}
	case ContentTypePlan:
		if write {
			return []string{ScopePlansWrite}
		}
		return []string{ScopePlansRead}
	default:
		if write {
			return []string{ScopeWikiWrite}
		}
		return []string{ScopeWikiRead}
	}
}

// checkWikiskillScope enforces the process role against one tool call. It returns nil
// when the call may proceed: a valid per-harness job token on a job lifecycle tool
// (story 04 — the token authorizes its own job regardless of process role), no role
// configured (unrestricted, pre-story-02 behavior), an unresolvable scope mapping,
// or every required scope granted. Otherwise it returns a *SkillScopeError naming
// the role and the missing scope(s).
func (srv *Server) checkWikiskillScope(toolName string, args json.RawMessage) *SkillScopeError {
	if srv.validJobTokenForArgs(toolName, args) {
		return nil
	}
	role := srv.roleForRequest()
	if role == "" {
		return nil
	}
	required := srv.requiredWikiskillScopes(toolName, args)
	if len(required) == 0 {
		return nil
	}
	grants := wikiskillRoleGrants[role]
	var missing []string
	for _, scope := range required {
		if !grants[scope] {
			missing = append(missing, scope)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	return &SkillScopeError{Role: role, Tool: toolName, Missing: missing}
}

// auditScopeDenial records a least-privilege refusal in the activity log, the same
// durable trail every other tool event lands in. Denials are ToolResponse errors, and
// the success-only logging hook skips those by design (a refused write must not look
// like a completed one), so the gate audits its own refusals here instead.
func (srv *Server) auditScopeDenial(toolName string, args json.RawMessage, agent string, scopeErr *SkillScopeError) {
	if srv == nil || srv.EventBus == nil || scopeErr == nil {
		return
	}
	if agent == "" {
		agent = DefaultAgentName
	}
	srv.EventBus.PublishActivity("mcp", DenyAction, toolName, slugOf(args), "", agent)
}

// DenyAction is the activity-log action recorded for a refused call — a scope denial
// here, and any future policy refusal that must read as "attempted, not done".
const DenyAction = "deny"
