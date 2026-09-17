// Package server holds the HTTP surface.
//
// There is one API. The web interface is an ordinary client of it with no
// private endpoints, so anything the browser can do an API token can do.
package server

import (
	"encoding/json"
	"net/http"
	"sync/atomic"

	"github.com/alexedwards/scs/v2"

	"github.com/fosslife/drive/internal/auth"
	"github.com/fosslife/drive/internal/scan"
)

type Server struct {
	ready      atomic.Bool
	scanStatus func() scan.Status
	users      *auth.Store
	sessions   *scs.SessionManager
	limiter    *auth.Limiter
}

func New(users *auth.Store, sessions *scs.SessionManager, scanStatus func() scan.Status) *Server {
	return &Server{
		scanStatus: scanStatus,
		users:      users,
		sessions:   sessions,
		limiter:    auth.NewLimiter(auth.DefaultMaxFailures, auth.DefaultFailureWindow),
	}
}

// SetReady marks the instance able to serve requests. Readiness is not "scan
// finished": a scan may be running for minutes and must never gate serving.
func (s *Server) SetReady(ready bool) { s.ready.Store(ready) }

// route is a registered endpoint. public is the explicit exception list the
// auth spec allows — health, login, and later share access and first-run setup.
// Anything not marked public is wrapped in authentication by construction, so a
// new handler cannot be added unauthenticated by forgetting to.
type route struct {
	pattern string
	public  bool
	handler http.HandlerFunc
}

func (s *Server) routes() []route {
	return []route{
		{"GET /healthz", true, s.health},
		{"POST /api/login", true, s.login},
		// First-run setup is public because there is nothing to authenticate
		// against yet; the printed one-time token is the credential.
		{"GET /api/setup", true, s.setupOpen},
		{"POST /api/setup", true, s.completeSetup},

		{"POST /api/logout", false, s.logout},
		{"GET /api/me", false, s.me},
		{"GET /api/scan", false, s.scan},

		{"GET /api/tokens", false, s.listTokens},
		{"POST /api/tokens", false, s.createToken},
		{"DELETE /api/tokens/{id}", false, s.revokeToken},

		{"GET /api/admin/users", false, s.requireAdmin(s.listUsers)},
		{"POST /api/admin/users", false, s.requireAdmin(s.createUser)},
		{"POST /api/admin/users/{username}/disabled", false, s.requireAdmin(s.setUserDisabled)},
		{"DELETE /api/admin/users/{username}", false, s.requireAdmin(s.deleteUser)},
	}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	for _, rt := range s.routes() {
		h := http.Handler(rt.handler)
		if !rt.public {
			h = s.requireAuth(h)
		}
		mux.Handle(rt.pattern, h)
	}
	// sameOrigin runs before authentication and before any handler, so a forged
	// cross-site request is refused before a file is read or written.
	return s.sessions.LoadAndSave(sameOrigin(mux))
}

// scan reports reconciliation progress. Clients need it to know whether an
// empty or short listing means "that is everything" or "not indexed yet".
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

func writeError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}
