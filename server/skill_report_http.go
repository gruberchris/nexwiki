package server

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
)

// This file holds the story-09 half of the evolution wizard's REST seam (Phase
// D3): the read endpoints the Review and Report steps render from, and the
// audited Rollback control. The shapes follow the story-08 seam
// (skill_wizard_http.go) exactly — read-mostly, under /api, origin-gated like
// the rest of the REST API, never carrying a per-harness job token: the
// browser is the operator, not the harness.
//
//   GET  /api/skills/{slug}/audit       — the skill's gate-decision trail
//                                         (story 03 records) for the Review
//                                         step's audit table and final R_best.
//   GET  /api/evolution/jobs/{id}/report — the run's Trained Skill Result
//                                         report pointer plus the skill's
//                                         live derived trained state
//                                         (staleness, reusing
//                                         deriveSkillTrainedState).
//   POST /api/skills/{slug}/rollback    — the human rollback control (the
//                                         story-06 storage path), audited in
//                                         the activity log like every other
//                                         loop control.
//
// Like every REST surface this file changes, the routes are registered in
// main.go (audit and rollback next to the /api/skills registry, the report
// route in the evolution wizard block) and documented in the "Review + result
// report (story 09)" subsection of the Evolution Wizard REST API section in
// docs/aiagent_skills.md. MCP tool count is unchanged (39): the wizard stays
// REST, per the docs-integrity rule.

// SkillAuditTrailView is the Review step's audit payload: every gate decision
// recorded for one skill (oldest first), with the derived running best.
type SkillAuditTrailView struct {
	Slug    string             `json:"slug"`
	RBest   float64            `json:"r_best"`
	Records []SkillAuditRecord `json:"records"`
}

// HandleGetSkillAuditTrail serves one skill's harness-owned gate trail. A
// missing skill and a slug that is not a skill both read 404.
func (srv *Server) HandleGetSkillAuditTrail(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	if slug == "" {
		writeError(w, http.StatusBadRequest, "skill slug is required")
		return
	}
	if _, err := srv.Storage.GetSkillTrainedState(slug); err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	records, err := srv.Storage.ListSkillAuditRecords()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	filtered := make([]SkillAuditRecord, 0, len(records))
	for _, rec := range records {
		if rec.SkillSlug == slug {
			filtered = append(filtered, rec)
		}
	}
	best, err := srv.Storage.currentRBest(slug)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, SkillAuditTrailView{Slug: slug, RBest: best, Records: filtered})
}

// HandleGetSkillResultReport serves the story-09 report view for one run: the
// report article pointer (omitted until the run ends and the report exists),
// the run's terminal shape, and the skill's live derived trained state so the
// wizard's Review and Report steps show staleness from server truth.
func (srv *Server) HandleGetSkillResultReport(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "evolution job id is required")
		return
	}
	job, err := srv.Storage.GetEvolutionJob(id)
	if err != nil {
		writeError(w, wizardJobErrorStatus(err), err.Error())
		return
	}
	view, err := srv.Storage.SkillResultReportViewFor(job)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, view)
}

// skillRollbackRequest is the body of a rollback control call.
type skillRollbackRequest struct {
	Reason string `json:"reason"`
}

// decodeSkillRollback reads the rollback body; an empty body decodes cleanly.
func decodeSkillRollback(r *http.Request) (skillRollbackRequest, error) {
	var req skillRollbackRequest
	err := json.NewDecoder(r.Body).Decode(&req)
	if errors.Is(err, io.EOF) {
		return req, nil
	}
	return req, err
}

// skillRollbackErrorStatus maps a rollback error onto a REST status code: an
// unknown skill or non-skill is 404, a state conflict (no marker, live run) is
// 409, and everything else is a server fault.
func skillRollbackErrorStatus(err error) int {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "not found"),
		strings.Contains(msg, "not a Custom AI Skill"):
		return http.StatusNotFound
	case strings.Contains(msg, "no trained marker"),
		strings.Contains(msg, "cannot roll back"),
		strings.Contains(msg, "evolution job"):
		return http.StatusConflict
	default:
		return http.StatusInternalServerError
	}
}

// HandleRollbackTrainedState is the human rollback control over the trained
// marker (story 09, Review step): the story-06 rollback path wired to HTTP.
// The skill body is restored to its pre-training version and the marker is
// recorded as revoked with the caller's reason; the audit trail and the run's
// report survive. Audited in the activity log like every other operator
// action, and the refreshed derived state is returned so the wizard re-renders
// staleness without a second request.
func (srv *Server) HandleRollbackTrainedState(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	if slug == "" {
		writeError(w, http.StatusBadRequest, "skill slug is required")
		return
	}
	req, derr := decodeSkillRollback(r)
	if derr != nil {
		writeDecodeError(w, derr)
		return
	}
	art, err := srv.Storage.RollbackTrainedSkill(slug, req.Reason)
	if err != nil {
		writeError(w, skillRollbackErrorStatus(err), err.Error())
		return
	}
	if srv.EventBus != nil {
		srv.EventBus.PublishActivity("api", "rollback", "", art.Slug, art.Title, DefaultAgentName)
	}
	state, err := srv.Storage.GetSkillTrainedState(art.Slug)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, state)
}
