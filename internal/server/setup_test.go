package server

import (
	"net/http"
	"testing"

	"github.com/fosslife/drive/internal/auth"
)

// open opens the first-run flow on a fresh harness and returns the token that
// would have been printed at startup.
func openSetup(t *testing.T, h *harness) string {
	t.Helper()
	token, err := h.users.OpenSetup()
	if err != nil {
		t.Fatal(err)
	}
	if token == "" {
		t.Fatal("setup did not open on an index with no account")
	}
	return token
}

// 6.1: a fresh instance has no usable account at all. Not a default one, not a
// blank one, not one the setup token itself can act as.
func TestNoCredentialsGrantAccessBeforeSetup(t *testing.T) {
	h := newHarness(t)
	token := openSetup(t, h)

	for _, c := range [][2]string{
		{"admin", "admin"}, {"admin", ""}, {"", ""}, {"root", "root"},
		{"drive", "drive"}, {"admin", token},
	} {
		// 429 once the limiter has seen enough of these; either way, no session.
		w := login(t, h, c[0], c[1])
		if w.Code != http.StatusUnauthorized && w.Code != http.StatusTooManyRequests {
			t.Errorf("login %q/%q: %d, want 401 or 429", c[0], c[1], w.Code)
		}
		if len(w.Result().Cookies()) != 0 {
			t.Errorf("login %q/%q set a cookie", c[0], c[1])
		}
	}

	// The setup token is not an API token either.
	req := request("GET", "/api/me", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	if w := h.do(req); w.Code != http.StatusUnauthorized {
		t.Errorf("the setup token as a bearer token: %d, want 401", w.Code)
	}

	if users, err := h.users.List(); err != nil || len(users) != 0 {
		t.Errorf("accounts before setup: %v, %v, want none", users, err)
	}
}

// 6.2: setup creates the first administrator, then shuts behind itself.
func TestSetupCreatesTheFirstAdministratorAndCloses(t *testing.T) {
	h := newHarness(t)
	token := openSetup(t, h)

	if w := h.do(request("GET", "/api/setup?token="+token, nil)); w.Code != http.StatusOK {
		t.Fatalf("opening the setup page: %d %s", w.Code, w.Body.String())
	}
	// Opening the page does not consume the token.
	if w := h.do(request("GET", "/api/setup?token="+token, nil)); w.Code != http.StatusOK {
		t.Fatalf("opening the setup page twice: %d %s", w.Code, w.Body.String())
	}

	w := h.do(request("POST", "/api/setup", map[string]string{
		"token": token, "username": "ada", "password": testPassword,
	}))
	if w.Code != http.StatusCreated {
		t.Fatalf("completing setup: %d %s", w.Code, w.Body.String())
	}
	created := decodeBody[auth.User](t, w)
	if created.Username != "ada" || !created.IsAdmin {
		t.Errorf("created %+v, want an administrator named ada", created)
	}

	// The account works.
	if got := login(t, h, "ada", testPassword); got.Code != http.StatusOK {
		t.Errorf("logging in as the new administrator: %d %s", got.Code, got.Body.String())
	}

	// The token is dead and the route is shut.
	if got := h.do(request("GET", "/api/setup?token="+token, nil)); got.Code != http.StatusForbidden {
		t.Errorf("the setup page after completion: %d %s, want 403", got.Code, got.Body.String())
	}
	if got := h.do(request("POST", "/api/setup", map[string]string{
		"token": token, "username": "mallory", "password": testPassword,
	})); got.Code != http.StatusForbidden {
		t.Errorf("reusing the setup token: %d %s, want 403", got.Code, got.Body.String())
	}
	users, err := h.users.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(users) != 1 {
		t.Errorf("%d accounts after setup, want 1", len(users))
	}
}

// 6.3: setup is refused without the token, and refused with the wrong one.
func TestSetupIsRefusedWithoutAValidToken(t *testing.T) {
	h := newHarness(t)
	token := openSetup(t, h)

	for _, bad := range []string{"", "not-the-token", token + "x", token[:len(token)-1]} {
		if w := h.do(request("GET", "/api/setup?token="+bad, nil)); w.Code != http.StatusForbidden {
			t.Errorf("GET with token %q: %d, want 403", bad, w.Code)
		}
		if w := h.do(request("POST", "/api/setup", map[string]string{
			"token": bad, "username": "mallory", "password": testPassword,
		})); w.Code != http.StatusForbidden {
			t.Errorf("POST with token %q: %d, want 403", bad, w.Code)
		}
	}

	if users, err := h.users.List(); err != nil || len(users) != 0 {
		t.Fatalf("accounts after the refusals: %v, %v, want none", users, err)
	}
	// The real token still works: a refusal must not consume it.
	if w := h.do(request("POST", "/api/setup", map[string]string{
		"token": token, "username": "ada", "password": testPassword,
	})); w.Code != http.StatusCreated {
		t.Errorf("the real token after the refusals: %d %s", w.Code, w.Body.String())
	}
}

// 6.3: a restart before setup completes leaves the flow open and reports a
// valid token. It is a fresh one, so the newest output is always the live one.
func TestSetupSurvivesARestartBeforeCompletion(t *testing.T) {
	h := newHarness(t)
	first := openSetup(t, h)

	// A restart is OpenSetup running again against the same index.
	second := openSetup(t, h)
	if second == first {
		t.Error("the restart reprinted the previous token rather than a fresh one")
	}
	if w := h.do(request("GET", "/api/setup?token="+first, nil)); w.Code != http.StatusForbidden {
		t.Errorf("the pre-restart token: %d, want 403", w.Code)
	}
	if w := h.do(request("GET", "/api/setup?token="+second, nil)); w.Code != http.StatusOK {
		t.Fatalf("the token reported after the restart: %d %s", w.Code, w.Body.String())
	}

	if w := h.do(request("POST", "/api/setup", map[string]string{
		"token": second, "username": "ada", "password": testPassword,
	})); w.Code != http.StatusCreated {
		t.Fatalf("completing setup after the restart: %d %s", w.Code, w.Body.String())
	}

	// And a restart after completion does not reopen it.
	token, err := h.users.OpenSetup()
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		t.Errorf("restarting after setup reopened the flow with token %q", token)
	}
	if w := h.do(request("GET", "/api/setup?token=anything", nil)); w.Code != http.StatusForbidden {
		t.Errorf("the setup route after a restart: %d, want 403", w.Code)
	}
}

// 6.2: a rejected username costs a retry, not the token. Locking an operator
// out of their own instance over a typo would mean restarting the process.
func TestARejectedSetupKeepsTheTokenUsable(t *testing.T) {
	h := newHarness(t)
	token := openSetup(t, h)

	if w := h.do(request("POST", "/api/setup", map[string]string{
		"token": token, "username": "Ada Lovelace", "password": testPassword,
	})); w.Code != http.StatusBadRequest {
		t.Fatalf("an unsafe username: %d %s, want 400", w.Code, w.Body.String())
	}
	if w := h.do(request("POST", "/api/setup", map[string]string{
		"token": token, "username": "ada", "password": "",
	})); w.Code != http.StatusBadRequest {
		t.Fatalf("an empty password: %d %s, want 400", w.Code, w.Body.String())
	}

	if w := h.do(request("POST", "/api/setup", map[string]string{
		"token": token, "username": "ada", "password": testPassword,
	})); w.Code != http.StatusCreated {
		t.Errorf("retrying after the rejections: %d %s", w.Code, w.Body.String())
	}
}
