package server

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// This file holds the wiki-wide utility tools: statistics, status tags, activity history, and OKF bundle I/O.
// Each tool pairs its JSON schema with its handler in one place, so the two can never
// drift apart. Registration order lives in mcp_tools.go.

var getWikiStatisticsTool = toolDef{
	Schema: map[string]interface{}{
		"name":        "get_wiki_statistics",
		"description": "Retrieve high-level wiki statistics, including total articles, storage footprint, and a list of dead or broken double-bracket internal WikiLinks.",
		"inputSchema": map[string]interface{}{
			"type":       "object",
			"properties": map[string]interface{}{},
		},
	},
	Handler:  (*Server).toolGetWikiStatistics,
	Behavior: toolBehavior{Title: "Get Wiki Statistics", ReadOnly: true},
}

func (srv *Server) toolGetWikiStatistics(args json.RawMessage) (interface{}, *JSONRPCError) {
	articles, err := srv.Storage.ListArticles()
	if err != nil {
		return ToolResponse{IsError: true, Content: []ToolContent{{Type: "text", Text: err.Error()}}}, nil
	}

	var fullArticles []*Article
	var activeSlugs = make(map[string]bool)
	activeSlugs["home"] = true // Implicitly exists

	for _, artMeta := range articles {
		art, err := srv.Storage.GetArticle(artMeta.Slug)
		if err == nil {
			fullArticles = append(fullArticles, art)
			activeSlugs[art.Slug] = true
		}
	}

	type BrokenLink struct {
		FromSlug   string
		TargetLink string
	}
	var brokenLinks []BrokenLink
	totalLinks := 0

	for _, art := range fullArticles {
		for _, target := range ExtractWikiLinkTargets(art.Content) {
			totalLinks++
			if !activeSlugs[Slugify(target)] {
				brokenLinks = append(brokenLinks, BrokenLink{
					FromSlug:   art.Slug,
					TargetLink: target,
				})
			}
		}
	}

	var respText string
	respText = "NexWiki Knowledge Base Statistics:\n"
	respText += fmt.Sprintf("- Total Articles: %d\n", len(articles))
	respText += fmt.Sprintf("- Total WikiLinks Scanned: %d\n", totalLinks)
	respText += fmt.Sprintf("- Total Broken/Dead WikiLinks: %d\n\n", len(brokenLinks))

	if len(brokenLinks) == 0 {
		respText += "Excellent! All double-bracket WikiLinks are healthy and fully connected! 🎉\n"
	} else {
		respText += "Broken/Dead WikiLinks Detected (AI suggestion: create these pages to heal the wiki!):\n"
		for _, bl := range brokenLinks {
			respText += fmt.Sprintf("  - Link '[[%s]]' inside article '/articles/%s' (Target slug: '%s' is missing)\n",
				bl.TargetLink, bl.FromSlug, Slugify(bl.TargetLink))
		}
	}

	return ToolResponse{Content: []ToolContent{{Type: "text", Text: respText}}}, nil
}

var getStatusTagsTool = toolDef{
	Schema: map[string]interface{}{
		"name":        "get_status_tags",
		"description": "Returns the canonical list of recognized status tags used in NexWiki to indicate the lifecycle state of wiki articles and AI plans. Use these tags when creating or editing articles and plans to signal their current status. Status tags are displayed with highest priority on the home dashboard.",
		"inputSchema": map[string]interface{}{
			"type":       "object",
			"properties": map[string]interface{}{},
		},
	},
	Handler:  (*Server).toolGetStatusTags,
	Behavior: toolBehavior{Title: "Get Status Tags", ReadOnly: true},
}

func (srv *Server) toolGetStatusTags(args json.RawMessage) (interface{}, *JSONRPCError) {
	text := "NexWiki Status Tags\n\nThe following tags indicate the lifecycle state of a wiki article or AI plan.\nApply them when creating or editing content to signal its current status.\nStatus tags are displayed with highest priority on the home dashboard.\n\nRecognized status tags:\n"
	for _, tag := range StatusTags {
		text += fmt.Sprintf("  • %s\n", tag)
	}
	text += "\nTips:\n"
	text += "  • Use 'list_agent_plans' with the 'tag' parameter to filter plans by status (e.g. tag: \"completed\").\n"
	text += "  • When a plan is fully implemented, use 'append_agent_plan' to add final notes, then use 'edit_agent_plan' to add the 'completed' status tag.\n"
	text += "  • The reserved AI-Agent-Plan type must NEVER be relabelled.\n"
	return ToolResponse{Content: []ToolContent{{Type: "text", Text: text}}}, nil
}

