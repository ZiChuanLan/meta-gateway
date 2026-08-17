package httpapi

import (
	"context"
	"crypto/subtle"
	"errors"
	"net/http"
	"net/http/httputil"
	"net/url"
	pathpkg "path"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/lan/meta-gateway/internal/plugins"
)

type sidecarAuthMode uint8

const (
	sidecarAuthUnauthorized sidecarAuthMode = iota
	sidecarAuthAdmin
	sidecarAuthPlugin
)

type PluginHandler struct {
	service *plugins.Service
	// verifyAdmin validates an admin token (raw or session). Wired by
	// SetTokenVerifier for the public proxy route, which accepts the token
	// via ?t= because iframes cannot send Authorization headers.
	verifyAdmin func(string) bool
}

func NewPluginHandler(service *plugins.Service) *PluginHandler {
	return &PluginHandler{service: service}
}

// SetTokenVerifier wires the admin token validator used by the public
// sidecar proxy route (Authorization header or ?t= query).
func (h *PluginHandler) SetTokenVerifier(fn func(string) bool) {
	h.verifyAdmin = fn
}

func (h *PluginHandler) Register(r chi.Router) {
	r.Get("/plugins/catalog", h.catalog)
	r.Get("/plugins/status", h.status)
	r.Get("/plugins", h.list)
	r.Get("/plugins/market", h.marketList)
	r.Post("/plugins/market/{id}/install", h.marketInstall)
	r.Post("/plugins/register", h.registerSidecar)
	r.Put("/plugins/{id}", h.updateSidecar)
	r.Post("/plugins/{id}/activate", h.activate)
	r.Post("/plugins/{id}/install", h.install)
	r.Post("/plugins/{id}/enable", h.enable)
	r.Post("/plugins/{id}/disable", h.disable)
	r.Delete("/plugins/{id}", h.uninstall)
}

// marketList returns the plugin market: all installable entries from every
// registry source plus the source list itself.
func (h *PluginHandler) marketList(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	writeJSON(w, http.StatusOK, map[string]any{
		"sources": h.service.MarketSources(),
		"plugins": h.service.MarketPlugins(ctx),
	})
}

// marketInstall installs a market entry as a sidecar plugin (register +
// health check + enable).
func (h *PluginHandler) marketInstall(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	rec, err := h.service.InstallMarket(ctx, id)
	if err != nil {
		switch {
		case errors.Is(err, plugins.ErrNotFound):
			writeError(w, http.StatusNotFound, "plugin_not_found")
		case strings.Contains(err.Error(), "plugin_market_unavailable"),
			strings.Contains(err.Error(), "plugin_manifest_"),
			strings.Contains(err.Error(), "plugin_health_check_failed"),
			strings.Contains(err.Error(), "plugin_path_invalid"),
			strings.Contains(err.Error(), "plugin_api_prefix_"):
			writeError(w, http.StatusBadRequest, err.Error())
		default:
			writePluginError(w, err)
		}
		return
	}
	writeJSON(w, http.StatusCreated, rec)
}

// RegisterPublic mounts the sidecar plugin proxy on the public router (it is
// NOT inside the admin middleware chain because iframe embedding cannot send
// an Authorization header — the handler verifies the token itself, accepting
// both the header and ?t=). Must be registered before r.Mount("/admin", …)
// so it wins the route match.
func (h *PluginHandler) RegisterPublic(r chi.Router) {
	r.Handle("/admin/plugins/{id}/proxy/*", http.HandlerFunc(h.proxySidecar))
	// Root-level API prefixes declared by sidecar plugins (e.g. CPAMC calls
	// /v0/management/*). Registering them here lets plugin frontends work
	// with their default API base — no manual address configuration.
	// Register one immutable fallback route. Adding/removing chi routes while
	// requests are being served mutates its radix tree and races with readers;
	// the dispatcher below resolves the current prefix list per request.
	r.Handle("/*", http.HandlerFunc(h.forwardAPIPrefix))
}

func (h *PluginHandler) catalog(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, h.service.Catalog())
}

