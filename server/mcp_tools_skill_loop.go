package server

import (
	"encoding/json"
	"fmt"
	"strings"
)

// This file holds the story 07 loop stepper's MCP surface: one read-only tool
// over the iteration state machine in skill_loop.go. The wizard (story 08)
// reads the same state through the internal Go APIs — this tool exists so a
// token-holding harness (and an operator inspecting a run over MCP) can see
// where the loop stands without filesystem access. Every other loop control —
// pause, abort, approve-early, dispatch — stays an internal Go API per the
// docs-integrity rule: the tool count grows by exactly one, and every doc
// reference carries it.
//
// Like every job tool it needs `jobs.manage`, which no evolution phase role
// holds, so agent roles are denied exactly like promotion; a call carrying the
// job's own per-harness token bypasses the process role via
// validJobTokenForArgs (the story-02 seam).

var getEvolutionIterationsTool = toolDef{
	Schema: map[string]interface{}{
		"name":        "get_evolution_iterations",
		"description": "Read the evolution loop stepper's state for one headless job: current iteration, current phase (inference → maintaining → proposing → gating), the latest progress note, and a summary of every recorded iteration — its candidate, pattern slugs, validation score versus R_best, and accepted/rejected outcome.",
		"inputSchema": map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"id": map[string]interface{}{
					"type":        "string",
					"description": "The evolution job ID whose loop state to read.",
				},
				"job_token": map[string]interface{}{
					"type":        "string",
					"description": "The per-harness token minted at creation, scoped to this job.",
				},
			},
			"required": []string{"id", "job_token"},
		},
	},
	Handler:  (*Server).toolGetEvolutionIterations,
	Behavior: toolBehavior{Title: "Get Evolution Iterations", ReadOnly: true},
}

func (srv *Server) toolGetEvolutionIterations(args json.RawMessage) (interface{}, *JSONRPCError) {
	type LoopArgs struct {
		ID       string `json:"id"`
		JobToken string `json:"job_token"`
	}
	var lArgs LoopArgs
	if e := decodeToolArgs(args, &lArgs); e != nil {
		return nil, e
	}
	if strings.TrimSpace(lArgs.ID) == "" {
		// A read-only tool answers missing arguments as an error result, not a
		// protocol error — every read tool degrades gracefully on empty args.
		return ToolResponse{IsError: true, Content: []ToolContent{{Type: "text", Text: "Error reading evolution loop: 'id' (and the job's 'job_token') is required."}}}, nil
	}
	job, err := srv.Storage.GetEvolutionJob(lArgs.ID)
	if err != nil {
		return ToolResponse{IsError: true, Content: []ToolContent{{Type: "text", Text: fmt.Sprintf("Error reading evolution loop: %v", err)}}}, nil
	}
	if !verifyJobToken(job, lArgs.JobToken) {
		authErr := &JobAuthError{JobID: job.ID, Action: "read iterations"}
		srv.auditJobDenial("get_evolution_iterations", job.ID, "", authErr)
		return ToolResponse{IsError: true, Content: []ToolContent{{Type: "text", Text: fmt.Sprintf("Error reading evolution loop: %v", authErr)}}}, nil
	}

	st, err := srv.Storage.GetLoopState(job.ID)
	if err != nil {
		return ToolResponse{IsError: true, Content: []ToolContent{{Type: "text", Text: fmt.Sprintf("Error reading evolution loop: %v", err)}}}, nil
	}
	recs, err := srv.Storage.ListIterationRecords(job.ID)
	if err != nil {
		return ToolResponse{IsError: true, Content: []ToolContent{{Type: "text", Text: fmt.Sprintf("Error reading evolution loop: %v", err)}}}, nil
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Evolution loop for job '%s' (skill '%s', status %s):\n", job.ID, job.SkillSlug, job.Status)
	if st == nil {
		b.WriteString("No loop state yet — the stepper has not driven this job. Queue it with a dispatch and it will advance inference → maintaining → proposing → gating per iteration.\n")
	} else {
		fmt.Fprintf(&b, "Loop status: %s · iteration %d · phase %s", st.Status, st.CurrentIteration, st.CurrentPhase)
		switch st.PhaseStatus {
		case LoopStatusRunning:
			b.WriteString(" (step running)")
		case "interrupted":
			b.WriteString(" (interrupted — resumes by re-running this phase)")
		}
		b.WriteString("\n")
		if st.Note != "" {
			fmt.Fprintf(&b, "Latest note: %s\n", st.Note)
		}
		plateau, _ := srv.Storage.computePlateauCount(job.ID)
		fmt.Fprintf(&b, "Plateau: %d consecutive rejected iterations without pattern gain (limit %d)\n", plateau, plateauLimitFor(job))
		if st.Outcome != "" {
			fmt.Fprintf(&b, "Loop outcome: %s — %s\n", st.Outcome, st.OutcomeReason)
		}
	}
	if len(recs) == 0 {
		b.WriteString("No iteration records yet.\n")
	} else {
		b.WriteString("Iteration history (oldest first):\n")
		for _, rec := range recs {
			summary := iterationSummaryLine(rec)
			fmt.Fprintf(&b, "  %s\n", summary)
		}
	}
	return ToolResponse{Content: []ToolContent{{Type: "text", Text: b.String()}}}, nil
}

// iterationSummaryLine renders one iteration record as a single history line:
// outcome, scores against R_best, candidate and pattern count, result version.
func iterationSummaryLine(rec IterationRecord) string {
	var b strings.Builder
	fmt.Fprintf(&b, "#%d %-10s score %.4f vs R_best %.4f", rec.Iteration, outcomeOrRunning(rec.Outcome), rec.ValScore, rec.RBest)
	if rec.CandidateID != "" {
		fmt.Fprintf(&b, " — candidate %s (%d patterns)", rec.CandidateID, len(rec.PatternSlugs))
	} else {
		b.WriteString(" — no candidate")
	}
	if rec.ResultVersion > 0 {
		fmt.Fprintf(&b, " — skill v%d", rec.ResultVersion)
	}
	if rec.Outcome == IterationOutcomeInterrupted && rec.Note != "" {
		fmt.Fprintf(&b, " — %s", rec.Note)
	}
	return b.String()
}

// outcomeOrRunning renders the in-flight outcome legibly.
func outcomeOrRunning(outcome string) string {
	if outcome == "" {
		return "running"
	}
	return outcome
}