var getRecentActivityTool = toolDef{
	Schema: map[string]interface{}{
		"name":        "get_recent_activity",
		"description": "Query the durable wiki activity log to see what changed and when — useful at session start to catch up on edits made by other agents, processes, or the human since you last looked. Events from a different MCP process may lag by milliseconds.",
		"inputSchema": map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"since": map[string]interface{}{
					"type":        "string",
					"description": "Only return events newer than this. Accepts a Go duration (e.g. '30m', '24h', '168h' for a week) or an RFC3339 timestamp (e.g. '2026-06-10T00:00:00Z'). Omit for the newest events regardless of age.",
				},
				"limit": map[string]interface{}{
					"type":        "integer",
					"description": "Maximum number of events to return, newest kept (default 50, max 500).",
				},
				"action": map[string]interface{}{
					"type":        "string",
					"description": "Optional filter by action type.",
					"enum":        []string{"create", "edit", "delete", "read", "revert"},
				},
				"source": map[string]interface{}{
					"type":        "string",
					"description": "Optional filter by origin: 'mcp' for AI tool calls, 'api' for human web UI actions.",
					"enum":        []string{"mcp", "api"},
				},
			},
		},
	},
	Handler:  (*Server).toolGetRecentActivity,
	Behavior: toolBehavior{Title: "Get Recent Activity", ReadOnly: true},
}

func (srv *Server) toolGetRecentActivity(args json.RawMessage) (interface{}, *JSONRPCError) {
	type ActivityArgs struct {
		Since  string `json:"since"`
		Limit  int    `json:"limit"`
		Action string `json:"action"`
		Source string `json:"source"`
	}
	var aArgs ActivityArgs
	_ = json.Unmarshal(args, &aArgs) // all args optional

	var since time.Time
	if s := strings.TrimSpace(aArgs.Since); s != "" {
		if dur, err := time.ParseDuration(s); err == nil {
			since = time.Now().Add(-dur)
		} else if ts, err := time.Parse(time.RFC3339, s); err == nil {
			since = ts
		} else {
			return ToolResponse{IsError: true, Content: []ToolContent{{Type: "text", Text: fmt.Sprintf("Error: invalid 'since' value '%s'. Use a Go duration (e.g. '24h') or an RFC3339 timestamp.", s)}}}, nil
		}
	}

	limit := aArgs.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > 500 {
		limit = 500
	}

	events, err := ReadActivityLog(ActivityLogPath(srv.Storage.DataDir), since, limit, aArgs.Action, aArgs.Source)
	if err != nil {
		return ToolResponse{IsError: true, Content: []ToolContent{{Type: "text", Text: fmt.Sprintf("Error reading activity log: %v", err)}}}, nil
	}

	// Fall back to the in-memory ring buffer when no durable log exists yet
	if events == nil && srv.EventBus != nil {
		for _, ev := range srv.EventBus.GetHistory() {
			if !since.IsZero() && ev.Timestamp.Before(since) {
				continue
			}
			if aArgs.Action != "" && ev.Action != aArgs.Action {
				continue
			}
			if aArgs.Source != "" && ev.Source != aArgs.Source {
				continue
			}
			events = append(events, ev)
		}
		if len(events) > limit {
			events = events[len(events)-limit:]
		}
	}

	var text string
	if len(events) == 0 {
		text = "No activity events found for the given filters.\n"
	} else {
		text = fmt.Sprintf("Recent wiki activity (%d events, oldest first):\n\n", len(events))
		for _, ev := range events {
			toolStr := ev.Tool
			if toolStr == "" {
				toolStr = "web-ui"
			}
			line := fmt.Sprintf("%s [%s/%s] %s", ev.Timestamp.Format("2006-01-02 15:04:05"), ev.Source, ev.Action, toolStr)
			if ev.Title != "" || ev.Slug != "" {
				line += fmt.Sprintf(" → '%s' (%s)", ev.Title, ev.Slug)
			}
			if ev.Agent != "" {
				line += " by " + ev.Agent
			}
			text += line + "\n"
		}
	}

	return ToolResponse{Content: []ToolContent{{Type: "text", Text: text}}}, nil
}

