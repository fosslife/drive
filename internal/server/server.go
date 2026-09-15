// Package server holds the HTTP surface.
package server

import (
	"encoding/json"
	"net/http"
	"sync/atomic"
)

type Server struct {
	ready atomic.Bool
}

func New() *Server { return &Server{} }

// SetReady marks the instance able to serve requests. Scanning progress will
// hang off this once the reconciler exists; readiness is not "scan finished",
// since scans must never block serving.
func (s *Server) SetReady(ready bool) { s.ready.Store(ready) }

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.health)
	return mux
}

// health reports readiness only. No version, no paths, no configuration: it is
// reachable without authentication.
func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	status, code := "starting", http.StatusServiceUnavailable
	if s.ready.Load() {
		status, code = "ready", http.StatusOK
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"status": status})
}
