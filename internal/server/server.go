package server

import (
	"fmt"
	"net/http"
	"os"
)

// Server holds the HTTP mux and listen address. Additional fields (WebSocket
// upgrader, session store, etc.) will be added in later phases.
type Server struct {
	addr string
	mux  *http.ServeMux
}

// New creates a Server listening on addr and registers the /health handler.
func New(addr string) *Server {
	mux := http.NewServeMux()
	s := &Server{addr: addr, mux: mux}
	mux.HandleFunc("/health", s.handleHealth)
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
