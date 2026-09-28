package auth

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// index rows for a user's files, written straight into the index: Usage reads
// the index, and what put the row there is not what is under test here.
func indexFile(t *testing.T, s *Store, u *User, name string, size int64, state string) {
	t.Helper()
	_, err := s.db.Exec(`INSERT INTO files (user_id, dir, name, kind, size, mtime, state)
	                     VALUES (?, '', ?, 'file', ?, 0, ?)`, u.ID, name, size, state)
	if err != nil {
		t.Fatal(err)
	}
}

func indexUpload(t *testing.T, s *Store, u *User, id string, size, offset int64) {
	t.Helper()
	_, err := s.db.Exec(`INSERT INTO uploads (id, user_id, dir, name, size, offset_bytes, temp_name, created_at, updated_at)
	                     VALUES (?, ?, '', ?, ?, ?, 'tmp', 0, 0)`, id, u.ID, id, size, offset)
	if err != nil {
		t.Fatal(err)
	}
}

// 1.2: a session left open on a shared machine is not authority to take the
// account over, so the current password has to be right.
func TestChangingAPasswordNeedsTheCurrentOne(t *testing.T) {
	s, _ := testStore(t)
	const old, next = "a long enough password", "an entirely different one"
	ada, err := s.Create("ada", old, false)
	if err != nil {
		t.Fatal(err)
	}

	if err := s.ChangePassword(ada.ID, "not the current password", next); !errors.Is(err, ErrInvalidCredentials) {
		t.Errorf("ChangePassword with the wrong current password: %v, want ErrInvalidCredentials", err)
	}
	if _, err := s.Authenticate("ada", old); err != nil {
		t.Fatalf("the old password after a refused change: %v; the stored hash must be untouched", err)
	}

	if err := s.ChangePassword(ada.ID, old, next); err != nil {
		t.Fatalf("ChangePassword: %v", err)
	}
	if _, err := s.Authenticate("ada", next); err != nil {
		t.Errorf("the new password: %v", err)
	}
	if _, err := s.Authenticate("ada", old); !errors.Is(err, ErrInvalidCredentials) {
		t.Errorf("the old password after the change: %v, want ErrInvalidCredentials", err)
	}
	if err := s.ChangePassword(ada.ID, next, ""); err == nil {
		t.Error("an empty new password was accepted")
	}
}

// 1.2: an administrator resetting a password does not know the old one, and
// the reset moves the account's session epoch so open sessions stop working.
func TestResettingAPasswordMovesTheSessionEpoch(t *testing.T) {
	s, _ := testStore(t)
	ada, err := s.Create("ada", "a long enough password", false)
	if err != nil {
		t.Fatal(err)
	}
	before := s.epoch(t, ada.ID)

	if err := s.SetPassword("ada", "reset by the operator"); err != nil {
		t.Fatalf("SetPassword: %v", err)
	}
	if _, err := s.Authenticate("ada", "reset by the operator"); err != nil {
		t.Errorf("the password an administrator set: %v", err)
	}
	if after := s.epoch(t, ada.ID); after <= before {
		t.Errorf("sessions_valid_from = %d, was %d; a reset must move it forward", after, before)
	}
	if err := s.SetPassword("nobody", "whatever"); !errors.Is(err, ErrNotFound) {
		t.Errorf("SetPassword for an unknown account: %v, want ErrNotFound", err)
	}
}

func (s *Store) epoch(t *testing.T, id int64) int64 {
	t.Helper()
	var at int64
	if err := s.db.QueryRow(`SELECT sessions_valid_from FROM users WHERE id = ?`, id).Scan(&at); err != nil {
		t.Fatal(err)
	}
	return at
}

