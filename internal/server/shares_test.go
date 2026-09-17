package server

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/fosslife/drive/internal/auth"
	"github.com/fosslife/drive/internal/share"
)

// created is the shape of a share-creation response: the one place the token
// ever appears.
type created struct {
	share.Link
	Token string `json:"token"`
	URL   string `json:"url"`
}

// visit makes a request with no credentials at all, which is what a share
// visitor is. The session cookie is carried between visits by the jar.
type visits struct {
	h      *harness
	t      *testing.T
	cookie *http.Cookie
}

func (v *visits) do(method, path string, body any) *httptest.ResponseRecorder {
	v.t.Helper()
	req := request(method, path, body)
	if v.cookie != nil {
		req.AddCookie(v.cookie)
	}
	w := v.h.do(req)
	// Whatever session the server hands back, carried to the next visit the way
	// a browser would: unlocking a protected link is held in the session.
	for _, c := range w.Result().Cookies() {
		v.cookie = c
	}
	return w
}

func (h *harness) visitor(t *testing.T) *visits { return &visits{h: h, t: t} }

// share creates a link the way the owner's browser does.
func (h *harness) share(t *testing.T, secret string, in map[string]any) created {
	t.Helper()
	w := h.as(secret, "POST", "/api/shares", in)
	if w.Code != http.StatusCreated {
		t.Fatalf("creating a share for %v: %d %s", in, w.Code, w.Body.String())
	}
	return decodeBody[created](t, w)
}

// 11.1: the link is an unguessable token that says nothing about its target,
// and it serves that target to somebody with no account.
func TestShareLinkServesAFileToAnAnonymousVisitor(t *testing.T) {
	h, _, secret := withFiles(t)
	link := h.share(t, secret, map[string]any{"path": "docs/budget.csv"})

	// 256 bits, and nothing of the target in it: not the name, not the path,
	// not the identifier.
	if len(link.Token) < 43 {
		t.Errorf("token %q is %d characters, want at least 43 for 256 bits", link.Token, len(link.Token))
	}
	for _, leak := range []string{"budget", "docs", "csv"} {
		if strings.Contains(strings.ToLower(link.Token), strings.ToLower(leak)) {
			t.Errorf("token %q contains %q", link.Token, leak)
		}
	}
	// Two links to the same file are different tokens.
	if other := h.share(t, secret, map[string]any{"path": "docs/budget.csv"}); other.Token == link.Token {
		t.Error("two share links for the same file got the same token")
	}
	if link.URL != "/s/"+link.Token {
		t.Errorf("url = %q, want /s/ plus the token", link.URL)
	}

	v := h.visitor(t)
	info := v.do("GET", "/api/shares/"+link.Token, nil)
	if info.Code != http.StatusOK {
		t.Fatalf("an anonymous visit: %d %s", info.Code, info.Body.String())
	}
	if got := decodeBody[struct {
		Name string `json:"name"`
		Kind string `json:"kind"`
	}](t, info); got.Name != "budget.csv" || got.Kind != "file" {
		t.Errorf("the link describes %+v, want budget.csv", got)
	}

	w := v.do("GET", "/api/shares/"+link.Token+"/download/", nil)
	if w.Code != http.StatusOK || w.Body.String() != "a,b,c\n1,2,3\n" {
		t.Fatalf("downloading through the link: %d %q", w.Code, w.Body.String())
	}
	// The same headers that stop stored bytes executing anywhere else.
	if w.Header().Get("X-Content-Type-Options") != "nosniff" ||
		!strings.Contains(w.Header().Get("Content-Disposition"), "attachment") {
		t.Errorf("share download headers: %v", w.Header())
	}

	// An invented token is a 404, and the list of what exists stays private.
	if w := v.do("GET", "/api/shares/"+strings.Repeat("A", 43), nil); w.Code != http.StatusNotFound {
		t.Errorf("a guessed token: %d, want 404", w.Code)
	}
}

