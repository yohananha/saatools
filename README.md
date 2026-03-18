# Babel Reader — Monorepo Overview

An AI-powered EPUB reader and translator that uses DeepSeek to translate books paragraph by paragraph.
Available as both a **Windows/macOS/Linux desktop app** (Wails) and an **Android APK** (WebView + Go HTTP server).

---

## Folder Structure

```
saatools/
├── saatool/           ← Core library (Go)       — the brain
├── saatool-wails/     ← Desktop app (Go + React) — Wails shell
├── saatool-android/   ← Android bridge (Go)      — HTTP server + gomobile
├── android/           ← Android app (Kotlin)     — WebView shell
├── build-android.bat  ← One-click build + install script
├── README.md
└── CHANGELOG.md
```

---

## Folder Roles

### `saatool/` — Core Library
The engine. Contains all business logic — no UI whatsoever.

- Parses and imports EPUB files
- Manages translation projects (`.spz` files)
- Calls AI translation APIs (DeepSeek, Ollama)
- Manages glossary, bookmarks, reading position, book metadata
- Handles file storage (respects `$FILESDIR` env var for Android paths)

**Has its own git repository** (`github.com/dtylman/saatool`).
All other folders depend on this one via a local `replace` directive in their `go.mod`.

---

### `saatool-wails/` — Desktop App
Wraps the core library with a **Wails** shell to produce a native desktop `.exe` / `.app`.

```
saatool-wails/
├── app.go              ← Exposes core library methods to JavaScript via Wails IPC
├── main.go             ← Wails entry point
├── go.mod              ← requires saatool + wails/v2
└── frontend/           ← React UI (shared with Android)
    ├── index.html
    ├── package.json    ← React 18 + Vite 5 (no component library)
    └── src/
        ├── main.jsx
        ├── App.jsx     ← Shell: routing, theme, toast, bottom nav (Library + Settings + toggle)
        ├── App.css     ← All styles (single global CSS file)
        ├── api/
        │   └── index.js  ← Auto-detects Wails vs HTTP mode
        └── pages/
            ├── Library.jsx   ← Book grid, import modal, filters
            ├── Reader.jsx    ← Paged reader (tap to navigate, RTL support, bookmarks, glossary)
            ├── Settings.jsx  ← AI keys, language defaults, inline log viewer
            └── Log.jsx       ← Translation activity log (standalone or inline)
```

**Run desktop app:**
```bat
cd saatool-wails
wails dev
```

---

### `saatool-android/` — Android Bridge (Go)
A separate Go module whose job is to make the core library usable from Android.

```
saatool-android/
├── go.mod                  ← requires saatool + gorilla/websocket + golang.org/x/mobile
├── server/
│   ├── server.go           ← HTTP server + WebSocket hub (runs on localhost:8766)
│   └── handlers.go         ← One REST handler per core library method
└── mobile/
    └── mobile.go           ← gomobile bind entry: Start(port, filesDir) → Server
```

- The HTTP server serves the **same React frontend** embedded via `//go:embed frontend/dist`
- WebSocket (`/ws/events`) pushes `translation:complete` events to the browser
- `gomobile bind` compiles this into `saatool-android.aar` (ARM64 + x86_64 native `.so` files + Java glue)

**Build AAR:**
```bat
cd saatool-android
gomobile bind -target android -androidapi 26 -o ..\android\app\libs\saatool-android.aar ./mobile/
```

---

### `android/` — Android App Shell (Kotlin)
A standard Android Studio project. Contains almost no logic — it just launches Go and shows the UI.

```
android/
├── settings.gradle
├── build.gradle
└── app/
    ├── build.gradle
    ├── libs/
    │   └── saatool-android.aar   ← compiled Go binary (generated, not in git)
    └── src/main/
        ├── AndroidManifest.xml   ← label="Babel Reader", icon=@mipmap/ic_launcher
        ├── res/
        │   ├── mipmap-*/         ← App icon at all densities (mdpi → xxxhdpi)
        │   └── values/colors.xml ← Adaptive icon background (#0D2153)
        └── java/com/saatool/app/
            ├── GoServerService.kt  ← Starts the Go HTTP server as a foreground service
            └── MainActivity.kt     ← Hides nav bar, shows fullscreen WebView → localhost:8766
```

**Build APK:**
```bat
cd android
gradlew assembleRelease
```