func (h *PluginHandler) list(w http.ResponseWriter, _ *http.Request) {
	items, err := h.service.ListInstalled()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error")
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func (h *PluginHandler) status(w http.ResponseWriter, _ *http.Request) {
	items, err := h.service.Status()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error")
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func (h *PluginHandler) activate(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	rec, err := h.service.Activate(id)
	if err != nil {
		writePluginError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rec)
}

func (h *PluginHandler) install(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	rec, err := h.service.Install(id)
	if err != nil {
		writePluginError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, rec)
}

func (h *PluginHandler) enable(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	rec, err := h.service.Enable(id)
	if err != nil {
		writePluginError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rec)
}

func (h *PluginHandler) disable(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	rec, err := h.service.Disable(id)
	if err != nil {
		writePluginError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rec)
}

func (h *PluginHandler) uninstall(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := h.service.Uninstall(id); err != nil {
		writePluginError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// registerSidecar registers a third-party sidecar plugin: fetches its
// /plugin.json manifest, health-checks the service, then installs + enables.
// When the service has no /plugin.json (e.g. CLIProxyAPI's built-in CPAMC
// page), id/name/page_path from the body are used as a manual manifest.
// Body: {"url": "http://127.0.0.1:9100", "api_key": "...",
//
//	"id": "cpa-console", "name": "CPA 管理", "page_path": "/management.html"}
func (h *PluginHandler) registerSidecar(w http.ResponseWriter, r *http.Request) {
	var body struct {
		URL         string `json:"url"`
		APIKey      string `json:"api_key"`
		ID          string `json:"id"`
		Name        string `json:"name"`
		PagePath    string `json:"page_path"`
		HealthPath  string `json:"health_path"`
		APIPrefix   string `json:"api_prefix"`
		ChannelPath string `json:"channel_path"`
	}
	if err := decodeJSON(w, r, &body, 1<<20, false); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	var manual *plugins.SidecarManifest
	if body.ID != "" || body.Name != "" || body.PagePath != "" || body.HealthPath != "" || body.APIPrefix != "" || body.ChannelPath != "" {
		manual = &plugins.SidecarManifest{
			ID:          strings.TrimSpace(body.ID),
			Version:     "1.0.0",
			Name:        strings.TrimSpace(body.Name),
			PagePath:    strings.TrimSpace(body.PagePath),
			HealthPath:  strings.TrimSpace(body.HealthPath),
			APIPrefix:   strings.TrimSpace(body.APIPrefix),
			ChannelPath: strings.TrimSpace(body.ChannelPath),
		}
	}
	rec, err := h.service.RegisterSidecar(body.URL, body.APIKey, manual)
	if err != nil {
		switch {
		case strings.Contains(err.Error(), "plugin_url_required"),
			strings.Contains(err.Error(), "plugin_url_invalid"),
			strings.Contains(err.Error(), "plugin_manifest_"),
			strings.Contains(err.Error(), "plugin_health_check_failed"),
			strings.Contains(err.Error(), "plugin_path_invalid"),
			strings.Contains(err.Error(), "plugin_api_prefix_"):
			writeError(w, http.StatusBadRequest, err.Error())
		default:
			writePluginError(w, err)
		}
		return
	}
	writeJSON(w, http.StatusCreated, rec)
}

// updateSidecar changes an installed sidecar plugin's connection settings
// (URL / API key / page & health paths, optional name). The health check is
// re-run with the new values, so a dead endpoint keeps the old config.
// Body: {"url": "http://127.0.0.1:9100", "api_key": "...",
//
//	"name": "...", "page_path": "...", "health_path": "..."}
func (h *PluginHandler) updateSidecar(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var body struct {
		Name        string `json:"name"`
		URL         string `json:"url"`
		APIKey      string `json:"api_key"`
		PagePath    string `json:"page_path"`
		HealthPath  string `json:"health_path"`
		APIPrefix   string `json:"api_prefix"`
		ChannelPath string `json:"channel_path"`
	}
	if err := decodeJSON(w, r, &body, 1<<20, false); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	spec := &plugins.SidecarSpec{
		URL:         strings.TrimSpace(body.URL),
		APIKey:      strings.TrimSpace(body.APIKey),
		PagePath:    strings.TrimSpace(body.PagePath),
		HealthPath:  strings.TrimSpace(body.HealthPath),
		APIPrefix:   strings.TrimSpace(body.APIPrefix),
		ChannelPath: strings.TrimSpace(body.ChannelPath),
	}
	rec, err := h.service.UpdateSidecar(id, body.Name, spec)
	if err != nil {
		switch {
		case strings.Contains(err.Error(), "plugin_url_required"),
			strings.Contains(err.Error(), "plugin_url_invalid"),
			strings.Contains(err.Error(), "plugin_health_check_failed"),
			strings.Contains(err.Error(), "plugin_not_sidecar"),
			strings.Contains(err.Error(), "plugin_path_invalid"),
			strings.Contains(err.Error(), "plugin_api_prefix_"):
			writeError(w, http.StatusBadRequest, err.Error())
		default:
			writePluginError(w, err)
		}
		return
	}
	writeJSON(w, http.StatusOK, rec)
}

// proxySidecar reverse-proxies requests to an enabled sidecar plugin's
// service. Path /plugins/{id}/proxy/ (no trailing path) serves the plugin's
// configured page path. The admin token is accepted via the standard
// Authorization header or the ?t= query parameter (for iframe embedding);
// every proxied request carries X-Plugin-Key when the plugin has an API key.
//
// Authorization handling is deliberately split into two explicit modes:
// a valid admin token is stripped before forwarding, while a plugin token is
// accepted only when it exactly matches the registered API key.  The old
// behaviour treated every non-empty Authorization value as a plugin token;
// that made a garbage header sufficient to reach a sidecar with the gateway's
// injected X-Plugin-Key.
func (h *PluginHandler) proxySidecar(w http.ResponseWriter, r *http.Request) {
	setPluginSecurityHeaders(w)
	id := chi.URLParam(r, "id")
	if !h.service.IsEnabled(id) {
		writeError(w, http.StatusNotFound, "plugin_not_enabled")
		return
	}
	spec, err := h.service.SidecarFor(id)
	if err != nil || spec == nil {
		writeError(w, http.StatusNotFound, "plugin_not_sidecar")
		return
	}
	authMode, pluginToken := h.sidecarAuth(r, spec.APIKey)
	if authMode == sidecarAuthUnauthorized {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return
	}
	target, err := url.Parse(spec.URL)
	if err != nil || (target.Scheme != "http" && target.Scheme != "https") || target.Host == "" || target.User != nil || target.RawPath != "" || !validPluginProxyPath(target.Path) {
		writeError(w, http.StatusBadGateway, "plugin_url_invalid")
		return
	}
	requestPath := chi.URLParam(r, "*")
	if requestPath == "" || requestPath == "/" {
		requestPath = spec.PagePath
	}
	if !validPluginProxyPath(requestPath) {
		writeError(w, http.StatusBadRequest, "plugin_path_invalid")
		return
	}
	if requestPath == "" {
		requestPath = "/"
	}
	target.Path = strings.TrimRight(target.Path, "/") + "/" + strings.TrimLeft(requestPath, "/")
	proxy := httputil.NewSingleHostReverseProxy(target)
	// NewSingleHostReverseProxy only rewrites scheme/host; the path must be
	// set explicitly or the plugin receives /admin/plugins/{id}/proxy/… and
	// 404s. Query params are preserved (the admin ?t= token is stripped).
	proxy.Director = func(req *http.Request) {
		req.URL.Scheme = target.Scheme
		req.URL.Host = target.Host
		req.URL.Path = target.Path
		if spec.APIKey != "" {
			req.Header.Set("X-Plugin-Key", spec.APIKey)
		}
		if authMode == sidecarAuthAdmin {
			req.Header.Del("Authorization")
		} else if authMode == sidecarAuthPlugin {
			// Normalize the accepted plugin credential so surrounding whitespace
			// cannot be interpreted differently by the sidecar.
			req.Header.Set("Authorization", "Bearer "+pluginToken)
		}
		q := req.URL.Query()
		q.Del("t")
		req.URL.RawQuery = q.Encode()
	}
	proxy.ModifyResponse = func(resp *http.Response) error {
		setPluginSecurityHeaderValues(resp.Header)
		return nil
	}
	proxy.ServeHTTP(w, r)
}

// forwardAPIPrefix reverse-proxies a root-level API prefix (declared via
// SidecarSpec.APIPrefix) to the owning sidecar plugin, e.g. /v0/management/*
// → {plugin}/v0/management/*. Authentication is the same as proxySidecar:
// Authorization header or ?t= query.
func (h *PluginHandler) forwardAPIPrefix(w http.ResponseWriter, r *http.Request) {
	setPluginSecurityHeaders(w)
	// Look up the plugin owning this prefix (first match wins).
	var owner *plugins.PrefixForwarder
	for _, fw := range h.service.PrefixForwarders() {
		if r.URL.Path == fw.Prefix || strings.HasPrefix(r.URL.Path, fw.Prefix+"/") {
			owner = &fw
			break
		}
	}
	if owner == nil {
		writeError(w, http.StatusNotFound, "plugin_not_enabled")
		return
	}
	authMode, pluginToken := h.sidecarAuth(r, owner.Spec.APIKey)
	if authMode == sidecarAuthUnauthorized {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return
	}
	target, err := url.Parse(owner.Spec.URL)
	if err != nil || (target.Scheme != "http" && target.Scheme != "https") || target.Host == "" || target.User != nil || target.RawPath != "" || !validPluginProxyPath(target.Path) {
		writeError(w, http.StatusBadGateway, "plugin_url_invalid")
		return
	}
	if !validPluginProxyPath(r.URL.Path) {
		writeError(w, http.StatusBadRequest, "plugin_path_invalid")
		return
	}
	// Preserve the full prefix path: /v0/management/login → {url}/v0/management/login.
	rest := strings.TrimPrefix(r.URL.Path, owner.Prefix)
	target.Path = strings.TrimRight(target.Path, "/") + owner.Prefix + rest
	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.Director = func(req *http.Request) {
		req.URL.Scheme = target.Scheme
		req.URL.Host = target.Host
		req.URL.Path = target.Path
		if owner.Spec.APIKey != "" {
			req.Header.Set("X-Plugin-Key", owner.Spec.APIKey)
		}
		if authMode == sidecarAuthAdmin {
			req.Header.Del("Authorization")
		} else if authMode == sidecarAuthPlugin {
			req.Header.Set("Authorization", "Bearer "+pluginToken)
		}
		q := req.URL.Query()
		q.Del("t")
		req.URL.RawQuery = q.Encode()
	}
	proxy.ModifyResponse = func(resp *http.Response) error {
		setPluginSecurityHeaderValues(resp.Header)
		return nil
	}
	proxy.ServeHTTP(w, r)
}

// sidecarAuth authenticates a public sidecar request and returns the forwarding
// mode.  A query token is accepted only as an admin token for iframe bootstrap;
// plugin API credentials must arrive in an Authorization header and match the
// key explicitly registered for that sidecar.  This prevents arbitrary bearer
// values from becoming an authentication bypass.
func (h *PluginHandler) sidecarAuth(r *http.Request, pluginKey string) (sidecarAuthMode, string) {
	if r == nil {
		return sidecarAuthUnauthorized, ""
	}
	authValues := r.Header.Values("Authorization")
	queryValues, hasQueryToken := r.URL.Query()["t"]
	if len(queryValues) > 1 || (hasQueryToken && len(authValues) > 0) {
		// Keep header and query authentication as mutually exclusive modes;
		// duplicate query values otherwise make it unclear which credential was
		// actually verified and are easy to introduce through URL concatenation.
		return sidecarAuthUnauthorized, ""
	}
	if len(authValues) > 1 {
		return sidecarAuthUnauthorized, ""
	}
	if len(authValues) == 1 {
		header := strings.TrimSpace(authValues[0])
		parts := strings.SplitN(header, " ", 2)
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
			return sidecarAuthUnauthorized, ""
		}
		token := strings.TrimSpace(parts[1])
		if token == "" {
			return sidecarAuthUnauthorized, ""
		}
		if h.verifyAdmin != nil && h.verifyAdmin(token) {
			return sidecarAuthAdmin, ""
		}
		if pluginKey != "" && equalSecret(token, pluginKey) {
			return sidecarAuthPlugin, token
		}
		return sidecarAuthUnauthorized, ""
	}
	// The query form exists solely for iframe navigation. Never treat it as a
	// plugin credential, since query strings are routinely copied and logged.
	queryToken := ""
	if len(queryValues) == 1 {
		queryToken = strings.TrimSpace(queryValues[0])
	}
	if queryToken != "" && h.verifyAdmin != nil && h.verifyAdmin(queryToken) {
		return sidecarAuthAdmin, ""
	}
	return sidecarAuthUnauthorized, ""
}

func setPluginSecurityHeaders(w http.ResponseWriter) {
	setPluginSecurityHeaderValues(w.Header())
}

func setPluginSecurityHeaderValues(header http.Header) {
	header.Set("Cache-Control", "no-store")
	header.Set("Pragma", "no-cache")
	header.Set("Referrer-Policy", "no-referrer")
}

// validPluginProxyPath accepts a normalized URL path and rejects traversal or
// encoded separators before it is joined with a sidecar base path. The
// gateway deliberately does not decode or normalize incoming paths here:
// either representation must already be canonical.
func validPluginProxyPath(raw string) bool {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "/" {
		return true
	}
	if strings.ContainsAny(raw, "?#%\\") || strings.Contains(raw, "//") || strings.IndexByte(raw, 0) >= 0 {
		return false
	}
	trimmed := strings.Trim(raw, "/")
	for _, segment := range strings.Split(trimmed, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return false
		}
	}
	return pathpkg.Clean("/"+trimmed) == "/"+trimmed
}

func equalSecret(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

func writePluginError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, plugins.ErrNotFound):
		writeError(w, http.StatusNotFound, "plugin_not_found")
	case errors.Is(err, plugins.ErrNotInstalled):
		writeError(w, http.StatusNotFound, "plugin_not_installed")
	case errors.Is(err, plugins.ErrAlreadyExists):
		writeError(w, http.StatusConflict, "plugin_already_installed")
	case errors.Is(err, plugins.ErrInvalidID):
		writeError(w, http.StatusBadRequest, "plugin_invalid_id")
	case errors.Is(err, plugins.ErrCoreImmutable):
		writeError(w, http.StatusBadRequest, "plugin_core_immutable")
	default:
		writeError(w, http.StatusInternalServerError, "internal_error")
	}
}

// requirePluginEnabled returns middleware that 404s unless plugin id is enabled.
func requirePluginEnabled(service *plugins.Service, id string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if service == nil || !service.IsEnabled(id) {
				writeError(w, http.StatusNotFound, "plugin_disabled")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
