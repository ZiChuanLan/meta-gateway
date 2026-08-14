package store

import (
	"database/sql"
	"fmt"
	"time"
)

// AdminTOTPStore persists the admin 2FA secret (encrypted by the caller)
// and the enabled flag in a single-row table.
type AdminTOTPStore struct {
	db *sql.DB
}

// AdminTOTPState is a row snapshot.
type AdminTOTPState struct {
	SecretEncrypted string
	Enabled         bool
	UpdatedAt       time.Time
}

// Get returns the current state (never nil; empty secret when not set up).
func (s *AdminTOTPStore) Get() (*AdminTOTPState, error) {
	var secret string
	var enabled int
	var updated string
	err := s.db.QueryRow(
		`SELECT secret_encrypted, enabled, updated_at FROM admin_totp WHERE id = 1`,
	).Scan(&secret, &enabled, &updated)
	if err != nil {
		return nil, fmt.Errorf("admin totp get: %w", err)
	}
	out := &AdminTOTPState{SecretEncrypted: secret, Enabled: enabled != 0}
	if t, perr := time.Parse(time.RFC3339Nano, updated); perr == nil {
		out.UpdatedAt = t
	}
	return out, nil
}

// SetSecret stores the encrypted secret (setup step; does not enable).
func (s *AdminTOTPStore) SetSecret(encrypted string) error {
	_, err := s.db.Exec(
		`UPDATE admin_totp SET secret_encrypted = ?, updated_at = ? WHERE id = 1`,
		encrypted, time.Now().UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		return fmt.Errorf("admin totp set secret: %w", err)
	}
	return nil
}

// SetEnabled flips the enabled flag.
func (s *AdminTOTPStore) SetEnabled(enabled bool) error {
	val := 0
	if enabled {
		val = 1
	}
	_, err := s.db.Exec(
		`UPDATE admin_totp SET enabled = ?, updated_at = ? WHERE id = 1`,
		val, time.Now().UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		return fmt.Errorf("admin totp set enabled: %w", err)
	}
	return nil
}

// Clear removes the secret and disables (unbind).
func (s *AdminTOTPStore) Clear() error {
	_, err := s.db.Exec(
		`UPDATE admin_totp SET secret_encrypted = '', enabled = 0, updated_at = ? WHERE id = 1`,
		time.Now().UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		return fmt.Errorf("admin totp clear: %w", err)
	}
	return nil
}
