package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// This file holds story 05's MCP surface for validated training-data intake:
// one tool covering the format gate in skill_eval.go. Uploading eval data is
// operator/harness work — it runs through the story-02 scope gate in
// executeToolCall* needing jobs.manage (granted to no phase), with the same
// per-harness token bypass as every other job lifecycle tool: a call carrying
// the job's own token authorizes that job regardless of the process-wide
// phase role, and anything else without the scope is refused and audited as a
// deny event. Reading stored splits stays operator-side
// (ReadEvolutionEvalSet) so the new surface — and its docs burden — stays one
// tool per the docs-integrity rule.

var uploadEvolutionEvalSetTool = toolDef{
	Schema: map[string]interface{}{
		"name":        "upload_evolution_eval_set",
		"description": "Upload a validated training-data file for an evolution job: CSV, JSONL, JSON, text, or logs shaped {input, expected [, scorer]}. The format gate enforces minimum split counts, dedupes identical cases, refuses train/val input overlap and secrets/PII, then stores deterministic splits with the S0 baseline, eval hash, 5-sample dry run, and a static cost-estimate stub. Malformed, too-small, duplicated, leaked, or secret-bearing sets are refused with per-issue errors and nothing is stored.",
		"inputSchema": map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"id": map[string]interface{}{
					"type":        "string",
					"description": "The job ID to attach the eval set to.",
				},
				"job_token": map[string]interface{}{
					"type":        "string",
					"description": "The per-harness token minted at creation, scoped to this job.",
				},
				"filename": map[string]interface{}{
					"type":        "string",
					"description": "Bare filename declaring the format: .csv, .json, .jsonl, .ndjson, .txt, .log, or .md.",
				},
				"content": map[string]interface{}{
					"type":        "string",
					"description": "Eval file content. JSON may carry user-supplied splits as {\"train\": [...], \"val\": [...]} (or per-record \"split\" fields in JSONL/CSV); anything else is deterministically auto-split (last quarter to val). Text/log lines are \"input ||| expected\". Cases are untrusted data: quoted, never executed.",
				},
			},
			"required": []string{"id", "job_token", "filename", "content"},
		},
	},
	Handler:  (*Server).toolUploadEvolutionEvalSet,
	Behavior: toolBehavior{Title: "Upload Evolution Eval Set", Destructive: false, Idempotent: true},
}

func (srv *Server) toolUploadEvolutionEvalSet(args json.RawMessage) (interface{}, *JSONRPCError) {
	type EvalArgs struct {
		ID       string `json:"id"`
		JobToken string `json:"job_token"`
		Filename string `json:"filename"`
		Content  string `json:"content"`
	}
	var eArgs EvalArgs
	if e := decodeToolArgs(args, &eArgs); e != nil {
		return nil, e
	}
	if strings.TrimSpace(eArgs.ID) == "" {
		return nil, &JSONRPCError{Code: -32602, Message: "Missing or invalid arguments. 'id' is required."}
	}
	res, err := srv.Storage.UploadEvolutionEvalSet(eArgs.ID, eArgs.JobToken, eArgs.Filename, eArgs.Content)
	if err != nil {
		var authErr *JobAuthError
		if errors.As(err, &authErr) {
			srv.auditJobDenial("upload_evolution_eval_set", eArgs.ID, "", authErr)
		}
		return ToolResponse{IsError: true, Content: []ToolContent{{Type: "text", Text: fmt.Sprintf("Error uploading evolution eval set: %v", err)}}}, nil
	}
	passes := 0
	for _, sample := range res.Meta.DryRun {
		if sample.Pass {
			passes++
		}
	}
	respText := fmt.Sprintf("Success! Eval set stored for evolution job '%s' (%s, %s).\n"+
		"Eval hash: %s\nS0 baseline (current skill on val, scorer %s): %.4f\n"+
		"Dry run: %d/%d sampled val cases pass (previews quote uploaded data).\n"+
		"Cost estimate: %.2f USD across %d val cases — static stub for F3 budgets, never enforced.\n",
		res.Meta.JobID, res.Meta.Filename, evalSetPreview(res.Meta.TrainCount, res.Meta.ValCount, res.Meta.SplitMode),
		res.Meta.EvalHash, res.Meta.ScorerVersion, res.Meta.BaselineS0,
		passes, len(res.Meta.DryRun),
		res.Meta.Estimate.EstimatedCostUSD, res.Meta.Estimate.EstimatedValCases)
	return ToolResponse{Content: []ToolContent{{Type: "text", Text: respText}}}, nil
}
