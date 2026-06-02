# NexWiki User & Content Creation Guide 📖

Welcome to **NexWiki**! This guide is designed to help you get started with creating, formatting, and linking documentation in your wiki. NexWiki is built from the ground up to support highly connected, media-rich flat-file Markdown knowledge bases with a gorgeous, fluid interface.

---

## 📂 Managing Wiki Articles

NexWiki is powered by flat Markdown files stored on the server. The interface makes managing these files simple and dynamic.

### 1. Creating Articles
To start a new document, you have two primary options:
* **The "New Page" Button**: Click the **New Page** button (or the `+` icon) in the sidebar or header. This opens a fresh editor screen.
* **The Wiki Link Creator (Broken Links)**: Wikilinks targeting non-existent pages render as a **red dotted broken link** (e.g. `[[My Future Page]]`). Clicking a broken link instantly opens the editor with the title already pre-filled so you can write it immediately!

#### Title & Clean Slugs
When typing your **Article Title** in the editor:
- NexWiki automatically generates a clean, URL-safe path slug (e.g., typing `Getting Started Guide 🚀` generates a `/articles/getting-started-guide` slug).
- The slug is displayed underneath the title input. Standard Markdown files are named according to this slug (e.g. `getting-started-guide.md`).

---

### 2. Editing Articles
When viewing an active article, click the **Edit** (pencil icon) button in the page header to edit it.

#### Editor View Modes
In the top-right corner of the editor, you can switch between three tailored layouts:
1. **Split View (Side-by-Side)** *(Recommended)*: Displays the raw Markdown editor on the left and a live-updating preview on the right. This lets you see exactly how your content renders in real-time.
2. **Edit Mode**: A full-screen, focused distraction-free code editor.
3. **Preview Mode**: A full-screen preview of the compiled Markdown document.

#### Formatter Toolbar
Use the top toolbar for rapid text styling:
- **Heading Styles**: Insert H1 (`#`), H2 (`##`), or H3 (`###`) markers.
- **Text Styles**: Apply Bold (`**bold**`), Italic (`*italic*`), or Inline Code (`` `code` ``).
- **Linking**: Insert standard web links or double-bracket WikiLinks.
- **Media Upload**: Upload and embed images directly at the cursor.

Click **Save Page** in the top right to commit your changes to disk, or **Cancel** to discard edits.

---

### 3. Deleting Articles
If an article is no longer needed:
1. Open the article you wish to delete.
2. Click the compact **Delete** (rose-tinted trash bin icon) button in the page header.
3. Confirm the prompt to delete the page.
*Note: This permanently deletes the Markdown file and its media folder from the server, and automatically de-indexes it from full-text search.*

---

### 4. Exporting, Sharing, and Copying Articles
To easily distribute and work with your wiki articles, NexWiki provides an elegant, glassmorphic **"Share & Export"** dropdown in the article header. This combines clipboard operations and multi-format document exporting into a single, compact workspace tool:

#### 📋 Clipboard Utilities
* **Copy Markdown**: Copies the raw, frontmatter-free Markdown body content to your clipboard. All YAML metadata and page header templates are automatically stripped out, leaving only the clean document body text.
* **Copy Share Link**: Instantly copies the current URL of the browser tab so you can share it with others.
*Visual alerts will confirm when items are copied successfully.*

#### 📄 Multi-Format Document Exporters
* **Export as PDF**: Converts the article into a professional, vector-drawn, high-fidelity PDF. NexWiki uses custom print-styled stylesheets (`@media print`) that automatically hide the sidebars, header buttons, TOC columns, and alert messages, leaving a clean, beautifully formatted, publication-ready document layout. It triggers your browser's print dialog, letting you choose "Save as PDF" and choose the exact destination.
* **Export as Word**: Compiles the rendered article HTML into a standard, CSS-styled Microsoft Word template block. Opening the downloaded `.docx` file in Microsoft Word or Apple Pages allows you to read and fully edit the formatted document.
* **Export as Markdown**: Instantly downloads a clean, formatted Markdown `.md` file containing the raw Markdown body text.

