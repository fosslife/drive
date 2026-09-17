// Package server holds the HTTP surface.
package server

import (
	"encoding/json"
	"net/http"
	"sync/atomic"

	"github.com/fosslife/drive/internal/scan"
)

type Server struct {
	ready      atomic.Bool
	scanStatus func() scan.Status
}

func New(scanStatus func() scan.Status) *Server {
	return &Server{scanStatus: scanStatus}
}

// SetReady marks the instance able to serve requests. Readiness is not "scan
// finished": a scan may be running for minutes and must never gate serving.
func (s *Server) SetReady(ready bool) { s.ready.Store(ready) }

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.health)
	mux.HandleFunc("GET /api/scan", s.scan)
	return mux
}

// scan reports reconciliation progress. Clients need it to know whether an
// empty or short listing means "that is everything" or "not indexed yet".
//
// ponytail: unauthenticated for now, like /healthz. It discloses scan counts
// and a storage root name, so task 5.9 must move it behind authentication
// rather than onto the public list.
func (s *Server) scan(w http.ResponseWriter, r *http.Request) {
	status := s.scanStatus()
	writeJSON(w, http.StatusOK, struct {
		scan.Status
		Indexing bool `json:"indexing"`
	}{status, status.Indexing()})
}

// health reports readiness only. No version, no paths, no configuration: it is
// reachable without authentication.
func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	status, code := "starting", http.StatusServiceUnavailable
	if s.ready.Load() {
		status, code = "ready", http.StatusOK
	}
	writeJSON(w, code, map[string]string{"status": status})
}

func writeJSON(w http.ResponseWriter, code int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(body)
}
