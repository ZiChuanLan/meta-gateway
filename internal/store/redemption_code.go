package store

import (
	"crypto/rand"
	"encoding/base32"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ErrCodeInvalid marks an unknown, already-used or expired redemption code.
var ErrCodeInvalid = errors.New("redemption code invalid")


// RedemptionCode is a single-use quota top-up voucher for downstream keys.
type RedemptionCode struct {
	ID              int64      `json:"id"`
	Code            string     `json:"code"`
	QuotaTokens     int64      `json:"quota_tokens"`
	CreatedBy       int64      `json:"created_by"`
	CreatedAt       time.Time  `json:"created_at"`
	ExpiresAt       *time.Time `json:"expires_at,omitempty"`
	RedeemedByKeyID int64      `json:"redeemed_by_key_id"`
	RedeemedAt      *time.Time `json:"redeemed_at,omitempty"`
}

// GenerateRedemptionCode returns a short random voucher code (base32, no
// confusing chars). Prefix helps humans recognize the product.
func GenerateRedemptionCode(prefix string) string {
	raw := make([]byte, 5)
	if _, err := rand.Read(raw); err != nil {
		// Fall back to a time-seeded code; collisions are caught by UNIQUE.
		return fmt.Sprintf("%s-%d", strings.ToUpper(prefix), time.Now().UnixNano()%100000000)
	}
	enc := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(raw)
	enc = strings.NewReplacer("0", "A", "1", "B", "O", "C", "I", "D").Replace(enc)
	code := strings.ToUpper(prefix) + "-" + enc[:8]
	return code
}

// CreateRedemptionCodes mints count single-use codes each worth quotaTokens.
func (s *DB) CreateRedemptionCodes(count int, quotaTokens int64, createdBy int64, expiresAt *time.Time) ([]RedemptionCode, error) {
	if count <= 0 || quotaTokens <= 0 {
		return nil, nil
	}
	now := time.Now().UTC()
	var out []RedemptionCode
	for i := 0; i < count; i++ {
		code := RedemptionCode{
			Code:        GenerateRedemptionCode("MG"),
			QuotaTokens: quotaTokens,
			CreatedBy:   createdBy,
			CreatedAt:   now,
			ExpiresAt:   expiresAt,
		}
		if _, err := s.Exec(
			`INSERT INTO redemption_codes (code, quota_tokens, created_by, created_at, expires_at) VALUES (?, ?, ?, ?, ?)`,
			code.Code, code.QuotaTokens, code.CreatedBy, code.CreatedAt.Format(time.RFC3339Nano), nullTime(expiresAt),
		); err != nil {
			// Extremely unlikely collision — retry once with a fresh code.
			code.Code = GenerateRedemptionCode("MG")
			if _, err := s.Exec(
				`INSERT INTO redemption_codes (code, quota_tokens, created_by, created_at, expires_at) VALUES (?, ?, ?, ?, ?)`,
				code.Code, code.QuotaTokens, code.CreatedBy, code.CreatedAt.Format(time.RFC3339Nano), nullTime(expiresAt),
			); err != nil {
				return out, fmt.Errorf("redemption code insert: %w", err)
			}
		}
		out = append(out, code)
	}
	return out, nil
}

// ListRedemptionCodes returns codes newest first, optionally only unredeemed.
func (s *DB) ListRedemptionCodes(onlyUnredeemed bool) ([]RedemptionCode, error) {
	query := `SELECT id, code, quota_tokens, created_by, created_at, expires_at, redeemed_by_key_id, redeemed_at FROM redemption_codes`
	if onlyUnredeemed {
		query += ` WHERE redeemed_by_key_id = 0`
	}
	query += ` ORDER BY id DESC LIMIT 500`
	rows, err := s.Query(query)
	if err != nil {
		return nil, fmt.Errorf("redemption code list: %w", err)
	}
	defer rows.Close()
	var out []RedemptionCode
	for rows.Next() {
		var c RedemptionCode
		var created string
		var expiresPtr, redeemedPtr *string
		if err := rows.Scan(&c.ID, &c.Code, &c.QuotaTokens, &c.CreatedBy, &created, &expiresPtr, &c.RedeemedByKeyID, &redeemedPtr); err != nil {
			return nil, fmt.Errorf("redemption code scan: %w", err)
		}
		if t, err := time.Parse(time.RFC3339Nano, created); err == nil {
			c.CreatedAt = t
		}
		if expiresPtr != nil {
			if t, err := time.Parse(time.RFC3339Nano, *expiresPtr); err == nil {
				c.ExpiresAt = &t
			}
		}
		if redeemedPtr != nil {
			if t, err := time.Parse(time.RFC3339Nano, *redeemedPtr); err == nil {
				c.RedeemedAt = &t
			}
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// RedeemCode atomically marks a code used and returns its quota. Returns
// (quota, nil) on success; (0, ErrCodeInvalid) when unknown/already used/expired.
func (s *DB) RedeemCode(code string, keyID int64, now time.Time) (int64, error) {
	if strings.TrimSpace(code) == "" || keyID <= 0 {
		return 0, ErrCodeInvalid
	}
	tx, err := s.Begin()
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()

	var id, quota int64
	var expires *string
	if err := tx.QueryRow(
		`SELECT id, quota_tokens, expires_at FROM redemption_codes WHERE code = ?`,
		strings.ToUpper(strings.TrimSpace(code)),
	).Scan(&id, &quota, &expires); err != nil {
		return 0, ErrCodeInvalid
	}
	if expires != nil {
		if t, err := time.Parse(time.RFC3339Nano, *expires); err == nil && now.After(t) {
			return 0, ErrCodeInvalid
		}
	}
	var usedBy int64
	if err := tx.QueryRow(`SELECT redeemed_by_key_id FROM redemption_codes WHERE id = ?`, id).Scan(&usedBy); err != nil {
		return 0, ErrCodeInvalid
	}
	if usedBy != 0 {
		return 0, ErrCodeInvalid
	}
	res, err := tx.Exec(
		`UPDATE redemption_codes SET redeemed_by_key_id = ?, redeemed_at = ? WHERE id = ? AND redeemed_by_key_id = 0`,
		keyID, now.UTC().Format(time.RFC3339Nano), id,
	)
	if err != nil {
		return 0, err
	}
	if affected, err := res.RowsAffected(); err != nil || affected != 1 {
		return 0, ErrCodeInvalid
	}
	// Top up the key's quota inside the same transaction.
	if _, err := tx.Exec(
		`UPDATE downstream_keys SET quota_total_tokens = quota_total_tokens + ? WHERE id = ?`,
		quota, keyID,
	); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	// Drop the key's in-process cache so the next auth read sees the top-up.
	s.DownstreamKey.Invalidate(keyID)
	return quota, nil
}

// DeleteRedemptionCode removes an unredeemed code (admin void).
func (s *DB) DeleteRedemptionCode(id int64) error {
	res, err := s.Exec(`DELETE FROM redemption_codes WHERE id = ? AND redeemed_by_key_id = 0`, id)
	if err != nil {
		return fmt.Errorf("redemption code delete: %w", err)
	}
	if affected, _ := res.RowsAffected(); affected == 0 {
		return ErrCodeInvalid
	}
	return nil
}

func nullTime(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.UTC().Format(time.RFC3339Nano)
}
