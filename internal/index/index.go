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
