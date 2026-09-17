package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"time"
)

var ErrInvalidToken = errors.New("invalid API token")

// Token is what a listing may show: never the secret, which exists only in the
// response to the request that created it.
type Token struct {
	ID         int64     `json:"id"`
	Name       string    `json:"name"`
	CreatedAt  time.Time `json:"created_at"`
	LastUsedAt time.Time `json:"last_used_at,omitzero"`
}

// tokenBytes of entropy. Guessing is not a threat model at 256 bits; the point
// of storing only the hash is that a leaked index does not leak usable tokens.
const tokenBytes = 32

// CreateToken returns the secret once. Only its hash is stored, and lookup is
// by that hash, so verification is an indexed equality test rather than a
// comparison over a set of secrets.
func (s *Store) CreateToken(userID int64, name string) (secret string, t Token, err error) {
	if name == "" {
		return "", Token{}, errors.New("token name must not be empty")
	}
	secret, err = newSecret()
	if err != nil {
		return "", Token{}, err
	}

	created := time.Now()
	res, err := s.db.Exec(`INSERT INTO api_tokens (user_id, name, token_hash, created_at) VALUES (?, ?, ?, ?)`,
		userID, name, hashToken(secret), created.Unix())
	if err != nil {
		return "", Token{}, fmt.Errorf("creating token: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return "", Token{}, fmt.Errorf("creating token: %w", err)
	}
	return secret, Token{ID: id, Name: name, CreatedAt: created.Truncate(time.Second)}, nil
}

func (s *Store) ListTokens(userID int64) ([]Token, error) {
	rs, err := s.db.Query(`SELECT id, name, created_at, last_used_at FROM api_tokens
	                       WHERE user_id = ? ORDER BY created_at, id`, userID)
	if err != nil {
		return nil, fmt.Errorf("listing tokens: %w", err)
	}
	defer rs.Close()

	tokens := []Token{} // an empty list, never a null, so clients can iterate it
	for rs.Next() {
		var (
			t        Token
			created  int64
			lastUsed sql.NullInt64
		)
		if err := rs.Scan(&t.ID, &t.Name, &created, &lastUsed); err != nil {
			return nil, fmt.Errorf("listing tokens: %w", err)
		}
		t.CreatedAt = time.Unix(created, 0)
		if lastUsed.Valid {
			t.LastUsedAt = time.Unix(lastUsed.Int64, 0)
		}
		tokens = append(tokens, t)
	}
	return tokens, rs.Err()
}

// RevokeToken deletes one token, scoped to its owner so an id from another
// account is a miss rather than a cross-account revoke.
func (s *Store) RevokeToken(userID, id int64) error {
	return s.affectOne(`DELETE FROM api_tokens WHERE id = ? AND user_id = ?`, id, userID)
}

// AuthenticateToken resolves a secret to its owner. A token carries no scope of
// its own: it authenticates as the user and gets exactly that user's access,
// which is why it can never reach outside their storage root.
func (s *Store) AuthenticateToken(secret string) (*User, error) {
	var (
		u  User
		id int64
	)
	err := s.db.QueryRow(`SELECT t.id, u.id, u.username, u.is_admin, u.disabled, u.storage_root
	                      FROM api_tokens t JOIN users u ON u.id = t.user_id
	                      WHERE t.token_hash = ? AND u.disabled = 0`, hashToken(secret)).
		Scan(&id, &u.ID, &u.Username, &u.IsAdmin, &u.Disabled, &u.StorageRoot)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return nil, ErrInvalidToken
	case err != nil:
		return nil, fmt.Errorf("looking up token: %w", err)
	}
	// Best effort: a token that works must not stop working because the audit
	// column could not be written.
	s.db.Exec(`UPDATE api_tokens SET last_used_at = ? WHERE id = ?`, time.Now().Unix(), id)
	return &u, nil
}

// newSecret is the shape every opaque secret here takes: 256 random bits in a
// form that survives a URL and a copy-paste out of a terminal.
func newSecret() (string, error) {
	b := make([]byte, tokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generating a secret: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// hashToken is a plain SHA-256: the secret is 256 random bits, so there is
// nothing to brute-force and no reason to pay Argon2id on every API request.
func hashToken(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}
