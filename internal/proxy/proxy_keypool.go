// Package proxy orchestrates routing, retries, upstream relay, and attempt logs.
package proxy

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"

	"github.com/lan/meta-gateway/internal/domain"
)

// resolveAPIKeyPool builds the ordered list of plaintext API keys for a channel.
// Prefer the bound credential first, then every other enabled api_key on the same site.
// Keys that hit the per-key auto-disable threshold are excluded until they heal.
// Keys whose models_csv allowlist does not cover the requested model are skipped
// (empty model = no filtering). When models_csv is unset, a key's discovered
// model set acts as the allowlist: a key that never listed the model is skipped,
// so a group-scoped key is only used for members of its group. With key-pool
// rotation disabled, only the bound key (or the first pool key) is used.
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
	var keys []string
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
		plaintext, err := s.enc.Decrypt(string(credential.SecretEnc))
		if err != nil || len(plaintext) == 0 {
			return
		}
		seen[credential.ID] = struct{}{}
		keys = append(keys, string(plaintext))
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
		if len(keys) == 0 {
			return nil, ErrCredential
		}
		return keys, nil
	}

	if channel.CredentialID != nil {
		bound, err := s.db.Credential.GetByID(*channel.CredentialID)
		if err == nil {
			appendCredential(bound)
		}
	}
	if channel.SiteID != nil {
		pool, err := s.db.Credential.ListEnabledAPIKeysBySite(*channel.SiteID)
		if err != nil {
			if len(keys) == 0 {
				return nil, ErrCredential
			}
			return keys, nil
		}
		for index := range pool {
			appendCredential(&pool[index])
		}
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