// 11.1, 11.2: a shared folder is browsable and downloadable by a visitor, and
// what they see is relative to the share.
func TestShareLinkBrowsesAFolderWithoutRevealingItsParent(t *testing.T) {
	h, ada, secret := withFiles(t)
	h.put(ada, "docs/deep/nested.txt", "down here")
	h.scan(ada)
	link := h.share(t, secret, map[string]any{"path": "docs"})

	v := h.visitor(t)
	w := v.do("GET", "/api/shares/"+link.Token+"/list", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("browsing a shared folder: %d %s", w.Code, w.Body.String())
	}
	got := decodeBody[listing](t, w)
	if got.Path != "" {
		t.Errorf("the share root is reported as %q, want the empty path", got.Path)
	}
	for _, e := range got.Entries {
		if strings.HasPrefix(e.Path, "docs") {
			t.Errorf("entry path %q discloses the owner's parent folder", e.Path)
		}
	}
	if !slicesContains(got.Entries, "budget.csv") || !slicesContains(got.Entries, "deep") {
		t.Errorf("the shared folder lists %v", paths(got.Entries))
	}

	// One level down, still relative to the share.
	sub := decodeBody[listing](t, v.do("GET", "/api/shares/"+link.Token+"/list?path=deep", nil))
	if sub.Path != "deep" || !slicesContains(sub.Entries, "nested.txt") {
		t.Errorf("a subfolder of the share lists %q %v", sub.Path, paths(sub.Entries))
	}
	if w := v.do("GET", "/api/shares/"+link.Token+"/download/deep/nested.txt", nil); w.Body.String() != "down here" {
		t.Errorf("downloading from inside a shared folder: %d %q", w.Code, w.Body.String())
	}
}

// 11.2: a link is read-only and reaches nothing outside its own subtree.
func TestShareLinkIsReadOnlyAndConfined(t *testing.T) {
	h, ada, secret := withFiles(t)
	h.put(ada, "private/secret.txt", "not shared")
	h.scan(ada)
	link := h.share(t, secret, map[string]any{"path": "docs"})
	v := h.visitor(t)

	// Above, beside, and out of the root: one answer, and it names nothing.
	for _, escape := range []string{"../private", "..", "../../etc", "/etc/passwd", "../notes.txt"} {
		w := v.do("GET", "/api/shares/"+link.Token+"/list?path="+url.QueryEscape(escape), nil)
		if w.Code != http.StatusNotFound {
			t.Errorf("listing %q through a share: %d %s, want 404", escape, w.Code, w.Body.String())
		}
		if strings.Contains(w.Body.String(), "private") || strings.Contains(w.Body.String(), "docs") {
			t.Errorf("the refusal for %q discloses a path: %s", escape, w.Body.String())
		}
		if w := v.do("GET", "/api/shares/"+link.Token+"/download/"+escape, nil); w.Code == http.StatusOK {
			t.Errorf("downloading %q through a share succeeded", escape)
		}
	}

	// Nothing that changes anything is reachable, by token or by route.
	for _, req := range [][2]string{
		{"DELETE", "/api/shares/" + link.Token + "/download/budget.csv"},
		{"POST", "/api/shares/" + link.Token + "/list"},
		{"PATCH", "/api/shares/" + link.Token + "/download/budget.csv"},
	} {
		if w := v.do(req[0], req[1], map[string]string{}); w.Code == http.StatusOK || w.Code == http.StatusNoContent {
			t.Errorf("%s %s through a share: %d, want a refusal", req[0], req[1], w.Code)
		}
	}
	// A share token is not an API credential: it authenticates as nobody.
	if w := h.as(link.Token, "GET", "/api/list", nil); w.Code != http.StatusUnauthorized {
		t.Errorf("a share token used as a bearer token: %d, want 401", w.Code)
	}
	if w := h.as(link.Token, "GET", "/api/shares", nil); w.Code != http.StatusUnauthorized {
		t.Errorf("a share token listing shares: %d, want 401", w.Code)
	}
}

