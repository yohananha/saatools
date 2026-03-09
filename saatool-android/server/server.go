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
	"sync"
	"time"

	"github.com/dtylman/saatool/config"
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
	CheckOrigin: func(r *http.Request) bool { return true }, // allow WebView origin
}

// ─── Server ───────────────────────────────────────────────────────────────────

// Server wraps the HTTP server + App + WebSocket hub.
type Server struct {
	app  *App
	hub  *wsHub
	http *http.Server
}

// New creates and configures a Server. Call ListenAndServe to start it.
func New(port int, filesDir string) (*Server, error) {
	// Set data directory so config/fs.go resolves paths correctly.
	if filesDir != "" {
		if err := setFilesDir(filesDir); err != nil {
			return nil, err
		}
	}

	// Load saved settings from disk (best effort).
	if err := config.LoadOptions(); err != nil {
		log.Printf("could not load options: %v", err)
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
		Handler: mux,
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
	mux.HandleFunc("/api/projects/cover", s.handleProjectCover)

	// Paragraphs
	mux.HandleFunc("/api/paragraphs/batch", s.handleParagraphsBatch)
	mux.HandleFunc("/api/paragraphs/position", s.handlePosition)
	mux.HandleFunc("/api/paragraphs/translate", s.handleTranslate)
	mux.HandleFunc("/api/paragraphs/fix", s.handleFixTranslation)

	// Book details
	mux.HandleFunc("/api/book", s.handleBook)
	mux.HandleFunc("/api/book/fetch", s.handleFetchBookDetails)

	// Glossary
	mux.HandleFunc("/api/glossary", s.handleGlossary)

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
