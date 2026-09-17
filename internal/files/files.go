// Package files is what a user does with their files: browse, create, rename,
// move, and put things in the trash.
//
// Listings come from the index because that is what the index is for — a sorted
// page of a 100,000-entry folder is one b-tree seek there and a full readdir on
// disk. Everything that changes anything touches the filesystem first and the
// index second: if the process dies in between, the reconciler agrees with the
// disk, which is the only order that cannot lose a file.
package files

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/fosslife/drive/internal/index"
	"github.com/fosslife/drive/internal/storage"
)

var (
	ErrNotFound = errors.New("no such file or folder")
	ErrExists   = errors.New("something is already there")
	// ErrInvalid is a request that cannot be satisfied at any path, such as
	// moving a folder inside itself.
	ErrInvalid = errors.New("invalid request")
	// ErrStale means an If-Match precondition named a version that is no longer
	// current. Last-write-wins, unless the caller asked to be told.
	ErrStale = errors.New("the entry has changed since it was read")
)

// Entry is one file or folder as the index has it.
type Entry struct {
	ID      int64     `json:"id"`
	Name    string    `json:"name"`
	Path    string    `json:"path"`
	Kind    string    `json:"kind"` // "file" or "folder"
	Size    int64     `json:"size"`
	ModTime time.Time `json:"modified"`
	ETag    string    `json:"etag"`
	// State is "present", "trashed", or "missing". Everything that returns an
	// Entry to a user has already filtered on it, so it is not serialised; it
	// matters to a caller that looked an entry up by identity and has to decide
	// what a trashed target means — a share link, for one.
	State string `json:"-"`
}

// DefaultPageSize bounds a listing when the caller does not.
const DefaultPageSize = 500

// MaxPageSize is the ceiling a caller can ask for. A listing is held in memory
// to be serialised, so the page is what bounds that, not the folder.
const MaxPageSize = 2000

const columns = `id, dir, name, kind, size, mtime, state`

func scanEntry(row interface{ Scan(...any) error }) (Entry, error) {
	var (
		e        Entry
		dir      string
		mtime    int64
		modified time.Time
	)
	if err := row.Scan(&e.ID, &dir, &e.Name, &e.Kind, &e.Size, &mtime, &e.State); err != nil {
		return Entry{}, err
	}
	modified = time.Unix(mtime, 0)
	e.Path = index.JoinPath(dir, e.Name)
	e.ModTime = modified
	e.ETag = etag(e.ID, mtime, e.Size)
	return e, nil
}

// etag identifies one version of one entry. Identity plus what a write changes:
// a modification that leaves size and second-resolution mtime alone is exactly
// the case storage.Verify exists to catch, and is not worth a rehash per
// request to tag precisely.
func etag(id, mtime, size int64) string {
	return fmt.Sprintf(`"%d-%d-%d"`, id, mtime, size)
}

// List returns one page of a folder's direct children ordered by name, starting
// after cursor. The returned cursor is the name to pass next, or "" at the end.
//
// dir is "" for the top of the storage root. A folder that does not exist is
// ErrNotFound rather than an empty page: a client must be able to tell them
// apart, and an empty listing for a typo'd path reads as data loss.
func List(db *index.DB, userID int64, dir, cursor string, limit int) (entries []Entry, next string, err error) {
	if limit <= 0 || limit > MaxPageSize {
		limit = DefaultPageSize
	}
	if dir != "" {
		parent, err := Lookup(db, userID, dir)
		if err != nil {
			return nil, "", err
		}
		if parent.Kind != "folder" {
			return nil, "", fmt.Errorf("%w: %q is a file", ErrInvalid, dir)
		}
	}

	rs, err := db.Query(`SELECT `+columns+` FROM files
	                     WHERE user_id = ? AND dir = ? AND state = 'present' AND name > ?
	                     ORDER BY name LIMIT ?`, userID, dir, cursor, limit)
	if err != nil {
		return nil, "", fmt.Errorf("listing %q: %w", dir, err)
	}
	defer rs.Close()

	entries = []Entry{}
	for rs.Next() {
		e, err := scanEntry(rs)
		if err != nil {
			return nil, "", fmt.Errorf("listing %q: %w", dir, err)
		}
		entries = append(entries, e)
	}
	if err := rs.Err(); err != nil {
		return nil, "", fmt.Errorf("listing %q: %w", dir, err)
	}
	// A full page means there may be more. One empty page at the end is cheaper
	// than a count over the whole folder on every request.
	if len(entries) == limit {
		next = entries[len(entries)-1].Name
	}
	return entries, next, nil
}

