package httpapi

import (
	"net/http"
	"strings"

	"github.com/lan/meta-gateway/internal/adapters"
	"github.com/lan/meta-gateway/internal/domain"
	"github.com/lan/meta-gateway/internal/proxy"
	"github.com/lan/meta-gateway/internal/sitedetect"
)

func (h *AdminHandler) listSites(w http.ResponseWriter, r *http.Request) {
	sites, err := h.db.Site.List()
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, sites)
}

// endpointPreview resolves the URLs this base would really produce for the
// channel type the operator picked — so the connection editor can show what the
// gateway will actually call BEFORE a mistake turns into a 404.
//
// It exists because the provider presets are only as good as the URL builder: a
// preset whose base landed on the wrong path used to be invisible until the
// first request failed, which is what pushed operators into hand-written
// endpoint overrides. The preview makes the join checkable from the dialog.
//
// The type is part of the question, not decoration. One host can serve OpenAI
// chat, OpenAI Responses or Anthropic Messages, and each joins a base
// differently — Anthropic appends its own `/v1/messages`, Gemini rides the
// version root its base already carries. A preview that answered with the OpenAI
// shape for all of them promised an endpoint the relay never calls (an
// `anthropic` channel was shown `/v1/chat/completions` while it really posts to
// `/v1/messages`).
func (h *AdminHandler) endpointPreview(w http.ResponseWriter, r *http.Request) {
	baseURL := strings.TrimSpace(r.URL.Query().Get("url"))
	if baseURL == "" {
		writeError(w, http.StatusBadRequest, "missing url")
		return
	}
	typeHint := strings.TrimSpace(r.URL.Query().Get("type"))
	// The adapter that will serve this channel builds the URL, so the preview
	// cannot drift from the relay; an unknown type falls back exactly like
	// Registry.ResolveForward does.
	adapter, protocol := previewAdapter(typeHint)
	// A base that carries its own endpoint is reported the way the relay will
	// really build it (root + endpoint override), not the way /v1 would join it.
	resolvedBase, endpointOverride, _ := adapters.SplitEndpointBaseURL(baseURL)
	if endpointOverride == "" {
		// A provider profile (TypeSafe's `/v1/systemone`) owns the endpoint its
		// type names and is written onto the channel at save time, so without it
		// the preview would show a join that channel never makes.
		if profile, ok := proxy.LookupProviderProfile(typeHint); ok {
			endpointOverride = strings.TrimSpace(profile.PathOverride)
		}
	}
	// The model list lives at the API root, never under a pasted chat endpoint:
	// `<root>/v1/models` is where discovery really looks, so that is what the
	// dialog reports.
	models, err := adapter.BuildUpstreamURL(resolvedBase, "models")
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid base_url")
		return
	}
	chat, err := previewChatURL(adapter, resolvedBase, endpointOverride)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid base_url")
		return
	}
	payload := map[string]any{
		"base_url":   resolvedBase,
		"protocol":   protocol,
		"chat_url":   chat,
		"models_url": models,
	}
	if endpointOverride != "" {
		payload["endpoint_override"] = endpointOverride
	}
	writeJSON(w, http.StatusOK, payload)
}

// previewAdapter picks the forward adapter and the protocol id (the wire shape
// the gateway will speak) for a channel type. The ids are the same families the
// translation matrix uses, and the frontend labels them.
func previewAdapter(typeHint string) (adapters.ForwardAdapter, string) {
	switch adapters.CanonicalFamily(adapters.CanonicalType(typeHint)) {
	case "anthropic":
		return adapters.AnthropicForwardAdapter{}, "anthropic"
	case "gemini":
		return adapters.GeminiForwardAdapter{}, "gemini"
	default:
		// Every OpenAI-compatible brand, every unknown id and `custom` speak the
		// passthrough adapter's protocol; see OpenAICompatibleBrands.
		return adapters.OpenAIPassthroughAdapter{}, "openai"
	}
}

// previewChatURL mirrors proxy.resolveUpstreamURL: an explicit endpoint override
// replaces the path — and on the OpenAI family the join switches to JoinRawPath
// so the base keeps its own API root instead of gaining a `/v1`.
func previewChatURL(adapter adapters.ForwardAdapter, base, override string) (string, error) {
	override = strings.Trim(strings.TrimSpace(override), "/")
	if override == "" {
		switch adapter.Name() {
		case "anthropic":
			return adapter.BuildUpstreamURL(base, "messages")
		case "gemini":
			// The model is only known at request time, and the joiner percent-escapes
			// the placeholder — show it the way an operator writes a model name.
			joined, err := adapter.BuildUpstreamURL(base, "models/{model}:generateContent")
			return strings.Replace(joined, "%7Bmodel%7D", "{model}", 1), err
		default:
			return adapter.BuildUpstreamURL(base, "chat/completions")
		}
	}
	if adapter.Name() == "openai-compatible" {
		return adapters.JoinRawPath(base, override)
	}
	return adapter.BuildUpstreamURL(base, override)
}

// detectSiteType runs the AAH-style site-detection chain against a candidate
// URL and returns the normalized family (new-api/one-api/sub2api/…).
func (h *AdminHandler) detectSiteType(w http.ResponseWriter, r *http.Request) {
	url := strings.TrimSpace(r.URL.Query().Get("url"))
	if url == "" {
		writeError(w, http.StatusBadRequest, "missing url")
		return
	}
	result, err := sitedetect.Detect(r.Context(), h.httpClient, url)
	if err != nil {
		writeError(w, http.StatusBadGateway, "site detection failed")
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *AdminHandler) createSite(w http.ResponseWriter, r *http.Request) {
	var site domain.Site
	if err := decodeJSON(w, r, &site, 0, false); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if site.Status == "" {
		site.Status = domain.StatusEnabled
	}
	id, err := h.db.Site.Create(&site)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	created, err := h.db.Site.GetByID(id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if created == nil {
		writeJSON(w, http.StatusCreated, map[string]any{"id": id})
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

func (h *AdminHandler) getSite(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	site, err := h.db.Site.GetByID(id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if site == nil {
		writeError(w, http.StatusNotFound, "site not found")
		return
	}
	writeJSON(w, http.StatusOK, site)
}

func (h *AdminHandler) updateSite(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	var site domain.Site
	if err := decodeJSON(w, r, &site, 0, false); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	site.ID = id
	if err := h.db.Site.Update(&site); err != nil {
		writeStoreError(w, err)
		return
	}
	updated, err := h.db.Site.GetByID(id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if updated == nil {
		writeError(w, http.StatusInternalServerError, "site vanished after update")
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (h *AdminHandler) deleteSite(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	if err := h.db.Site.Delete(id); err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// ---------------------------------------------------------------------------
// Connections (one-shot create: site + credential + channel)
// ---------------------------------------------------------------------------
