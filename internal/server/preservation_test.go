package server

import (
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/fosslife/drive/internal/files"
)

// 15.7: the rule the whole design is arranged around — nothing but a permanent
// delete destroys content. Here are the three operations that displace one
// file's bytes with another's, each run to the end, each followed by getting
// the previous version back out of the trash.
func TestOnlyAPermanentDeleteDestroysContent(t *testing.T) {
	h := newHarness(t)
	ada := h.account("ada", false)
	c := newUploader(t, h, ada)
	secret := c.secret

	const (
		original = "the version worth keeping\n"
		replaced = "what was uploaded over it\n"
		moved    = "the draft that displaced it\n"
	)
	h.put(ada, "report.txt", original)
	h.put(ada, "draft.txt", moved)
	h.scan(ada)

	// An overwrite: the upload names an occupied path and asks to replace it.
	id := c.create("", "report.txt", len(replaced), true)
	if w := c.patch(id, 0, strings.NewReader(replaced)); w.Code != http.StatusCreated {
		t.Fatalf("overwriting report.txt: %d %s", w.Code, w.Body.String())
	}
	if got := h.as(secret, "GET", "/api/download/report.txt", nil).Body.String(); got != replaced {
		t.Fatalf("after the overwrite report.txt is %q", got)
	}
	if got := restored(t, h, secret, "report.txt"); got != original {
		t.Errorf("the overwritten version came back as %q, want %q", got, original)
	}

	// A move onto an occupied name, which displaces the same way.
	if w := h.as(secret, "POST", "/api/move", map[string]any{
		"from": "draft.txt", "to": "report.txt", "replace": true,
	}); w.Code != http.StatusOK {
		t.Fatalf("moving draft.txt onto report.txt: %d %s", w.Code, w.Body.String())
	}
	if got := h.as(secret, "GET", "/api/download/report.txt", nil).Body.String(); got != moved {
		t.Fatalf("after the move report.txt is %q", got)
	}
	if got := restored(t, h, secret, "report.txt"); got != replaced {
		t.Errorf("the displaced version came back as %q, want %q", got, replaced)
	}

	// A replace that never finishes. The swap happens on the last byte, so the
	// previous content is not displaced at all: it is still at its own path,
	// which is better than recoverable.
	before := len(h.trash(t, secret).Entries)
	const interrupted = "half of this will never arrive\n"
	id = c.create("", "report.txt", len(interrupted), true)
	if w := c.patch(id, 0, strings.NewReader(interrupted[:10])); w.Code != http.StatusNoContent {
		t.Fatalf("first chunk of the interrupted replace: %d %s", w.Code, w.Body.String())
	}
	if got := h.as(secret, "GET", "/api/download/report.txt", nil).Body.String(); got != moved {
		t.Errorf("an interrupted replace changed report.txt to %q", got)
	}
	if now := len(h.trash(t, secret).Entries); now != before {
		t.Errorf("an interrupted replace put %d items in the trash", now-before)
	}
	// The partial upload holds its own bytes and nothing of the file's.
	if w := h.as(secret, "GET", "/api/download/report.txt", nil); w.Body.String() == interrupted[:10] {
		t.Error("the destination is showing the interrupted upload's bytes")
	}

	// And the one operation that does destroy, so that the claim above is a
	// contrast and not just an absence: a delete moves the bytes to the trash
	// and a purge is what frees them.
	doomed := h.delete(t, secret, "report.txt")
	if got := readTrashed(t, h, ada, doomed.ID, ""); got != moved {
		t.Errorf("the deleted file is in the trash as %q, want %q", got, moved)
	}
	id64 := strconv.FormatInt(doomed.ID, 10)
	if w := h.as(secret, "DELETE", "/api/trash/"+id64, nil); w.Code != http.StatusNoContent {
		t.Fatalf("purging: %d %s", w.Code, w.Body.String())
	}
	if _, err := os.Stat(h.trashPath(ada, doomed.ID)); !os.IsNotExist(err) {
		t.Errorf("a purged item is still on disk: %v", err)
	}
	if w := h.as(secret, "POST", "/api/trash/"+id64+"/restore", nil); w.Code < 400 {
		t.Errorf("a purged item was restored: %d %s", w.Code, w.Body.String())
	}
}

// restored takes the most recently trashed version of path back out and returns
// its content. Restoring never overwrites, so it lands beside whatever occupies
// the original path and the response says where.
func restored(t *testing.T, h *harness, secret, path string) string {
	t.Helper()
	var target files.TrashEntry
	for _, e := range h.trash(t, secret).Entries {
		if e.Path == path && (target.ID == 0 || e.DeletedAt.After(target.DeletedAt)) {
			target = e
		}
	}
	if target.ID == 0 {
		t.Fatalf("nothing in the trash came from %q: %+v", path, h.trash(t, secret).Entries)
	}

	w := h.as(secret, "POST", "/api/trash/"+strconv.FormatInt(target.ID, 10)+"/restore", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("restoring %d: %d %s", target.ID, w.Code, w.Body.String())
	}
	// The restored name can carry a space — "report (2).txt" — so it goes
	// through URL escaping the way a client's would.
	back := decodeBody[files.Entry](t, w)
	download := (&url.URL{Path: "/api/download/" + back.Path}).RequestURI()
	return h.as(secret, "GET", download, nil).Body.String()
}
