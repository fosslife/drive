// Package index opens and migrates the SQLite index.
//
// The index is a cache: the filesystem is the source of truth. Nothing here may
// hold user-visible state that cannot be rebuilt by scanning the storage roots.
package index

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

type DB struct {
	*sql.DB
}

// Version is the schema version this binary understands.
func Version() int { return len(migrations) }

// Open opens the index at path, creating it if absent, and applies any pending
// migrations. It fails rather than running against an index it does not
// understand or could not fully migrate.
func Open(path string) (*DB, error) { return open(path, migrations) }

// OpenOrReset opens the index and, if it cannot be opened or migrated, moves it
// aside and starts an empty one, returning where the old file went. The index
// holds no file content — the reconciler rebuilds it by scanning the storage
// roots — so a corrupt index is a setback, not a reason to refuse to start.
//
// The cost of a reset is real and is not file data: accounts, API tokens, and
// share links live only here. That is why the damaged file is kept rather than
// removed, and why a VersionError is never reset: an index written by a newer
// binary is ahead of us, not broken, and discarding it would destroy state the
// newer binary can still read.
func OpenOrReset(path string) (db *DB, movedTo string, err error) {
	db, err = Open(path)
	if err == nil {
		return db, "", nil
	}
	var ve *VersionError
	if errors.As(err, &ve) {
		return nil, "", err
	}
	if _, statErr := os.Stat(path); statErr != nil {
		// No file to move aside, so the failure is the directory or the driver.
		// Resetting cannot help and would hide the real cause.
		return nil, "", err
	}

	movedTo = path + ".corrupt-" + time.Now().UTC().Format("20060102T150405Z")
	// The write-ahead log and shared-memory files belong to the same database;
	// leaving them behind would corrupt the replacement too.
	for _, suffix := range []string{"", "-wal", "-shm"} {
		if _, err := os.Stat(path + suffix); err == nil {
			if err := os.Rename(path+suffix, movedTo+suffix); err != nil {
				return nil, "", fmt.Errorf("moving unreadable index aside: %w", err)
			}
		}
	}
	db, err = Open(path)
	if err != nil {
		return nil, movedTo, err
	}
	return db, movedTo, nil
}

func open(path string, migs []string) (*DB, error) {
	dsn := "file:" + path +
		"?_pragma=journal_mode(WAL)" +
		"&_pragma=busy_timeout(5000)" +
		"&_pragma=foreign_keys(on)" +
		"&_pragma=synchronous(NORMAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("opening index %s: %w", path, err)
	}
	// ponytail: one connection is the single writer, and the index is small and
	// read-mostly. Add a second read-only pool if reads ever queue behind writes.
	db.SetMaxOpenConns(1)
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("opening index %s: %w", path, err)
	}
	if err := migrate(db, migs); err != nil {
		db.Close()
		return nil, err
	}
	return &DB{db}, nil
}

// VersionError reports an index written by a newer binary. Downgrading would
// mean writing against a schema we do not understand, so we refuse instead.
type VersionError struct {
	Found, Supported int
}

func (e *VersionError) Error() string {
	return fmt.Sprintf("index schema is version %d but this binary understands at most %d: run the newer version of drive", e.Found, e.Supported)
}

// migrate applies pending migrations in one transaction each, so a failure
// leaves the index at the last version that applied cleanly.
func migrate(db *sql.DB, migs []string) error {
	var current int
	if err := db.QueryRow("PRAGMA user_version").Scan(&current); err != nil {
		return fmt.Errorf("reading index schema version: %w", err)
	}
	if current > len(migs) {
		return &VersionError{Found: current, Supported: len(migs)}
	}
	for i := current; i < len(migs); i++ {
		version := i + 1
		if err := applyOne(db, version, migs[i]); err != nil {
			return err
		}
	}
	return nil
}

func applyOne(db *sql.DB, version int, stmt string) error {
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("migration %d: %w", version, err)
	}
	defer tx.Rollback()

	if _, err := tx.Exec(stmt); err != nil {
		return fmt.Errorf("migration %d failed, index left at version %d: %w", version, version-1, err)
	}
	// PRAGMA does not take a placeholder; version is an int from our own slice.
	if _, err := tx.Exec(fmt.Sprintf("PRAGMA user_version = %d", version)); err != nil {
		return fmt.Errorf("migration %d: recording schema version: %w", version, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("migration %d: %w", version, err)
	}
	return nil
}

// SplitPath separates a relative path into the (dir, name) pair the files table
// stores, and JoinPath puts it back. A top-level entry has dir "", not ".":
// these two functions are the only place that knows it.
func SplitPath(p string) (dir, name string) {
	if i := strings.LastIndexByte(p, '/'); i >= 0 {
		return p[:i], p[i+1:]
	}
	return "", p
}

func JoinPath(dir, name string) string {
	if dir == "" {
		return name
	}
	return dir + "/" + name
}

// Querier is the part of *DB that a read needs, and *sql.Tx satisfies it too.
// It exists so one query can be asked either on its own or inside somebody
// else's transaction.
type Querier interface {
	QueryRow(query string, args ...any) *sql.Row
}

// Usage is what one account occupies.
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

// MeasureUsage counts what an account holds rather than reading a total kept
// somewhere. The index is the disposable half of this system: a stored counter
// would be authoritative state for user-visible behaviour living in the half
// that gets thrown away and rebuilt, with a drift bug waiting at every path
// that frees or consumes bytes.
//
// ponytail: one aggregate over one user's rows, at upload creation and when an
// administrator opens a screen. A maintained counter only if a folder of
// 100,000 files makes it show up in upload latency.
func MeasureUsage(q Querier, userID int64) (Usage, error) {
	var u Usage
	err := q.QueryRow(`SELECT COALESCE(SUM(size), 0), COUNT(*) FROM files
	                    WHERE user_id = ? AND kind = 'file' AND state IN ('present', 'trashed')`,
		userID).Scan(&u.Bytes, &u.Files)
	if err != nil {
		return Usage{}, fmt.Errorf("measuring account usage: %w", err)
	}
	if err := q.QueryRow(`SELECT COALESCE(SUM(size), 0) FROM uploads WHERE user_id = ?`,
		userID).Scan(&u.Pending); err != nil {
		return Usage{}, fmt.Errorf("measuring account usage: %w", err)
	}
	return u, nil
}
