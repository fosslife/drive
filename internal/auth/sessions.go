package auth

import (
	"context"
	"net/http"
	"time"

	"github.com/alexedwards/scs/sqlite3store"
	"github.com/alexedwards/scs/v2"

	"github.com/fosslife/drive/internal/index"
)

// sessionUserKey is the only thing a session holds. The cookie carries an
// opaque token and nothing else: identity and privileges are read from the
// index on every request, so editing the cookie can only invalidate it.
const sessionUserKey = "user_id"

// SessionLifetime is deliberately long. This is a drive people leave open, and
// logout, disable, and delete all revoke server-side immediately regardless.
const SessionLifetime = 30 * 24 * time.Hour

// NewSessions returns a session manager backed by the index.
//
// secure sets the Secure cookie attribute, which a browser will not send over
// plain HTTP. It follows whether the listener is encrypted, because a hardcoded
// true would silently break every login on an instance still being set up.
func NewSessions(db *index.DB, secure bool) *scs.SessionManager {
	m := scs.New()
	m.Store = sqlite3store.NewWithCleanupInterval(db.DB, time.Hour)
	m.Lifetime = SessionLifetime
	m.IdleTimeout = 7 * 24 * time.Hour
	m.Cookie.Name = "drive_session"
	m.Cookie.HttpOnly = true // not reachable from script, so stored XSS cannot lift it
	m.Cookie.Secure = secure
	m.Cookie.SameSite = http.SameSiteLaxMode // first line of the CSRF defence
	m.Cookie.Path = "/"
	return m
}

// Login binds a session to a user. RenewToken issues a new session identifier
// first, which is the whole reason scs is here: without it an attacker who
// planted a known identifier in the victim's browser stays authenticated as
// them once they log in.
func Login(ctx context.Context, m *scs.SessionManager, u *User) error {
	if err := m.RenewToken(ctx); err != nil {
		return err
	}
	m.Put(ctx, sessionUserKey, u.ID)
	return nil
}

// Logout destroys the session server-side, so the identifier stops
// authenticating even if the cookie is kept and replayed.
func Logout(ctx context.Context, m *scs.SessionManager) error {
	return m.Destroy(ctx)
}

// SessionUser returns the id the session is bound to, or 0 for a session that
// has not authenticated.
func SessionUser(ctx context.Context, m *scs.SessionManager) int64 {
	return m.GetInt64(ctx, sessionUserKey)
}