#### 📂 File System Save Selection
To give you complete control over your filesystem:
* NexWiki utilizes the modern browser **File System Access API (`showSaveFilePicker`)** where supported (Chrome, Edge, Safari, and Opera on macOS/Windows). 
* When exporting `.docx` or `.md` files, this triggers a **native macOS / Windows "Save As" file dialog**, letting you name the file and select the exact folder on your local filesystem to save it to.
* In unsupported browsers (such as Firefox) or non-secure contexts, it gracefully falls back to a standard browser download trigger that places the file in your default Downloads folder.

---

## 🔗 Linking Between Wiki Articles

A strong wiki is defined by how well its pages connect to one another. NexWiki supports two separate linking styles.

### 1. Internal WikiLinks (Double Brackets)
WikiLinks allow you to link articles together simply by referencing their titles inside double square brackets.

* **Standard WikiLink**: Use the title of the target article.
  ```markdown
  Refer to [[Formatting Guide]] for advanced syntax.
  ```
  *Renders as:* Refer to [Formatting Guide](/articles/formatting-guide) (dynamically Slugified to direct to the `formatting-guide` page).

* **Custom Display Text (Piped WikiLink)**: If you want to customize the clickable text while pointing to a different page, use a vertical pipe `|`:
  ```markdown
  Read our [[setup-guide|Detailed Setup Instructions]].
  ```
  *Renders as:* Read our [Detailed Setup Instructions](/articles/setup-guide).

#### 🔗 Smart Link Resolution (Broken Links)
NexWiki keeps track of all pages. If you add a WikiLink to a page that **does not exist yet**:
- It will render as a red dotted link with a question mark.
- Clicking the link doesn't break the app; instead, it automatically opens the creation screen with that title pre-filled.
- This allows you to plan your documentation structure ahead of time and fill in pages as you go!

---

### 2. External Web Links
For standard external websites, use traditional Markdown link syntax:
```markdown
For more information, visit the [Go Language Homepage](https://go.dev).
```
*Note: External links automatically render with standard secure headers and open in a new browser tab so users don't lose their place in the wiki.*

---

## 🖼️ Uploading and Embedding Media Resources

NexWiki makes it incredibly easy to attach and display media assets directly within your Markdown files.

### Supported Image Formats
- JPEG (`.jpg`, `.jpeg`)
- PNG (`.png`)
- GIF (`.gif`)
- WEBP (`.webp`)
- SVG (`.svg`)

### How to Upload Media
You can upload images in two fast, native ways while editing:

* **Option A: Drag & Drop (Easiest)**
  Select an image file on your computer, drag it over the editor textarea, and drop it. It will automatically upload in the background.
* **Option B: Toolbar File Picker**
  Click the **Image** icon in the formatting toolbar. A native file selection window will open, letting you browse and pick an image.

### How Images are Embedded
Upon upload, NexWiki saves the file securely under the article's specific asset directory and inserts the correct Markdown reference at your cursor position:

```markdown
![My Uploaded Image](/api/assets/article-slug/image-filename.png)
```

You can customize the alternative description text inside the leading brackets `![Alt Text]` to ensure web accessibility.

---

## 📝 Markdown Cheat Sheet

NexWiki fully supports **GitHub Flavored Markdown (GFM)**. Here is a quick reference table of common formatting syntaxes:

| Element | Markdown Syntax |
| :--- | :--- |
| **Header 1** | `# Title 1` |
| **Header 2** | `## Title 2` |
| **Header 3** | `### Title 3` |
| **Bold** | `**Bold Text**` |
| **Italics** | `*Italic Text*` |
| **Checklist** | `- [ ] Unchecked Task`<br>`- [x] Completed Task` |
| **Bullet List** | `- Item A`<br>`- Item B` |
| **Numbered List**| `1. First Item`<br>`2. Second Item` |
| **Blockquote** | `> This is a quote` |
| **Code Block** | \`\`\`go<br>fmt.Println("Hello World!")<br>\`\`\` |
| **Horizontal Rule**| `---` |
