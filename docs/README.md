# NexWiki Documentation Hub 📚

Welcome to the NexWiki documentation directory. This folder contains detailed guides, technical descriptions, and manuals to help you master NexWiki's features and understand its architecture.

---

## 📖 Available Guides

Select a guide below to explore specific features:

### 1. [NexWiki User & Content Creation Guide](./user_guide.md)
A comprehensive manual designed to help you create, format, link, share, upload, and export wiki content:
* **Managing Articles**: Creating, editing in different view layouts, and deleting wiki articles.
* **WikiLinks & Linking**: Creating double-bracket internal WikiLinks (`[[WikiLink]]`), custom display tags, and secure external links.
* **Media & Uploads**: Backed by a drag-and-drop file uploader and embedded image assets.
* **Exporting & Sharing**: Sharing page URLs, copying Markdown body text, and exporting articles directly to PDF, Microsoft Word (`.docx`), and plain Text (`.txt`) files using the native File System Access API.

### 2. [NexWiki Article Versioning & Revision Guide](./version_control.md)
An advanced technical and user guide covering the flat-file gzipped backup and conflict prevention systems:
* **Flat-File Gzip backups**: How NexWiki backs up history using lightweight compressed `.md.gz` snapshots on disk.
* **Interactive Difference Panels**: Navigating and reviewing historical revisions in side-by-side **Split Diff** or unified **Inline Diff** views.
* **Instant Reversion**: Restoring past versions of articles safely.
* **Optimistic Locking Guards**: Understanding version numbers and how NexWiki prevents multi-session write collisions.

---

## 🏗️ Architecture Overview

NexWiki integrates documentation directly with active AI pipelines. If you are an AI developer or are connecting an AI agent (like Claude Desktop or Cursor) to NexWiki, refer to the root [AGENTS.md](../AGENTS.md) for specifications on the exposed Model Context Protocol (MCP) server endpoints.
