package server

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/fosslife/drive/internal/auth"
	"github.com/fosslife/drive/internal/files"
)

// uploader drives the tus endpoints the way a client would.
type uploader struct {
	h      *harness
	secret string
	t      *testing.T
}

func newUploader(t *testing.T, h *harness, u *auth.User) *uploader {
	return &uploader{h: h, secret: h.token(u), t: t}
}

func (c *uploader) req(method, path string, body io.Reader) *http.Request {
	req := httptest.NewRequest(method, path, body)
	req.Header.Set("Tus-Resumable", "1.0.0")
	req.Header.Set("Authorization", "Bearer "+c.secret)
	return req
}

// create reserves an upload and returns its id.
func (c *uploader) create(dir, name string, size int, replace bool) string {
	c.t.Helper()
	meta := []string{
		"filename " + base64.StdEncoding.EncodeToString([]byte(name)),
		"dir " + base64.StdEncoding.EncodeToString([]byte(dir)),
	}
	if replace {
		meta = append(meta, "replace")
	}
	req := c.req("POST", "/api/uploads", nil)
	req.Header.Set("Upload-Length", strconv.Itoa(size))
	req.Header.Set("Upload-Metadata", strings.Join(meta, ","))

	w := c.h.do(req)
	if w.Code != http.StatusCreated {
		c.t.Fatalf("creating an upload: %d %s", w.Code, w.Body.String())
	}
	location := w.Header().Get("Location")
	if !strings.HasPrefix(location, "/api/uploads/") {
		c.t.Fatalf("Location = %q, want an upload URL", location)
	}
	return strings.TrimPrefix(location, "/api/uploads/")
}

// offset asks the server where it got to.
func (c *uploader) offset(id string) int64 {
	c.t.Helper()
	w := c.h.do(c.req("HEAD", "/api/uploads/"+id, nil))
	if w.Code != http.StatusOK {
		c.t.Fatalf("HEAD upload: %d %s", w.Code, w.Body.String())
	}
	if w.Header().Get("Cache-Control") != "no-store" {
		c.t.Errorf("the offset is cacheable: Cache-Control = %q", w.Header().Get("Cache-Control"))
	}
	n, err := strconv.ParseInt(w.Header().Get("Upload-Offset"), 10, 64)
	if err != nil {
		c.t.Fatalf("Upload-Offset = %q", w.Header().Get("Upload-Offset"))
	}
	return n
}

// patch sends one chunk at the given offset.
func (c *uploader) patch(id string, at int64, chunk io.Reader) *httptest.ResponseRecorder {
	c.t.Helper()
	req := c.req("PATCH", "/api/uploads/"+id, chunk)
	req.Header.Set("Content-Type", "application/offset+octet-stream")
	req.Header.Set("Upload-Offset", strconv.FormatInt(at, 10))
	return c.h.do(req)
}

// 8.1: the three tus verbs, end to end, with the file landing where it was
// addressed and with the right contents.
func TestUploadCreateOffsetAppend(t *testing.T) {
	h := newHarness(t)
	ada := h.account("ada", false)
	c := newUploader(t, h, ada)

	if w := h.do(c.req("OPTIONS", "/api/uploads", nil)); w.Code != http.StatusNoContent ||
		w.Header().Get("Tus-Version") != "1.0.0" {
		t.Errorf("OPTIONS: %d, Tus-Version %q", w.Code, w.Header().Get("Tus-Version"))
	}

	content := strings.Repeat("payload-", 1000) // 8,000 bytes
	id := c.create("docs", "report.txt", len(content), false)

	if got := c.offset(id); got != 0 {
		t.Fatalf("a fresh upload is at %d, want 0", got)
	}
	// Nothing at the destination until the last byte arrives.
	if _, err := os.Stat(filepath.Join(h.dataDir, "users", "ada", "docs", "report.txt")); err == nil {
		t.Fatal("the destination exists before the upload finished")
	}

	half := len(content) / 2
	if w := c.patch(id, 0, strings.NewReader(content[:half])); w.Code != http.StatusNoContent {
		t.Fatalf("first chunk: %d %s", w.Code, w.Body.String())
	}
	if got := c.offset(id); got != int64(half) {
		t.Fatalf("after the first chunk the offset is %d, want %d", got, half)
	}

	w := c.patch(id, int64(half), strings.NewReader(content[half:]))
	if w.Code != http.StatusCreated {
		t.Fatalf("last chunk: %d %s", w.Code, w.Body.String())
	}
	stored := decodeBody[files.Entry](t, w)
	if stored.Path != "docs/report.txt" || stored.Size != int64(len(content)) {
		t.Errorf("stored %+v, want docs/report.txt of %d bytes", stored, len(content))
	}

	secret := c.secret
	if got := h.as(secret, "GET", "/api/download/docs/report.txt", nil).Body.String(); got != content {
		t.Errorf("downloaded %d bytes, want the %d uploaded", len(got), len(content))
	}
	if !slicesContains(h.list(t, secret, "docs").Entries, "report.txt") {
		t.Error("the uploaded file is not in its folder's listing")
	}
	// The upload is closed out, not left as debris.
	var open int
	h.db.QueryRow(`SELECT count(*) FROM uploads`).Scan(&open)
	if open != 0 {
		t.Errorf("%d upload rows remain after finishing", open)
	}
}

