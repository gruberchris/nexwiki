# NexWiki Article Versioning & Revision Guide 🕒

Welcome to **NexWiki Version Control**! NexWiki includes a built-in, lightning-fast **Gzipped Flat-File Versioning System** designed to track all edits, audit modifications, and revert changes effortlessly—all while keeping your storage footprint extremely lightweight.

This guide explains how versioning operates, how to review document differences (diffs), how to perform reverts, and how the system protects your work from multi-user edit collisions.

---

## 📂 How Article Versioning Works

Every time you create or modify a wiki article, NexWiki automatically captures a snapshot of the document’s complete state (its metadata, title, and Markdown content).

1. **Initial Creation (v1)**: When you create a new page, it is saved as **Version 1** with the default comment `"Initial version"`.
2. **Subsequent Revisions (v2, v3, etc.)**: Each time you modify and save the document, the server increments the version number sequentially.
3. **Storage Efficiency (Gzip)**: To prevent disk bloat, historical snapshots are stored compressed as `.md.gz` gzip files under `data/history/<slug>/<version>.md.gz`. Text compresses by up to **80%**, keeping your entire history timeline miniscule (typically under 10–30 MB for hundreds of page edits).
4. **Assets Are Preserved**: Uploaded media (images, files) are **not** duplicated per revision. They are stored in your article's central assets folder, and historical markdowns simply link back to them, preventing duplicate storage of heavy media files.

---

## 📜 Auditing Article History (The Timeline)

To examine all changes made to a wiki article over time:

1. Open the article you want to audit.
2. Click the **History** (clock icon) button in the page actions bar next to **Edit Page**.
3. A premium **Revision Timeline** drawer slides open from the right side of the screen.

### Inside the Timeline Drawer:
- **Chronological List**: Displays all versions of the document, sorted with the newest edits at the top.
- **Active Version**: The current published version of the article is clearly badged with a green **Active** label.
- **Edit Auditing**: Each entry shows the version number, the precise date and time of modification, and the **Edit Summary** cataloging what was changed.

---

## 🔍 Inspecting Article Differences (Diff View)

You can visually compare any historical version directly against the current live version of the document.

1. Open the **History** timeline.
2. Click the **Inspect** (eye icon) button next to any historical version.
3. The drawer transitions to the **Comparison Diffs** view, loading the historical file and contrasting it line-by-line against the active page.

### Layout Formats:
NexWiki provides a layout control switcher in the top right of the diff pane to customize your view:
* **Split Pane (Side-by-Side)** *(Default)*: Splits the screen into two columns. The left column shows the historical version, and the right column shows the active version. Highly recommended for structural revisions, renaming headers, or comparing block content.
* **Unified Inline**: Combines both versions into a single list. Deletions and additions are blended in sequence, mimicking classic git inline logs. Highly recommended for reading prose, stories, and flowing paragraphs.

### Color-Coded Diff Highlights:
*   🔴 **Deletions (Old Version)**: Render with a soft rose-pink background, prefixed by a red minus sign (`-`) in the columns.
*   🟢 **Insertions (New Version)**: Render with a soft emerald-green background, prefixed by a green plus sign (`+`) in the columns.
*   ⚪ **Unchanged Lines**: Render with standard dark text, prefixed by an empty space, indicating identical content.

---

## ↩️ Reverting to Prior Versions

If a page has been edited incorrectly, you can roll it back to the exact content of any previous revision.

### How to Revert:
1. Open the **History** timeline of your article.
2. Either click the **Quick Revert** (curved arrow icon) button directly on the timeline card, or click **Inspect** to review the differences first and click the **Revert to this version** button at the bottom of the comparison pane.
3. Confirm the pop-up modal asking if you want to overwrite active content.

### ⚠️ Revert Rules & Safeguards:
*   **Slug Integrity**: Reverting an article **preserves the current active URL slug**. Even if you roll back to a version that had a completely different title, the URL route (e.g. `/articles/my-active-page`) remains unchanged. This guarantees that your internal WikiLinks, external bookmarks, and routing maps **never break**.
*   **The Revert Commit**: Reverting does not delete history. Instead, the server takes the old content and saves it as a **brand new incremented version** (e.g., if you have 4 versions, reverting to `v2` creates `v5`). The system assigns an automatic comment: `"Reverted to version 2"`. This ensures you can easily roll back the revert itself if you change your mind!

---

## 🛡️ Edit Conflict Protection (Optimistic Locking)

To prevent multiple users (or browser tabs) from accidentally overwriting and erasing each other's work:

1. **Session Locks**: When you open the editor, NexWiki silently registers the active version number you loaded (e.g., version `3`).
2. **Conflict Detection**: When you click **Save Page**, the server checks if the document has been modified on disk since you loaded it.
3. **Safeguard Block**: If another session saved a new revision (making the active file version `4`), the server blocks your edit and returns a conflict notice:
   > ⚠️ **Conflict Detected**
   > *This article has been updated in another session. Please copy your edits, reload the page, and try again.*
4. **Resolution**: This prevents silent data loss, allowing you to copy your edits, reload to review the other user's changes in history, and merge your changes safely.
