package auth

import (
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/fosslife/drive/internal/index"
	"github.com/fosslife/drive/internal/storage"
)

var (
	// ErrInvalidCredentials is returned for a wrong password, an unknown
	// username, and a disabled account alike. Callers must not distinguish
	// them: which one it was is exactly what an attacker is asking.
	ErrInvalidCredentials = errors.New("invalid username or password")
	ErrInvalidUsername    = errors.New("invalid username")
	ErrUserExists         = errors.New("username is taken")
	// ErrNotFound is returned by every lookup and every update that matched no
	// row, for accounts and for tokens alike.
	ErrNotFound = errors.New("not found")
	// ErrSelfAction stops the one accident that can leave an instance with
	// nobody able to administer it. Administrator status cannot be granted or
	// revoked at all, so this is the only rule the account surface needs.
	ErrSelfAction = errors.New("an administrator cannot delete or disable their own account")
	// ErrInvalidValue is a refusal of what was asked for — an empty password, a
	// negative quota — as opposed to a failure to carry it out. The two become
	// different status codes, so they cannot be the same error.
	ErrInvalidValue = errors.New("invalid value")
)

// User is an account. There is no password field: the hash never leaves Store.
type User struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
	IsAdmin  bool   `json:"is_admin"`
	Disabled bool   `json:"disabled"`
	// QuotaBytes is the account's allowance for new bytes. Zero is unlimited.
	QuotaBytes int64 `json:"quota_bytes"`
	CreatedAt  int64 `json:"created_at"`
	// StorageRoot is relative to the data directory, e.g. users/ada. The data
	// directory moves between a host and a container; the roots inside it do not.
	StorageRoot string `json:"-"`
}

// usernamePattern is the design's `[a-z0-9._-]`. The username is a directory
// name under users/, so anything outside this set would have to be escaped
// somewhere, and usernames are immutable in v1 because renaming one means
// moving a storage root.
var usernamePattern = regexp.MustCompile(`^[a-z0-9._-]{1,32}$`)

func ValidUsername(name string) error {
	if !usernamePattern.MatchString(name) {
		return fmt.Errorf("%w: %q must be 1-32 characters of a-z, 0-9, dot, underscore or hyphen", ErrInvalidUsername, name)
	}
	if name == "." || name == ".." {
		return fmt.Errorf("%w: %q is a directory reference", ErrInvalidUsername, name)
	}
	return nil
}

// Store is the account table plus the storage roots those accounts own.
type Store struct {
	db      *index.DB
	dataDir string
	minFree int64
	// DefaultQuota is the allowance a new account is created with, in bytes;
	// zero is unlimited. Set once at startup from configuration, before the
	// listener accepts anything.
	DefaultQuota int64
}

func NewStore(db *index.DB, dataDir string, minFree int64) *Store {
	return &Store{db: db, dataDir: dataDir, minFree: minFree}
}

// Root opens the user's storage root. Every path a request names is resolved
// through this, which is what stops any credential — session or API token —
// from reaching outside its owner's root.
func (s *Store) Root(u *User) (*storage.Root, error) {
	return storage.Open(filepath.Join(s.dataDir, filepath.FromSlash(u.StorageRoot)), s.minFree)
}