// 11.3: a password-protected link discloses nothing until the password is in,
// and wrong guesses are limited.
func TestPasswordProtectedShareWithholdsEverythingUntilUnlocked(t *testing.T) {
	h, _, secret := withFiles(t)
	link := h.share(t, secret, map[string]any{"path": "docs", "password": "open sesame"})
	v := h.visitor(t)

	// Every read answers the same way, and none of them names the target.
	for _, path := range []string{"", "/list", "/download/budget.csv"} {
		w := v.do("GET", "/api/shares/"+link.Token+path, nil)
		if w.Code != http.StatusUnauthorized {
			t.Errorf("GET %s before unlocking: %d %s, want 401", path, w.Code, w.Body.String())
		}
		if !strings.Contains(w.Body.String(), `"password_required":true`) {
			t.Errorf("GET %s does not tell the client a password is wanted: %s", path, w.Body.String())
		}
		if strings.Contains(w.Body.String(), "budget") || strings.Contains(w.Body.String(), "docs") {
			t.Errorf("GET %s discloses the target: %s", path, w.Body.String())
		}
	}

	if w := v.do("POST", "/api/shares/"+link.Token+"/unlock",
		map[string]string{"password": "guess"}); w.Code != http.StatusUnauthorized {
		t.Errorf("a wrong password: %d %s, want 401", w.Code, w.Body.String())
	}
	if w := v.do("GET", "/api/shares/"+link.Token+"/list", nil); w.Code != http.StatusUnauthorized {
		t.Error("a wrong password unlocked the link anyway")
	}

	if w := v.do("POST", "/api/shares/"+link.Token+"/unlock",
		map[string]string{"password": "open sesame"}); w.Code != http.StatusNoContent {
		t.Fatalf("the right password: %d %s", w.Code, w.Body.String())
	}
	if w := v.do("GET", "/api/shares/"+link.Token+"/list", nil); w.Code != http.StatusOK {
		t.Fatalf("after unlocking: %d %s", w.Code, w.Body.String())
	}
	if w := v.do("GET", "/api/shares/"+link.Token+"/download/budget.csv", nil); w.Body.String() != "a,b,c\n1,2,3\n" {
		t.Errorf("downloading after unlocking: %d %q", w.Code, w.Body.String())
	}

	// Another visitor's unlock is their own; this one still owes the password.
	if w := h.visitor(t).do("GET", "/api/shares/"+link.Token+"/list", nil); w.Code != http.StatusUnauthorized {
		t.Errorf("a second visitor inherited the first one's unlock: %d", w.Code)
	}

	// 11.3: repeated wrong guesses are refused rather than answered forever.
	other := h.visitor(t)
	refused := false
	for range auth.DefaultMaxFailures + 2 {
		w := other.do("POST", "/api/shares/"+link.Token+"/unlock", map[string]string{"password": "nope"})
		if w.Code == http.StatusTooManyRequests {
			refused = true
			break
		}
	}
	if !refused {
		t.Error("guessing a share password is never rate-limited")
	}
}

// 11.4: expiry needs nobody to act on it, and a link without one keeps working.
func TestShareExpiry(t *testing.T) {
	h, _, secret := withFiles(t)
	soon := h.share(t, secret, map[string]any{
		"path": "notes.txt", "expires": time.Now().Add(time.Hour).Format(time.RFC3339),
	})
	forever := h.share(t, secret, map[string]any{"path": "notes.txt"})
	v := h.visitor(t)

	if w := v.do("GET", "/api/shares/"+soon.Token, nil); w.Code != http.StatusOK {
		t.Fatalf("before expiry: %d %s", w.Code, w.Body.String())
	}

	// Expire it by moving the clock in the index rather than sleeping an hour.
	if _, err := h.db.Exec(`UPDATE shares SET expires_at = ? WHERE id = ?`,
		time.Now().Add(-time.Minute).Unix(), soon.ID); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"", "/download/"} {
		w := v.do("GET", "/api/shares/"+soon.Token+path, nil)
		if w.Code != http.StatusGone {
			t.Errorf("after expiry GET %s: %d %s, want 410", path, w.Code, w.Body.String())
		}
		if strings.Contains(w.Body.String(), "notes") {
			t.Errorf("the expiry refusal discloses the target: %s", w.Body.String())
		}
	}
	if w := v.do("GET", "/api/shares/"+forever.Token+"/download/", nil); w.Code != http.StatusOK {
		t.Errorf("a link with no expiry: %d, want it still working", w.Code)
	}

	// An expiry in the past is refused at creation rather than created dead.
	if w := h.as(secret, "POST", "/api/shares", map[string]any{
		"path": "notes.txt", "expires": time.Now().Add(-time.Hour).Format(time.RFC3339),
	}); w.Code != http.StatusBadRequest {
		t.Errorf("creating an already-expired link: %d, want 400", w.Code)
	}
}

