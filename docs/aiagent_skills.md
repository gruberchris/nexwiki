# 🛠️ AI Agent Skills & Custom Registry User Guide

NexWiki serves as a **dynamic AI skills registry** that allows you to author procedural instructions, templates, and rules (commonly referred to as `SKILL.md` documents) and expose them directly to AI agents.

This guide details what AI skills are, how to manage them inside NexWiki, how they interact with search, and how to connect your AI agents (such as JetBrains AI Assistant, custom agents, or Claude Code) to NexWiki's custom skills registry.

---

## 💡 What is an AI Agent Skill?

In the AI ecosystem, **Skills** are reusable packages of specialized logic, conventions, and instructions that tell an AI agent how to perform a task. They represent your **procedural knowledge**, whereas articles represent **declarative knowledge** and AI Memories represent the **AI's state/logs**.

A typical skill folder in standard registries (like the `JetBrains/skills` repository) contains:
1. **`SKILL.md`**: Standardized Markdown file featuring YAML frontmatter metadata (`name`, `description`, `tags`) and a Markdown body detailing the agent's procedural directives.
2. **References & Scripts (Optional)**: Accompanying scripts or reference files.

NexWiki replicates this structure seamlessly. By tagging any wiki page with `aiagent-skill`, that article is instantly compiled into a registry-ready skill that AI agents can consume via REST APIs.

---

## 🎨 Managing AI Skills in the UI

NexWiki makes creating and managing AI Skills extremely easy:

### 1. Registering a Skill
To turn any standard wiki article into an AI skill:
- Click **Edit Page** (or click **New Wiki Page** to start fresh).
- In the Tag Editor section (at the top, under the title), toggle the checkbox **Register as AI Skill**.
- NexWiki will automatically append the `aiagent-skill` tag to your article.
- Add additional tags (like `git`, `docker`, or `javascript`) and write your instructions in the editor.
- Click **Save Article**.

### 2. Collapsible Sidebar Folder
Once saved, skill pages are instantly moved out of your main **Articles** section and grouped under the dedicated **🛠️ AI skills** collapsible sidebar folder. This keeps your standard personal notes and wiki index clean.

### 3. Glassmorphic Registry Dashboard
When viewing any page registered as an AI skill, NexWiki renders a beautiful, glassmorphic **AI Agent Skill Active** banner right above your markdown content.
* This banner confirms that the page is currently being served on your local custom skills registry.
* It provides direct click-to-open links to inspect the **JSON Schema** metadata or view the **Raw SKILL.md** representation served to agents.

---

## 🔍 How AI Skills Interact with Search

To keep your standard wiki searches focused, **AI skills do not appear in standard wiki search results by default.** They are filtered out of quick search just like AI Memories.

However, NexWiki includes a smart **Explicit Search Bypass** rule:
If you are explicitly looking for a skill, it will appear in your search results *only* if:
1. Your search query contains `"skill"` or `"aiagent-skill"`.
2. Your search query matches the skill's **title** or **slug** (e.g. `docker-clean`).
3. Your search query matches one of the skill's **associated tags** exactly (e.g. searching for the `git` tag will show your git skills).

---

## 🔌 Using NexWiki as an AI Skills Registry

NexWiki registers three lightweight REST API endpoints, allowing any AI agent, CLI tool, or custom LLM client to pull and consume your skills.

### 1. Registry Index
* **Endpoint**: `GET /api/skills`
* **Response**: Returns a JSON array of all registered skills, parsed descriptions (extracted from the first paragraph of your page), and dynamic raw URLs.
```json
[
  {
    "name": "docker-cleanup",
    "title": "Docker Container Cleanup Guide",
    "description": "This skill instructs the agent on how to safely prune unused Docker containers, images, and volumes while safeguarding running environments.",
    "tags": ["docker", "devops"],
    "version": 2,
    "raw_url": "http://localhost:8080/api/skills/docker-cleanup/raw",
    "updated_at": "2026-06-01T00:36:25-04:00"
  }
]
```

### 2. Individual Skill Details
* **Endpoint**: `GET /api/skills/{slug}`
* **Response**: Returns the JSON metadata representation of a single skill.

### 3. Raw SKILL.md Content
* **Endpoint**: `GET /api/skills/{slug}/raw`
* **Response**: Serves the raw physical Markdown file (YAML frontmatter + Markdown body) directly as plain text (`text/plain`). This corresponds exactly to the `SKILL.md` file format that AI agents require.

---

## 🚀 Practical Example: Creating a Git Skill

Here is a practical example of a skill that you can write inside NexWiki to guide your AI assistant on how you prefer git commits to be structured.

### 1. Frontmatter and Content
Write this in the editor and check **Register as AI Skill**:

```markdown
# Git Commit Standardizer

## Overview
Guides the agent on how to construct clean, meaningful git commits according to the Conventional Commits specification.

## When to Use
Trigger this skill whenever the user asks you to write a commit message, commit changes, or prepare a pull request.

## Instructions
1. Format all commit messages as: `<type>(<scope>): <description>`
2. Allowed types: `feat`, `fix`, `docs`, `style`, `refactor`, `perf`, `test`, `chore`.
3. Keep the subject line under 50 characters.
4. Use the imperative, present tense (e.g., "add feature" instead of "added feature").

## Example Output
```text
feat(auth): add google oauth2 login provider
```
```

### 2. Consume in JetBrains AI Assistant or Claude Code
In editors supporting custom registries, or custom Python scripts, you can direct your client to query `/api/skills` to discover available skills, and fetch their raw rules from `/api/skills/<slug>/raw` to inject into the system prompt!
