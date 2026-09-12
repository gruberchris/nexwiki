package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// This file holds story 04's MCP surface for the headless evolution job runner:
// five tools covering the lifecycle in skill_jobs.go. Cancellation, pause, and
// resume stay internal Go APIs (Storage.Cancel/Pause/ResumeEvolutionJob) for the
// story 08 UI to wire — the MCP surface stays minimal per the docs-integrity
// rule. All five run through the story-02 scope gate in executeToolCall*: they
// need jobs.manage, which no phase role holds, so agent roles are denied exactly
// like promotion. A call carrying the job's own per-harness token bypasses the
// process role (validJobTokenForArgs); anything else without the scope is
// refused and audited as a deny event.

var createEvolutionJobTool = toolDef{
	Schema: map[string]interface{}{
		"name":        "create_evolution_job",
		"description": "Queue a headless evolution run for one skill and mint its per-harness token. One run per skill — refused while the skill has an active job. The token is returned once and never retrievable again; every later lifecycle call must present it.",
		"inputSchema": map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"skill_slug": map[string]interface{}{
					"type":        "string",
					"description": "The URL-safe slug of the skill to evolve.",
				},
				"candidate_id": map[string]interface{}{
					"type":        "string",
					"description": "Optional pending candidate this run evolves. Must belong to the same skill.",
				},
				"profile": map[string]interface{}{
					"type":        "string",
					"description": "BYO CLI profile to run (server configuration): opencode or claude-code. Defaults to opencode.",
				},
			},
			"required": []string{"skill_slug"},
		},
	},
	Handler:  (*Server).toolCreateEvolutionJob,
	Behavior: toolBehavior{Title: "Create Evolution Job", Destructive: false, Idempotent: false},
}

func (srv *Server) toolCreateEvolutionJob(args json.RawMessage) (interface{}, *JSONRPCError) {
	type CreateArgs struct {
		SkillSlug   string `json:"skill_slug"`
		CandidateID string `json:"candidate_id"`
		Profile     string `json:"profile"`
	}
	var cArgs CreateArgs
	if e := decodeToolArgs(args, &cArgs); e != nil {
		return nil, e
	}
	if strings.TrimSpace(cArgs.SkillSlug) == "" {
		return nil, &JSONRPCError{Code: -32602, Message: "Missing or invalid arguments. 'skill_slug' is required."}
	}
	job, token, err := srv.Storage.CreateEvolutionJob(cArgs.SkillSlug, cArgs.CandidateID, cArgs.Profile)
	if err != nil {
		return ToolResponse{IsError: true, Content: []ToolContent{{Type: "text", Text: fmt.Sprintf("Error creating evolution job: %v", err)}}}, nil
	}
	respText := fmt.Sprintf("Success! Evolution job '%s' queued for skill '%s' (profile %s, run cap %s).\n"+
		"Job token (shown ONCE — store it harness-side, it is never retrievable again): %s\nStatus: %s\n",
		job.ID, job.SkillSlug, job.Profile, job.RunDeadlineAt.Format(time.RFC3339), token, job.Status)
	return ToolResponse{Content: []ToolContent{{Type: "text", Text: respText}}}, nil
}

var claimEvolutionJobTool = toolDef{
	Schema: map[string]interface{}{
		"name":        "claim_evolution_job",
		"description": "Claim a queued evolution job for a runner, starting its 60-second heartbeat lease. Requires the job's per-harness token. A lease-expired (requeued) job is claimable again.",
		"inputSchema": map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"id": map[string]interface{}{
					"type":        "string",
					"description": "The job ID to claim.",
				},
				"job_token": map[string]interface{}{
					"type":        "string",
					"description": "The per-harness token minted at creation, scoped to this job.",
				},
				"runner": map[string]interface{}{
					"type":        "string",
					"description": "Optional runner identity recorded on the claim.",
				},
			},
			"required": []string{"id", "job_token"},
		},
	},
	Handler:  (*Server).toolClaimEvolutionJob,
	Behavior: toolBehavior{Title: "Claim Evolution Job", Destructive: false, Idempotent: false},
}

