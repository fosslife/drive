package server

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/fosslife/drive/internal/auth"
)

// inventory reads the administrator's account list.
func inventory(t *testing.T, h *harness, secret string) map[string]map[string]any {
	t.Helper()
	w := h.as(secret, "GET", "/api/admin/users", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("listing accounts: %d %s", w.Code, w.Body.String())
	}
	var accounts []map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &accounts); err != nil {
		t.Fatalf("decoding the account list %s: %v", w.Body.String(), err)
	}
	byName := make(map[string]map[string]any, len(accounts))
	for _, a := range accounts {
		byName[a["username"].(string)] = a
	}
	return byName
}

// 3.1: one endpoint for the administrable fields, each optional, each applied
// on its own.
func TestPatchAppliesEachFieldOnItsOwn(t *testing.T) {
	h := newHarness(t)
	root := h.account("root", true)
	h.account("ada", false)
	admin := h.token(root)

	cases := []struct {
		name  string
		body  map[string]any
		check func(a map[string]any) string
	}{
		{"quota alone", map[string]any{"quota_bytes": 4096}, func(a map[string]any) string {
			if a["quota_bytes"] != float64(4096) {
				return "quota_bytes did not change"
			}
			if a["is_admin"] != false || a["disabled"] != false {
				return "a quota patch changed role or state"
			}
			return ""
		}},
		{"role alone", map[string]any{"is_admin": true}, func(a map[string]any) string {
			if a["is_admin"] != true {
				return "is_admin did not change"
			}
			if a["quota_bytes"] != float64(4096) {
				return "a role patch moved the quota"
			}
			return ""
		}},
		{"state alone", map[string]any{"disabled": true}, func(a map[string]any) string {
			if a["disabled"] != true {
				return "disabled did not change"
			}
			if a["is_admin"] != true || a["quota_bytes"] != float64(4096) {
				return "a state patch changed role or quota"
			}
			return ""
		}},
		{"nothing at all", map[string]any{}, func(a map[string]any) string {
			if a["disabled"] != true || a["is_admin"] != true || a["quota_bytes"] != float64(4096) {
				return "an empty patch changed something"
			}
			return ""
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if w := h.as(admin, "PATCH", "/api/admin/users/ada", c.body); w.Code != http.StatusNoContent {
				t.Fatalf("PATCH %v: %d %s", c.body, w.Code, w.Body.String())
			}
			if complaint := c.check(inventory(t, h, admin)["ada"]); complaint != "" {
				t.Error(complaint)
			}
		})
	}

	if w := h.as(admin, "PATCH", "/api/admin/users/nobody", map[string]any{}); w.Code != http.StatusNotFound {
		t.Errorf("an empty patch of an unknown account: %d %s, want 404", w.Code, w.Body.String())
	}
	// The route this replaced is gone rather than kept as a second way in.
	if w := h.as(admin, "POST", "/api/admin/users/ada/disabled", map[string]bool{"disabled": true}); w.Code < 400 {
		t.Errorf("the replaced disabled route answered %d; it must no longer be routed", w.Code)
	}
}