// 8.1: memory does not scale with the file. A 64 MiB upload in 8 MiB chunks
// must cost what a small one does, because the body goes straight to disk.
func TestUploadDoesNotScaleWithFileSize(t *testing.T) {
	h := newHarness(t)
	ada := h.account("ada", false)
	c := newUploader(t, h, ada)

	const (
		chunkSize = 8 << 20
		chunks    = 8
		total     = chunkSize * chunks
	)
	id := c.create("", "big.bin", total, false)

	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)

	sum := sha256.New()
	var at int64
	for i := range chunks {
		// A reader rather than a buffer: materialising 8 MiB here would
		// measure the test, not the server.
		body := io.TeeReader(io.LimitReader(repeating('a'+byte(i)), chunkSize), sum)
		w := c.patch(id, at, body)
		wantCode := http.StatusNoContent
		if i == chunks-1 {
			wantCode = http.StatusCreated
		}
		if w.Code != wantCode {
			t.Fatalf("chunk %d: %d %s", i, w.Code, w.Body.String())
		}
		at += chunkSize
	}
	runtime.ReadMemStats(&after)

	// Total allocation rather than live heap: it is monotonic, so it does not
	// depend on when the collector happened to run. Buffering even one chunk
	// would add 8 MiB here, and buffering the file would add 64.
	const ceiling = 4 << 20
	if allocated := after.TotalAlloc - before.TotalAlloc; allocated > ceiling {
		t.Errorf("uploading %d bytes allocated %d, want under %d: the body is being buffered", total, allocated, ceiling)
	}

	st, err := os.Stat(filepath.Join(h.dataDir, "users", "ada", "big.bin"))
	if err != nil || st.Size() != total {
		t.Fatalf("stored file: %v %v, want %d bytes", st, err, total)
	}
	var stored string
	if err := h.db.QueryRow(`SELECT checksum FROM files WHERE user_id = ? AND name = 'big.bin'`, ada.ID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored != hex.EncodeToString(sum.Sum(nil)) {
		t.Errorf("recorded checksum %s does not match what was sent", stored)
	}
}

