package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func get(t *testing.T, s *Server) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	return w
}

func TestHealthReportsReadiness(t *testing.T) {
	s := New()

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
	s := New()
	s.SetReady(true)

	body := get(t, s).Body.String()
	for _, leak := range []string{"/", "DRIVE_", "version", "user"} {
		if strings.Contains(body, leak) {
			t.Errorf("health body %q contains %q", body, leak)
		}
	}
}
