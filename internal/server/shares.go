package server

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/fosslife/drive/internal/files"
	"github.com/fosslife/drive/internal/share"
)

func shareError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, share.ErrNotFound):
		writeError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, share.ErrExpired), errors.Is(err, share.ErrGone):
		writeError(w, http.StatusGone, err.Error())
	case errors.Is(err, share.ErrPassword):
		// The one field a visitor's client needs to decide what to show. It says
		// a password is wanted and nothing whatsoever about what is behind it.
		writeJSON(w, http.StatusUnauthorized, map[string]any{
			"error":             share.ErrPassword.Error(),
			"password_required": true,
		})
	default:
		fileError(w, err)
	}
}

// createShare issues a link to something in the caller's own root. The token is
// in this response and nowhere else: only its hash is stored, so a link that is
// not copied now has to be created again.
func (s *Server) createShare(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Path     string    `json:"path"`
		Password string    `json:"password"`
		Expires  time.Time `json:"expires"`
	}
	if !decode(w, r, &in) {
		return
	}
	secret, link, err := share.Create(s.db, userFrom(r.Context()).ID, in.Path, in.Password, in.Expires)
	if err != nil {
		shareError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, struct {
		share.Link
		Token string `json:"token"`
		// Relative: this process does not reliably know the name it is reached
		// by, and guessing one into a link people paste is worse than not.
		URL string `json:"url"`
	}{link, secret, "/s/" + secret})
}

func (s *Server) listShares(w http.ResponseWriter, r *http.Request) {
	links, err := share.List(s.db, userFrom(r.Context()).ID)
	if err != nil {
		shareError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, links)
}

func (s *Server) revokeShare(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "share id must be a number")
		return
	}
	if err := share.Revoke(s.db, userFrom(r.Context()).ID, id); err != nil {
		shareError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// shareKey names one unlocked link in a visitor's session. The session is the
// only thing carrying the proof: a password in a query string ends up in logs,
// in history, and in whatever the visitor pastes the URL into next.
func shareKey(id int64) string { return "share:" + strconv.FormatInt(id, 10) }

// visitor resolves the token in the URL into an access, or writes the reason it
// could not. Every public share handler starts here, which is what makes
// expiry, revocation, deletion and the password one decision rather than four.
func (s *Server) visitor(w http.ResponseWriter, r *http.Request) (share.Access, bool) {
	a, err := share.Open(s.db, r.PathValue("token"))
	if err != nil {
		shareError(w, err)
		return share.Access{}, false
	}
	if a.Protected && !s.sessions.GetBool(r.Context(), shareKey(a.ID)) {
		shareError(w, share.ErrPassword)
		return share.Access{}, false
	}
	return a, true
}

// shareInfo is what a visitor's client asks first: what is behind this link,
// and does it want a password. A protected link answers only the second part
// until the password is in, so the name of the file is not disclosed either.
func (s *Server) shareInfo(w http.ResponseWriter, r *http.Request) {
	a, ok := s.visitor(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Name      string    `json:"name"`
		Kind      string    `json:"kind"`
		Size      int64     `json:"size"`
		Modified  time.Time `json:"modified"`
		ExpiresAt time.Time `json:"expires,omitzero"`
	}{a.Target.Name, a.Target.Kind, a.Target.Size, a.Target.ModTime, a.ExpiresAt})
}

// unlockShare takes the password for a protected link and records the result in
// the visitor's session. Attempts are limited per link and per source, so a
// share password is no more guessable than an account password.
func (s *Server) unlockShare(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Password string `json:"password"`
	}
	if !decode(w, r, &in) {
		return
	}
	// Resolved without s.visitor: this is the request that supplies the password,
	// so it must get past the password check that everything else runs into.
	a, err := share.Open(s.db, r.PathValue("token"))
	if err != nil {
		shareError(w, err)
		return
	}

	link, source := shareKey(a.ID), "ip:"+clientIP(r)
	if !s.limiter.Allow(link, source) {
		writeError(w, http.StatusTooManyRequests, "too many failed attempts, try again later")
		return
	}
	if err := share.Unlock(s.db, a.ID, in.Password); err != nil {
		s.limiter.Fail(link, source)
		shareError(w, err)
		return
	}
	s.limiter.Succeed(link, source)
	// RenewToken first: the visitor's session is about to gain a privilege, and
	// a session identifier that predates it is one an attacker could have set.
	if err := s.sessions.RenewToken(r.Context()); err != nil {
		slog.Error("renewing a session for a share link", "error", err)
		writeError(w, http.StatusInternalServerError, "could not open the link")
		return
	}
	s.sessions.Put(r.Context(), shareKey(a.ID), true)
	w.WriteHeader(http.StatusNoContent)
}

// shareList browses inside a shared folder. Paths in the response are relative
// to the share, so a visitor learns nothing about where it sits in the owner's
// root.
func (s *Server) shareList(w http.ResponseWriter, r *http.Request) {
	a, ok := s.visitor(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	dir, err := a.Entry(s.db, q.Get("path"))
	if err != nil {
		shareError(w, err)
		return
	}
	if dir.Kind != "folder" {
		writeError(w, http.StatusBadRequest, "that is a file; download it instead")
		return
	}

	limit, _ := strconv.Atoi(q.Get("limit"))
	entries, next, err := files.List(s.db, a.UserID, dir.Path, q.Get("cursor"), limit)
	if err != nil {
		shareError(w, err)
		return
	}
	for i, e := range entries {
		entries[i] = a.Relative(e)
	}
	writeJSON(w, http.StatusOK, struct {
		Path    string        `json:"path"`
		Entries []files.Entry `json:"entries"`
		Next    string        `json:"next,omitempty"`
	}{a.Relative(dir).Path, entries, next})
}

// shareDownload serves one file from inside a share, through the same headers
// every other stored file is served with.
func (s *Server) shareDownload(w http.ResponseWriter, r *http.Request) {
	a, ok := s.visitor(w, r)
	if !ok {
		return
	}
	e, err := a.Entry(s.db, r.PathValue("path"))
	if err != nil {
		shareError(w, err)
		return
	}
	if e.Kind != "file" {
		writeError(w, http.StatusBadRequest, "that is a folder")
		return
	}

	root, err := s.rootOf(a.UserID)
	if err != nil {
		shareError(w, err)
		return
	}
	defer root.Close()

	serveFile(w, r, root, e)
}
