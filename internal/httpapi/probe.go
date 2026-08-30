package httpapi

import (
	"context"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/lan/meta-gateway/internal/probe"
	"github.com/lan/meta-gateway/internal/store"
)

// ProbeHandler exposes model probing: start a run over a selection of
// (channel, model) pairs, watch it, cancel it, and read the outcome.
type ProbeHandler struct {
	db      *store.DB
	service *probe.Service
}

func NewProbeHandler(db *store.DB, service *probe.Service) *ProbeHandler {
	return &ProbeHandler{db: db, service: service}
}

func (h *ProbeHandler) Register(r chi.Router) {
	r.Post("/probes", h.create)
	r.Get("/probes", h.list)
	r.Get("/probes/{id}", h.get)
	r.Get("/probes/{id}/results", h.results)
	r.Post("/probes/{id}/cancel", h.cancel)
	r.Get("/model-health", h.health)
}

type probeCreateRequest struct {
	// Empty means "everything": every model of every route, on every channel.
	ChannelIDs []int64  `json:"channel_ids,omitempty"`
	Models     []string `json:"models,omitempty"`
	MaxTokens  int      `json:"max_tokens,omitempty"`
	// Prompt is the user message sent upstream; empty means the default "hi".
	Prompt      string `json:"prompt,omitempty"`
	Concurrency int    `json:"concurrency,omitempty"`
	// AutoDisableAfter disables a member after this many consecutive probe
	// failures, for this run only; 0 leaves routing untouched.
	AutoDisableAfter int `json:"auto_disable_after,omitempty"`
}

func (h *ProbeHandler) create(w http.ResponseWriter, r *http.Request) {
	var request probeCreateRequest
	if err := decodeJSON(w, r, &request, 0, false); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	// Probes are real upstream traffic, so only one run at a time — otherwise
	// two operators could unknowingly double the load on every channel.
	tasks, err := h.db.ListProbeTasks(20)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	for _, task := range tasks {
		if task.Status == store.ProbeTaskRunning {
			writeError(w, http.StatusConflict, "a probe run is already in progress")
			return
		}
	}
	pairs, err := h.candidatePairs(request.ChannelIDs, request.Models)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if len(pairs) == 0 {
		writeError(w, http.StatusBadRequest, "no channel/model pairs to probe")
		return
	}
	// Deliberately not r.Context(): the run outlives this response, and a
	// request context would cancel it the moment the client got its 202.
	task, err := h.service.Start(context.Background(), pairs, probe.Options{
		MaxTokens:        request.MaxTokens,
		Concurrency:      request.Concurrency,
		Prompt:           request.Prompt,
		AutoDisableAfter: request.AutoDisableAfter,
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, task)
}

// candidatePairs defers to the probe package so the scheduled run and the
// on-demand run select from exactly the same set.
func (h *ProbeHandler) candidatePairs(channelIDs []int64, models []string) ([]probe.Pair, error) {
	return probe.CandidatePairs(h.db, channelIDs, models)
}

func (h *ProbeHandler) list(w http.ResponseWriter, _ *http.Request) {
	tasks, err := h.db.ListProbeTasks(20)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, tasks)
}

func (h *ProbeHandler) get(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	task, err := h.db.GetProbeTask(id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if task == nil {
		writeError(w, http.StatusNotFound, "probe task not found")
		return
	}
	writeJSON(w, http.StatusOK, task)
}

func (h *ProbeHandler) results(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	items, err := h.db.ListProbeResults(id, 0)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func (h *ProbeHandler) cancel(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	if err := h.service.Cancel(id); err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "cancelling"})
}

func (h *ProbeHandler) health(w http.ResponseWriter, _ *http.Request) {
	items, err := h.db.ListModelHealth()
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, items)
}
