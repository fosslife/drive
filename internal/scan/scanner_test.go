package scan

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fosslife/drive/internal/index"
	"github.com/fosslife/drive/internal/storage"
)

// waitFor polls until cond holds, which is how a background scanner is
// observed without sleeping for a guess at how long it takes.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func (f *fixture) scanner(interval time.Duration) *Scanner {
	// The fixture's root is <base>/users/ada, so the data directory is two up.
	return NewScanner(f.db, filepath.Dir(filepath.Dir(f.dir)), interval)
}

// 4.7
func TestScanRunsAtStartupThenOnDemand(t *testing.T) {
	f := setup(t)
	f.put(t, "at-startup.txt", "indexed by the first scan")

	// An interval long enough that only the startup scan and the explicit
	// trigger can fire: a timer must not be what makes this test pass.
	s := f.scanner(time.Hour)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go s.Run(ctx)

	waitFor(t, "the startup scan", func() bool { return s.Status().Scans >= 1 })
	if _, ok := f.lookup(t, "at-startup.txt"); !ok {
		t.Fatal("the startup scan did not index an existing file")
	}

	// A file that lands seconds later is picked up on demand, not on the timer.
	f.put(t, "added-later.txt", "indexed by the triggered scan")
	before := s.Status().Scans
	s.Trigger()
	waitFor(t, "the triggered scan", func() bool { return s.Status().Scans > before })

	if _, ok := f.lookup(t, "added-later.txt"); !ok {
		t.Error("the triggered scan did not pick up a file added after startup")
	}
}

// 4.7: the periodic scan is the safety net for external changes nobody told us
// about, so it must actually fire without a trigger.
func TestScanRunsPeriodically(t *testing.T) {
	f := setup(t)
	s := f.scanner(5 * time.Millisecond)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go s.Run(ctx)

	waitFor(t, "the startup scan", func() bool { return s.Status().Scans >= 1 })
	f.put(t, "dropped-in.txt", "nobody told the server")
	waitFor(t, "a later periodic scan", func() bool {
		_, ok := f.lookup(t, "dropped-in.txt")
		return ok
	})

	cancel()
	waitFor(t, "the scanner to stop", func() bool { return !s.Status().Running })
}

// 4.7: Trigger is called from request handlers and must never block them, even
// with no scanner running and no room in the queue.
func TestTriggerNeverBlocks(t *testing.T) {
	f := setup(t)
	s := f.scanner(time.Hour)
	done := make(chan struct{})
	go func() {
		for range 100 {
			s.Trigger()
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Trigger blocked")
	}
}

// 4.8: a scan of a large root reports how far it has got while it runs, and
// admits the index is incomplete until one has finished. The HTTP side of this
// requirement is TestRequestsAreServedThroughoutARebuild in package server.
func TestScanReportsProgressWhileItRuns(t *testing.T) {
	f := setup(t)
	const files = 4000
	for i := range files {
		f.put(t, fmt.Sprintf("bulk/%02d/file-%04d.txt", i%20, i), strings.Repeat("x", i%64))
	}

	s := f.scanner(time.Hour)
	if !s.Status().Indexing() {
		t.Fatal("a scanner that has never run claims the index is complete")
	}

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go s.Run(ctx)

	// One sample per iteration: reading the status twice would race the scan
	// finishing between the two reads.
	samples, progressed := 0, false
	for {
		st := s.Status()
		if st.Scans > 0 {
			break
		}
		samples++
		if !st.Indexing() {
			t.Fatal("the index was reported complete while the first scan was still running")
		}
		if st.Seen > 0 && st.Seen < files {
			progressed = true
		}
	}
	if samples < 5 {
		t.Fatalf("only %d samples during the rebuild; it finished too fast to prove anything", samples)
	}
	if !progressed {
		t.Error("the scan never reported partial progress, only a start and an end")
	}

	final := s.Status()
	if final.Indexing() {
		t.Error("indexing still reported incomplete after a finished scan")
	}
	if final.Seen != files+21 { // the files, their 20 folders, and bulk itself
		t.Errorf("Seen = %d after the rebuild, want %d", final.Seen, files+21)
	}
	var indexed int
	if err := f.db.QueryRow(`SELECT COUNT(*) FROM files WHERE kind = 'file'`).Scan(&indexed); err != nil {
		t.Fatal(err)
	}
	if indexed != files {
		t.Errorf("%d files indexed, want %d", indexed, files)
	}
}

// 4.4 at the scanner level: one unreadable root is reported and leaves its own
// index entries alone, and does not stop the scanner.
func TestUnreadableRootIsReportedWithoutStoppingTheScanner(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: permission bits do not make a directory unreadable")
	}
	f := setup(t)
	f.put(t, "locked/a.txt", "x")

	s := f.scanner(time.Hour)
	if err := s.ScanAll(t.Context()); err != nil {
		t.Fatalf("first ScanAll: %v", err)
	}

	locked := filepath.Join(f.dir, "locked")
	if err := os.Chmod(locked, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(locked, 0o700) })

	if err := s.ScanAll(t.Context()); err == nil {
		t.Fatal("ScanAll succeeded over an unreadable root, want an error")
	}
	if st := s.Status(); st.Running || st.Error == "" {
		t.Errorf("status after a failed scan = %+v, want stopped with an error", st)
	}
	if e := f.mustLookup(t, "locked/a.txt"); e.state != "present" {
		t.Errorf("locked/a.txt = %+v after the failed scan, want present and untouched", e)
	}
}

