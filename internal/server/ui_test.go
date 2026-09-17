package server

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fosslife/drive/web"
)

// requireUI skips when the binary was built without running the frontend build.
// `go test ./...` has to pass on a machine with no Node toolchain; what it must
// never do is pass quietly while claiming to have checked the interface.
func requireUI(t *testing.T) {
	t.Helper()
	if !web.Built() {
		t.Skip("no built interface: run `npm --prefix web ci && npm --prefix web run build`")
	}
}

// 13.1: the interface comes out of the executable. The test runs from a
// directory with no assets in it — which is every directory, since the only
// copy is the embedded one — and still gets a page.
func TestInterfaceIsServedFromTheBinaryWithNoAssetDirectory(t *testing.T) {
	requireUI(t)
	h := newHarness(t)

	// Somewhere with nothing in it but the test's own temp files, to make the
	// point that no file next to the process is being read.
	empty := t.TempDir()
	if entries, err := os.ReadDir(empty); err != nil || len(entries) != 0 {
		t.Fatalf("temp dir is not empty: %v %v", entries, err)
	}
	t.Chdir(empty)

	w := h.do(httptest.NewRequest("GET", "/", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("GET /: %d %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, "<div id=\"root\">") {
		t.Errorf("GET / did not serve the application shell:\n%s", body)
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type = %q", ct)
	}
	// The one thing that would make the shell dangerous: stored files are
	// served under their own strict policy, and the page must not widen it.
	if csp := w.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "default-src 'self'") {
		t.Errorf("Content-Security-Policy = %q", csp)
	}

	// And the assets the page names are there too, out of the same binary.
	src := strings.SplitN(strings.SplitN(body, `src="/assets/`, 2)[1], `"`, 2)[0]
	w = h.do(httptest.NewRequest("GET", "/assets/"+src, nil))
	if w.Code != http.StatusOK || w.Body.Len() == 0 {
		t.Fatalf("GET /assets/%s: %d, %d bytes", src, w.Code, w.Body.Len())
	}
	if cc := w.Header().Get("Cache-Control"); !strings.Contains(cc, "immutable") {
		t.Errorf("hashed asset Cache-Control = %q", cc)
	}
}

// A deep link is a page, not a 404: the interface routes /browse/... and /s/...
// itself, and the server has no way to know which paths those are.
func TestDeepLinksServeTheApplicationShell(t *testing.T) {
	requireUI(t)
	h := newHarness(t)

	for _, path := range []string{"/browse/photos/2024", "/trash", "/tokens", "/s/sometoken", "/setup?token=x"} {
		w := h.do(httptest.NewRequest("GET", path, nil))
		if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "<div id=\"root\">") {
			t.Errorf("GET %s: %d, not the application shell", path, w.Code)
		}
	}
}

// An API path that does not exist answers as the API, not as a web page. A
// client that gets 200 and HTML for a typo goes looking in the wrong place.
func TestUnknownAPIPathIsAJSONNotFound(t *testing.T) {
	h := newHarness(t)

	w := h.do(httptest.NewRequest("GET", "/api/nonesuch", nil))
	if w.Code != http.StatusNotFound {
		t.Errorf("GET /api/nonesuch: %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, body %s", ct, w.Body.String())
	}
}

// The build output really is what got embedded. Without this, a stale dist/
// compiled a month ago would serve a stale interface and nothing would say so.
func TestEmbeddedAssetsMatchTheBuildOutput(t *testing.T) {
	requireUI(t)

	onDisk, err := os.ReadFile(filepath.Join("..", "..", "web", "dist", "index.html"))
	if err != nil {
		t.Skipf("no dist/index.html on disk: %v", err)
	}

	h := newHarness(t)
	w := h.do(httptest.NewRequest("GET", "/", nil))
	if w.Body.String() != string(onDisk) {
		t.Error("the embedded index.html is not the one in web/dist: rebuild the binary after building the interface")
	}
}
