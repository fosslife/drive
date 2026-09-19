package server

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/fosslife/drive/internal/files"
)

// 15.6: one user, one storage root, and no request shape that crosses from one
// to another. Every path below is resolved through the caller's own root, so
// the matrix is not a list of special cases to remember — it is a list of the
// ways someone might try, all answered by the same mechanism.
//
// The control is the last column: ada's own token on the same URLs. Without it
// a 404 could mean "your path is wrong" instead of "not yours".
func TestNoRequestReachesAnotherUsersFiles(t *testing.T) {
	h := newHarness(t)
	ada, bob := h.account("ada", false), h.account("bob", false)
	adaToken, bobToken := h.token(ada), h.token(bob)

	h.put(ada, "private/secret.txt", "ada's secret")
	h.putBytes(ada, "private/photo.jpg", encodeJPEG(t, picture(64, 32)))
	h.put(ada, "private/bin.txt", "to be deleted")
	h.scan(ada)
	// bob has a root of his own, so nothing here is about him having no files.
	h.put(bob, "mine.txt", "bob's own")
	h.scan(bob)

	link := h.share(t, adaToken, map[string]any{"path": "private/secret.txt"})
	adaTrashed := h.delete(t, adaToken, "private/bin.txt")
	adaTokens := decodeBody[[]struct {
		ID int64 `json:"id"`
	}](t, h.as(adaToken, "GET", "/api/tokens", nil))
	if len(adaTokens) == 0 {
		t.Fatal("ada has no token to try to revoke")
	}
	upload := newUploader(t, h, ada)
	adaUpload := upload.create("private", "in-flight.txt", 16, false)

	// try is h.as plus whatever headers the endpoint requires of everyone: a
	// tus request without its version header is refused before the question of
	// whose upload it is ever comes up, which would prove nothing.
	try := func(secret, method, path string, in any, headers map[string]string) *httptest.ResponseRecorder {
		t.Helper()
		req := request(method, path, in)
		req.Header.Set("Authorization", "Bearer "+secret)
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		return h.do(req)
	}
	tus := map[string]string{"Tus-Resumable": "1.0.0"}

	for _, attempt := range []struct {
		what    string
		method  string
		path    string
		body    any
		headers map[string]string
	}{
		{what: "listing a folder", method: "GET", path: "/api/list?path=private"},
		{what: "downloading", method: "GET", path: "/api/download/private/secret.txt"},
		{what: "downloading an archive", method: "GET", path: "/api/archive?path=private"},
		{what: "asking for a thumbnail", method: "GET", path: "/api/thumb/private/photo.jpg"},
		{what: "creating a share", method: "POST", path: "/api/shares", body: map[string]any{"path": "private/secret.txt"}},
		{what: "revoking a share", method: "DELETE", path: fmt.Sprintf("/api/shares/%d", link.ID)},
		{what: "moving a file", method: "POST", path: "/api/move", body: map[string]any{"from": "private/secret.txt", "to": "taken.txt"}},
		{what: "deleting a file", method: "DELETE", path: "/api/files/private/secret.txt"},
		{what: "restoring from the trash", method: "POST", path: fmt.Sprintf("/api/trash/%d/restore", adaTrashed.ID)},
		{what: "purging from the trash", method: "DELETE", path: fmt.Sprintf("/api/trash/%d", adaTrashed.ID)},
		{what: "revoking a token", method: "DELETE", path: fmt.Sprintf("/api/tokens/%d", adaTokens[0].ID)},
		{what: "resuming an upload", method: "HEAD", path: "/api/uploads/" + adaUpload, headers: tus},
	} {
		w := try(bobToken, attempt.method, attempt.path, attempt.body, attempt.headers)
		if w.Code < 400 {
			t.Errorf("%s: bob got %d %s", attempt.what, w.Code, w.Body.String())
		}
		// A refusal may echo the path bob asked for — that is his own input —
		// but never a byte of what is behind it.
		if strings.Contains(w.Body.String(), "ada's secret") {
			t.Errorf("%s: the refusal carried ada's content: %s", attempt.what, w.Body.String())
		}

		// The control: the same request from the owner works, so the refusals
		// above are about ownership and not about a malformed path. Only the
		// reads, since the writes would spoil the rest of the matrix.
		switch attempt.method {
		case "GET", "HEAD":
			if w := try(adaToken, attempt.method, attempt.path, attempt.body, attempt.headers); w.Code >= 400 {
				t.Errorf("%s: ada was refused her own: %d %s", attempt.what, w.Code, w.Body.String())
			}
		}
	}

	// Search is the one that cannot answer 404: a search that matches nothing is
	// an empty result, and an empty result is exactly what bob has to get.
	if found := h.search(t, bobToken, "secret", 0); len(found.Entries) != 0 {
		t.Errorf("bob's search found %+v", found.Entries)
	}

	// And nothing of ada's moved while bob was trying.
	if got := h.as(adaToken, "GET", "/api/download/private/secret.txt", nil).Body.String(); got != "ada's secret" {
		t.Errorf("ada's file reads %q after bob's attempts", got)
	}
	if _, err := files.Lookup(h.db, ada.ID, "private/secret.txt"); err != nil {
		t.Errorf("ada's file is no longer indexed: %v", err)
	}
	if w := h.as(adaToken, "GET", "/api/me", nil); w.Code != http.StatusOK {
		t.Errorf("ada's token stopped working: %d %s", w.Code, w.Body.String())
	}
	if trash := h.trash(t, adaToken); len(trash.Entries) != 1 {
		t.Errorf("ada's trash holds %d items, want the one she deleted", len(trash.Entries))
	}
	if got := h.list(t, bobToken, ""); len(got.Entries) != 1 || got.Entries[0].Name != "mine.txt" {
		t.Errorf("bob's own listing is %+v, want just mine.txt", got.Entries)
	}
}

// 15.6: a share link is a capability for one subtree, so it does not become a
// second way into the rest of the owner's root either.
func TestAShareLinkReachesNothingBesideIt(t *testing.T) {
	h := newHarness(t)
	ada := h.account("ada", false)
	adaToken := h.token(ada)
	h.put(ada, "public/notice.txt", "read me")
	h.put(ada, "private/secret.txt", "not for you")
	h.scan(ada)

	link := h.share(t, adaToken, map[string]any{"path": "public"})
	v := h.visitor(t)
	for _, path := range []string{
		"/download/../private/secret.txt",
		"/download/%2e%2e/private/secret.txt",
		"/download//private/secret.txt",
		"/list?path=../private",
	} {
		// The mux normalises a path with dot segments into a redirect before any
		// handler sees it, so follow where it points: what matters is where the
		// visitor ends up, not the first status on the way.
		w := v.do("GET", "/api/shares/"+link.Token+path, nil)
		for range 3 {
			if w.Code < 300 || w.Code >= 400 {
				break
			}
			w = v.do("GET", w.Header().Get("Location"), nil)
		}
		if w.Code < 400 {
			t.Errorf("%s through a share: %d %s", path, w.Code, w.Body.String())
		}
		if strings.Contains(w.Body.String(), "not for you") {
			t.Errorf("%s through a share returned the private file", path)
		}
	}
	if w := v.do("GET", "/api/shares/"+link.Token+"/download/notice.txt", nil); w.Body.String() != "read me" {
		t.Errorf("the shared file itself: %d %s", w.Code, w.Body.String())
	}
}
