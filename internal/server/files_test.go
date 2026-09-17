package server

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/fosslife/drive/internal/auth"
	"github.com/fosslife/drive/internal/files"
)

// listing is the shape of a /api/list response.
type listing struct {
	Path    string        `json:"path"`
	Entries []files.Entry `json:"entries"`
	Next    string        `json:"next"`
}

// withFiles returns a harness holding one user with a small tree already
// indexed, plus that user's API token.
func withFiles(t *testing.T) (*harness, *auth.User, string) {
	t.Helper()
	h := newHarness(t)
	ada := h.account("ada", false)
	h.put(ada, "notes.txt", "ada's notes")
	h.put(ada, "docs/budget.csv", "a,b,c\n1,2,3\n")
	h.put(ada, "docs/deep/readme.md", "# hello")
	h.scan(ada)
	return h, ada, h.token(ada)
}

func (h *harness) list(t *testing.T, secret, dir string) listing {
	t.Helper()
	w := h.as(secret, "GET", "/api/list?path="+url.QueryEscape(dir), nil)
	if w.Code != http.StatusOK {
		t.Fatalf("listing %q: %d %s", dir, w.Code, w.Body.String())
	}
	return decodeBody[listing](t, w)
}

// 7.1: every direct child, with name, type, size, and modification time.
func TestListReturnsEveryChildWithItsDetails(t *testing.T) {
	h, _, secret := withFiles(t)

	got := h.list(t, secret, "")
	if len(got.Entries) != 2 {
		t.Fatalf("top level has %d entries: %+v, want notes.txt and docs", len(got.Entries), got.Entries)
	}
	byName := map[string]files.Entry{}
	for _, e := range got.Entries {
		byName[e.Name] = e
	}

	notes := byName["notes.txt"]
	if notes.Kind != "file" || notes.Size != int64(len("ada's notes")) {
		t.Errorf("notes.txt = %+v, want a file of %d bytes", notes, len("ada's notes"))
	}
	if notes.ModTime.IsZero() || time.Since(notes.ModTime) > time.Hour {
		t.Errorf("notes.txt modified %v, want roughly now", notes.ModTime)
	}
	if notes.ETag == "" {
		t.Error("notes.txt has no ETag to make a precondition out of")
	}
	if docs := byName["docs"]; docs.Kind != "folder" {
		t.Errorf("docs = %+v, want a folder", docs)
	}

	// Children are the folder's own, not the whole subtree.
	sub := h.list(t, secret, "docs")
	names := []string{}
	for _, e := range sub.Entries {
		names = append(names, e.Name)
	}
	if strings.Join(names, ",") != "budget.csv,deep" {
		t.Errorf("docs contains %v, want budget.csv and deep", names)
	}
}

