# NexWiki Rich Text Sharing & Clipboard Interoperability Guide 📋

NexWiki is designed as an AI-ready personal and team knowledge base that bridges Markdown-based documentation with enterprise workplace collaboration. While Markdown is the ideal format for developer velocity, version control, and AI agent interoperability, modern corporate communication takes place primarily in rich-text WYSIWYG applications.

The **Copy as Rich Text** feature provides a one-click clipboard utility in the **Share & Export** dropdown menu of every article. It bridges the gap between your flat-file Markdown wiki and tools like Microsoft Teams, Microsoft Outlook, Slack, Microsoft Word, and Google Docs by writing formatted HTML and plain text simultaneously to the system clipboard.

---

## ⚡ The Core Problem: Markdown vs. WYSIWYG Composers

Markdown documents rely on plain-text syntax markers:
* `# Header 1` and `## Header 2` for hierarchical headings
* `**bold text**` and `*italic text*` for emphasis
* `[link text](https://...)` for hyperlinks
* `- [ ] Task 1` and `- [x] Task 2` for checklist items
* `| Col A | Col B |` for structured data tables
* ```` ```lang ... ``` ```` for fenced code blocks

When you copy raw Markdown text into a developer code editor or a GitHub issue, the application understands the syntax or displays the plain text cleanly. However, enterprise message composers and office suites operate on **WYSIWYG (What You See Is What You Get)** DOM engines (`contenteditable` containers):

| Destination Application | Result of Pasting Raw Markdown | Result of Pasting Rich Text |
| :--- | :--- | :--- |
| **Microsoft Teams** (Chat / Channels) | Shows raw `#` hashes, `**` asterisks, and broken table pipes | Renders formatted headings, styled tables, clickable links, and bullet lists |
| **Microsoft Outlook** (Desktop & Web) | Unparsed plain text; recipients see raw markdown tags | Full visual HTML email layout with native typography and styled data tables |
| **Slack** (Rich Text Composer) | Unparsed syntax symbols or requires manual backslash escaping | Formatted bold, italic, blockquotes, code spans, and hyperlinks |
| **Microsoft Word** | Flat monospace or unstyled text; tables remain raw pipes | Native Word headings (H1–H6), styled tables, and formatted lists |
| **Google Docs** | Raw text requiring manual find-and-replace or reformatting | Cleanly pasted document structure preserving typography and layout |

Without native rich-text clipboard support, users are forced to manually reformat their text after pasting, screenshot their wiki pages, or export standalone `.docx` files for quick communications.

---

## 🛠️ How NexWiki Solves It: Dual-Payload Clipboard Architecture

NexWiki implements a modern **dual-payload clipboard architecture** that writes two representations of your article to the clipboard in a single operation:
1. `text/html`: The compiled, rendered DOM structure of the article, containing styled HTML elements (`<h1>`-`<h6>`, `<table>`, `<ul>`, `<ol>`, `<code>`, `<pre>`, `<blockquote>`, `<a>`).
2. `text/plain`: Clean, human-readable text content without raw markdown syntax noise.

```mermaid
flowchart TD
    User([User clicks 'Copy as Rich Text']) --> Trigger[useArticleActions Hook]
    Trigger --> DOM[Extract rendered HTML & text from .wiki-content]
    DOM --> Detect{navigator.clipboard.write & ClipboardItem supported?}
    Detect -->|Yes (Modern Browsers)| AsyncClip[Construct ClipboardItem with text/html & text/plain Blobs]
    AsyncClip --> SysClip[System Clipboard]
    Detect -->|No (Legacy / Fallback)| ExecFallback[document.addEventListener copy + execCommand copy]
    ExecFallback --> SysClip
    SysClip --> PasteApp{Target Application Paste}
    PasteApp -->|Teams, Outlook, Word, Slack| ParseHTML[Reads text/html: Native WYSIWYG Rendering]
    PasteApp -->|Terminal, Notepad| ParsePlain[Reads text/plain: Clean Text Fallback]
```

### 1. The Modern Asynchronous Clipboard API (`ClipboardItem`)
When your browser supports the modern W3C Async Clipboard API, NexWiki creates two `Blob` objects and writes them to the clipboard via `navigator.clipboard.write`:

```typescript
const item = new ClipboardItem({
  'text/html': new Blob([html], { type: 'text/html' }),
  'text/plain': new Blob([plainText], { type: 'text/plain' }),
});
await navigator.clipboard.write([item]);
```

