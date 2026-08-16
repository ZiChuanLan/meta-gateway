package httpapi

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/lan/meta-gateway/internal/auth"
	"github.com/lan/meta-gateway/internal/domain"
)

func (h *AdminHandler) listDownstreamKeys(w http.ResponseWriter, r *http.Request) {
	keys, err := h.db.DownstreamKey.List()
	if err != nil {
		writeStoreError(w, err)
		return
	}
	// Never expose token_hash.
	type safeKey struct {
		ID                   int64   `json:"id"`
		Name                 string  `json:"name"`
		Enabled              bool    `json:"enabled"`
		Scopes               string  `json:"scopes,omitempty"`
		QuotaTotalTokens     int64   `json:"quota_total_tokens"`
		QuotaUsedTokens      int64   `json:"quota_used_tokens"`
		PricePromptPer1k     float64 `json:"price_prompt_per_1k"`
		PriceCompletionPer1k float64 `json:"price_completion_per_1k"`
		PriceCachePer1k      float64 `json:"price_cache_per_1k"`
		ModelAllowlist       string  `json:"model_allowlist,omitempty"`
		ModelDenylist        string  `json:"model_denylist,omitempty"`
		ExpiresAt            string  `json:"expires_at,omitempty"`
		AllowedIPs           string  `json:"allowed_ips,omitempty"`
		EstimatedCost        float64 `json:"estimated_cost"`
		CreatedAt            string  `json:"created_at"`
		HasToken             bool    `json:"has_token"`
	}
	result := make([]safeKey, 0, len(keys))
	for _, k := range keys {
		// Cost estimate uses used tokens split is unknown at key level; charge all used as prompt-equivalent mixed average.
		estimated := 0.0
		if k.QuotaUsedTokens > 0 {
			// Prefer average of prompt/completion unit prices when both set; else whichever is set.
			unit := 0.0
			if k.PricePromptPer1k > 0 && k.PriceCompletionPer1k > 0 {
				unit = (k.PricePromptPer1k + k.PriceCompletionPer1k) / 2
			} else if k.PricePromptPer1k > 0 {
				unit = k.PricePromptPer1k
			} else {
				unit = k.PriceCompletionPer1k
			}
			if unit > 0 {
				estimated = (float64(k.QuotaUsedTokens) / 1000.0) * unit
			}
		}
		// Re-viewable plaintext is available only for keys created after
		// plaintext storage landed (token_enc set).
		hasToken := len(k.TokenEnc) > 0
		result = append(result, safeKey{
			ID:                   k.ID,
			Name:                 k.Name,
			Enabled:              k.Enabled,
			Scopes:               k.Scopes,
			QuotaTotalTokens:     k.QuotaTotalTokens,
			QuotaUsedTokens:      k.QuotaUsedTokens,
			PricePromptPer1k:     k.PricePromptPer1k,
			PriceCompletionPer1k: k.PriceCompletionPer1k,
			PriceCachePer1k:      k.PriceCachePer1k,
			ModelAllowlist:       k.ModelAllowlist,
			ModelDenylist:        k.ModelDenylist,
			ExpiresAt:            k.ExpiresAt,
			AllowedIPs:           k.AllowedIPs,
			EstimatedCost:        estimated,
			CreatedAt:            k.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
			HasToken:             hasToken,
		})
	}
	writeJSON(w, http.StatusOK, result)
}

type createKeyResponse struct {
	ID                   int64   `json:"id"`
	Name                 string  `json:"name"`
	Token                string  `json:"token"`
	Enabled              bool    `json:"enabled"`
	Scopes               string  `json:"scopes,omitempty"`
	QuotaTotalTokens     int64   `json:"quota_total_tokens"`
	QuotaUsedTokens      int64   `json:"quota_used_tokens"`
	PricePromptPer1k     float64 `json:"price_prompt_per_1k"`
	PriceCompletionPer1k float64 `json:"price_completion_per_1k"`
	PriceCachePer1k      float64 `json:"price_cache_per_1k"`
	ModelAllowlist       string  `json:"model_allowlist,omitempty"`
	ModelDenylist        string  `json:"model_denylist,omitempty"`
	ExpiresAt            string  `json:"expires_at,omitempty"`
	AllowedIPs           string  `json:"allowed_ips,omitempty"`
	CreatedAt            string  `json:"created_at"`
}