// 7.1: a folder of 100,000 entries returns its first page without the folder
// passing through memory. The rows are inserted directly because it is the
// index a listing reads, and 100,000 real files would measure the filesystem.
func TestLargeFolderFirstPageDoesNotLoadTheFolder(t *testing.T) {
	h := newHarness(t)
	ada := h.account("ada", false)
	secret := h.token(ada)

	const entries = 100_000
	tx, err := h.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	stmt, err := tx.Prepare(`INSERT INTO files (user_id, dir, name, kind, size, mtime, checksum)
	                         VALUES (?, 'bulk', ?, 'file', 10, 1, 'x')`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`INSERT INTO files (user_id, dir, name, kind, size, mtime)
	                      VALUES (?, '', 'bulk', 'folder', 0, 1)`, ada.ID); err != nil {
		t.Fatal(err)
	}
	for i := range entries {
		if _, err := stmt.Exec(ada.ID, fmt.Sprintf("file-%06d.txt", i)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	start := time.Now()
	w := h.as(secret, "GET", "/api/list?path=bulk&limit=100", nil)
	elapsed := time.Since(start)
	runtime.ReadMemStats(&after)

	if w.Code != http.StatusOK {
		t.Fatalf("listing the large folder: %d %s", w.Code, w.Body.String())
	}
	got := decodeBody[listing](t, w)
	if len(got.Entries) != 100 {
		t.Fatalf("first page has %d entries, want 100", len(got.Entries))
	}
	if got.Entries[0].Name != "file-000000.txt" || got.Next != "file-000099.txt" {
		t.Errorf("first page runs %s..%s with cursor %q", got.Entries[0].Name, got.Entries[99].Name, got.Next)
	}

	// Holding all 100,000 rows would be megabytes. A page of 100 is kilobytes,
	// so a generous ceiling still fails loudly if the whole folder is read.
	const ceiling = 2 << 20
	if allocated := after.TotalAlloc - before.TotalAlloc; allocated > ceiling {
		t.Errorf("the first page allocated %d bytes, want under %d: the whole folder is being read", allocated, ceiling)
	}
	if elapsed > 2*time.Second {
		t.Errorf("the first page took %v", elapsed)
	}

	// The cursor walks on rather than restarting.
	next := h.as(secret, "GET", "/api/list?path=bulk&limit=100&cursor="+got.Next, nil)
	page2 := decodeBody[listing](t, next)
	if page2.Entries[0].Name != "file-000100.txt" {
		t.Errorf("second page starts at %s, want file-000100.txt", page2.Entries[0].Name)
	}
}

// 7.2: a folder that does not exist is a not-found, never an empty listing. An
// empty page for a typo'd path reads as data loss.
func TestListingAMissingFolderIsNotFound(t *testing.T) {
	h, _, secret := withFiles(t)

	for _, dir := range []string{"nope", "docs/nope", "notes.txt/deeper"} {
		w := h.as(secret, "GET", "/api/list?path="+url.QueryEscape(dir), nil)
		if w.Code != http.StatusNotFound {
			t.Errorf("listing %q: %d %s, want 404", dir, w.Code, w.Body.String())
		}
		if strings.Contains(w.Body.String(), `"entries"`) {
			t.Errorf("listing %q returned a partial listing: %s", dir, w.Body.String())
		}
	}

	// A file is not a folder, and says so rather than 404ing.
	if w := h.as(secret, "GET", "/api/list?path=notes.txt", nil); w.Code != http.StatusBadRequest {
		t.Errorf("listing a file: %d %s, want 400", w.Code, w.Body.String())
	}
}

// 3.2/7.1: .drive is the machinery, not the user's files. It is not listed, not
// reachable, and not something a path can name.
func TestInternalDirectoryIsNotReachable(t *testing.T) {
	h, _, secret := withFiles(t)

	if slicesContains(h.list(t, secret, "").Entries, ".drive") {
		t.Error(".drive appears in the top-level listing")
	}
	for _, path := range []string{".drive", ".drive/tmp", ".drive/trash"} {
		if w := h.as(secret, "GET", "/api/list?path="+url.QueryEscape(path), nil); w.Code == http.StatusOK {
			t.Errorf("listing %q succeeded", path)
		}
		if w := h.as(secret, "GET", "/api/download/"+path, nil); w.Code == http.StatusOK {
			t.Errorf("downloading %q succeeded", path)
		}
		if w := h.as(secret, "GET", "/api/archive?path="+url.QueryEscape(path), nil); w.Code == http.StatusOK {
			t.Errorf("archiving %q succeeded", path)
		}
	}
	if w := h.as(secret, "POST", "/api/move", map[string]any{"from": "notes.txt", "to": ".drive/tmp/x"}); w.Code == http.StatusOK {
		t.Error("a file was moved into .drive")
	}
	if w := h.as(secret, "POST", "/api/folders", map[string]string{"path": ".drive/mine"}); w.Code == http.StatusCreated {
		t.Error("a folder was created inside .drive")
	}
}

// 7.3: a full download is byte-identical and a range request returns only the
// range it asked for.
func TestDownloadIsByteIdenticalAndRanged(t *testing.T) {
	h := newHarness(t)
	ada := h.account("ada", false)
	secret := h.token(ada)

	content := strings.Repeat("abcdefghij", 5000) // 50,000 bytes
	h.put(ada, "big.bin", content)
	h.scan(ada)

	w := h.as(secret, "GET", "/api/download/big.bin", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("download: %d %s", w.Code, w.Body.String())
	}
	if w.Body.String() != content {
		t.Errorf("downloaded %d bytes, want the stored %d", w.Body.Len(), len(content))
	}
	if w.Header().Get("ETag") == "" {
		t.Error("the download carries no ETag to build a precondition on")
	}
	if w.Header().Get("Accept-Ranges") != "bytes" {
		t.Errorf("Accept-Ranges = %q, want bytes", w.Header().Get("Accept-Ranges"))
	}

	req := request("GET", "/api/download/big.bin", nil)
	req.Header.Set("Authorization", "Bearer "+secret)
	req.Header.Set("Range", "bytes=100-199")
	ranged := h.do(req)
	if ranged.Code != http.StatusPartialContent {
		t.Fatalf("ranged download: %d %s, want 206", ranged.Code, ranged.Body.String())
	}
	if ranged.Body.String() != content[100:200] {
		t.Errorf("range returned %d bytes, want exactly the 100 asked for", ranged.Body.Len())
	}
	if got := ranged.Header().Get("Content-Range"); got != "bytes 100-199/50000" {
		t.Errorf("Content-Range = %q", got)
	}

	if w := h.as(secret, "GET", "/api/download/missing.txt", nil); w.Code != http.StatusNotFound {
		t.Errorf("downloading a missing file: %d, want 404", w.Code)
	}
	if w := h.as(secret, "GET", "/api/download/../../etc/passwd", nil); w.Code == http.StatusOK {
		t.Error("a traversal path was served")
	}
}

// 7.8: stored bytes never run in this origin. HTML and SVG come back as opaque
// downloads; an allowlisted raster image renders.
func TestStoredContentCannotExecuteInTheApplicationOrigin(t *testing.T) {
	h := newHarness(t)
	ada := h.account("ada", false)
	secret := h.token(ada)

	h.put(ada, "page.html", `<script>fetch('/api/me')</script>`)
	h.put(ada, "logo.svg", `<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`)
	h.put(ada, "report.pdf", "%PDF-1.4 /JavaScript")
	h.put(ada, "photo.jpg", "\xff\xd8\xff\xe0 not really a jpeg")
	h.scan(ada)

	for _, name := range []string{"page.html", "logo.svg", "report.pdf"} {
		w := h.as(secret, "GET", "/api/download/"+name, nil)
		if w.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", name, w.Code, w.Body.String())
		}
		h := w.Header()
		if !strings.HasPrefix(h.Get("Content-Disposition"), "attachment") {
			t.Errorf("%s: Content-Disposition = %q, want attachment", name, h.Get("Content-Disposition"))
		}
		if ct := h.Get("Content-Type"); ct != "application/octet-stream" {
			t.Errorf("%s: Content-Type = %q, want application/octet-stream", name, ct)
		}
		if h.Get("X-Content-Type-Options") != "nosniff" {
			t.Errorf("%s: no nosniff", name)
		}
		csp := h.Get("Content-Security-Policy")
		if !strings.Contains(csp, "default-src 'none'") || !strings.Contains(csp, "sandbox") {
			t.Errorf("%s: Content-Security-Policy = %q", name, csp)
		}
	}

	img := h.as(secret, "GET", "/api/download/photo.jpg", nil)
	if got := img.Header().Get("Content-Type"); got != "image/jpeg" {
		t.Errorf("photo.jpg: Content-Type = %q, want image/jpeg", got)
	}
	if got := img.Header().Get("Content-Disposition"); !strings.HasPrefix(got, "inline") {
		t.Errorf("photo.jpg: Content-Disposition = %q, want inline", got)
	}
	if img.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Error("photo.jpg: no nosniff")
	}

	// An inline type can still be asked for as a download.
	forced := h.as(secret, "GET", "/api/download/photo.jpg?download", nil)
	if got := forced.Header().Get("Content-Disposition"); !strings.HasPrefix(got, "attachment") {
		t.Errorf("photo.jpg?download: Content-Disposition = %q, want attachment", got)
	}
}

