package server

import (
	"io"
	"net/http"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/fosslife/drive/internal/auth"
	"github.com/fosslife/drive/internal/files"
)

func itoa(id int64) string { return strconv.FormatInt(id, 10) }

// trashListing is the shape of a /api/trash response.
type trashListing struct {
	Entries []files.TrashEntry `json:"entries"`
	Next    string             `json:"next"`
}

func (h *harness) trash(t *testing.T, secret string) trashListing {
	t.Helper()
	w := h.as(secret, "GET", "/api/trash", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("listing the trash: %d %s", w.Code, w.Body.String())
	}
	return decodeBody[trashListing](t, w)
}

// delete removes a path the way a client does, and returns the entry as it was.
func (h *harness) delete(t *testing.T, secret, path string) files.Entry {
	t.Helper()
	w := h.as(secret, "DELETE", "/api/files/"+path, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("deleting %q: %d %s", path, w.Code, w.Body.String())
	}
	return decodeBody[files.Entry](t, w)
}

// readTrashed reads a trashed entry's content through the storage API, which is
// the only way in: the ordinary file API refuses to name .drive at all.
func readTrashed(t *testing.T, h *harness, u *auth.User, id int64, rel string) string {
	t.Helper()
	root, err := h.users.Root(u)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	f, err := root.OpenTrashed(id, rel)
	if err != nil {
		t.Fatalf("opening trashed %d/%q: %v", id, rel, err)
	}
	defer f.Close()
	content, err := io.ReadAll(f)
	if err != nil {
		t.Fatal(err)
	}
	return string(content)
}

// 9.1: deleting takes a file out of its folder and puts it in the trash with
// its original path, still readable. Nothing is destroyed.
func TestDeleteMovesToTrashAndKeepsTheContent(t *testing.T) {
	h, ada, secret := withFiles(t)

	deleted := h.delete(t, secret, "notes.txt")

	if slicesContains(h.list(t, secret, "").Entries, "notes.txt") {
		t.Error("the deleted file is still in its folder's listing")
	}
	if w := h.as(secret, "GET", "/api/download/notes.txt", nil); w.Code != http.StatusNotFound {
		t.Errorf("downloading a deleted file: %d, want 404", w.Code)
	}

	trashed := h.trash(t, secret).Entries
	if len(trashed) != 1 || trashed[0].ID != deleted.ID || trashed[0].Path != "notes.txt" {
		t.Fatalf("trash holds %+v, want notes.txt with id %d", trashed, deleted.ID)
	}
	if time.Since(trashed[0].DeletedAt) > time.Hour {
		t.Errorf("deleted at %v, want roughly now", trashed[0].DeletedAt)
	}
	if got := readTrashed(t, h, ada, deleted.ID, ""); got != "ada's notes" {
		t.Errorf("the trashed content is %q, want it intact", got)
	}
}

// 9.2: a folder goes to trash as one unit — one entry in the trash, not one
// per file — and restoring it brings back every descendant.
func TestDeletingAFolderTrashesAndRestoresItWhole(t *testing.T) {
	h, ada, secret := withFiles(t)

	docs := h.delete(t, secret, "docs")

	if slicesContains(h.list(t, secret, "").Entries, "docs") {
		t.Error("the deleted folder is still listed")
	}
	trashed := h.trash(t, secret).Entries
	if len(trashed) != 1 || trashed[0].Path != "docs" {
		t.Fatalf("trash holds %+v, want the folder alone", trashed)
	}
	// The contents went with it and are readable at their path inside it.
	if got := readTrashed(t, h, ada, docs.ID, "deep/readme.md"); got != "# hello" {
		t.Errorf("a file inside the trashed folder reads %q", got)
	}

	if w := h.as(secret, "POST", "/api/trash/"+itoa(docs.ID)+"/restore", nil); w.Code != http.StatusOK {
		t.Fatalf("restoring: %d %s", w.Code, w.Body.String())
	}

	if !slicesContains(h.list(t, secret, "docs").Entries, "budget.csv") ||
		!slicesContains(h.list(t, secret, "docs").Entries, "deep") {
		t.Errorf("docs after restore holds %+v", h.list(t, secret, "docs").Entries)
	}
	if got := h.as(secret, "GET", "/api/download/docs/deep/readme.md", nil).Body.String(); got != "# hello" {
		t.Errorf("a restored descendant reads %q, want it back", got)
	}
	if len(h.trash(t, secret).Entries) != 0 {
		t.Error("the restored folder is still in the trash")
	}
}