// 11.5: the owner can see what each link points at and revoke it, and
// revocation is effective on the next request.
func TestListingAndRevokingShareLinks(t *testing.T) {
	h, _, secret := withFiles(t)
	open := h.share(t, secret, map[string]any{"path": "notes.txt"})
	locked := h.share(t, secret, map[string]any{
		"path": "docs", "password": "hunter2", "expires": time.Now().Add(time.Hour).Format(time.RFC3339),
	})

	links := decodeBody[[]share.Link](t, h.as(secret, "GET", "/api/shares", nil))
	if len(links) != 2 {
		t.Fatalf("listed %+v, want both links", links)
	}
	byID := map[int64]share.Link{}
	for _, l := range links {
		byID[l.ID] = l
	}
	if l := byID[open.ID]; l.Path != "notes.txt" || l.Kind != "file" || l.Protected || !l.ExpiresAt.IsZero() {
		t.Errorf("the open link lists as %+v", l)
	}
	if l := byID[locked.ID]; l.Path != "docs" || l.Kind != "folder" || !l.Protected || l.ExpiresAt.IsZero() {
		t.Errorf("the protected link lists as %+v", l)
	}
	// The listing is not a way to get the URL back: only the hash was stored.
	if body := h.as(secret, "GET", "/api/shares", nil).Body.String(); strings.Contains(body, open.Token) {
		t.Error("the share listing carries the token")
	}

	v := h.visitor(t)
	if w := v.do("GET", "/api/shares/"+open.Token, nil); w.Code != http.StatusOK {
		t.Fatalf("before revocation: %d", w.Code)
	}
	if w := h.as(secret, "DELETE", "/api/shares/"+itoa(open.ID), nil); w.Code != http.StatusNoContent {
		t.Fatalf("revoking: %d %s", w.Code, w.Body.String())
	}
	// Immediately: the very next request, with no restart and no expiry.
	if w := v.do("GET", "/api/shares/"+open.Token, nil); w.Code != http.StatusNotFound {
		t.Errorf("after revocation: %d %s, want 404", w.Code, w.Body.String())
	}
	if w := v.do("GET", "/api/shares/"+locked.Token, nil); w.Code != http.StatusUnauthorized {
		t.Errorf("revoking one link affected the other: %d", w.Code)
	}
}

