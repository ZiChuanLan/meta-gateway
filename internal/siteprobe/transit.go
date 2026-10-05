package siteprobe

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/lan/meta-gateway/internal/store"
)

// The Sub2API "public transit" discovery protocol: a site that wants to be read
// machine-publishes /.well-known/ai-transit.json pointing at a snapshot of its
// own probe data (prices, groups, availability). Reading it costs no tokens and
// no credentials, which is the only kind of source this package accepts.
//
// The discovery file and the snapshot are served by the site on purpose — the
// well-known name, the schema version and the capability list are the site's own
// statement that this data is public. A site that does not publish them simply
// has nothing to read: the collector reports that instead of guessing.

// TransitDiscovery is the /.well-known/ai-transit.json document.
type TransitDiscovery struct {
	SchemaVersion string `json:"schema_version"`
	System        string `json:"system"`
	SnapshotURL   string `json:"snapshot_url"`
	SnapshotV2URL string `json:"snapshot_v2_url"`
	HomepageURL   string `json:"homepage_url"`
	GeneratedAt   string `json:"generated_at"`
}

// TransitSnapshot is the union of the V1 (active probing) and V2 (passive
// aggregation) snapshot shapes. Both carry prices and groups; availability
// differs:
//
//   - V1 (`monitors`): the site probed its own channels, so each monitor has a
//     measured availability.
//   - V2 (`passive_monitoring`): the site aggregated its real traffic, which is
//     even better evidence — but it says nothing about OUR key, so it is still
//     displayed as the site's own numbers.
type TransitSnapshot struct {
	SchemaVersion string `json:"schema_version"`
	System        string `json:"system"`
	GeneratedAt   string `json:"generated_at"`
	Mode          string `json:"mode,omitempty"`

	Groups []TransitGroup `json:"groups,omitempty"`
	Models []TransitModel `json:"models,omitempty"`

	Monitors          []TransitMonitor          `json:"monitors,omitempty"`
	PassiveMonitoring []TransitPassiveAggregate `json:"passive_monitoring,omitempty"`
}

// TransitGroup is one public group with its multipliers.
type TransitGroup struct {
	Name         string   `json:"name"`
	Platform     string   `json:"platform,omitempty"`
	RechargeRate float64  `json:"recharge_rate_multiplier,omitempty"`
	GroupRate    float64  `json:"group_rate_multiplier,omitempty"`
	ModelCount   int      `json:"model_count,omitempty"`
	Models       []string `json:"models,omitempty"`
	CacheHitRate *float64 `json:"cache_hit_rate,omitempty"`
}

// TransitModel is one model's published price.
type TransitModel struct {
	Name             string   `json:"name"`
	BillingMode      string   `json:"billing_mode,omitempty"`
	InputPrice       float64  `json:"input_price,omitempty"`
	OutputPrice      float64  `json:"output_price,omitempty"`
	CacheInputPrice  float64  `json:"cache_input_price,omitempty"`
	CacheCreatePrice float64  `json:"cache_create_price,omitempty"`
	PerRequestPrice  float64  `json:"per_request_price,omitempty"`
	Currency         string   `json:"currency,omitempty"`
	Groups           []string `json:"groups,omitempty"`
	// GroupRate is the multiplier of the cheapest group that serves it; the
	// snapshot is the site's own view of what a request costs.
	GroupRate float64 `json:"group_rate,omitempty"`
}

// TransitMonitor is one V1 availability reading.
type TransitMonitor struct {
	Name         string   `json:"name"`
	Status       string   `json:"status,omitempty"`
	Available    *bool    `json:"available,omitempty"`
	Availability *float64 `json:"availability,omitempty"`
	LatencyMS    *float64 `json:"latency_ms,omitempty"`
	Group        string   `json:"group,omitempty"`
}

// TransitPassiveAggregate is one V2 availability reading: real-traffic
// aggregates for a model (optionally per group) over the snapshot window.
type TransitPassiveAggregate struct {
	Model       string   `json:"model"`
	Group       string   `json:"group,omitempty"`
	Requests    int64    `json:"requests,omitempty"`
	Successes   int64    `json:"successes,omitempty"`
	Errors      int64    `json:"errors,omitempty"`
	SuccessRate *float64 `json:"success_rate,omitempty"`
	AvgLatency  *float64 `json:"avg_latency_ms,omitempty"`
	AvgTTFT     *float64 `json:"avg_ttft_ms,omitempty"`
	LastSeenAt  string   `json:"last_seen_at,omitempty"`
}

// ResolveTransitDiscovery finds the discovery document for a site root or any
// of its pages and returns the snapshot URL to read.
//
// Accepting a deep URL (a pricing page, the public transit page) matters because
// operators paste what they have; the discovery file is always read from the
// host root, which is where the protocol defines it.
func ResolveTransitDiscovery(ctx context.Context, client *http.Client, rawURL string) (*TransitDiscovery, string, error) {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || parsed.Host == "" {
		return nil, "", fmt.Errorf("invalid probe source url")
	}
	base := parsed.Scheme + "://" + parsed.Host
	body, err := getBody(ctx, client, base+"/.well-known/ai-transit.json")
	if err != nil {
		return nil, "", err
	}
	var discovery TransitDiscovery
	if err := json.Unmarshal(body, &discovery); err != nil {
		return nil, "", fmt.Errorf("the site publishes no transit discovery document: %w", err)
	}
	if strings.TrimSpace(discovery.SnapshotURL) == "" {
		return nil, "", fmt.Errorf("the transit discovery document names no snapshot url")
	}
	return &discovery, discovery.SnapshotURL, nil
}

