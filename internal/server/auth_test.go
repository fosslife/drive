package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/fosslife/drive/internal/auth"
)

func login(t *testing.T, h *harness, username, password string) *httptest.ResponseRecorder {
	t.Helper()
	return h.do(request("POST", "/api/login", map[string]string{
		"username": username, "password": password,
	}))
}

// sessionCookie returns the cookie the response set, failing if it set none.
func sessionCookie(t *testing.T, w *httptest.ResponseRecorder) *http.Cookie {
	t.Helper()
	for _, c := range w.Result().Cookies() {
		if c.Name == "drive_session" {
			return c
		}
	}
	t.Fatalf("no session cookie in %v", w.Result().Cookies())
	return nil
}

// 5.2: an unknown account and a wrong password are the same event as far as
// anyone outside is concerned, byte for byte.
func TestLoginRevealsNothingAboutAccountExistence(t *testing.T) {
	h := newHarness(t)
	h.account("ada", false)

	wrong := login(t, h, "ada", "not the password")
	unknown := login(t, h, "grace", "not the password")

	if wrong.Code != http.StatusUnauthorized || unknown.Code != http.StatusUnauthorized {
		t.Fatalf("status: wrong password %d, unknown user %d, want 401 for both", wrong.Code, unknown.Code)
	}
	if wrong.Body.String() != unknown.Body.String() {
		t.Errorf("wrong password says %q but an unknown user says %q", wrong.Body.String(), unknown.Body.String())
	}
	for _, w := range []*httptest.ResponseRecorder{wrong, unknown} {
		if len(w.Result().Cookies()) != 0 {
			t.Errorf("a failed login set %d cookies", len(w.Result().Cookies()))
		}
	}
	if strings.Contains(strings.ToLower(wrong.Body.String()), "ada") {
		t.Errorf("the failure body names the account: %q", wrong.Body.String())
	}
}

// 5.3: guessing is throttled per account and per source, and the correct
// password does not buy a way past the limit.
func TestRepeatedFailedLoginsAreThrottled(t *testing.T) {
	h := newHarness(t)
	h.account("ada", false)

	for i := range auth.DefaultMaxFailures {
		if w := login(t, h, "ada", "not the password"); w.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d: %d, want 401", i+1, w.Code)
		}
	}
	if w := login(t, h, "ada", "not the password"); w.Code != http.StatusTooManyRequests {
		t.Errorf("the attempt after the threshold: %d, want 429", w.Code)
	}
	if w := login(t, h, "ada", testPassword); w.Code != http.StatusTooManyRequests {
		t.Errorf("the correct password while throttled: %d, want 429", w.Code)
	}

	// A different account from the same source is throttled too: the source
	// spent its budget guessing.
	h.account("grace", false)
	if w := login(t, h, "grace", testPassword); w.Code != http.StatusTooManyRequests {
		t.Errorf("a second account from a throttled source: %d, want 429", w.Code)
	}
}

// 5.3: a successful login clears the counters a user's own typos earned.
func TestSuccessfulLoginClearsTheFailureCount(t *testing.T) {
	h := newHarness(t)
	h.account("ada", false)

	for range auth.DefaultMaxFailures - 1 {
		login(t, h, "ada", "not the password")
	}
	if w := login(t, h, "ada", testPassword); w.Code != http.StatusOK {
		t.Fatalf("login after some typos: %d %s", w.Code, w.Body.String())
	}
	for i := range auth.DefaultMaxFailures {
		if w := login(t, h, "ada", "not the password"); w.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d after the reset: %d, want 401", i+1, w.Code)
		}
	}
}

// 5.4: HttpOnly so stored XSS cannot lift it, Secure so it never crosses
// plaintext, SameSite so another site cannot ride it.
func TestSessionCookieAttributes(t *testing.T) {
	h := newHarness(t)
	h.account("ada", false)

	c := sessionCookie(t, login(t, h, "ada", testPassword))
	if !c.HttpOnly {
		t.Error("the session cookie is readable from script")
	}
	if !c.Secure {
		t.Error("the session cookie is not restricted to secure transport")
	}
	if c.SameSite != http.SameSiteLaxMode {
		t.Errorf("SameSite = %v, want Lax", c.SameSite)
	}
	if c.Expires.IsZero() && c.MaxAge == 0 {
		t.Error("the session cookie never expires")
	}
}