// 7.4: create, rename, and move, with identity and contents surviving each.
func TestCreateRenameAndMovePreserveIdentity(t *testing.T) {
	h, ada, secret := withFiles(t)

	if w := h.as(secret, "POST", "/api/folders", map[string]string{"path": "archive/2025"}); w.Code != http.StatusCreated {
		t.Fatalf("creating a folder: %d %s", w.Code, w.Body.String())
	}
	if _, err := os.Stat(filepath.Join(h.dataDir, "users", "ada", "archive", "2025")); err != nil {
		t.Errorf("the created folder is not on disk: %v", err)
	}
	top := h.list(t, secret, "")
	if !slicesContains(top.Entries, "archive") {
		t.Errorf("the new folder is not in its parent's listing: %+v", top.Entries)
	}

	before := h.list(t, secret, "")
	notesID := entryNamed(t, before.Entries, "notes.txt").ID

	// Rename: same identity, same contents, new name.
	if w := h.as(secret, "POST", "/api/move", map[string]any{"from": "notes.txt", "to": "journal.txt"}); w.Code != http.StatusOK {
		t.Fatalf("renaming: %d %s", w.Code, w.Body.String())
	}
	renamed := entryNamed(t, h.list(t, secret, "").Entries, "journal.txt")
	if renamed.ID != notesID {
		t.Errorf("rename changed the identifier from %d to %d", notesID, renamed.ID)
	}
	if body := h.as(secret, "GET", "/api/download/journal.txt", nil).Body.String(); body != "ada's notes" {
		t.Errorf("contents after rename: %q", body)
	}

	// Move a folder with contents: everything comes with it, once.
	docsID := entryNamed(t, h.list(t, secret, "").Entries, "docs").ID
	if w := h.as(secret, "POST", "/api/move", map[string]any{"from": "docs", "to": "archive/2025/docs"}); w.Code != http.StatusOK {
		t.Fatalf("moving a folder: %d %s", w.Code, w.Body.String())
	}
	moved := entryNamed(t, h.list(t, secret, "archive/2025").Entries, "docs")
	if moved.ID != docsID {
		t.Errorf("moving the folder changed its identifier from %d to %d", docsID, moved.ID)
	}
	if slicesContains(h.list(t, secret, "").Entries, "docs") {
		t.Error("the folder is still listed at its old location")
	}
	if got := h.as(secret, "GET", "/api/download/archive/2025/docs/deep/readme.md", nil).Body.String(); got != "# hello" {
		t.Errorf("a nested file after the folder move: %q", got)
	}

	// And the reconciler agrees with what is on disk: no duplicates, nothing missing.
	h.scan(ada)
	var rows int
	if err := h.db.QueryRow(`SELECT count(*) FROM files WHERE user_id = ? AND state = 'missing'`, ada.ID).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 0 {
		t.Errorf("%d rows are marked missing after the move; the index and the disk disagree", rows)
	}
}

