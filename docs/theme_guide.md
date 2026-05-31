# NexWiki Customizable Themes Engine Guide 🎨

Welcome to the customizable multimode theme engine documentation. NexWiki features a state-of-the-art styling system where every theme includes **both a Light Mode and a Dark Mode variant**. Toggling the light/dark button in the sidebar dynamically switches between the light and dark color profiles of your active theme.

This guide covers:
1. **Configuring Default Themes** via command-line flags and environment variables.
2. **Standard System Themes** and their active modes.
3. **Designing Custom Themes** directly inside the interactive UI using integrated color pickers for Light and Dark modes.
4. **Theme Persistence and Portability** on disk.

---

## 🚀 1. Configuration on Server Startup

You can set the initial, default theme for your NexWiki deployment using command-line startup flags or environment variables. This serves as the initial theme for new browser sessions before users choose their custom preferences in the UI.

### Option A: Command-Line Flags
Use the `-theme` configuration flag when running the compiled Go binary:

```bash
# Boot NexWiki with a custom default theme
./nexwiki -theme default -name "My Personal Brain" -data ./wiki-data
```

### Option B: Environment Variables
Alternatively, you can set the `NEXWIKI_THEME` environment variable (with legacy support for `WIKI_THEME` or `THEME`). These automatically override the command-line flag if present:

```bash
# Set default theme using NEXWIKI_THEME environment variable
export NEXWIKI_THEME="ocean-breeze"
./nexwiki -data ./wiki-data
```

If no configurations are specified, NexWiki defaults to the `"default"` system theme.

---

## 🎨 2. Standard System Themes

NexWiki compiles with five pre-installed system default themes, all configured to render in **Light Mode by default** while fully supporting dual-variant dark modes:

### 1. Default (`"default"`)
The standard premium look and feel of NexWiki:
* **Light Mode**: High-fidelity indigo accents (`#4f46e5`) on a clean slate-50 backdrop.
* **Dark Mode**: High-contrast emerald accents (`#10b981`) on a deep slate-955 backdrop.

### 2. Fourth of July (`"july-4th"`)
A patriotic theme capturing the crimson red, white, and navy blue spirit:
* **Light Mode**: Clean snow-white background with patriotic crimson red (`#b91c1c`) links and bold navy blue (`#1d4ed8`) highlighting.
* **Dark Mode**: Deep navy starry night backdrop (`#0b0f19`) with bright holiday red (`#ef4444`) and blue (`#3b82f6`) accents.

### 3. Halloween (`"halloween"`)
A festive, warm, and spooky theme:
* **Light Mode**: Pumpkin cream canvas (`#fffbeb`) with stone accents, orange pumpkin (`#ea580c`) highlights, and spooky witch purple (`#7c3aed`) indicators.
* **Dark Mode**: Haunted crypt stone black (`#0c0a09`) with glowing neon orange (`#f97316`) and violet (`#a78bfa`) accents.

### 4. Christmas (`"christmas"`)
A comforting, snowy, evergreen festive theme:
* **Light Mode**: Minty snow canvas (`#f0fdf4`) with evergreen pine headers, crimson red (`#b91c1c`) links, and shimmering gold (`#eab308`) highlighting.
* **Dark Mode**: Warm evergreen cabin forest backdrop (`#022c22`) with bright holiday red (`#ef4444`) and gold (`#facc15`) accents.

### 5. New Year's Eve (`"new-years"`)
An elegant, glittering celebration theme:
* **Light Mode**: Warm champagne stone backdrop (`#fafaf9`) with metallic midnight gold (`#ca8a04`) primary elements and celebration silver (`#78716c`) accents.
* **Dark Mode**: Midnight sky black backdrop (`#09090b`) with glistening silver text, sparkling fireworks gold (`#eab308`), and silver (`#a1a1aa`) celebration elements.

---

## 🛠️ 3. Designing Custom Themes in the UI

You can craft and refine customized dual-variant themes directly inside the web browser!

### Accessing the Theme Manager
1. In the sidebar's branding header, click the **Theme Palette Button 🎨** next to the Sun/Moon toggle.
2. This opens the **Wiki Theme Manager Modal**.

### Creating a Theme
1. In the Theme Manager, click the **+ Create Custom Theme** button in the left column.
2. In the designer panel:
   * Enter a **Theme Name** (e.g., `Emerald Forest` or `Solar Eclipse`).
   * Select the **Default Active Mode** (specify whether the Light or Dark variant should display by default when this theme is selected).
3. Select the **☀️ Light Variant Colors** tab and customize the colors:
   * Click the color box to open a visual HTML5 color picker.
   * Customize key tokens like **Primary Background**, **Secondary Background**, **Primary Text**, and **Primary Accent** to your liking.
4. Select the **🌙 Dark Variant Colors** tab and specify the dark-mode color tokens for this theme.
5. Click **Save Custom Theme**.

### Activating and Deleting Themes
* **Activate**: Select any theme in the left list and click **Activate Theme** at the bottom of the details card. If you activate a custom theme, NexWiki immediately switches to its default mode (light or dark) and applies the custom colors.
* **Delete**: Click the small **Trash Icon 🗑️** next to any custom theme in the sidebar list to permanently delete it. *(Predefined system themes cannot be deleted).*

---

## 💾 4. Theme Persistence and Portability

To ensure that your custom designs are fully portable and persistent, NexWiki manages them under its Go storage subsystem:

* **File Storage**: Custom themes are saved to a plain JSON file named `custom_themes.json` inside your designated wiki data directory (e.g. `./data/custom_themes.json`).
* **Session Persistence**: When you select a theme in the browser, your choice is saved to local storage so that reloading the page or bookmarking articles will always display your customized palette.
* **Cross-Device Portability**: Because custom themes are persisted directly inside the data directory, backing up or transferring your wiki folder to another machine or a Docker container will preserve all your custom themes!

### Example: Inside `custom_themes.json`
```json
[
  {
    "name": "Solar Breeze",
    "default_mode": "light",
    "light": {
      "bg_primary": "#fffbeb",
      "bg_secondary": "#ffffff",
      "text_primary": "#78350f",
      "text_secondary": "#92400e",
      "text_muted": "#b45309",
      "border_color": "#fef3c7",
      "accent_primary": "#d97706",
      "accent_secondary": "#b45309",
      "accent_hover": "#92400e",
      "accent_bg": "#fef3c7"
    },
    "dark": {
      "bg_primary": "#1c1917",
      "bg_secondary": "#292524",
      "text_primary": "#fafaf9",
      "text_secondary": "#d6d3d1",
      "text_muted": "#a8a29e",
      "border_color": "#44403c",
      "accent_primary": "#f59e0b",
      "accent_secondary": "#d97706",
      "accent_hover": "#fbbf24",
      "accent_bg": "rgba(217, 119, 6, 0.2)"
    },
    "custom": true
  }
]
```