// Create adds an account and its storage root. An account re-created with a
// username that was used before reattaches to the same root, files and all:
// that is the recovery path after the index is lost.
func (s *Store) Create(username, password string, admin bool) (*User, error) {
	if err := ValidUsername(username); err != nil {
		return nil, err
	}
	hash, err := hashNew(password)
	if err != nil {
		return nil, err
	}

	root, createdAt := "users/"+username, time.Now().Unix()
	res, err := s.db.Exec(`INSERT INTO users (username, password_hash, is_admin, storage_root, created_at, quota_bytes)
	                       VALUES (?, ?, ?, ?, ?, ?)`,
		username, hash, admin, root, createdAt, s.DefaultQuota)
	if err != nil {
		// modernc.org/sqlite reports constraint failures only in the message;
		// there is no shared error value to compare against.
		if strings.Contains(err.Error(), "UNIQUE") {
			return nil, fmt.Errorf("%w: %q", ErrUserExists, username)
		}
		return nil, fmt.Errorf("creating account %q: %w", username, err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("creating account %q: %w", username, err)
	}

	u := &User{ID: id, Username: username, IsAdmin: admin, QuotaBytes: s.DefaultQuota, CreatedAt: createdAt, StorageRoot: root}
	dir, err := s.Root(u)
	if err != nil {
		// No account without a usable root: undo rather than leave one that
		// fails every request.
		s.db.Exec(`DELETE FROM users WHERE id = ?`, id)
		return nil, err
	}
	dir.Close()
	return u, nil
}

const userColumns = `id, username, is_admin, disabled, quota_bytes, created_at, storage_root`

func scanUser(row interface{ Scan(...any) error }) (*User, error) {
	var u User
	if err := row.Scan(&u.ID, &u.Username, &u.IsAdmin, &u.Disabled, &u.QuotaBytes, &u.CreatedAt, &u.StorageRoot); err != nil {
		return nil, err
	}
	return &u, nil
}

func hashNew(password string) (string, error) {
	if password == "" {
		return "", fmt.Errorf("%w: a password must not be empty", ErrInvalidValue)
	}
	return HashPassword(password)
}

// ChangePassword is a user changing their own password. The current one has to
// be right: a session left open on a shared machine is not authority to take
// the account over.
func (s *Store) ChangePassword(userID int64, current, next string) error {
	var username, hash string
	err := s.db.QueryRow(`SELECT username, password_hash FROM users WHERE id = ? AND disabled = 0`, userID).
		Scan(&username, &hash)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return ErrNotFound
	case err != nil:
		return fmt.Errorf("reading account: %w", err)
	}
	if !VerifyPassword(hash, current) {
		return ErrInvalidCredentials
	}
	return s.SetPassword(username, next)
}

// SetPassword replaces a password without knowing the old one, which is what an
// administrator resetting one does.
//
// It moves the account's session epoch forward, so every session that
// authenticated before this moment stops authenticating. API tokens are
// deliberately untouched: a token is revoked by revoking it, and a password
// change that silently killed an account's automation would surprise the wrong
// person at the wrong time.
func (s *Store) SetPassword(username, password string) error {
	hash, err := hashNew(password)
	if err != nil {
		return err
	}
	// Unix nanoseconds, matching what a session records at login; see the note
	// on sessionLoginKey for why this is not seconds.
	return s.affectOne(`UPDATE users SET password_hash = ?, sessions_valid_from = ? WHERE username = ?`,
		hash, time.Now().UnixNano(), username)
}

// ActiveSession is Active for a browser session: the account must be usable and
// the session must not predate the account's last credential change. loginAt is
// when the session authenticated.
func (s *Store) ActiveSession(id, loginAt int64) (*User, error) {
	u, err := scanUser(s.db.QueryRow(`SELECT `+userColumns+` FROM users
	                                  WHERE id = ? AND disabled = 0 AND sessions_valid_from <= ?`, id, loginAt))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return u, err
}

