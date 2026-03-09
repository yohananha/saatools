# SaaTools — Monorepo Overview

A book translation tool that uses AI (DeepSeek / Ollama) to translate EPUBs paragraph by paragraph.
Available as both a **Windows/macOS/Linux desktop app** (Wails) and an **Android APK** (WebView + Go HTTP server).

---

## Folder Structure

```
saatools/
├── saatool/           ← Core library (Go)       — the brain
├── saatool-wails/     ← Desktop app (Go + React) — Wails shell
├── saatool-android/   ← Android bridge (Go)      — HTTP server + gomobile
├── android/           ← Android app (Kotlin)     — WebView shell
└── build-android.bat  ← One-click build + install script
```

---

## Folder Roles

### `saatool/` — Core Library
The engine. Contains all business logic — no UI whatsoever.

- Parses and imports EPUB files
- Manages translation projects (`.spz` files)
- Calls AI translation APIs (DeepSeek, Ollama)
- Manages glossary, positions, book metadata
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
        ├── App.jsx     ← Shell: routing, theme, toast, bottom nav
        ├── App.css     ← All styles (single global CSS file)
        ├── api/
        │   └── index.js  ← Auto-detects Wails vs HTTP mode
        └── pages/
            ├── Library.jsx   ← Book grid, import modal
            ├── Reader.jsx    ← Paged reader (tap to navigate, RTL support)
            ├── Settings.jsx  ← AI keys, language defaults
            └── Log.jsx       ← Translation activity log
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
gomobile bind -target=android -androidapi 26 -o ..\android\app\libs\saatool-android.aar ./mobile/
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
        ├── AndroidManifest.xml
        └── java/com/saatool/app/
            ├── GoServerService.kt  ← Starts the Go HTTP server as a foreground service
            └── MainActivity.kt     ← Hides nav bar, shows fullscreen WebView → localhost:8766
```

**Build APK:**
```bat
cd android
gradlew assembleDebug
```

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
The `api/index.js` layer auto-detects which environment it is in:
- `window.go` exists → **Wails mode** (calls Go functions directly via IPC)
- No `window.go` → **HTTP mode** (calls `http://localhost:8766/api/*` REST endpoints)

---

## Tech Stack

| Layer | Technology |
|---|---|
| Core logic | Go 1.24 |
| Desktop shell | Wails v2 |
| Android shell | Kotlin + Android WebView |
| Android Go bridge | `golang.org/x/mobile` (gomobile) |
| UI framework | React 18 |
| Build tool | Vite 5 |
| Styling | Plain CSS (no Tailwind, no component library) |
| Fonts | Inter (UI) + Lora (reader) via Google Fonts |
| AI backends | DeepSeek API, Ollama (local) |

---

## Full Rebuild & Install (Android)

Run from the repo root (`saatools/`):

```bat
:: 1. Build React
cd saatool-wails\frontend && npm run build && cd ..\..

:: 2. Copy React dist into Go embed dir
xcopy /E /I /Y saatool-wails\frontend\dist saatool-android\server\frontend\dist

:: 3. Build Go AAR
cd saatool-android
gomobile bind -target=android -androidapi 26 -o ..\android\app\libs\saatool-android.aar ./mobile/
cd ..

:: 4. Build APK
cd android && gradlew assembleDebug && cd ..

:: 5. Install on connected device
adb install -r android\app\build\outputs\apk\debug\app-debug.apk
```

Or just run **`build-android.bat`** at the repo root — it does all five steps automatically.

---

## Prerequisites

| Tool | Purpose |
|---|---|
| Go 1.22+ | Build everything |
| Node.js 18+ | Build the React frontend |
| Wails v2 CLI | Desktop app (`go install github.com/wailsapp/wails/v2/cmd/wails@latest`) |
| gomobile | Android AAR (`go install golang.org/x/mobile/cmd/gomobile@latest && gomobile init`) |
| Android Studio | Android SDK, NDK r25c+, emulator |
| ADB | Install APK on device |

---

## Source Control

Only `saatool/` has its own git repository. The remaining folders (`saatool-wails/`, `saatool-android/`, `android/`) are not yet tracked. To initialize:

```bat
cd "C:\Users\Yohanan.H\source\repos\saatools"
git init
git add saatool-wails saatool-android android build-android.bat README.md
git commit -m "Initial commit — Wails desktop + Android port"
```

> `saatool/` can optionally be added as a git submodule if you want the full tree tracked from one repo.