// FetchTransit reads a Sub2API site's public snapshot. mode reports which shape
// the site serves (v1 active probing / v2 passive aggregation), because the two
// carry availability in different fields.
func FetchTransit(ctx context.Context, client *http.Client, snapshotURL string) (*TransitSnapshot, string, error) {
	var snapshot TransitSnapshot
	if err := getJSON(ctx, client, snapshotURL, &snapshot); err != nil {
		return nil, "", err
	}
	mode := snapshot.Mode
	if mode == "" {
		mode = "v1"
		if len(snapshot.PassiveMonitoring) > 0 {
			mode = "v2"
		}
	}
	return &snapshot, mode, nil
}

// TransitAvailability folds the snapshot's availability readings into one
// reading per model, preferring V2 (real traffic) over V1 (active probe) when a
// model appears in both. Exported so the admin detect endpoint can preview what
// the site currently reports.
func TransitAvailability(snapshot *TransitSnapshot) map[string]KumaMonitor {
	readings := make(map[string]KumaMonitor)
	for _, aggregate := range snapshot.PassiveMonitoring {
		model := strings.TrimSpace(aggregate.Model)
		if model == "" {
			continue
		}
		samples := aggregate.Requests
		up := aggregate.Successes
		if up == 0 && aggregate.SuccessRate != nil {
			up = int64(*aggregate.SuccessRate * float64(samples))
		}
		reading := KumaMonitor{Name: model, GroupName: aggregate.Group, Type: MonitorTypePassive}
		if samples > 0 {
			reading.Samples = int(samples)
			reading.UpCount = int(up)
			reading.Ratio = float64(up) / float64(samples)
		}
		if aggregate.AvgTTFT != nil {
			ms := int(*aggregate.AvgTTFT)
			reading.AvgPingMS = &ms
		}
		// Real traffic is the site's own users' experience — the strongest
		// evidence a site can publish about itself, and still not a measurement
		// of our keys.
		reading.WeakEvidence = false
		readings[model] = reading
	}
	for _, monitor := range snapshot.Monitors {
		model := strings.TrimSpace(monitor.Name)
		if model == "" {
			continue
		}
		if _, hasPassive := readings[model]; hasPassive {
			continue
		}
		reading := KumaMonitor{Name: model, GroupName: monitor.Group, Type: MonitorTypeActive}
		switch {
		case monitor.Availability != nil:
			reading.Samples = 100
			reading.UpCount = int(*monitor.Availability * 100)
			reading.Ratio = *monitor.Availability
		case monitor.Available != nil:
			// A bare up/down flag: one sample. It proves the site answered once,
			// which is weak evidence — never enough to park a member on its own.
			if *monitor.Available {
				reading.UpCount = 1
				reading.Ratio = 1
			}
			reading.Samples = 1
			reading.WeakEvidence = true
		}
		if monitor.LatencyMS != nil {
			ms := int(*monitor.LatencyMS)
			reading.AvgPingMS = &ms
		}
		readings[model] = reading
	}
	return readings
}

// TransitSamples converts a snapshot into storable samples: prices for every
// published model, availability for every monitored one.
func TransitSamples(snapshot *TransitSnapshot, siteID, runID int64, observedAt time.Time) []store.SiteProbeSample {
	out := make([]store.SiteProbeSample, 0, len(snapshot.Models)+len(snapshot.Monitors))
	availability := TransitAvailability(snapshot)
	priced := make(map[string]bool, len(snapshot.Models))
	for _, model := range snapshot.Models {
		priced[model.Name] = true
		sample := store.SiteProbeSample{
			RunID: runID, SiteID: siteID,
			MonitorName: model.Name, MonitorType: MonitorTypePrice,
			RawModel: model.Name, ObservedAt: observedAt,
			Price: transitPrice(model),
		}
		if reading, ok := availability[model.Name]; ok {
			sample.MonitorType = reading.Type
			sample.Samples = reading.Samples
			sample.UpCount = reading.UpCount
			sample.Ratio = reading.Ratio
			sample.AvgPingMS = reading.AvgPingMS
			sample.WeakEvidence = reading.WeakEvidence
			sample.GroupName = reading.GroupName
		}
		out = append(out, sample)
	}
	// Monitors without a published price still carry availability evidence.
	for name, reading := range availability {
		if priced[name] {
			continue
		}
		out = append(out, store.SiteProbeSample{
			RunID: runID, SiteID: siteID,
			MonitorID: name, MonitorName: name, MonitorType: reading.Type,
			RawModel: name, ObservedAt: observedAt,
			Samples: reading.Samples, UpCount: reading.UpCount, Ratio: reading.Ratio,
			AvgPingMS: reading.AvgPingMS, WeakEvidence: reading.WeakEvidence,
			GroupName: reading.GroupName,
		})
	}
	return out
}

// transitPrice converts a published model price into the stored shape. The
// snapshot prices are per million tokens in the site's display currency; the
// currency rides along and the normalizer does not guess an exchange rate.
func transitPrice(model TransitModel) store.SiteProbePrice {
	price := store.SiteProbePrice{
		Currency:   strings.TrimSpace(model.Currency),
		GroupRatio: model.GroupRate,
		Raw:        model.BillingMode,
	}
	if model.PerRequestPrice > 0 {
		price.Mode = "fixed"
		price.PerRequest = model.PerRequestPrice
		return price
	}
	if model.InputPrice > 0 || model.OutputPrice > 0 {
		price.Mode = "token"
		price.InputPerMillion = model.InputPrice
		price.OutputPerMillion = model.OutputPrice
		price.CacheReadPerMillion = model.CacheInputPrice
	}
	return price
}