Because both MIME types are registered together:
- When you paste into a rich-text composer (such as Microsoft Teams or Outlook), the operating system and target application negotiate the paste payload, select `text/html`, and parse the formatting natively.
- When you paste into a plain text editor (such as VS Code, a terminal, or Notepad), the application requests `text/plain` and receives clean, unformatted text without any HTML tags.

### 2. Live DOM Content Extraction
Instead of re-running a standalone markdown parser in JavaScript, NexWiki extracts the already-rendered DOM directly from the active `.wiki-content` view element. This ensures that:
- Code syntax highlights and code block structures are faithfully retained.
- Custom table styling, borders, and alignments match what you see on the screen.
- Task checklists and blockquotes are properly converted to standard HTML list and quote elements.
- Relative links and clean anchors are preserved.
- If the viewer container is not available (such as in programmatic or background states), the utility cleanly falls back to the article's raw content.

### 3. Graceful Legacy Fallback
For environments where `window.ClipboardItem` is unavailable (e.g., older browser versions or specific webview wrappers), NexWiki registers a one-time synthetic `copy` event listener that populates `e.clipboardData.setData('text/html', ...)` and `e.clipboardData.setData('text/plain', ...)` before triggering `document.execCommand('copy')`. The listener is always cleaned up immediately in a `finally` block to prevent memory leaks or side effects.

---

## 📖 Step-by-Step Practical Guides

### 1. Sharing into Microsoft Teams 💬

Microsoft Teams is one of the most common destinations for status updates, runbooks, sprint summaries, and incident reports.

#### Channel Conversations & Chat Posts
1. Open the desired article in NexWiki (e.g., `Sprint 42 Retrospective` or `Deployment Playbook`).
2. In the article header action bar, click the **Share & Export** button (paper plane icon) to reveal the dropdown menu.
3. Click **Copy as Rich Text** (clipboard icon).
   - The dropdown closes automatically.
   - The button icon changes to an emerald checkmark, and a confirmation toast appears: *"Rich text copied to clipboard! Ready to paste into Teams, Word, or Outlook."*
4. Open **Microsoft Teams** and select your target channel or 1:1 chat conversation.
5. In the message compose box, press `Ctrl+V` (Windows/Linux) or `Cmd+V` (macOS).
6. **Result**:
   - Section headings (`##`, `###`) appear as bold, styled Teams heading blocks.
   - Tables appear as fully interactive Teams grid tables with distinct header styling.
   - Bullet lists and numbered lists appear as native Teams lists.
   - Inline code snippets and multiline code blocks appear in shaded code containers.
   - Links remain clickable and retain their titles.

> [!TIP]
> In Microsoft Teams channel posts, click the **Format** button (letter **A** with a pen icon) before pasting if you want to add an explicit conversation Subject line above your pasted wiki content.

---

### 2. Sharing into Microsoft Outlook & Email Clients ✉️

Emailing meeting notes, executive summaries, or customer architecture proposals usually requires a polished, corporate appearance without raw symbols.

#### Outlook Desktop & Outlook Web (OWA)
1. In NexWiki, navigate to your article and choose **Share & Export** → **Copy as Rich Text**.
2. Open **Microsoft Outlook** (desktop client or Outlook on the web) and click **New Email**.
3. Place your cursor in the email message body.
4. Press `Ctrl+V` / `Cmd+V` to paste.
5. **Result**:
   - The email body renders the article's typography, headings, tables, and callouts with inline styling compatible with Outlook's Word-based email rendering engine.
   - Hyperlinks automatically configure with appropriate link text.
   - Recipients on mobile devices (Outlook for iOS / Android) receive a cleanly formatted, responsive email body.

> [!NOTE]
> Other modern email clients—including Apple Mail, Gmail (web), Thunderbird, and Fastmail—fully support the `text/html` clipboard payload and paste identically.

---

### 3. Sharing into Slack 💼

Slack's message input supports both rich-text and markdown formatting modes.

1. In NexWiki, click **Share & Export** → **Copy as Rich Text**.
2. Switch to **Slack** and focus on the message composer in your chosen channel or direct message.
3. Paste using `Ctrl+V` / `Cmd+V`.
4. **Result**:
   - Headings, bold text, italics, lists, and links are immediately translated into Slack's rich-text blocks.
   - You avoid the issue where Slack's WYSIWYG editor accidentally escapes raw markdown asterisks with backslashes (`\*\*text\*\*`).

---

### 4. Sharing into Microsoft Word & Google Docs 📄

When authoring formal proposals, audit deliverables, or customer-facing whitepapers, starting from an existing NexWiki page is a significant time saver.

