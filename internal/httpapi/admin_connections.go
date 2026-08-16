package httpapi

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/lan/meta-gateway/internal/domain"
)

type createConnectionRequest struct {
	Name     string `json:"name"`
	BaseURL  string `json:"base_url"`
	Secret   string `json:"secret"`
	TypeHint string `json:"type_hint"`
	Platform string `json:"platform"`
	Status   string `json:"status"`
}

// normalizeBaseURL canonicalizes a provider base URL for site reuse matching
// (trim whitespace and trailing slashes).
func normalizeBaseURL(raw string) string {
	return strings.TrimRight(strings.TrimSpace(raw), "/")
}

func (h *AdminHandler) createConnection(w http.ResponseWriter, r *http.Request) {
	var req createConnectionRequest
	if err := decodeJSON(w, r, &req, 0, false); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	baseURL := normalizeBaseURL(req.BaseURL)
	if baseURL == "" {
		writeError(w, http.StatusBadRequest, "base_url is required")
		return
	}
	if strings.TrimSpace(req.Secret) == "" {
		writeError(w, http.StatusBadRequest, "secret is required")
		return
	}
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.User != nil {
		writeError(w, http.StatusBadRequest, "invalid base_url")
		return
	}
	platform := strings.TrimSpace(req.Platform)
	if platform == "" {
		platform = strings.TrimSpace(req.TypeHint)
	}
	if platform == "" {
		platform = "openai-compatible"
	}
	status := strings.TrimSpace(req.Status)
	if status == "" {
		status = domain.StatusEnabled
	}
	if status != domain.StatusEnabled && status != domain.StatusDisabled {
		writeError(w, http.StatusBadRequest, "invalid status")
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = hostLabel(baseURL)
	}

	// Reuse an existing site with the same normalized base URL.
	siteID, reusedSite := int64(0), false
	if existing, ok := h.findSiteByBaseURL(baseURL); ok {
		siteID = existing.ID
		reusedSite = true
	} else {
		id, err := h.db.Site.Create(&domain.Site{Name: name, BaseURL: baseURL, Platform: platform, Status: status})
		if err != nil {
			writeStoreError(w, err)
			return
		}
		siteID = id
	}
	// Roll back a site we created when a later step fails.
	rollbackSite := func() {
		if !reusedSite {
			_ = h.db.Site.Delete(siteID)
		}
	}

	encSecret, err := h.enc.Encrypt([]byte(req.Secret))
	if err != nil {
		rollbackSite()
		writeError(w, http.StatusInternalServerError, "encryption failed")
		return
	}
	credID, err := h.db.Credential.Create(&domain.Credential{
		SiteID:    siteID,
		Kind:      "api_key",
		SecretEnc: []byte(encSecret),
		Status:    domain.StatusEnabled,
	})
	if err != nil {
		rollbackSite()
		writeStoreError(w, err)
		return
	}
	channelID, err := h.db.Channel.Create(&domain.Channel{
		SiteID:       &siteID,
		CredentialID: &credID,
		Name:         name,
		GroupName:    "default",
		Priority:     0,
		Weight:       100,
		Status:       status,
		TypeHint:     strings.TrimSpace(req.TypeHint),
	})
	if err != nil {
		_ = h.db.Credential.Delete(credID)
		rollbackSite()
		writeStoreError(w, err)
		return
	}
	h.modelsCache.Invalidate()

	channel, _ := h.db.Channel.GetByID(channelID)
	site, _ := h.db.Site.GetByID(siteID)
	writeJSON(w, http.StatusCreated, map[string]any{
		"channel":           channel,
		"site":              site,
		"credential_id":     credID,
		"reused_site":       reusedSite,
		"has_secret":        true,
		"platform":          platform,
		"detection_matched": false,
	})
}

// findSiteByBaseURL returns the first site whose normalized base URL equals
// the given one (used to reuse a site across connection creations).
func (h *AdminHandler) findSiteByBaseURL(baseURL string) (*domain.Site, bool) {
	sites, err := h.db.Site.List()
	if err != nil {
		return nil, false
	}
	for i := range sites {
		if normalizeBaseURL(sites[i].BaseURL) == baseURL {
			return &sites[i], true
		}
	}
	return nil, false
}

func hostLabel(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" {
		return raw
	}
	return parsed.Host
}

// ---------------------------------------------------------------------------
// Credentials
// ---------------------------------------------------------------------------
