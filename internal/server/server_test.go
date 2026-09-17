package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fosslife/drive/internal/index"
	"github.com/fosslife/drive/internal/scan"
	"github.com/fosslife/drive/internal/storage"
)

func get(t *testing.T, s *Server) *httptest.ResponseRecorder {
	t.Helper()
	return fetch(t, s, "/healthz")
}

func fetch(t *testing.T, s *Server, path string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
	return w
}

// idle is a scanner that has never run, which is what a fresh instance sees.
func idle() *Server { return New(func() scan.Status { return scan.Status{} }) }

func TestHealthReportsReadiness(t *testing.T) {
	s := idle()

	if w := get(t, s); w.Code != http.StatusServiceUnavailable || !strings.Contains(w.Body.String(), "starting") {
		t.Errorf("before ready: %d %s, want 503 starting", w.Code, w.Body.String())
	}

	s.SetReady(true)
	w := get(t, s)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "ready") {
		t.Errorf("when ready: %d %s, want 200 ready", w.Code, w.Body.String())
	}
}

func TestHealthDisclosesNothingElse(t *testing.T) {
	s := idle()
	s.SetReady(true)

	body := get(t, s).Body.String()
	for _, leak := range []string{"/", "DRIVE_", "version", "user"} {
		if strings.Contains(body, leak) {
			t.Errorf("health body %q contains %q", body, leak)
		}
	}
}

// 4.8: a client cannot tell a short listing from an unfinished index without
// being told, so the server says which it is.
func TestScanStatusReportsIncompleteIndexing(t *testing.T) {
	status := scan.Status{}
	s := New(func() scan.Status { return status })
	s.SetReady(true)

	body := fetch(t, s, "/api/scan").Body.String()
	if !strings.Contains(body, `"indexing":true`) {
		t.Errorf("before any scan: %s, want indexing true", body)
	}

	status = scan.Status{Running: true, Root: "users/ada", Seen: 400, Scans: 1}
	body = fetch(t, s, "/api/scan").Body.String()
	if !strings.Contains(body, `"indexing":true`) || !strings.Contains(body, `"seen":400`) {
		t.Errorf("during a scan: %s, want indexing true and progress", body)
	}

	status = scan.Status{Scans: 1, Seen: 900}
	body = fetch(t, s, "/api/scan").Body.String()
	if !strings.Contains(body, `"indexing":false`) {
		t.Errorf("after a completed scan: %s, want indexing false", body)
	}
}

// 4.8: a rebuild of a large root takes minutes. The port stays open and every
// request is answered throughout, and each answer says the index is not yet
// complete so a client never mistakes a partial listing for an empty drive.
func TestRequestsAreServedThroughoutARebuild(t *testing.T) {
	base := t.TempDir()
	db, err := index.Open(filepath.Join(base, "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`INSERT INTO users (username, password_hash, storage_root, created_at)
	                      VALUES ('ada', 'x', 'users/ada', 0)`); err != nil {
		t.Fatal(err)
	}

	rootDir := filepath.Join(base, "users", "ada")
	root, err := storage.Open(rootDir, 0)
	if err != nil {
		t.Fatal(err)
	}
	root.Close()
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

	scanner := scan.NewScanner(db, base, time.Hour)
	s := New(scanner.Status)
	s.SetReady(true)
	httpSrv := httptest.NewServer(s.Handler())
	defer httpSrv.Close()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go scanner.Run(ctx)

	// One status read per iteration, so the scan cannot finish between the
	// loop condition and the assertions and make this flap.
	served := 0
	for {
		resp, err := http.Get(httpSrv.URL + "/api/scan")
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
		if w := get(t, s); w.Code != http.StatusOK {
			t.Fatalf("health check during the rebuild: %d", w.Code)
		}
	}
	if served < 5 {
		t.Fatalf("only %d requests were served during the rebuild; it finished too fast to prove anything", served)
	}

	body := fetch(t, s, "/api/scan").Body.String()
	if !strings.Contains(body, `"indexing":false`) {
		t.Errorf("after the rebuild: %s, want indexing false", body)
	}
}
