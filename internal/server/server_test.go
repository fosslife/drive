package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/fosslife/drive/internal/auth"
	"github.com/fosslife/drive/internal/index"
	"github.com/fosslife/drive/internal/scan"
)

const testPassword = "correct horse battery staple"

// harness is a server over a real index in a temp data directory: the auth
// paths are mostly database behaviour, so there is nothing useful to fake.
type harness struct {
	*Server
	t       *testing.T
	db      *index.DB
	users   *auth.Store
	dataDir string
	status  scan.Status // what the scanner reports; assign to change it
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	dir := t.TempDir()
	db, err := index.Open(filepath.Join(dir, "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	h := &harness{t: t, db: db, dataDir: dir, users: auth.NewStore(db, dir, 0)}
	// secure=true is the HTTPS case the spec describes; main follows the listener.
	h.Server = New(db, h.users, auth.NewSessions(db, true), func() scan.Status { return h.status })
	h.SetReady(true)
	return h
}

func (h *harness) account(name string, admin bool) *auth.User {
	h.t.Helper()
	u, err := h.users.Create(name, testPassword, admin)
	if err != nil {
		h.t.Fatal(err)
	}
	return u
}

func (h *harness) token(u *auth.User) string {
	h.t.Helper()
	secret, _, err := h.users.CreateToken(u.ID, "test")
	if err != nil {
		h.t.Fatal(err)
	}
	return secret
}

// put writes a file into a user's storage root the way anything outside the
// application would: straight onto disk. Call scan afterwards to index it.
func (h *harness) put(u *auth.User, rel, content string) {
	h.t.Helper()
	root, err := h.users.Root(u)
	if err != nil {
		h.t.Fatal(err)
	}
	defer root.Close()
	if _, err := root.Write(rel, strings.NewReader(content), int64(len(content))); err != nil {
		h.t.Fatalf("writing %s: %v", rel, err)
	}
}

// trashPath is where a trashed entry's content sits on disk: the entry itself,
// moved under its own identifier. Tests read it directly to assert that trash
// is a move and not a delete.
func (h *harness) trashPath(u *auth.User, id int64) string {
	return filepath.Join(h.dataDir, "users", u.Username, ".drive", "trash", fmt.Sprint(id))
}

// scan runs the reconciler over one user's root, which is how files that
// arrived on disk become browsable.
func (h *harness) scan(u *auth.User) {
	h.t.Helper()
	root, err := h.users.Root(u)
	if err != nil {
		h.t.Fatal(err)
	}
	defer root.Close()
	if _, err := scan.Scan(h.db, u.ID, root, nil); err != nil {
		h.t.Fatal(err)
	}
}

// as makes an API-token-authenticated request, which is how a programmatic
// client reaches the same endpoints the browser uses.
func (h *harness) as(secret, method, path string, body any) *httptest.ResponseRecorder {
	h.t.Helper()
	req := request(method, path, body)
	req.Header.Set("Authorization", "Bearer "+secret)
	return h.do(req)
}

func (h *harness) do(req *http.Request) *httptest.ResponseRecorder {
	h.t.Helper()
	w := httptest.NewRecorder()
	h.Handler().ServeHTTP(w, req)
	return w
}

// request is what a first-party browser request looks like: the browser sets
// Sec-Fetch-Site itself and script cannot forge it.
func request(method, path string, body any) *http.Request {
	req := bare(method, path, body)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	return req
}

// bare omits every origin signal, for the tests that supply their own.
func bare(method, path string, body any) *http.Request {
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			panic(err)
		}
		r = bytes.NewReader(b)
	}
	return httptest.NewRequest(method, path, r)
}

func decodeBody[T any](t *testing.T, w *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil {
		t.Fatalf("decoding %q: %v", w.Body.String(), err)
	}
	return v
}

func TestHealthReportsReadiness(t *testing.T) {
	h := newHarness(t)
	h.SetReady(false)

	if w := h.do(request("GET", "/healthz", nil)); w.Code != http.StatusServiceUnavailable || !strings.Contains(w.Body.String(), "starting") {
		t.Errorf("before ready: %d %s, want 503 starting", w.Code, w.Body.String())
	}

	h.SetReady(true)
	w := h.do(request("GET", "/healthz", nil))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "ready") {
		t.Errorf("when ready: %d %s, want 200 ready", w.Code, w.Body.String())
	}
}

func TestHealthDisclosesNothingElse(t *testing.T) {
	h := newHarness(t)

	body := h.do(request("GET", "/healthz", nil)).Body.String()
	for _, leak := range []string{"/", "DRIVE_", "version", "user"} {
		if strings.Contains(body, leak) {
			t.Errorf("health body %q contains %q", body, leak)
		}
	}
}

// 4.8: a client cannot tell a short listing from an unfinished index without
// being told, so the server says which it is.
func TestScanStatusReportsIncompleteIndexing(t *testing.T) {
	h := newHarness(t)
	secret := h.token(h.account("ada", false))
	get := func() string {
		req := request("GET", "/api/scan", nil)
		req.Header.Set("Authorization", "Bearer "+secret)
		return h.do(req).Body.String()
	}

	if body := get(); !strings.Contains(body, `"indexing":true`) {
		t.Errorf("before any scan: %s, want indexing true", body)
	}

	h.status = scan.Status{Running: true, Root: "users/ada", Seen: 400, Scans: 1}
	if body := get(); !strings.Contains(body, `"indexing":true`) || !strings.Contains(body, `"seen":400`) {
		t.Errorf("during a scan: %s, want indexing true and progress", body)
	}

	h.status = scan.Status{Scans: 1, Seen: 900}
	if body := get(); !strings.Contains(body, `"indexing":false`) {
		t.Errorf("after a completed scan: %s, want indexing false", body)
	}
}

