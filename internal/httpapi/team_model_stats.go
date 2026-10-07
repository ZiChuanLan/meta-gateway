package httpapi

import (
	"net/http"
	"sort"
	"time"

	"github.com/lan/meta-gateway/internal/store"
)

// The member's per-model figures, and the site's per-model probe state.
//
// These are two different questions and they are answered from two different
// sources on purpose:
//
//   - "how did MY traffic go on this model" is the account's own request log,
//     scoped by user id — the same rows the log page lists.
//   - "are the upstreams behind this model up right now" is the gateway's own
//     probe data. It is not derived from the member's requests, and a member
//     who has never called a model still gets a true answer for it.
//
// Nothing here names an upstream, a channel or a URL: a member learns how many
// upstreams serve a model and whether they answered a probe, which is what the
// question needs, and not which site the gateway buys from.
const (
	// memberModelStatWindow is the default window for the per-model figures. The
	// model list has no range picker, so the default has to be small enough to
	// stay cheap on a busy account and long enough to answer "is this model
	// worth using".
	memberModelStatWindow = 24 * time.Hour
	// memberProbeWindow is how much probe history the availability ratio covers.
	memberProbeWindow = 24 * time.Hour
)

// memberModelStatsView carries the window with the figures: a rate without the
// span it covers is not a fact, and the console prints both.
type memberModelStatsView struct {
	Since string `json:"since"`
	Until string `json:"until"`
	// Unassigned counts windowed requests that reached no upstream at all — a
	// refusal by a quota, a policy or the rate limiter. They belong to no model,
	// so counting them into one would be a lie and dropping them would flatter
	// every success rate.
	Unassigned int                     `json:"unassigned"`
	Models     []store.MemberModelStat `json:"models"`
}

func (h *TeamHandler) myModelStats(w http.ResponseWriter, r *http.Request) {
	s, _ := h.settings()
	if !s.Branding.ShowUsage {
		teamFail(w, 403, "usage_disabled")
		return
	}
	since, until, ok := parseTimeRange(w, r.URL.Query())
	if !ok {
		return
	}
	now := time.Now().UTC()
	if since == nil {
		from := now.Add(-memberModelStatWindow)
		since = &from
	}
	if until == nil {
		until = &now
	}
	models, unassigned, err := h.db.MemberModelStats(teamActor(r).User.ID, since, until)
	if err != nil {
		teamFail(w, 500, "usage_unavailable")
		return
	}
	if models == nil {
		models = []store.MemberModelStat{}
	}
	teamJSON(w, 200, memberModelStatsView{
		Since:      since.UTC().Format(time.RFC3339),
		Until:      until.UTC().Format(time.RFC3339),
		Unassigned: unassigned,
		Models:     models,
	})
}

// memberModelHealthView is one model's upstream state as a member may see it.
type memberModelHealthView struct {
	Model string `json:"model"`
	// Upstreams is how many enabled upstreams serve this model for this member;
	// Probed is how many of them have ever answered a probe.
	Upstreams int `json:"upstreams"`
	Probed    int `json:"probed"`
	Healthy   int `json:"healthy"`
	// Samples is the probe history the ratio covers. Availability is meaningless
	// when it is 0 — the console renders "不适用" instead of a fabricated 100%.
	Samples      int     `json:"samples"`
	OKSamples    int     `json:"ok_samples"`
	Availability float64 `json:"availability"`
	AvgLatencyMS int     `json:"avg_latency_ms"`
	LastProbedAt string  `json:"last_probed_at"`
}

func (h *TeamHandler) myModelAvailability(w http.ResponseWriter, r *http.Request) {
	p, err := h.policy(teamActor(r).User.PolicyID)
	if err != nil {
		teamFail(w, 503, "policy_unavailable")
		return
	}
	access := policyAccess(p)
	since := time.Now().UTC().Add(-memberProbeWindow)
	rows, err := h.db.MemberModelProbes(&since)
	if err != nil {
		teamFail(w, 500, "health_unavailable")
		return
	}

	type aggregate struct {
		view         memberModelHealthView
		okSamples    int
		probeLatency float64
		probeWeight  int
		stateLatency int
		stateCount   int
	}
	byModel := map[string]*aggregate{}
	for _, row := range rows {
		// The policy decides what the member may reach; a model they cannot call
		// is not their business, and its absence here is not a gap.
		if !access.AllowsModel(row.Model) || !access.AllowsGrant(row.GrantID) {
			continue
		}
		entry := byModel[row.Model]
		if entry == nil {
			entry = &aggregate{view: memberModelHealthView{Model: row.Model}}
			byModel[row.Model] = entry
		}
		entry.view.Upstreams++
		if row.HasState {
			entry.view.Probed++
			if row.OK {
				entry.view.Healthy++
				entry.stateLatency += row.LatencyMS
				entry.stateCount++
			}
		}
		if row.ProbedAt > entry.view.LastProbedAt {
			entry.view.LastProbedAt = row.ProbedAt
		}
		entry.view.Samples += row.Samples
		entry.okSamples += row.OKSamples
		if row.Samples > 0 {
			entry.probeLatency += row.AvgLatencyMS * float64(row.Samples)
			entry.probeWeight += row.Samples
		}
	}

	items := make([]memberModelHealthView, 0, len(byModel))
	for _, entry := range byModel {
		view := entry.view
		view.OKSamples = entry.okSamples
		if view.Samples > 0 {
			view.Availability = float64(entry.okSamples) / float64(view.Samples)
		}
		// Prefer the probe window's own latency; fall back to the latest state's
		// latency when no probe history was recorded for the window at all.
		if entry.probeWeight > 0 {
			view.AvgLatencyMS = int(entry.probeLatency/float64(entry.probeWeight) + 0.5)
		} else if entry.stateCount > 0 {
			view.AvgLatencyMS = (entry.stateLatency + entry.stateCount/2) / entry.stateCount
		}
		items = append(items, view)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Model < items[j].Model })
	teamJSON(w, 200, items)
}