// 3.2: an administrator resets, a user changes their own with the current one
// in hand, and neither endpoint hands back anything about a stored hash.
func TestPasswordResetAndSelfServiceChange(t *testing.T) {
	h := newHarness(t)
	root := h.account("root", true)
	adaUser, bobUser := h.account("ada", false), h.account("bob", false)
	admin, ada, bob := h.token(root), h.token(adaUser), h.token(bobUser)

	// Only an administrator resets somebody else's password.
	if w := h.as(bob, "POST", "/api/admin/users/ada/password", map[string]string{"password": "bob decided"}); w.Code != http.StatusForbidden {
		t.Errorf("bob resetting ada's password: %d %s, want 403", w.Code, w.Body.String())
	}
	if w := h.as(admin, "POST", "/api/admin/users/ada/password", map[string]string{"password": "reset by the operator"}); w.Code != http.StatusNoContent {
		t.Fatalf("resetting ada's password: %d %s", w.Code, w.Body.String())
	}
	if w := login(t, h, "ada", "reset by the operator"); w.Code != http.StatusOK {
		t.Errorf("signing in after the reset: %d %s", w.Code, w.Body.String())
	}

	// A user changes their own, and the current one has to be right.
	if w := h.as(ada, "POST", "/api/me/password", map[string]string{
		"current_password": "not it", "new_password": "chosen by ada",
	}); w.Code != http.StatusForbidden {
		t.Errorf("the wrong current password: %d %s, want 403", w.Code, w.Body.String())
	}
	if w := h.as(ada, "POST", "/api/me/password", map[string]string{
		"current_password": "reset by the operator", "new_password": "chosen by ada",
	}); w.Code != http.StatusNoContent {
		t.Fatalf("changing her own password: %d %s", w.Code, w.Body.String())
	}
	if w := login(t, h, "ada", "chosen by ada"); w.Code != http.StatusOK {
		t.Errorf("signing in with the password ada chose: %d %s", w.Code, w.Body.String())
	}
	if w := h.as(ada, "POST", "/api/me/password", map[string]string{
		"current_password": "chosen by ada", "new_password": "",
	}); w.Code != http.StatusBadRequest {
		t.Errorf("an empty new password: %d %s, want 400", w.Code, w.Body.String())
	}

	// Wrong current passwords count against the same limiter a failed login
	// does, so this endpoint is not an unthrottled way to guess one.
	for range auth.DefaultMaxFailures {
		h.as(bob, "POST", "/api/me/password", map[string]string{
			"current_password": "guess", "new_password": "whatever",
		})
	}
	if w := h.as(bob, "POST", "/api/me/password", map[string]string{
		"current_password": "guess", "new_password": "whatever",
	}); w.Code != http.StatusTooManyRequests {
		t.Errorf("after %d wrong current passwords: %d %s, want 429", auth.DefaultMaxFailures, w.Code, w.Body.String())
	}

	for _, body := range []string{
		h.as(admin, "GET", "/api/admin/users", nil).Body.String(),
		h.as(ada, "GET", "/api/me", nil).Body.String(),
	} {
		if strings.Contains(body, "argon2") || strings.Contains(body, "password_hash") {
			t.Errorf("an account response carries password material: %s", body)
		}
	}
}

// 3.3 and 3.4: the figures an administrator sees match an independent count,
// and a user's own view is their own account only.
func TestUsageReportedToAdministratorAndToTheAccountItself(t *testing.T) {
	h := newHarness(t)
	root := h.account("root", true)
	ada := h.account("ada", false)
	h.account("bob", false)
	admin, adaToken := h.token(root), h.token(ada)

	h.put(ada, "notes.txt", "eleven byte")
	h.put(ada, "budget.csv", "a,b\n1,2\n")
	h.scan(ada)

	var want int64
	var files int64
	if err := h.db.QueryRow(`SELECT COALESCE(SUM(size), 0), COUNT(*) FROM files
	                          WHERE user_id = ? AND kind = 'file' AND state IN ('present', 'trashed')`,
		ada.ID).Scan(&want, &files); err != nil {
		t.Fatal(err)
	}
	if want == 0 || files != 2 {
		t.Fatalf("the index holds %d bytes in %d files; the test set up nothing to measure", want, files)
	}

	listed := inventory(t, h, admin)["ada"]
	if listed["usage_bytes"] != float64(want) || listed["file_count"] != float64(files) {
		t.Errorf("inventory reports %v bytes in %v files, want %d in %d",
			listed["usage_bytes"], listed["file_count"], want, files)
	}
	if _, ok := listed["created_at"]; !ok {
		t.Error("the inventory does not report when an account was created")
	}
	if bob := inventory(t, h, admin)["bob"]; bob["usage_bytes"] != float64(0) {
		t.Errorf("bob's usage = %v, want 0", bob["usage_bytes"])
	}

	var me map[string]any
	if err := json.Unmarshal(h.as(adaToken, "GET", "/api/me", nil).Body.Bytes(), &me); err != nil {
		t.Fatal(err)
	}
	if me["username"] != "ada" || me["usage_bytes"] != float64(want) {
		t.Errorf("/api/me = %v, want ada's own %d bytes", me, want)
	}
	if _, ok := me["quota_bytes"]; !ok {
		t.Error("/api/me does not report the account's quota")
	}
	// /api/me names no account but the caller's, so there is no shape of this
	// request that reads somebody else's figures.
	if w := h.as(adaToken, "GET", "/api/me?username=bob", nil); w.Code == http.StatusOK &&
		!strings.Contains(w.Body.String(), `"username":"ada"`) {
		t.Errorf("/api/me with another username in the query: %s", w.Body.String())
	}
	if w := h.as(adaToken, "GET", "/api/admin/users", nil); w.Code != http.StatusForbidden {
		t.Errorf("ada reading the inventory: %d, want 403", w.Code)
	}
}

