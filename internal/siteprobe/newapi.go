package siteprobe

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/lan/meta-gateway/internal/adapters"
)

// defaultQuotaPerUnit is New-API's built-in quota-per-currency-unit factor,
// used when a site does not publish one.
const defaultQuotaPerUnit = 500000

// PriceSnapshot is a site's public price table, normalized through the same
// function the finance sweep uses (internal/adapters/pricing.go).
type PriceSnapshot struct {
	QuotaPerUnit int64
	// Currency and Symbol are what the site declares for its own price table
	// (/api/status: quota_display_type + custom_currency_symbol). They are copied
	// onto every quote: the same formula produces the amount in THAT currency,
	// which is only USD when the site says so.
	Currency string
	Symbol   string
	// Unparsed counts quotes whose published form we refuse to guess at; they
	// are kept in Quotes with Unparsed=true so the UI can show the raw
	// expression instead of a number.
	Unparsed int
	Quotes   map[string]adapters.PriceQuote
}

// AutoSource resolves the probe source a platform implies. new-api publishes
// /api/pricing at its root and Sub2API (with the public-transit fork) publishes
// /.well-known/ai-transit.json — both without credentials. An operator should
// never have to type those URLs: the site's platform already tells us where to
// look, and a source that answers nothing fails the round harmlessly.
func AutoSource(platform, baseURL string) (kind, sourceURL string, ok bool) {
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if base == "" {
		return "", "", false
	}
	switch strings.ToLower(strings.TrimSpace(platform)) {
	case "new-api", "newapi", "one-api", "oneapi", "done-hub", "veloera":
		return SourceNewAPI, base, true
	case "sub2api":
		return SourceSub2APITransit, base, true
	default:
		return "", "", false
	}
}

// currencySymbolFor picks the sign to print for a declared currency.
//
// A known ISO code wins over the deployment's custom symbol: measured, sites
// declare CNY while carrying custom_currency_symbol "¤" (the generic placeholder
// meaning "unspecified"), "Ɇ" (a mis-encoded ¥) or even a fish-cake emoji, and
// "¤30" tells an operator nothing. The custom symbol is only used when the
// currency itself is unknown — that is the case it was meant for.
func currencySymbolFor(currency, declared string) string {
	if symbol := defaultCurrencySymbol(currency); symbol != "" {
		return symbol
	}
	trimmed := strings.TrimSpace(declared)
	if trimmed == "¤" {
		return ""
	}
	return trimmed
}

// defaultCurrencySymbol fills in the sign for the currencies the fleet actually
// declares. It only applies when a site leaves custom_currency_symbol empty.
func defaultCurrencySymbol(currency string) string {
	switch strings.ToUpper(strings.TrimSpace(currency)) {
	case "USD":
		return "$"
	case "CNY", "RMB", "JPY":
		return "¥"
	case "EUR":
		return "€"
	case "GBP":
		return "£"
	case "HKD":
		return "HK$"
	case "KRW":
		return "₩"
	default:
		return ""
	}
}

// ResolveNewAPIBase accepts either a site root or one of its public pricing
// URLs and returns the API base to call.
func ResolveNewAPIBase(rawURL string) (string, error) {
	trimmed := strings.TrimSpace(rawURL)
	if trimmed == "" {
		return "", fmt.Errorf("probe source url is empty")
	}
	parsed, err := url.Parse(trimmed)
	if err != nil || parsed.Host == "" {
		return "", fmt.Errorf("invalid probe source url")
	}
	path := strings.TrimRight(parsed.Path, "/")
	for _, suffix := range []string{"/api/pricing", "/api/status", "/pricing"} {
		if strings.HasSuffix(path, suffix) {
			path = strings.TrimSuffix(path, suffix)
			break
		}
	}
	return parsed.Scheme + "://" + parsed.Host + path, nil
}

// FetchPublicPrices reads a New-API site's public price table (no credentials)
// and normalizes every quote into the gateway's storage units.
func FetchPublicPrices(ctx context.Context, client *http.Client, rawURL string) (*PriceSnapshot, error) {
	base, err := ResolveNewAPIBase(rawURL)
	if err != nil {
		return nil, err
	}
	body, err := getBody(ctx, client, base+"/api/pricing")
	if err != nil {
		return nil, err
	}
	items, err := adapters.ParsePricingPayload(body)
	if err != nil {
		return nil, fmt.Errorf("parse pricing: %w", err)
	}
	snapshot := &PriceSnapshot{QuotaPerUnit: defaultQuotaPerUnit, Quotes: make(map[string]adapters.PriceQuote, len(items))}
	// quota_per_unit lives on the site's public /api/status. A site that hides
	// it still gets the New-API default, which is what its own billing uses.
	var status struct {
		Data struct {
			QuotaPerUnit int64  `json:"quota_per_unit"`
			DisplayType  string `json:"quota_display_type"`
			Symbol       string `json:"custom_currency_symbol"`
			DisplayIn    bool   `json:"display_in_currency"`
		} `json:"data"`
	}
	if statusBody, statusErr := getBody(ctx, client, base+"/api/status"); statusErr == nil {
		if jsonErr := json.Unmarshal(statusBody, &status); jsonErr == nil {
			if status.Data.QuotaPerUnit > 0 {
				snapshot.QuotaPerUnit = status.Data.QuotaPerUnit
			}
			snapshot.Currency = strings.ToUpper(strings.TrimSpace(status.Data.DisplayType))
			snapshot.Symbol = currencySymbolFor(snapshot.Currency, strings.TrimSpace(status.Data.Symbol))
		}
	}
	for _, item := range items {
		quote, ok := adapters.NormalizePrice(item, snapshot.QuotaPerUnit)
		if !ok {
			continue
		}
		// The site-level declaration wins over whatever the price row carried:
		// one table, one currency, and the site is the authority on it.
		if snapshot.Currency != "" {
			quote.Currency = snapshot.Currency
		}
		if snapshot.Symbol != "" {
			quote.CurrencySymbol = snapshot.Symbol
		}
		if quote.Unparsed {
			snapshot.Unparsed++
		}
		snapshot.Quotes[quote.Model] = quote
	}
	return snapshot, nil
}
