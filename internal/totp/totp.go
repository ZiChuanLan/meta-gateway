// Package totp implements RFC 6238 TOTP (HMAC-SHA1, 30s window) with the
// ±1-step tolerance used by Google Authenticator, plus secret generation.
package totp

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"strings"
	"time"
)

// SecretLength is the byte length of generated secrets (160 bits → 32 base32 chars).
const SecretLength = 20

// NewSecret returns a random base32 secret without padding, suitable for
// Google Authenticator / otpauth URIs.
func NewSecret() (string, error) {
	raw := make([]byte, SecretLength)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("totp: random: %w", err)
	}
	return base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(raw), nil
}

// Code computes the TOTP code for secret at the given time (RFC 6238,
// SHA1, 6 digits, 30s step).
func Code(secret string, at time.Time) (string, error) {
	key, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(strings.ToUpper(strings.TrimSpace(secret)))
	if err != nil {
		// Also accept padded input from manual entry.
		if key, err = base32.StdEncoding.DecodeString(strings.ToUpper(strings.TrimSpace(secret))); err != nil {
			return "", fmt.Errorf("totp: secret decode: %w", err)
		}
	}
	counter := uint64(at.Unix() / 30)
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], counter)
	mac := hmac.New(sha1.New, key)
	mac.Write(msg[:])
	sum := mac.Sum(nil)
	offset := sum[len(sum)-1] & 0x0f
	value := (uint32(sum[offset])&0x7f)<<24 |
		uint32(sum[offset+1])<<16 |
		uint32(sum[offset+2])<<8 |
		uint32(sum[offset+3])
	return fmt.Sprintf("%06d", value%1_000_000), nil
}

// Verify checks code against secret with a ±1 step tolerance (window of 90s).
func Verify(secret, code string, at time.Time) bool {
	code = strings.TrimSpace(code)
	if code == "" {
		return false
	}
	for _, offset := range []int64{0, -1, 1} {
		if candidate, err := Code(secret, at.Add(time.Duration(offset)*30*time.Second)); err == nil &&
			hmac.Equal([]byte(candidate), []byte(code)) {
			return true
		}
	}
	return false
}

// URI builds an otpauth://totp URI for QR rendering (issuer + account).
func URI(issuer, account, secret string) string {
	label := issuer + ":" + account
	return fmt.Sprintf("otpauth://totp/%s?secret=%s&issuer=%s&algorithm=SHA1&digits=6&period=30",
		label, secret, issuer)
}