func (h *AdminHandler) createDownstreamKey(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
		// Token is optional. When empty, the server generates an mg-… secret.
		// When set, the provided secret is stored as a hash only (raw never re-readable).
		Token                string  `json:"token,omitempty"`
		Scopes               string  `json:"scopes,omitempty"`
		QuotaTotalTokens     int64   `json:"quota_total_tokens"`
		PricePromptPer1k     float64 `json:"price_prompt_per_1k"`
		PriceCompletionPer1k float64 `json:"price_completion_per_1k"`
		PriceCachePer1k      float64 `json:"price_cache_per_1k"`
		GroupName            string  `json:"group_name,omitempty"`
		ModelAllowlist       string  `json:"model_allowlist,omitempty"`
		ModelDenylist        string  `json:"model_denylist,omitempty"`
		ExpiresAt            string  `json:"expires_at,omitempty"`
		AllowedIPs           string  `json:"allowed_ips,omitempty"`
	}
	if err := decodeJSON(w, r, &req, 0, false); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	normalizedScopes, scopeErr := auth.NormalizeScopes(req.Scopes)
	if scopeErr != nil {
		writeError(w, http.StatusBadRequest, scopeErr.Error())
		return
	}
	req.Scopes = auth.FormatScopes(normalizedScopes)

	raw := auth.NormalizeDownstreamToken(req.Token)
	var hash string
	if raw == "" {
		var genErr error
		hash, raw, genErr = auth.NewToken()
		if genErr != nil {
			writeError(w, http.StatusInternalServerError, "token generation failed")
			return
		}
	} else {
		if err := validateCustomDownstreamToken(raw); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		hash = auth.HashToken(raw)
		existing, lookupErr := h.db.DownstreamKey.GetByHash(hash)
		if lookupErr != nil {
			writeStoreError(w, lookupErr)
			return
		}
		if existing != nil {
			writeError(w, http.StatusConflict, "token already exists")
			return
		}
	}

	if req.QuotaTotalTokens < 0 {
		writeError(w, http.StatusBadRequest, "quota_total_tokens must be >= 0")
		return
	}
	if req.PricePromptPer1k < 0 || req.PriceCompletionPer1k < 0 || req.PriceCachePer1k < 0 {
		writeError(w, http.StatusBadRequest, "prices must be >= 0")
		return
	}
	// Keep the encrypted plaintext token so operators can re-view/copy it
	// later (like New-API). Encrypt failure is non-fatal: the key still works,
	// it just cannot be re-viewed (users can rotate it instead).
	tokenEnc := ""
	if encToken, encErr := h.enc.Encrypt([]byte(raw)); encErr == nil {
		tokenEnc = encToken
	}
	key := &domain.DownstreamKey{
		TokenHash:            hash,
		TokenEnc:             []byte(tokenEnc),
		Name:                 req.Name,
		Enabled:              true,
		Scopes:               req.Scopes,
		QuotaTotalTokens:     req.QuotaTotalTokens,
		PricePromptPer1k:     req.PricePromptPer1k,
		PriceCompletionPer1k: req.PriceCompletionPer1k,
		PriceCachePer1k:      req.PriceCachePer1k,
		ModelAllowlist:       strings.TrimSpace(req.ModelAllowlist),
		ModelDenylist:        strings.TrimSpace(req.ModelDenylist),
		ExpiresAt:            strings.TrimSpace(req.ExpiresAt),
		AllowedIPs:           strings.TrimSpace(req.AllowedIPs),
		GroupName:            strings.TrimSpace(req.GroupName),
	}
	id, err := h.db.DownstreamKey.Create(key)
	if err != nil {
		writeStoreError(w, err)
		return
	}

	createdAt := ""
	if created, err := h.db.DownstreamKey.GetByID(id); err == nil && created != nil {
		createdAt = created.CreatedAt.Format("2006-01-02T15:04:05Z07:00")
	}

	writeJSON(w, http.StatusCreated, createKeyResponse{
		ID:                   id,
		Name:                 req.Name,
		Token:                raw,
		Enabled:              true,
		Scopes:               req.Scopes,
		QuotaTotalTokens:     req.QuotaTotalTokens,
		QuotaUsedTokens:      0,
		PricePromptPer1k:     req.PricePromptPer1k,
		PriceCompletionPer1k: req.PriceCompletionPer1k,
		PriceCachePer1k:      req.PriceCachePer1k,
		ModelAllowlist:       strings.TrimSpace(req.ModelAllowlist),
		ModelDenylist:        strings.TrimSpace(req.ModelDenylist),
		ExpiresAt:            strings.TrimSpace(req.ExpiresAt),
		AllowedIPs:           strings.TrimSpace(req.AllowedIPs),
		CreatedAt:            createdAt,
	})
}

