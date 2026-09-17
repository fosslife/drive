package files

import (
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/fosslife/drive/internal/index"
	"github.com/fosslife/drive/internal/storage"
)

// TrashEntry is one deleted item as the trash shows it. Path is where it came
// from, which is where restoring puts it back.
type TrashEntry struct {
	Entry
	DeletedAt time.Time `json:"deleted"`
}

// Trash moves an entry out of the user's tree into .drive/trash, where it stops
// appearing in listings and searches but keeps its original path as the restore
// target. A folder takes its contents with it, as one unit.
//
// It destroys nothing. Everything that removes something a user can see funnels
// through here; only Purge frees a byte.
func Trash(db *index.DB, userID int64, root *storage.Root, e Entry) error {
	if err := root.Trash(e.ID, e.Path); err != nil {
		return err
	}
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("trashing %q: %w", e.Path, err)
	}
	defer tx.Rollback()

	now := time.Now().Unix()
	if _, err := tx.Exec(`UPDATE files SET state = 'trashed', trashed_at = ?, trash_root = ? WHERE id = ?`,
		now, e.ID, e.ID); err != nil {
		return fmt.Errorf("trashing %q: %w", e.Path, err)
	}
	if e.Kind == "folder" {
		// The whole subtree went with it in one rename. Marking each row keeps
		// them out of listings, stops the reconciler reporting them as vanished,
		// and records which deletion they belong to so a restore takes them all.
		cut := utf8.RuneCountInString(e.Path) + 1
		if _, err := tx.Exec(`UPDATE files SET state = 'trashed', trashed_at = ?, trash_root = ?
		                      WHERE user_id = ? AND state = 'present'
		                        AND (dir = ? OR substr(dir, 1, ?) = ?)`,
			now, e.ID, userID, e.Path, cut, e.Path+"/"); err != nil {
			return fmt.Errorf("trashing the contents of %q: %w", e.Path, err)
		}
	}
	return tx.Commit()
}

// ListTrash returns one page of a user's deleted items, newest first. Only the
// items that were actually deleted appear: a folder is one row, not one row per
// file inside it.
//
// The cursor is opaque and comes from the previous page, "" at the end.
func ListTrash(db *index.DB, userID int64, cursor string, limit int) (entries []TrashEntry, next string, err error) {
	if limit <= 0 || limit > MaxPageSize {
		limit = DefaultPageSize
	}
	// Keyset again, on the ordering key: identity breaks the tie because a
	// folder and its neighbour can be deleted in the same second.
	at, id, err := splitTrashCursor(cursor)
	if err != nil {
		return nil, "", err
	}
	rs, err := db.Query(`SELECT `+columns+`, trashed_at FROM files
	                     WHERE user_id = ? AND state = 'trashed' AND trash_root = id
	                       AND (trashed_at < ? OR (trashed_at = ? AND id < ?))
	                     ORDER BY trashed_at DESC, id DESC LIMIT ?`,
		userID, at, at, id, limit)
	if err != nil {
		return nil, "", fmt.Errorf("listing the trash: %w", err)
	}
	defer rs.Close()

	entries = []TrashEntry{}
	for rs.Next() {
		var (
			t         TrashEntry
			deletedAt int64
		)
		t.Entry, err = scanEntry(rowWithExtra{rs, &deletedAt})
		if err != nil {
			return nil, "", fmt.Errorf("listing the trash: %w", err)
		}
		t.DeletedAt = time.Unix(deletedAt, 0)
		entries = append(entries, t)
	}
	if err := rs.Err(); err != nil {
		return nil, "", fmt.Errorf("listing the trash: %w", err)
	}
	if len(entries) == limit {
		last := entries[len(entries)-1]
		next = fmt.Sprintf("%d.%d", last.DeletedAt.Unix(), last.ID)
	}
	return entries, next, nil
}

// rowWithExtra lets scanEntry read the standard columns while the caller takes
// one it appended to the same SELECT.
type rowWithExtra struct {
	row   interface{ Scan(...any) error }
	extra any
}

func (r rowWithExtra) Scan(dest ...any) error { return r.row.Scan(append(dest, r.extra)...) }