// Lookup finds one entry by its path relative to the storage root.
func Lookup(db *index.DB, userID int64, path string) (Entry, error) {
	dir, name := index.SplitPath(path)
	e, err := scanEntry(db.QueryRow(`SELECT `+columns+` FROM files
	                                 WHERE user_id = ? AND dir = ? AND name = ? AND state = 'present'`,
		userID, dir, name))
	if errors.Is(err, sql.ErrNoRows) {
		return Entry{}, fmt.Errorf("%w: %q", ErrNotFound, path)
	}
	if err != nil {
		return Entry{}, fmt.Errorf("looking up %q: %w", path, err)
	}
	return e, nil
}

// ByID finds one entry by identity, in whatever state it is in. Anything that
// holds a reference rather than a path — a share link — goes through here: the
// reference survives a rename and a move for free, and the caller decides what
// a trashed or missing target means to it.
func ByID(db *index.DB, userID, id int64) (Entry, error) {
	e, err := scanEntry(db.QueryRow(`SELECT `+columns+` FROM files WHERE id = ? AND user_id = ?`, id, userID))
	if errors.Is(err, sql.ErrNoRows) {
		return Entry{}, fmt.Errorf("%w: no entry with id %d", ErrNotFound, id)
	}
	if err != nil {
		return Entry{}, fmt.Errorf("looking up %d: %w", id, err)
	}
	return e, nil
}

// CheckETag enforces an If-Match precondition. An empty want is no precondition
// at all: concurrency is last-write-wins and the header is how a caller opts
// out of it.
func CheckETag(e Entry, want string) error {
	if want == "" || want == "*" {
		return nil
	}
	for _, tag := range strings.Split(want, ",") {
		if strings.TrimSpace(tag) == e.ETag {
			return nil
		}
	}
	return fmt.Errorf("%w: %q is now %s", ErrStale, e.Path, e.ETag)
}

// CreateFolder creates path and any missing parent, on disk first and then in
// the index.
func CreateFolder(db *index.DB, userID int64, root *storage.Root, path string) (Entry, error) {
	if path == "" {
		return Entry{}, fmt.Errorf("%w: no folder name given", ErrInvalid)
	}
	switch _, err := Lookup(db, userID, path); {
	case err == nil:
		return Entry{}, fmt.Errorf("%w: %q", ErrExists, path)
	case !errors.Is(err, ErrNotFound):
		return Entry{}, err
	}
	if err := root.MkdirAll(path); err != nil {
		return Entry{}, err
	}
	if err := indexFolders(db, userID, root, path); err != nil {
		return Entry{}, err
	}
	return Lookup(db, userID, path)
}

// indexFolders records path and every ancestor MkdirAll just created. The
// reconciler would find them on its next pass anyway; doing it now is what
// makes a new folder appear in its parent's listing immediately.
func indexFolders(db *index.DB, userID int64, root *storage.Root, path string) error {
	if path == "" {
		return nil // the storage root itself, which has no row
	}
	parts := strings.Split(path, "/")
	for i := range parts {
		p := strings.Join(parts[:i+1], "/")
		switch _, err := Lookup(db, userID, p); {
		case err == nil:
			continue
		case !errors.Is(err, ErrNotFound):
			return err
		}
		mtime := time.Now().Unix()
		if st, err := root.Stat(p); err == nil {
			mtime = st.ModTime().Unix()
		}
		dir, name := index.SplitPath(p)
		if _, err := db.Exec(`INSERT INTO files (user_id, dir, name, kind, size, mtime)
		                      VALUES (?, ?, ?, 'folder', 0, ?)`, userID, dir, name, mtime); err != nil {
			return fmt.Errorf("indexing folder %q: %w", p, err)
		}
	}
	return nil
}

