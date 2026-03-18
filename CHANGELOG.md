# Changelog — Babel Reader

All notable changes to this project are documented here.
Format follows [Keep a Changelog](https://keepachangelog.com/en/1.0.0/).

---

## [Unreleased] — 2026-03-18

### Added
- **Fix all visible paragraphs** — tapping Fix now fixes every paragraph currently visible on the page (not just the first). All fix requests fire in parallel and the button shows a spinner until every paragraph completes.

### Changed
- **Fix uses dedicated semaphore** (`fixSem`) — Fix no longer competes for slots with background batch translation, so it starts immediately even when translation is running.

### Fixed
- **Fix button showed "pressed" state permanently** — the button used `.active` (solid accent fill, no animation) even while idle; replaced with `.fixing` which is only applied while the fix is in progress.
- **No visual feedback during fix** — button now shows a spinner and "Fixing…" label while waiting, and pulses via `@keyframes pulse-fix` so it's clear the request is in flight.
- **Fix applied result to wrong paragraph** — `fixIndex` was derived from `paragraphs[0]?.index`, which could be stale when the batch hadn't refreshed yet after navigation. Index is now taken directly from the live `visibleParagraphs` slice, which is always in sync with the current page.

---

## [Unreleased] — 2026-03-15

### Added
- **Export from Reader** — EPUB export button (📖 EPUB) in the reader overlay so users can export without returning to the Library.
- **Android export persistence** — On Android, EPUB/SPZ/TXT export now saves files to a chosen folder. The WebView does not handle programmatic `<a download>`; the app shows the folder picker (Downloads / App storage or document tree), fetches the export from the server, then writes the file via a new `AndroidBridge.saveFile(fullPath, base64)` bridge method.
- **Request logging** — All HTTP requests are logged (method + path, and query for `/api/` routes) to aid debugging in the in-app log.

### Changed
- **EPUB export filename** — Exported EPUB filename is now `{bookName} - {author} - {targetLang} by Babel.epub` (e.g. `Hooray for Hair! - Dr. Seuss - he by Babel.epub`). The server sets this in `Content-Disposition`; the frontend parses quoted filenames correctly so the full name is used when saving on Android and in the browser.

### Fixed
- **Android EPUB export produced no file** — The server returned the EPUB bytes with 200, but the frontend only triggered a programmatic download; on Android WebView that does not persist to storage. Export now uses the folder picker + `saveFile` bridge so the file appears in the chosen folder.
- **Android “permission denied” creating temp dir** — go-epub used `os.TempDir()` (e.g. `/data/local/tmp`), which the app cannot write to. When running with a `filesDir` (Android), the server calls `epub.Use(epub.MemoryFS)` so EPUB building uses in-memory storage, and `TMPDIR` is set to the app’s tmp directory in `mobile.Start()`.
- **Export failures returned 200 with empty body** — EPUB (and SPZ/TXT) handlers now buffer the response first; on error they return HTTP 500 with an error message instead of 200 and an empty body, so the client can show a proper error toast.
- **Empty projects could produce invalid EPUB** — If every target paragraph was empty, no sections were added and go-epub could write an invalid or zero-byte file. The exporter now adds a single “Content” section with an empty paragraph when no sections were added.
- **EPUB filename with spaces not used on Android** — The frontend parsed `Content-Disposition` with a regex that stopped at the first space, so only the first word of the filename was used. Added `filenameFromContentDisposition()` to correctly parse quoted values (e.g. `filename="Book - Author - he by Babel.epub"`) and use the full name for all exports (EPUB, SPZ, TXT).

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