---

## Features

| Feature | Description |
|---------|-------------|
| **EPUB import** | Import any EPUB for translation or direct reading |
| **Direct read** | Import with "Read as-is" — no translation, no language fields |
| **AI translation** | Paragraph-by-paragraph via DeepSeek API (batch of 5) |
| **Proofread / Fix** | Re-translate all visible paragraphs with a second AI pass; spinner shown while in progress |
| **Bookmarks** | Named bookmarks per book, accessible from the reader overlay |
| **Glossary** | Per-book glossary with tap-to-lookup in reader |
| **Reading position** | Book reopens at the last read paragraph |
| **Dual progress bar** | Separate reading progress (green) and translation progress (accent) |
| **Library filters** | Filter by status, author, genre, writing style; genre tags auto-merged |
| **Book details** | AI-assisted fetch of author, genre, synopsis, characters |
| **Export** | Export as EPUB (translation + metadata), as project (.spz), or translation-only (.txt). On Android, export shows a folder picker and saves the file to the chosen location (Downloads or app storage). |
| **Dark / light mode** | Persistent theme toggle in nav bar |
| **RTL support** | Right-to-left paragraph layout for Hebrew, Arabic, etc. |

---

## Dependency Flow

```
saatool/  (core library — own git repo)
     │
     ├──────────────────────┐
     ▼                      ▼
saatool-wails/          saatool-android/
(Wails desktop app)     (HTTP server + gomobile)
     │                      │
     ▼                      ▼ gomobile bind
  wails build            saatool-android.aar
     │                      │
     ▼                      ▼
  .exe / .app           android/  (Kotlin shell)
                             │
                             ▼
                          .apk
```

Both apps use the **exact same React frontend** (`saatool-wails/frontend/src/`).
The `api/index.js` layer auto-detects the environment:
- `window.go` exists → **Wails mode** (calls Go functions directly via IPC)
- No `window.go` → **HTTP mode** (calls `http://localhost:8766/api/*` REST endpoints)

---

## Tech Stack

| Layer | Technology |
|-------|-----------|
| Core logic | Go 1.24 |
| Desktop shell | Wails v2 |
| Android shell | Kotlin + Android WebView |
| Android Go bridge | `golang.org/x/mobile` (gomobile) |
| UI framework | React 18 |
| Build tool | Vite 5 |
| Styling | Plain CSS (no Tailwind, no component library) |
| Fonts | Inter (UI) + Lora (reader) via Google Fonts |
| AI backend | DeepSeek API (`deepseek-chat` + `deepseek-reasoner`) |

---

## Full Rebuild & Install (Android)

Run from the repo root (`saatools/`):

```bash
# 1. Build React
cd saatool-wails/frontend && npm run build && cd ../..

# 2. Copy React dist into Go embed dir
cp -r saatool-wails/frontend/dist saatool-android/server/frontend/dist

# 3. Build Go AAR
cd saatool-android
gomobile bind -target android -androidapi 26 -o ../android/app/libs/saatool-android.aar ./mobile/
cd ..

# 4. Build APK
cd android && gradlew assembleRelease && cd ..

# 5. Install on connected device
adb install -r android/app/build/outputs/apk/release/app-release.apk
```

Or run **`build-android.bat`** at the repo root — it does all five steps automatically.

> **Tip (bash/PowerShell):** Steps 3–5 are also in `build_android.ps1` for use after a manual React build+copy.

---

## Prerequisites

| Tool | Purpose |
|------|---------|
| Go 1.22+ | Build everything |
| Node.js 18+ | Build the React frontend |
| Wails v2 CLI | Desktop app (`go install github.com/wailsapp/wails/v2/cmd/wails@latest`) |
| gomobile | Android AAR (`go install golang.org/x/mobile/cmd/gomobile@latest && gomobile init`) |
| Android Studio | Android SDK (API 26+), NDK r29+ |
| ADB | Install APK on device |
| Python 3 + Pillow | Regenerate app icon PNGs from source image (`pip install pillow`) |

---

## NDK / SDK paths (Windows)

```
ANDROID_HOME = C:\Users\<user>\AppData\Local\Android\Sdk
NDK          = C:\Users\<user>\AppData\Local\Android\Sdk\ndk\29.0.14206865
JAVA_HOME    = C:\Program Files\Android\Android Studio\jbr
```