// 8.2: an interrupted upload resumes from the offset the server reports, and
// the finished file matches the source byte for byte.
func TestInterruptedUploadResumesToTheRightChecksum(t *testing.T) {
	h := newHarness(t)
	ada := h.account("ada", false)
	c := newUploader(t, h, ada)

	source := make([]byte, 300_000)
	for i := range source {
		source[i] = byte(i * 7 % 251)
	}
	want := sha256.Sum256(source)
	id := c.create("", "resumed.bin", len(source), false)

	// The connection dies partway through a chunk: the reader stops early.
	const delivered = 120_000
	w := c.patch(id, 0, &brokenReader{data: source, fail: delivered})
	if w.Code == http.StatusCreated {
		t.Fatal("the interrupted chunk finished the upload")
	}
	// Whatever arrived is on disk and the reported offset says so.
	at := c.offset(id)
	if at != delivered {
		t.Fatalf("after the interruption the offset is %d, want the %d that arrived", at, delivered)
	}
	if _, err := os.Stat(filepath.Join(h.dataDir, "users", "ada", "resumed.bin")); err == nil {
		t.Fatal("the destination exists while the upload is incomplete")
	}

	// Resuming from a stale offset is refused rather than silently leaving a
	// hole or duplicating bytes.
	if got := c.patch(id, 0, strings.NewReader("x")); got.Code != http.StatusConflict {
		t.Errorf("resuming from the wrong offset: %d %s, want 409", got.Code, got.Body.String())
	}

	final := c.patch(id, at, strings.NewReader(string(source[at:])))
	if final.Code != http.StatusCreated {
		t.Fatalf("resumed chunk: %d %s", final.Code, final.Body.String())
	}

	stored, err := os.ReadFile(filepath.Join(h.dataDir, "users", "ada", "resumed.bin"))
	if err != nil {
		t.Fatal(err)
	}
	if got := sha256.Sum256(stored); got != want {
		t.Errorf("the resumed file hashes to %x, want %x", got, want)
	}
	var recorded string
	h.db.QueryRow(`SELECT checksum FROM files WHERE user_id = ? AND name = 'resumed.bin'`, ada.ID).Scan(&recorded)
	if recorded != hex.EncodeToString(want[:]) {
		t.Errorf("recorded checksum %s, want %x", recorded, want)
	}
}

// 8.3: an abandoned upload leaves nothing at the destination and does not touch
// a file already there.
func TestAbortedUploadLeavesTheDestinationUntouched(t *testing.T) {
	h := newHarness(t)
	ada := h.account("ada", false)
	c := newUploader(t, h, ada)
	h.put(ada, "notes.txt", "the original contents")
	h.scan(ada)
	original := entryNamed(t, h.list(t, c.secret, "").Entries, "notes.txt")

	// One upload aimed at a name nothing occupies, one aimed at an existing file.
	fresh := c.create("", "new.txt", 100, false)
	onto := c.create("", "notes.txt", 100, true)
	c.patch(fresh, 0, strings.NewReader(strings.Repeat("x", 40)))
	c.patch(onto, 0, strings.NewReader(strings.Repeat("y", 40)))

	for _, id := range []string{fresh, onto} {
		if w := h.do(c.req("DELETE", "/api/uploads/"+id, nil)); w.Code != http.StatusNoContent {
			t.Fatalf("aborting %s: %d %s", id, w.Code, w.Body.String())
		}
	}

	if _, err := os.Stat(filepath.Join(h.dataDir, "users", "ada", "new.txt")); err == nil {
		t.Error("the aborted upload left a file at its destination")
	}
	got, err := os.ReadFile(filepath.Join(h.dataDir, "users", "ada", "notes.txt"))
	if err != nil || string(got) != "the original contents" {
		t.Errorf("the existing file after an aborted replace: %q %v", got, err)
	}
	now := entryNamed(t, h.list(t, c.secret, "").Entries, "notes.txt")
	if now.ID != original.ID || now.ETag != original.ETag {
		t.Errorf("the existing file changed: %+v, was %+v", now, original)
	}

	// And the temp data went with the abort.
	tmp, _ := os.ReadDir(filepath.Join(h.dataDir, "users", "ada", ".drive", "tmp"))
	if len(tmp) != 0 {
		t.Errorf("%d temp files remain after aborting", len(tmp))
	}
	var open int
	h.db.QueryRow(`SELECT count(*) FROM uploads`).Scan(&open)
	if open != 0 {
		t.Errorf("%d upload rows remain after aborting", open)
	}
}

