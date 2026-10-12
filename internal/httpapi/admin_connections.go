package httpapi

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/lan/meta-gateway/internal/adapters"
	"github.com/lan/meta-gateway/internal/domain"
	"github.com/lan/meta-gateway/internal/proxy"
)

type createConnectionRequest struct {
	Name      string `json:"name"`
	BaseURL   string `json:"base_url"`
	Secret    string `json:"secret"`
	TypeHint  string `json:"type_hint"`
	Platform  string `json:"platform"`
	Status    string `json:"status"`
	ModelsCSV string `json:"models_csv"`
	GroupName string `json:"group_name"`
	// ModelSyncMode is optional: an empty value makes Channel.Create inherit
	// the Admin-configured default (runtime_settings.default_model_sync_mode).
	ModelSyncMode string `json:"model_sync_mode"`
}

// normalizeBaseURL canonicalizes a provider base URL for site reuse matching
// (trim whitespace and trailing slashes).
func normalizeBaseURL(raw string) string {
	return strings.TrimRight(strings.TrimSpace(raw), "/")
}

func (h *AdminHandler) createConnection(w http.ResponseWriter, r *http.Request) {
	h.connectionMu.Lock()
	defer h.connectionMu.Unlock()
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
	// new-api-style one-field configuration, applied on the add path too: an
	// operator who pastes the complete upstream endpoint
	// (`https://api.typesafe.ai/v1/systemone`) gets a clean site root plus the
	// endpoint override, instead of a channel whose relay builds
	// `/v1/systemone/v1/chat/completions`. The site stores the root, so reusing
	// a site by base URL keeps matching the same upstream.
	endpointOverride := ""
	if splitBase, inferred, splitErr := adapters.SplitEndpointBaseURL(baseURL); splitErr == nil && inferred != "" {
		baseURL, endpointOverride = splitBase, inferred
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
	// model_sync_mode is a strict enum when supplied; leaving it empty is how
	// the UI asks for the Admin-configured default.
	syncMode := strings.ToLower(strings.TrimSpace(req.ModelSyncMode))
	if syncMode != "" && syncMode != domain.ModelSyncModeAuto && syncMode != domain.ModelSyncModeManual {
		writeError(w, http.StatusBadRequest, "model_sync_mode must be auto or manual")
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
	groupName := strings.TrimSpace(req.GroupName)
	if groupName == "" {
		groupName = "default"
	}
	draft := &domain.Channel{
		SiteID:               &siteID,
		CredentialID:         &credID,
		Name:                 name,
		GroupName:            groupName,
		Priority:             0,
		Weight:               100,
		Status:               status,
		TypeHint:             strings.TrimSpace(req.TypeHint),
		ModelsCSV:            strings.TrimSpace(req.ModelsCSV),
		ModelSyncMode:        syncMode,
		UpstreamPathOverride: endpointOverride,
	}
	// A provider whose wire contract is not OpenAI chat ships its endpoint and
	// field mapping with the provider itself (see proxy.ProviderProfile), so
	// picking it is enough — the connection is created already able to talk to
	// the upstream, without the operator writing a mapping by hand.
	proxy.ApplyProviderProfile(draft, platform)
	channelID, err := h.db.Channel.Create(draft)
	if err != nil {
		_ = h.db.Credential.Delete(credID)
		rollbackSite()
		writeStoreError(w, err)
		return
	}
	h.modelsCache.Invalidate()

	channel, err := h.db.Channel.GetByID(channelID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	site, err := h.db.Site.GetByID(siteID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if channel == nil || site == nil {
		writeError(w, http.StatusInternalServerError, "connection vanished after create")
		return
	}
	// Checking a model while adding the connection adopts it: the checklist on
	// this dialog is the routing state (see the console's adopt hint), so the
	// names picked here must land as routes and members. Storing them as a mere
	// candidate list would make the operator tick the same boxes twice and read
	// "已选 0" on the connection they just created.
	adopted := make([]string, 0)
	adoptErrors := map[string]string{}
	if domain.NormalizeModelSyncMode(channel.ModelSyncMode) == domain.ModelSyncModeManual {
		adopted, adoptErrors = h.adoptChannelModels(channel.ID, channel.ModelsCSV)
	}
	if len(adopted) > 0 {
		h.modelsCache.Invalidate()
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"channel":           channel,
		"site":              site,
		"credential_id":     credID,
		"reused_site":       reusedSite,
		"has_secret":        true,
		"platform":          platform,
		"detection_matched": false,
		"adopted_models":    adopted,
		"adopt_errors":      adoptErrors,
	})
}

// adoptChannelModels wires a freshly created manual-sync channel's picked
// models into routing: every name gets an exact route (reused when the name is
// already routed — routes are global, and a disabled one is on purpose) plus
// one enabled member for this channel.
//
// Two properties the add path depends on. It never fails as a whole: the
// connection already exists, so a name that cannot be adopted is reported back
// and the remaining names are still wired. And re-running is a no-op: the route
// is looked up before it is created, and a member that is already there is left
// exactly as stored (its enabled flag and group belong to the operator, not to
// the add dialog). Auto-sync channels never come through here — what they serve
// is whatever discovery probes.
func (h *AdminHandler) adoptChannelModels(channelID int64, modelsCSV string) ([]string, map[string]string) {
	adopted := make([]string, 0)
	failures := make(map[string]string)
	seen := make(map[string]struct{})
	for _, raw := range strings.Split(modelsCSV, ",") {
		model := strings.TrimSpace(raw)
		if model == "" {
			continue
		}
		if _, duplicate := seen[model]; duplicate {
			continue
		}
		seen[model] = struct{}{}
		route, err := h.db.Route.GetByModelAny(model)
		if err != nil {
			failures[model] = err.Error()
			continue
		}
		routeID := int64(0)
		if route == nil {
			routeID, err = h.db.Route.Create(&domain.Route{ModelPattern: model, Enabled: true})
			if err != nil {
				failures[model] = err.Error()
				continue
			}
		} else {
			routeID = route.ID
		}
		members, err := h.db.RouteMember.ListByRoute(routeID)
		if err != nil {
			failures[model] = err.Error()
			continue
		}
		bound := false
		for i := range members {
			if members[i].ChannelID == channelID {
				bound = true
				break
			}
		}
		if !bound {
			if _, err := h.db.RouteMember.Create(&domain.RouteMember{
				RouteID:   routeID,
				ChannelID: channelID,
				Priority:  0,
				Weight:    100,
				Enabled:   true,
				Auto:      true,
			}); err != nil {
				failures[model] = err.Error()
				continue
			}
		}
		adopted = append(adopted, model)
		// Same follow-up as creating a route by hand: the capability registry
		// answers "how do I call it" right away instead of at the next catalog
		// sweep, and the queue drops rather than fails when it is full.
		h.bootstrapNewModel(model)
	}
	return adopted, failures
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