// 11.6: a link follows its target through a rename and a move, stops serving
// when the target is trashed, and tells the owner that is why.
func TestShareFollowsItsTargetAndStopsWhenTrashed(t *testing.T) {
	h, _, secret := withFiles(t)
	link := h.share(t, secret, map[string]any{"path": "notes.txt"})
	v := h.visitor(t)

	for _, move := range [][2]string{{"notes.txt", "renamed.txt"}, {"renamed.txt", "docs/renamed.txt"}} {
		if w := h.as(secret, "POST", "/api/move", map[string]any{
			"from": move[0], "to": move[1],
		}); w.Code != http.StatusOK {
			t.Fatalf("moving %v: %d %s", move, w.Code, w.Body.String())
		}
		w := v.do("GET", "/api/shares/"+link.Token+"/download/", nil)
		if w.Code != http.StatusOK || w.Body.String() != "ada's notes" {
			t.Fatalf("after moving to %s: %d %q", move[1], w.Code, w.Body.String())
		}
	}
	// The owner's listing follows it too.
	if links := decodeBody[[]share.Link](t, h.as(secret, "GET", "/api/shares", nil)); links[0].Path != "docs/renamed.txt" {
		t.Errorf("the link points at %q after the move", links[0].Path)
	}

	h.delete(t, secret, "docs/renamed.txt")
	if w := v.do("GET", "/api/shares/"+link.Token+"/download/", nil); w.Code != http.StatusGone {
		t.Errorf("a link to a trashed file: %d %s, want 410", w.Code, w.Body.String())
	}
	links := decodeBody[[]share.Link](t, h.as(secret, "GET", "/api/shares", nil))
	if len(links) != 1 || links[0].State != "trashed" {
		t.Fatalf("the owner sees %+v, want the link reported as pointing at a trashed item", links)
	}

	// Restoring the target brings the same link back: trash is not destruction.
	trashed := decodeBody[trashListing](t, h.as(secret, "GET", "/api/trash", nil)).Entries
	if w := h.as(secret, "POST", "/api/trash/"+itoa(trashed[0].ID)+"/restore", nil); w.Code != http.StatusOK {
		t.Fatalf("restoring: %d %s", w.Code, w.Body.String())
	}
	if w := v.do("GET", "/api/shares/"+link.Token+"/download/", nil); w.Code != http.StatusOK {
		t.Errorf("after restoring the target: %d, want the link working again", w.Code)
	}

	// Permanent deletion takes the link with it, by cascade.
	h.delete(t, secret, "docs/renamed.txt")
	trashed = decodeBody[trashListing](t, h.as(secret, "GET", "/api/trash", nil)).Entries
	if w := h.as(secret, "DELETE", "/api/trash/"+itoa(trashed[0].ID), nil); w.Code != http.StatusNoContent {
		t.Fatalf("purging: %d %s", w.Code, w.Body.String())
	}
	if w := v.do("GET", "/api/shares/"+link.Token, nil); w.Code != http.StatusNotFound {
		t.Errorf("a link to a purged file: %d, want 404", w.Code)
	}
	if links := decodeBody[[]share.Link](t, h.as(secret, "GET", "/api/shares", nil)); len(links) != 0 {
		t.Errorf("the owner still has %+v after the target was purged", links)
	}
}

// 11.7: a share can only be made for something in the creator's own root.
func TestCannotShareWhatYouDoNotOwn(t *testing.T) {
	h, _, secret := withFiles(t)
	bob := h.account("bob", false)
	h.put(bob, "secret.txt", "bob's secret")
	h.scan(bob)
	bobsToken := h.token(bob)

	// Ada owns notes.txt; bob does not, by any spelling of the path.
	for _, path := range []string{
		"notes.txt", "docs/budget.csv", "../ada/notes.txt", "/home/spark/notes.txt", "..",
	} {
		w := h.as(bobsToken, "POST", "/api/shares", map[string]any{"path": path})
		if w.Code != http.StatusNotFound {
			t.Errorf("bob sharing %q: %d %s, want 404", path, w.Code, w.Body.String())
		}
	}
	if links := decodeBody[[]share.Link](t, h.as(bobsToken, "GET", "/api/shares", nil)); len(links) != 0 {
		t.Errorf("bob created %+v", links)
	}

	// And one user cannot revoke another's link.
	link := h.share(t, secret, map[string]any{"path": "notes.txt"})
	if w := h.as(bobsToken, "DELETE", "/api/shares/"+itoa(link.ID), nil); w.Code != http.StatusNotFound {
		t.Errorf("bob revoking ada's link: %d, want 404", w.Code)
	}
	if w := h.visitor(t).do("GET", "/api/shares/"+link.Token, nil); w.Code != http.StatusOK {
		t.Error("ada's link stopped working after bob tried to revoke it")
	}
}

// 11.2: the file a share serves goes out under the same rules as any other
// stored file — an HTML file is a download, never a page in this origin.
func TestSharedContentCannotExecuteInTheOrigin(t *testing.T) {
	h, ada, secret := withFiles(t)
	h.put(ada, "page.html", "<script>alert(document.cookie)</script>")
	h.scan(ada)
	link := h.share(t, secret, map[string]any{"path": "page.html"})

	w := h.visitor(t).do("GET", "/api/shares/"+link.Token+"/download/", nil)
	if ct := w.Header().Get("Content-Type"); ct != "application/octet-stream" {
		t.Errorf("Content-Type = %q, want an opaque download", ct)
	}
	if !strings.Contains(w.Header().Get("Content-Disposition"), "attachment") {
		t.Errorf("Content-Disposition = %q", w.Header().Get("Content-Disposition"))
	}
	if csp := w.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "default-src 'none'") {
		t.Errorf("Content-Security-Policy = %q", csp)
	}
}
