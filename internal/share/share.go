// Package share hands one file or folder to somebody who has no account here.
//
// A link is a reference to a file's identity, never to its path: renaming or
// moving the target keeps the link working, and there is nothing in the token
// for a visitor to decode or increment. Everything a link grants is read-only
// and confined to the shared item, which is a property of the two functions
// below and not of the handlers that call them.
package share

import (
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"strings"
	"time"

	"github.com/fosslife/drive/internal/auth"
	"github.com/fosslife/drive/internal/files"
	"github.com/fosslife/drive/internal/index"
)

var (
	// ErrNotFound covers an unknown token and a revoked one alike: a revoked
	// link leaves no row, so a visitor cannot tell "never existed" from "was
	// taken away", which is the answer they are least entitled to.
	ErrNotFound = errors.New("no such share link")
	// ErrExpired is a link past its expiry time. It is separate from ErrNotFound
	// because the holder of a real token is owed a reason they can act on.
	ErrExpired = errors.New("this share link has expired")
	// ErrGone is a link whose target has been deleted or has vanished from disk.
	ErrGone = errors.New("this share link no longer points at anything")
	// ErrPassword is a protected link with no password or the wrong one.
	ErrPassword = errors.New("this share link needs a password")
)

// Link is one share as its owner sees it. It never carries the token: only the
// hash is stored, so the URL exists exactly once, in the response that created
// it.
type Link struct {
	ID   int64  `json:"id"`
	Path string `json:"path"`
	Name string `json:"name"`
	Kind string `json:"kind"`
	// State is the target's, so a listing can show the owner that a link points
	// at something they have since deleted.
	State     string    `json:"state"`
	Protected bool      `json:"protected"`
	ExpiresAt time.Time `json:"expires,omitzero"`
	CreatedAt time.Time `json:"created"`
}

// Access is a resolved link: which account's root holds the content, what it
// points at, and whether the visitor still owes a password.
type Access struct {
	ID        int64
	UserID    int64
	Target    files.Entry
	Protected bool
	ExpiresAt time.Time
}