#### Microsoft Word
1. Copy the formatted article with **Copy as Rich Text**.
2. Open a blank or templated document in **Microsoft Word**.
3. Press `Ctrl+V` / `Cmd+V`.
4. Word maps the HTML heading levels directly to Word heading styles (`Heading 1`, `Heading 2`), enabling an instant Navigation Pane outline and auto-generated Table of Contents in Word.

#### Google Docs
1. Copy the formatted article with **Copy as Rich Text**.
2. Open a document in **Google Docs**.
3. Press `Ctrl+V` / `Cmd+V`.
4. Headings, bullet points, checklists, and tables paste seamlessly with font hierarchy intact.

---

## ⚖️ "Copy Markdown" vs. "Copy as Rich Text"

NexWiki provides both clipboard options in the **Share & Export** dropdown. Use this matrix to choose the best option for your task:

| Scenario / Target Tool | Recommended Option | Why? |
| :--- | :--- | :--- |
| **Microsoft Teams, Outlook, Word, Slack** | **Copy as Rich Text** | Delivers rendered typography, styled tables, and formatted lists without requiring markdown previewers. |
| **Google Docs, Apple Pages, Confluence WYSIWYG** | **Copy as Rich Text** | Retains visual formatting and heading hierarchies directly into document editors. |
| **GitHub / GitLab Issues & Pull Requests** | **Copy Markdown** | Code repositories render raw Markdown natively and preserve commit/issue tags. |
| **Source Code / IDEs** (VS Code, Cursor, GoLand) | **Copy Markdown** | Ideal for copying raw notes into repo `.md` files or code comments. |
| **AI Prompts & LLM Chat Windows** | **Copy Markdown** | LLMs read and process structured Markdown syntax efficiently with lower token overhead. |
| **Plain Text Terminals & Scratchpads** | Either | "Copy as Rich Text" automatically supplies plain text to terminal emulators, while "Copy Markdown" includes structural syntax tags. |

---

## 🔍 Troubleshooting & Browser Compatibility

### 1. Browser Support Matrix

The asynchronous Clipboard API with dual MIME types (`ClipboardItem`) is supported across all major modern evergreen browsers:

| Browser | `ClipboardItem` Support | Behavior in NexWiki |
| :--- | :---: | :--- |
| **Google Chrome** (v66+) | ✅ Full | Uses native `ClipboardItem` async API |
| **Microsoft Edge** (v79+) | ✅ Full | Uses native `ClipboardItem` async API |
| **Mozilla Firefox** (v87+) | ✅ Full | Uses native `ClipboardItem` async API |
| **Apple Safari** (v13.1+) | ✅ Full | Uses native `ClipboardItem` async API |
| **Brave / Opera / Vivaldi** | ✅ Full | Uses native `ClipboardItem` async API |
| **Legacy WebViews / Headless** | ⚠️ Fallback | Uses `document.execCommand('copy')` fallback |

### 2. Secure Contexts (HTTPS & Localhost)
The browser W3C Clipboard specification mandates that `navigator.clipboard.write` can only be invoked in **Secure Contexts** (`window.isSecureContext === true`):
* **Local Development**: `http://localhost:5808` or `http://127.0.0.1:5808` is treated as inherently secure by Chromium, Firefox, and Safari.
* **Production Deployments**: When hosting NexWiki over a network or reverse proxy (Caddy, Nginx, Traefik), you **must serve NexWiki over HTTPS (TLS)**. If accessed over an unencrypted remote HTTP connection (`http://wiki.mycompany.internal`), modern browsers will disable `navigator.clipboard.write`, and NexWiki will utilize the `execCommand` fallback.

> [!IMPORTANT]
> Always configure valid TLS certificates (e.g., via Let's Encrypt or corporate internal PKI) when exposing NexWiki across team networks to guarantee modern clipboard features operate without restrictions. See the [Production Deployment & Reverse Proxy Guide](./production_deployment.md) for complete reverse proxy configurations.

### 3. Clipboard Permissions & Transient User Gestures
Browsers protect clipboard writes to prevent malicious background snooping:
* NexWiki's rich text copy action is executed **directly in response to an explicit user interaction** (clicking the "Copy as Rich Text" button). This satisfies the browser's transient user activation requirement.
* If your browser or enterprise policy enforces strict site-by-site clipboard permissions, ensure that clipboard access is allowed for your NexWiki domain.

### 4. Handling of Embedded Images
When an article contains embedded media uploaded to NexWiki:
* Images reference `/api/assets/{article-slug}/{filename}`.
* When pasting into applications within the same network or local machine where the browser can reach the NexWiki server, images render inline.
* When sharing externally to recipients who cannot access your private NexWiki host, consider attaching the asset directly or exporting a standalone PDF using **Share & Export** → **Export as PDF**.
