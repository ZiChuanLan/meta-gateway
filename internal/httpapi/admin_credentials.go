package httpapi

import (
	"net/http"
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
	}
	result := make([]safeCred, 0, len(creds))
	for _, c := range creds {
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
	cred := &domain.Credential{
		SiteID:    siteID,
		Kind:      req.Kind,
		AuthMode:  authMode,
		SecretEnc: []byte(encSecret),
		CookieEnc: []byte(encCookie),
		MetaJSON:  req.MetaJSON,
		Status:    req.Status,
		ModelsCSV: req.ModelsCSV,
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
		existing.MetaJSON = req.MetaJSON
	}
	if req.ModelsCSV != nil {
		existing.ModelsCSV = strings.TrimSpace(*req.ModelsCSV)
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
