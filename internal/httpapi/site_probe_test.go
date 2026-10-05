package httpapi_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lan/meta-gateway/internal/config"
	"github.com/lan/meta-gateway/internal/crypto"
	"github.com/lan/meta-gateway/internal/httpapi"
	"github.com/lan/meta-gateway/internal/store"
)

// putJSON is put() with the response body kept: the probe-source endpoint
// answers with the stored site, and the test asserts on it.
func putJSON(t *testing.T, url string, payload any) []byte {
	t.Helper()
	encoded, _ := json.Marshal(payload)
	req, _ := http.NewRequest(http.MethodPut, url, bytes.NewReader(encoded))
	req.Header.Set("Authorization", "Bearer admin-test")
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT %s = %d: %s", url, resp.StatusCode, body)
	}
	return body
}

func putStatus(t *testing.T, url string, payload any) int {
	t.Helper()
	encoded, _ := json.Marshal(payload)
	req, _ := http.NewRequest(http.MethodPut, url, bytes.NewReader(encoded))
	req.Header.Set("Authorization", "Bearer admin-test")
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	_, _ = io.ReadAll(resp.Body)
	return resp.StatusCode
}

// kumaStub serves the two public endpoints a real Uptime Kuma status page
// exposes, in the shape one actually returns: heartbeats carry a null ping on
// every down beat, and a monitor may be listed under a group.
func kumaStub(t *testing.T, up, down *atomic.Int64, monitor string) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/status-page/ai":
			_, _ = w.Write([]byte(`{"config":{"slug":"ai","title":"AI"},
				"publicGroupList":[{"id":6,"name":"GLM","weight":8,
				"monitorList":[{"id":44,"name":"` + monitor + `","type":"keyword"}]}]}`))
		case r.URL.Path == "/api/status-page/heartbeat/ai":
			beats := []string{}
			now := time.Now().UTC()
			for i := int64(0); i < up.Load(); i++ {
				beats = append(beats, `{"status":1,"time":"`+now.Add(-time.Duration(i)*time.Minute).Format("2006-01-02 15:04:05.000")+`","msg":"","ping":120}`)
			}
			for i := int64(0); i < down.Load(); i++ {
				beats = append(beats, `{"status":0,"time":"`+now.Add(-time.Duration(i)*time.Minute).Format("2006-01-02 15:04:05.000")+`","msg":"","ping":null}`)
			}
			_, _ = w.Write([]byte(`{"heartbeatList":{"44":[` + strings.Join(beats, ",") + `]},"uptimeList":{}}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

type siteProbeReport struct {
	Policy struct {
		RatioThreshold float64 `json:"ratio_threshold"`
		LowRounds      int     `json:"low_rounds"`
	} `json:"policy"`
	Rows []struct {
		Route              string `json:"route"`
		Match              string `json:"match"`
		Verdict            string `json:"verdict"`
		AvailabilitySource string `json:"availability_source"`
		Members            []struct {
			ChannelName  string `json:"channel_name"`
			Enabled      bool   `json:"enabled"`
			AutoDisabled bool   `json:"auto_disabled"`
		} `json:"members"`
		Rounds []struct {
			Ratio   float64 `json:"ratio"`
			Samples int     `json:"samples"`
		} `json:"rounds"`
	} `json:"rows"`
	Sites []struct {
		SiteName         string `json:"site_name"`
		MonitorCount     int    `json:"monitor_count"`
		LastRunStatus    string `json:"last_run_status"`
		ProbeSourceError string `json:"probe_last_error"`
	} `json:"sites"`
}

type siteProbeApplyResponse struct {
	Actions []struct {
		Route        string `json:"route"`
		Kind         string `json:"kind"`
		ChannelName  string `json:"channel_name"`
		Reason       string `json:"reason"`
		MembersMoved int    `json:"members_moved"`
		Skipped      string `json:"skipped"`
	} `json:"actions"`
}

// TestSiteProbeCollectAndApply covers the whole external-probe path through the
// admin API: configure a source, collect a round from a public status page,
// watch the verdict need consecutive rounds, and only then park the member.
func TestSiteProbeCollectAndApply(t *testing.T) {
	dataDir := t.TempDir()
	db, _ := store.Open(dataDir)
	defer db.Close()
	enc, _ := crypto.New("site-probe-test-master-key-32-ch!")
	cfg := &config.Config{AdminToken: "admin-test", MetricsToken: "metrics-test", BackupDir: filepath.Join(dataDir, "backups"), MaxAdminBodyBytes: 1 << 20, AuditRetentionDays: 90, AuditRetentionRows: 100000, Cooldown: time.Second}
	server := httptest.NewServer(httpapi.NewTestRouter(t, cfg, db, enc))
	defer server.Close()

	var up, down atomic.Int64
	up.Store(3)
	down.Store(7)
	kuma := kumaStub(t, &up, &down, "z-ai/glm-5.2")

	var site struct{ ID int64 }
	json.Unmarshal(post(t, server.URL+"/admin/sites", map[string]any{"name": "公益站A", "base_url": "https://a.example", "platform": "new-api", "status": "enabled"}), &site)
	var cred struct{ ID int64 }
	json.Unmarshal(post(t, server.URL+"/admin/sites/"+itoa(site.ID)+"/credentials", map[string]any{"kind": "api_key", "secret": "sk-test", "status": "enabled"}), &cred)
	var channel struct{ ID int64 }
	json.Unmarshal(post(t, server.URL+"/admin/channels", map[string]any{"site_id": site.ID, "credential_id": cred.ID, "name": "A-key", "base_url": "https://a.example", "type_hint": "new-api", "status": "enabled"}), &channel)

	// A malformed source is rejected before it can be stored.
	if status := putStatus(t, server.URL+"/admin/site-probe/source", map[string]any{"site_id": site.ID, "kind": "nonsense", "url": kuma.URL, "enabled": true}); status != http.StatusBadRequest {
		t.Fatalf("unknown kind = %d, want 400", status)
	}
	if status := putStatus(t, server.URL+"/admin/site-probe/source", map[string]any{"site_id": site.ID, "kind": "uptime_kuma", "url": kuma.URL + "/status/ai", "config": "not json", "enabled": true}); status != http.StatusBadRequest {
		t.Fatalf("unparseable config = %d, want 400", status)
	}
	if status := putStatus(t, server.URL+"/admin/site-probe/source", map[string]any{"site_id": site.ID, "kind": "uptime_kuma", "url": kuma.URL + "/status/ai", "config": `{"autoapply":true}`, "enabled": true}); status != http.StatusBadRequest {
		t.Fatalf("unknown config key = %d, want 400", status)
	}
	var stored struct {
		ProbeSourceKind    string `json:"probe_source_kind"`
		ProbeSourceEnabled bool   `json:"probe_source_enabled"`
	}
	json.Unmarshal(putJSON(t, server.URL+"/admin/site-probe/source", map[string]any{"site_id": site.ID, "kind": "uptime_kuma", "url": kuma.URL + "/status/ai", "enabled": true}), &stored)
	if stored.ProbeSourceKind != "uptime_kuma" || !stored.ProbeSourceEnabled {
		t.Fatalf("stored source = %+v", stored)
	}

	var route struct{ ID int64 }
	json.Unmarshal(post(t, server.URL+"/admin/routes", map[string]any{"model_pattern": "glm-5.2", "enabled": true}), &route)
	post(t, server.URL+"/admin/routes/"+itoa(route.ID)+"/members", map[string]any{"channel_id": channel.ID, "priority": 0, "weight": 100, "enabled": true, "auto": true})

	// Round 1: a single low reading is not enough to act on.
	var collect struct {
		Runs []struct {
			Status       string `json:"status"`
			MonitorCount int    `json:"monitor_count"`
		} `json:"runs"`
		Failed int `json:"failed"`
	}
	json.Unmarshal(post(t, server.URL+"/admin/site-probe/collect", map[string]any{"site_ids": []int64{site.ID}}), &collect)
	if len(collect.Runs) != 1 || collect.Runs[0].Status != "ok" || collect.Runs[0].MonitorCount != 1 || collect.Failed != 0 {
		t.Fatalf("collect = %+v", collect)
	}
	report := readReport(t, server.URL)
	if len(report.Rows) != 1 {
		t.Fatalf("rows = %+v, want the one matched model", report.Rows)
	}
	row := report.Rows[0]
	if row.Route != "glm-5.2" || row.Match != "normalized" || row.Verdict != "pending" {
		t.Fatalf("first round = %+v, want a normalized match still pending", row)
	}
	if len(row.Rounds) != 1 || row.Rounds[0].Samples != 10 || row.Rounds[0].Ratio != 0.3 {
		t.Fatalf("rounds = %+v, want 3/10", row.Rounds)
	}
	if len(row.Members) != 1 || row.Members[0].ChannelName != "A-key" {
		t.Fatalf("members = %+v", row.Members)
	}

	// A dry run over one round proposes nothing.
	var dry siteProbeApplyResponse
	json.Unmarshal(post(t, server.URL+"/admin/site-probe/apply", map[string]any{"dry_run": true}), &dry)
	if len(dry.Actions) != 0 {
		t.Fatalf("actions after one round = %+v", dry.Actions)
	}

	// Round 2: two consecutive low rounds are what the policy acts on.
	json.Unmarshal(post(t, server.URL+"/admin/site-probe/collect", map[string]any{"site_ids": []int64{site.ID}}), &collect)
	report = readReport(t, server.URL)
	if report.Rows[0].Verdict != "low" {
		t.Fatalf("verdict after two rounds = %q, want low", report.Rows[0].Verdict)
	}
	if report.Rows[0].Members[0].AutoDisabled {
		t.Fatal("a dry run must not have disabled the member")
	}

	body := post(t, server.URL+"/admin/site-probe/apply", map[string]any{"dry_run": true})
	var preview siteProbeApplyResponse
	json.Unmarshal(body, &preview)
	if len(preview.Actions) != 1 || preview.Actions[0].Kind != "disable" || preview.Actions[0].Route != "glm-5.2" {
		t.Fatalf("dry run = %+v, want one disable", preview.Actions)
	}
	if !strings.Contains(preview.Actions[0].Reason, "site probe") {
		t.Fatalf("reason = %q, want the site-probe explanation", preview.Actions[0].Reason)
	}
	if report = readReport(t, server.URL); report.Rows[0].Members[0].AutoDisabled {
		t.Fatal("the dry run changed the member")
	}

	json.Unmarshal(post(t, server.URL+"/admin/site-probe/apply", map[string]any{"dry_run": false}), &preview)
	if len(preview.Actions) != 1 || preview.Actions[0].MembersMoved != 1 || preview.Actions[0].Skipped != "" {
		t.Fatalf("apply = %+v, want the member moved", preview.Actions)
	}
	report = readReport(t, server.URL)
	if member := report.Rows[0].Members[0]; member.Enabled || !member.AutoDisabled {
		t.Fatalf("member after apply = %+v, want the probe-disabled state", member)
	}

	// Clearing the source stops collection without touching member state.
	if status := deleteStatus(t, server.URL+"/admin/site-probe/source/"+itoa(site.ID)); status != http.StatusOK {
		t.Fatalf("clear source = %d, want 200", status)
	}
	var sites []struct {
		ProbeSourceKind    string `json:"probe_source_kind"`
		ProbeSourceEnabled bool   `json:"probe_source_enabled"`
	}
	json.Unmarshal(get(t, server.URL+"/admin/sites"), &sites)
	if len(sites) != 1 || sites[0].ProbeSourceKind != "" || sites[0].ProbeSourceEnabled {
		t.Fatalf("sites after clear = %+v", sites)
	}
	if report = readReport(t, server.URL); !report.Rows[0].Members[0].AutoDisabled {
		t.Fatal("clearing the source recovered a member; only a healthy reading may do that")
	}
}

// A source that cannot be collected must leave every member alone: the feature
// is fail-open, and a broken page is not evidence of a broken upstream.
func TestSiteProbeFailedCollectionIsInert(t *testing.T) {
	dataDir := t.TempDir()
	db, _ := store.Open(dataDir)
	defer db.Close()
	enc, _ := crypto.New("site-probe-test-master-key-32-ch!")
	cfg := &config.Config{AdminToken: "admin-test", MetricsToken: "metrics-test", BackupDir: filepath.Join(dataDir, "backups"), MaxAdminBodyBytes: 1 << 20, AuditRetentionDays: 90, AuditRetentionRows: 100000, Cooldown: time.Second}
	server := httptest.NewServer(httpapi.NewTestRouter(t, cfg, db, enc))
	defer server.Close()

	var site struct{ ID int64 }
	json.Unmarshal(post(t, server.URL+"/admin/sites", map[string]any{"name": "dead", "base_url": "https://dead.example", "platform": "new-api", "status": "enabled"}), &site)
	var cred struct{ ID int64 }
	json.Unmarshal(post(t, server.URL+"/admin/sites/"+itoa(site.ID)+"/credentials", map[string]any{"kind": "api_key", "secret": "sk-test", "status": "enabled"}), &cred)
	var channel struct{ ID int64 }
	json.Unmarshal(post(t, server.URL+"/admin/channels", map[string]any{"site_id": site.ID, "credential_id": cred.ID, "name": "dead-key", "base_url": "https://dead.example", "type_hint": "new-api", "status": "enabled"}), &channel)
	var route struct{ ID int64 }
	json.Unmarshal(post(t, server.URL+"/admin/routes", map[string]any{"model_pattern": "glm-5.2", "enabled": true}), &route)
	post(t, server.URL+"/admin/routes/"+itoa(route.ID)+"/members", map[string]any{"channel_id": channel.ID, "enabled": true, "auto": true, "weight": 100})

	// A closed port: the round fails, and nothing else happens.
	json.Unmarshal(putJSON(t, server.URL+"/admin/site-probe/source", map[string]any{"site_id": site.ID, "kind": "uptime_kuma", "url": "http://127.0.0.1:1/status/ai", "enabled": true}), &struct{}{})

	var collect struct {
		Runs []struct {
			Status string `json:"status"`
			Error  string `json:"error"`
		} `json:"runs"`
		Failed int `json:"failed"`
	}
	json.Unmarshal(post(t, server.URL+"/admin/site-probe/collect", map[string]any{"site_ids": []int64{site.ID}}), &collect)
	if len(collect.Runs) != 1 || collect.Runs[0].Status != "failed" || collect.Runs[0].Error == "" || collect.Failed != 1 {
		t.Fatalf("collect = %+v, want a recorded failure", collect)
	}
	report := readReport(t, server.URL)
	// The site's served model is listed (so the operator sees it and the missing
	// data), but with no verdict and no evidence source: a broken probe page is
	// not evidence about the upstream.
	for _, row := range report.Rows {
		if row.Verdict != "no_data" || row.AvailabilitySource != "" {
			t.Fatalf("row = %+v, want no_data with no source", row)
		}
	}
	if len(report.Sites) != 1 || report.Sites[0].ProbeSourceError == "" || report.Sites[0].LastRunStatus != "failed" {
		t.Fatalf("sites = %+v, want the failure surfaced", report.Sites)
	}
	var applied siteProbeApplyResponse
	json.Unmarshal(post(t, server.URL+"/admin/site-probe/apply", map[string]any{"dry_run": false}), &applied)
	if len(applied.Actions) != 0 {
		t.Fatalf("actions = %+v, want none", applied.Actions)
	}
}

// closeTo compares floats that crossed a ÷1000 unit conversion, where exact
// equality is a coin flip.
func closeTo(got, want float64) bool {
	diff := got - want
	if diff < 0 {
		diff = -diff
	}
	return diff < 1e-12
}

func readReport(t *testing.T, baseURL string) siteProbeReport {
	t.Helper()
	var report siteProbeReport
	json.Unmarshal(get(t, baseURL+"/admin/site-probe/report"), &report)
	return report
}

func deleteStatus(t *testing.T, url string) int {
	t.Helper()
	req, _ := http.NewRequest(http.MethodDelete, url, nil)
	req.Header.Set("Authorization", "Bearer admin-test")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	_, _ = io.ReadAll(resp.Body)
	return resp.StatusCode
}

// Auto-apply turns "collect and show" into "collect and act" for the sites that
// opted in: no apply call, and the member is parked as soon as the second low
// round lands.
func TestSiteProbeAutoApplyActsOnItsOwn(t *testing.T) {
	dataDir := t.TempDir()
	db, _ := store.Open(dataDir)
	defer db.Close()
	enc, _ := crypto.New("site-probe-test-master-key-32-ch!")
	cfg := &config.Config{AdminToken: "admin-test", MetricsToken: "metrics-test", BackupDir: filepath.Join(dataDir, "backups"), MaxAdminBodyBytes: 1 << 20, AuditRetentionDays: 90, AuditRetentionRows: 100000, Cooldown: time.Second}
	server := httptest.NewServer(httpapi.NewTestRouter(t, cfg, db, enc))
	defer server.Close()

	var up, down atomic.Int64
	up.Store(1)
	down.Store(9)
	kuma := kumaStub(t, &up, &down, "z-ai/glm-5.2")

	var site struct{ ID int64 }
	json.Unmarshal(post(t, server.URL+"/admin/sites", map[string]any{"name": "自动站", "base_url": "https://auto.example", "platform": "new-api", "status": "enabled"}), &site)
	var cred struct{ ID int64 }
	json.Unmarshal(post(t, server.URL+"/admin/sites/"+itoa(site.ID)+"/credentials", map[string]any{"kind": "api_key", "secret": "sk-test", "status": "enabled"}), &cred)
	var channel struct{ ID int64 }
	json.Unmarshal(post(t, server.URL+"/admin/channels", map[string]any{"site_id": site.ID, "credential_id": cred.ID, "name": "auto-key", "base_url": "https://auto.example", "type_hint": "new-api", "status": "enabled"}), &channel)
	var route struct{ ID int64 }
	json.Unmarshal(post(t, server.URL+"/admin/routes", map[string]any{"model_pattern": "glm-5.2", "enabled": true}), &route)
	post(t, server.URL+"/admin/routes/"+itoa(route.ID)+"/members", map[string]any{"channel_id": channel.ID, "enabled": true, "auto": true, "weight": 100})

	// Both streaks are lowered to one round: the override has to be honored in
	// both directions, and the test should not depend on the default of two.
	json.Unmarshal(putJSON(t, server.URL+"/admin/site-probe/source", map[string]any{
		"site_id": site.ID, "kind": "uptime_kuma", "url": kuma.URL + "/status/ai", "enabled": true,
		"config": `{"auto_apply":true,"policy":{"low_rounds":1,"high_rounds":1}}`,
	}), &struct{}{})

	var collect struct {
		Collected int `json:"collected"`
		Failed    int `json:"failed"`
		Actions   []struct {
			Kind         string `json:"kind"`
			ChannelName  string `json:"channel_name"`
			MembersMoved int    `json:"members_moved"`
		} `json:"actions"`
	}
	json.Unmarshal(post(t, server.URL+"/admin/site-probe/collect", map[string]any{"site_ids": []int64{site.ID}}), &collect)
	if len(collect.Actions) != 1 || collect.Actions[0].Kind != "disable" || collect.Actions[0].MembersMoved != 1 {
		t.Fatalf("collect actions = %+v, want the opted-in site to park its member", collect.Actions)
	}
	report := readReport(t, server.URL)
	if member := report.Rows[0].Members[0]; member.Enabled || !member.AutoDisabled {
		t.Fatalf("member after an automatic round = %+v, want parked", member)
	}

	// The same site reporting healthy again recovers the member automatically.
	up.Store(10)
	down.Store(0)
	json.Unmarshal(post(t, server.URL+"/admin/site-probe/collect", map[string]any{"site_ids": []int64{site.ID}}), &collect)
	if len(collect.Actions) != 1 || collect.Actions[0].Kind != "recover" || collect.Actions[0].MembersMoved != 1 {
		t.Fatalf("recovery actions = %+v, want the member back", collect.Actions)
	}
	report = readReport(t, server.URL)
	if member := report.Rows[0].Members[0]; !member.Enabled || member.AutoDisabled {
		t.Fatalf("member after recovery = %+v, want it in rotation", member)
	}
}

// adoptStatus is post() with the status code kept: the endpoint's rejection of
// an empty member list is part of the contract.
func adoptStatus(t *testing.T, url string, payload any) int {
	t.Helper()
	encoded, _ := json.Marshal(payload)
	req, _ := http.NewRequest(http.MethodPost, url, bytes.NewReader(encoded))
	req.Header.Set("Authorization", "Bearer admin-test")
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	_, _ = io.ReadAll(resp.Body)
	return resp.StatusCode
}

// Adopting an observed price goes through the same public price table the
// collector reads, so the billing layer and the tool can never disagree about
// what a site costs. The quote itself is never taken from the request: it comes
// from the collected sample of the row the member belongs to.
func TestSiteProbeAdoptPriceWritesTheBillingColumns(t *testing.T) {
	dataDir := t.TempDir()
	db, _ := store.Open(dataDir)
	defer db.Close()
	enc, _ := crypto.New("site-probe-test-master-key-32-ch!")
	cfg := &config.Config{AdminToken: "admin-test", MetricsToken: "metrics-test", BackupDir: filepath.Join(dataDir, "backups"), MaxAdminBodyBytes: 1 << 20, AuditRetentionDays: 90, AuditRetentionRows: 100000, Cooldown: time.Second}
	server := httptest.NewServer(httpapi.NewTestRouter(t, cfg, db, enc))
	defer server.Close()

	// A New-API price table in the shape a real site returns: the real prices
	// live in the billing expression; model_ratio is a placeholder.
	priceServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/pricing":
			_, _ = w.Write([]byte(`{"data":[{"model_name":"glm-5.3-flash","quota_type":0,"model_ratio":37.5,
				"billing_mode":"tiered_expr","billing_expr":"tier(\"base\", p * 0.15 + c * 0.5 + cr * 0.03)"}],
				"group_ratio":{"default":1},"success":true}`))
		case "/api/status":
			_, _ = w.Write([]byte(`{"success":true,"data":{"quota_per_unit":500000}}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer priceServer.Close()

	var site struct{ ID int64 }
	json.Unmarshal(post(t, server.URL+"/admin/sites", map[string]any{"name": "价格站", "base_url": "https://p.example", "platform": "new-api", "status": "enabled"}), &site)
	var cred struct{ ID int64 }
	json.Unmarshal(post(t, server.URL+"/admin/sites/"+itoa(site.ID)+"/credentials", map[string]any{"kind": "api_key", "secret": "sk-test", "status": "enabled"}), &cred)
	var channel struct{ ID int64 }
	json.Unmarshal(post(t, server.URL+"/admin/channels", map[string]any{"site_id": site.ID, "credential_id": cred.ID, "name": "P-key", "base_url": "https://p.example", "type_hint": "new-api", "status": "enabled"}), &channel)
	json.Unmarshal(putJSON(t, server.URL+"/admin/site-probe/source", map[string]any{"site_id": site.ID, "kind": "newapi", "url": priceServer.URL, "enabled": true}), &struct{}{})
	var route struct{ ID int64 }
	json.Unmarshal(post(t, server.URL+"/admin/routes", map[string]any{"model_pattern": "glm-5.3-flash", "enabled": true}), &route)
	var member struct{ ID int64 }
	json.Unmarshal(post(t, server.URL+"/admin/routes/"+itoa(route.ID)+"/members", map[string]any{"channel_id": channel.ID, "enabled": true, "auto": true, "weight": 100}), &member)

	json.Unmarshal(post(t, server.URL+"/admin/site-probe/collect", map[string]any{"site_ids": []int64{site.ID}}), &struct{}{})

	// Adopting without naming a member is a 400: the endpoint writes into the
	// billing layer, so it must be told exactly which members.
	if status := adoptStatus(t, server.URL+"/admin/site-probe/adopt-price", map[string]any{}); status != http.StatusBadRequest {
		t.Fatalf("adopt without members = %d, want 400", status)
	}

	var adopt struct {
		Results []struct {
			MemberID int64    `json:"member_id"`
			Adopted  []string `json:"adopted"`
			Skipped  string   `json:"skipped"`
		} `json:"results"`
	}
	json.Unmarshal(post(t, server.URL+"/admin/site-probe/adopt-price", map[string]any{"member_ids": []int64{member.ID}}), &adopt)
	if len(adopt.Results) != 1 || adopt.Results[0].MemberID != member.ID || adopt.Results[0].Skipped != "" {
		t.Fatalf("adopt = %+v", adopt.Results)
	}
	if len(adopt.Results[0].Adopted) != 3 {
		t.Fatalf("adopted = %v, want prompt+completion+cache", adopt.Results[0].Adopted)
	}
	// USD/1M published → USD/1k stored: 0.15 → 0.00015.
	prompt, completion, cache, perRequest, found, err := db.RouteMember.MemberPrices(member.ID)
	if err != nil || !found {
		t.Fatalf("member prices: found=%v err=%v", found, err)
	}
	if !closeTo(prompt, 0.00015) || !closeTo(completion, 0.0005) || !closeTo(cache, 0.00003) || perRequest != 0 {
		t.Fatalf("billing columns = %v/%v/%v/%v", prompt, completion, cache, perRequest)
	}

	// A second adopt is a no-op: the operator's billing layer is not
	// overwritten by whatever the status page published later.
	var again struct {
		Results []struct {
			MemberID int64    `json:"member_id"`
			Adopted  []string `json:"adopted"`
			Skipped  string   `json:"skipped"`
		} `json:"results"`
	}
	json.Unmarshal(post(t, server.URL+"/admin/site-probe/adopt-price", map[string]any{"member_ids": []int64{member.ID}}), &again)
	if len(again.Results) != 1 || again.Results[0].Skipped != "already_priced" || len(again.Results[0].Adopted) != 0 {
		t.Fatalf("second adopt = %+v, want already_priced", again.Results)
	}
}

// The directory is a source of addresses, not of site records: an import
// attaches probe sources to the sites this gateway already routes through, and
// leaves every entry we have no channel for alone (there is no site management
// screen, so creating them would leave debris nobody could remove).
func TestSiteProbeCatalogImportMatchesExistingSitesOnly(t *testing.T) {
	dataDir := t.TempDir()
	db, _ := store.Open(dataDir)
	defer db.Close()
	enc, _ := crypto.New("site-probe-test-master-key-32-ch!")
	cfg := &config.Config{AdminToken: "admin-test", MetricsToken: "metrics-test", BackupDir: filepath.Join(dataDir, "backups"), MaxAdminBodyBytes: 1 << 20, AuditRetentionDays: 90, AuditRetentionRows: 100000, Cooldown: time.Second}
	server := httptest.NewServer(httpapi.NewTestRouter(t, cfg, db, enc))
	defer server.Close()

	// A directory in the watchbot shape: one row per (site × model × group).
	// Four entries: one on a site with a hand-typed source, one status page on a
	// subdomain of an existing site, one pricing page on a site whose platform we
	// never detected, and one for a site we do not have at all.
	catalog := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"rows":[
			{"siteId":52,"siteName":"手工站","siteUrl":"https://p.example/pricing","provider":"openai","ruleName":"gpt-5.5","totalCount":10},
			{"siteId":20,"siteName":"探针站","siteUrl":"https://stat.p2.example/status/ai","provider":"anthropic","ruleName":"claude-sonnet-4-6","totalCount":16},
			{"siteId":30,"siteName":"未知平台站","siteUrl":"https://p3.example/pricing","provider":"openai","ruleName":"gpt-5.5","totalCount":4},
			{"siteId":99,"siteName":"没见过的站","siteUrl":"https://nobody.example/pricing","provider":"openai","ruleName":"gpt-5.5","totalCount":1}
		]}`))
	}))
	defer catalog.Close()

	// Site A: a source the operator typed.
	var manual struct{ ID int64 }
	json.Unmarshal(post(t, server.URL+"/admin/sites", map[string]any{"name": "手工站", "base_url": "https://p.example", "platform": "new-api", "status": "enabled"}), &manual)
	json.Unmarshal(putJSON(t, server.URL+"/admin/site-probe/source", map[string]any{"site_id": manual.ID, "kind": "uptime_kuma", "url": "https://stat.example.com/status/ai", "enabled": true}), &struct{}{})
	// Site B: the bare domain, no source yet — the status page lives on a
	// subdomain, which the matcher has to see through.
	json.Unmarshal(post(t, server.URL+"/admin/sites", map[string]any{"name": "探针站", "base_url": "https://p2.example", "platform": "", "status": "enabled"}), &struct{}{})
	// Site C: an unknown platform, so the pricing entry needs an explicit source
	// instead of relying on the platform derivation.
	json.Unmarshal(post(t, server.URL+"/admin/sites", map[string]any{"name": "未知平台站", "base_url": "https://p3.example", "platform": "", "status": "enabled"}), &struct{}{})

	var imported struct {
		Matched   int `json:"matched"`
		Skipped   int `json:"skipped"`
		Unmatched int `json:"unmatched"`
		Created   int `json:"created"`
	}
	json.Unmarshal(post(t, server.URL+"/admin/site-probe/catalog/import", map[string]any{"url": catalog.URL}), &imported)
	if imported.Matched != 2 || imported.Skipped != 1 || imported.Unmatched != 1 || imported.Created != 0 {
		t.Fatalf("import = %+v, want matched=2 skipped=1 unmatched=1 created=0", imported)
	}

	var sites []struct {
		Name               string `json:"name"`
		ProbeSourceKind    string `json:"probe_source_kind"`
		ProbeSourceURL     string `json:"probe_source_url"`
		ProbeAuto          bool   `json:"probe_auto"`
		ProbeSourceEnabled bool   `json:"probe_source_enabled"`
	}
	json.Unmarshal(get(t, server.URL+"/admin/sites"), &sites)
	// The unmatched directory entry must not have become a site.
	if len(sites) != 3 {
		t.Fatalf("sites = %d (%+v), want the 3 we created", len(sites), sites)
	}
	bySite := map[string]struct {
		kind    string
		url     string
		auto    bool
		enabled bool
	}{}
	for _, site := range sites {
		bySite[site.Name] = struct {
			kind    string
			url     string
			auto    bool
			enabled bool
		}{site.ProbeSourceKind, site.ProbeSourceURL, site.ProbeAuto, site.ProbeSourceEnabled}
	}
	if _, exists := bySite["没见过的站"]; exists {
		t.Fatal("the import created a site for a directory entry we route nothing through")
	}
	// The status entry matched through the subdomain, and its own URL is what we
	// read (the status page is not on the site's host).
	probeSite := bySite["探针站"]
	if probeSite.kind != "uptime_kuma" || probeSite.url != "https://stat.p2.example/status/ai" || probeSite.auto || !probeSite.enabled {
		t.Fatalf("matched status site = %+v", probeSite)
	}
	// The pricing entry fell back to an explicit new-api source because the
	// platform column is empty (auto derivation would have found nothing).
	unknown := bySite["未知平台站"]
	if unknown.kind != "newapi" || unknown.url != "https://p3.example" || unknown.auto || !unknown.enabled {
		t.Fatalf("matched unknown-platform site = %+v", unknown)
	}
	// A hand-typed source is never overwritten by a directory.
	if manualSite := bySite["手工站"]; manualSite.url != "https://stat.example.com/status/ai" {
		t.Fatalf("the import overwrote a hand-typed source: %+v", manualSite)
	}
}

// Cleanup for the debris an import that created missing sites leaves behind:
// only sites with neither a channel nor a credential are removed.
func TestSiteProbeCatalogPruneKeepsSitesInUse(t *testing.T) {
	dataDir := t.TempDir()
	db, _ := store.Open(dataDir)
	defer db.Close()
	enc, _ := crypto.New("site-probe-test-master-key-32-ch!")
	cfg := &config.Config{AdminToken: "admin-test", MetricsToken: "metrics-test", BackupDir: filepath.Join(dataDir, "backups"), MaxAdminBodyBytes: 1 << 20, AuditRetentionDays: 90, AuditRetentionRows: 100000, Cooldown: time.Second}
	server := httptest.NewServer(httpapi.NewTestRouter(t, cfg, db, enc))
	defer server.Close()

	// An unused shell, exactly the kind an over-eager import leaves.
	var unused struct{ ID int64 }
	json.Unmarshal(post(t, server.URL+"/admin/sites", map[string]any{"name": "空壳站", "base_url": "https://shell.example", "platform": "new-api", "status": "enabled"}), &unused)
	json.Unmarshal(putJSON(t, server.URL+"/admin/site-probe/source", map[string]any{"site_id": unused.ID, "kind": "newapi", "url": "https://shell.example", "enabled": true}), &struct{}{})

	// A site that carries a channel, plus a credential-only site: both hold
	// operator data and must survive the prune.
	var inUse struct{ ID int64 }
	json.Unmarshal(post(t, server.URL+"/admin/sites", map[string]any{"name": "在用站", "base_url": "https://used.example", "platform": "new-api", "status": "enabled"}), &inUse)
	var credential struct{ ID int64 }
	json.Unmarshal(post(t, server.URL+"/admin/sites/"+itoa(inUse.ID)+"/credentials", map[string]any{"kind": "api_key", "secret": "sk-prune", "status": "enabled"}), &credential)
	post(t, server.URL+"/admin/channels", map[string]any{"site_id": inUse.ID, "credential_id": credential.ID, "name": "used-key", "base_url": "https://used.example", "type_hint": "new-api", "status": "enabled"})
	var credOnly struct{ ID int64 }
	json.Unmarshal(post(t, server.URL+"/admin/sites", map[string]any{"name": "仅凭据站", "base_url": "https://cred.example", "platform": "new-api", "status": "enabled"}), &credOnly)
	json.Unmarshal(post(t, server.URL+"/admin/sites/"+itoa(credOnly.ID)+"/credentials", map[string]any{"kind": "api_key", "secret": "sk-cred", "status": "enabled"}), &struct{}{})

	var pruned struct {
		Removed int `json:"removed"`
	}
	json.Unmarshal(post(t, server.URL+"/admin/site-probe/catalog/prune", map[string]any{}), &pruned)
	if pruned.Removed != 1 {
		t.Fatalf("pruned = %+v, want only the unused shell removed", pruned)
	}
	var sites []struct {
		Name string `json:"name"`
	}
	json.Unmarshal(get(t, server.URL+"/admin/sites"), &sites)
	names := make([]string, 0, len(sites))
	for _, site := range sites {
		names = append(names, site.Name)
	}
	if len(sites) != 2 {
		t.Fatalf("sites after prune = %v, want the two that hold data", names)
	}
}

// An auto site needs no URL: the platform derives the source, and the collector
// reads it. A site with an unknown platform is skipped silently instead of
// erroring every round.
func TestSiteProbeAutoSourceCollects(t *testing.T) {
	dataDir := t.TempDir()
	db, _ := store.Open(dataDir)
	defer db.Close()
	enc, _ := crypto.New("site-probe-test-master-key-32-ch!")
	cfg := &config.Config{AdminToken: "admin-test", MetricsToken: "metrics-test", BackupDir: filepath.Join(dataDir, "backups"), MaxAdminBodyBytes: 1 << 20, AuditRetentionDays: 90, AuditRetentionRows: 100000, Cooldown: time.Second}
	server := httptest.NewServer(httpapi.NewTestRouter(t, cfg, db, enc))
	defer server.Close()

	pricing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/pricing" {
			_, _ = w.Write([]byte(`{"data":[{"model_name":"glm-5.2","quota_type":0,"model_ratio":0.25,"completion_ratio":4}],
				"group_ratio":{"default":1},"success":true}`))
			return
		}
		if r.URL.Path == "/api/status" {
			_, _ = w.Write([]byte(`{"success":true,"data":{"quota_per_unit":500000}}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer pricing.Close()

	var site struct{ ID int64 }
	json.Unmarshal(post(t, server.URL+"/admin/sites", map[string]any{"name": "自动站", "base_url": pricing.URL, "platform": "new-api", "status": "enabled"}), &site)
	// Auto mode: no kind, no URL — just the flag.
	json.Unmarshal(putJSON(t, server.URL+"/admin/site-probe/source", map[string]any{"site_id": site.ID, "kind": "", "url": "", "auto": true, "enabled": true}), &struct{}{})

	var collect struct {
		Collected int `json:"collected"`
		Failed    int `json:"failed"`
	}
	json.Unmarshal(post(t, server.URL+"/admin/site-probe/collect", map[string]any{}), &collect)
	if collect.Collected != 1 || collect.Failed != 0 {
		t.Fatalf("collect = %+v, want the auto site collected", collect)
	}
	var report struct {
		Unmatched []struct {
			RawModel string              `json:"raw_model"`
			Price    *siteProbePriceView `json:"price"`
		} `json:"unmatched"`
	}
	json.Unmarshal(get(t, server.URL+"/admin/site-probe/report"), &report)
	// No route was created in this test, so the model shows up as unmatched —
	// which is exactly where a price-only reading belongs.
	if len(report.Unmatched) != 1 || report.Unmatched[0].RawModel != "glm-5.2" {
		t.Fatalf("unmatched = %+v, want the priced model", report.Unmatched)
	}
	if report.Unmatched[0].Price == nil || !closeTo(report.Unmatched[0].Price.InputPerMillion, 0.5) {
		t.Fatalf("unmatched price = %+v, want 0.5 USD/1M from ratio 0.25", report.Unmatched[0].Price)
	}
}

// siteProbePriceView mirrors the JSON shape of the stored price on a reading.
type siteProbePriceView struct {
	InputPerMillion float64 `json:"input_per_million"`
}
