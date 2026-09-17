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
)

// User is an account. There is no password field: the hash never leaves Store.
type User struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
	IsAdmin  bool   `json:"is_admin"`
	Disabled bool   `json:"disabled"`
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
	if password == "" {
		return nil, errors.New("password must not be empty")
	}
	hash, err := HashPassword(password)
	if err != nil {
		return nil, err
	}

	root := "users/" + username
	res, err := s.db.Exec(`INSERT INTO users (username, password_hash, is_admin, storage_root, created_at)
	                       VALUES (?, ?, ?, ?, ?)`,
		username, hash, admin, root, time.Now().Unix())
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

	u := &User{ID: id, Username: username, IsAdmin: admin, StorageRoot: root}
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

const userColumns = `id, username, is_admin, disabled, storage_root`

func scanUser(row interface{ Scan(...any) error }) (*User, error) {
	var u User
	if err := row.Scan(&u.ID, &u.Username, &u.IsAdmin, &u.Disabled, &u.StorageRoot); err != nil {
		return nil, err
	}
	return &u, nil
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
		Scan(&u.ID, &u.Username, &u.IsAdmin, &u.Disabled, &u.StorageRoot, &hash)
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
func (s *Store) SetDisabled(username string, disabled bool) error {
	return s.affectOne(`UPDATE users SET disabled = ? WHERE username = ?`, disabled, username)
}

// Delete removes the account, its file rows, its tokens, and its shares.
//
// It deliberately does not touch the storage root: nothing but a permanent
// delete destroys user bytes, and the directory left behind is what makes
// re-creating the account a recovery rather than a fresh start.
func (s *Store) Delete(username string) error {
	return s.affectOne(`DELETE FROM users WHERE username = ?`, username)
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
