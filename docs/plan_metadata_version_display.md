# Collaborative Plans Metadata Updates & Document Version Displays 🛠️🕒

NexWiki provides powerful tools for combining human editing and programmatic AI management. This guide covers two premium features designed to streamline collaborative editing, track document revisions, and allow AI agents to manage project plans programmatically:
1. **Programmatic Metadata Updates (`save_article` MCP Tool)**
2. **Version Number & Relative Time Displays in the UI**

---

## 🤖 Programmatic Plan Metadata Updates

Collaborative AI plans are saved on disk as OKF documents with real YAML front matter carrying their `type`, tags, titles, and edit summaries. Previously, AI agents could create plans or append content but could not edit existing metadata such as tags or titles without using the frontend GUI. 

The **`save_article`** MCP tool bridges this gap. Called with the `slug` of an existing plan, it updates that plan in place, allowing AI agents to programmatically rename plans, replace plan content, adjust tag classifications, move the lifecycle `status`, and perform bulk metadata updates safely.

### 🔒 Governance & Protections
To maintain workspace integrity and prevent conflict:
* **Optimistic Locking**: Pass the `loaded_version` you got from `read_article`. If a concurrent session has committed updates to disk in the meantime, the tool rejects the operation with a version conflict error, preventing overwrite collisions. (`loaded_version` is optional, but an edit without it cannot detect a collision.)
* **Type Preservation**: An edit that omits `type` keeps the plan's reserved OKF `type: AI-Agent-Plan`, regardless of the tags array submitted. The type changes only when you pass `type` explicitly — which relabels the document and applies that type's status rules — so an ordinary metadata update never needs it. (Older NexWiki builds enforced the class with a protected `aiagent-plan` *tag*; that tag no longer exists.)
* **Plan Arguments**: On a plan, `status` must be one of the eight plan lifecycle statuses (omit it to preserve the current one), and `project_context` adds a project tag. Lifecycle words are rejected as tags.
* **Content Replacement**: `title` and `content` are required on every call, and `content` fully replaces the plan body — read the plan first and send its current body back when you only mean to change metadata. Omitted `tags`, `description`, and `source` are preserved. Use `append_article` when you only want to add progress notes.

### 🔌 Tool Arguments & Signature
```json
{
  "name": "save_article",
  "arguments": {
    "slug": "unique-plan-slug",
    "title": "New Plan Title",
    "content": "<the plan body, unchanged or revised>",
    "tags": ["project-alpha", "milestone-1"],
    "loaded_version": 2,
    "edit_summary": "Renamed plan and categorized with milestone tag"
  }
}
```

### 📝 Example: Direct JSON-RPC Execution via curl
You can trigger the tool over the HTTP MCP transport layer using a standard `POST` request:

```bash
curl -X POST http://localhost:5808/api/mcp \
  -H "Content-Type: application/json" \
  -d '{
    "jsonrpc": "2.0",
    "method": "tools/call",
    "params": {
      "name": "save_article",
      "arguments": {
        "slug": "database-migration-plan",
        "title": "Production Database Migration Plan",
        "content": "# Production Database Migration Plan\n\n- [ ] Snapshot the current database\n- [ ] Run the migration in staging",
        "tags": ["postgres", "q3-goals"],
        "loaded_version": 1,
        "edit_summary": "Promoted title and tagged with Postgres DB stack"
      }
    },
    "id": 1
  }'
```

---

## 🕒 Version Display in the UI

Understanding the history of an article or plan is vital when collaborating. NexWiki upgrades the standard document header and editor panels to display the document's active **Version Number** next to the edited metadata.

### 1. Standard Article Viewer Metadata
When viewing any wiki article, the metadata block at the top of the page leaves the creation date exactly as it was, while prepending the active version number to the edited date:
* **Current UI**: `EDITED June 1, 2026 AT 09:06 PM`
* **Upgraded UI**: `V3 EDITED June 1, 2026 AT 09:06 PM`

This ensures that the creation time remains unmodified, while edited documents cleanly present their active revision number (e.g. `V5`) in front of the absolute edit timestamp.

### 2. Editor Mode Badges
When editing an existing article, custom skill, or plan, the top control bar's **Mode Badges** adapt dynamically to render the active document's version number next to the mode indicator:
* **Wiki Article**: `Wiki Article Mode (V2)`
* **AI Plan**: `Collaborative AI Plan Mode (V4)`
* **AI Skill**: `Custom AI Skill Mode (V1)`

This ensures that you always know exactly which version of the document you are currently editing, reducing conflict risks and improving editing context.
