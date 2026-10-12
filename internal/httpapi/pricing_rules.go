package httpapi

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/lan/meta-gateway/internal/domain"
	"github.com/lan/meta-gateway/internal/store"
)

func billingTarget(w http.ResponseWriter, r *http.Request) (string, string, bool) {
	scope, key := r.URL.Query().Get("scope"), strings.TrimSpace(r.URL.Query().Get("key"))
	if scope != "model" && scope != "member" || key == "" || len(key) > 512 || strings.ContainsAny(key, "*?") {
		writeError(w, 400, "invalid billing target")
		return "", "", false
	}
	if scope == "member" {
		id, err := strconv.ParseInt(key, 10, 64)
		if err != nil || id <= 0 {
			writeError(w, 400, "invalid member")
			return "", "", false
		}
	}
	return scope, key, true
}
func priceWriteError(w http.ResponseWriter, err error) {
	if errors.Is(err, store.ErrPriceConflict) {
		writeError(w, 409, err.Error())
		return
	}
	if strings.Contains(strings.ToLower(err.Error()), "sql") || strings.Contains(strings.ToLower(err.Error()), "constraint") {
		writeStoreError(w, err)
		return
	}
	writeError(w, 400, err.Error())
}
func (h *AdminHandler) billingRule(w http.ResponseWriter, r *http.Request) {
	scope, key, ok := billingTarget(w, r)
	if !ok {
		return
	}
	rule, err := h.db.BillingRule(scope, key)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	memberID := int64(0)
	model := key
	upstream := ""
	if scope == "member" {
		memberID, _ = strconv.ParseInt(key, 10, 64)
		member, err := h.db.RouteMember.GetByID(memberID)
		if err != nil {
			writeStoreError(w, err)
			return
		}
		if member == nil {
			writeError(w, 404, "member missing")
			return
		}
		route, err := h.db.Route.GetByID(member.RouteID)
		if err != nil || route == nil {
			writeError(w, 404, "route missing")
			return
		}
		model = route.ModelPattern
		upstream = domain.ResolveUpstreamModel(model, member.MappingJSON, route.MappingJSON)
	}
	layer, priced, err := h.db.ResolveBillingLayer(model, upstream, memberID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	state := "unpriced"
	if priced {
		state = layer.State
	}
	revision := int64(0)
	if rule != nil {
		revision = rule.Revision
	}
	writeJSON(w, 200, map[string]any{"rule": rule, "revision": revision, "effective_state": state, "effective_rates": layer.Rates(0, time.Now()), "owner": layer.Owner, "tiers": layer.Tiers, "windows": layer.Windows, "currency": "USD"})
}
func (h *AdminHandler) saveBillingRule(w http.ResponseWriter, r *http.Request) {
	scope, key, ok := billingTarget(w, r)
	if !ok {
		return
	}
	var body struct {
		Rule     domain.BillingRule `json:"rule"`
		Expected *int64             `json:"expected_revision"`
	}
	if err := decodeJSON(w, r, &body, 0, false); err != nil {
		return
	}
	if body.Expected == nil || *body.Expected < 0 {
		writeError(w, 400, "expected_revision required")
		return
	}
	body.Rule.Source = "manual"
	body.Rule.QuoteID = 0
	rule, err := h.db.SaveBillingRule(scope, key, body.Rule, *body.Expected)
	if err != nil {
		priceWriteError(w, err)
		return
	}
	writeJSON(w, 200, rule)
}
func (h *AdminHandler) copyBillingQuote(w http.ResponseWriter, r *http.Request) {
	scope, key, ok := billingTarget(w, r)
	if !ok {
		return
	}
	var body struct {
		QuoteID    int64    `json:"quote_id"`
		Expected   *int64   `json:"expected_revision"`
		USDPerUnit *float64 `json:"usd_per_unit"`
	}
	if err := decodeJSON(w, r, &body, 0, false); err != nil {
		return
	}
	if body.QuoteID <= 0 || body.Expected == nil || *body.Expected < 0 {
		writeError(w, 400, "quote_id and expected_revision required")
		return
	}
	rule, err := h.db.CopyQuoteRule(scope, key, body.QuoteID, *body.Expected, body.USDPerUnit)
	if err != nil {
		priceWriteError(w, err)
		return
	}
	writeJSON(w, 200, rule)
}
func (h *AdminHandler) priceQuotes(w http.ResponseWriter, r *http.Request) {
	model := strings.TrimSpace(r.URL.Query().Get("model"))
	if len(model) > 512 {
		writeError(w, 400, "invalid model")
		return
	}
	quotes, err := h.db.PriceQuotes(model)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if raw := r.URL.Query().Get("site_id"); raw != "" {
		id, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || id <= 0 {
			writeError(w, 400, "invalid site_id")
			return
		}
		filtered := []domain.PriceQuoteSnapshot{}
		for _, q := range quotes {
			if q.SiteID == id {
				filtered = append(filtered, q)
			}
		}
		quotes = filtered
	}
	writeJSON(w, 200, map[string]any{"items": quotes})
}
func (h *AdminHandler) manualPriceQuote(w http.ResponseWriter, r *http.Request) {
	var q domain.PriceQuoteSnapshot
	if err := decodeJSON(w, r, &q, 0, false); err != nil {
		return
	}
	q.ID = 0
	q.Source = "manual_upstream"
	q.Purpose = "upstream"
	q.Status = "calculable"
	q.Raw = ""
	if q.SiteID <= 0 {
		writeError(w, 400, "site_id required for an upstream quote")
		return
	}
	site, err := h.db.Site.GetByID(q.SiteID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if site == nil {
		writeError(w, 404, "site missing")
		return
	}
	if q.Unit.Kind == "site_credit" {
		q.Unit.Code = fmt.Sprintf("credit:site/%d", q.SiteID)
	}
	q.ScopeKey = fmt.Sprintf("manual:%d:%d:%s:%s", q.SiteID, q.AccountID, q.UpstreamGroup, q.Model)
	if q.AccountID > 0 {
		var accountSite int64
		if err = h.db.QueryRow("SELECT site_id FROM credentials WHERE id=?", q.AccountID).Scan(&accountSite); err != nil || accountSite != q.SiteID {
			writeError(w, 400, "account must belong to this site")
			return
		}
	}
	q.ObservedAt = time.Now().UTC().Format(time.RFC3339Nano)
	// Manual observations remain valid until explicitly changed or expired.
	if q.ExpiresAt != "" {
		at, err := time.Parse(time.RFC3339Nano, q.ExpiresAt)
		if err != nil || !time.Now().Before(at) {
			writeError(w, 400, "invalid quote expiry")
			return
		}
	}
	if err = q.Validate(); err != nil {
		priceWriteError(w, err)
		return
	}
	workload := domain.PriceWorkload{}
	if q.Values.Mode == "token" || q.Values.Mode == "hybrid" {
		workload.Input = 1
		workload.Output = 1
	}
	estimate := domain.EstimateQuote(q, workload, nil, time.Now())
	if estimate.Status != "estimated" {
		writeError(w, 400, "unsupported price mode or missing rate")
		return
	}
	id, err := h.db.SavePriceQuote(q)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	q.ID = id
	writeJSON(w, 201, q)
}
func (h *AdminHandler) estimatePriceQuote(w http.ResponseWriter, r *http.Request) {
	var body struct {
		QuoteID     int64                `json:"quote_id"`
		Workload    domain.PriceWorkload `json:"workload"`
		Balance     *float64             `json:"balance"`
		BalanceUnit string               `json:"balance_unit"`
	}
	if err := decodeJSON(w, r, &body, 0, false); err != nil {
		return
	}
	q, err := h.db.PriceQuote(body.QuoteID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if q == nil {
		writeError(w, 404, "quote missing")
		return
	}
	if body.Balance != nil && body.BalanceUnit != q.Unit.Code {
		writeError(w, 400, "balance and quote must use the same native unit")
		return
	}
	writeJSON(w, 200, domain.EstimateQuote(*q, body.Workload, body.Balance, time.Now()))
}