func (srv *Server) toolClaimEvolutionJob(args json.RawMessage) (interface{}, *JSONRPCError) {
	type ClaimArgs struct {
		ID       string `json:"id"`
		JobToken string `json:"job_token"`
		Runner   string `json:"runner"`
	}
	var cArgs ClaimArgs
	if e := decodeToolArgs(args, &cArgs); e != nil {
		return nil, e
	}
	if strings.TrimSpace(cArgs.ID) == "" {
		return nil, &JSONRPCError{Code: -32602, Message: "Missing or invalid arguments. 'id' is required."}
	}
	job, err := srv.Storage.ClaimEvolutionJob(cArgs.ID, cArgs.JobToken, cArgs.Runner)
	if err != nil {
		var authErr *JobAuthError
		if errors.As(err, &authErr) {
			srv.auditJobDenial("claim_evolution_job", cArgs.ID, "", authErr)
		}
		return ToolResponse{IsError: true, Content: []ToolContent{{Type: "text", Text: fmt.Sprintf("Error claiming evolution job: %v", err)}}}, nil
	}
	respText := fmt.Sprintf("Success! Evolution job '%s' claimed by '%s'.\nStatus: %s\nLease expires: %s (heartbeat within 60s to renew)\n",
		job.ID, job.Runner, job.Status, job.LeaseExpiresAt.Format(time.RFC3339))
	return ToolResponse{Content: []ToolContent{{Type: "text", Text: respText}}}, nil
}

var heartbeatEvolutionJobTool = toolDef{
	Schema: map[string]interface{}{
		"name":        "heartbeat_evolution_job",
		"description": "Renew an evolution job's 60-second lease and record progress. Requires the job's per-harness token. When a pause was requested, the heartbeat parks the job at this step boundary instead of advancing it.",
		"inputSchema": map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"id": map[string]interface{}{
					"type":        "string",
					"description": "The job ID to heartbeat.",
				},
				"job_token": map[string]interface{}{
					"type":        "string",
					"description": "The per-harness token minted at creation, scoped to this job.",
				},
				"iteration": map[string]interface{}{
					"type":        "integer",
					"description": "Optional step number completed (max 12). Past the cap the job fails.",
				},
				"progress": map[string]interface{}{
					"type":        "number",
					"description": "Optional progress fraction 0-1.",
				},
				"note": map[string]interface{}{
					"type":        "string",
					"description": "Optional progress note recorded on the job.",
				},
			},
			"required": []string{"id", "job_token"},
		},
	},
	Handler:  (*Server).toolHeartbeatEvolutionJob,
	Behavior: toolBehavior{Title: "Heartbeat Evolution Job", Destructive: false, Idempotent: false},
}

func (srv *Server) toolHeartbeatEvolutionJob(args json.RawMessage) (interface{}, *JSONRPCError) {
	type HeartbeatArgs struct {
		ID        string  `json:"id"`
		JobToken  string  `json:"job_token"`
		Iteration int     `json:"iteration"`
		Progress  float64 `json:"progress"`
		Note      string  `json:"note"`
	}
	var hArgs HeartbeatArgs
	if e := decodeToolArgs(args, &hArgs); e != nil {
		return nil, e
	}
	if strings.TrimSpace(hArgs.ID) == "" {
		return nil, &JSONRPCError{Code: -32602, Message: "Missing or invalid arguments. 'id' is required."}
	}
	job, parked, err := srv.Storage.HeartbeatEvolutionJob(hArgs.ID, hArgs.JobToken, hArgs.Iteration, hArgs.Progress, hArgs.Note)
	if err != nil {
		var authErr *JobAuthError
		if errors.As(err, &authErr) {
			srv.auditJobDenial("heartbeat_evolution_job", hArgs.ID, "", authErr)
		}
		return ToolResponse{IsError: true, Content: []ToolContent{{Type: "text", Text: fmt.Sprintf("Error heartbeating evolution job: %v", err)}}}, nil
	}
	status := string(job.Status)
	extra := ""
	if parked {
		extra = " Paused at step boundary — stop scheduling further steps; resume re-queues the job from its checkpoint."
	}
	respText := fmt.Sprintf("Success! Evolution job '%s' heartbeat recorded (iteration %d, progress %.2f).\nStatus: %s\nLease expires: %s.%s\n",
		job.ID, job.Iteration, job.Progress, status, job.LeaseExpiresAt.Format(time.RFC3339), extra)
	return ToolResponse{Content: []ToolContent{{Type: "text", Text: respText}}}, nil
}

var uploadJobArtifactTool = toolDef{
	Schema: map[string]interface{}{
		"name":        "upload_job_artifact",
		"description": "Attach a file to a claimed/running evolution job. The content_hash (SHA-256 hex over the content bytes) is verified before acceptance — a mismatch stores nothing. Re-uploads with the same idempotency_key return the original without duplicating.",
		"inputSchema": map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"id": map[string]interface{}{
					"type":        "string",
					"description": "The job ID to attach the artifact to.",
				},
				"job_token": map[string]interface{}{
					"type":        "string",
					"description": "The per-harness token minted at creation, scoped to this job.",
				},
				"name": map[string]interface{}{
					"type":        "string",
					"description": "Bare filename for the artifact.",
				},
				"content": map[string]interface{}{
					"type":        "string",
					"description": "Artifact content (stored byte-identical after hash verification).",
				},
				"content_hash": map[string]interface{}{
					"type":        "string",
					"description": "SHA-256 hex over the content bytes. Required; checked before acceptance.",
				},
				"idempotency_key": map[string]interface{}{
					"type":        "string",
					"description": "Optional key deduplicating re-uploads.",
				},
			},
			"required": []string{"id", "job_token", "name", "content", "content_hash"},
		},
	},
	Handler:  (*Server).toolUploadJobArtifact,
	Behavior: toolBehavior{Title: "Upload Job Artifact", Destructive: false, Idempotent: true},
}

