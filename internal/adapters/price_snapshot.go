package adapters

import (
	"fmt"
	"time"

	"github.com/lan/meta-gateway/internal/domain"
)

// SnapshotQuote persists only allowlisted pricing facts, never account payloads
// or credentials. Native money and cosmetic display preferences stay separate.
func SnapshotQuote(q PriceQuote, siteID, accountID int64, source string, at time.Time) domain.PriceQuoteSnapshot {
	unit := q.Unit
	if unit.Kind == "" {
		unit = domain.QuoteUnit{Kind: "unknown", Code: q.Currency}
	}
	status := q.Status
	if status == "" {
		status = "calculable"
	}
	if q.Unparsed {
		status = "unsupported"
	}
	values := domain.QuoteValues{Mode: q.Mode}
	if q.Mode == "fixed" {
		v := q.PerRequest
		values.PerRequest = &v
	} else if !q.Unparsed {
		a, b := q.InputPerMillion, q.OutputPerMillion
		values.Input = &a
		values.Output = &b
		if q.HasCacheRead {
			v := q.CacheReadPerMillion
			values.CacheRead = &v
		}
		if q.HasCacheWrite {
			v := q.CacheWritePerMillion
			values.CacheWrite = &v
		}
	}
	return domain.PriceQuoteSnapshot{ScopeKey: fmt.Sprintf("%s:%d:%d:%s:%s", source, siteID, accountID, q.UpstreamGroup, q.Model), Source: source, Purpose: "upstream", Model: q.Model, SiteID: siteID, AccountID: accountID, UpstreamGroup: q.UpstreamGroup, Unit: unit, Values: values, Raw: q.Raw, Status: status, ObservedAt: at.UTC().Format(time.RFC3339Nano), ExpiresAt: at.Add(24 * time.Hour).UTC().Format(time.RFC3339Nano)}
}
