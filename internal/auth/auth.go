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

	"github.com/lan/meta-gateway/internal/store"
)

type downstreamKeyIDContextKey struct{}
type downstreamScopesContextKey struct{}
type downstreamModelFilterContextKey struct{}

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

// ModelFilter restricts which models a downstream key may relay.
// A nil filter (or one with an empty Allowlist and empty Denylist) means
// "no model restriction".
type ModelFilter struct {
	// Allowlist, when non-empty, is the only set of models the key may use.
	Allowlist []string
	// Denylist, when non-empty, blocks these models even if allowlisted.
	Denylist []string
}

// ParseModelFilter splits comma-separated model lists into a ModelFilter.
// Names are trimmed and de-duplicated; empty entries are ignored.
func ParseModelFilter(allowlistCSV, denylistCSV string) *ModelFilter {
	return &ModelFilter{
		Allowlist: splitModels(allowlistCSV),
		Denylist:  splitModels(denylistCSV),
	}
}

// Allows reports whether the given model may be relayed under this filter.
// A nil filter allows everything.
func (f *ModelFilter) Allows(model string) bool {
	if f == nil {
		return true
	}
	model = strings.TrimSpace(model)
	if model == "" {
		return true
	}
	if len(f.Allowlist) > 0 && !containsModel(f.Allowlist, model) {
		return false
	}
	if containsModel(f.Denylist, model) {
		return false
	}
	return true
}

// Empty reports whether the filter imposes no restriction at all.
func (f *ModelFilter) Empty() bool {
	return f == nil || (len(f.Allowlist) == 0 && len(f.Denylist) == 0)
}

// DownstreamModelFilter returns the model filter attached by DownstreamAuth.
// A nil value means the key has no model restriction.
func DownstreamModelFilter(r *http.Request) *ModelFilter {
	filter, _ := r.Context().Value(downstreamModelFilterContextKey{}).(*ModelFilter)
	return filter
}

func splitModels(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	seen := make(map[string]struct{})
	var out []string
	for _, part := range strings.Split(raw, ",") {
		model := strings.TrimSpace(part)
		if model == "" {
			continue
		}
		if _, exists := seen[model]; exists {
			continue
		}
		seen[model] = struct{}{}
		out = append(out, model)
	}
	return out
}

func containsModel(list []string, model string) bool {
	for _, entry := range list {
		if entry == model {
			return true
		}
	}
	return false
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
// Returns key id, normalized scopes, model filter, and nil if valid.
// The model filter is nil when the key has no model restriction.
func (da *DownstreamAuth) Authenticate(r *http.Request) (int64, []string, *ModelFilter, error) {
	token, err := extractBearer(r)
	if err != nil {
		return 0, nil, nil, fmt.Errorf("auth: %w", err)
	}
	hash := hashToken(token)
	key, err := da.store.GetByHash(hash)
	if err != nil {
		return 0, nil, nil, fmt.Errorf("auth: lookup: %w", err)
	}
	if key == nil || !key.Enabled {
		return 0, nil, nil, fmt.Errorf("auth: invalid or disabled key")
	}
	scopes, err := NormalizeScopes(key.Scopes)
	if err != nil {
		// Corrupt historical scopes still allow full relay so ops can fix the row.
		scopes = []string{ScopeRelay}
	}
	filter := ParseModelFilter(key.ModelAllowlist, key.ModelDenylist)
	if filter.Empty() {
		filter = nil
	}
	return key.ID, scopes, filter, nil
}

// Middleware returns an HTTP middleware that uses DownstreamAuth.
func (da *DownstreamAuth) Middleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id, scopes, filter, err := da.Authenticate(r)
			if err != nil {
				http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
				return
			}
			ctx := context.WithValue(r.Context(), downstreamKeyIDContextKey{}, id)
			ctx = context.WithValue(ctx, downstreamScopesContextKey{}, scopes)
			if filter != nil {
				ctx = context.WithValue(ctx, downstreamModelFilterContextKey{}, filter)
			}
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
