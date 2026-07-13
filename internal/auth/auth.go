// Package auth provides authentication helpers for admin and downstream access.
package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"

	"github.com/lan/meta-gateway/internal/store"
)

// AdminMiddleware returns an HTTP middleware that requires a valid ADMIN_TOKEN Bearer token.
func AdminMiddleware(adminToken string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token, err := extractBearer(r)
			if err != nil || !secureCompare(token, adminToken) {
				http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// DownstreamAuth authenticates /v1/* requests against downstream_keys.
type DownstreamAuth struct {
	store *store.DownstreamKeyStore
}

func NewDownstreamAuth(s *store.DownstreamKeyStore) *DownstreamAuth {
	return &DownstreamAuth{store: s}
}

// Authenticate extracts the Bearer token, hashes it, and looks up in the DB.
// Returns the key ID and nil if valid, or an error.
func (da *DownstreamAuth) Authenticate(r *http.Request) (int64, error) {
	token, err := extractBearer(r)
	if err != nil {
		return 0, fmt.Errorf("auth: %w", err)
	}
	hash := hashToken(token)
	key, err := da.store.GetByHash(hash)
	if err != nil {
		return 0, fmt.Errorf("auth: lookup: %w", err)
	}
	if key == nil || !key.Enabled {
		return 0, fmt.Errorf("auth: invalid or disabled key")
	}
	return key.ID, nil
}

// Middleware returns an HTTP middleware that uses DownstreamAuth.
func (da *DownstreamAuth) Middleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if _, err := da.Authenticate(r); err != nil {
				http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// NewToken generates a random token and returns its hash and raw form.
func NewToken() (hash, raw string, err error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", "", fmt.Errorf("auth: rand: %w", err)
	}
	raw = "mg-" + hex.EncodeToString(b)
	hash = hashToken(raw)
	return hash, raw, nil
}

// HashToken creates a SHA-256 hex digest of the token.
func hashToken(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}

func extractBearer(r *http.Request) (string, error) {
	auth := r.Header.Get("Authorization")
	if auth == "" {
		return "", fmt.Errorf("missing Authorization header")
	}
	parts := strings.SplitN(auth, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return "", fmt.Errorf("invalid Authorization header format")
	}
	return parts[1], nil
}

func secureCompare(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
