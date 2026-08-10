package httpapi

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
)

// CPAHandler probes a local CLIProxyAPI instance (the OAuth subscription
// account pool) reachable at a user-configured loopback base URL. meta-gateway
// never manages the CPA process; the add-on only surfaces its health and helps
// wire it up as an upstream channel.
type CPAHandler struct {
	client *http.Client
}

func NewCPAHandler() *CPAHandler {
	return &CPAHandler{
		client: &http.Client{Timeout: 3 * time.Second},
	}
}

func (h *CPAHandler) Register(r chi.Router) {
	r.Get("/cpa/status", h.status)
}

// status reports whether a local CLIProxyAPI answers on /healthz.
// base_url is restricted to loopback to keep the admin surface SSRF-free.
func (h *CPAHandler) status(w http.ResponseWriter, r *http.Request) {
	baseURL := strings.TrimSpace(r.URL.Query().Get("base_url"))
	if baseURL == "" {
		baseURL = "http://127.0.0.1:9090"
	}
	parsed, err := url.Parse(baseURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		writeError(w, http.StatusBadRequest, "cpa base_url must be http(s)")
		return
	}
	host := parsed.Hostname()
	if host != "localhost" && host != "127.0.0.1" && !strings.HasPrefix(host, "127.") && !strings.HasPrefix(host, "[::1]") {
		writeError(w, http.StatusBadRequest, "cpa base_url must be a loopback address")
		return
	}
	healthURL := strings.TrimRight(baseURL, "/") + "/healthz"
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	start := time.Now()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, healthURL, nil)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid cpa base_url")
		return
	}
	resp, err := h.client.Do(req)
	latency := time.Since(start).Milliseconds()
	running := false
	errText := ""
	if err != nil {
		errText = err.Error()
	} else {
		defer resp.Body.Close()
		running = resp.StatusCode >= 200 && resp.StatusCode < 300
		if !running {
			errText = "status " + resp.Status
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"running":    running,
		"latency_ms": latency,
		"base_url":   baseURL,
		"error":      errText,
	})
}
