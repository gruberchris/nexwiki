package server

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// This file holds story 02's MCP surface for the story 01 candidate flow: proposing a
// skill candidate and promoting one. Two tools, deliberately, not five: listing and
// fetching candidates stay operator-side (story 04 runner tooling) so the new surface
// — and its docs burden — stays minimal. Both tools run through the story 02 scope
// gate in executeToolCall*: propose needs candidates.propose (the proposer phase),
// promote needs skills.promote + audit.write (no phase holds either — promotion is an
// operator action until the story 04 runner).

var proposeSkillCandidateTool = toolDef{
	Schema: map[string]interface{}{
		"name":        "propose_skill_candidate",
		"description": "Propose a skill evolution candidate: records a unified diff and/or a full proposed body against a parent skill, with motivating wiki pattern slugs. The live skill is never written — promotion is a separate, operator-only step.",
		"inputSchema": map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"parent_slug": map[string]interface{}{
					"type":        "string",
					"description": "The URL-safe slug of the parent skill this candidate evolves.",
				},
				"proposed_body": map[string]interface{}{
					"type":        "string",
					"description": "Full proposed replacement body for the skill (required to ever promote; a diff alone cannot be promoted).",
				},
				"diff": map[string]interface{}{
					"type":        "string",
					"description": "Optional unified diff of the proposed change, for review.",
				},
				"pattern_slugs": map[string]interface{}{
					"type": "array",
					"items": map[string]interface{}{
						"type": "string",
					},
					"description": "Optional wiki pattern slugs motivating this evolution.",
				},
				"proposer": map[string]interface{}{
					"type":        "string",
					"description": "Optional proposer identity recorded on the candidate. Self-reported provenance, not authorization — the scope gate decides who may propose.",
				},
			},
			"required": []string{"parent_slug"},
		},
	},
	Handler:  (*Server).toolProposeSkillCandidate,
	Behavior: toolBehavior{Title: "Propose Skill Candidate", Destructive: false, Idempotent: false},
}

func (srv *Server) toolProposeSkillCandidate(args json.RawMessage) (interface{}, *JSONRPCError) {
	type ProposeArgs struct {
		ParentSlug   string   `json:"parent_slug"`
		ProposedBody string   `json:"proposed_body"`
		Diff         string   `json:"diff"`
		PatternSlugs []string `json:"pattern_slugs"`
		Proposer     string   `json:"proposer"`
	}
	var pArgs ProposeArgs
	if e := decodeToolArgs(args, &pArgs); e != nil {
		return nil, e
	}
	if strings.TrimSpace(pArgs.ParentSlug) == "" {
		return nil, &JSONRPCError{Code: -32602, Message: "Missing or invalid arguments. 'parent_slug' is required."}
	}
	proposer := strings.TrimSpace(pArgs.Proposer)
	if proposer == "" {
		proposer = DefaultAgentName
	}
	c, err := srv.Storage.CreateSkillCandidate(pArgs.ParentSlug, pArgs.ProposedBody, pArgs.Diff, pArgs.PatternSlugs, proposer)
	if err != nil {
		return ToolResponse{IsError: true, Content: []ToolContent{{Type: "text", Text: fmt.Sprintf("Error proposing skill candidate: %v", err)}}}, nil
	}
	respText := fmt.Sprintf("Success! Skill candidate '%s' proposed for skill '%s' (parent v%d, proposed by %s).\nCreated At: %s\nStatus: %s\n",
		c.ID, c.ParentSlug, c.ParentVersion, c.Proposer, c.CreatedAt.Format(time.RFC3339), c.Status)
	return ToolResponse{Content: []ToolContent{{Type: "text", Text: respText}}}, nil
}

var promoteSkillCandidateTool = toolDef{
	Schema: map[string]interface{}{
		"name":        "promote_skill_candidate",
		"description": "Promote a pending skill candidate: writes its proposed body as a new version of the parent skill and records the linkage. Operator-only — no evolution phase role may call it.",
		"inputSchema": map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"id": map[string]interface{}{
					"type":        "string",
					"description": "The candidate ID to promote (returned by propose_skill_candidate).",
				},
			},
			"required": []string{"id"},
		},
	},
	Handler:  (*Server).toolPromoteSkillCandidate,
	Behavior: toolBehavior{Title: "Promote Skill Candidate", Destructive: true, Idempotent: false},
}

func (srv *Server) toolPromoteSkillCandidate(args json.RawMessage) (interface{}, *JSONRPCError) {
	type PromoteArgs struct {
		ID string `json:"id"`
	}
	var pArgs PromoteArgs
	if e := decodeToolArgs(args, &pArgs); e != nil {
		return nil, e
	}
	if strings.TrimSpace(pArgs.ID) == "" {
		return nil, &JSONRPCError{Code: -32602, Message: "Missing or invalid arguments. 'id' is required."}
	}
	art, err := srv.Storage.PromoteSkillCandidate(pArgs.ID)
	if err != nil {
		return ToolResponse{IsError: true, Content: []ToolContent{{Type: "text", Text: fmt.Sprintf("Error promoting skill candidate: %v", err)}}}, nil
	}
	respText := fmt.Sprintf("Success! Skill candidate '%s' promoted.\nSkill: %s\nNew Version: %d\nLast Edited: %s\n",
		strings.TrimSpace(pArgs.ID), art.Slug, art.Version, art.Timestamp.Format(time.RFC3339))
	return ToolResponse{Content: []ToolContent{{Type: "text", Text: respText}}}, nil
}