func splitTrashCursor(cursor string) (trashedAt, id int64, err error) {
	if cursor == "" {
		// Before the first row of a descending scan: everything is older than
		// the end of time.
		return 1<<62 - 1, 1<<62 - 1, nil
	}
	at, rest, ok := strings.Cut(cursor, ".")
	trashedAt, err1 := strconv.ParseInt(at, 10, 64)
	id, err2 := strconv.ParseInt(rest, 10, 64)
	if !ok || err1 != nil || err2 != nil {
		return 0, 0, fmt.Errorf("%w: %q is not a cursor from a previous page", ErrInvalid, cursor)
	}
	return trashedAt, id, nil
}

// Restore puts a trashed item back at its original path, folder contents and
// all, recreating a parent folder that has been deleted since.
//
// If something else holds the path now, the item lands beside it under a free
// name and the returned entry says where it actually went: a restore reports a
// fallback rather than failing or overwriting, because either loses a file.
func Restore(db *index.DB, userID int64, root *storage.Root, id int64) (Entry, error) {
	e, err := trashedRoot(db, userID, id)
	if err != nil {
		return Entry{}, err
	}

	dir, name := index.SplitPath(e.Path)
	// The index and the disk can each hold something the other has not seen, so
	// both get a say in whether the original path is free.
	switch _, err := Lookup(db, userID, e.Path); {
	case err == nil:
		if name, err = freeName(db, userID, root, dir, name); err != nil {
			return Entry{}, err
		}
	case !errors.Is(err, ErrNotFound):
		return Entry{}, err
	}
	dest := index.JoinPath(dir, name)
	if _, err := root.Stat(dest); err == nil {
		if name, err = freeName(db, userID, root, dir, name); err != nil {
			return Entry{}, err
		}
		dest = index.JoinPath(dir, name)
	}

	// Disk first, as always: if the index update below never happens the file is
	// back where the user asked for it and the reconciler adopts it there.
	if err := root.Restore(id, dest); err != nil {
		if errors.Is(err, storage.ErrExists) {
			return Entry{}, fmt.Errorf("%w: %q", ErrExists, dest)
		}
		return Entry{}, err
	}

	tx, err := db.Begin()
	if err != nil {
		return Entry{}, fmt.Errorf("restoring %q: %w", e.Path, err)
	}
	defer tx.Rollback()
	// Rename before un-trashing, not after: the unique path index ignores
	// trashed rows, so a row made present at an occupied path would collide.
	if dest != e.Path {
		if err := reparent(tx, userID, e, dest); err != nil {
			return Entry{}, err
		}
	}
	if _, err := tx.Exec(`UPDATE files SET state = 'present', trashed_at = NULL, trash_root = NULL
	                      WHERE user_id = ? AND trash_root = ?`, userID, id); err != nil {
		return Entry{}, fmt.Errorf("restoring %q: %w", e.Path, err)
	}
	if err := tx.Commit(); err != nil {
		return Entry{}, fmt.Errorf("restoring %q: %w", e.Path, err)
	}
	// The parent may have been recreated on disk just now by root.Restore.
	if err := indexFolders(db, userID, root, dir); err != nil {
		return Entry{}, err
	}
	return Lookup(db, userID, dest)
}

// Purge permanently deletes one trashed item and everything under it.
//
// This is the only code path in the application that destroys user file
// content. Nothing else may grow one.
func Purge(db *index.DB, userID int64, root *storage.Root, id int64) error {
	e, err := trashedRoot(db, userID, id)
	if err != nil {
		return err
	}
	// The thumbnails go first, and for the whole subtree: a permanent delete
	// that leaves a picture of the file behind has not deleted it.
	ids, err := subtreeIDs(db, userID, id)
	if err != nil {
		return err
	}
	for _, fileID := range ids {
		if err := root.DiscardThumb(fileID); err != nil {
			return err
		}
	}
	if err := root.Purge(id); err != nil {
		return err
	}
	if _, err := db.Exec(`DELETE FROM files WHERE user_id = ? AND trash_root = ?`, userID, id); err != nil {
		return fmt.Errorf("deleting %q: %w", e.Path, err)
	}
	return nil
}