// 5.4: the cookie is an opaque handle. It carries no identity and no
// privileges, and editing it can only stop it working.
func TestSessionCookieCarriesNoAuthority(t *testing.T) {
	h := newHarness(t)
	ada := h.account("ada", false)
	c := sessionCookie(t, login(t, h, "ada", testPassword))

	for _, leak := range []string{"ada", "admin", "user", "id"} {
		if strings.Contains(strings.ToLower(c.Value), leak) {
			t.Errorf("the session cookie value %q contains %q", c.Value, leak)
		}
	}

	req := request("GET", "/api/me", nil)
	req.AddCookie(c)
	me := decodeBody[auth.User](t, h.do(req))
	if me.ID != ada.ID || me.Username != "ada" || me.IsAdmin {
		t.Fatalf("/api/me = %+v, want ada, not an administrator", me)
	}

	// Flipping a byte must not promote or re-identify anyone; it must fail.
	tampered := &http.Cookie{Name: c.Name, Value: strings.Repeat("A", len(c.Value))}
	req = request("GET", "/api/me", nil)
	req.AddCookie(tampered)
	if w := h.do(req); w.Code != http.StatusUnauthorized {
		t.Errorf("an edited cookie: %d %s, want 401", w.Code, w.Body.String())
	}
}

// 5.4: logout is server-side revocation, so replaying the cookie afterwards
// does nothing.
func TestLogoutRevokesTheSession(t *testing.T) {
	h := newHarness(t)
	h.account("ada", false)
	c := sessionCookie(t, login(t, h, "ada", testPassword))

	req := request("POST", "/api/logout", nil)
	req.AddCookie(c)
	if w := h.do(req); w.Code != http.StatusNoContent {
		t.Fatalf("logout: %d %s", w.Code, w.Body.String())
	}

	req = request("GET", "/api/me", nil)
	req.AddCookie(c)
	if w := h.do(req); w.Code != http.StatusUnauthorized {
		t.Errorf("the cookie after logout: %d %s, want 401", w.Code, w.Body.String())
	}
}

