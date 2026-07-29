package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"
	"github.com/lan/meta-gateway/internal/proxy"
	"github.com/lan/meta-gateway/internal/routing"
)

// TryHandler lets the admin console probe chat completions without a downstream key.
// Auth is the admin Bearer; upstream selection uses the same routing/proxy path as /v1.
// Optional channel_id pins a specific upstream when multiple members share the model name.
type TryHandler struct {
	proxy RelayProxy
}

func NewTryHandler(service RelayProxy) *TryHandler {
	return &TryHandler{proxy: service}
}

func (h *TryHandler) Register(r chi.Router) {
	r.Post("/try/chat", h.tryChat)
}

type tryChatRequest struct {
	Model     string `json:"model"`
	Prompt    string `json:"prompt"`
	MaxTokens int    `json:"max_tokens"`
	ChannelID int64  `json:"channel_id"`
}

func (h *TryHandler) tryChat(w http.ResponseWriter, r *http.Request) {
	var request tryChatRequest
	if err := decodeJSON(w, r, &request, 1<<20, false); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	model := strings.TrimSpace(request.Model)
	prompt := strings.TrimSpace(request.Prompt)
	if model == "" {
		writeError(w, http.StatusBadRequest, "model is required")
		return
	}
	if prompt == "" {
		prompt = "Say hello in one short sentence."
	}
	maxTokens := request.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 128
	}
	if maxTokens > 2048 {
		maxTokens = 2048
	}

	body, err := json.Marshal(map[string]any{
		"model": model,
		"messages": []map[string]string{
			{"role": "user", "content": prompt},
		},
		"stream":     false,
		"max_tokens": maxTokens,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to build request")
		return
	}

	requestID := chimw.GetReqID(r.Context())
	if requestID == "" {
		requestID = "admin-try"
	}
	started := time.Now()
	result, meta := h.proxy.ChatCompletionsWithMeta(r.Context(), proxy.Request{
		RequestID:       requestID,
		Model:           model,
		Body:            body,
		Stream:          false,
		PreferChannelID: request.ChannelID,
	})
	latency := int(time.Since(started).Milliseconds())
	if latency < 0 {
		latency = 0
	}

	if result == nil {
		writeError(w, http.StatusBadGateway, "empty_proxy_result")
		return
	}
	if result.Err != nil {
		if errors.Is(result.Err, routing.ErrRouteNotFound) {
			writeError(w, http.StatusNotFound, "route_not_found")
			return
		}
		if errors.Is(result.Err, routing.ErrNoEligible) {
			writeError(w, http.StatusNotFound, "no_eligible_upstream")
			return
		}
		if errors.Is(result.Err, proxy.ErrPreferredChannel) {
			writeError(w, http.StatusUnprocessableEntity, "preferred_channel_unavailable")
			return
		}
		if errors.Is(result.Err, proxy.ErrCredential) {
			writeError(w, http.StatusUnprocessableEntity, "credential_unavailable")
			return
		}
		if errors.Is(result.Err, r.Context().Err()) {
			writeError(w, http.StatusRequestTimeout, "canceled")
			return
		}
		writeError(w, http.StatusBadGateway, "upstream_failure")
		return
	}
	defer result.Body.Close()

	upstreamBody, err := io.ReadAll(io.LimitReader(result.Body, 2<<20))
	if err != nil {
		writeError(w, http.StatusBadGateway, "upstream_read_failed")
		return
	}

	var parsed any
	if json.Unmarshal(upstreamBody, &parsed) != nil {
		parsed = string(upstreamBody)
	}
	payload := map[string]any{
		"status":     result.StatusCode,
		"latency_ms": latency,
		"model":      model,
		"body":       parsed,
	}
	if meta != nil {
		payload["channel_id"] = meta.ChannelID
		payload["channel_name"] = meta.ChannelName
		payload["member_id"] = meta.MemberID
		payload["priority"] = meta.Priority
		payload["weight"] = meta.Weight
	}
	writeJSON(w, http.StatusOK, payload)
}
