package scan

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fosslife/drive/internal/index"
	"github.com/fosslife/drive/internal/storage"
)

// fixture is one user with an open storage root and an index that knows about
// them. Every test starts here because a scan is meaningless without both.
type fixture struct {
	db     *index.DB
	root   *storage.Root
	userID int64
	dir    string
}

func setup(t *testing.T) *fixture {
	t.Helper()
	base := t.TempDir()

	db, err := index.Open(filepath.Join(base, "index.db"))
	if err != nil {
		t.Fatalf("opening index: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	res, err := db.Exec(`INSERT INTO users (username, password_hash, storage_root, created_at)
	                     VALUES ('ada', 'x', 'users/ada', 0)`)
	if err != nil {
		t.Fatalf("creating user: %v", err)
	}
	userID, _ := res.LastInsertId()

	dir := filepath.Join(base, "users", "ada")
	root, err := storage.Open(dir, 0)
	if err != nil {
		t.Fatalf("opening storage root: %v", err)
	}
	t.Cleanup(func() { root.Close() })

	return &fixture{db: db, root: root, userID: userID, dir: dir}
}

func (f *fixture) scan(t *testing.T) Result {
	t.Helper()
	res, err := Scan(f.db, f.userID, f.root, nil)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	return res
}

// put writes a file directly to disk, the way an external tool would.
func (f *fixture) put(t *testing.T, rel, content string) {
	t.Helper()
	full := filepath.Join(f.dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// entry is what the index says about one path.
type entry struct {
	id       int64
	kind     string
	size     int64
	checksum string
	state    string
}

func (f *fixture) lookup(t *testing.T, path string) (entry, bool) {
	t.Helper()
	dir, name := split(path)
	var e entry
	err := f.db.QueryRow(`SELECT id, kind, size, IFNULL(checksum, ''), state FROM files
	                      WHERE user_id = ? AND dir = ? AND name = ? AND state <> 'trashed'`,
		f.userID, dir, name).Scan(&e.id, &e.kind, &e.size, &e.checksum, &e.state)
	if err != nil {
		return entry{}, false
	}
	return e, true
}

func (f *fixture) mustLookup(t *testing.T, path string) entry {
	t.Helper()
	e, ok := f.lookup(t, path)
	if !ok {
		t.Fatalf("%s is not in the index", path)
	}
	return e
}

func (f *fixture) countRows(t *testing.T) int {
	t.Helper()
	var n int
	if err := f.db.QueryRow(`SELECT COUNT(*) FROM files`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// tree is every path under the storage root with its contents, for asserting
// that a scan changed nothing on disk.
func tree(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		if d.IsDir() {
			out[rel] = "<dir>"
			return nil
		}
		content, err := os.ReadFile(p)
		out[rel] = string(content)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// 4.1
func TestUnchangedFilesAreNotRehashed(t *testing.T) {
	f := setup(t)
	f.put(t, "notes.txt", "hello")
	f.put(t, "docs/taxes.pdf", "money")

	first := f.scan(t)
	if first.Hashed != 2 || first.Added != 3 { // two files plus the docs folder
		t.Fatalf("first scan = %+v, want 2 hashed and 3 added", first)
	}

	second := f.scan(t)
	if second.Hashed != 0 {
		t.Errorf("second scan rehashed %d unchanged files, want 0", second.Hashed)
	}
	if second.Added+second.Updated+second.Moved+second.Missing != 0 {
		t.Errorf("second scan changed the index: %+v", second)
	}
	if second.Files != 2 || second.Dirs != 1 {
		t.Errorf("second scan saw %d files and %d dirs, want 2 and 1", second.Files, second.Dirs)
	}

	// Same size, different bytes, newer mtime: the fast path must not swallow
	// this, or corruption and edits alike go unnoticed.
	f.put(t, "notes.txt", "world")
	later := time.Now().Add(time.Hour)
	if err := os.Chtimes(filepath.Join(f.dir, "notes.txt"), later, later); err != nil {
		t.Fatal(err)
	}
	third := f.scan(t)
	if third.Hashed != 1 || third.Updated != 1 {
		t.Errorf("third scan = %+v, want 1 hashed and 1 updated", third)
	}
}

// 4.1: .drive holds in-flight uploads, trash, and thumbnails. None of it is
// user-visible, so none of it may be indexed.
func TestInternalDirectoryIsSkipped(t *testing.T) {
	f := setup(t)
	f.put(t, ".drive/tmp/half-an-upload", "partial")
	f.put(t, ".drive/trash/deleted.txt", "gone")
	f.put(t, "visible.txt", "here")

	res := f.scan(t)
	if res.Files != 1 || res.Added != 1 {
		t.Errorf("scan = %+v, want exactly the one visible file", res)
	}
	if f.countRows(t) != 1 {
		t.Errorf("%d rows indexed, want 1", f.countRows(t))
	}
}

// 4.2
func TestExternallyAddedFilesAreAdopted(t *testing.T) {
	f := setup(t)
	const n = 500
	for i := range n {
		f.put(t, fmt.Sprintf("bulk/file-%03d.txt", i), strings.Repeat("x", i))
	}

	res := f.scan(t)
	if res.Files != n {
		t.Fatalf("scan saw %d files, want %d", res.Files, n)
	}

	for i := range n {
		path := fmt.Sprintf("bulk/file-%03d.txt", i)
		e := f.mustLookup(t, path)
		if e.kind != "file" || e.state != "present" {
			t.Fatalf("%s indexed as %+v", path, e)
		}
		if e.size != int64(i) {
			t.Fatalf("%s size = %d, want %d", path, e.size, i)
		}
		want, err := f.root.Checksum(path)
		if err != nil {
			t.Fatal(err)
		}
		if e.checksum != want.Checksum {
			t.Fatalf("%s checksum = %q, want %q", path, e.checksum, want.Checksum)
		}
	}

	// mtime is recorded from the file, not from the clock at scan time.
	var mtime int64
	if err := f.db.QueryRow(`SELECT mtime FROM files WHERE user_id = ? AND name = 'file-001.txt'`,
		f.userID).Scan(&mtime); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(filepath.Join(f.dir, "bulk", "file-001.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if mtime != st.ModTime().Unix() {
		t.Errorf("mtime = %d, want %d", mtime, st.ModTime().Unix())
	}
}

// 4.3
func TestVanishedFileIsMarkedMissingAndNothingIsDeleted(t *testing.T) {
	f := setup(t)
	f.put(t, "gone.txt", "bytes")
	f.put(t, "stays.txt", "other bytes")
	f.scan(t)

	before := f.mustLookup(t, "gone.txt")
	if err := os.Remove(filepath.Join(f.dir, "gone.txt")); err != nil {
		t.Fatal(err)
	}
	onDisk := tree(t, f.dir)

	res := f.scan(t)
	if res.Missing != 1 {
		t.Errorf("scan = %+v, want 1 missing", res)
	}

	after := f.mustLookup(t, "gone.txt")
	if after.state != "missing" {
		t.Errorf("state = %q, want missing", after.state)
	}
	if after.id != before.id {
		t.Errorf("identity changed from %d to %d; a missing file keeps its row", before.id, after.id)
	}
	if e := f.mustLookup(t, "stays.txt"); e.state != "present" {
		t.Errorf("stays.txt = %+v, want present", e)
	}
	if got := tree(t, f.dir); !equal(got, onDisk) {
		t.Errorf("the scan changed the filesystem:\n got %v\nwant %v", got, onDisk)
	}

	// Rescanning does not rewrite a row that is already marked missing.
	if again := f.scan(t); again.Missing != 0 {
		t.Errorf("second scan re-marked %d already-missing rows", again.Missing)
	}

	// And the file coming back is adopted again under the same identity.
	f.put(t, "gone.txt", "bytes")
	f.scan(t)
	back := f.mustLookup(t, "gone.txt")
	if back.state != "present" || back.id != before.id {
		t.Errorf("restored file = %+v, want present under id %d", back, before.id)
	}
}

// 4.4
func TestUnreadableRootAbortsWithZeroIndexWrites(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: permission bits do not make a directory unreadable")
	}
	f := setup(t)
	f.put(t, "readable.txt", "fine")
	f.put(t, "locked/secret.txt", "also fine")
	f.scan(t)
	rowsBefore := f.countRows(t)

	// New files that a successful scan would have adopted, so "zero writes"
	// means the abort, not an empty scan.
	f.put(t, "new.txt", "adopt me")
	locked := filepath.Join(f.dir, "locked")
	if err := os.Chmod(locked, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(locked, 0o700) })

	if _, err := Scan(f.db, f.userID, f.root, nil); err == nil {
		t.Fatal("Scan succeeded over an unreadable directory, want an error")
	} else if !strings.Contains(err.Error(), "locked") {
		t.Errorf("error %q does not name the unreadable directory", err)
	}

	if got := f.countRows(t); got != rowsBefore {
		t.Errorf("%d rows after the aborted scan, want %d unchanged", got, rowsBefore)
	}
	if _, indexed := f.lookup(t, "new.txt"); indexed {
		t.Error("the aborted scan still wrote new.txt to the index")
	}
	for _, path := range []string{"readable.txt", "locked/secret.txt"} {
		if e := f.mustLookup(t, path); e.state != "present" {
			t.Errorf("%s = %+v after the aborted scan, want present", path, e)
		}
	}
}

// 4.5
func TestExternalMovePreservesIdentity(t *testing.T) {
	f := setup(t)
	f.put(t, "taxes.pdf", "one of a kind")
	f.scan(t)
	before := f.mustLookup(t, "taxes.pdf")

	if err := os.MkdirAll(filepath.Join(f.dir, "archive"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(f.dir, "taxes.pdf"), filepath.Join(f.dir, "archive", "taxes-2024.pdf")); err != nil {
		t.Fatal(err)
	}

	res := f.scan(t)
	if res.Moved != 1 || res.Missing != 0 {
		t.Errorf("scan = %+v, want 1 moved and 0 missing", res)
	}
	after := f.mustLookup(t, "archive/taxes-2024.pdf")
	if after.id != before.id {
		t.Errorf("identity %d became %d across an external move", before.id, after.id)
	}
	if after.state != "present" {
		t.Errorf("state = %q, want present", after.state)
	}
	if _, stillThere := f.lookup(t, "taxes.pdf"); stillThere {
		t.Error("the old path is still indexed after the move")
	}
}

// 4.5: two byte-identical files make the match ambiguous. A wrong guess would
// silently reassign identity, so a new identity is the correct answer.
func TestAmbiguousMoveGetsANewIdentity(t *testing.T) {
	f := setup(t)
	f.put(t, "a.txt", "identical")
	f.put(t, "b.txt", "identical")
	f.scan(t)
	a := f.mustLookup(t, "a.txt")
	b := f.mustLookup(t, "b.txt")

	for _, name := range []string{"a.txt", "b.txt"} {
		if err := os.Rename(filepath.Join(f.dir, name), filepath.Join(f.dir, "moved-"+name)); err != nil {
			t.Fatal(err)
		}
	}

	res := f.scan(t)
	if res.Moved != 0 {
		t.Errorf("scan = %+v, want 0 moved: the match is ambiguous", res)
	}
	if res.Added != 2 || res.Missing != 2 {
		t.Errorf("scan = %+v, want 2 added and 2 missing", res)
	}
	for _, name := range []string{"moved-a.txt", "moved-b.txt"} {
		e := f.mustLookup(t, name)
		if e.id == a.id || e.id == b.id {
			t.Errorf("%s reused identity %d from a file it may not be", name, e.id)
		}
	}
}

func equal(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}
