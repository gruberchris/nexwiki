package server

import (
	"encoding/json"
	"fmt"
	"strings"
)

// This file holds story 06's MCP surface for the trained marker: one small
// read tool. The wizard (story 08) is the primary consumer, but any agent can
// check whether a skill is trained, stale, or untrained before loading it —
// no write tool exists here, because stamping belongs to the promote path and
// rollback/unlock to the operator, exactly as in stories 01–05.

var getSkillTrainedStateTool = toolDef{
	Schema: map[string]interface{}{
		"name":        "get_skill_trained_state",
		"description": "Get the trained state of Custom AI Skills: untrained (no marker), trained (with trained_at/version/val_score/eval_hash metadata), or stale (flag + reason — post-train skill edit, eval-set change, revocation, or a tampered marker). Pass 'slug' for one skill; omit it to list every skill.",
		"inputSchema": map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"slug": map[string]interface{}{
					"type":        "string",
					"description": "Optional URL-safe slug of one skill. Omit to list the trained state of every skill.",
				},
			},
		},
	},
	Output:   skillTrainedStateOutputSchema("Every skill's derived trained state."),
	Handler:  (*Server).toolGetSkillTrainedState,
	Behavior: toolBehavior{Title: "Get Skill Trained State", ReadOnly: true},
}

func (srv *Server) toolGetSkillTrainedState(args json.RawMessage) (interface{}, *JSONRPCError) {
	var gArgs struct {
		Slug string `json:"slug"`
	}
	if e := decodeToolArgs(args, &gArgs); e != nil {
		return nil, e
	}

	if strings.TrimSpace(gArgs.Slug) == "" {
		entries, err := srv.Storage.ListSkillTrainedStates()
		if err != nil {
			return ToolResponse{IsError: true, Content: []ToolContent{{Type: "text", Text: fmt.Sprintf("Error listing skill trained states: %v", err)}}}, nil
		}
		var text string
		if len(entries) == 0 {
			text = "No Custom AI Agent Skills found inside the knowledge base.\n"
		} else {
			var b strings.Builder
			b.WriteString("Skill Trained States:\n\n")
			for _, e := range entries {
				b.WriteString(fmt.Sprintf("- %s (%s): %s\n", e.Title, e.Slug, trainedStateLine(e.SkillTrainedState)))
			}
			text = b.String()
		}
		return ToolResponse{
			Content:           []ToolContent{{Type: "text", Text: text}},
			StructuredContent: SkillTrainedStateListOutput{Count: len(entries), Skills: entries},
		}, nil
	}

	entry, err := srv.Storage.GetSkillTrainedState(gArgs.Slug)
	if err != nil {
		return ToolResponse{IsError: true, Content: []ToolContent{{Type: "text", Text: fmt.Sprintf("Error: %v", err)}}}, nil
	}
	text := fmt.Sprintf("%s (%s): %s\n", entry.Title, entry.Slug, trainedStateLine(entry.SkillTrainedState))
	return ToolResponse{
		Content:           []ToolContent{{Type: "text", Text: text}},
		StructuredContent: SkillTrainedStateListOutput{Count: 1, Skills: []SkillTrainedStateEntry{*entry}},
	}, nil
}

// trainedStateLine renders one picker row for the prose half: the state first,
// then the marker metadata or the staleness reason, never both silent.
func trainedStateLine(st SkillTrainedState) string {
	switch st.State {
	case TrainedStateTrained:
		line := fmt.Sprintf("trained (v%d", st.TrainedVersion)
		if st.TrainedParentVersion > 0 {
			line += fmt.Sprintf(", pre-train v%d", st.TrainedParentVersion)
		}
		line += fmt.Sprintf(", val score %.4f", st.TrainedValScore)
		if st.TrainedEvalHash != "" {
			line += fmt.Sprintf(", eval %.12s", st.TrainedEvalHash)
		} else {
			line += ", no eval set"
		}
		if st.TrainedCandidate != "" {
			line += fmt.Sprintf(", candidate %s", st.TrainedCandidate)
		}
		if !st.TrainedAt.IsZero() {
			line += fmt.Sprintf(", at %s", st.TrainedAt.UTC().Format("2006-01-02 15:04:05"))
		}
		return line + ")"
	case TrainedStateStale:
		return fmt.Sprintf("stale — %s", st.Reason)
	default:
		return "untrained"
	}
}