func (srv *Server) toolUploadJobArtifact(args json.RawMessage) (interface{}, *JSONRPCError) {
	type ArtifactArgs struct {
		ID             string `json:"id"`
		JobToken       string `json:"job_token"`
		Name           string `json:"name"`
		Content        string `json:"content"`
		ContentHash    string `json:"content_hash"`
		IdempotencyKey string `json:"idempotency_key"`
	}
	var aArgs ArtifactArgs
	if e := decodeToolArgs(args, &aArgs); e != nil {
		return nil, e
	}
	if strings.TrimSpace(aArgs.ID) == "" {
		return nil, &JSONRPCError{Code: -32602, Message: "Missing or invalid arguments. 'id' is required."}
	}
	art, err := srv.Storage.UploadJobArtifact(aArgs.ID, aArgs.JobToken, aArgs.Name, aArgs.Content, aArgs.ContentHash, aArgs.IdempotencyKey)
	if err != nil {
		var authErr *JobAuthError
		if errors.As(err, &authErr) {
			srv.auditJobDenial("upload_job_artifact", aArgs.ID, "", authErr)
		}
		return ToolResponse{IsError: true, Content: []ToolContent{{Type: "text", Text: fmt.Sprintf("Error uploading job artifact: %v", err)}}}, nil
	}
	respText := fmt.Sprintf("Success! Artifact '%s' attached to evolution job '%s' (hash %s, %d bytes).\n",
		art.Name, strings.TrimSpace(aArgs.ID), art.ContentHash, art.Size)
	return ToolResponse{Content: []ToolContent{{Type: "text", Text: respText}}}, nil
}

var completeEvolutionJobTool = toolDef{
	Schema: map[string]interface{}{
		"name":        "complete_evolution_job",
		"description": "Mark a claimed/running evolution job complete or failed (terminal — releases the per-skill lock). Requires the job's per-harness token. Re-completing with the same outcome is a no-op.",
		"inputSchema": map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"id": map[string]interface{}{
					"type":        "string",
					"description": "The job ID to complete.",
				},
				"job_token": map[string]interface{}{
					"type":        "string",
					"description": "The per-harness token minted at creation, scoped to this job.",
				},
				"outcome": map[string]interface{}{
					"type":        "string",
					"description": "complete or failed.",
				},
				"error": map[string]interface{}{
					"type":        "string",
					"description": "Optional failure detail recorded on the job.",
				},
			},
			"required": []string{"id", "job_token", "outcome"},
		},
	},
	Handler:  (*Server).toolCompleteEvolutionJob,
	Behavior: toolBehavior{Title: "Complete Evolution Job", Destructive: true, Idempotent: true},
}

func (srv *Server) toolCompleteEvolutionJob(args json.RawMessage) (interface{}, *JSONRPCError) {
	type CompleteArgs struct {
		ID       string `json:"id"`
		JobToken string `json:"job_token"`
		Outcome  string `json:"outcome"`
		Error    string `json:"error"`
	}
	var cArgs CompleteArgs
	if e := decodeToolArgs(args, &cArgs); e != nil {
		return nil, e
	}
	if strings.TrimSpace(cArgs.ID) == "" {
		return nil, &JSONRPCError{Code: -32602, Message: "Missing or invalid arguments. 'id' is required."}
	}
	job, err := srv.Storage.CompleteEvolutionJob(cArgs.ID, cArgs.JobToken, cArgs.Outcome, cArgs.Error)
	if err != nil {
		var authErr *JobAuthError
		if errors.As(err, &authErr) {
			srv.auditJobDenial("complete_evolution_job", cArgs.ID, "", authErr)
		}
		return ToolResponse{IsError: true, Content: []ToolContent{{Type: "text", Text: fmt.Sprintf("Error completing evolution job: %v", err)}}}, nil
	}
	respText := fmt.Sprintf("Success! Evolution job '%s' marked %s.\n", job.ID, job.Status)
	return ToolResponse{Content: []ToolContent{{Type: "text", Text: respText}}}, nil
}
