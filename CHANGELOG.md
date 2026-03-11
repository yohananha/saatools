# Changelog — Babel Reader

All notable changes to this project are documented here.
Format follows [Keep a Changelog](https://keepachangelog.com/en/1.0.0/).

---

## [Unreleased] — 2026-03-11

### Added
- **Direct-read import** — EPUB can now be imported with "Read as-is (no translation)"; source/target language fields hidden, toggle-language and Fix/Save buttons suppressed in reader.
- **Dual progress bar** — Book cards show two overlaid bars: translation progress (accent colour) and reading progress (green). The longer bar sits behind; the shorter in front.
- **Reading position persistence** — Book always reopens at the last read paragraph.
- **Bookmarks** — Add, list, and delete named bookmarks while reading (`BookmarkAddModal`, `BookmarkListModal`). Stored per-project in `Bookmark` struct.
- **Glossary panel** — Tap-and-hold (or select) a word in the reader to view or add glossary entries.
- **Batch translation** — `TranslateBatch` / `ProofReadBatch` / `TranslateAndProofReadBatch`; configurable batch size (`translationBatchSize = 5`).
- **Fix model toggle** — Settings dropdown to pick between `deepseek-chat` (fast) and `deepseek-reasoner` (thorough) for the Fix button.

### Changed
- **App renamed** from *SaaTool* to **Babel Reader** (AndroidManifest, HTML `<title>`).
- **App icon** replaced with the Babel Reader logo (all five mipmap densities: mdpi→xxxhdpi; adaptive icon background `#0D2153`).
- **Bottom navigation** reduced to two tabs (Library, Settings); theme toggle (☀️/🌙) is now a third equal-flex column so all three items are proportionally centred.
- **Log viewer** moved from its own bottom-nav tab into a collapsible section at the bottom of Settings (collapsed by default).
- **Settings → Save** now navigates back to the Library page and shows a "Settings saved" toast.
- **Theme persistence** — tapping ☀️/🌙 immediately persists `darkMode` to saved settings so the choice survives app restarts.
- **Library grid** switched from flexbox to CSS Grid (`auto-fill, 150px`) with `justify-content: center`; books fill rows left→right and the entire grid block is centred on screen.
- **Book card size** — max width 150 px, portrait 1.6:1 aspect ratio (150 × 240 px).
- **Genre tag normalisation** — tags are trimmed, title-cased, and alias-merged (`sci-fi` / `Sci Fi` / `science fiction` → `Science Fiction`, etc.).
- **Filter refresh** — filter tag lists (`allGenres`, `allStyles`) re-derive from `projects` state after every import, so new tags appear immediately.
- **Overlay actions wrap** — reader bottom overlay changed from `flex-wrap: nowrap; overflow: hidden` to `flex-wrap: wrap` so all buttons (Fix, Bookmark, Bookmark list, Glossary) remain visible.

### Fixed
- Glossary button was clipped by `overflow: hidden` on `.overlay-actions` — resolved.
- Book grid was left-aligned; now centred using CSS Grid.

---

## Earlier sessions (pre-changelog)

### Added
- Initial Android port: Go HTTP server (`saatool-android/`) + Kotlin WebView shell (`android/`).
- Shared React frontend auto-detecting Wails IPC vs Android HTTP (`api/index.js`).
- Library page: book grid, import modal (EPUB + `.spz`), delete confirmation.
- Reader page: paged paragraphs, tap zones (prev/next), RTL support, font size controls.
- Settings page: dark mode, font size, language defaults, AI API key, translate-ahead, auto-proofread, context doc size.
- Flash fix on page turn (`fadeKey` + CSS `fade-in` animation).
- Library filter redesign: multi-section checkbox dropdown (status / author / genre / style) with localStorage persistence and active-filter badge.
- Book info modal: AI-assisted fetch of author, genre, synopsis, writing style, characters.
- `build-android.bat` / `build_android.ps1` one-click build + install scripts.
