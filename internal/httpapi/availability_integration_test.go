package httpapi_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lan/meta-gateway/internal/config"
	"github.com/lan/meta-gateway/internal/crypto"
	"github.com/lan/meta-gateway/internal/domain"
	"github.com/lan/meta-gateway/internal/httpapi"
	"github.com/lan/meta-gateway/internal/siteprobe"
	"github.com/lan/meta-gateway/internal/store"
)

func TestAvailabilityHTTPSeparatesAttemptsAndFinalRequest(t *testing.T) {
	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(503)
		_, _ = w.Write([]byte(`{"error":{"message":"busy"}}`))
	}))
	defer first.Close()
	second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"ok"}}],"usage":{"prompt_tokens":2,"completion_tokens":1,"total_tokens":3}}`))
	}))
	defer second.Close()
	db, err := store.OpenTest(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	enc, err := crypto.New("availability-http-test-master-key")
	if err != nil {
		t.Fatal(err)
	}
	secret, err := enc.Encrypt([]byte("synthetic-test-key"))
	if err != nil {
		t.Fatal(err)
	}
	siteID, err := db.Site.Create(&domain.Site{Name: "fixture", Status: domain.StatusEnabled})
	if err != nil {
		t.Fatal(err)
	}
	credID, err := db.Credential.Create(&domain.Credential{SiteID: siteID, Kind: "api_key", SecretEnc: []byte(secret), Status: domain.StatusEnabled})
	if err != nil {
		t.Fatal(err)
	}
	routeID, err := db.Route.Create(&domain.Route{ModelPattern: "test-alias", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	memberIDs := []int64{}
	for index, base := range []string{first.URL, second.URL} {
		channelID, err := db.Channel.Create(&domain.Channel{Name: fmt.Sprintf("up-%d", index), SiteID: &siteID, CredentialID: &credID, BaseURL: base, TypeHint: "openai-compatible", Status: domain.StatusEnabled, Weight: 100})
		if err != nil {
			t.Fatal(err)
		}
		id, err := db.RouteMember.Create(&domain.RouteMember{RouteID: routeID, ChannelID: channelID, MappingJSON: fmt.Sprintf(`{"real":"original-%d"}`, index), GroupName: "default", Priority: 2 - index, Weight: 100, Enabled: true})
		if err != nil {
			t.Fatal(err)
		}
		memberIDs = append(memberIDs, id)
	}
	cfg := &config.Config{AdminToken: "admin-test", AdminTokens: []string{"admin-test"}, RetryTimes: 2, ChannelRetryTimes: 0, CrossChannelFailoverEnabled: true, OutboundAllowCIDRs: []string{"127.0.0.0/8"}, ExchangeAllowSecretExport: true}
	gw := httptest.NewServer(httpapi.NewTestRouter(t, cfg, db, enc))
	t.Cleanup(gw.Close)
	var key struct{ Token string }
	if err := json.Unmarshal(post(t, gw.URL+"/admin/downstream-keys", map[string]any{"name": "fixture-key"}), &key); err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest("POST", gw.URL+"/v1/chat/completions", bytes.NewBufferString(`{"model":"test-alias","messages":[{"role":"user","content":"hi"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+key.Token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("response %d %s %v", resp.StatusCode, body, err)
	}
	req, err = http.NewRequest("GET", fmt.Sprintf("%s/admin/availability?route_ids=%d&group=default", gw.URL, routeID), nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer admin-test")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var report siteprobe.AvailabilityReport
	if err := json.NewDecoder(resp.Body).Decode(&report); err != nil {
		t.Fatal(err)
	}
	if len(report.Routes) != 1 || report.Routes[0].Primary == nil || report.Routes[0].Primary.Samples != 1 || report.Routes[0].Primary.OK != 1 {
		t.Fatalf("final route result=%+v", report.Routes)
	}
	byID := map[int64]*store.AvailabilitySeries{}
	for _, m := range report.Members {
		byID[m.MemberID] = m.Primary
	}
	if byID[memberIDs[0]] == nil || byID[memberIDs[0]].Failed != 1 || byID[memberIDs[1]] == nil || byID[memberIDs[1]].OK != 1 {
		t.Fatalf("attempt results=%+v", byID)
	}
	// A request that fails before a member is chosen (here: no eligible channel,
	// because the only member's channel is disabled) must still be attributed to
	// the group the client asked for. Recording it with an empty group would make
	// the console's group-scoped number silently drop those failures.
	if _, err := db.Exec(`UPDATE channels SET status='disabled' WHERE site_id=?`, siteID); err != nil {
		t.Fatal(err)
	}
	req, err = http.NewRequest("POST", gw.URL+"/v1/chat/completions", bytes.NewBufferString(`{"model":"test-alias","messages":[{"role":"user","content":"hi"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+key.Token)
	req.Header.Set("Content-Type", "application/json")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode == 200 {
		t.Fatalf("disabled channel still served the request")
	}
	var group string
	if err := db.QueryRow(`SELECT group_name FROM route_request_outcomes WHERE outcome='failed' ORDER BY id DESC LIMIT 1`).Scan(&group); err != nil {
		t.Fatal(err)
	}
	if group != "default" {
		t.Fatalf("pre-selection failure group=%q, want default", group)
	}
	req, err = http.NewRequest("GET", fmt.Sprintf("%s/admin/availability?route_ids=%d&group=default", gw.URL, routeID), nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer admin-test")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	report = siteprobe.AvailabilityReport{}
	if err := json.NewDecoder(resp.Body).Decode(&report); err != nil {
		t.Fatal(err)
	}
	if report.Routes[0].Primary.Samples != 2 || report.Routes[0].Primary.Failed != 1 {
		t.Fatalf("group-scoped route result dropped the pre-selection failure: %+v", report.Routes[0].Primary)
	}

	// Admin-only measurements never leak through an unauthenticated read.
	unauthorized, err := http.Get(gw.URL + "/admin/availability")
	if err != nil {
		t.Fatal(err)
	}
	unauthorized.Body.Close()
	if unauthorized.StatusCode != 401 {
		t.Fatalf("unauthenticated=%d", unauthorized.StatusCode)
	}
}
