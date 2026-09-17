package storage

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const noReserve = 0

func testRoot(t *testing.T) *Root {
	t.Helper()
	r, err := Open(filepath.Join(t.TempDir(), "root"), noReserve)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { r.Close() })
	return r
}

func write(t *testing.T, r *Root, rel, content string) Info {
	t.Helper()
	info, err := r.Write(rel, strings.NewReader(content), int64(len(content)))
	if err != nil {
		t.Fatalf("Write %s: %v", rel, err)
	}
	return info
}

func sum(content string) string {
	h := sha256.Sum256([]byte(content))
	return hex.EncodeToString(h[:])
}

// 3.1
func TestPathsOutsideTheRootAreRejected(t *testing.T) {
	r := testRoot(t)

	// The secret lives beside the root, which is where a traversal would land.
	outside := filepath.Join(filepath.Dir(r.Dir()), "secret.txt")
	if err := os.WriteFile(outside, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name string
		path string
	}{
		{"relative segment", "../secret.txt"},
		{"relative segment mid-path", "docs/../../secret.txt"},
		{"bare parent", ".."},
		{"absolute path", "/etc/passwd"},
		{"absolute inside root", "/docs/a.txt"},
		{"empty path", ""},
		{"trailing slash", "docs/"},
		{"empty element", "docs//a.txt"},
		{"current directory element", "docs/./a.txt"},
		{"NUL byte", "docs/a.txt\x00.png"},
		{"reserved internal directory", ".drive/tmp/a.txt"},
		{"reserved internal directory itself", ".drive"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := r.Write(tc.path, strings.NewReader("x"), 1); !errors.Is(err, ErrInvalidPath) {
				t.Errorf("Write(%q) = %v, want ErrInvalidPath", tc.path, err)
			}
			if _, err := r.Checksum(tc.path); !errors.Is(err, ErrInvalidPath) {
				t.Errorf("Checksum(%q) = %v, want ErrInvalidPath", tc.path, err)
			}
		})
	}

	if content, _ := os.ReadFile(outside); string(content) != "secret" {
		t.Errorf("file outside the root was modified: %q", content)
	}
}

// 3.1: percent-encoded separators are traversals once decoded, which is the
// form this package receives from net/http.
func TestEncodedSeparatorsAreRejectedOnceDecoded(t *testing.T) {
	r := testRoot(t)
	for _, encoded := range []string{"%2e%2e%2fsecret.txt", "docs%2f..%2f..%2fsecret.txt"} {
		decoded, err := url.PathUnescape(encoded)
		if err != nil {
			t.Fatalf("decoding %q: %v", encoded, err)
		}
		if _, err := r.Write(decoded, strings.NewReader("x"), 1); !errors.Is(err, ErrInvalidPath) {
			t.Errorf("Write(%q decoded from %q) = %v, want ErrInvalidPath", decoded, encoded, err)
		}
	}
}

