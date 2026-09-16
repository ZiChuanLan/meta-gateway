package httpapi

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/lan/meta-gateway/internal/domain"
	"github.com/lan/meta-gateway/internal/modelcatalog"
	"github.com/lan/meta-gateway/internal/store"
)

// catalogPolicyFromRequest reads the per-request write policy. Every group
// defaults to on, so an empty body means "do everything a sync can do"; a
// caller that only wants capabilities switches the rest off explicitly.
type catalogPolicyRequest struct {
	Models       []string `json:"models"`
	Capabilities *bool    `json:"capabilities"`
	Metadata     *bool    `json:"metadata"`
	Prices       *bool    `json:"prices"`
}

func (req catalogPolicyRequest) policy() modelcatalog.Policy {
	policy := modelcatalog.DefaultPolicy
	if req.Capabilities != nil {
		policy.Capabilities = *req.Capabilities
	}
	if req.Metadata != nil {
		policy.Metadata = *req.Metadata
	}
	if req.Prices != nil {
		policy.Prices = *req.Prices
	}
	return policy
}

// catalogTargetModels resolves which models a sync covers. An explicit list
// wins; otherwise the route table decides, because a registry row only earns
// its keep for a model this gateway actually serves.
func (h *AdminHandler) catalogTargetModels(requested []string) ([]string, error) {
	if len(requested) > 0 {
		return requested, nil
	}
	return routableModelPatterns(h.db)
}

// routableModelPatterns lists the enabled, concrete route patterns — the models
// a sync is worth running for. Wildcards are matchers rather than callable ids,
// and no catalog can describe "gpt-*".
func routableModelPatterns(db *store.DB) ([]string, error) {
	routes, err := db.Route.ListEnabledPatterns()
	if err != nil {
		return nil, err
	}
	seen := make(map[string]struct{}, len(routes))
	out := make([]string, 0, len(routes))
	for _, pattern := range routes {
		pattern = strings.TrimSpace(pattern)
		if pattern == "" || strings.ContainsAny(pattern, "*?") {
			continue
		}
		if _, ok := seen[pattern]; ok {
			continue
		}
		seen[pattern] = struct{}{}
		out = append(out, pattern)
	}
	return out, nil
}

// catalogStatus reports what the last sync did and which sources are wired.
func (h *AdminHandler) catalogStatus(w http.ResponseWriter, _ *http.Request) {
	state, err := h.db.ModelCatalog.GetCatalogState()
	if err != nil {
		writeStoreError(w, err)
		return
	}
	sources := []string{}
	if h.modelCatalog != nil {
		sources = h.modelCatalog.Sources()
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"state":   state,
		"sources": sources,
		// The scheduled sweep's own setting, so the console can say whether the
		// registry will refresh on its own.
		"scheduled":      h.modelCatalogScheduled,
		"prices_enabled": h.modelCatalogPrices,
	})
}

// previewModelCatalog answers "what would a sync change" without writing.
func (h *AdminHandler) previewModelCatalog(w http.ResponseWriter, r *http.Request) {
	if h.modelCatalog == nil {
		writeError(w, http.StatusServiceUnavailable, "model catalog is not configured")
		return
	}
	var req catalogPolicyRequest
	if err := decodeJSON(w, r, &req, 0, false); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	policy := req.policy()
	models, err := h.catalogTargetModels(req.Models)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if len(models) == 0 {
		// Nothing to plan, so nothing is downloaded; the effective policy is
		// still reported so the console renders the same controls it would
		// otherwise.
		writeJSON(w, http.StatusOK, &domain.CatalogPreview{
			Items:         []domain.CatalogPreviewItem{},
			Sources:       h.modelCatalog.Sources(),
			PricesEnabled: policy.Prices,
		})
		return
	}
	// A live preview is the honest default: the operator is about to act on it.
	preview, err := h.modelCatalog.Preview(r.Context(), models, policy)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, preview)
}

// syncModelCatalog applies the plan and records the outcome.
func (h *AdminHandler) syncModelCatalog(w http.ResponseWriter, r *http.Request) {
	if h.modelCatalog == nil {
		writeError(w, http.StatusServiceUnavailable, "model catalog is not configured")
		return
	}
	var req catalogPolicyRequest
	if err := decodeJSON(w, r, &req, 0, false); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	models, err := h.catalogTargetModels(req.Models)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if len(models) == 0 {
		writeError(w, http.StatusBadRequest, "no routable models to sync")
		return
	}
	state, err := h.modelCatalog.Sync(r.Context(), models, req.policy())
	if err != nil {
		// A sync that reached no source at all is an upstream problem, not a
		// client one, and the operator needs the reason.
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"state":   state,
		"sources": h.modelCatalog.Sources(),
	})
}

// modelCatalogURLs maps the configured source ids onto their endpoints. An
// unknown id is dropped rather than turned into an empty URL, because config
// validation already rejects it and a silent no-op would be worse than a gap.
func modelCatalogURLs(sources []string) map[string]string {
	out := make(map[string]string, len(sources))
	for _, source := range sources {
		key := strings.ToLower(strings.TrimSpace(source))
		if endpoint, ok := modelcatalog.DefaultURLs[key]; ok {
			out[key] = endpoint
		}
	}
	return out
}

// catalogSweepInitialDelay staggers the first run so a restart storm across
// replicas does not hammer the public indexes at the same instant.
const catalogSweepInitialDelay = 3 * time.Minute

// catalogSweepTimeout bounds one sweep, catalog download included.
const catalogSweepTimeout = 5 * time.Minute

// runCatalogSweep refreshes the registry on a fixed cadence. A failed sweep is
// logged and retried on the next tick; it never stops the loop, because a
// transient network problem must not silently end the schedule.
func runCatalogSweep(ctx context.Context, logger *slog.Logger, db *store.DB, runner modelcatalog.Runner, interval time.Duration, prices bool) {
	policy := modelcatalog.DefaultPolicy
	policy.Prices = prices
	timer := time.NewTimer(catalogSweepInitialDelay)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		models, err := routableModelPatterns(db)
		if err != nil {
			logger.Error("model catalog sweep skipped", "category", "catalog", "error", err)
		} else if len(models) > 0 {
			syncCtx, cancel := context.WithTimeout(ctx, catalogSweepTimeout)
			state, err := runner.Sync(syncCtx, models, policy)
			cancel()
			if err != nil {
				logger.Error("model catalog sweep failed", "category", "catalog", "error", err)
			} else {
				logger.Info("model catalog sweep done", "category", "catalog",
					"models", state.Matched, "capabilities", state.Capabilities,
					"metadata", state.Metadata, "prices", state.Prices)
			}
		}
		timer.Reset(interval)
	}
}