// 8.4: a collision is sidestepped by default and replaces only when asked.
func TestUploadCollisionStoresUnderANonCollidingName(t *testing.T) {
	h := newHarness(t)
	ada := h.account("ada", false)
	c := newUploader(t, h, ada)
	h.put(ada, "report.pdf", "the first one")
	h.scan(ada)
	first := entryNamed(t, h.list(t, c.secret, "").Entries, "report.pdf")

	upload := func(name, content string, replace bool) files.Entry {
		t.Helper()
		id := c.create("", name, len(content), replace)
		w := c.patch(id, 0, strings.NewReader(content))
		if w.Code != http.StatusCreated {
			t.Fatalf("uploading %s: %d %s", name, w.Code, w.Body.String())
		}
		return decodeBody[files.Entry](t, w)
	}

	second := upload("report.pdf", "the second one", false)
	if second.Name != "report (2).pdf" {
		t.Errorf("stored as %q, want report (2).pdf", second.Name)
	}
	third := upload("report.pdf", "the third one", false)
	if third.Name != "report (3).pdf" {
		t.Errorf("stored as %q, want report (3).pdf", third.Name)
	}

	// The original was never touched by either.
	if got := h.as(c.secret, "GET", "/api/download/report.pdf", nil).Body.String(); got != "the first one" {
		t.Errorf("the original after two collisions: %q", got)
	}
	if now := entryNamed(t, h.list(t, c.secret, "").Entries, "report.pdf"); now.ID != first.ID {
		t.Errorf("the original's identity changed from %d to %d", first.ID, now.ID)
	}
}

// 8.5: a replacement puts the previous content in trash before the new content
// becomes visible, so two sequential overwrites leave both prior versions.
func TestReplacementMovesPreviousContentToTrash(t *testing.T) {
	h := newHarness(t)
	ada := h.account("ada", false)
	c := newUploader(t, h, ada)
	h.put(ada, "notes.txt", "version one")
	h.scan(ada)

	trashPath := func(id int64) string {
		return filepath.Join(h.dataDir, "users", "ada", ".drive", "trash", fmt.Sprint(id), "notes.txt")
	}
	current := func() files.Entry {
		return entryNamed(t, h.list(t, c.secret, "").Entries, "notes.txt")
	}

	v1 := current()
	replace := func(content string) files.Entry {
		t.Helper()
		id := c.create("", "notes.txt", len(content), true)
		w := c.patch(id, 0, strings.NewReader(content))
		if w.Code != http.StatusCreated {
			t.Fatalf("replacing: %d %s", w.Code, w.Body.String())
		}
		return decodeBody[files.Entry](t, w)
	}

	v2 := replace("version two")
	if v2.ID == v1.ID {
		t.Error("the replacement reused the replaced file's identity")
	}
	if got := h.as(c.secret, "GET", "/api/download/notes.txt", nil).Body.String(); got != "version two" {
		t.Errorf("at the path after the first replace: %q", got)
	}
	if got, err := os.ReadFile(trashPath(v1.ID)); err != nil || string(got) != "version one" {
		t.Errorf("version one in trash: %q %v", got, err)
	}

	v3 := replace("version three")
	if got := h.as(c.secret, "GET", "/api/download/notes.txt", nil).Body.String(); got != "version three" {
		t.Errorf("at the path after the second replace: %q", got)
	}
	// Both prior versions are still accounted for, each under its own identity.
	for id, want := range map[int64]string{v1.ID: "version one", v2.ID: "version two"} {
		if got, err := os.ReadFile(trashPath(id)); err != nil || string(got) != want {
			t.Errorf("trashed %d: %q %v, want %q", id, got, err, want)
		}
		var state string
		h.db.QueryRow(`SELECT state FROM files WHERE id = ?`, id).Scan(&state)
		if state != "trashed" {
			t.Errorf("row %d is %q, want trashed", id, state)
		}
	}
	// Exactly one live entry at the path.
	entries := h.list(t, c.secret, "").Entries
	live := 0
	for _, e := range entries {
		if e.Name == "notes.txt" {
			live++
		}
	}
	if live != 1 || current().ID != v3.ID {
		t.Errorf("%d live entries named notes.txt: %+v", live, entries)
	}
}

