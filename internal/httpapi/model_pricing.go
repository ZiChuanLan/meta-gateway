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
	Model       string    `json:"model"`
	InputTokens int       `json:"input_tokens"`
	Currency    string    `json:"currency"`
	EvaluatedAt time.Time `json:"evaluated_at"`
	Timezone    string    `json:"timezone"`
	Candidates  int       `json:"candidates"`
	// PricedCandidates counts the candidates whose layer actually prices.
	// Candidates - PricedCandidates bill at zero only because no layer priced
	// them, which is different from a free layer that states a price of zero;
	// Status is the summary a caller can branch on without re-deriving it.
	PricedCandidates int        `json:"priced_candidates"`
	Status           string     `json:"status"`
	Ratio            float64    `json:"ratio"`
	Input            priceRange `json:"input_per_1k"`
	Output           priceRange `json:"output_per_1k"`
	Cache            priceRange `json:"cache_per_1k"`
	PerRequest       priceRange `json:"per_request"`
	Tiered           bool       `json:"tiered"`
	Scheduled        bool       `json:"scheduled"`
	// CacheKnown / CacheWriteKnown report whether the cache prices are the
	// layer's own declarations. PriceLayer.Rates falls back to the input price
	// when a layer declares no cache price, so a caller that cannot tell the
	// two apart would present that fallback as a known cache price.
	CacheKnown      bool `json:"cache_known"`
	CacheWriteKnown bool `json:"cache_write_known"`
}

// modelPricingBatch is one request's worth of reference views: the models that
// resolved, and the models the caller sent that have no enabled authorized
// candidate. Unavailable only ever echoes a string the caller sent.
type modelPricingBatch struct {
	Items       []*modelPriceView `json:"items"`
	Unavailable []string          `json:"unavailable"`
}

// maxBatchModels caps one request so a single call cannot fan out into an
// unbounded read; the directory asks for one page at a time (at most 100).
const maxBatchModels = 100

