// Package scan reconciles the index with the filesystem.
//
// The filesystem wins every disagreement. There is deliberately no code path
// in here that deletes, moves, or truncates a file, and no statement that
// deletes an index row: a file that vanished marks its row missing, and a root
// that cannot be read aborts the scan whole. Those two rules are the reason
// this package exists as its own thing instead of being folded into storage.
package scan

import (
	"fmt"
	"strconv"

	"github.com/fosslife/drive/internal/index"
	"github.com/fosslife/drive/internal/storage"
)

// Result counts what one scan saw and what it changed.
type Result struct {
	Files, Dirs int // entries found on disk
	Hashed      int // files whose contents were read, the expensive part
	Added       int
	Updated     int
	Moved       int // identities preserved across an external move
	Missing     int // rows whose file is no longer on disk
}

// row is an indexed entry as the last scan left it.
type row struct {
	id       int64
	dir      string
	name     string
	kind     string
	size     int64
	mtime    int64
	checksum string
	state    string
}

func (r row) path() string { return index.JoinPath(r.dir, r.name) }

// action is one pending index write. The walk collects these without touching
// the index, so a walk that fails partway is a read-only no-op.
type action struct {
	stmt string
	args []any
}

// batchSize bounds one transaction. Writing in batches means a long rebuild
// becomes browsable as it progresses instead of appearing at the very end.
const batchSize = 500

// progressEvery bounds how often a scan reports progress: often enough to look
// alive, rarely enough that the status lock is not the bottleneck.
const progressEvery = 200

// Scan reconciles one user's storage root with the index and reports what it
// found. It is safe to run while requests are being served, and safe to run
// repeatedly: an unchanged file costs one stat.
//
// progress, when non-nil, is called with the number of entries walked so far,
// so a rebuild that takes minutes can say how far it has got. It is called
// from the scanning goroutine and must not block.
func Scan(db *index.DB, userID int64, root *storage.Root, progress func(entries int)) (Result, error) {
	known, err := loadRows(db, userID)
	if err != nil {
		return Result{}, err
	}

	var (
		res      Result
		updates  []action
		appeared []storage.Entry
		sums     = map[string]string{} // path of an appeared file -> checksum
	)

	err = root.Walk(func(e storage.Entry) error {
		kind := "file"
		if e.IsDir {
			kind = "folder"
			res.Dirs++
		} else {
			res.Files++
		}
		if progress != nil && (res.Files+res.Dirs)%progressEvery == 0 {
			progress(res.Files + res.Dirs)
		}
		mtime := e.ModTime.Unix()

		cur, isKnown := known[e.Path]
		if !isKnown {
			if e.IsDir {
				res.Added++
				dir, name := index.SplitPath(e.Path)
				updates = append(updates, action{
					`INSERT INTO files (user_id, dir, name, kind, size, mtime) VALUES (?, ?, ?, 'folder', 0, ?)`,
					[]any{userID, dir, name, mtime},
				})
				return nil
			}
			info, err := root.Checksum(e.Path)
			if err != nil {
				return err
			}
			res.Hashed++
			appeared = append(appeared, e)
			sums[e.Path] = info.Checksum
			return nil
		}

		delete(known, e.Path) // what is left over vanished from disk
		// The fast path: same kind, same size, same mtime, already hashed and
		// already present. Costs one stat and writes nothing.
		//
		// ponytail: mtime is compared at one-second resolution, so a file
		// rewritten to the same size within the same second is not rehashed.
		// storage.Verify is the backstop; store UnixNano if that ever bites.
		if cur.kind == kind && cur.state == "present" && cur.mtime == mtime &&
			(e.IsDir || (cur.size == e.Size && cur.checksum != "")) {
			return nil
		}

		checksum := ""
		if !e.IsDir {
			info, err := root.Checksum(e.Path)
			if err != nil {
				return err
			}
			res.Hashed++
			checksum = info.Checksum
		}
		res.Updated++
		updates = append(updates, action{
			`UPDATE files SET kind = ?, size = ?, mtime = ?, checksum = ?, state = 'present' WHERE id = ?`,
			[]any{kind, e.Size, mtime, nullable(checksum), cur.id},
		})
		return nil
	})
	if err != nil {
		// Nothing has been written yet, and nothing will be. The index keeps
		// describing the last state we could actually read.
		return Result{}, fmt.Errorf("scanning %s: %w", root.Dir(), err)
	}
	if progress != nil {
		progress(res.Files + res.Dirs)
	}

	moved := matchMoves(known, appeared, sums)
	for path, m := range moved {
		res.Moved++
		dir, name := index.SplitPath(path)
		updates = append(updates, action{
			`UPDATE files SET dir = ?, name = ?, mtime = ?, state = 'present' WHERE id = ?`,
			[]any{dir, name, m.entry.ModTime.Unix(), m.row.id},
		})
		delete(known, m.row.path())
	}

	for _, e := range appeared {
		if _, wasMove := moved[e.Path]; wasMove {
			continue
		}
		res.Added++
		dir, name := index.SplitPath(e.Path)
		updates = append(updates, action{
			`INSERT INTO files (user_id, dir, name, kind, size, mtime, checksum) VALUES (?, ?, ?, 'file', ?, ?, ?)`,
			[]any{userID, dir, name, e.Size, e.ModTime.Unix(), sums[e.Path]},
		})
	}

	// Everything still in known is indexed but absent from disk. It is marked,
	// never removed: the file may be on an unmounted disk, and the row is the
	// only record that it ever existed.
	for _, cur := range known {
		if cur.state == "missing" {
			continue
		}
		res.Missing++
		updates = append(updates, action{
			`UPDATE files SET state = 'missing' WHERE id = ?`,
			[]any{cur.id},
		})
	}

	if err := apply(db, updates); err != nil {
		return Result{}, err
	}
	return res, nil
}

