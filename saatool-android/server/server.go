package server

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/dtylman/saatool/config"
	"github.com/go-shiori/go-epub"
	"github.com/gorilla/websocket"
)

//go:embed frontend/dist
var frontendFS embed.FS

// ─── WebSocket Hub ────────────────────────────────────────────────────────────

type wsHub struct {
	mu    sync.Mutex
	conns map[*websocket.Conn]struct{}
}

func newHub() *wsHub {
	return &wsHub{conns: make(map[*websocket.Conn]struct{})}
}

func (h *wsHub) add(c *websocket.Conn) {
	h.mu.Lock()
	h.conns[c] = struct{}{}
	h.mu.Unlock()
}

func (h *wsHub) remove(c *websocket.Conn) {
	h.mu.Lock()
	delete(h.conns, c)
	h.mu.Unlock()
}

func (h *wsHub) broadcast(eventType string, payload interface{}) {
	data, err := json.Marshal(map[string]interface{}{
		"type": eventType,
		"data": payload,
	})
	if err != nil {
		log.Printf("ws: marshal error: %v", err)
		return
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	for c := range h.conns {
		if err := c.WriteMessage(websocket.TextMessage, data); err != nil {
			log.Printf("ws: write error: %v", err)
			c.Close()
			delete(h.conns, c)
		}
	}
}

var upgrader = websocket.Upgrader{
	// Only accept WebSocket connections from localhost origins.
	// This prevents cross-origin attacks from malicious web pages while still
	// allowing the embedded Android WebView (http://127.0.0.1:<port>) to connect.
	// Non-browser clients (no Origin header) are also allowed.
	CheckOrigin: isLocalhostOrigin,
}

// isLocalhostOrigin returns true when the request comes from a localhost origin
// or has no Origin header (non-browser client). Used by both the WebSocket
// upgrader and the localhostOnly HTTP middleware.
func isLocalhostOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true // not a browser cross-origin request
	}
	return strings.HasPrefix(origin, "http://127.0.0.1:") ||
		strings.HasPrefix(origin, "http://localhost:") ||
		origin == "file://" // Wails desktop WebView
}

// localhostOnly is an HTTP middleware that rejects requests whose Origin header
// points to a non-localhost source. This prevents cross-site request forgery
// from a malicious page trying to reach the local API server.
// Requests without an Origin header (curl, Wails IPC) are always allowed.
func localhostOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isLocalhostOrigin(r) {
			http.Error(w, "forbidden: cross-origin request rejected", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// requestLogging logs every request so in-app logs show API activity (e.g. export-epub).
func requestLogging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.Printf("req %s %s", r.Method, r.URL.Path)
		if r.URL.RawQuery != "" && strings.HasPrefix(r.URL.Path, "/api/") {
			log.Printf("  query: %s", r.URL.RawQuery)
		}
		next.ServeHTTP(w, r)
	})
}

// securityHeaders adds defensive HTTP response headers.
// The app is served only to localhost, so headers like HSTS are omitted.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Content-Security-Policy",
			"default-src 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline' https://fonts.googleapis.com; font-src 'self' https://fonts.gstatic.com; img-src 'self' data: blob:")
		next.ServeHTTP(w, r)
	})
}

// ─── Server ───────────────────────────────────────────────────────────────────

// Server wraps the HTTP server + App + WebSocket hub.
type Server struct {
	app  *App
	hub  *wsHub
	http *http.Server
}

// New creates and configures a Server. Call ListenAndServe to start it.
// defaultProjectsDir: if non-empty (e.g. Android Downloads path) and Options.ProjectsDirectory
// is not yet set, it is set and persisted so first run uses an accessible folder.
func New(port int, filesDir string, defaultProjectsDir string) (*Server, error) {
	// Set data directory so config/fs.go resolves paths correctly.
	if filesDir != "" {
		if err := setFilesDir(filesDir); err != nil {
			return nil, err
		}
		// On Android, go-epub defaults to os.TempDir() (/data/local/tmp, not writable).
		// Use in-memory storage so EPUB export never touches the filesystem.
		if err := epub.Use(epub.MemoryFS); err != nil {
			log.Printf("epub.Use(MemoryFS) failed: %v", err)
		}
	}

	// Load saved settings from disk (best effort).
	if err := config.LoadOptions(); err != nil {
		log.Printf("could not load options: %v", err)
	}

	// On first run with a default (e.g. Android), use it so projects go to Downloads.
	if defaultProjectsDir != "" && config.Options.ProjectsDirectory == "" {
		config.Options.ProjectsDirectory = defaultProjectsDir
		if err := config.SaveOptions(); err != nil {
			log.Printf("could not persist default projects dir: %v", err)
		}
	}

	// Install mem-logger so GetLog() works.
	log.SetOutput(GetMemLogger())

	hub := newHub()

	broadcast := func(ev TranslationEvent) {
		hub.broadcast("translation:complete", ev)
	}

	app := newApp(broadcast)

	mux := http.NewServeMux()
	s := &Server{app: app, hub: hub}
	s.registerRoutes(mux)

	s.http = &http.Server{
		Addr:    fmt.Sprintf("127.0.0.1:%d", port),
		Handler: securityHeaders(localhostOnly(requestLogging(mux))), // C-3 + L-3 + request log
		// H-2: Prevent goroutine/connection leaks from stalled clients.
		// WriteTimeout is generous (5 min) to allow large project exports to stream.
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 5 * time.Minute,
		IdleTimeout:  120 * time.Second,
	}
	return s, nil
}