// 5.5: session fixation. An identifier that existed before authentication must
// not be the one that authenticates afterwards.
func TestSessionIdentifierIsRenewedOnLogin(t *testing.T) {
	h := newHarness(t)
	h.account("ada", false)

	// A pre-authentication session, as an attacker would plant in a browser.
	ctx, err := h.sessions.Load(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	h.sessions.Put(ctx, "visited", true)
	planted, _, err := h.sessions.Commit(ctx)
	if err != nil {
		t.Fatal(err)
	}

	req := request("POST", "/api/login", map[string]string{"username": "ada", "password": testPassword})
	req.AddCookie(&http.Cookie{Name: "drive_session", Value: planted})
	w := h.do(req)
	if w.Code != http.StatusOK {
		t.Fatalf("login: %d %s", w.Code, w.Body.String())
	}

	issued := sessionCookie(t, w)
	if issued.Value == planted {
		t.Fatal("login kept the pre-authentication session identifier")
	}

	// The planted identifier must not have been promoted along the way.
	replay := request("GET", "/api/me", nil)
	replay.AddCookie(&http.Cookie{Name: "drive_session", Value: planted})
	if got := h.do(replay); got.Code != http.StatusUnauthorized {
		t.Errorf("the planted identifier after login: %d %s, want 401", got.Code, got.Body.String())
	}

	// The one actually issued does authenticate.
	req = request("GET", "/api/me", nil)
	req.AddCookie(issued)
	if got := h.do(req); got.Code != http.StatusOK {
		t.Errorf("the issued identifier: %d %s", got.Code, got.Body.String())
	}
}

// 5.6: a valid session cookie is not enough. A state-changing request must also
// show it came from this application, and it is refused before the handler runs.
func TestCrossSiteStateChangeIsRefused(t *testing.T) {
	h := newHarness(t)
	ada := h.account("ada", false)
	c := sessionCookie(t, login(t, h, "ada", testPassword))

	forged := []struct {
		name    string
		headers map[string]string
	}{
		{"another site's form", map[string]string{"Sec-Fetch-Site": "cross-site", "Origin": "https://evil.example"}},
		{"another site without Sec-Fetch-Site", map[string]string{"Origin": "https://evil.example"}},
		{"a subdomain of another site", map[string]string{"Origin": "https://example.com.evil.example"}},
		{"no origin signal at all", nil},
	}
	for _, f := range forged {
		t.Run(f.name, func(t *testing.T) {
			req := bare("POST", "/api/tokens", map[string]string{"name": "stolen"})
			req.AddCookie(c)
			for k, v := range f.headers {
				req.Header.Set(k, v)
			}
			if w := h.do(req); w.Code != http.StatusForbidden {
				t.Errorf("%d %s, want 403", w.Code, w.Body.String())
			}
		})
	}

	// Nothing the refused requests asked for was done.
	tokens, err := h.users.ListTokens(ada.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(tokens) != 0 {
		t.Errorf("%d tokens were created by refused requests", len(tokens))
	}

	// The same request from the application itself works, by either signal.
	for _, ok := range []map[string]string{
		{"Sec-Fetch-Site": "same-origin"},
		{"Origin": "https://example.com"},
	} {
		req := bare("POST", "/api/tokens", map[string]string{"name": "laptop"})
		req.Host = "example.com"
		req.AddCookie(c)
		for k, v := range ok {
			req.Header.Set(k, v)
		}
		if w := h.do(req); w.Code != http.StatusCreated {
			t.Errorf("a first-party request with %v: %d %s", ok, w.Code, w.Body.String())
		}
	}
}

// 5.6: a bearer token is not sent automatically by a browser, so there is
// nothing to forge and an ordinary API client needs no Origin header.
func TestTokenAuthenticatedRequestNeedsNoOriginHeader(t *testing.T) {
	h := newHarness(t)
	secret := h.token(h.account("ada", false))

	req := bare("POST", "/api/tokens", map[string]string{"name": "second"})
	req.Header.Set("Authorization", "Bearer "+secret)
	if w := h.do(req); w.Code != http.StatusCreated {
		t.Errorf("%d %s, want 201", w.Code, w.Body.String())
	}
}

// 5.7: the secret exists in exactly one response, and revoking one token stops
// it without touching the others.
func TestAPITokenLifecycleOverHTTP(t *testing.T) {
	h := newHarness(t)
	h.account("ada", false)
	c := sessionCookie(t, login(t, h, "ada", testPassword))

	create := func(name string) (int64, string) {
		t.Helper()
		req := request("POST", "/api/tokens", map[string]string{"name": name})
		req.AddCookie(c)
		w := h.do(req)
		if w.Code != http.StatusCreated {
			t.Fatalf("creating %q: %d %s", name, w.Code, w.Body.String())
		}
		got := decodeBody[struct {
			ID     int64  `json:"id"`
			Secret string `json:"secret"`
		}](t, w)
		if got.Secret == "" {
			t.Fatalf("creating %q returned no secret", name)
		}
		return got.ID, got.Secret
	}

	laptopID, laptop := create("laptop")
	_, phone := create("phone")

	req := request("GET", "/api/tokens", nil)
	req.AddCookie(c)
	w := h.do(req)
	body := w.Body.String()
	if !strings.Contains(body, "laptop") || !strings.Contains(body, "phone") {
		t.Errorf("token list %q is missing a token", body)
	}
	for _, secret := range []string{laptop, phone} {
		if strings.Contains(body, secret) {
			t.Errorf("the token list discloses a secret: %q", body)
		}
	}
	if strings.Contains(body, "secret") {
		t.Errorf("the token list has a secret field: %q", body)
	}

	// Both work before the revoke.
	for _, secret := range []string{laptop, phone} {
		req := request("GET", "/api/me", nil)
		req.Header.Set("Authorization", "Bearer "+secret)
		if got := h.do(req); got.Code != http.StatusOK {
			t.Fatalf("a fresh token: %d %s", got.Code, got.Body.String())
		}
	}

	req = request("DELETE", "/api/tokens/"+strconv.FormatInt(laptopID, 10), nil)
	req.AddCookie(c)
	if got := h.do(req); got.Code != http.StatusNoContent {
		t.Fatalf("revoking: %d %s", got.Code, got.Body.String())
	}

	req = request("GET", "/api/me", nil)
	req.Header.Set("Authorization", "Bearer "+laptop)
	if got := h.do(req); got.Code != http.StatusUnauthorized {
		t.Errorf("the revoked token: %d %s, want 401", got.Code, got.Body.String())
	}
	req = request("GET", "/api/me", nil)
	req.Header.Set("Authorization", "Bearer "+phone)
	if got := h.do(req); got.Code != http.StatusOK {
		t.Errorf("the other token after a revoke: %d %s", got.Code, got.Body.String())
	}
}

// 5.1: account management is an administrator's, and the change lands on the
// user's next request rather than their next login.
func TestAdministratorManagesAccounts(t *testing.T) {
	h := newHarness(t)
	h.account("root", true)
	admin := sessionCookie(t, login(t, h, "root", testPassword))

	as := func(c *http.Cookie, method, path string, body any) *httptest.ResponseRecorder {
		t.Helper()
		req := request(method, path, body)
		req.AddCookie(c)
		return h.do(req)
	}

	if w := as(admin, "POST", "/api/admin/users", map[string]any{
		"username": "ada", "password": testPassword, "is_admin": false,
	}); w.Code != http.StatusCreated {
		t.Fatalf("creating an account: %d %s", w.Code, w.Body.String())
	}
	if w := as(admin, "POST", "/api/admin/users", map[string]any{
		"username": "Ada", "password": testPassword,
	}); w.Code != http.StatusBadRequest {
		t.Errorf("an unsafe username: %d %s, want 400", w.Code, w.Body.String())
	}

	ada := sessionCookie(t, login(t, h, "ada", testPassword))
	if w := as(ada, "GET", "/api/me", nil); w.Code != http.StatusOK {
		t.Fatalf("the new account: %d %s", w.Code, w.Body.String())
	}
	// A non-administrator cannot manage accounts.
	if w := as(ada, "GET", "/api/admin/users", nil); w.Code != http.StatusForbidden {
		t.Errorf("ada listing accounts: %d %s, want 403", w.Code, w.Body.String())
	}

	if w := as(admin, "POST", "/api/admin/users/ada/disabled", map[string]bool{"disabled": true}); w.Code != http.StatusNoContent {
		t.Fatalf("disabling: %d %s", w.Code, w.Body.String())
	}
	if w := as(ada, "GET", "/api/me", nil); w.Code != http.StatusUnauthorized {
		t.Errorf("a disabled account's existing session: %d, want 401", w.Code)
	}
	if w := login(t, h, "ada", testPassword); w.Code != http.StatusUnauthorized {
		t.Errorf("a disabled account logging in: %d, want 401", w.Code)
	}

	if w := as(admin, "DELETE", "/api/admin/users/ada", nil); w.Code != http.StatusNoContent {
		t.Fatalf("deleting: %d %s", w.Code, w.Body.String())
	}
	if w := as(admin, "DELETE", "/api/admin/users/ada", nil); w.Code != http.StatusNotFound {
		t.Errorf("deleting twice: %d, want 404", w.Code)
	}
	if w := as(admin, "GET", "/api/admin/users", nil); !strings.Contains(w.Body.String(), "root") || strings.Contains(w.Body.String(), "ada") {
		t.Errorf("account list after the delete: %s", w.Body.String())
	}
}