// 7.5: a move onto an occupied name is refused, and neither side changes.
func TestMoveOntoAnExistingNameIsRefused(t *testing.T) {
	h, ada, secret := withFiles(t)
	h.put(ada, "journal.txt", "the other file")
	h.scan(ada)

	w := h.as(secret, "POST", "/api/move", map[string]any{"from": "notes.txt", "to": "journal.txt"})
	if w.Code != http.StatusConflict {
		t.Fatalf("moving onto an existing name: %d %s, want 409", w.Code, w.Body.String())
	}
	if got := h.as(secret, "GET", "/api/download/notes.txt", nil).Body.String(); got != "ada's notes" {
		t.Errorf("the source after a refused move: %q", got)
	}
	if got := h.as(secret, "GET", "/api/download/journal.txt", nil).Body.String(); got != "the other file" {
		t.Errorf("the destination after a refused move: %q", got)
	}

	// With replacement asked for, the displaced content goes to trash first —
	// nothing but a permanent delete may destroy it.
	displaced := entryNamed(t, h.list(t, secret, "").Entries, "journal.txt")
	if w := h.as(secret, "POST", "/api/move", map[string]any{
		"from": "notes.txt", "to": "journal.txt", "replace": true,
	}); w.Code != http.StatusOK {
		t.Fatalf("replacing: %d %s", w.Code, w.Body.String())
	}
	if got := h.as(secret, "GET", "/api/download/journal.txt", nil).Body.String(); got != "ada's notes" {
		t.Errorf("the destination after the replace: %q", got)
	}

	trashed, err := os.ReadFile(filepath.Join(h.dataDir, "users", "ada", ".drive", "trash",
		fmt.Sprint(displaced.ID), "journal.txt"))
	if err != nil || string(trashed) != "the other file" {
		t.Errorf("the replaced content in trash: %q %v, want it intact", trashed, err)
	}
	var state string
	if err := h.db.QueryRow(`SELECT state FROM files WHERE id = ?`, displaced.ID).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if state != "trashed" {
		t.Errorf("the replaced row is %q, want trashed", state)
	}
	if slicesContains(h.list(t, secret, "").Entries, "notes.txt") {
		t.Error("the source is still listed after the move")
	}
}

