package httpapi

import (
	"net/http"
	"sort"
	"strings"

	"github.com/lan/meta-gateway/internal/domain"
)

func (h *AdminHandler) listCredentials(w http.ResponseWriter, r *http.Request) {
	siteID, ok := pathID(w, r, "siteId")
	if !ok {
		return
	}
	creds, err := h.db.Credential.ListBySite(siteID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	// Discovered model sets per key, used to show which models each key
	// actually lists upstream (group-scoped keys differ).
	modelSets, modelSetsErr := h.db.Credential.ModelSetsBySite(siteID)
	if modelSetsErr != nil {
		writeStoreError(w, modelSetsErr)
		return
	}
	// Never expose secret_enc in JSON responses.
	type safeCred struct {
		ID             int64  `json:"id"`
		SiteID         int64  `json:"site_id"`
		Kind           string `json:"kind"`
		AuthMode       string `json:"auth_mode,omitempty"`
		HasSecret      bool   `json:"has_secret"`
		HasCookie      bool   `json:"has_cookie"`
		MetaJSON       string `json:"meta_json,omitempty"`
		Status         string `json:"status"`
		CheckinEnabled bool   `json:"checkin_enabled"`
		ModelsCSV      string `json:"models_csv,omitempty"`
		// ModelCount is how many distinct models this key listed in the latest
		// successful discovery snapshot. -1 when the site has no snapshot yet.
		ModelCount int `json:"model_count"`
		// Models are the model names from that same snapshot, so a console can
		// offer this key's own vocabulary instead of the channel-wide union
		// (an explicit models_csv allowlist overrides the per-key filter, so a
		// picker fed from the union invites unusable selections).
		Models []string `json:"models,omitempty"`
		// Priority is the key's tier in the site relay pool (higher first;
		// equal priorities rotate).
		Priority int `json:"priority"`
	}
	result := make([]safeCred, 0, len(creds))
	for _, c := range creds {
		modelCount := -1
		var models []string
		if set, ok := modelSets[c.ID]; ok {
			modelCount = len(set)
			models = make([]string, 0, len(set))
			for name := range set {
				models = append(models, name)
			}
			sort.Strings(models)
		}
		result = append(result, safeCred{
			ID:             c.ID,
			SiteID:         c.SiteID,
			Kind:           c.Kind,
			AuthMode:       normalizeCredentialAuthMode(c.AuthMode),
			HasSecret:      len(c.SecretEnc) > 0,
			HasCookie:      len(c.CookieEnc) > 0,
			MetaJSON:       c.MetaJSON,
			Status:         c.Status,
			CheckinEnabled: c.CheckinEnabled,
			ModelsCSV:      c.ModelsCSV,
			ModelCount:     modelCount,
			Models:         models,
			Priority:       c.Priority,
		})
	}
	writeJSON(w, http.StatusOK, result)
}

type createCredentialRequest struct {
	Kind     string `json:"kind"`
	AuthMode string `json:"auth_mode,omitempty"`
	Secret   string `json:"secret,omitempty"`
	Cookie   string `json:"cookie,omitempty"`
	MetaJSON string `json:"meta_json,omitempty"`
	Status   string `json:"status,omitempty"`
	// ModelsCSV is the per-key model allowlist (comma-separated; empty = all).
	ModelsCSV string `json:"models_csv,omitempty"`
	// Priority is the key's tier in the site relay pool; omitted = balanced (0).
	Priority *int `json:"priority,omitempty"`
}

func (h *AdminHandler) createCredential(w http.ResponseWriter, r *http.Request) {
	siteID, ok := pathID(w, r, "siteId")
	if !ok {
		return
	}
	var req createCredentialRequest
	if err := decodeJSON(w, r, &req, 0, false); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	authMode := normalizeCredentialAuthMode(req.AuthMode)
	if req.Secret == "" && req.Cookie == "" {
		writeError(w, http.StatusBadRequest, "secret or cookie is required")
		return
	}
	var encSecret, encCookie string
	var err error
	if req.Secret != "" {
		encSecret, err = h.enc.Encrypt([]byte(req.Secret))
		if err != nil {
			writeError(w, http.StatusInternalServerError, "encryption failed")
			return
		}
	}
	if req.Cookie != "" {
		encCookie, err = h.enc.Encrypt([]byte(req.Cookie))
		if err != nil {
			writeError(w, http.StatusInternalServerError, "encryption failed")
			return
		}
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "encryption failed")
		return
	}
	if req.Status == "" {
		req.Status = domain.StatusEnabled
	}
	if !validCredentialStatus(req.Status) {
		writeError(w, http.StatusBadRequest, "status must be enabled or disabled")
		return
	}
	metaJSON, metaErr := normalizeCredentialMeta(req.MetaJSON)
	if metaErr != nil {
		writeError(w, http.StatusBadRequest, metaErr.Error())
		return
	}
	priority := domain.CredentialPriorityBalanced
	if req.Priority != nil {
		if !domain.ValidCredentialPriority(*req.Priority) {
			writeError(w, http.StatusBadRequest, "priority must be between -100 and 100")
			return
		}
		priority = *req.Priority
	}
	cred := &domain.Credential{
		SiteID:    siteID,
		Kind:      req.Kind,
		AuthMode:  authMode,
		SecretEnc: []byte(encSecret),
		CookieEnc: []byte(encCookie),
		MetaJSON:  metaJSON,
		Status:    req.Status,
		ModelsCSV: req.ModelsCSV,
		Priority:  priority,
	}
	id, err := h.db.Credential.Create(cred)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	created, err := h.db.Credential.GetByID(id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if created == nil {
		writeError(w, http.StatusInternalServerError, "credential vanished after create")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]interface{}{
		"id":              created.ID,
		"site_id":         created.SiteID,
		"kind":            created.Kind,
		"auth_mode":       normalizeCredentialAuthMode(created.AuthMode),
		"has_secret":      len(created.SecretEnc) > 0,
		"has_cookie":      len(created.CookieEnc) > 0,
		"meta_json":       created.MetaJSON,
		"status":          created.Status,
		"checkin_enabled": created.CheckinEnabled,
		"models_csv":      created.ModelsCSV,
		"priority":        created.Priority,
		"created_at":      created.CreatedAt,
	})
}

