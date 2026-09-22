# NexWiki History Redaction & Audit Guide 🧹

NexWiki keeps every revision of every document. That is what makes the history timeline, diffs, and reverts work — and it also means that rewriting a document does **not** remove anything from it. The old text stays one `read_article(version: N)` call away.

This guide covers the two tools for taking text *out* of a wiki without deleting the document that held it:

* **`save_article(purge_history: true)`** — rewrite a document and permanently drop every earlier revision.
* **`search_wiki(include_history: true)`** — prove afterwards that no revision of any document still contains the text.

It also explains how to fix a document that was saved with the wrong **type**, which is the other edit people used to reach for delete-and-recreate to do.

---

## 🤔 When to redact

A second brain that agents write into unattended accumulates things that should not have been written down:

* a project or vendor name that is under embargo,
* a customer's personal details,
* an internal hostname, IP address, or ticket number that must not leave the team,
* a sentence you were asked to retract.

Before this feature the only way to clear history was `delete_article`, which also destroys the document, its slug, its backlinks, and every revision nobody objected to. Agent harnesses rightly refuse an irreversible deletion without asking, so a routine redaction turned into a round trip to the user.

A purge is not a deletion of anything a reader sees today. The document, its slug, its type, its status, and every link to it stay exactly as they are.

> **Credentials are a different problem.** NexWiki refuses to store a write that looks like an API key, token, private key, or password in the first place (see the secret-scanning section of the [MCP Server Guide](./mcp_server.md#-secret-scanning--every-write-is-checked-and-a-hit-is-refused)). If a credential did land in a document, rotate it: a purge removes it from NexWiki, not from wherever else it was copied.

---

## 🧽 Redacting a document

### Step 1 — Read the document

A purge only removes revisions you have seen, so it requires the version you loaded:

```jsonc
// read_article
{ "slug": "perceptea" }
// → Version: 4
```

### Step 2 — Save the clean text with `purge_history: true`

Rewrite **everything** that carries the text: the body, and also `description`, `source`, `tags`, and `title` if they mention it. A revision is the whole file, front matter included, so the current revision must be clean too — the purge keeps it.

```jsonc
// save_article
{
  "slug": "perceptea",
  "title": "Perceptea",
  "content": "# Perceptea\n\nA vision pipeline for ...",
  "description": "A vision pipeline",
  "source": "internal design review",
  "tags": ["vision"],
  "loaded_version": 4,
  "purge_history": true
}
```

The response states exactly what happened:

```
Success! Document 'Perceptea' (slug: perceptea) updated successfully.
Type: Wiki
New Version: 5
...
History purged.
revisions_removed: [1, 2, 3, 4]
activity_entries_retitled: 7
What survives: only revision 5, the one this save wrote. The document, its slug, type, status, and backlinks are unchanged.
```

What the purge does:

| Removed | Kept |
|---|---|
| Every `data/history/<slug>/<N>.md.gz` older than the revision just written — body **and** metadata | The revision just written, and any newer one a concurrent save produced |
| The old title and slug on the document's earlier activity-log entries (the durable `activity.jsonl`, its rotated archives, and the live Activity Drawer feed) — they are rewritten to the current title and slug | The activity entries themselves: who did what, and when |
| | The version counter — the next save is version 6, never a reused number, so optimistic locking stays safe |

If you renamed the document in the same save (its title, and so its slug, changed), activity entries recorded under the old slug are moved to the new one as well: a slug is derived from a title, so it can carry the very name being removed.

### Step 3 — Verify

```jsonc
// search_wiki
{ "query": "JEV", "include_history": true }
```

```
== Version history scan ==
Scanned every stored revision (history and live files) for "JEV" as a case-insensitive literal substring, front matter included.
Filters never narrow this scan; it covers every document.
No revision of any document contains it.
```

That last line is a finding, not a claim. An agent can now report "no revision anywhere contains X" with evidence.

### Over REST

The web API accepts the same field on `PUT /api/articles/{slug}`:

```bash
curl -X PUT http://localhost:5808/api/articles/perceptea \
  -H 'Content-Type: application/json' \
  -d '{"title":"Perceptea","content":"# Perceptea\n\nA vision pipeline.","loaded_version":4,"purge_history":true}'
```

The response is the saved article plus `revisions_removed` and `activity_entries_retitled`. Without `loaded_version` the request is refused with `400`; `purge_history` on `POST /api/articles` (a create) is also a `400`, because a new document has no earlier revisions.

---

## 🔎 Auditing the whole wiki with `include_history`

`search_wiki` normally searches the Bleve index, which holds only the **current** text of each document. `include_history: true` adds a second, exhaustive pass:

* It reads **every** history snapshot and **every** live article file — so a document written before version history existed is covered too.
* It matches the query as a **case-insensitive literal substring** over the whole serialized file, front matter included. No Bleve syntax is interpreted; one pair of surrounding double quotes is stripped, so `"Acme Corp"` looks for `Acme Corp`.
* The `type`, `tag`, `memory_kind`, and archived filters **do not narrow it**. An audit that quietly skipped part of the corpus would report "not found" for text that is still there. The response says so.

The ordinary Bleve results are still returned alongside it. A term the Bleve query parser rejects — a regex fragment, an unbalanced quote — still gets the full history scan; the index half is reported as failed. The history matches arrive as `history_matches` in `structuredContent`:

```jsonc
{
  "query": "acme",
  "count": 1,
  "results": [ /* current-text hits, as usual */ ],
  "include_history": true,
  "history_matches": [
    { "slug": "q3-roadmap", "title": "Q3 Roadmap", "version": 2, "current": false },
    { "slug": "q3-roadmap", "title": "Q3 Roadmap", "version": 5, "current": true }
  ],
  "history_match_count": 2
}
```

* `current: false` — the text is only in an earlier revision; `read_article(slug, version)` shows it, and `purge_history` removes it.
* `current: true` — the live document still contains it; rewrite it first.
* `title` is always the document's **current** title, so the report does not repeat a redacted old title.
* At most 500 matches are listed; `history_match_count` is the full total. Narrow the term if it is truncated.

### Practical examples

**Scrub a name from every document that ever mentioned it**

1. `search_wiki(query: "Acme Corp", include_history: true)` — the list of affected `(slug, version)` pairs.
2. For each slug: `read_article`, rewrite the text, `save_article(..., loaded_version, purge_history: true)`.
3. Run step 1 again. An empty `history_matches` means you are done.

**Check a single sensitive string before publishing a bundle**

```jsonc
{ "query": "10.0.4.17", "include_history": true, "limit": 1 }
```

`limit` only caps the Bleve results; the history scan is always complete.

---

## 🏷️ Fixing a document saved with the wrong type

A document created as a `Wiki` that should have been an `AI-Agent-Plan` is invisible to `search_wiki(type: "plans")`, even though it reads perfectly well by slug. Fix it **in place** — never by deleting and recreating it, which throws away its history and backlinks:

```jsonc
// save_article
{
  "slug": "kimmydb-cold-start",
  "title": "KimmyDB Cold Start",
  "content": "...",
  "loaded_version": 3,
  "type": "AI-Agent-Plan",
  "project_context": "kimmydb",
  "status": "implementing"
}
// → Type: Wiki → AI-Agent-Plan
```

Then confirm with `search_wiki(type: "plans")`, not with a read — the listing is what was wrong.

The rules, which apply equally to `PUT /api/articles/{slug}` with a `"type"` field:

* **Omitting `type` keeps the current type.** An ordinary edit never reclassifies a document.
* `status`, `memory_kind`, `memory_type`, and `project_context` are validated and applied against the type the document is **becoming**.
* Into **`AI-Agent-Plan`** without a `status`: a status already valid for a plan is kept; otherwise the plan enters at `draft`.
* Into **`Wiki`** or **`AI-Agent-Memory`**: the lifecycle status is dropped (those types have none) unless you pass one.
* A plan or skill **status the new type does not accept** (a plan at `implementing` becoming a skill, a skill at `ready` becoming a plan) is refused — pass an explicit `status`. A lifecycle state is never silently converted.
* **Leaving `AI-Agent-Memory`** drops the `memory-<scope>` tags and the `memory_kind`.
* An **unknown type** is an error naming the valid values — never a silent `Wiki`. The same holds for `search_wiki(type)`, `POST /api/articles`, and OKF bundle imports over an existing slug.

Type changes are ordinary revisions: the previous type stays in history, and you can purge it like any other text.