// Active returns the user only while the account can be used. Every
// authenticated request goes through it, so disabling or deleting an account
// takes effect on that user's next request rather than at their next login.
func (s *Store) Active(id int64) (*User, error) {
	u, err := scanUser(s.db.QueryRow(`SELECT `+userColumns+` FROM users WHERE id = ? AND disabled = 0`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return u, err
}

func (s *Store) List() ([]*User, error) {
	rs, err := s.db.Query(`SELECT ` + userColumns + ` FROM users ORDER BY username`)
	if err != nil {
		return nil, fmt.Errorf("listing accounts: %w", err)
	}
	defer rs.Close()

	var users []*User
	for rs.Next() {
		u, err := scanUser(rs)
		if err != nil {
			return nil, fmt.Errorf("listing accounts: %w", err)
		}
		users = append(users, u)
	}
	return users, rs.Err()
}

// Authenticate checks a password. Every failure returns ErrInvalidCredentials
// and every failure costs one Argon2id verification, so neither the response
// nor the time it took says whether the account exists.
func (s *Store) Authenticate(username, password string) (*User, error) {
	var (
		u    User
		hash string
	)
	err := s.db.QueryRow(`SELECT `+userColumns+`, password_hash FROM users WHERE username = ?`, username).
		Scan(&u.ID, &u.Username, &u.IsAdmin, &u.Disabled, &u.QuotaBytes, &u.CreatedAt, &u.StorageRoot, &hash)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		equaliseTiming(password)
		return nil, ErrInvalidCredentials
	case err != nil:
		return nil, fmt.Errorf("looking up account: %w", err)
	}
	if !VerifyPassword(hash, password) || u.Disabled {
		return nil, ErrInvalidCredentials
	}
	return &u, nil
}

// SetDisabled turns an account off without touching its files or its root.
//
// actorID is the administrator asking, or 0 for no-one in particular — a test
// or a future command line.
func (s *Store) SetDisabled(actorID int64, username string, disabled bool) error {
	if disabled {
		if err := s.refuseSelf(actorID, username); err != nil {
			return err
		}
	}
	return s.affectOne(`UPDATE users SET disabled = ? WHERE username = ?`, disabled, username)
}

// Delete removes the account, its file rows, its tokens, and its shares.
//
// It deliberately does not touch the storage root: nothing but a permanent
// delete destroys user bytes, and the directory left behind is what makes
// re-creating the account a recovery rather than a fresh start.
func (s *Store) Delete(actorID int64, username string) error {
	if err := s.refuseSelf(actorID, username); err != nil {
		return err
	}
	return s.affectOne(`DELETE FROM users WHERE username = ?`, username)
}

// refuseSelf is the whole of what keeps an instance administrable.
//
// There is no other rule because there is no role management: administrator
// status is written by first-run setup and by nothing else, so no sequence of
// permitted operations can leave an instance without the administrator it was
// set up with. What is left to prevent is the accident — the operator removing
// their own account — and that is a comparison, not a guard.
func (s *Store) refuseSelf(actorID int64, username string) error {
	if actorID == 0 {
		return nil
	}
	actor, err := s.Active(actorID)
	if err != nil {
		return err
	}
	if actor.Username == username {
		return ErrSelfAction
	}
	return nil
}

// ByUsername looks an account up by name.
func (s *Store) ByUsername(username string) (*User, error) {
	u, err := scanUser(s.db.QueryRow(`SELECT `+userColumns+` FROM users WHERE username = ?`, username))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return u, err
}

// SetQuota sets an account's allowance for new bytes; 0 is unlimited.
//
// A quota below what the account already holds is allowed and deliberately
// changes nothing about the files that are there: it is a limit on the next
// write, never a reason to delete or hide anything.
func (s *Store) SetQuota(username string, bytes int64) error {
	if bytes < 0 {
		return fmt.Errorf("%w: a quota must not be negative", ErrInvalidValue)
	}
	return s.affectOne(`UPDATE users SET quota_bytes = ? WHERE username = ?`, bytes, username)
}

// Usage is what an account occupies and what it has spoken for.
type Usage struct {
	// Bytes is stored files plus trashed files: trash has not been freed.
	Bytes int64 `json:"usage_bytes"`
	Files int64 `json:"file_count"`
	// Pending is the declared size of uploads that have not finished. They
	// count against a quota — without them ten concurrent uploads each pass a
	// check the ten of them together blow past — and the upload retention
	// sweep is what releases an abandoned one.
	Pending int64 `json:"pending_bytes"`
}

// Usage counts from the index rather than keeping a running total. The index is
// the disposable half of this system: a stored counter would be authoritative
// state for user-visible behaviour living in the half that gets thrown away and
// rebuilt, with a drift bug waiting at every path that frees or consumes bytes.
//
// ponytail: one aggregate over a user's rows, at upload creation and when an
// administrator opens a screen. A maintained counter only if a folder of
// 100,000 files makes it show up in upload latency.
func (s *Store) Usage(userID int64) (Usage, error) {
	var u Usage
	err := s.db.QueryRow(`SELECT COALESCE(SUM(size), 0), COUNT(*) FROM files
	                       WHERE user_id = ? AND kind = 'file' AND state IN ('present', 'trashed')`,
		userID).Scan(&u.Bytes, &u.Files)
	if err != nil {
		return Usage{}, fmt.Errorf("measuring account usage: %w", err)
	}
	err = s.db.QueryRow(`SELECT COALESCE(SUM(size), 0) FROM uploads WHERE user_id = ?`,
		userID).Scan(&u.Pending)
	if err != nil {
		return Usage{}, fmt.Errorf("measuring account usage: %w", err)
	}
	return u, nil
}

func (s *Store) affectOne(query string, args ...any) error {
	res, err := s.db.Exec(query, args...)
	if err != nil {
		return fmt.Errorf("updating account: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}