// 7.6: a folder cannot be moved inside itself. One syscall would detach the
// whole subtree from the filesystem.
func TestMovingAFolderIntoItsOwnDescendantIsRefused(t *testing.T) {
	h, ada, secret := withFiles(t)

	for _, to := range []string{"docs/deep/docs", "docs/docs", "docs/deep"} {
		w := h.as(secret, "POST", "/api/move", map[string]any{"from": "docs", "to": to})
		if w.Code != http.StatusBadRequest && w.Code != http.StatusConflict {
			t.Errorf("moving docs into %q: %d %s, want a refusal", to, w.Code, w.Body.String())
		}
	}

	// Nothing moved.
	if got := h.as(secret, "GET", "/api/download/docs/deep/readme.md", nil).Body.String(); got != "# hello" {
		t.Errorf("the subtree after the refusals: %q", got)
	}
	h.scan(ada)
	var missing int
	h.db.QueryRow(`SELECT count(*) FROM files WHERE user_id = ? AND state = 'missing'`, ada.ID).Scan(&missing)
	if missing != 0 {
		t.Errorf("%d rows went missing after a refused move", missing)
	}
}

// 7.7: last write wins, unless the caller says what it expected to find.
func TestStaleETagPreconditionIsRejected(t *testing.T) {
	h, ada, secret := withFiles(t)
	stale := entryNamed(t, h.list(t, secret, "").Entries, "notes.txt").ETag

	// Something changes the file behind the caller's back.
	h.put(ada, "notes.txt", "rewritten by someone else, at a different length")
	h.scan(ada)

	move := func(etag, to string) *httptest.ResponseRecorder {
		req := request("POST", "/api/move", map[string]any{"from": "notes.txt", "to": to})
		req.Header.Set("Authorization", "Bearer "+secret)
		if etag != "" {
			req.Header.Set("If-Match", etag)
		}
		return h.do(req)
	}

	if w := move(stale, "journal.txt"); w.Code != http.StatusPreconditionFailed {
		t.Fatalf("a stale precondition: %d %s, want 412", w.Code, w.Body.String())
	}
	if !slicesContains(h.list(t, secret, "").Entries, "notes.txt") {
		t.Error("the refused move happened anyway")
	}

	current := entryNamed(t, h.list(t, secret, "").Entries, "notes.txt").ETag
	if current == stale {
		t.Fatal("the ETag did not change when the file did")
	}
	if w := move(current, "journal.txt"); w.Code != http.StatusOK {
		t.Errorf("a current precondition: %d %s", w.Code, w.Body.String())
	}

	// No precondition at all is last-write-wins, which is the default.
	req := request("POST", "/api/move", map[string]any{"from": "journal.txt", "to": "notes.txt"})
	req.Header.Set("Authorization", "Bearer "+secret)
	if w := h.do(req); w.Code != http.StatusOK {
		t.Errorf("a move with no precondition: %d %s", w.Code, w.Body.String())
	}
}

