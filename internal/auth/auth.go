// Package auth provides authentication helpers for admin and downstream access.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"

	"github.com/lan/meta-gateway/internal/domain"
	"github.com/lan/meta-gateway/internal/store"
)

type downstreamKeyIDContextKey struct{}
type downstreamScopesContextKey struct{}

// Well-known downstream scopes. "relay" is a superset of public /v1 access.
const (
	ScopeRelay       = "relay"
	ScopeModels      = "models"
	ScopeChat        = "chat"
	ScopeCompletions = "completions"
	ScopeEmbeddings  = "embeddings"
	ScopeResponses   = "responses"
	ScopeMessages    = "messages"
	ScopeImages      = "images"
	ScopeAudio       = "audio"
	ScopeModerations = "moderations"
)

var knownScopes = map[string]struct{}{
	ScopeRelay:       {},
	ScopeModels:      {},
	ScopeChat:        {},
	ScopeCompletions: {},
	ScopeEmbeddings:  {},
	ScopeResponses:   {},
	ScopeMessages:    {},
	ScopeImages:      {},
	ScopeAudio:       {},
	ScopeModerations: {},
}

func DownstreamKeyID(r *http.Request) (int64, bool) {
	id, ok := r.Context().Value(downstreamKeyIDContextKey{}).(int64)
	return id, ok
}

// DownstreamScopes returns the normalized scope list attached by DownstreamAuth.
func DownstreamScopes(r *http.Request) []string {
	scopes, _ := r.Context().Value(downstreamScopesContextKey{}).([]string)
	return scopes
}

// NormalizeScopes validates and canonicalizes a comma/space-separated scope list.
// Empty input becomes ["relay"] for backward compatibility.
func NormalizeScopes(raw string) ([]string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return []string{ScopeRelay}, nil
	}
	parts := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == ' ' || r == ';' || r == '|'
	})
	seen := make(map[string]struct{}, len(parts))
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		scope := strings.ToLower(strings.TrimSpace(part))
		if scope == "" {
			continue
		}
		if _, ok := knownScopes[scope]; !ok {
			return nil, fmt.Errorf("unknown scope %q", scope)
		}
		if _, exists := seen[scope]; exists {
			continue
		}
		seen[scope] = struct{}{}
		out = append(out, scope)
	}
	if len(out) == 0 {
		return []string{ScopeRelay}, nil
	}
	return out, nil
}

// FormatScopes joins normalized scopes for storage.
func FormatScopes(scopes []string) string {
	return strings.Join(scopes, ",")
}

// HasScope reports whether granted scopes allow the required capability.
// The "relay" scope is a superset of all public relay endpoints.
func HasScope(granted []string, required string) bool {
	required = strings.ToLower(strings.TrimSpace(required))
	if required == "" {
		return true
	}
	for _, scope := range granted {
		scope = strings.ToLower(strings.TrimSpace(scope))
		if scope == ScopeRelay || scope == required {
			return true
		}
	}
	return false
}

// AdminMiddleware returns an HTTP middleware that requires a valid admin Bearer token.
// tokens may contain multiple rotation candidates; comparison is length-safe and
// always walks the full candidate set to reduce timing leakage.
func AdminMiddleware(tokens ...string) func(http.Handler) http.Handler {
	candidates := make([]string, 0, len(tokens))
	for _, token := range tokens {
		token = strings.TrimSpace(token)
		if token != "" {
			candidates = append(candidates, token)
		}
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token, err := extractBearer(r)
			if err != nil || !secureCompareAny(token, candidates) {
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
// Returns key id, normalized scopes, and nil if valid.
func (da *DownstreamAuth) Authenticate(r *http.Request) (int64, []string, error) {
	token, err := extractBearer(r)
	if err != nil {
		return 0, nil, fmt.Errorf("auth: %w", err)
	}
	hash := hashToken(token)
	key, err := da.store.GetByHash(hash)
	if err != nil {
		return 0, nil, fmt.Errorf("auth: lookup: %w", err)
	}
	if key == nil || !key.Enabled {
		return 0, nil, fmt.Errorf("auth: invalid or disabled key")
	}
	scopes, err := NormalizeScopes(key.Scopes)
	if err != nil {
		// Corrupt historical scopes still allow full relay so ops can fix the row.
		scopes = []string{ScopeRelay}
	}
	return key.ID, scopes, nil
}

// Middleware returns an HTTP middleware that uses DownstreamAuth.
func (da *DownstreamAuth) Middleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id, scopes, err := da.Authenticate(r)
			if err != nil {
				http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
				return
			}
			ctx := context.WithValue(r.Context(), downstreamKeyIDContextKey{}, id)
			ctx = context.WithValue(ctx, downstreamScopesContextKey{}, scopes)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// HashToken returns the SHA-256 hex digest used for downstream key storage/lookup.
func HashToken(token string) string {
	return hashToken(token)
}

// NormalizeDownstreamToken trims space; empty means “generate a random token”.
func NormalizeDownstreamToken(token string) string {
	return strings.TrimSpace(token)
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

func hashToken(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}

func extractBearer(r *http.Request) (string, error) {
	authHeader := r.Header.Get("Authorization")
	if authHeader == "" {
		return "", fmt.Errorf("missing Authorization header")
	}
	parts := strings.SplitN(authHeader, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return "", fmt.Errorf("invalid Authorization header format")
	}
	return parts[1], nil
}

// secureCompareAny compares presented against every candidate without early exit
// on the first match, and only accepts equal-length candidates for ConstantTimeCompare.
func secureCompareAny(presented string, candidates []string) bool {
	if len(candidates) == 0 {
		return false
	}
	matched := 0
	presentedBytes := []byte(presented)
	for _, candidate := range candidates {
		candidateBytes := []byte(candidate)
		if len(presentedBytes) != len(candidateBytes) {
			// Keep work roughly constant: compare against presented itself when lengths differ.
			_ = subtle.ConstantTimeCompare(presentedBytes, presentedBytes)
			continue
		}
		matched |= subtle.ConstantTimeCompare(presentedBytes, candidateBytes)
	}
	return matched == 1
}

// Ensure domain import stays available for future key helpers.
var _ = domain.DownstreamKey{}