// 1.3: the store half of the session epoch. The server half — a real cookie
// refused after a reset — is in internal/server.
func TestSessionOlderThanTheEpochStopsResolving(t *testing.T) {
	s, _ := testStore(t)
	ada, err := s.Create("ada", "a long enough password", false)
	if err != nil {
		t.Fatal(err)
	}
	const loggedInAt = 1_000
	if _, err := s.ActiveSession(ada.ID, loggedInAt); err != nil {
		t.Fatalf("a session with no epoch set: %v", err)
	}
	if err := s.SetPassword("ada", "reset by the operator"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ActiveSession(ada.ID, loggedInAt); !errors.Is(err, ErrNotFound) {
		t.Errorf("a session older than the epoch: %v, want ErrNotFound", err)
	}
	if _, err := s.ActiveSession(ada.ID, s.epoch(t, ada.ID)); err != nil {
		t.Errorf("a session opened at the epoch: %v; the login that follows a reset must work", err)
	}
}

// 1.4: an administrator can administer every account but their own, and there
// is no role to change — which is why one comparison is the whole of the rule.
func TestAnAdministratorCannotRemoveThemselves(t *testing.T) {
	operations := map[string]func(s *Store, actor int64, name string) error{
		"delete":  func(s *Store, actor int64, name string) error { return s.Delete(actor, name) },
		"disable": func(s *Store, actor int64, name string) error { return s.SetDisabled(actor, name, true) },
	}

	for operation, apply := range operations {
		t.Run(operation, func(t *testing.T) {
			s, _ := testStore(t)
			root, err := s.Create("root", "a long enough password", true)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.Create("ada", "another long password", false); err != nil {
				t.Fatal(err)
			}

			if err := apply(s, root.ID, "root"); !errors.Is(err, ErrSelfAction) {
				t.Errorf("%s your own account: %v, want ErrSelfAction", operation, err)
			}
			if u, err := s.Active(root.ID); err != nil || !u.IsAdmin {
				t.Errorf("the administrator after a refused %s: %+v, %v", operation, u, err)
			}

			if err := apply(s, root.ID, "ada"); err != nil {
				t.Errorf("%s somebody else's account: %v", operation, err)
			}
			if err := apply(s, 0, "nobody"); !errors.Is(err, ErrNotFound) {
				t.Errorf("%s an unknown account: %v, want ErrNotFound", operation, err)
			}
		})
	}

	// Enabling is nobody's lockout, including your own.
	t.Run("enabling is never refused", func(t *testing.T) {
		s, _ := testStore(t)
		root, err := s.Create("root", "a long enough password", true)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.SetDisabled(root.ID, "root", false); err != nil {
			t.Errorf("enabling your own already-enabled account: %v", err)
		}
	})
}

// 1.4: the status exists, and nothing in the store can hand it out or take it
// away. This is what makes "an instance keeps the administrator it was set up
// with" a fact about the code rather than a rule somebody has to enforce.
func TestNothingGrantsOrRevokesAdministratorStatus(t *testing.T) {
	store := reflect.TypeOf(&Store{})
	for i := range store.NumMethod() {
		if name := store.Method(i).Name; strings.Contains(name, "Admin") {
			t.Errorf("Store has a method %q; administrator status is written by first-run setup and by nothing else", name)
		}
	}

	s, _ := testStore(t)
	ada, err := s.Create("ada", "a long enough password", false)
	if err != nil {
		t.Fatal(err)
	}
	if u, err := s.Active(ada.ID); err != nil || u.IsAdmin {
		t.Errorf("an account created through Create: %+v, %v, want an ordinary user", u, err)
	}
}

// 1.5: usage is what the account occupies. Trash counts because its bytes are
// still on the volume; a missing row does not, because its bytes are not.
func TestUsageCountsFilesTrashAndPendingUploads(t *testing.T) {
	s, _ := testStore(t)
	ada, err := s.Create("ada", "a long enough password", false)
	if err != nil {
		t.Fatal(err)
	}
	bob, err := s.Create("bob", "another long password", false)
	if err != nil {
		t.Fatal(err)
	}

	indexFile(t, s, ada, "notes.txt", 100, "present")
	indexFile(t, s, ada, "photo.jpg", 250, "trashed")
	indexFile(t, s, ada, "gone.txt", 999, "missing")
	indexFile(t, s, bob, "budget.csv", 7, "present")

	got, err := s.Usage(ada.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Bytes != 350 || got.Files != 2 {
		t.Errorf("usage = %d bytes in %d files, want 350 in 2 (present + trashed, never missing)", got.Bytes, got.Files)
	}
	if got.Pending != 0 {
		t.Errorf("pending = %d with no uploads in flight", got.Pending)
	}

	indexUpload(t, s, ada, "upload-1", 1_000, 400)
	if got, err = s.Usage(ada.ID); err != nil || got.Pending != 1_000 {
		t.Errorf("pending = %d, %v; an upload reserves its declared size, not its progress", got.Pending, err)
	}
	if got.Bytes != 350 {
		t.Errorf("stored bytes = %d, want 350; a reservation is not storage", got.Bytes)
	}

	if got, err = s.Usage(bob.ID); err != nil || got.Bytes != 7 || got.Files != 1 {
		t.Errorf("bob's usage = %+v, %v, want 7 bytes in 1 file", got, err)
	}
}

// 1.6: a quota is a limit on the next write and nothing else. Setting one below
// what is already stored must not cost a byte.
func TestQuotaBelowUsageIsStoredAndCostsNothing(t *testing.T) {
	s, dir := testStore(t)
	ada, err := s.Create("ada", "a long enough password", false)
	if err != nil {
		t.Fatal(err)
	}
	put(t, s, ada, "notes.txt", "ada's notes")
	indexFile(t, s, ada, "notes.txt", 11, "present")

	if err := s.SetQuota("ada", 1); err != nil {
		t.Fatalf("SetQuota below current usage: %v", err)
	}
	u, err := s.Active(ada.ID)
	if err != nil || u.QuotaBytes != 1 {
		t.Errorf("quota after setting it: %+v, %v, want 1", u, err)
	}
	if got, err := os.ReadFile(filepath.Join(dir, "users", "ada", "notes.txt")); err != nil || string(got) != "ada's notes" {
		t.Errorf("the file after a quota below usage: %q, %v; a quota deletes nothing", got, err)
	}
	if usage, err := s.Usage(ada.ID); err != nil || usage.Files != 1 {
		t.Errorf("usage after a quota below it: %+v, %v; a quota hides nothing", usage, err)
	}

	if err := s.SetQuota("ada", 0); err != nil {
		t.Fatalf("clearing a quota: %v", err)
	}
	if u, err := s.Active(ada.ID); err != nil || u.QuotaBytes != 0 {
		t.Errorf("quota after clearing it: %+v, %v, want 0 (unlimited)", u, err)
	}
	if err := s.SetQuota("ada", -1); err == nil {
		t.Error("a negative quota was accepted")
	}
	if err := s.SetQuota("nobody", 10); !errors.Is(err, ErrNotFound) {
		t.Errorf("SetQuota for an unknown account: %v, want ErrNotFound", err)
	}
}

// 1.6: a new account takes the configured default, and unlimited stays the
// default default.
func TestNewAccountTakesTheConfiguredQuota(t *testing.T) {
	s, _ := testStore(t)
	if u, err := s.Create("ada", "a long enough password", false); err != nil || u.QuotaBytes != 0 {
		t.Errorf("a new account with no default configured: %+v, %v, want unlimited", u, err)
	}

	s.DefaultQuota = 5 << 30
	u, err := s.Create("bob", "another long password", false)
	if err != nil || u.QuotaBytes != 5<<30 {
		t.Fatalf("a new account with a default configured: %+v, %v", u, err)
	}
	if stored, err := s.Active(u.ID); err != nil || stored.QuotaBytes != 5<<30 {
		t.Errorf("the stored quota: %+v, %v", stored, err)
	}
}