type updateCredentialRequest struct {
	Kind        string `json:"kind,omitempty"`
	AuthMode    string `json:"auth_mode,omitempty"`
	Secret      string `json:"secret,omitempty"` // empty keeps existing secret
	Cookie      string `json:"cookie,omitempty"` // empty keeps existing cookie
	ClearSecret bool   `json:"clear_secret,omitempty"`
	ClearCookie bool   `json:"clear_cookie,omitempty"`
	MetaJSON    string `json:"meta_json,omitempty"`
	Status      string `json:"status,omitempty"`
	// ModelsCSV is the per-key model allowlist; nil keeps the existing value.
	ModelsCSV *string `json:"models_csv,omitempty"`
	// Priority is the key's tier in the site relay pool; nil keeps the existing
	// value. Zero is a real tier (balanced), so this must stay a pointer.
	Priority *int `json:"priority,omitempty"`
}

func (h *AdminHandler) updateCredential(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	existing, err := h.db.Credential.GetByID(id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if existing == nil {
		writeError(w, http.StatusNotFound, "credential not found")
		return
	}
	var req updateCredentialRequest
	if err := decodeJSON(w, r, &req, 0, false); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if req.Kind != "" {
		existing.Kind = req.Kind
	}
	if req.AuthMode != "" {
		existing.AuthMode = normalizeCredentialAuthMode(req.AuthMode)
	}
	if req.Status != "" {
		if !validCredentialStatus(req.Status) {
			writeError(w, http.StatusBadRequest, "status must be enabled or disabled")
			return
		}
		existing.Status = req.Status
	}
	if req.MetaJSON != "" {
		metaJSON, metaErr := normalizeCredentialMeta(req.MetaJSON)
		if metaErr != nil {
			writeError(w, http.StatusBadRequest, metaErr.Error())
			return
		}
		existing.MetaJSON = metaJSON
	}
	if req.ModelsCSV != nil {
		existing.ModelsCSV = strings.TrimSpace(*req.ModelsCSV)
	}
	if req.Priority != nil {
		if !domain.ValidCredentialPriority(*req.Priority) {
			writeError(w, http.StatusBadRequest, "priority must be between -100 and 100")
			return
		}
		existing.Priority = *req.Priority
	}
	if req.ClearSecret {
		existing.SecretEnc = nil
		existing.ImportFingerprint = ""
	}
	if strings.TrimSpace(req.Secret) != "" {
		encSecret, err := h.enc.Encrypt([]byte(req.Secret))
		if err != nil {
			writeError(w, http.StatusInternalServerError, "encryption failed")
			return
		}
		existing.SecretEnc = []byte(encSecret)
		// Replacing secret invalidates any import fingerprint identity.
		existing.ImportFingerprint = ""
	}
	if req.ClearCookie {
		existing.CookieEnc = nil
	}
	if strings.TrimSpace(req.Cookie) != "" {
		encCookie, err := h.enc.Encrypt([]byte(req.Cookie))
		if err != nil {
			writeError(w, http.StatusInternalServerError, "encryption failed")
			return
		}
		existing.CookieEnc = []byte(encCookie)
	}
	if err := h.db.Credential.Update(existing); err != nil {
		writeStoreError(w, err)
		return
	}
	updated, err := h.db.Credential.GetByID(id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if updated == nil {
		writeError(w, http.StatusInternalServerError, "credential vanished after update")
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"id":              updated.ID,
		"site_id":         updated.SiteID,
		"kind":            updated.Kind,
		"auth_mode":       normalizeCredentialAuthMode(updated.AuthMode),
		"has_secret":      len(updated.SecretEnc) > 0,
		"has_cookie":      len(updated.CookieEnc) > 0,
		"meta_json":       updated.MetaJSON,
		"status":          updated.Status,
		"checkin_enabled": updated.CheckinEnabled,
		"models_csv":      updated.ModelsCSV,
		"priority":        updated.Priority,
		"created_at":      updated.CreatedAt,
		"updated_at":      updated.UpdatedAt,
	})
}

func normalizeCredentialAuthMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "cookie":
		return "cookie"
	case "auto":
		return "auto"
	default:
		return "access_token"
	}
}

func (h *AdminHandler) deleteCredential(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	if err := h.db.Credential.Delete(id); err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// revealCredentialSecret decrypts and returns a stored credential secret
// (api_key / session token). POST so the admin audit trail records reveals.
// The response is marked no-store and never cached.
func (h *AdminHandler) revealCredentialSecret(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	cred, err := h.db.Credential.GetByID(id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if cred == nil {
		writeError(w, http.StatusNotFound, "credential not found")
		return
	}
	if len(cred.SecretEnc) == 0 {
		writeError(w, http.StatusNotFound, "secret_plaintext_unavailable")
		return
	}
	plain, decryptErr := h.enc.Decrypt(string(cred.SecretEnc))
	if decryptErr != nil {
		writeError(w, http.StatusInternalServerError, "secret_decrypt_failed")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]string{"secret": string(plain)})
}

// ---------------------------------------------------------------------------
// Channels
// ---------------------------------------------------------------------------
