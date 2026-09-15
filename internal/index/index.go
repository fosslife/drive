// Package index opens and migrates the SQLite index.
//
// The index is a cache: the filesystem is the source of truth. Nothing here may
// hold user-visible state that cannot be rebuilt by scanning the storage roots.
package index

import (
	"database/sql"
	"fmt"

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