var exportOkfBundleTool = toolDef{
	Schema: map[string]interface{}{
		"name":        "export_okf_bundle",
		"description": "Export the entire knowledge base as a conformant Open Knowledge Format (OKF v0.1) bundle (a .zip). The bundle hierarchy is synthesized from each document's type, with reserved index.md / log.md files and bundle-relative links. Writes the archive into the data directory and returns its path.",
		"inputSchema": map[string]interface{}{
			"type":       "object",
			"properties": map[string]interface{}{},
		},
	},
	Handler:  (*Server).toolExportOkfBundle,
	Behavior: toolBehavior{Title: "Export OKF Bundle", ReadOnly: true},
}

func (srv *Server) toolExportOkfBundle(args json.RawMessage) (interface{}, *JSONRPCError) {
	data, err := srv.Storage.ExportOKFBundle()
	if err != nil {
		return ToolResponse{IsError: true, Content: []ToolContent{{Type: "text", Text: fmt.Sprintf("Error exporting OKF bundle: %v", err)}}}, nil
	}
	fileName := fmt.Sprintf("okf-export-%s.zip", time.Now().UTC().Format("2006-01-02T15-04-05Z"))
	outPath := filepath.Join(srv.Storage.DataDir, fileName)
	if err := os.WriteFile(outPath, data, 0644); err != nil {
		return ToolResponse{IsError: true, Content: []ToolContent{{Type: "text", Text: fmt.Sprintf("Error writing OKF bundle to disk: %v", err)}}}, nil
	}
	respText := fmt.Sprintf("Success! Exported OKF v%s bundle (%d bytes) to:\n%s\n", OKFVersion, len(data), outPath)
	return ToolResponse{Content: []ToolContent{{Type: "text", Text: respText}}}, nil
}

var importOkfBundleTool = toolDef{
	Schema: map[string]interface{}{
		"name":        "import_okf_bundle",
		"description": "Import an Open Knowledge Format (OKF v0.1) bundle (.zip) from a filesystem path into the knowledge base. Each concept document is created or updated (dedup by slug), bundle-relative links are translated back to WikiLinks, and a permissive conformance report is returned (documents missing a type default to Wiki and are flagged rather than rejected).",
		"inputSchema": map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"path": map[string]interface{}{
					"type":        "string",
					"description": "Filesystem path to the .zip OKF bundle to import.",
				},
			},
			"required": []string{"path"},
		},
	},
	Handler:  (*Server).toolImportOkfBundle,
	Behavior: toolBehavior{Title: "Import OKF Bundle", Destructive: true, Idempotent: false},
}

func (srv *Server) toolImportOkfBundle(args json.RawMessage) (interface{}, *JSONRPCError) {
	type ImportArgs struct {
		Path string `json:"path"`
	}
	var iArgs ImportArgs
	if e := decodeToolArgs(args, &iArgs); e != nil {
		return nil, e
	}
	if strings.TrimSpace(iArgs.Path) == "" {
		return nil, &JSONRPCError{Code: -32602, Message: "Missing or invalid 'path' argument"}
	}
	data, err := os.ReadFile(iArgs.Path)
	if err != nil {
		return ToolResponse{IsError: true, Content: []ToolContent{{Type: "text", Text: fmt.Sprintf("Error reading bundle at '%s': %v", iArgs.Path, err)}}}, nil
	}
	report, err := srv.Storage.ImportOKFBundle(data)
	if err != nil {
		return ToolResponse{IsError: true, Content: []ToolContent{{Type: "text", Text: fmt.Sprintf("Error importing OKF bundle: %v", err)}}}, nil
	}
	respText := fmt.Sprintf("OKF import complete: %d imported, %d skipped.\n", report.Imported, report.Skipped)
	if len(report.MissingType) > 0 {
		respText += fmt.Sprintf("Documents defaulted to Wiki (missing/unknown type): %s\n", strings.Join(report.MissingType, ", "))
	}
	for _, wmsg := range report.Warnings {
		respText += "Warning: " + wmsg + "\n"
	}
	return ToolResponse{Content: []ToolContent{{Type: "text", Text: respText}}}, nil
}
