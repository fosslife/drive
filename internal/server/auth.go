package server

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/fosslife/drive/internal/auth"
)

// errAuthRequired is what a route behind requireAuth answers an anonymous
// request with. A public route never produces it, which is what makes the
// route audit in the tests mean something.
const errAuthRequired = "authentication required"

type userKey struct{}

// userFrom returns the authenticated user. It is only ever called from a
// handler behind requireAuth, so a missing user is a wiring bug, not a request.
func userFrom(ctx context.Context) *auth.User {
	u, _ := ctx.Value(userKey{}).(*auth.User)
	return u
}

// requireAuth resolves a session cookie or a bearer token to a live account.
// Both are checked against the index on every request, so disabling or deleting
// an account takes effect on that user's next request.
func (s *Server) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, err := s.authenticate(r)
		if err != nil {
			writeError(w, http.StatusUnauthorized, errAuthRequired)
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), userKey{}, u)))
	})
}

func (s *Server) requireAdmin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if u := userFrom(r.Context()); u == nil || !u.IsAdmin {
			writeError(w, http.StatusForbidden, "administrator access required")
			return
		}
		next(w, r)
	}
}

func (s *Server) authenticate(r *http.Request) (*auth.User, error) {
	if secret, ok := bearerToken(r); ok {
		return s.users.AuthenticateToken(secret)
	}
	id := auth.SessionUser(r.Context(), s.sessions)
	if id == 0 {
		return nil, auth.ErrInvalidCredentials
	}
	return s.users.Active(id)
}

func bearerToken(r *http.Request) (string, bool) {
	const prefix = "Bearer "
	h := r.Header.Get("Authorization")
	if len(h) <= len(prefix) || !strings.EqualFold(h[:len(prefix)], prefix) {
		return "", false
	}
	return h[len(prefix):], true
}

// sameOrigin refuses a state-changing request that cannot show it came from
// this application. `SameSite=Lax` already stops most of it; this closes the
// cases it does not cover and does so without threading a synchroniser token
// through the SPA.
//
// A bearer token is exempt: it is not sent automatically by a browser, so there
// is nothing for another site to forge. That is also what keeps ordinary API
// clients working, which would otherwise have to send an Origin header they
// have no reason to have.
func sameOrigin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !safeMethod(r.Method) {
			if _, isToken := bearerToken(r); !isToken && !originAllowed(r) {
				writeError(w, http.StatusForbidden, "cross-site request refused")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func safeMethod(m string) bool {
	return m == http.MethodGet || m == http.MethodHead || m == http.MethodOptions
}

func originAllowed(r *http.Request) bool {
	// Sec-Fetch-Site is the browser's own answer and cannot be set by script.
	// "none" is a direct navigation, which no other site can cause to be a POST.
	switch r.Header.Get("Sec-Fetch-Site") {
	case "same-origin", "none":
		return true
	case "":
		// Not sent: fall through to Origin, which every browser sends on a
		// cross-origin or non-GET request.
	default:
		return false
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		return false
	}
	// Host only. The scheme is not comparable behind a TLS-terminating proxy,
	// where the browser sees https and this process sees http.
	u, err := url.Parse(origin)
	return err == nil && u.Host != "" && u.Host == r.Host
}

func clientIP(r *http.Request) string {
	// ponytail: RemoteAddr only, no X-Forwarded-For. Trusting a header no proxy
	// is configured to strip would let a client pick its own rate-limit bucket.
	// Honour it behind a configured trusted-proxy list, not before.
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}

type credentials struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var in credentials
	if !decode(w, r, &in) {
		return
	}
	// Keyed by account and by source, so guessing one password everywhere and
	// every password on one account both run into the limit.
	account, source := "user:"+in.Username, "ip:"+clientIP(r)
	if !s.limiter.Allow(account, source) {
		writeError(w, http.StatusTooManyRequests, "too many failed attempts, try again later")
		return
	}

	u, err := s.users.Authenticate(in.Username, in.Password)
	if err != nil {
		s.limiter.Fail(account, source)
		// One response for a wrong password, an unknown account, and a disabled
		// one. Which it was is exactly what an attacker is asking.
		writeError(w, http.StatusUnauthorized, auth.ErrInvalidCredentials.Error())
		return
	}
	if err := auth.Login(r.Context(), s.sessions, u); err != nil {
		writeError(w, http.StatusInternalServerError, "could not establish a session")
		return
	}
	s.limiter.Succeed(account, source)
	writeJSON(w, http.StatusOK, u)
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if err := auth.Logout(r.Context(), s.sessions); err != nil {
		writeError(w, http.StatusInternalServerError, "could not end the session")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, userFrom(r.Context()))
}

func (s *Server) listTokens(w http.ResponseWriter, r *http.Request) {
	tokens, err := s.users.ListTokens(userFrom(r.Context()).ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not list tokens")
		return
	}
	writeJSON(w, http.StatusOK, tokens)
}

func (s *Server) createToken(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name string `json:"name"`
	}
	if !decode(w, r, &in) {
		return
	}
	secret, token, err := s.users.CreateToken(userFrom(r.Context()).ID, in.Name)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	// The only response that ever carries the secret.
	writeJSON(w, http.StatusCreated, struct {
		auth.Token
		Secret string `json:"secret"`
	}{token, secret})
}

func (s *Server) revokeToken(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "token id must be a number")
		return
	}
	switch err := s.users.RevokeToken(userFrom(r.Context()).ID, id); {
	case errors.Is(err, auth.ErrNotFound):
		writeError(w, http.StatusNotFound, "no such token")
	case err != nil:
		writeError(w, http.StatusInternalServerError, "could not revoke the token")
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

func (s *Server) listUsers(w http.ResponseWriter, r *http.Request) {
	users, err := s.users.List()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not list accounts")
		return
	}
	writeJSON(w, http.StatusOK, users)
}

func (s *Server) createUser(w http.ResponseWriter, r *http.Request) {
	var in struct {
		credentials
		IsAdmin bool `json:"is_admin"`
	}
	if !decode(w, r, &in) {
		return
	}
	u, err := s.users.Create(in.Username, in.Password, in.IsAdmin)
	switch {
	case errors.Is(err, auth.ErrUserExists):
		writeError(w, http.StatusConflict, err.Error())
	case err != nil:
		writeError(w, http.StatusBadRequest, err.Error())
	default:
		writeJSON(w, http.StatusCreated, u)
	}
}

func (s *Server) setUserDisabled(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Disabled bool `json:"disabled"`
	}
	if !decode(w, r, &in) {
		return
	}
	s.userChange(w, s.users.SetDisabled(r.PathValue("username"), in.Disabled))
}

func (s *Server) deleteUser(w http.ResponseWriter, r *http.Request) {
	s.userChange(w, s.users.Delete(r.PathValue("username")))
}

func (s *Server) userChange(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, auth.ErrNotFound):
		writeError(w, http.StatusNotFound, "no such account")
	case err != nil:
		writeError(w, http.StatusInternalServerError, "could not update the account")
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

func decode(w http.ResponseWriter, r *http.Request, into any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16))
	dec.DisallowUnknownFields()
	if err := dec.Decode(into); err != nil {
		writeError(w, http.StatusBadRequest, "malformed request body")
		return false
	}
	return true
}