// Create issues a link to a path in the user's own root. password may be empty
// for an open link, and a zero expires means it lasts until it is revoked.
//
// Nothing outside the creator's root is reachable: the target is looked up by
// (user, path) in the index, so another account's file is simply not there.
func Create(db *index.DB, userID int64, path, password string, expires time.Time) (secret string, l Link, err error) {
	target, err := files.Lookup(db, userID, path)
	if err != nil {
		return "", Link{}, err
	}
	if !expires.IsZero() && !expires.After(time.Now()) {
		return "", Link{}, fmt.Errorf("%w: %s is already past", files.ErrInvalid, expires.Format(time.RFC3339))
	}

	secret, err = auth.NewSecret()
	if err != nil {
		return "", Link{}, err
	}
	var hash any
	if password != "" {
		// Argon2id, the same as an account password: this one is chosen by a
		// human and typed by a human, so it is the one thing here worth the cost.
		h, err := auth.HashPassword(password)
		if err != nil {
			return "", Link{}, err
		}
		hash = h
	}
	var expiresAt any
	if !expires.IsZero() {
		expiresAt = expires.Unix()
	}

	created := time.Now()
	res, err := db.Exec(`INSERT INTO shares (user_id, file_id, token_hash, password_hash, expires_at, created_at)
	                     VALUES (?, ?, ?, ?, ?, ?)`,
		userID, target.ID, auth.HashSecret(secret), hash, expiresAt, created.Unix())
	if err != nil {
		return "", Link{}, fmt.Errorf("creating a share link: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return "", Link{}, fmt.Errorf("creating a share link: %w", err)
	}
	return secret, Link{
		ID: id, Path: target.Path, Name: target.Name, Kind: target.Kind, State: target.State,
		Protected: password != "", ExpiresAt: expires, CreatedAt: created.Truncate(time.Second),
	}, nil
}

// List returns the user's links, each with what it currently points at.
func List(db *index.DB, userID int64) ([]Link, error) {
	rs, err := db.Query(`SELECT s.id, s.password_hash IS NOT NULL, s.expires_at, s.created_at,
	                            f.dir, f.name, f.kind, f.state
	                     FROM shares s JOIN files f ON f.id = s.file_id
	                     WHERE s.user_id = ? ORDER BY s.created_at, s.id`, userID)
	if err != nil {
		return nil, fmt.Errorf("listing share links: %w", err)
	}
	defer rs.Close()

	links := []Link{} // an empty list, never a null
	for rs.Next() {
		var (
			l       Link
			dir     string
			expires sql.NullInt64
			created int64
		)
		if err := rs.Scan(&l.ID, &l.Protected, &expires, &created, &dir, &l.Name, &l.Kind, &l.State); err != nil {
			return nil, fmt.Errorf("listing share links: %w", err)
		}
		l.Path = index.JoinPath(dir, l.Name)
		l.CreatedAt = time.Unix(created, 0)
		if expires.Valid {
			l.ExpiresAt = time.Unix(expires.Int64, 0)
		}
		links = append(links, l)
	}
	return links, rs.Err()
}

// Revoke deletes one link, scoped to its owner. It takes effect on the next
// request because the next request is a lookup by token hash that finds nothing.
func Revoke(db *index.DB, userID, id int64) error {
	res, err := db.Exec(`DELETE FROM shares WHERE id = ? AND user_id = ?`, id, userID)
	if err != nil {
		return fmt.Errorf("revoking a share link: %w", err)
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return ErrNotFound
	}
	return nil
}

// Open resolves a visitor's token. It reports expiry and a deleted target
// before it reports anything about the content, and it never returns the
// target's owner or path to a caller that has not satisfied the password.
func Open(db *index.DB, secret string) (Access, error) {
	var (
		a        Access
		fileID   int64
		password sql.NullString
		expires  sql.NullInt64
	)
	err := db.QueryRow(`SELECT id, user_id, file_id, password_hash, expires_at FROM shares WHERE token_hash = ?`,
		auth.HashSecret(secret)).Scan(&a.ID, &a.UserID, &fileID, &password, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		return Access{}, ErrNotFound
	}
	if err != nil {
		return Access{}, fmt.Errorf("opening a share link: %w", err)
	}
	a.Protected = password.Valid
	if expires.Valid {
		a.ExpiresAt = time.Unix(expires.Int64, 0)
		if time.Now().After(a.ExpiresAt) {
			return Access{}, ErrExpired
		}
	}

	target, err := files.ByID(db, a.UserID, fileID)
	if errors.Is(err, files.ErrNotFound) {
		return Access{}, ErrGone
	}
	if err != nil {
		return Access{}, err
	}
	// Deleted or vanished: the link stops serving, and the owner sees why in
	// their own listing. Restoring the target makes the same link work again.
	if target.State != "present" {
		return Access{}, ErrGone
	}
	a.Target = target
	return a, nil
}

// Unlock checks a visitor's password against the link's hash.
func Unlock(db *index.DB, id int64, password string) error {
	var hash sql.NullString
	err := db.QueryRow(`SELECT password_hash FROM shares WHERE id = ?`, id).Scan(&hash)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("checking a share password: %w", err)
	}
	if !hash.Valid || !auth.VerifyPassword(hash.String, password) {
		return ErrPassword
	}
	return nil
}

// Entry resolves a path a visitor asked for, relative to the shared item.
//
// An empty rel is the shared item itself. Anything else must name something
// inside a shared folder: a traversal, a path above the share, and a subpath of
// a shared *file* are all the same answer, ErrNotFound, because telling them
// apart tells a visitor about the owner's tree above the share.
func (a Access) Entry(db *index.DB, rel string) (files.Entry, error) {
	if rel == "" {
		return a.Target, nil
	}
	if a.Target.Kind != "folder" || !fs.ValidPath(rel) {
		return files.Entry{}, ErrNotFound
	}
	e, err := files.Lookup(db, a.UserID, a.Target.Path+"/"+rel)
	if errors.Is(err, files.ErrNotFound) {
		return files.Entry{}, ErrNotFound
	}
	if err != nil {
		return files.Entry{}, err
	}
	// fs.ValidPath already rules out the ways out of the subtree. This is the
	// assertion that the rule held, because the cost of it not holding is
	// another account's file served to the internet.
	if !a.contains(e.Path) {
		return files.Entry{}, ErrNotFound
	}
	return e, nil
}

func (a Access) contains(path string) bool {
	return path == a.Target.Path || strings.HasPrefix(path, a.Target.Path+"/")
}

// Relative rewrites an entry's path to be relative to the shared item: a
// visitor is told where something sits inside what they were given, never where
// that sits in the owner's root.
func (a Access) Relative(e files.Entry) files.Entry {
	e.Path = strings.TrimPrefix(strings.TrimPrefix(e.Path, a.Target.Path), "/")
	return e
}