// 4.8: a rebuild of a large root takes minutes. The port stays open and every
// request is answered throughout, and each answer says the index is not yet
// complete so a client never mistakes a partial listing for an empty drive.
func TestRequestsAreServedThroughoutARebuild(t *testing.T) {
	h := newHarness(t)
	ada := h.account("ada", false)
	secret := h.token(ada)

	rootDir := filepath.Join(h.dataDir, "users", "ada")
	for i := range 3000 {
		dir := filepath.Join(rootDir, fmt.Sprintf("bulk/%02d", i%20))
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		name := filepath.Join(dir, fmt.Sprintf("file-%04d.txt", i))
		if err := os.WriteFile(name, []byte(strings.Repeat("x", i%64)), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	scanner := scan.NewScanner(h.db, h.dataDir, time.Hour)
	h.Server.scanStatus = scanner.Status
	httpSrv := httptest.NewServer(h.Handler())
	defer httpSrv.Close()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go scanner.Run(ctx)

	// One status read per iteration, so the scan cannot finish between the
	// loop condition and the assertions and make this flap.
	served := 0
	for {
		req, err := http.NewRequest("GET", httpSrv.URL+"/api/scan", nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+secret)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("request %d during the rebuild failed: %v", served+1, err)
		}
		var got struct {
			Seen     int  `json:"seen"`
			Scans    int  `json:"scans"`
			Indexing bool `json:"indexing"`
		}
		err = json.NewDecoder(resp.Body).Decode(&got)
		resp.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("request %d during the rebuild: %d", served+1, resp.StatusCode)
		}
		if got.Scans > 0 {
			break
		}
		served++
		if !got.Indexing {
			t.Fatal("a response during the rebuild claimed indexing was complete")
		}
		if w := h.do(request("GET", "/healthz", nil)); w.Code != http.StatusOK {
			t.Fatalf("health check during the rebuild: %d", w.Code)
		}
	}
	if served < 5 {
		t.Fatalf("only %d requests were served during the rebuild; it finished too fast to prove anything", served)
	}

	req := request("GET", "/api/scan", nil)
	req.Header.Set("Authorization", "Bearer "+secret)
	if body := h.do(req).Body.String(); !strings.Contains(body, `"indexing":false`) {
		t.Errorf("after the rebuild: %s, want indexing false", body)
	}
}

// 5.9: the public list is the exception, and it is short. Every other route
// answers an unauthenticated request with 401 rather than doing the work.
func TestEveryRouteIsAuthenticatedOrExplicitlyPublic(t *testing.T) {
	public := map[string]bool{
		"GET /healthz":    true, // readiness, and it discloses nothing
		"POST /api/login": true, // the thing that produces credentials
		"GET /api/setup":  true, // first run: the printed token is the credential
		"POST /api/setup": true,
		// Share access: the token in the URL is the whole credential, and every
		// one of these is read-only and confined to one shared item.
		"GET /api/shares/{token}":                    true,
		"POST /api/shares/{token}/unlock":            true,
		"GET /api/shares/{token}/list":               true,
		"GET /api/shares/{token}/download/{path...}": true,
	}
	// A new entry here is the moment to ask why.

	h := newHarness(t)
	placeholder := regexp.MustCompile(`\{[^}]*\}`)
	for _, rt := range h.routes() {
		method, pattern, _ := strings.Cut(rt.pattern, " ")
		path := placeholder.ReplaceAllString(pattern, "1")

		if rt.public != public[rt.pattern] {
			t.Errorf("%s is marked public=%v but the reviewed list says %v", rt.pattern, rt.public, public[rt.pattern])
		}
		// A public route may still reject the request on its own terms — login
		// with no credentials is a 401 — so the marker is the middleware's own
		// message, not the status.
		w := h.do(request(method, path, map[string]string{}))
		demandedAuth := w.Code == http.StatusUnauthorized && strings.Contains(w.Body.String(), errAuthRequired)
		if rt.public == demandedAuth {
			t.Errorf("%s: public=%v but answered %d %s", rt.pattern, rt.public, w.Code, strings.TrimSpace(w.Body.String()))
		}
	}
}

// 5.8: a token authenticates as its owner and gets exactly that user's access.
// Every path is resolved through the owner's root, so there is no request shape
// that reaches another account's files.
func TestTokenCannotReachOutsideItsOwnerRoot(t *testing.T) {
	h := newHarness(t)
	ada := h.account("ada", false)
	bob := h.account("bob", false)
	secret := h.token(ada)

	bobRoot, err := h.users.Root(bob)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bobRoot.Write("secret.txt", strings.NewReader("bob's secret"), 12); err != nil {
		t.Fatal(err)
	}
	bobRoot.Close()

	req := request("GET", "/api/me", nil)
	req.Header.Set("Authorization", "Bearer "+secret)

	var checked bool
	h.requireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		checked = true
		u := userFrom(r.Context())
		if u.ID != ada.ID {
			t.Fatalf("the token authenticated as %q, want ada", u.Username)
		}
		root, err := h.users.Root(u)
		if err != nil {
			t.Fatal(err)
		}
		defer root.Close()

		for _, p := range []string{"../bob/secret.txt", "../../users/bob/secret.txt", "/users/bob/secret.txt", ".drive/tmp"} {
			if _, err := root.Checksum(p); err == nil {
				t.Errorf("a token for ada read %q", p)
			}
		}
	})).ServeHTTP(httptest.NewRecorder(), req)

	if !checked {
		t.Fatal("the token did not authenticate at all")
	}
}