func (h *AdminHandler) updateDownstreamKey(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	existing, err := h.db.DownstreamKey.GetByID(id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if existing == nil {
		writeError(w, http.StatusNotFound, "key not found")
		return
	}
	var req struct {
		Name                 *string  `json:"name"`
		Enabled              *bool    `json:"enabled"`
		Scopes               *string  `json:"scopes"`
		QuotaTotalTokens     *int64   `json:"quota_total_tokens"`
		PricePromptPer1k     *float64 `json:"price_prompt_per_1k"`
		PriceCompletionPer1k *float64 `json:"price_completion_per_1k"`
		PriceCachePer1k      *float64 `json:"price_cache_per_1k"`
		GroupName            *string  `json:"group_name"`
		ModelAllowlist       *string  `json:"model_allowlist"`
		ModelDenylist        *string  `json:"model_denylist"`
		ExpiresAt            *string  `json:"expires_at"`
		AllowedIPs           *string  `json:"allowed_ips"`
		ResetUsed            bool     `json:"reset_used"`
	}
	if err := decodeJSON(w, r, &req, 0, false); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if req.Name != nil {
		name := strings.TrimSpace(*req.Name)
		if name == "" {
			writeError(w, http.StatusBadRequest, "name is required")
			return
		}
		existing.Name = name
	}
	if req.Enabled != nil {
		existing.Enabled = *req.Enabled
	}
	if req.Scopes != nil {
		normalizedScopes, scopeErr := auth.NormalizeScopes(*req.Scopes)
		if scopeErr != nil {
			writeError(w, http.StatusBadRequest, scopeErr.Error())
			return
		}
		existing.Scopes = auth.FormatScopes(normalizedScopes)
	}
	if req.QuotaTotalTokens != nil {
		if *req.QuotaTotalTokens < 0 {
			writeError(w, http.StatusBadRequest, "quota_total_tokens must be >= 0")
			return
		}
		existing.QuotaTotalTokens = *req.QuotaTotalTokens
	}
	if req.PricePromptPer1k != nil {
		if *req.PricePromptPer1k < 0 {
			writeError(w, http.StatusBadRequest, "price_prompt_per_1k must be >= 0")
			return
		}
		existing.PricePromptPer1k = *req.PricePromptPer1k
	}
	if req.PriceCompletionPer1k != nil {
		if *req.PriceCompletionPer1k < 0 {
			writeError(w, http.StatusBadRequest, "price_completion_per_1k must be >= 0")
			return
		}
		existing.PriceCompletionPer1k = *req.PriceCompletionPer1k
	}
	if req.PriceCachePer1k != nil {
		if *req.PriceCachePer1k < 0 {
			writeError(w, http.StatusBadRequest, "price_cache_per_1k must be >= 0")
			return
		}
		existing.PriceCachePer1k = *req.PriceCachePer1k
	}
	if req.ModelAllowlist != nil {
		existing.ModelAllowlist = strings.TrimSpace(*req.ModelAllowlist)
	}
	if req.ModelDenylist != nil {
		existing.ModelDenylist = strings.TrimSpace(*req.ModelDenylist)
	}
	if req.ExpiresAt != nil {
		existing.ExpiresAt = strings.TrimSpace(*req.ExpiresAt)
	}
	if req.AllowedIPs != nil {
		existing.AllowedIPs = strings.TrimSpace(*req.AllowedIPs)
	}
	if req.GroupName != nil {
		existing.GroupName = strings.TrimSpace(*req.GroupName)
	}
	// Reset the usage counter before persisting field changes: a reset failure
	// then leaves the row untouched (retry is idempotent), instead of applying
	// the field update but failing half the request.
	if req.ResetUsed {
		if err := h.db.DownstreamKey.ResetUsage(id); err != nil {
			writeStoreError(w, err)
			return
		}
		existing.QuotaUsedTokens = 0
	}
	if err := h.db.DownstreamKey.Update(existing); err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id":                      existing.ID,
		"name":                    existing.Name,
		"enabled":                 existing.Enabled,
		"scopes":                  existing.Scopes,
		"quota_total_tokens":      existing.QuotaTotalTokens,
		"quota_used_tokens":       existing.QuotaUsedTokens,
		"price_prompt_per_1k":     existing.PricePromptPer1k,
		"price_completion_per_1k": existing.PriceCompletionPer1k,
		"price_cache_per_1k":      existing.PriceCachePer1k,
		"created_at":              existing.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
	})
}

func (h *AdminHandler) deleteDownstreamKey(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	if err := h.db.DownstreamKey.Delete(id); err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// revealDownstreamKeyToken returns the stored plaintext token for a key.
// Keys created before plaintext storage landed (token_enc empty) return 404
// so the operator can rotate instead. Admin audit records every reveal.
func (h *AdminHandler) revealDownstreamKeyToken(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	enc, err := h.db.DownstreamKey.GetTokenEnc(id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if enc == "" {
		writeError(w, http.StatusNotFound, "token_plaintext_unavailable")
		return
	}
	plain, decryptErr := h.enc.Decrypt(enc)
	if decryptErr != nil {
		writeError(w, http.StatusInternalServerError, "token_decrypt_failed")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]string{"token": string(plain)})
}

// rotateDownstreamKeyToken replaces a key's token with a freshly generated
// one; the old token stops working immediately. The new plaintext is returned
// once and also stored encrypted so it can be re-viewed later.
func (h *AdminHandler) rotateDownstreamKeyToken(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	existing, err := h.db.DownstreamKey.GetByID(id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if existing == nil {
		writeError(w, http.StatusNotFound, "downstream key not found")
		return
	}
	hash, raw, genErr := auth.NewToken()
	if genErr != nil {
		writeError(w, http.StatusInternalServerError, "token generation failed")
		return
	}
	tokenEnc, encErr := h.enc.Encrypt([]byte(raw))
	if encErr != nil {
		writeError(w, http.StatusInternalServerError, "encryption failed")
		return
	}
	if err := h.db.DownstreamKey.RotateToken(id, hash, tokenEnc); err != nil {
		writeStoreError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]string{"id": strconv.FormatInt(id, 10), "token": raw})
}
