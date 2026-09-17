package auth

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fosslife/drive/internal/index"
)

func testStore(t *testing.T) (*Store, string) {
	t.Helper()
	dir := t.TempDir()
	db, err := index.Open(filepath.Join(dir, "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return NewStore(db, dir, 0), dir
}

// put writes a file into a user's storage root through the storage API, which
// is the only way anything gets in there.
func put(t *testing.T, s *Store, u *User, name, content string) {
	t.Helper()
	root, err := s.Root(u)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if _, err := root.Write(name, strings.NewReader(content), int64(len(content))); err != nil {
		t.Fatal(err)
	}
}

// 5.1: deleting an account is an index operation. It must not reach into
// anyone's bytes — not the other account's, and not even its own.
func TestDeletingAnAccountLeavesEveryFileWhereItWas(t *testing.T) {
	s, dir := testStore(t)
	ada, err := s.Create("ada", "a long enough password", false)
	if err != nil {
		t.Fatal(err)
	}
	bob, err := s.Create("bob", "another long password", false)
	if err != nil {
		t.Fatal(err)
	}
	put(t, s, ada, "notes.txt", "ada's notes")
	put(t, s, bob, "budget.csv", "bob's budget")

	if err := s.Delete("ada"); err != nil {
		t.Fatal(err)
	}

	if got, err := os.ReadFile(filepath.Join(dir, "users", "bob", "budget.csv")); err != nil || string(got) != "bob's budget" {
		t.Errorf("bob's file after deleting ada: %q %v", got, err)
	}
	if got, err := os.ReadFile(filepath.Join(dir, "users", "ada", "notes.txt")); err != nil || string(got) != "ada's notes" {
		t.Errorf("ada's own bytes after deleting her account: %q %v; only a permanent delete may destroy content", got, err)
	}
	if _, err := s.Active(bob.ID); err != nil {
		t.Errorf("bob's account after deleting ada: %v", err)
	}
	if _, err := s.Active(ada.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("ada's account after deletion: %v, want ErrNotFound", err)
	}
	if err := s.Delete("ada"); !errors.Is(err, ErrNotFound) {
		t.Errorf("deleting ada twice: %v, want ErrNotFound", err)
	}
}

// 5.1: the storage root is a directory named for the username, so anything
// outside [a-z0-9._-] would have to be escaped somewhere. It is refused instead.
func TestUnsafeUsernameIsRefused(t *testing.T) {
	unsafe := []string{
		"", ".", "..", "../bob", "ada/bob", `ada\bob`, "ada bob", "Ada", "ADA",
		"ada\x00", "ada\n", "~root", "a:b", "ада", strings.Repeat("a", 33),
	}
	for _, name := range unsafe {
		t.Run(name, func(t *testing.T) {
			s, dir := testStore(t)
			if _, err := s.Create(name, "a long enough password", false); !errors.Is(err, ErrInvalidUsername) {
				t.Fatalf("Create(%q) = %v, want ErrInvalidUsername", name, err)
			}
			// Nothing was created on the way to the rejection.
			if entries, _ := os.ReadDir(filepath.Join(dir, "users")); len(entries) != 0 {
				t.Errorf("Create(%q) left %d entries under users/", name, len(entries))
			}
		})
	}

	s, _ := testStore(t)
	for _, name := range []string{"ada", "ada.b_c-1", "0", "a-very-long-but-legal-name-here"} {
		if _, err := s.Create(name, "a long enough password", false); err != nil {
			t.Errorf("Create(%q) = %v, want success", name, err)
		}
	}
}

// 5.1: the recovery path after the index is lost. A new account with the old
// username gets the old root back, files and all.
func TestRecreatedAccountReattachesToItsStorageRoot(t *testing.T) {
	s, _ := testStore(t)
	first, err := s.Create("ada", "a long enough password", false)
	if err != nil {
		t.Fatal(err)
	}
	put(t, s, first, "notes.txt", "ada's notes")

	if _, err := s.Create("ada", "a long enough password", false); !errors.Is(err, ErrUserExists) {
		t.Fatalf("creating a duplicate username: %v, want ErrUserExists", err)
	}

	if err := s.Delete("ada"); err != nil {
		t.Fatal(err)
	}
	second, err := s.Create("ada", "a completely different password", false)
	if err != nil {
		t.Fatal(err)
	}
	if second.ID == first.ID {
		t.Errorf("account id %d was reused; identities are never recycled", second.ID)
	}
	if second.StorageRoot != first.StorageRoot {
		t.Fatalf("storage root %q, want the original %q", second.StorageRoot, first.StorageRoot)
	}

	root, err := s.Root(second)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if _, err := root.Checksum("notes.txt"); err != nil {
		t.Errorf("file in the reattached root: %v", err)
	}
}

// 5.2: Argon2id, parameters carried in the hash so they can be raised without
// invalidating anything already stored.
func TestPasswordHashing(t *testing.T) {
	hash, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(hash, "$argon2id$v=19$m=") {
		t.Errorf("hash %q is not a PHC Argon2id string", hash)
	}
	if !VerifyPassword(hash, "correct horse battery staple") {
		t.Error("the correct password did not verify")
	}
	if VerifyPassword(hash, "correct horse battery stapl") {
		t.Error("a wrong password verified")
	}

	again, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	if again == hash {
		t.Error("two hashes of the same password are identical; the salt is not random")
	}

	// A damaged row must read as a wrong password, never as a match.
	for _, bad := range []string{"", "plaintext", "$argon2id$v=19$m=x,t=2,p=1$aaaa$bbbb", "$argon2i$v=19$m=19456,t=2,p=1$aaaa$bbbb"} {
		if VerifyPassword(bad, "anything") {
			t.Errorf("VerifyPassword(%q, ...) = true", bad)
		}
	}
}

// 5.2: the index is what an operator backs up and what an attacker who gets the
// disk reads. No password may be recoverable from it.
func TestIndexHoldsNoPlaintextPassword(t *testing.T) {
	const password = "sphinx-of-black-quartz-judge-my-vow"
	dir := t.TempDir()
	path := filepath.Join(dir, "index.db")
	db, err := index.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	s := NewStore(db, dir, 0)
	if _, err := s.Create("ada", password, false); err != nil {
		t.Fatal(err)
	}
	var stored string
	if err := db.QueryRow(`SELECT password_hash FROM users WHERE username = 'ada'`).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stored, password) {
		t.Fatalf("password_hash %q contains the password", stored)
	}
	// Closing checkpoints the write-ahead log into the database file.
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	for _, suffix := range []string{"", "-wal", "-shm"} {
		raw, err := os.ReadFile(path + suffix)
		if err != nil {
			continue
		}
		if bytes.Contains(raw, []byte(password)) {
			t.Errorf("%s contains the plaintext password", filepath.Base(path+suffix))
		}
	}
}

// 5.2: a wrong password, an unknown account, and a disabled one are one answer.
func TestAuthenticateGivesOneAnswerToEveryFailure(t *testing.T) {
	s, _ := testStore(t)
	if _, err := s.Create("ada", "a long enough password", false); err != nil {
		t.Fatal(err)
	}

	if _, err := s.Authenticate("ada", "a long enough password"); err != nil {
		t.Fatalf("the correct password: %v", err)
	}
	for _, c := range []struct{ user, password string }{
		{"ada", "the wrong password"},
		{"nobody", "the wrong password"},
		{"nobody", "a long enough password"},
	} {
		if _, err := s.Authenticate(c.user, c.password); !errors.Is(err, ErrInvalidCredentials) {
			t.Errorf("Authenticate(%q, %q) = %v, want ErrInvalidCredentials", c.user, c.password, err)
		}
	}

	if err := s.SetDisabled("ada", true); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Authenticate("ada", "a long enough password"); !errors.Is(err, ErrInvalidCredentials) {
		t.Errorf("a disabled account with the right password: %v, want ErrInvalidCredentials", err)
	}
}

// 5.1: a disable takes effect on the account's next request, not its next login.
func TestDisabledAccountStopsResolving(t *testing.T) {
	s, _ := testStore(t)
	u, err := s.Create("ada", "a long enough password", false)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetDisabled("ada", true); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Active(u.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("a disabled account resolved: %v", err)
	}
	if err := s.SetDisabled("ada", false); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Active(u.ID); err != nil {
		t.Errorf("a re-enabled account: %v", err)
	}
}

// 5.3: the threshold, and that it lets go once the window passes.
func TestLimiterRefusesAfterThreshold(t *testing.T) {
	l := NewLimiter(3, 50*time.Millisecond)
	for i := range 3 {
		if !l.Allow("user:ada", "ip:10.0.0.1") {
			t.Fatalf("attempt %d was refused before the threshold", i+1)
		}
		l.Fail("user:ada", "ip:10.0.0.1")
	}
	if l.Allow("user:ada", "ip:10.0.0.1") {
		t.Error("the attempt after the threshold was allowed")
	}
	// A different source is still spending the account's budget, and vice versa.
	if l.Allow("user:ada", "ip:10.0.0.9") {
		t.Error("the same account from a new source was allowed")
	}
	if l.Allow("user:grace", "ip:10.0.0.1") {
		t.Error("a new account from the same source was allowed")
	}
	if !l.Allow("user:grace", "ip:10.0.0.9") {
		t.Error("an unrelated attempt was refused")
	}

	time.Sleep(60 * time.Millisecond)
	if !l.Allow("user:ada", "ip:10.0.0.1") {
		t.Error("the attempt after the window passed was still refused")
	}
}

// 5.7: only the hash is stored, and a revoked token stops resolving.
func TestAPITokens(t *testing.T) {
	s, _ := testStore(t)
	ada, err := s.Create("ada", "a long enough password", false)
	if err != nil {
		t.Fatal(err)
	}
	secret, token, err := s.CreateToken(ada.ID, "laptop")
	if err != nil {
		t.Fatal(err)
	}

	got, err := s.AuthenticateToken(secret)
	if err != nil || got.ID != ada.ID {
		t.Fatalf("AuthenticateToken = %v, %v, want ada", got, err)
	}
	if _, err := s.AuthenticateToken(secret + "x"); !errors.Is(err, ErrInvalidToken) {
		t.Errorf("a wrong secret: %v, want ErrInvalidToken", err)
	}

	var stored string
	if err := s.db.QueryRow(`SELECT token_hash FROM api_tokens WHERE id = ?`, token.ID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored == secret || strings.Contains(stored, secret) {
		t.Error("the token secret is stored rather than its hash")
	}

	if err := s.RevokeToken(ada.ID, token.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AuthenticateToken(secret); !errors.Is(err, ErrInvalidToken) {
		t.Errorf("a revoked token: %v, want ErrInvalidToken", err)
	}
}

// 5.7: revoking is scoped to the owner, so an id guessed from another account
// is a miss rather than a cross-account revoke.
func TestTokenRevokeIsScopedToItsOwner(t *testing.T) {
	s, _ := testStore(t)
	ada, err := s.Create("ada", "a long enough password", false)
	if err != nil {
		t.Fatal(err)
	}
	bob, err := s.Create("bob", "a long enough password", false)
	if err != nil {
		t.Fatal(err)
	}
	secret, token, err := s.CreateToken(ada.ID, "laptop")
	if err != nil {
		t.Fatal(err)
	}

	if err := s.RevokeToken(bob.ID, token.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("bob revoking ada's token: %v, want ErrNotFound", err)
	}
	if _, err := s.AuthenticateToken(secret); err != nil {
		t.Errorf("ada's token after bob tried to revoke it: %v", err)
	}
	if tokens, err := s.ListTokens(bob.ID); err != nil || len(tokens) != 0 {
		t.Errorf("bob's token list: %v, %v, want empty", tokens, err)
	}
}

// 5.1: deleting an account takes its tokens with it.
func TestDeletingAnAccountRevokesItsTokens(t *testing.T) {
	s, _ := testStore(t)
	ada, err := s.Create("ada", "a long enough password", false)
	if err != nil {
		t.Fatal(err)
	}
	secret, _, err := s.CreateToken(ada.ID, "laptop")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Delete("ada"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AuthenticateToken(secret); !errors.Is(err, ErrInvalidToken) {
		t.Errorf("a deleted account's token: %v, want ErrInvalidToken", err)
	}
}