// 9.3: restore puts a file back where it came from.
func TestRestorePutsAFileBackAtItsOriginalPath(t *testing.T) {
	h, _, secret := withFiles(t)

	deleted := h.delete(t, secret, "docs/budget.csv")
	w := h.as(secret, "POST", "/api/trash/"+itoa(deleted.ID)+"/restore", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("restoring: %d %s", w.Code, w.Body.String())
	}

	back := decodeBody[files.Entry](t, w)
	if back.Path != "docs/budget.csv" || back.ID != deleted.ID {
		t.Errorf("restored to %+v, want docs/budget.csv with its original identity", back)
	}
	if got := h.as(secret, "GET", "/api/download/docs/budget.csv", nil).Body.String(); got != "a,b,c\n1,2,3\n" {
		t.Errorf("the restored file reads %q", got)
	}
	if len(h.trash(t, secret).Entries) != 0 {
		t.Error("the restored file is still in the trash")
	}
}

// 9.3: the original parent may have been deleted since. The file comes back
// anyway, with the folder recreated under it.
func TestRestoreRecreatesADeletedParent(t *testing.T) {
	h, _, secret := withFiles(t)

	budget := h.delete(t, secret, "docs/budget.csv")
	h.delete(t, secret, "docs") // the parent follows it into the trash

	w := h.as(secret, "POST", "/api/trash/"+itoa(budget.ID)+"/restore", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("restoring into a deleted folder: %d %s", w.Code, w.Body.String())
	}
	if back := decodeBody[files.Entry](t, w); back.Path != "docs/budget.csv" {
		t.Errorf("restored to %q, want docs/budget.csv", back.Path)
	}
	if got := h.as(secret, "GET", "/api/download/docs/budget.csv", nil).Body.String(); got != "a,b,c\n1,2,3\n" {
		t.Errorf("the restored file reads %q, want its contents back", got)
	}
	// The recreated parent is a real folder in the listing, not just a path.
	if !slicesContains(h.list(t, secret, "").Entries, "docs") {
		t.Error("the recreated parent is not listed")
	}
}

// 9.3: something else may hold the path by the time a file is restored.
// Neither file may be lost, so the restore reports where it actually landed.
func TestRestoreFallsBackWhenThePathIsTaken(t *testing.T) {
	h, ada, secret := withFiles(t)

	deleted := h.delete(t, secret, "notes.txt")
	h.put(ada, "notes.txt", "a newer note")
	h.scan(ada)

	w := h.as(secret, "POST", "/api/trash/"+itoa(deleted.ID)+"/restore", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("restoring onto an occupied path: %d %s", w.Code, w.Body.String())
	}
	back := decodeBody[files.Entry](t, w)
	if back.Path != "notes (2).txt" {
		t.Fatalf("restored to %q, want a reported fallback beside the original", back.Path)
	}
	if got := h.as(secret, "GET", "/api/download/notes.txt", nil).Body.String(); got != "a newer note" {
		t.Errorf("the occupying file reads %q, want it untouched", got)
	}
	if got := h.as(secret, "GET", "/api/download/notes%20(2).txt", nil).Body.String(); got != "ada's notes" {
		t.Errorf("the restored file reads %q, want the trashed contents", got)
	}
}

