package index

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func openTemp(t *testing.T) *DB {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "index.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestOpenWritesAndReadsBack(t *testing.T) {
	db := openTemp(t)

	var mode string
	if err := db.QueryRow("PRAGMA journal_mode").Scan(&mode); err != nil {
		t.Fatalf("reading journal mode: %v", err)
	}
	if !strings.EqualFold(mode, "wal") {
		t.Errorf("journal_mode = %q, want wal", mode)
	}

	if _, err := db.Exec(`INSERT INTO settings (key, value) VALUES ('smoke', 'ok')`); err != nil {
		t.Fatalf("write: %v", err)
	}
	var value string
	if err := db.QueryRow(`SELECT value FROM settings WHERE key = 'smoke'`).Scan(&value); err != nil {
		t.Fatalf("read: %v", err)
	}
	if value != "ok" {
		t.Errorf("read back %q, want ok", value)
	}
}

func TestMigrateFromEmptyDirectory(t *testing.T) {
	dir := t.TempDir()
	db, err := Open(filepath.Join(dir, "index.db"))
	if err != nil {
		t.Fatalf("Open on an empty directory: %v", err)
	}
	defer db.Close()

	var version int
	if err := db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		t.Fatalf("reading schema version: %v", err)
	}
	if version != Version() {
		t.Errorf("schema version = %d, want %d", version, Version())
	}

	for _, table := range []string{"users", "files", "sessions", "api_tokens", "shares", "uploads", "settings"} {
		var name string
		err := db.QueryRow(`SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&name)
		if err != nil {
			t.Errorf("table %s missing after migration: %v", table, err)
		}
	}
}

func TestRefusesNewerSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "index.db")
	db, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO settings (key, value) VALUES ('marker', 'untouched')`); err != nil {
		t.Fatalf("seeding index: %v", err)
	}
	future := Version() + 41
	if _, err := db.Exec(fmt.Sprintf("PRAGMA user_version = %d", future)); err != nil {
		t.Fatalf("setting future version: %v", err)
	}
	db.Close()

	_, err = Open(path)
	var versionErr *VersionError
	if !errors.As(err, &versionErr) {
		t.Fatalf("Open returned %v, want a *VersionError", err)
	}
	if versionErr.Found != future || versionErr.Supported != Version() {
		t.Errorf("VersionError = %+v, want found %d supported %d", versionErr, future, Version())
	}

	// The refused index must be left exactly as it was found.
	after, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("reopening index: %v", err)
	}
	defer after.Close()
	var version int
	if err := after.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		t.Fatalf("reading schema version: %v", err)
	}
	if version != future {
		t.Errorf("schema version = %d, want %d unchanged", version, future)
	}
	var marker string
	if err := after.QueryRow(`SELECT value FROM settings WHERE key = 'marker'`).Scan(&marker); err != nil {
		t.Fatalf("reading marker row: %v", err)
	}
	if marker != "untouched" {
		t.Errorf("marker = %q, want untouched", marker)
	}
}

func TestFailingMigrationAbortsAndTouchesNoUserFile(t *testing.T) {
	dir := t.TempDir()
	userFile := filepath.Join(dir, "taxes.pdf")
	if err := os.WriteFile(userFile, []byte("user bytes"), 0o600); err != nil {
		t.Fatalf("seeding user file: %v", err)
	}

	_, err := open(filepath.Join(dir, "index.db"), []string{schemaV1, `CREATE TABLE files (this is not sql)`})
	if err == nil {
		t.Fatal("open succeeded with a broken migration, want an error")
	}
	if !strings.Contains(err.Error(), "migration 2") {
		t.Errorf("error %q does not name the migration that failed", err)
	}

	content, readErr := os.ReadFile(userFile)
	if readErr != nil || string(content) != "user bytes" {
		t.Errorf("user file was modified: content %q, err %v", content, readErr)
	}

	// The failed migration rolled back; the index stays at the last good version.
	db, err := sql.Open("sqlite", filepath.Join(dir, "index.db"))
	if err != nil {
		t.Fatalf("reopening index: %v", err)
	}
	defer db.Close()
	var version int
	if err := db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		t.Fatalf("reading schema version: %v", err)
	}
	if version != 1 {
		t.Errorf("schema version = %d, want 1", version)
	}
}

func TestIdentifiersAreMonotonicAndNotRecycled(t *testing.T) {
	db := openTemp(t)

	res, err := db.Exec(`INSERT INTO users (username, password_hash, storage_root, created_at)
	                     VALUES ('ada', 'x', 'users/ada', 0)`)
	if err != nil {
		t.Fatalf("creating user: %v", err)
	}
	userID, _ := res.LastInsertId()

	insert := func(name string) int64 {
		t.Helper()
		res, err := db.Exec(`INSERT INTO files (user_id, dir, name, kind, size, mtime)
		                     VALUES (?, '', ?, 'file', 0, 0)`, userID, name)
		if err != nil {
			t.Fatalf("inserting %s: %v", name, err)
		}
		id, _ := res.LastInsertId()
		return id
	}

	first, second := insert("a.txt"), insert("b.txt")
	if second <= first {
		t.Errorf("identifiers are not monotonic: %d then %d", first, second)
	}

	if _, err := db.Exec(`DELETE FROM files WHERE id = ?`, second); err != nil {
		t.Fatalf("deleting: %v", err)
	}
	third := insert("c.txt")
	if third <= second {
		t.Errorf("identifier %d was recycled after deleting %d", third, second)
	}
}
