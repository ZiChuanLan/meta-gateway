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
	Successes   *int64   `json:"successes,omitempty"`
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
func transitReadingKey(model, group string) string {
	if group == "" {
		return model
	}
	return model + "\x00" + group
}

func TransitAvailability(snapshot *TransitSnapshot) map[string]KumaMonitor {
	readings := map[string]KumaMonitor{}
	for _, aggregate := range snapshot.PassiveMonitoring {
		model, group := strings.TrimSpace(aggregate.Model), strings.TrimSpace(aggregate.Group)
		if model == "" {
			continue
		}
		reading := KumaMonitor{ID: transitReadingKey(model, group), Name: model, GroupName: group, Type: MonitorTypePassive}
		if aggregate.Successes != nil && aggregate.Requests > 0 && *aggregate.Successes >= 0 && *aggregate.Successes <= aggregate.Requests {
			reading.Samples = int(aggregate.Requests)
			reading.UpCount = int(*aggregate.Successes)
			reading.Ratio = float64(reading.UpCount) / float64(reading.Samples)
			reading.RatioKnown = true
		} else if aggregate.SuccessRate != nil && *aggregate.SuccessRate >= 0 && *aggregate.SuccessRate <= 1 {
			// A ratio without a published numerator is a ratio, never a fake count.
			reading.Ratio = *aggregate.SuccessRate
			reading.RatioKnown = true
		}
		if aggregate.AvgTTFT != nil {
			ms := int(*aggregate.AvgTTFT)
			reading.AvgPingMS = &ms
		}
		reading.LastBeatAt = parseTransitTime(aggregate.LastSeenAt, snapshot.GeneratedAt)
		readings[reading.ID] = reading
	}
	for _, monitor := range snapshot.Monitors {
		model, group := strings.TrimSpace(monitor.Name), strings.TrimSpace(monitor.Group)
		if model == "" {
			continue
		}
		key := transitReadingKey(model, group)
		if _, exists := readings[key]; exists {
			continue
		}
		reading := KumaMonitor{ID: key, Name: model, GroupName: group, Type: MonitorTypeActive, LastBeatAt: parseTransitTime(snapshot.GeneratedAt, "")}
		if monitor.Availability != nil && *monitor.Availability >= 0 && *monitor.Availability <= 1 {
			reading.Ratio = *monitor.Availability
			reading.RatioKnown = true
		} else if monitor.Available != nil {
			reading.Samples = 1
			reading.WeakEvidence = true
			reading.RatioKnown = true
			if *monitor.Available {
				reading.UpCount = 1
				reading.Ratio = 1
			}
		}
		if monitor.LatencyMS != nil {
			ms := int(*monitor.LatencyMS)
			reading.AvgPingMS = &ms
		}
		readings[key] = reading
	}
	return readings
}
func parseTransitTime(value, fallback string) time.Time {
	if value == "" {
		value = fallback
	}
	at, _ := time.Parse(time.RFC3339Nano, value)
	return at
}

// Preserve each published group/monitor instead of taking an arbitrary group's
// ratio. Snapshots without point history remain aggregate-only evidence.
func TransitSamples(snapshot *TransitSnapshot, siteID, runID int64, collectedAt time.Time) []store.SiteProbeSample {
	readings := TransitAvailability(snapshot)
	byModel := map[string][]KumaMonitor{}
	for _, reading := range readings {
		byModel[reading.Name] = append(byModel[reading.Name], reading)
	}
	out := []store.SiteProbeSample{}
	priced := map[string]bool{}
	appendSample := func(model string, price store.SiteProbePrice, reading *KumaMonitor, group string) {
		sample := store.SiteProbeSample{RunID: runID, SiteID: siteID, RawModel: model, MonitorName: model, MonitorType: MonitorTypePrice,
			ObservedAt: collectedAt, GroupName: group, Price: price}
		if reading != nil {
			sample.MonitorID = reading.ID
			sample.MonitorType = reading.Type
			sample.GroupName = reading.GroupName
			sample.Samples = reading.Samples
			sample.UpCount = reading.UpCount
			sample.Ratio = reading.Ratio
			sample.RatioKnown = reading.RatioKnown
			sample.WeakEvidence = reading.WeakEvidence
			sample.AvgPingMS = reading.AvgPingMS
			// A fetched old snapshot must not look new just because we polled it again.
			sample.ObservedAt = reading.LastBeatAt
		}
		out = append(out, sample)
	}
	for _, model := range snapshot.Models {
		priced[model.Name] = true
		price := transitPrice(model)
		if rs := byModel[model.Name]; len(rs) > 0 {
			for i := range rs {
				appendSample(model.Name, price, &rs[i], rs[i].GroupName)
			}
		} else if len(model.Groups) > 0 {
			for _, group := range model.Groups {
				appendSample(model.Name, price, nil, group)
			}
		} else {
			appendSample(model.Name, price, nil, "")
		}
	}
	for model, rs := range byModel {
		if !priced[model] {
			for i := range rs {
				appendSample(model, store.SiteProbePrice{}, &rs[i], rs[i].GroupName)
			}
		}
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
