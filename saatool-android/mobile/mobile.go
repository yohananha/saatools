// Package mobile is the gomobile bind entry point.
// gomobile generates Java/Kotlin bindings from the exported symbols here.
//
// Build with:
//
//	gomobile bind -target=android -androidapi 26 \
//	              -o ../android/app/libs/saatool-android.aar ./mobile/
package mobile

import (
	"os"
	"path/filepath"

	"github.com/dtylman/saatool-android/server"
)

// Server is a running HTTP server instance.
// Obtained from Start(); call Stop() to shut it down.
type Server struct {
	srv *server.Server
}

// Start initialises the Go server and begins listening on the given port.
// filesDir must be the Android app's internal files directory
// (applicationContext.filesDir.absolutePath).
// defaultProjectsDir is the default folder for saving projects (e.g. Downloads);
// if non-empty and no saved setting exists, it is used and persisted. Pass "" on desktop.
// Returns a Server handle; call Stop() to shut down.
func Start(port int, filesDir string, defaultProjectsDir string) (*Server, error) {
	// On Android, os.TempDir() returns /data/local/tmp which is not writable.
	// Set TMPDIR so go-epub and other libs use app storage for temp files.
	if filesDir != "" {
		tmpDir := filepath.Join(filesDir, "tmp")
		_ = os.MkdirAll(tmpDir, 0755)
		os.Setenv("TMPDIR", tmpDir)
	}
	srv, err := server.New(port, filesDir, defaultProjectsDir)
	if err != nil {
		return nil, err
	}
	go srv.ListenAndServe()
	return &Server{srv: srv}, nil
}

// Stop gracefully shuts down the HTTP server.
func (s *Server) Stop() {
	s.srv.Shutdown()
}
