package domain

import (
	"fmt"
	"math"
	"strings"
	"time"
)

// BillingRule is intentional gateway policy, never a live upstream quote.
// Rates are USD/1k (request fees are USD/call). Nil means unknown, zero is free.
type BillingRule struct {
	State      string        `json:"state"` // explicit | free | inherit
	Mode       string        `json:"mode"`  // token | request | hybrid
	Input      *float64      `json:"input_per_1k,omitempty"`
	Output     *float64      `json:"output_per_1k,omitempty"`
	CacheRead  *float64      `json:"cache_per_1k,omitempty"`
	CacheWrite *float64      `json:"cache_write_per_1k,omitempty"`
	PerRequest *float64      `json:"per_request,omitempty"`
	Tiers      []PriceTier   `json:"tiers,omitempty"`
	Windows    []PriceWindow `json:"windows,omitempty"`
	Timezone   string        `json:"timezone"`
	Source     string        `json:"source"` // manual | quote_copy | legacy
	USDPerUnit *float64      `json:"usd_per_unit,omitempty"`
	QuoteID    int64         `json:"quote_id,omitempty"`
	Revision   int64         `json:"revision"`
	UpdatedAt  string        `json:"updated_at,omitempty"`
}

func (r BillingRule) Validate() error {
	if r.State != "explicit" && r.State != "free" && r.State != "inherit" {
		return fmt.Errorf("invalid billing state")
	}
	if r.Timezone == "" {
		return fmt.Errorf("billing timezone required")
	}
	if _, err := time.LoadLocation(r.Timezone); err != nil {
		return fmt.Errorf("invalid billing timezone")
	}
	for _, v := range []*float64{r.Input, r.Output, r.CacheRead, r.CacheWrite, r.PerRequest} {
		if v != nil && (*v < 0 || math.IsNaN(*v) || math.IsInf(*v, 0) || *v > 1e9) {
			return fmt.Errorf("invalid rate")
		}
	}
	if len(r.Source) > 64 || strings.ContainsAny(r.Source, "\r\n") {
		return fmt.Errorf("invalid rule source")
	}
	if r.State != "explicit" {
		return nil
	}
	switch r.Mode {
	case "token", "hybrid":
		if len(r.Tiers) == 0 && (r.Input == nil || r.Output == nil) {
			return fmt.Errorf("token input and output rates required (0 is free)")
		}
	case "request":
		if r.PerRequest == nil {
			return fmt.Errorf("request fee required")
		}
	default:
		return fmt.Errorf("invalid billing mode")
	}
	if r.Mode == "hybrid" && r.PerRequest == nil {
		return fmt.Errorf("hybrid request fee required")
	}
	if r.Mode == "request" && (len(r.Tiers) > 0 || r.Input != nil || r.Output != nil || r.CacheRead != nil || r.CacheWrite != nil) {
		return fmt.Errorf("request mode cannot contain token rates")
	}
	if r.Mode == "token" && r.PerRequest != nil && *r.PerRequest != 0 {
		return fmt.Errorf("use hybrid for a token rule with a call fee")
	}
	if err := ValidatePriceTiers(r.Tiers); err != nil {
		return err
	}
	return ValidatePriceWindows(r.Windows)
}

func (r BillingRule) Layer(scope, key string) PriceLayer {
	l := PriceLayer{State: r.State, Mode: r.Mode, Revision: r.Revision, Owner: scope + ":" + key, Source: r.Source, QuoteID: r.QuoteID, Timezone: r.Timezone, Tiers: r.Tiers, Windows: r.Windows, CachePresent: r.CacheRead != nil, CacheWrite: r.CacheWrite}
	if r.State == "free" {
		return l
	}
	if r.Input != nil {
		l.Prompt = *r.Input
	}
	if r.Output != nil {
		l.Completion = *r.Output
	}
	if r.CacheRead != nil {
		l.Cache = *r.CacheRead
	}
	if r.PerRequest != nil {
		l.PerRequest = *r.PerRequest
	}
	return l
}

// QuoteUnit identifies native site credit separately from display preferences.
// DisplayRate is cosmetic. USDPerUnit is an explicit operator cost declaration.
type QuoteUnit struct {
	Kind            string   `json:"kind"` // iso | site_credit | unknown
	Code            string   `json:"code"`
	QuotaPerUnit    int64    `json:"quota_per_unit,omitempty"`
	DisplayCurrency string   `json:"display_currency,omitempty"`
	DisplaySymbol   string   `json:"display_symbol,omitempty"`
	DisplayRate     *float64 `json:"display_rate,omitempty"`
	USDPerUnit      *float64 `json:"usd_per_unit,omitempty"`
}

// QuoteValues preserve missing dimensions without making them zero-priced.
type QuoteValues struct {
	Mode       string   `json:"mode"`
	Input      *float64 `json:"input_per_million,omitempty"`
	Output     *float64 `json:"output_per_million,omitempty"`
	CacheRead  *float64 `json:"cache_read_per_million,omitempty"`
	CacheWrite *float64 `json:"cache_write_per_million,omitempty"`
	PerRequest *float64 `json:"per_request,omitempty"`
}
type PriceQuoteSnapshot struct {
	ID            int64       `json:"id"`
	ScopeKey      string      `json:"scope_key"`
	Source        string      `json:"source"`
	Purpose       string      `json:"purpose"` // reference | upstream
	Model         string      `json:"model"`
	SiteID        int64       `json:"site_id,omitempty"`
	AccountID     int64       `json:"account_id,omitempty"`
	UpstreamGroup string      `json:"upstream_group,omitempty"`
	Unit          QuoteUnit   `json:"unit"`
	Values        QuoteValues `json:"values"`
	Raw           string      `json:"raw,omitempty"`
	Status        string      `json:"status"` // calculable | partial | unsupported | unknown_group
	ObservedAt    string      `json:"observed_at"`
	ExpiresAt     string      `json:"expires_at,omitempty"`
}
