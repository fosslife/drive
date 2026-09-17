package auth

import (
	"database/sql"
	"errors"
	"fmt"
)

// ErrSetupUnavailable covers a wrong token and a flow that has already been
// completed. They are one answer on purpose: which it was tells a stranger
// whether this instance still has no administrator.
var ErrSetupUnavailable = errors.New("setup is not available")

const setupTokenKey = "setup_token_hash"

// OpenSetup is called once at startup. With no account it opens the first-run
// flow and returns a fresh one-time token to print; with an account it closes
// the flow and returns "".
//
// The token is regenerated on every start rather than persisted in the clear,
// so the only live token is the one in the most recent output and a restart
// costs an operator a scroll, not a lockout.
func (s *Store) OpenSetup() (string, error) {
	var accounts int
	if err := s.db.QueryRow(`SELECT count(*) FROM users`).Scan(&accounts); err != nil {
		return "", fmt.Errorf("counting accounts: %w", err)
	}
	if accounts > 0 {
		// Belt and braces: CompleteSetup already removed it, but an index
		// carried over from a crash must not keep a live token.
		if _, err := s.db.Exec(`DELETE FROM settings WHERE key = ?`, setupTokenKey); err != nil {
			return "", fmt.Errorf("closing setup: %w", err)
		}
		return "", nil
	}

	secret, err := newSecret()
	if err != nil {
		return "", err
	}
	if _, err := s.db.Exec(`INSERT INTO settings (key, value) VALUES (?, ?)
	                        ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		setupTokenKey, hashToken(secret)); err != nil {
		return "", fmt.Errorf("opening setup: %w", err)
	}
	return secret, nil
}

// CheckSetupToken reports whether the flow is open to the holder of token. It
// consumes nothing, so opening the setup page does not burn the token.
func (s *Store) CheckSetupToken(token string) error {
	var stored string
	err := s.db.QueryRow(`SELECT value FROM settings WHERE key = ?`, setupTokenKey).Scan(&stored)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return ErrSetupUnavailable
	case err != nil:
		return fmt.Errorf("reading setup state: %w", err)
	case stored != hashToken(token):
		return ErrSetupUnavailable
	}
	return nil
}

// CompleteSetup creates the first administrator and closes the flow.
//
// The token is consumed by a conditional DELETE, so two requests racing with
// the same token produce one administrator: the loser deletes no row and stops.
// Input is validated before that point, so a typo'd username costs a retry
// rather than the token.
func (s *Store) CompleteSetup(token, username, password string) (*User, error) {
	if err := s.CheckSetupToken(token); err != nil {
		return nil, err
	}
	if err := ValidUsername(username); err != nil {
		return nil, err
	}
	if password == "" {
		return nil, errors.New("password must not be empty")
	}

	hash := hashToken(token)
	res, err := s.db.Exec(`DELETE FROM settings WHERE key = ? AND value = ?`, setupTokenKey, hash)
	if err != nil {
		return nil, fmt.Errorf("consuming the setup token: %w", err)
	}
	if n, err := res.RowsAffected(); err != nil || n != 1 {
		return nil, ErrSetupUnavailable
	}

	u, err := s.Create(username, password, true)
	if err != nil {
		// The account was not created, so leaving the flow closed would lock the
		// operator out of their own instance until they restarted it.
		s.db.Exec(`INSERT INTO settings (key, value) VALUES (?, ?)`, setupTokenKey, hash)
		return nil, err
	}
	return u, nil
}