// 9.4: permanent deletion on demand is what actually frees the bytes.
func TestPurgeDestroysContentAndEmptyTrashClearsTheRest(t *testing.T) {
	h, ada, secret := withFiles(t)

	notes := h.delete(t, secret, "notes.txt")
	docs := h.delete(t, secret, "docs")

	if w := h.as(secret, "DELETE", "/api/trash/"+itoa(notes.ID), nil); w.Code != http.StatusNoContent {
		t.Fatalf("purging: %d %s", w.Code, w.Body.String())
	}
	if _, err := os.Stat(h.trashPath(ada, notes.ID)); !os.IsNotExist(err) {
		t.Errorf("the purged content is still on disk: %v", err)
	}
	var rows int
	h.db.QueryRow(`SELECT count(*) FROM files WHERE id = ?`, notes.ID).Scan(&rows)
	if rows != 0 {
		t.Error("the purged file still has an index row")
	}
	// The other deletion is untouched by that one.
	if trashed := h.trash(t, secret).Entries; len(trashed) != 1 || trashed[0].ID != docs.ID {
		t.Fatalf("trash holds %+v, want the folder still there", trashed)
	}

	if w := h.as(secret, "DELETE", "/api/trash", nil); w.Code != http.StatusOK {
		t.Fatalf("emptying the trash: %d %s", w.Code, w.Body.String())
	}
	if len(h.trash(t, secret).Entries) != 0 {
		t.Error("the trash is not empty after emptying it")
	}
	if _, err := os.Stat(h.trashPath(ada, docs.ID)); !os.IsNotExist(err) {
		t.Errorf("the purged folder is still on disk: %v", err)
	}
	// Purging the trash is not allowed to touch anything a user can still see.
	var live int
	h.db.QueryRow(`SELECT count(*) FROM files WHERE user_id = ? AND state = 'present'`, ada.ID).Scan(&live)
	if live != 0 {
		t.Errorf("%d rows are still present after everything was deleted and purged", live)
	}
}

// 9.4: trash expires after the retention period, and a retention of zero is the
// never-expire setting.
func TestTrashExpiresAfterRetentionUnlessItNeverExpires(t *testing.T) {
	h, ada, secret := withFiles(t)
	deleted := h.delete(t, secret, "notes.txt")

	// Deleted forty days ago, as far as the sweep is concerned.
	if _, err := h.db.Exec(`UPDATE files SET trashed_at = ? WHERE id = ?`,
		time.Now().Add(-40*24*time.Hour).Unix(), deleted.ID); err != nil {
		t.Fatal(err)
	}

	// Never-expire first: the item is well past any retention period, and zero
	// must still leave it alone.
	if n, err := files.ExpireTrash(h.db, h.dataDir, 0); err != nil || n != 0 {
		t.Fatalf("never-expire swept %d items: %v", n, err)
	}
	if len(h.trash(t, secret).Entries) != 1 {
		t.Fatal("never-expire removed the item anyway")
	}

	n, err := files.ExpireTrash(h.db, h.dataDir, 30*24*time.Hour)
	if err != nil || n != 1 {
		t.Fatalf("expiring: %d items, %v, want 1", n, err)
	}
	if _, err := os.Stat(h.trashPath(ada, deleted.ID)); !os.IsNotExist(err) {
		t.Errorf("the expired content is still on disk: %v", err)
	}
	if len(h.trash(t, secret).Entries) != 0 {
		t.Error("the expired item is still in the trash")
	}

	// A file deleted just now survives the same sweep.
	fresh := h.delete(t, secret, "docs/budget.csv")
	if n, err := files.ExpireTrash(h.db, h.dataDir, 30*24*time.Hour); err != nil || n != 0 {
		t.Fatalf("expiring a fresh deletion: %d items, %v", n, err)
	}
	if got := readTrashed(t, h, ada, fresh.ID, ""); got != "a,b,c\n1,2,3\n" {
		t.Errorf("the fresh deletion reads %q, want it kept", got)
	}
}

// 9.1: one user's trash is their own, and nobody else can name an item in it.
func TestTrashIsPerUser(t *testing.T) {
	h, _, secret := withFiles(t)
	bob := h.account("bob", false)
	bobsToken := h.token(bob)

	deleted := h.delete(t, secret, "notes.txt")

	if entries := h.trash(t, bobsToken).Entries; len(entries) != 0 {
		t.Errorf("bob sees %+v in his trash", entries)
	}
	for _, req := range [][2]string{
		{"POST", "/api/trash/" + itoa(deleted.ID) + "/restore"},
		{"DELETE", "/api/trash/" + itoa(deleted.ID)},
	} {
		if w := h.as(bobsToken, req[0], req[1], nil); w.Code != http.StatusNotFound {
			t.Errorf("bob %s %s: %d, want 404", req[0], req[1], w.Code)
		}
	}
	if len(h.trash(t, secret).Entries) != 1 {
		t.Error("ada's trashed file did not survive bob's attempts on it")
	}
}