// EmptyTrash purges everything in a user's trash and returns how much went.
func EmptyTrash(db *index.DB, userID int64, root *storage.Root) (int, error) {
	ids, err := trashedRootIDs(db, userID)
	if err != nil {
		return 0, err
	}
	for i, id := range ids {
		if err := Purge(db, userID, root, id); err != nil {
			return i, err
		}
	}
	return len(ids), nil
}

// ExpireTrash purges items deleted longer ago than olderThan, across every
// user, and returns how many went. A retention of zero means never expire:
// keeping deleted files forever is a legitimate choice, and it is the one
// setting where doing nothing is the safe failure.
func ExpireTrash(db *index.DB, dataDir string, olderThan time.Duration) (int, error) {
	if olderThan <= 0 {
		return 0, nil
	}
	cutoff := time.Now().Add(-olderThan).Unix()
	rs, err := db.Query(`SELECT f.id, f.user_id, u.storage_root FROM files f
	                     JOIN users u ON u.id = f.user_id
	                     WHERE f.state = 'trashed' AND f.trash_root = f.id AND f.trashed_at < ?`, cutoff)
	if err != nil {
		return 0, fmt.Errorf("finding expired trash: %w", err)
	}
	type owner struct {
		userID int64
		root   string
	}
	due := map[owner][]int64{}
	for rs.Next() {
		var (
			id int64
			o  owner
		)
		if err := rs.Scan(&id, &o.userID, &o.root); err != nil {
			rs.Close()
			return 0, fmt.Errorf("finding expired trash: %w", err)
		}
		due[o] = append(due[o], id)
	}
	rs.Close()
	if err := rs.Err(); err != nil {
		return 0, fmt.Errorf("finding expired trash: %w", err)
	}

	purged := 0
	for o, ids := range due {
		root, err := storage.Open(filepath.Join(dataDir, filepath.FromSlash(o.root)), 0)
		if err != nil {
			// One unreachable root must not stop the others, the same way the
			// reconciler skips it rather than giving up on every account.
			slog.Error("expiring trash", "root", o.root, "error", err)
			continue
		}
		for _, id := range ids {
			if err := Purge(db, o.userID, root, id); err != nil {
				slog.Error("expiring trash", "item", id, "error", err)
				continue
			}
			purged++
		}
		root.Close()
	}
	return purged, nil
}

// trashedRoot finds one deleted item by identity. A row that is merely inside
// something deleted is not one: it is restored and purged with its parent.
func trashedRoot(db *index.DB, userID, id int64) (Entry, error) {
	e, err := scanEntry(db.QueryRow(`SELECT `+columns+` FROM files
	                                 WHERE id = ? AND user_id = ? AND state = 'trashed' AND trash_root = id`,
		id, userID))
	if errors.Is(err, sql.ErrNoRows) {
		return Entry{}, fmt.Errorf("%w: nothing deleted with id %d", ErrNotFound, id)
	}
	if err != nil {
		return Entry{}, fmt.Errorf("reading the trash: %w", err)
	}
	return e, nil
}

func trashedRootIDs(db *index.DB, userID int64) ([]int64, error) {
	return queryIDs(db, `SELECT id FROM files
	                     WHERE user_id = ? AND state = 'trashed' AND trash_root = id`, userID)
}

// subtreeIDs is every row that went to the trash in one deletion, the deleted
// entry included.
func subtreeIDs(db *index.DB, userID, trashRoot int64) ([]int64, error) {
	return queryIDs(db, `SELECT id FROM files WHERE user_id = ? AND trash_root = ?`, userID, trashRoot)
}

func queryIDs(db *index.DB, query string, args ...any) ([]int64, error) {
	rs, err := db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("reading the trash: %w", err)
	}
	defer rs.Close()
	var ids []int64
	for rs.Next() {
		var id int64
		if err := rs.Scan(&id); err != nil {
			return nil, fmt.Errorf("reading the trash: %w", err)
		}
		ids = append(ids, id)
	}
	return ids, rs.Err()
}