// Move renames or relocates an entry within the storage root, keeping its
// identity and, for a folder, everything underneath it.
//
// An occupied destination is refused unless replace is set, in which case what
// was there goes to the trash before the move — never under it.
func Move(db *index.DB, userID int64, root *storage.Root, from, to string, replace bool) (Entry, error) {
	src, err := Lookup(db, userID, from)
	if err != nil {
		return Entry{}, err
	}
	if to == "" {
		return Entry{}, fmt.Errorf("%w: no destination given", ErrInvalid)
	}
	if to == from {
		return src, nil
	}
	// A folder cannot contain itself. Renaming it into its own subtree would
	// detach the whole thing from the filesystem in one syscall.
	if src.Kind == "folder" && strings.HasPrefix(to, from+"/") {
		return Entry{}, fmt.Errorf("%w: %q is inside %q", ErrInvalid, to, from)
	}

	switch existing, err := Lookup(db, userID, to); {
	case err == nil:
		if !replace {
			return Entry{}, fmt.Errorf("%w: %q", ErrExists, to)
		}
		if err := Trash(db, userID, root, existing); err != nil {
			return Entry{}, err
		}
	case !errors.Is(err, ErrNotFound):
		return Entry{}, err
	}

	// Disk first. If the index update below never happens, the reconciler finds
	// the entry at its new path and agrees with the disk.
	if err := root.Rename(from, to); err != nil {
		if errors.Is(err, storage.ErrExists) {
			return Entry{}, fmt.Errorf("%w: %q", ErrExists, to)
		}
		return Entry{}, err
	}
	tx, err := db.Begin()
	if err != nil {
		return Entry{}, fmt.Errorf("moving %q: %w", from, err)
	}
	defer tx.Rollback()
	if err := reparent(tx, userID, src, to); err != nil {
		return Entry{}, err
	}
	if err := tx.Commit(); err != nil {
		return Entry{}, fmt.Errorf("moving %q: %w", from, err)
	}
	return Lookup(db, userID, to)
}

// reparent rewrites the moved row's path and, for a folder, the stored dir of
// everything beneath it. It takes the caller's transaction: a subtree half at
// each path would show the same file twice.
func reparent(tx *sql.Tx, userID int64, src Entry, to string) error {
	dir, name := index.SplitPath(to)
	if _, err := tx.Exec(`UPDATE files SET dir = ?, name = ? WHERE id = ?`, dir, name, src.ID); err != nil {
		return fmt.Errorf("moving %q: %w", src.Path, err)
	}
	if src.Kind == "folder" {
		// SQLite's substr counts characters, not bytes, so the offsets must be
		// rune counts or a folder with a non-ASCII name loses its subtree.
		cut := utf8.RuneCountInString(src.Path) + 1
		if _, err := tx.Exec(`UPDATE files SET dir = ? || substr(dir, ?)
		                      WHERE user_id = ? AND (dir = ? OR substr(dir, 1, ?) = ?)`,
			to, cut, userID, src.Path, cut, src.Path+"/"); err != nil {
			return fmt.Errorf("moving the contents of %q: %w", src.Path, err)
		}
	}
	return nil
}

// Descendants streams every present file and folder under an entry, deepest
// path last, for callers that need a whole subtree. A folder yields itself too,
// so an empty one still appears in an archive.
func Descendants(db *index.DB, userID int64, e Entry, fn func(Entry) error) error {
	if e.Kind != "folder" {
		return fn(e)
	}
	if err := fn(e); err != nil {
		return err
	}
	cut := utf8.RuneCountInString(e.Path) + 1
	rs, err := db.Query(`SELECT `+columns+` FROM files
	                     WHERE user_id = ? AND state = 'present'
	                       AND (dir = ? OR substr(dir, 1, ?) = ?)
	                     ORDER BY dir, name`, userID, e.Path, cut, e.Path+"/")
	if err != nil {
		return fmt.Errorf("reading %q: %w", e.Path, err)
	}
	defer rs.Close()

	for rs.Next() {
		child, err := scanEntry(rs)
		if err != nil {
			return fmt.Errorf("reading %q: %w", e.Path, err)
		}
		if err := fn(child); err != nil {
			return err
		}
	}
	return rs.Err()
}