// 7.9: a folder download is one archive, streamed, structure intact, with
// trashed and other users' items absent.
func TestArchiveStreamsAFolder(t *testing.T) {
	h, ada, secret := withFiles(t)
	bob := h.account("bob", false)
	h.put(bob, "secret.txt", "bob's secret")
	h.scan(bob)
	h.put(ada, "docs/gone.txt", "about to be trashed")
	h.scan(ada)

	// Trash one entry: an archive must not carry it.
	gone := entryNamed(t, h.list(t, secret, "docs").Entries, "gone.txt")
	root, err := h.users.Root(ada)
	if err != nil {
		t.Fatal(err)
	}
	if err := files.Trash(h.db, ada.ID, root, gone); err != nil {
		t.Fatal(err)
	}
	root.Close()

	w := h.as(secret, "GET", "/api/archive?path=docs", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("archive: %d %s", w.Code, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/zip" {
		t.Errorf("Content-Type = %q, want application/zip", ct)
	}
	if !strings.Contains(w.Header().Get("Content-Disposition"), "docs.zip") {
		t.Errorf("Content-Disposition = %q", w.Header().Get("Content-Disposition"))
	}

	got := readZip(t, w.Body.Bytes())
	if got["docs/budget.csv"] != "a,b,c\n1,2,3\n" || got["docs/deep/readme.md"] != "# hello" {
		t.Errorf("archive contents: %v", keys(got))
	}
	if _, ok := got["docs/deep/"]; !ok {
		t.Errorf("the archive lost the folder structure: %v", keys(got))
	}
	if _, ok := got["docs/gone.txt"]; ok {
		t.Error("the archive contains a trashed file")
	}
	for name := range got {
		if strings.Contains(name, "secret") {
			t.Errorf("the archive contains another user's file: %q", name)
		}
	}

	// A selection of several items arrives as one archive of exactly those.
	multi := h.as(secret, "GET", "/api/archive?path=docs%2Fbudget.csv&path=docs%2Fdeep", nil)
	if multi.Code != http.StatusOK {
		t.Fatalf("multi-selection archive: %d %s", multi.Code, multi.Body.String())
	}
	sel := readZip(t, multi.Body.Bytes())
	if sel["budget.csv"] != "a,b,c\n1,2,3\n" || sel["deep/readme.md"] != "# hello" {
		t.Errorf("selection archive: %v", keys(sel))
	}
	if len(sel) != 3 { // budget.csv, deep/, deep/readme.md
		t.Errorf("selection archive holds %v, want exactly the selection", keys(sel))
	}

	// Another user's path is not reachable through the archive endpoint either.
	if w := h.as(secret, "GET", "/api/archive?path=secret.txt", nil); w.Code != http.StatusNotFound {
		t.Errorf("archiving another user's file: %d, want 404", w.Code)
	}
}

// 7.9: a folder larger than memory streams out without being buffered or
// staged. Nothing new appears under the data directory while it is produced.
func TestArchiveDoesNotStageOrBuffer(t *testing.T) {
	h := newHarness(t)
	ada := h.account("ada", false)
	secret := h.token(ada)

	const (
		chunks    = 40
		chunkSize = 1 << 20 // 40 MiB total, well past any sane buffer
	)
	blob := strings.Repeat("z", chunkSize)
	for i := range chunks {
		h.put(ada, fmt.Sprintf("big/part-%02d.bin", i), blob)
	}
	h.scan(ada)

	srv := httptest.NewServer(h.Handler())
	defer srv.Close()
	req, err := http.NewRequest("GET", srv.URL+"/api/archive?path=big", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+secret)

	filesBefore := countFiles(t, h.dataDir)
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("archive: %d", resp.StatusCode)
	}
	// No Content-Length: the server cannot know the size without producing the
	// whole archive first, which is precisely what it does not do.
	if resp.ContentLength != -1 {
		t.Errorf("Content-Length = %d, want unknown: the archive was produced before it was sent", resp.ContentLength)
	}

	n, err := io.Copy(io.Discard, resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	runtime.ReadMemStats(&after)

	if n < chunks*chunkSize {
		t.Errorf("archive is %d bytes, want at least the %d it contains", n, chunks*chunkSize)
	}
	if filesAfter := countFiles(t, h.dataDir); filesAfter != filesBefore {
		t.Errorf("the data directory gained %d files: the archive was staged on disk", filesAfter-filesBefore)
	}
	// A buffered 40 MiB archive would show up plainly here.
	const ceiling = 16 << 20
	if grew := after.HeapAlloc; grew > before.HeapAlloc+ceiling {
		t.Errorf("heap grew from %d to %d streaming a %d byte archive", before.HeapAlloc, grew, n)
	}
}

func readZip(t *testing.T, raw []byte) map[string]string {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		t.Fatalf("reading the archive: %v", err)
	}
	out := map[string]string{}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			t.Fatal(err)
		}
		out[f.Name] = string(body)
	}
	return out
}

func countFiles(t *testing.T, dir string) int {
	t.Helper()
	n := 0
	err := filepath.WalkDir(dir, func(_ string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			n++
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func entryNamed(t *testing.T, entries []files.Entry, name string) files.Entry {
	t.Helper()
	for _, e := range entries {
		if e.Name == name {
			return e
		}
	}
	t.Fatalf("no entry named %q in %+v", name, entries)
	return files.Entry{}
}

func slicesContains(entries []files.Entry, name string) bool {
	for _, e := range entries {
		if e.Name == name {
			return true
		}
	}
	return false
}

func keys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