// 3.1
func TestSymlinkEscapingTheRootIsNotFollowed(t *testing.T) {
	r := testRoot(t)
	outside := filepath.Join(filepath.Dir(r.Dir()), "secret.txt")
	if err := os.WriteFile(outside, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(r.Dir(), "escape.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/", filepath.Join(r.Dir(), "rootlink")); err != nil {
		t.Fatal(err)
	}

	if _, err := r.Checksum("escape.txt"); err == nil {
		t.Error("Checksum followed a symlink out of the root")
	}
	if _, err := r.Checksum("rootlink/etc/passwd"); err == nil {
		t.Error("Checksum followed an absolute symlink")
	}
	// Writing to the link's path replaces the link with a regular file inside
	// the root. A rename never follows the destination, which is what keeps the
	// target outside the root safe.
	if _, err := r.Write("escape.txt", strings.NewReader("overwritten"), 11); err != nil {
		t.Fatalf("Write over a symlink: %v", err)
	}
	st, err := os.Lstat(filepath.Join(r.Dir(), "escape.txt"))
	if err != nil || st.Mode()&os.ModeSymlink != 0 {
		t.Errorf("escape.txt is %v (err %v), want a regular file", st.Mode(), err)
	}
	if content, _ := os.ReadFile(outside); string(content) != "secret" {
		t.Errorf("file outside the root was modified through a symlink: %q", content)
	}
}

// 3.2
func TestInternalLayoutCreatedOnFirstUse(t *testing.T) {
	r := testRoot(t)
	for _, sub := range []string{".drive", ".drive/tmp", ".drive/trash", ".drive/thumbs"} {
		st, err := os.Stat(filepath.Join(r.Dir(), sub))
		if err != nil {
			t.Errorf("%s missing: %v", sub, err)
			continue
		}
		if !st.IsDir() {
			t.Errorf("%s is not a directory", sub)
		}
	}
}

// gatedReader delivers its content in two halves, pausing in between so a test
// can observe the destination while the write is in flight.
type gatedReader struct {
	halves  [2]string
	started chan struct{}
	release chan struct{}
	stage   int
}

func (g *gatedReader) Read(p []byte) (int, error) {
	switch g.stage {
	case 0:
		g.stage++
		close(g.started)
		return copy(p, g.halves[0]), nil
	case 1:
		<-g.release
		g.stage++
		return copy(p, g.halves[1]), nil
	default:
		return 0, io.EOF
	}
}

// 3.3
func TestDestinationDoesNotExistUntilRename(t *testing.T) {
	r := testRoot(t)
	src := &gatedReader{halves: [2]string{"first half ", "second half"}, started: make(chan struct{}), release: make(chan struct{})}

	done := make(chan error, 1)
	go func() {
		_, err := r.Write("report.txt", src, -1)
		done <- err
	}()

	<-src.started
	if _, err := os.Stat(filepath.Join(r.Dir(), "report.txt")); !os.IsNotExist(err) {
		t.Errorf("destination exists mid-write: %v", err)
	}
	close(src.release)
	if err := <-done; err != nil {
		t.Fatalf("Write: %v", err)
	}

	content, err := os.ReadFile(filepath.Join(r.Dir(), "report.txt"))
	if err != nil || string(content) != "first half second half" {
		t.Errorf("after write: content %q, err %v", content, err)
	}
	assertTempEmpty(t, r)
}

// 3.3: a write that fails partway publishes nothing and leaves no temp data.
func TestFailedWriteLeavesNothingBehind(t *testing.T) {
	r := testRoot(t)
	write(t, r, "report.txt", "original")

	failing := io.MultiReader(strings.NewReader("partial"), errReader{})
	if _, err := r.Write("report.txt", failing, -1); err == nil {
		t.Fatal("Write succeeded on a failing source, want an error")
	}

	content, _ := os.ReadFile(filepath.Join(r.Dir(), "report.txt"))
	if string(content) != "original" {
		t.Errorf("existing file was modified: %q", content)
	}
	assertTempEmpty(t, r)
}

type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errors.New("connection reset") }

// 3.4
func TestChecksumMatchesIndependentComputation(t *testing.T) {
	r := testRoot(t)
	const content = "the quick brown fox"

	info := write(t, r, "fox.txt", content)
	if info.Checksum != sum(content) {
		t.Errorf("Write checksum = %s, want %s", info.Checksum, sum(content))
	}
	if info.Size != int64(len(content)) {
		t.Errorf("Write size = %d, want %d", info.Size, len(content))
	}

	// A file that arrived outside the application is checksummed on first index.
	external := "copied in over ssh"
	if err := os.WriteFile(filepath.Join(r.Dir(), "external.txt"), []byte(external), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := r.Checksum("external.txt")
	if err != nil {
		t.Fatalf("Checksum: %v", err)
	}
	if got.Checksum != sum(external) {
		t.Errorf("Checksum = %s, want %s", got.Checksum, sum(external))
	}
}

// 3.5
func TestVerifyReportsCorruptionAndChangesNothing(t *testing.T) {
	r := testRoot(t)
	good := write(t, r, "good.txt", "intact")
	bad := write(t, r, "bad.txt", "original content")

	// Corrupt one file's bytes behind the application's back.
	corrupt := filepath.Join(r.Dir(), "bad.txt")
	if err := os.WriteFile(corrupt, []byte("corrupted bytes"), 0o600); err != nil {
		t.Fatal(err)
	}

	mismatches := r.Verify(map[string]string{
		"good.txt":    good.Checksum,
		"bad.txt":     bad.Checksum,
		"missing.txt": "deadbeef",
	})

	reported := map[string]Mismatch{}
	for _, m := range mismatches {
		reported[m.Path] = m
	}
	if _, ok := reported["good.txt"]; ok {
		t.Error("intact file reported as a mismatch")
	}
	if m, ok := reported["bad.txt"]; !ok {
		t.Error("corrupted file not reported")
	} else if m.Got != sum("corrupted bytes") || m.Want != bad.Checksum {
		t.Errorf("mismatch = %+v, want the recorded and actual checksums", m)
	}
	if m, ok := reported["missing.txt"]; !ok || m.Err == nil {
		t.Errorf("unreadable file reported as %+v, want an error", m)
	}

	// Nothing is deleted, quarantined, or repaired.
	if content, err := os.ReadFile(corrupt); err != nil || string(content) != "corrupted bytes" {
		t.Errorf("corrupt file was touched: content %q, err %v", content, err)
	}
	if content, err := os.ReadFile(filepath.Join(r.Dir(), "good.txt")); err != nil || string(content) != "intact" {
		t.Errorf("intact file was touched: content %q, err %v", content, err)
	}
}

// 3.6
func TestWritesAreRefusedBelowTheReserve(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "root")
	// A reserve larger than the volume stands in for a constrained volume: the
	// guard compares free space against the reserve either way.
	full, err := Open(dir, 1<<62)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer full.Close()

	// Seed a file while the guard is off, then reopen with the huge reserve.
	seed, err := Open(dir, noReserve)
	if err != nil {
		t.Fatal(err)
	}
	existing := write(t, seed, "existing.txt", "keep me")
	seed.Close()

	_, err = full.Write("new.txt", strings.NewReader("more data"), 9)
	if !errors.Is(err, ErrNoSpace) {
		t.Fatalf("Write = %v, want ErrNoSpace", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "new.txt")); !os.IsNotExist(err) {
		t.Errorf("refused write left a file at the destination: %v", err)
	}
	assertTempEmpty(t, full)

	// Reads keep working so space can be reclaimed.
	got, err := full.Checksum("existing.txt")
	if err != nil {
		t.Fatalf("Checksum below the reserve: %v", err)
	}
	if got.Checksum != existing.Checksum {
		t.Errorf("Checksum = %s, want %s", got.Checksum, existing.Checksum)
	}
}