// 3.5: each refusal gets the status that says what happened, and says why.
func TestAccountRefusalsCarryTheirReason(t *testing.T) {
	h := newHarness(t)
	root := h.account("root", true)
	h.account("ada", false)
	admin := h.token(root)

	cases := []struct {
		name   string
		method string
		path   string
		body   any
		status int
		reason string
	}{
		// The last-administrator guard is not reachable from here, and that is
		// not a gap: whoever is calling is an enabled administrator, so a
		// different enabled administrator as the target means two of them
		// exist. What stops an administrator being the last one out is the
		// self-action rule below. The guard itself is covered in
		// internal/auth, where a caller with no identity can reach it.
		{"demote your own account", "PATCH", "/api/admin/users/root",
			map[string]any{"is_admin": false}, http.StatusConflict, "own account"},
		{"delete your own account", "DELETE", "/api/admin/users/root",
			nil, http.StatusConflict, "own account"},
		{"unknown account", "DELETE", "/api/admin/users/nobody",
			nil, http.StatusNotFound, "no such account"},
		{"negative quota", "PATCH", "/api/admin/users/ada",
			map[string]any{"quota_bytes": -1}, http.StatusBadRequest, "negative"},
		{"empty reset password", "POST", "/api/admin/users/ada/password",
			map[string]string{"password": ""}, http.StatusBadRequest, "password"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := h.as(admin, c.method, c.path, c.body)
			if w.Code != c.status {
				t.Fatalf("%s %s: %d %s, want %d", c.method, c.path, w.Code, w.Body.String(), c.status)
			}
			if !strings.Contains(w.Body.String(), c.reason) {
				t.Errorf("the refusal %s does not say why (want %q)", w.Body.String(), c.reason)
			}
		})
	}

	// Self-action, which needs a second administrator to prove it is the rule
	// refusing rather than the last-administrator one.
	h.account("second", true)
	for _, body := range []any{map[string]any{"is_admin": false}, map[string]any{"disabled": true}} {
		if w := h.as(admin, "PATCH", "/api/admin/users/root", body); w.Code != http.StatusConflict ||
			!strings.Contains(w.Body.String(), "own account") {
			t.Errorf("patching your own account with %v: %d %s, want 409 naming it", body, w.Code, w.Body.String())
		}
	}
	if w := h.as(admin, "DELETE", "/api/admin/users/root", nil); w.Code != http.StatusConflict {
		t.Errorf("deleting your own account: %d %s, want 409", w.Code, w.Body.String())
	}
}

// 3.6: administrator status administers accounts. It is not a key to anyone's
// files, and the refusal is the same one anybody else gets.
func TestAdministratorHasNoPathIntoAnotherAccountsFiles(t *testing.T) {
	h := newHarness(t)
	root := h.account("root", true)
	bob := h.account("bob", false)
	admin, ada := h.token(root), h.token(h.account("ada", false))

	h.put(bob, "budget.csv", "bob's budget")
	h.scan(bob)

	for _, request := range []struct{ name, path string }{
		{"download", "/api/download/budget.csv"},
		{"thumbnail", "/api/thumb/budget.csv"},
		{"listing", "/api/list?path=" + url.QueryEscape("")},
		{"search", "/api/search?q=budget"},
	} {
		t.Run(request.name, func(t *testing.T) {
			asAdmin := h.as(admin, "GET", request.path, nil)
			asUser := h.as(ada, "GET", request.path, nil)
			if asAdmin.Code != asUser.Code {
				t.Errorf("%s answered %d for an administrator and %d for another user; being an administrator changed the answer",
					request.path, asAdmin.Code, asUser.Code)
			}
			// The content, always. The name only where the caller did not
			// supply it: a "no such file" that quotes the path asked for
			// discloses nothing the asker did not already type.
			if strings.Contains(asAdmin.Body.String(), "bob's budget") {
				t.Errorf("%s disclosed bob's content to an administrator: %s", request.path, asAdmin.Body.String())
			}
			if !strings.Contains(request.path, "budget.csv") && strings.Contains(asAdmin.Body.String(), "budget.csv") {
				t.Errorf("%s disclosed bob's file to an administrator: %s", request.path, asAdmin.Body.String())
			}
		})
	}
}
