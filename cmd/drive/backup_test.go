package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fosslife/drive/internal/auth"
	"github.com/fosslife/drive/internal/files"
	"github.com/fosslife/drive/internal/index"
	"github.com/fosslife/drive/internal/scan"
	"github.com/fosslife/drive/internal/share"
)

const backupContent = "the quick brown fox\n"

// 14.6: a backup restored into a fresh data directory is the same instance —
// same accounts, same files, same shares.
func TestBackupRestoresEverything(t *testing.T) {
	requireRsync(t)
	data := t.TempDir()
	populate(t, data)

	backup := filepath.Join(t.TempDir(), "backup")
	runScript(t, "backup", data, backup)
	restored := filepath.Join(t.TempDir(), "restored")
	runScript(t, "restore", backup, restored)

	db := openIndex(t, restored)
	users := auth.NewStore(db, restored, 0)
	ada, err := users.Authenticate("ada", "correct horse battery staple")
	if err != nil {
		t.Fatalf("logging in after a restore: %v", err)
	}
	if links, err := share.List(db, ada.ID); err != nil || len(links) != 1 {
		t.Errorf("share links after a restore: %v, %v, want one", links, err)
	}
	entry, err := files.Lookup(db, ada.ID, "notes.txt")
	if err != nil {
		t.Fatalf("notes.txt is not in the restored index: %v", err)
	}
	if entry.Size != int64(len(backupContent)) {
		t.Errorf("restored size %d, want %d", entry.Size, len(backupContent))
	}
	if got := read(t, filepath.Join(restored, "users", "ada", "notes.txt")); got != backupContent {
		t.Errorf("restored content %q, want %q", got, backupContent)
	}

	// The trash came along too; a file deleted yesterday is still a file.
	if _, err := os.Stat(filepath.Join(restored, "users", "ada", ".drive", "trash")); err != nil {
		t.Errorf("trash was not backed up: %v", err)
	}
	// And the half-written upload did not: it is data nobody has seen.
	if _, err := os.Stat(filepath.Join(restored, "users", "ada", ".drive", "tmp", "abandoned")); !os.IsNotExist(err) {
		t.Errorf("an in-flight upload was backed up: %v", err)
	}
}

// 14.6: and with the index left out it is still a working drive, once the
// accounts are created again. This is the promise the whole design rests on —
// the filesystem is the source of truth, the index is disposable.
func TestRestoringFilesAloneStillWorks(t *testing.T) {
	requireRsync(t)
	data := t.TempDir()
	populate(t, data)

	backup := filepath.Join(t.TempDir(), "backup")
	runScript(t, "backup", data, backup)
	if err := os.Remove(filepath.Join(backup, "index.db")); err != nil {
		t.Fatal(err)
	}
	restored := filepath.Join(t.TempDir(), "restored")
	runScript(t, "restore", backup, restored)

	db := openIndex(t, restored)
	users := auth.NewStore(db, restored, 0)
	if _, err := users.Authenticate("ada", "correct horse battery staple"); err == nil {
		t.Error("the account survived a backup without the index, which it cannot have")
	}

	// Same username, so the same storage root, which is the whole point of
	// naming roots after the user rather than after a row id.
	ada, err := users.Create("ada", "a different password entirely", true)
	if err != nil {
		t.Fatal(err)
	}
	root, err := users.Root(ada)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if _, err := scan.Scan(db, ada.ID, root, nil); err != nil {
		t.Fatal(err)
	}

	entry, err := files.Lookup(db, ada.ID, "notes.txt")
	if err != nil {
		t.Fatalf("notes.txt was not re-adopted by the rebuilt index: %v", err)
	}
	if entry.Size != int64(len(backupContent)) {
		t.Errorf("re-indexed size %d, want %d", entry.Size, len(backupContent))
	}
	if got := read(t, filepath.Join(restored, "users", "ada", "notes.txt")); got != backupContent {
		t.Errorf("content after a files-only restore is %q, want %q", got, backupContent)
	}
	if entries, _, err := files.Search(db, ada.ID, "notes", 10); err != nil || len(entries) != 1 {
		t.Errorf("searching the rebuilt index found %v (%v), want notes.txt", entries, err)
	}
}

// populate builds a data directory with everything a backup has to carry: an
// account, a file, a share link, something in the trash, and an upload that
// was never finished.
func populate(t *testing.T, data string) {
	t.Helper()
	db := openIndex(t, data)
	users := auth.NewStore(db, data, 0)
	ada, err := users.Create("ada", "correct horse battery staple", true)
	if err != nil {
		t.Fatal(err)
	}
	root, err := users.Root(ada)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()

	for _, name := range []string{"notes.txt", "deleted.txt"} {
		if _, err := root.Write(name, strings.NewReader(backupContent), int64(len(backupContent))); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := scan.Scan(db, ada.ID, root, nil); err != nil {
		t.Fatal(err)
	}
	doomed, err := files.Lookup(db, ada.ID, "deleted.txt")
	if err != nil {
		t.Fatal(err)
	}
	if err := files.Trash(db, ada.ID, root, doomed); err != nil {
		t.Fatal(err)
	}
	if _, _, err := share.Create(db, ada.ID, "notes.txt", "", time.Time{}); err != nil {
		t.Fatal(err)
	}

	abandoned := filepath.Join(data, "users", "ada", ".drive", "tmp", "abandoned")
	if err := os.WriteFile(abandoned, []byte("half an upload"), 0o600); err != nil {
		t.Fatal(err)
	}
	// The index is closed before it is copied: this is the "stop the drive
	// first" case, and it keeps the test honest about which state is captured.
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
}

func openIndex(t *testing.T, data string) *index.DB {
	t.Helper()
	if err := os.MkdirAll(data, 0o700); err != nil {
		t.Fatal(err)
	}
	db, movedTo, err := index.OpenOrReset(filepath.Join(data, "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	if movedTo != "" {
		t.Fatalf("the index at %s could not be opened and was moved to %s", data, movedTo)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func runScript(t *testing.T, args ...string) {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "sh", append([]string{"scripts/backup.sh"}, args...)...)
	cmd.Dir = "../.."
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("backup.sh %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

func requireRsync(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("rsync"); err != nil {
		t.Skip("rsync is not installed, and the documented procedure uses it")
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