// modelPricingInput validates input_tokens, shared by the single and batch
// endpoints so the two cannot drift.
func modelPricingInput(w http.ResponseWriter, r *http.Request) (int, bool) {
	raw := r.URL.Query().Get("input_tokens")
	if raw == "" {
		return 0, true
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 0 || value > 100000000 {
		writeError(w, 400, "invalid input_tokens")
		return 0, false
	}
	return value, true
}

func modelPricingParams(w http.ResponseWriter, r *http.Request) (string, int, bool) {
	input, ok := modelPricingInput(w, r)
	if !ok {
		return "", 0, false
	}
	model := strings.TrimSpace(r.URL.Query().Get("model"))
	if model == "" || len(model) > 512 || strings.ContainsAny(model, "*?") {
		writeError(w, 400, "a concrete model is required")
		return "", 0, false
	}
	return model, input, true
}

// batchModelPricingParams parses the repeated `model` parameters. Duplicates
// collapse in first-seen order so one page asks for each model once, and every
// entry has to be a concrete model name (no wildcard, no unbounded length).
func batchModelPricingParams(w http.ResponseWriter, r *http.Request) ([]string, int, bool) {
	input, ok := modelPricingInput(w, r)
	if !ok {
		return nil, 0, false
	}
	seen := map[string]bool{}
	models := []string{}
	for _, raw := range r.URL.Query()["model"] {
		model := strings.TrimSpace(raw)
		if model == "" || len(model) > 512 || strings.ContainsAny(model, "*?") {
			writeError(w, 400, "a concrete model is required")
			return nil, 0, false
		}
		if seen[model] {
			continue
		}
		seen[model] = true
		models = append(models, model)
	}
	if len(models) == 0 {
		writeError(w, 400, "a concrete model is required")
		return nil, 0, false
	}
	if len(models) > maxBatchModels {
		writeError(w, 400, fmt.Sprintf("at most %d models per request", maxBatchModels))
		return nil, 0, false
	}
	return models, input, true
}

func referenceModelPricing(db *store.DB, model string, input int, allowed func(int64) bool) (*modelPriceView, error) {
	ratio, err := db.ModelRatio.GetRatio(model)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	view := &modelPriceView{Model: model, InputTokens: input, Currency: "USD", EvaluatedAt: now, Timezone: now.Location().String(), Ratio: ratio}
	rows, err := db.Query(`SELECT rm.id,rm.price_prompt_per_1k,rm.price_completion_per_1k,rm.price_cache_per_1k,rm.price_per_request,rm.price_tiers,rm.price_schedule,rm.mapping_json,r.mapping_json
 FROM route_members rm JOIN routes r ON r.id=rm.route_id JOIN channels c ON c.id=rm.channel_id
 WHERE r.model_pattern=? AND r.enabled=1 AND rm.enabled=1 AND c.status='enabled'
 ORDER BY rm.id`, model)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type priceRow struct {
		id                                           int64
		prompt, completion, cache, perRequest        float64
		tiers, schedule, memberMapping, routeMapping string
	}
	var prices []priceRow
	for rows.Next() {
		var row priceRow
		if err := rows.Scan(&row.id, &row.prompt, &row.completion, &row.cache, &row.perRequest, &row.tiers, &row.schedule, &row.memberMapping, &row.routeMapping); err != nil {
			return nil, err
		}
		if allowed == nil || allowed(row.id) {
			prices = append(prices, row)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// Release the SQLite cursor before resolving metadata through the same DB.
	if err := rows.Close(); err != nil {
		return nil, err
	}
	priced := 0
	for _, row := range prices {
		upstream := domain.ResolveUpstreamModel(model, row.memberMapping, row.routeMapping)
		layer, _, err := db.ResolveBillingLayer(model, upstream, row.id)
		if err != nil {
			return nil, err
		}
		// An unpriced model is free in the existing ledger. A cache-only fallback
		// must not invent a charge when the billing path considers it unpriced.
		if layer.Priced() {
			priced++
		} else {
			layer = domain.PriceLayer{}
		}
		readKnown, writeKnown := cachePriceKnown(layer, input)
		rates := layer.Rates(input, now)
		a, b, c, d := rates.Input, rates.Output, rates.CacheRead, rates.PerRequest
		multiplier := ratio * rates.Multiplier
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
		// The cache price is only "known" when every candidate declared one.
		if view.Candidates == 0 {
			view.CacheKnown, view.CacheWriteKnown = readKnown, writeKnown
		} else {
			view.CacheKnown = view.CacheKnown && readKnown
			view.CacheWriteKnown = view.CacheWriteKnown && writeKnown
		}
		view.Candidates++
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	view.PricedCandidates = priced
	view.Status = pricingStatus(priced, view.Candidates)
	return view, nil
}

// cachePriceKnown reports whether a layer's cache prices are its own
// declarations rather than the input-price fallback PriceLayer.Rates applies
// when a cache price is absent. A free layer states a price of zero, so both
// are known.
func cachePriceKnown(layer domain.PriceLayer, input int) (read, write bool) {
	if layer.State == "free" {
		return true, true
	}
	_, _, effectiveCache, _ := layer.EffectivePrices(input)
	return layer.CachePresent || effectiveCache > 0, layer.CacheWrite != nil
}

// pricingStatus summarizes how many candidates carry a price. A free layer
// counts as priced: it states a price of zero, unlike an unpriced candidate
// that bills at zero only because no layer priced it.
func pricingStatus(priced, candidates int) string {
	switch {
	case priced == 0:
		return "unpriced"
	case priced == candidates:
		return "priced"
	default:
		return "partially_priced"
	}
}

// referenceModelPricingBatch resolves one view per requested model. A model the
// caller may not use, or one with no enabled authorized candidate, is reported
// in unavailable instead of failing the batch, and unavailable only ever holds
// a string the caller sent. A store error aborts the whole batch: a broken read
// must not read as "model unavailable".
func referenceModelPricingBatch(db *store.DB, models []string, input int, permit func(string) bool, allowed func(int64) bool) ([]*modelPriceView, []string, error) {
	items := []*modelPriceView{}
	unavailable := []string{}
	for _, model := range models {
		if permit != nil && !permit(model) {
			unavailable = append(unavailable, model)
			continue
		}
		view, err := referenceModelPricing(db, model, input, allowed)
		if err != nil {
			return nil, nil, err
		}
		if view.Candidates == 0 {
			unavailable = append(unavailable, model)
			continue
		}
		items = append(items, view)
	}
	return items, unavailable, nil
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

func (h *AdminHandler) modelPricingBatch(w http.ResponseWriter, r *http.Request) {
	models, input, ok := batchModelPricingParams(w, r)
	if !ok {
		return
	}
	items, unavailable, err := referenceModelPricingBatch(h.db, models, input, nil, nil)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, 200, modelPricingBatch{Items: items, Unavailable: unavailable})
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

func (h *TeamHandler) myModelPricingBatch(w http.ResponseWriter, r *http.Request) {
	models, input, ok := batchModelPricingParams(w, r)
	if !ok {
		return
	}
	policy, err := h.policy(teamActor(r).User.PolicyID)
	if err != nil {
		teamFail(w, 503, "policy_unavailable")
		return
	}
	access := policyAccess(policy)
	items, unavailable, err := referenceModelPricingBatch(h.db, models, input, access.AllowsModel, access.AllowsGrant)
	if err != nil {
		teamFail(w, 500, "pricing_unavailable")
		return
	}
	teamJSON(w, 200, modelPricingBatch{Items: items, Unavailable: unavailable})
}