// 8.6: upload data nobody came back for is reclaimed, and nothing a user can
// see is touched by the sweep.
func TestStaleUploadsAreReclaimed(t *testing.T) {
	h := newHarness(t)
	ada := h.account("ada", false)
	c := newUploader(t, h, ada)
	h.put(ada, "keep.txt", "not an upload")
	h.scan(ada)

	abandoned := c.create("", "abandoned.bin", 1000, false)
	c.patch(abandoned, 0, strings.NewReader(strings.Repeat("x", 400)))
	fresh := c.create("", "in-progress.bin", 1000, false)
	c.patch(fresh, 0, strings.NewReader(strings.Repeat("y", 400)))

	// Age only the abandoned one.
	if _, err := h.db.Exec(`UPDATE uploads SET updated_at = ? WHERE id = ?`,
		time.Now().Add(-48*time.Hour).Unix(), abandoned); err != nil {
		t.Fatal(err)
	}

	n, err := files.Reclaim(h.db, h.dataDir, 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("reclaimed %d uploads, want 1", n)
	}

	if w := h.do(c.req("HEAD", "/api/uploads/"+abandoned, nil)); w.Code != http.StatusNotFound {
		t.Errorf("the reclaimed upload: %d, want 404", w.Code)
	}
	// The one still in progress can carry on exactly where it was.
	if got := c.offset(fresh); got != 400 {
		t.Errorf("the live upload is at %d, want 400", got)
	}
	tmp, _ := os.ReadDir(filepath.Join(h.dataDir, "users", "ada", ".drive", "tmp"))
	if len(tmp) != 1 {
		t.Errorf("%d temp files after the sweep, want only the live upload's", len(tmp))
	}
	if got, err := os.ReadFile(filepath.Join(h.dataDir, "users", "ada", "keep.txt")); err != nil || string(got) != "not an upload" {
		t.Errorf("a real file after the sweep: %q %v", got, err)
	}
}

// 8.1: an upload is confined like every other write.
func TestUploadCannotEscapeItsRoot(t *testing.T) {
	h := newHarness(t)
	bob := h.account("bob", false)
	h.put(bob, "secret.txt", "bob's secret")
	ada := h.account("ada", false)
	c := newUploader(t, h, ada)

	for _, dest := range [][2]string{
		{"..", "escape.txt"},
		{"../bob", "planted.txt"},
		{".drive/tmp", "planted.txt"},
		{"", "../escape.txt"},
		{"", "a/b.txt"},
	} {
		meta := "filename " + base64.StdEncoding.EncodeToString([]byte(dest[1])) +
			",dir " + base64.StdEncoding.EncodeToString([]byte(dest[0]))
		req := c.req("POST", "/api/uploads", nil)
		req.Header.Set("Upload-Length", "10")
		req.Header.Set("Upload-Metadata", meta)
		if w := h.do(req); w.Code == http.StatusCreated {
			t.Errorf("an upload to %q/%q was accepted", dest[0], dest[1])
		}
	}
	if got, err := os.ReadFile(filepath.Join(h.dataDir, "users", "bob", "secret.txt")); err != nil || string(got) != "bob's secret" {
		t.Errorf("bob's file: %q %v", got, err)
	}
}

// 8.1: an upload belongs to whoever created it.
func TestUploadIsScopedToItsOwner(t *testing.T) {
	h := newHarness(t)
	ada := newUploader(t, h, h.account("ada", false))
	bob := newUploader(t, h, h.account("bob", false))

	id := ada.create("", "mine.txt", 10, false)
	for _, method := range []string{"HEAD", "DELETE"} {
		if w := h.do(bob.req(method, "/api/uploads/"+id, nil)); w.Code != http.StatusNotFound {
			t.Errorf("bob %s on ada's upload: %d, want 404", method, w.Code)
		}
	}
	if w := bob.patch(id, 0, strings.NewReader("stolen")); w.Code != http.StatusNotFound {
		t.Errorf("bob patching ada's upload: %d, want 404", w.Code)
	}
	if got := ada.offset(id); got != 0 {
		t.Errorf("ada's upload is at %d after bob's attempts, want 0", got)
	}
}

// brokenReader delivers fail bytes and then errors, the way a dropped
// connection does mid-chunk.
type brokenReader struct {
	data []byte
	fail int
	at   int
}

func (b *brokenReader) Read(p []byte) (int, error) {
	if b.at >= b.fail {
		return 0, io.ErrUnexpectedEOF
	}
	n := min(len(p), b.fail-b.at)
	copy(p, b.data[b.at:b.at+n])
	b.at += n
	return n, nil
}

// repeating is an endless reader of one byte, so a large body costs no memory
// to produce.
type repeating byte

func (r repeating) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = byte(r)
	}
	return len(p), nil
}