// ListenAndServe starts the HTTP server. Blocks until Shutdown is called.
func (s *Server) ListenAndServe() {
	log.Printf("HTTP server listening on %s", s.http.Addr)
	if err := s.http.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Printf("HTTP server error: %v", err)
	}
}

// Shutdown gracefully stops the server.
func (s *Server) Shutdown() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.http.Shutdown(ctx); err != nil {
		log.Printf("shutdown error: %v", err)
	}
}

// ─── Route registration ───────────────────────────────────────────────────────

func (s *Server) registerRoutes(mux *http.ServeMux) {
	// Settings
	mux.HandleFunc("/api/settings", s.handleSettings)

	// Projects
	mux.HandleFunc("/api/projects", s.handleProjects)
	mux.HandleFunc("/api/projects/load", s.handleLoadProject)
	mux.HandleFunc("/api/projects/delete", s.handleDeleteProject)
	mux.HandleFunc("/api/projects/save", s.handleSaveProject)
	mux.HandleFunc("/api/projects/import-epub", s.handleImportEPUB)
	mux.HandleFunc("/api/projects/import-spz", s.handleImportSPZ)
	mux.HandleFunc("/api/projects/export", s.handleExportProject)
	mux.HandleFunc("/api/projects/export-epub", s.handleExportEPUB)
	mux.HandleFunc("/api/projects/export-txt", s.handleExportTXT)
	mux.HandleFunc("/api/projects/cover", s.handleProjectCover)

	// Paragraphs
	mux.HandleFunc("/api/paragraphs/batch", s.handleParagraphsBatch)
	mux.HandleFunc("/api/paragraphs/position", s.handlePosition)
	mux.HandleFunc("/api/paragraphs/translate", s.handleTranslate)
	mux.HandleFunc("/api/translation/active", s.handleSetActiveProject)
	mux.HandleFunc("/api/translation/whole-book", s.handleTranslateWholeBook)
	mux.HandleFunc("/api/paragraphs/fix", s.handleFixTranslation)

	// Book details
	mux.HandleFunc("/api/book", s.handleBook)
	mux.HandleFunc("/api/book/fetch", s.handleFetchBookDetails)

	// Glossary
	mux.HandleFunc("/api/glossary", s.handleGlossary)

	// Bookmarks
	mux.HandleFunc("/api/bookmarks", s.handleBookmarks)

	// Log
	mux.HandleFunc("/api/log", s.handleLog)

	// WebSocket events
	mux.HandleFunc("/ws/events", s.handleWS)

	// React SPA — serve embedded frontend/dist
	distFS, err := fs.Sub(frontendFS, "frontend/dist")
	if err != nil {
		log.Fatalf("could not sub embed FS: %v", err)
	}
	fileServer := http.FileServer(http.FS(distFS))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// Try to serve the static file; fall back to index.html for SPA routing.
		f, err := distFS.Open(r.URL.Path[1:])
		if err != nil {
			// Not found → serve index.html so React Router handles it.
			r.URL.Path = "/"
		} else {
			f.Close()
		}
		fileServer.ServeHTTP(w, r)
	})
}

// ─── WebSocket handler ────────────────────────────────────────────────────────

func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("ws upgrade error: %v", err)
		return
	}
	// H-6: Cap incoming message size. Clients only send pings; 4 KB is generous.
	conn.SetReadLimit(4096)
	s.hub.add(conn)
	// Read loop — keep connection alive; remove on close.
	go func() {
		defer func() {
			s.hub.remove(conn)
			conn.Close()
		}()
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				break
			}
		}
	}()
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

func setFilesDir(dir string) error {
	return os.Setenv("FILESDIR", dir)
}