// move is an indexed row recognised at a new path.
type move struct {
	row   row
	entry storage.Entry
}

// matchMoves pairs a file that vanished with one that appeared when their
// contents and size match exactly one candidate on each side. An external `mv`
// looks like a delete plus a create, and this is what keeps identity across it.
// Two byte-identical files are ambiguous, so both sides are left alone and the
// new path gets a new identity rather than a guessed one.
func matchMoves(vanished map[string]row, appeared []storage.Entry, sums map[string]string) map[string]move {
	from := map[string][]row{}
	for _, cur := range vanished {
		if cur.kind == "file" && cur.state == "present" && cur.checksum != "" {
			key := contentKey(cur.checksum, cur.size)
			from[key] = append(from[key], cur)
		}
	}
	to := map[string][]storage.Entry{}
	for _, e := range appeared {
		key := contentKey(sums[e.Path], e.Size)
		to[key] = append(to[key], e)
	}

	moves := map[string]move{}
	for key, candidates := range to {
		if len(candidates) != 1 || len(from[key]) != 1 {
			continue
		}
		moves[candidates[0].Path] = move{row: from[key][0], entry: candidates[0]}
	}
	return moves
}

func contentKey(checksum string, size int64) string {
	return checksum + ":" + strconv.FormatInt(size, 10)
}

// loadRows reads a user's indexed entries, keyed by path. Trashed rows are
// excluded: their path is a restore target, not a claim about what is on disk.
//
// ponytail: one root's index held in memory, roughly 150 bytes a file. Page it
// by directory if a single user ever holds tens of millions of files.
func loadRows(db *index.DB, userID int64) (map[string]row, error) {
	rs, err := db.Query(`SELECT id, dir, name, kind, size, mtime, IFNULL(checksum, ''), state
	                     FROM files WHERE user_id = ? AND state <> 'trashed'`, userID)
	if err != nil {
		return nil, fmt.Errorf("reading the index for user %d: %w", userID, err)
	}
	defer rs.Close()

	out := map[string]row{}
	for rs.Next() {
		var r row
		if err := rs.Scan(&r.id, &r.dir, &r.name, &r.kind, &r.size, &r.mtime, &r.checksum, &r.state); err != nil {
			return nil, fmt.Errorf("reading the index for user %d: %w", userID, err)
		}
		out[r.path()] = r
	}
	return out, rs.Err()
}

func apply(db *index.DB, updates []action) error {
	for len(updates) > 0 {
		n := min(batchSize, len(updates))
		if err := applyBatch(db, updates[:n]); err != nil {
			return err
		}
		updates = updates[n:]
	}
	return nil
}

func applyBatch(db *index.DB, updates []action) error {
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("updating the index: %w", err)
	}
	defer tx.Rollback()

	for _, u := range updates {
		if _, err := tx.Exec(u.stmt, u.args...); err != nil {
			return fmt.Errorf("updating the index: %w", err)
		}
	}
	return tx.Commit()
}

// nullable keeps the checksum column NULL for folders rather than empty.
func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}
