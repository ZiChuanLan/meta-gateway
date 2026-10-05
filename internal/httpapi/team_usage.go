package httpapi

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/lan/meta-gateway/internal/store"
)

// The member app's overview reads the same usage_records the console does, with
// one difference that is the whole point of these handlers: the scope is the
// signed-in account, not the gateway.
//
// These are not the admin endpoints behind a different path. The console asks
// "what did this key spend" against the whole site; a member asks "what did I
// spend" and must never see anyone else's traffic. Both go through
// store.UsageScope, so the two dimensions cannot drift apart — and a member's
// figure is necessarily the sum over their own keys, which is also what their
// bill is.
//
// The dimensions differ in a way worth stating: a member's view is by ACCOUNT
// (every key that person holds), while the console usually looks at one KEY.
// Asking by account is the only honest way to answer "how much have I used",
// because a person may hold several keys.
func (h *TeamHandler) myUsageSummary(w http.ResponseWriter, r *http.Request) {
	user := teamActor(r).User
	since, until, ok := parseTimeRange(w, r.URL.Query())
	if !ok {
		return
	}
	summary, err := h.db.Usage.SummaryRange(store.UsageScope{UserID: &user.ID}, since, until)
	if err != nil {
		teamFail(w, 500, "usage_unavailable")
		return
	}
	teamJSON(w, 200, summary)
}

// myDisplaySettings is the member's read of the money display settings.
//
// The console reads the same values from /admin/display-settings, which sits
// behind the admin gate — so a member's console would otherwise format every
// amount with the default symbol while the operator sees their own currency.
// Symbol and rate are presentation only, not policy, so reading them is not an
// operator privilege.
func (h *TeamHandler) myDisplaySettings(w http.ResponseWriter, r *http.Request) {
	if h.db == nil || h.db.Display == nil {
		teamJSON(w, 200, currencyView{Symbol: store.DefaultCurrencySymbol, Rate: 1})
		return
	}
	teamJSON(w, 200, currencyFor(h.db.Display.Get()))
}

func (h *TeamHandler) myUsageSeries(w http.ResponseWriter, r *http.Request) {
	user := teamActor(r).User
	query := r.URL.Query()
	since, until, ok := parseTimeRange(w, query)
	if !ok {
		return
	}
	start, end, buckets := seriesWindow(query, since, until)
	series, err := h.db.Usage.Series(store.UsageScope{UserID: &user.ID}, start, end, buckets)
	if err != nil {
		teamFail(w, 500, "usage_unavailable")
		return
	}
	teamJSON(w, 200, series)
}

func (h *TeamHandler) myTopModels(w http.ResponseWriter, r *http.Request) {
	user := teamActor(r).User
	query := r.URL.Query()
	since, until, ok := parseTimeRange(w, query)
	if !ok {
		return
	}
	limit := 8
	if raw := strings.TrimSpace(query.Get("limit")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 && parsed <= 50 {
			limit = parsed
		}
	}
	rows, err := h.db.Usage.TopModels(store.UsageScope{UserID: &user.ID}, since, until, limit)
	if err != nil {
		teamFail(w, 500, "usage_unavailable")
		return
	}
	if rows == nil {
		rows = []store.ModelUsage{}
	}
	teamJSON(w, 200, rows)
}

// myLatencyHistogram shares the admin buckets and percentiles, with a mandatory
// server-derived account scope. Disabling usage hides both list and histogram.
func (h *TeamHandler) myLatencyHistogram(w http.ResponseWriter, r *http.Request) {
	settings, err := h.settings()
	if err != nil {
		teamFail(w, 500, "settings_unavailable")
		return
	}
	if !settings.Branding.ShowUsage {
		teamFail(w, 403, "usage_disabled")
		return
	}
	query := r.URL.Query()
	since, until, ok := parseTimeRange(w, query)
	if !ok {
		return
	}
	sample := 1000
	if value, err := strconv.Atoi(query.Get("sample")); err == nil && value > 0 && value <= maxHistogramSample {
		sample = value
	}
	hist, err := h.db.ProxyLog.MemberLatencyHistogram(teamActor(r).User.ID, sample, since, until)
	if err != nil {
		teamFail(w, 500, "usage_unavailable")
		return
	}
	teamJSON(w, 200, hist)
}
