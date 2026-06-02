# Collaborative Plans Metadata Updates & Document Version Displays 🛠️🕒

NexWiki provides powerful tools for combining human editing and programmatic AI management. This guide covers two premium features designed to streamline collaborative editing, track document revisions, and allow AI agents to manage project plans programmatically:
1. **Programmatic Metadata Updates (`edit_agent_plan` MCP Tool)**
2. **Version Number & Relative Time Displays in the UI**

---

## 🤖 Programmatic Plan Metadata Updates

Collaborative AI plans are saved on disk with standard YAML front matter containing their tags, titles, and edit summaries. Previously, AI agents could create plans or append content but could not edit existing metadata such as tags or titles without using the frontend GUI. 

The new **`edit_agent_plan`** MCP tool bridges this gap, allowing AI agents to programmatically rename plans, adjust tag classifications, and perform bulk metadata updates safely.

### 🔒 Governance & Protections
To maintain workspace integrity and prevent conflict:
* **Optimistic Locking**: Just like standard article edits, `edit_agent_plan` requires a positive `loaded_version` parameter. If a concurrent session has committed updates to disk in the meantime, the tool rejects the operation with a version conflict error, preventing overwrite collisions.
* **Tag Preservation**: The tool strictly enforces the preservation of the protected `aiagent-plan` tag. Even if an agent submits a new tags list that omits it, NexWiki automatically prepends `aiagent-plan` to the tag set before saving.
* **Plan Verification**: The target page must possess the `aiagent-plan` tag. The tool will reject requests targeting standard wiki articles or other memory types.

### 🔌 Tool Arguments & Signature
```json
{
  "name": "edit_agent_plan",
  "arguments": {
    "slug": "unique-plan-slug",
    "title": "New Plan Title (Optional)",
    "tags": ["project-alpha", "milestone-1"],
    "loaded_version": 2,
    "edit_summary": "Renamed plan and categorized with milestone tag"
  }
}
```

### 📝 Example: Direct JSON-RPC Execution via curl
You can trigger the tool over the HTTP MCP transport layer using a standard `POST` request:

```bash
curl -X POST http://localhost:8080/api/mcp \
  -H "Content-Type: application/json" \
  -d '{
    "jsonrpc": "2.0",
    "method": "tools/call",
    "params": {
      "name": "edit_agent_plan",
      "arguments": {
        "slug": "database-migration-plan",
        "title": "Production Database Migration Plan",
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