// 4.6
func TestDeletedIndexIsRebuiltFromTheFilesystem(t *testing.T) {
	f := setup(t)
	f.put(t, "taxes.pdf", "money")
	f.put(t, "photos/beach.jpg", "not really a jpeg")
	s := f.scanner(time.Hour)
	if err := s.ScanAll(t.Context()); err != nil {
		t.Fatal(err)
	}
	before := f.mustLookup(t, "taxes.pdf")

	dataDir := filepath.Dir(filepath.Dir(f.dir))
	indexPath := filepath.Join(dataDir, "index.db")
	f.db.Close()
	for _, suffix := range []string{"", "-wal", "-shm"} {
		os.Remove(indexPath + suffix)
	}

	// Restart: a fresh index, and an account re-created with the same username
	// lands on the same storage root. That is the whole recovery story.
	db, movedTo, err := index.OpenOrReset(indexPath)
	if err != nil {
		t.Fatalf("reopening a deleted index: %v", err)
	}
	defer db.Close()
	if movedTo != "" {
		t.Errorf("a missing index was moved to %q; there was nothing to move", movedTo)
	}
	res, err := db.Exec(`INSERT INTO users (username, password_hash, storage_root, created_at)
	                     VALUES ('ada', 'x', 'users/ada', 0)`)
	if err != nil {
		t.Fatal(err)
	}
	userID, _ := res.LastInsertId()

	rebuilt := &fixture{db: db, root: f.root, userID: userID, dir: f.dir}
	if err := NewScanner(db, dataDir, time.Hour).ScanAll(t.Context()); err != nil {
		t.Fatalf("rebuilding: %v", err)
	}

	for _, path := range []string{"taxes.pdf", "photos", "photos/beach.jpg"} {
		if e := rebuilt.mustLookup(t, path); e.state != "present" {
			t.Errorf("%s = %+v after the rebuild, want present", path, e)
		}
	}
	if e := rebuilt.mustLookup(t, "taxes.pdf"); e.checksum != before.checksum {
		t.Errorf("checksum after rebuild = %q, want %q", e.checksum, before.checksum)
	}
}

// 4.6: a damaged index must not become an outage. The file is kept, because it
// holds the accounts, which are the one thing a scan cannot reconstruct.
func TestCorruptIndexIsMovedAsideAndRebuilt(t *testing.T) {
	base := t.TempDir()
	indexPath := filepath.Join(base, "index.db")

	rootDir := filepath.Join(base, "users", "ada")
	root, err := storage.Open(rootDir, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if err := os.WriteFile(filepath.Join(rootDir, "taxes.pdf"), []byte("money"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(indexPath, []byte("this is not a database"), 0o600); err != nil {
		t.Fatal(err)
	}

	db, movedTo, err := index.OpenOrReset(indexPath)
	if err != nil {
		t.Fatalf("OpenOrReset over a corrupt index: %v", err)
	}
	defer db.Close()
	if movedTo == "" {
		t.Fatal("the corrupt index was not moved aside")
	}
	if kept, err := os.ReadFile(movedTo); err != nil || string(kept) != "this is not a database" {
		t.Errorf("the corrupt index was not kept intact at %s: %q %v", movedTo, kept, err)
	}

	res, err := db.Exec(`INSERT INTO users (username, password_hash, storage_root, created_at)
	                     VALUES ('ada', 'x', 'users/ada', 0)`)
	if err != nil {
		t.Fatalf("the replacement index is not usable: %v", err)
	}
	userID, _ := res.LastInsertId()
	if err := NewScanner(db, base, time.Hour).ScanAll(t.Context()); err != nil {
		t.Fatalf("rebuilding after a reset: %v", err)
	}

	f := &fixture{db: db, root: root, userID: userID, dir: rootDir}
	if e := f.mustLookup(t, "taxes.pdf"); e.state != "present" {
		t.Errorf("taxes.pdf = %+v after the reset, want present", e)
	}
	if content, err := os.ReadFile(filepath.Join(rootDir, "taxes.pdf")); err != nil || string(content) != "money" {
		t.Errorf("a user file was touched by the index reset: %q %v", content, err)
	}
}

// 4.6: an index from a newer binary is ahead of us, not damaged. Resetting it
// would destroy accounts and shares that the newer binary can still read.
func TestFutureSchemaIsRefusedNotReset(t *testing.T) {
	base := t.TempDir()
	indexPath := filepath.Join(base, "index.db")
	db, err := index.Open(indexPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(fmt.Sprintf("PRAGMA user_version = %d", index.Version()+5)); err != nil {
		t.Fatal(err)
	}
	db.Close()

	reopened, movedTo, err := index.OpenOrReset(indexPath)
	if err == nil {
		reopened.Close()
		t.Fatal("OpenOrReset accepted an index from a newer binary")
	}
	if movedTo != "" {
		t.Errorf("a future-schema index was moved to %q; it must be left alone", movedTo)
	}
	entries, err := os.ReadDir(base)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), "corrupt") {
			t.Errorf("a future-schema index produced %s", e.Name())
		}
	}
}
