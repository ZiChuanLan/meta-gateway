package httpapi

import (
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/lan/meta-gateway/internal/domain"
	"github.com/lan/meta-gateway/internal/store"
)

// Reference prices are not a promise of which upstream will serve a request.
// They include every enabled, authorized member of the exact model route;
// transient health, key groups and personal routing may narrow that set later.
// No member/channel identity, provider quote or credential crosses this API.
type priceRange struct {
	Min float64 `json:"min"`
	Max float64 `json:"max"`
}
type modelPriceView struct {
	Model       string     `json:"model"`
	InputTokens int        `json:"input_tokens"`
	Currency    string     `json:"currency"`
	EvaluatedAt time.Time  `json:"evaluated_at"`
	Timezone    string     `json:"timezone"`
	Candidates  int        `json:"candidates"`
	Ratio       float64    `json:"ratio"`
	Input       priceRange `json:"input_per_1k"`
	Output      priceRange `json:"output_per_1k"`
	Cache       priceRange `json:"cache_per_1k"`
	PerRequest  priceRange `json:"per_request"`
	Tiered      bool       `json:"tiered"`
	Scheduled   bool       `json:"scheduled"`
}

func modelPricingParams(w http.ResponseWriter, r *http.Request) (string, int, bool) {
	model := strings.TrimSpace(r.URL.Query().Get("model"))
	input := 0
	if raw := r.URL.Query().Get("input_tokens"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 0 || value > 100000000 {
			writeError(w, 400, "invalid input_tokens")
			return "", 0, false
		}
		input = value
	}
	if model == "" || len(model) > 512 || strings.ContainsAny(model, "*?") {
		writeError(w, 400, "a concrete model is required")
		return "", 0, false
	}
	return model, input, true
}

func referenceModelPricing(db *store.DB, model string, input int, allowed func(int64) bool) (*modelPriceView, error) {
	meta, err := db.ModelMetadata.Get(model)
	if err != nil {
		return nil, err
	}
	fallback := domain.PriceLayer{}
	if meta != nil {
		fallback, err = domain.ResolvePriceLayer(meta.PricePromptPer1k, meta.PriceCompletionPer1k, meta.PriceCachePer1k, meta.PricePerRequest, meta.PriceTiers, meta.PriceSchedule)
		if err != nil {
			return nil, err
		}
	}
	ratio, err := db.ModelRatio.GetRatio(model)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	view := &modelPriceView{Model: model, InputTokens: input, Currency: "USD", EvaluatedAt: now, Timezone: now.Location().String(), Ratio: ratio}
	rows, err := db.Query(`SELECT rm.id,rm.price_prompt_per_1k,rm.price_completion_per_1k,rm.price_cache_per_1k,rm.price_per_request,rm.price_tiers,rm.price_schedule
 FROM route_members rm JOIN routes r ON r.id=rm.route_id JOIN channels c ON c.id=rm.channel_id
 WHERE r.model_pattern=? AND r.enabled=1 AND rm.enabled=1 AND c.status='enabled'
 ORDER BY rm.id`, model)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var prompt, completion, cache, perRequest float64
		var tiers, schedule string
		if err := rows.Scan(&id, &prompt, &completion, &cache, &perRequest, &tiers, &schedule); err != nil {
			return nil, err
		}
		if allowed != nil && !allowed(id) {
			continue
		}
		layer, err := domain.ResolvePriceLayer(prompt, completion, cache, perRequest, tiers, schedule)
		if err != nil {
			return nil, err
		}
		if !layer.Priced() {
			layer = fallback
		}
		// An unpriced model is free in the existing ledger. A cache-only fallback
		// must not invent a charge when the billing path considers it unpriced.
		if !layer.Priced() {
			layer = domain.PriceLayer{}
		}
		a, b, c, d := layer.EffectivePrices(input)
		if c <= 0 {
			c = a
		}
		multiplier := ratio * layer.TimeMultiplier(now)
		for _, value := range []float64{a * multiplier, b * multiplier, c * multiplier, d * multiplier} {
			if math.IsInf(value, 0) || math.IsNaN(value) || value < 0 {
				return nil, fmt.Errorf("invalid reference price for model")
			}
		}
		update := func(target *priceRange, value float64) {
			value *= multiplier
			if view.Candidates == 0 || value < target.Min {
				target.Min = value
			}
			if view.Candidates == 0 || value > target.Max {
				target.Max = value
			}
		}
		update(&view.Input, a)
		update(&view.Output, b)
		update(&view.Cache, c)
		update(&view.PerRequest, d)
		view.Tiered = view.Tiered || len(layer.Tiers) > 0
		view.Scheduled = view.Scheduled || len(layer.Windows) > 0
		view.Candidates++
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return view, nil
}

func (h *AdminHandler) modelPricing(w http.ResponseWriter, r *http.Request) {
	model, input, ok := modelPricingParams(w, r)
	if !ok {
		return
	}
	view, err := referenceModelPricing(h.db, model, input, nil)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if view.Candidates == 0 {
		writeError(w, 404, "model unavailable")
		return
	}
	writeJSON(w, 200, view)
}

func (h *TeamHandler) myModelPricing(w http.ResponseWriter, r *http.Request) {
	model, input, ok := modelPricingParams(w, r)
	if !ok {
		return
	}
	policy, err := h.policy(teamActor(r).User.PolicyID)
	if err != nil {
		teamFail(w, 503, "policy_unavailable")
		return
	}
	access := policyAccess(policy)
	if !access.AllowsModel(model) {
		teamFail(w, 404, "model_unavailable")
		return
	}
	view, err := referenceModelPricing(h.db, model, input, access.AllowsGrant)
	if err != nil {
		teamFail(w, 500, "pricing_unavailable")
		return
	}
	if view.Candidates == 0 {
		teamFail(w, 404, "model_unavailable")
		return
	}
	teamJSON(w, 200, view)
}
