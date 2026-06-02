# NexWiki Advanced Editor, Linter, Scheduling & SSE Activity Guide 🚀

Welcome to the advanced guide for NexWiki's unified editor and real-time syncing features. This guide covers how to use the high-fidelity CodeMirror 6 Markdown editor, seasonal theme scheduling, real-time syntax linter, quick cheatsheet panel, and live activity streaming.

---

## 🎨 1. Seasonal Theme Scheduling

NexWiki automatically schedules seasonal or holiday themes throughout the year based on custom or built-in scheduling rules.

### Built-in Seasonal Themes
NexWiki ships with four default annual holiday schedules:
* **July 4th (`july-4th`)**: Jun 28 – Jul 6 (Contextual badge: 🎆)
* **Halloween (`halloween`)**: Oct 15 – Nov 1 (Contextual badge: 🎃)
* **Christmas (`christmas`)**: Dec 1 – Dec 25 (Contextual badge: 🎄)
* **New Year's (`new-years`)**: Dec 26 – Jan 7 (Contextual badge: 📅)

### How to Configure Scheduling
1. **Enable the Scheduler**: Run the backend server with the `-theme-scheduling` flag:
   ```bash
   ./nexwiki -theme-scheduling=true
   ```
   Or set the environment variable:
   ```bash
   export NEXWIKI_THEME_SCHEDULING=true
   ```
2. **Schedule Custom Themes**:
   * Open the **Theme Manager Modal** (Palette icon in Sidebar).
   * Click **Create Custom Theme** or edit an existing one.
   * Configure the annual **Start Month/Day** and **End Month/Day** (e.g., Start: `10/15`, End: `11/01`).
   * Save the theme. When the system calendar falls within this range, it will automatically adopt this theme.
3. **Boundary overlaps**: If today's date falls under overlapping seasonal themes (e.g., Dec 26 overlap of Christmas and New Year's), NexWiki hashes the date string to deterministically select a single theme that stays stable for the entire day, avoiding layout flicker.

---

## 💻 2. CodeMirror 6 Markdown Editor

The composition interface has been upgraded to a high-fidelity **CodeMirror 6** editor, offering standard IDE capabilities directly inside your browser.

### Premium Capabilities
* **Option B Dynamic Colors**: The editor composing pane dynamically wraps active Manual, Custom, or Scheduled themes using CSS custom properties natively, updating colors instantly when themes toggle.
* **Toolbar Transactions**: Formatting buttons (Bold, Italic, Code, Headings, Link, WikiLink, Image Upload) execute clean CodeMirror transactions, preserving history stack undo/redo operations.
* **Auto-resizing & Scroll**: Composing text takes exactly the available vertical height with line numbers and internal scrollbars, avoiding clumsy outer scrolling.
* **Cheatsheet panel**: Pressing `Ctrl+/` (Windows/Linux) or `Cmd+/` (macOS) toggles a glassmorphic **Markdown Syntax Guide** overlaying the editor, providing formatted syntax cheat cards.

---

## 🔍 3. Real-Time Markdown Linter & Warnings Dashboard

To guarantee structural health and pristine formatting, NexWiki executes debounced lint checking as you type.

### Active Validation Rules
* **MD001 (Heading Sequence)**: Heading level should only increase by one level at a time (e.g., warning on H1 jumping directly to H3).
* **MD025 (Multiple H1s)**: Errors on multiple top-level H1 headers (only one `# Title` per article is recommended).
* **MD037 (Surrounding Spaces)**: Warns on spaces right inside bold/italic triggers (e.g., `** text **` instead of `**text**`).
* **MD034 (Bare URLs)**: Advises wrapping plain links in angle brackets (e.g. `<http://example.com>`).
* **WIKILINK_BROKEN**: Warns on double-bracketed `[[WikiLink]]` tags targeting slugs that do not exist yet in the database.

### Inline Diagnostics & Context Menus
* **Wavy Underlines**: Issues draw wavy glows (red for errors, amber for warnings, indigo for info). Hovering over a span displays details and click-to-apply quick-fixes.
* **Custom Context Menu**: Right-clicking a wavy underline span opens a custom glassmorphic context menu containing `"Fix: [suggestion]"` and `"Show in Error Panel"`.
* **Toolbar Count Badges**: The toolbar displays an active counter of errors (`E`) and warnings (`W`).
* **Errors Dashboard**: Clicking the toolbar counter opens the `MarkdownLintErrorModal` allowing you to:
  - Sort issues by line number or severity.
  - Filter by Errors, Warnings, or Info.
  - Copy all diagnostics text.
  - Copy a formatted **AI Correction Prompt** containing your active draft and list of errors, ready to paste into Cursor Composer or Claude Desktop!
  - Click any diagnostic row to automatically center and highlight that specific warning inside the CodeMirror editor.

---

## 📡 4. Real-Time SSE Activity drawer

NexWiki drives instant multi-client syncing and live logs through a single persistent **Server-Sent Events (SSE)** connection.

### Circular EventBus Cache
* The backend circular EventBus caches the last 200 activity operations (mutations by REST APIs vs. AI agent tool executions).
* Establishes a stream endpoint `/api/activity/stream` piping live payloads.

### UI Features
* **Activity Log Drawer**: Opens via the "Activity" button in the Sidebar. Displays categorized action chips (purple for MCP AI tools, slate for REST APIs) and time stamps.
* **Cumulative Unread Badge**: If an AI agent executes rapid tool operations in the background (e.g., Claude Desktop executing consecutive writes), the sidebar notification badge buffers updates over a **500 ms cumulative cooldown window** to show a single glowing increment (e.g. `+5` or `+12`), avoiding rapid visual layout noise.
* **Zero-Refresh Synchronization**: When a mutation is made (by you, another browser tab, or an MCP agent):
  - The Sidebar listing and Hero counts update immediately.
  - If you are viewing the mutated article, its content refreshes instantly in the viewer with zero-page refresh!

---

## 📤 5. Advanced Bulk Exporter

In addition to sharing page URLs, copying raw Markdown, and exporting to PDF, Word, and text, NexWiki supports **Bulk Archive Exporter**:
* Click the **"Download All Content (.zip)"** button at the Sidebar footer.
* NexWiki packages your entire wiki database, grouping notes into category-specific folders:
  - `wiki/` (Standard wiki notes)
  - `aimemories/` (Isolated AI memories)
  - `aiplans/` (Collaborative plans)
  - `aiskills/` (Custom AI skills)
* Inserts a professional, styled `README.md` index file at the root.
* Automatically downloads the archive as `nexwiki-export-YYYY-MM-DD.zip`.
