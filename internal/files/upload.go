package files

import (
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/fosslife/drive/internal/index"
	"github.com/fosslife/drive/internal/storage"
)

// ErrOffsetConflict means the client and the server disagree about how much of
// an upload has arrived. The server's answer is the file on disk.
var ErrOffsetConflict = errors.New("upload offset does not match")

// Upload is an in-flight transfer. Its bytes live in a temp file under
// .drive/tmp and appear at Dir/Name only once the last one has arrived, so an
// abandoned upload leaves the destination exactly as it was.
type Upload struct {
	ID      string `json:"id"`
	UserID  int64  `json:"-"`
	Dir     string `json:"dir"`
	Name    string `json:"name"`
	Size    int64  `json:"size"`
	Offset  int64  `json:"offset"`
	Replace bool   `json:"replace"`
	Temp    string `json:"-"`
}

const uploadColumns = `id, user_id, dir, name, size, offset_bytes, temp_name, replace`

func scanUpload(row interface{ Scan(...any) error }) (Upload, error) {
	var u Upload
	err := row.Scan(&u.ID, &u.UserID, &u.Dir, &u.Name, &u.Size, &u.Offset, &u.Temp, &u.Replace)
	return u, err
}

// NewUpload reserves an upload. The space guard runs here rather than at the
// last chunk: refusing 4 GB up front is an error message, refusing it at the
// end is a wasted evening.
func NewUpload(db *index.DB, userID int64, root *storage.Root, dir, name string, size int64, replace bool) (Upload, error) {
	if name == "" || strings.Contains(name, "/") {
		return Upload{}, fmt.Errorf("%w: %q is not a file name", ErrInvalid, name)
	}
	if size < 0 {
		return Upload{}, fmt.Errorf("%w: no upload length given", ErrInvalid)
	}
	// Reject an illegal destination before anything is created, by the same
	// rules every other write obeys. Not existing yet is the normal case; a
	// path that could never be legal is not.
	if _, err := root.Stat(index.JoinPath(dir, name)); errors.Is(err, storage.ErrInvalidPath) {
		return Upload{}, err
	}
	if err := root.CheckSpace(size); err != nil {
		return Upload{}, err
	}

	temp, err := root.CreateUpload()
	if err != nil {
		return Upload{}, err
	}
	id, err := uploadID()
	if err != nil {
		root.DiscardUpload(temp)
		return Upload{}, err
	}

	now := time.Now().Unix()
	if _, err := db.Exec(`INSERT INTO uploads (id, user_id, dir, name, size, temp_name, replace, created_at, updated_at)
	                      VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, userID, dir, name, size, temp, replace, now, now); err != nil {
		root.DiscardUpload(temp)
		return Upload{}, fmt.Errorf("creating upload: %w", err)
	}
	return Upload{ID: id, UserID: userID, Dir: dir, Name: name, Size: size, Replace: replace, Temp: temp}, nil
}

// GetUpload returns an upload scoped to its owner, with the offset read from
// disk rather than from the row: the bytes that were fsynced are the ones that
// are really there, whatever a column recorded before a crash.
func GetUpload(db *index.DB, userID int64, root *storage.Root, id string) (Upload, error) {
	u, err := scanUpload(db.QueryRow(`SELECT `+uploadColumns+` FROM uploads WHERE id = ? AND user_id = ?`, id, userID))
	if errors.Is(err, sql.ErrNoRows) {
		return Upload{}, fmt.Errorf("%w: no such upload", ErrNotFound)
	}
	if err != nil {
		return Upload{}, fmt.Errorf("reading upload: %w", err)
	}
	offset, err := root.UploadOffset(u.Temp)
	if err != nil {
		return Upload{}, fmt.Errorf("reading upload: %w", err)
	}
	u.Offset = offset
	return u, nil
}

// Append writes the next chunk. offset must be where the server actually is, so
// a client that lost track re-reads it rather than writing over a gap.
//
// The returned upload carries the new offset; when it reaches Size the caller
// finishes it.
func Append(db *index.DB, root *storage.Root, u Upload, offset int64, body io.Reader) (Upload, error) {
	if offset != u.Offset {
		return u, fmt.Errorf("%w: server is at %d, client sent %d", ErrOffsetConflict, u.Offset, offset)
	}
	if u.Offset >= u.Size {
		return u, fmt.Errorf("%w: the upload is already complete", ErrOffsetConflict)
	}

	// Never accept more than was declared: the declared length is what the
	// space guard approved.
	written, err := root.AppendUpload(u.Temp, io.LimitReader(body, u.Size-u.Offset))
	u.Offset = written
	// Record what arrived even when the transfer failed partway. The row is
	// what the reclaim sweep reads, and a dropped connection is a resume.
	if _, dbErr := db.Exec(`UPDATE uploads SET offset_bytes = ?, updated_at = ? WHERE id = ?`,
		written, time.Now().Unix(), u.ID); dbErr != nil && err == nil {
		err = dbErr
	}
	return u, err
}

// Finish publishes a completed upload and returns the entry it became.
//
// A destination that is already occupied is either replaced — previous content
// to trash first, so it stays recoverable — or sidestepped with a
// non-colliding name. Neither path can leave the existing file half-written,
// because the new content arrives by rename.
func Finish(db *index.DB, root *storage.Root, u Upload) (Entry, error) {
	if u.Offset != u.Size {
		return Entry{}, fmt.Errorf("%w: %d of %d bytes received", ErrInvalid, u.Offset, u.Size)
	}

	name := u.Name
	switch existing, err := Lookup(db, u.UserID, index.JoinPath(u.Dir, name)); {
	case err == nil && u.Replace:
		if err := Trash(db, u.UserID, root, existing); err != nil {
			return Entry{}, err
		}
	case err == nil:
		if name, err = freeName(db, u.UserID, root, u.Dir, name); err != nil {
			return Entry{}, err
		}
	case !errors.Is(err, ErrNotFound):
		return Entry{}, err
	}
	// The index can be behind the disk. The filesystem decides.
	if _, err := root.Stat(index.JoinPath(u.Dir, name)); err == nil {
		if name, err = freeName(db, u.UserID, root, u.Dir, name); err != nil {
			return Entry{}, err
		}
	}

	dest := index.JoinPath(u.Dir, name)
	info, err := root.FinishUpload(u.Temp, dest)
	if err != nil {
		return Entry{}, err
	}
	// Disk first, index second, and the upload row goes last: if this process
	// dies here the file is already at its path and the reconciler adopts it.
	if _, err := db.Exec(`INSERT INTO files (user_id, dir, name, kind, size, mtime, checksum)
	                      VALUES (?, ?, ?, 'file', ?, ?, ?)`,
		u.UserID, u.Dir, name, info.Size, info.ModTime.Unix(), info.Checksum); err != nil {
		return Entry{}, fmt.Errorf("indexing %q: %w", dest, err)
	}
	if err := indexFolders(db, u.UserID, root, u.Dir); err != nil {
		return Entry{}, err
	}
	if _, err := db.Exec(`DELETE FROM uploads WHERE id = ?`, u.ID); err != nil {
		return Entry{}, fmt.Errorf("closing upload: %w", err)
	}
	return Lookup(db, u.UserID, dest)
}

// Abort discards an upload's temporary data. The destination was never touched,
// so there is nothing to undo.
func Abort(db *index.DB, root *storage.Root, u Upload) error {
	if err := root.DiscardUpload(u.Temp); err != nil {
		return err
	}
	if _, err := db.Exec(`DELETE FROM uploads WHERE id = ?`, u.ID); err != nil {
		return fmt.Errorf("closing upload: %w", err)
	}
	return nil
}

// freeName finds a name next to the one asked for, the way a desktop does:
// report.pdf, then report (2).pdf. It checks the index and the disk, because
// either can hold something the other has not seen yet.
func freeName(db *index.DB, userID int64, root *storage.Root, dir, name string) (string, error) {
	ext := path.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	for i := 2; i < 1000; i++ {
		candidate := fmt.Sprintf("%s (%d)%s", stem, i, ext)
		_, err := Lookup(db, userID, index.JoinPath(dir, candidate))
		if err != nil && !errors.Is(err, ErrNotFound) {
			return "", err
		}
		if err == nil {
			continue
		}
		if _, err := root.Stat(index.JoinPath(dir, candidate)); err == nil {
			continue
		}
		return candidate, nil
	}
	return "", fmt.Errorf("%w: no free name next to %q", ErrExists, name)
}

func uploadID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("naming upload: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// Reclaim discards uploads that have not been touched for olderThan and returns
// how many went. It only ever removes temp data: an upload has no presence at
// its destination until it completes, so nothing user-visible can be affected.
func Reclaim(db *index.DB, dataDir string, olderThan time.Duration) (int, error) {
	cutoff := time.Now().Add(-olderThan).Unix()
	rs, err := db.Query(`SELECT u.id, u.temp_name, s.storage_root
	                     FROM uploads u JOIN users s ON s.id = u.user_id
	                     WHERE u.updated_at < ?`, cutoff)
	if err != nil {
		return 0, fmt.Errorf("finding stale uploads: %w", err)
	}
	type stale struct{ id, temp, root string }
	var found []stale
	for rs.Next() {
		var s stale
		if err := rs.Scan(&s.id, &s.temp, &s.root); err != nil {
			rs.Close()
			return 0, fmt.Errorf("finding stale uploads: %w", err)
		}
		found = append(found, s)
	}
	rs.Close()
	if err := rs.Err(); err != nil {
		return 0, fmt.Errorf("finding stale uploads: %w", err)
	}

	reclaimed := 0
	for _, s := range found {
		root, err := storage.Open(filepath.Join(dataDir, filepath.FromSlash(s.root)), 0)
		if err != nil {
			slog.Error("reclaiming upload", "upload", s.id, "error", err)
			continue
		}
		err = root.DiscardUpload(s.temp)
		root.Close()
		if err != nil {
			slog.Error("reclaiming upload", "upload", s.id, "error", err)
			continue
		}
		if _, err := db.Exec(`DELETE FROM uploads WHERE id = ?`, s.id); err != nil {
			slog.Error("reclaiming upload", "upload", s.id, "error", err)
			continue
		}
		reclaimed++
	}
	return reclaimed, nil
}
