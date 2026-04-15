package server

import (
	"context"
	"fmt"
	"net/http"
	"os"
)

// Server holds the HTTP mux, listen address, and signaling state.
type Server struct {
	addr  string
	mux   *http.ServeMux
	store *SessionStore
}

// New creates a Server listening on addr and registers all handlers.
// ctx is used to stop background goroutines (e.g. session cleanup).
func New(ctx context.Context, addr string) *Server {
	mux := http.NewServeMux()
	store := NewSessionStore()
	s := &Server{addr: addr, mux: mux, store: store}
	mux.HandleFunc("/health", s.handleHealth)
	mux.HandleFunc("/ws", s.handleWS)
	store.StartCleanup(ctx)
	return s
}

// ListenAndServe starts the HTTP server. It blocks until the server stops.
func (s *Server) ListenAndServe() error {
	fmt.Fprintf(os.Stderr, "wani-server: listening on %s\n", s.addr)
	if err := http.ListenAndServe(s.addr, s.mux); err != nil {
		return fmt.Errorf("server.ListenAndServe: %w", err)
	}
	return nil
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("Server is listening on " + s.addr + "\n"))
}
