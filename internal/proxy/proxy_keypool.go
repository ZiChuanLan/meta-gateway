// Package proxy orchestrates routing, retries, upstream relay, and attempt logs.
package proxy

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strings"

	"github.com/lan/meta-gateway/internal/domain"
)

// resolveAPIKeyPool builds the ordered list of plaintext API keys for a channel.
//
// The pool is the site's enabled pool-capable credentials — bound credential
// included — ordered by priority: keys sharing a priority form a tier, tiers
// are exhausted top-down, and inside a tier the starting key rotates
// (round-robin) so equal-priority keys share the traffic instead of pinning it
// on the first one. With key-pool rotation disabled, only the bound key (or the
// first pool key) is used.
//
// Credentials that cannot serve the requested model are skipped: a models_csv
// allowlist that does not cover it, or — when no explicit allowlist exists — a
// key whose recorded discovery set never listed it (empty model = no
// filtering). Keys are only excluded through those filters and their own
// status/kind; there is no per-key failure bookkeeping in the relay path, so a
// key that is enabled but broken upstream stays in the pool (mark it disabled,
// or demote it to the backup tier, to take it out of rotation).
//
// The discovered-set hint is best effort: when NOTHING claims the model (a
// renamed/aliased/custom name no key ever listed), the pool fails open with
// the model-blind selection — the explicit models_csv allowlists still apply.
// A wrong-group key just draws a missable 404 upstream and failover moves on;
// a hard credential error would misreport a naming problem as an auth one.
func (s *Service) resolveAPIKeyPool(channel domain.Channel, model string) ([]string, error) {
	keys, err := s.filterAPIKeyPool(channel, model)
	if len(keys) > 0 {
		return keys, nil
	}
	if model != "" {
		// Nothing claimed the model: fail open with the model-blind pool.
		keys, err = s.filterAPIKeyPool(channel, "")
		if len(keys) > 0 {
			return keys, nil
		}
	}
	if err == nil {
		err = ErrCredential
	}
	return nil, err
}

func (s *Service) filterAPIKeyPool(channel domain.Channel, model string) ([]string, error) {
	seen := make(map[int64]struct{})
	var usable []domain.Credential
	// Per-credential discovered model sets for the channel's site. nil means
	// "no sets loaded" (cache hit on empty site or lookup error): the naive
	// allowlist filter stays authoritative.
	var modelSets map[int64]map[string]struct{}
	if channel.SiteID != nil {
		modelSets, _ = s.db.Credential.ModelSetsBySite(*channel.SiteID)
	}

	appendCredential := func(credential *domain.Credential) {
		if credential == nil {
			return
		}
		if _, exists := seen[credential.ID]; exists {
			return
		}
		if credential.Status != domain.StatusEnabled || len(credential.SecretEnc) == 0 {
			return
		}
		// Bearer-style kinds only: api_key plus session/access_token (the
		// latter can 401 → refresh-retry through the check-in machinery).
		kind := strings.ToLower(strings.TrimSpace(credential.Kind))
		if kind != "api_key" && kind != "session" && kind != "access_token" {
			return
		}
		if !modelAllowedByKey(model, credential.ModelsCSV) {
			return
		}
		// A key without an explicit allowlist is only trusted for models it
		// has actually listed. modelSets is keyed by credential id; a missing
		// entry means no successful snapshot recorded sets for this key, so it
		// stays usable (the pool fallback preserves current behaviour).
		if strings.TrimSpace(credential.ModelsCSV) == "" && model != "" {
			if set, ok := modelSets[credential.ID]; ok && len(set) > 0 {
				if _, serves := set[model]; !serves {
					return
				}
			}
		}
		seen[credential.ID] = struct{}{}
		usable = append(usable, *credential)
	}

	if !s.keyPoolRotation.Load() {
		// Rotation off: never rotate through the pool — bound key first, or
		// the first enabled pool key as a fallback.
		if channel.CredentialID != nil {
			bound, err := s.db.Credential.GetByID(*channel.CredentialID)
			if err == nil {
				appendCredential(bound)
			}
		} else if channel.SiteID != nil {
			pool, err := s.db.Credential.ListEnabledAPIKeysBySite(*channel.SiteID)
			if err == nil && len(pool) > 0 {
				appendCredential(&pool[0])
			}
		}
		return s.orderAndDecryptPool(usable)
	}

	if channel.CredentialID != nil {
		bound, err := s.db.Credential.GetByID(*channel.CredentialID)
		if err == nil {
			appendCredential(bound)
		}
	}
	if channel.SiteID != nil {
		pool, err := s.db.Credential.ListEnabledAPIKeysBySite(*channel.SiteID)
		if err != nil && len(usable) == 0 {
			return nil, ErrCredential
		}
		for index := range pool {
			appendCredential(&pool[index])
		}
	}
	return s.orderAndDecryptPool(usable)
}

// orderAndDecryptPool turns the usable credentials into the final try order:
// tiers by priority (highest first), rotating the starting key inside each tier
// so equal-priority keys share the load. Credentials whose secret cannot be
// decrypted are dropped silently — that is local key material, not an upstream
// signal — and an empty result is reported as an unavailable credential.
func (s *Service) orderAndDecryptPool(usable []domain.Credential) ([]string, error) {
	sort.SliceStable(usable, func(i, j int) bool {
		if usable[i].Priority != usable[j].Priority {
			return usable[i].Priority > usable[j].Priority
		}
		return usable[i].ID < usable[j].ID
	})
	// One advance per resolution; every tier of this pool shares the offset.
	offset := s.keyPoolCursor.Add(1) - 1
	keys := make([]string, 0, len(usable))
	for start := 0; start < len(usable); {
		end := start
		for end < len(usable) && usable[end].Priority == usable[start].Priority {
			end++
		}
		tier := usable[start:end]
		shift := 0
		if len(tier) > 1 {
			shift = int(offset % uint64(len(tier)))
		}
		for index := range tier {
			credential := tier[(shift+index)%len(tier)]
			plaintext, err := s.enc.Decrypt(string(credential.SecretEnc))
			if err != nil || len(plaintext) == 0 {
				continue
			}
			keys = append(keys, string(plaintext))
		}
		start = end
	}
	if len(keys) == 0 {
		return nil, ErrCredential
	}
	return keys, nil
}

// modelAllowedByKey reports whether a key's model allowlist (models_csv)
// permits serving the model. Empty allowlist = all models; entries support
// "*" suffix wildcards ("gpt-4*" matches "gpt-4o"). An empty model skips
// filtering entirely.
func modelAllowedByKey(model, modelsCSV string) bool {
	if strings.TrimSpace(model) == "" || strings.TrimSpace(modelsCSV) == "" {
		return true
	}
	for _, part := range strings.Split(modelsCSV, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if strings.HasSuffix(part, "*") {
			if strings.HasPrefix(model, strings.TrimSuffix(part, "*")) {
				return true
			}
		} else if part == model {
			return true
		}
	}
	return false
}

// keyFingerprint hashes an upstream api key so the in-memory failure tables
// never hold (or log) the plaintext secret.
func keyFingerprint(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:8])
}
