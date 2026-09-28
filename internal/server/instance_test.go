package server

import (
	"net/http"
	"testing"
	"time"

	"github.com/fosslife/drive/internal/scan"
)

// 5.2 and 5.4: what the operator can see about the instance, including the
// version it was built as.
func TestInstanceStateReportsWhatTheOperatorNeeds(t *testing.T) {
	h := newHarness(t)
	root := h.account("root", true)
	ada := h.account("ada", false)
	h.status = scan.Status{
		Scans: 3, Seen: 120, TookMS: 42,
		Started:  time.Now().Add(-time.Minute),
		Finished: time.Now().Add(-time.Second),
		Error:    "users/ada: permission denied",
	}

	w := h.as(h.token(root), "GET", "/api/admin/instance", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("instance state: %d %s", w.Code, w.Body.String())
	}
	report := decodeBody[instanceReport](t, w)
	if report.Version != "test" {
		t.Errorf("version = %q, want the one the server was constructed with", report.Version)
	}
	if report.DataDir != h.dataDir {
		t.Errorf("data_dir = %q, want %q", report.DataDir, h.dataDir)
	}
	if report.UptimeSec < 0 {
		t.Errorf("uptime = %d", report.UptimeSec)
	}
	if report.TotalBytes <= 0 || report.FreeBytes <= 0 || report.FreeBytes > report.TotalBytes {
		t.Errorf("free %d of total %d bytes; both must be real figures for the data volume",
			report.FreeBytes, report.TotalBytes)
	}
	// A failed scan is reported rather than hidden.
	if report.Scan.Error != "users/ada: permission denied" || report.Scan.Scans != 3 || report.Scan.TookMS != 42 {
		t.Errorf("scan state = %+v, want the scanner's own answer", report.Scan)
	}

	if w := h.as(h.token(ada), "GET", "/api/admin/instance", nil); w.Code != http.StatusForbidden {
		t.Errorf("a non-administrator reading instance state: %d %s, want 403", w.Code, w.Body.String())
	}
	if w := h.do(request("GET", "/api/admin/instance", nil)); w.Code != http.StatusUnauthorized {
		t.Errorf("an anonymous request for instance state: %d, want 401", w.Code)
	}
}

// 5.3: pressing rescan is safe to press twice. The scanner queues at most one
// pass, so a second request while one is running asks for nothing new and
// disturbs nothing.
func TestRescanIsAcceptedAndIdempotent(t *testing.T) {
	h := newHarness(t)
	admin := h.token(h.account("root", true))
	h.status = scan.Status{Running: true, Root: "users/ada", Seen: 7}

	for range 3 {
		w := h.as(admin, "POST", "/api/admin/scan", nil)
		if w.Code != http.StatusAccepted {
			t.Fatalf("asking for a rescan: %d %s, want 202", w.Code, w.Body.String())
		}
		if got := decodeBody[scan.Status](t, w); !got.Running || got.Seen != 7 {
			t.Errorf("the running scan was reported as %+v; asking must not disturb it", got)
		}
	}
	if asked := h.scansAsked.Load(); asked != 3 {
		t.Errorf("the scanner was asked %d times for 3 requests", asked)
	}

	ada := h.token(h.account("ada", false))
	if w := h.as(ada, "POST", "/api/admin/scan", nil); w.Code != http.StatusForbidden {
		t.Errorf("a non-administrator asking for a rescan: %d, want 403", w.Code)
	}
	if asked := h.scansAsked.Load(); asked != 3 {
		t.Errorf("a refused request still reached the scanner (%d asks)", asked)
	}
}