func assertTempEmpty(t *testing.T, r *Root) {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(r.Dir(), Internal, "tmp"))
	if err != nil {
		t.Fatalf("reading temp directory: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("%d temp files left behind, want none", len(entries))
	}
}

// 4.1: Walk is what the reconciler sees. It must not offer it .drive, and it
// must not offer it a symlink, which has no bytes of its own and could point
// anywhere.
func TestWalkSkipsInternalAndNonRegularEntries(t *testing.T) {
	r := testRoot(t)
	write(t, r, "notes.txt", "hello")
	// Write does not create parent directories; folder creation lands in 7.4.
	if err := os.Mkdir(filepath.Join(r.Dir(), "docs"), 0o700); err != nil {
		t.Fatal(err)
	}
	write(t, r, "docs/taxes.pdf", "money")

	outside := filepath.Join(filepath.Dir(r.Dir()), "secret.txt")
	if err := os.WriteFile(outside, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(r.Dir(), "escape.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("notes.txt", filepath.Join(r.Dir(), "inside-link.txt")); err != nil {
		t.Fatal(err)
	}

	seen := map[string]int64{}
	if err := r.Walk(func(e Entry) error {
		if e.IsDir {
			seen[e.Path] = -1
			return nil
		}
		seen[e.Path] = e.Size
		return nil
	}); err != nil {
		t.Fatalf("Walk: %v", err)
	}

	want := map[string]int64{"notes.txt": 5, "docs": -1, "docs/taxes.pdf": 5}
	if len(seen) != len(want) {
		t.Fatalf("Walk saw %v, want %v", seen, want)
	}
	for path, size := range want {
		if seen[path] != size {
			t.Errorf("Walk saw %s as %d, want %d", path, seen[path], size)
		}
	}
}

// 4.4: a directory that cannot be read is an error, never an empty result.
func TestWalkFailsOnAnUnreadableDirectory(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: permission bits do not make a directory unreadable")
	}
	r := testRoot(t)
	locked := filepath.Join(r.Dir(), "locked")
	if err := os.Mkdir(locked, 0o700); err != nil {
		t.Fatal(err)
	}
	write(t, r, "locked/a.txt", "x")
	if err := os.Chmod(locked, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(locked, 0o700) })

	err := r.Walk(func(Entry) error { return nil })
	if err == nil {
		t.Fatal("Walk succeeded over an unreadable directory, want an error")
	}
	if !strings.Contains(err.Error(), "locked") {
		t.Errorf("error %q does not name the unreadable directory", err)
	}
}
