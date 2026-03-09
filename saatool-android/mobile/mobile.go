// Package mobile is the gomobile bind entry point.
// gomobile generates Java/Kotlin bindings from the exported symbols here.
//
// Build with:
//
//	gomobile bind -target=android -androidapi 26 \
//	              -o ../android/app/libs/saatool-android.aar ./mobile/
package mobile

import (
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
// Returns a Server handle; call Stop() to shut down.
func Start(port int, filesDir string) (*Server, error) {
	srv, err := server.New(port, filesDir)
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
