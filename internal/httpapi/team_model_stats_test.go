package httpapi

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/lan/meta-gateway/internal/domain"
	"github.com/lan/meta-gateway/internal/store"
)

// seedMemberRequest writes the three rows the relay leaves behind for one
// completed request: the client request the log page lists, the billed usage
// record, and the upstream log row. Each caller passes only the rows that
// request actually produced, which is how the model-resolution fallback and the
// unassigned count get exercised instead of assumed.
func seedMemberRequest(t *testing.T, e *teamTestEnv, requestID string, userID, keyID int64, model string, status, latency int) {
	t.Helper()
	if _, err := e.db.Exec(`INSERT INTO team_requests(request_id,user_id,key_id,path,status,latency_ms) VALUES(?,?,?,'chat/completions',?,?)`,
		requestID, userID, keyID, status, latency); err != nil {
		t.Fatal(err)
	}
	if model == "" {
		return
	}
	if _, err := e.db.Exec(`INSERT INTO proxy_logs(request_id,channel_id,model,status,latency_ms,downstream_key_id,path) VALUES(?,1,?,?,?,?,'chat/completions')`,
		requestID, model, status, latency, keyID); err != nil {
		t.Fatal(err)
	}
}

func TestMemberModelStatsReadsOnlyTheAccountsOwnTraffic(t *testing.T) {
	e := newTeamTestEnv(t)
	e.enable()
	alice := e.member("stats-alice", "member")
	bob := e.member("stats-bob", "member")
	aliceKey, _ := alice.key("alice-key")
	bobKey, _ := bob.key("bob-key")

	// alice-1 carries only a billing record: the model has to come from there.
	seedMemberRequest(t, e, "alice-1", alice.userID, aliceKey, "", 200, 1000)
	if _, err := e.db.Exec(`INSERT INTO usage_records(request_id,downstream_key_id,channel_id,model,path,status,prompt_tokens,completion_tokens,total_tokens,cost,user_id) VALUES('alice-1',?,1,'model-a','chat/completions',200,80,20,100,0.5,?)`, aliceKey, alice.userID); err != nil {
		t.Fatal(err)
	}
	// alice-2 failed hard: no usage row exists, so only the upstream log can
	// name the model. A stats page that skipped it would report a 100% success
	// rate for the model that just failed.
	seedMemberRequest(t, e, "alice-2", alice.userID, aliceKey, "model-a", 500, 2000)
	// alice-3 has both sources agreeing.
	seedMemberRequest(t, e, "alice-3", alice.userID, aliceKey, "model-b", 200, 3000)
	if _, err := e.db.Exec(`INSERT INTO usage_records(request_id,downstream_key_id,channel_id,model,path,status,prompt_tokens,completion_tokens,total_tokens,cost,user_id) VALUES('alice-3',?,1,'model-b','chat/completions',200,40,10,50,0.2,?)`, aliceKey, alice.userID); err != nil {
		t.Fatal(err)
	}
	// alice-4 never reached an upstream: neither source knows a model.
	seedMemberRequest(t, e, "alice-4", alice.userID, aliceKey, "", 429, 5)
	// bob's traffic must not appear in alice's figures.
	seedMemberRequest(t, e, "bob-1", bob.userID, bobKey, "model-a", 200, 9000)
	if _, err := e.db.Exec(`INSERT INTO usage_records(request_id,downstream_key_id,channel_id,model,path,status,total_tokens,cost,user_id) VALUES('bob-1',?,1,'model-a','chat/completions',200,7777,7.7,?)`, bobKey, bob.userID); err != nil {
		t.Fatal(err)
	}

	var view memberModelStatsView
	raw := alice.request("GET", "/me/model-stats", nil, 200)
	if err := json.Unmarshal(raw, &view); err != nil {
		t.Fatal(err)
	}
	if view.Since == "" || view.Until == "" {
		t.Fatalf("window missing from the answer: %s", raw)
	}
	if view.Unassigned != 1 {
		t.Fatalf("unassigned=%d, want 1 (the request that reached no upstream)", view.Unassigned)
	}
	if len(view.Models) != 2 {
		t.Fatalf("models=%+v, want model-a and model-b", view.Models)
	}
	// Ordered by request count: model-a (2) first, then model-b (1).
	a, b := view.Models[0], view.Models[1]
	if a.Model != "model-a" || a.Requests != 2 || a.OK != 1 || a.Failed != 1 {
		t.Fatalf("model-a: %+v", a)
	}
	if a.TotalTokens != 100 || a.PromptTokens != 80 || a.CompletionTokens != 20 || a.Cost != 0.5 {
		t.Fatalf("model-a billing: %+v", a)
	}
	if a.AvgLatencyMS != 1500 || a.P50MS != 2000 || a.P95MS != 2000 {
		t.Fatalf("model-a latency: %+v", a)
	}
	if a.LastAt == "" {
		t.Fatal("model-a has no last-request time")
	}
	if b.Model != "model-b" || b.Requests != 1 || b.Failed != 0 || b.TotalTokens != 50 || b.Cost != 0.2 {
		t.Fatalf("model-b: %+v", b)
	}
	if a.TotalTokens == 7777 || b.TotalTokens == 7777 {
		t.Fatalf("another account's tokens leaked into the figures: %+v", view.Models)
	}

	// An explicit window excludes the rows instead of returning them anyway.
	raw = alice.request("GET", "/me/model-stats?since=2099-01-01T00:00:00Z", nil, 200)
	if err := json.Unmarshal(raw, &view); err != nil {
		t.Fatal(err)
	}
	if len(view.Models) != 0 || view.Unassigned != 0 {
		t.Fatalf("window ignored: %+v", view)
	}
	alice.request("GET", "/me/model-stats?since=nonsense", nil, 400)

	// Usage visibility governs the figures, exactly as it governs the log page.
	if _, err := e.db.Exec(`UPDATE team_settings SET branding_json=json_set(branding_json,'$.show_usage',json('false')) WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	alice.request("GET", "/me/model-stats", nil, 403)
}

func TestMemberModelAvailabilityFollowsThePolicyAndTheProbes(t *testing.T) {
	e := newTeamTestEnv(t)
	e.enable()
	alice := e.member("health-alice", "member")

	up, err := e.db.Channel.Create(&domain.Channel{Name: "up-a", Status: domain.StatusEnabled})
	if err != nil {
		t.Fatal(err)
	}
	down, err := e.db.Channel.Create(&domain.Channel{Name: "down-b", Status: domain.StatusEnabled})
	if err != nil {
		t.Fatal(err)
	}
	retired, err := e.db.Channel.Create(&domain.Channel{Name: "retired-c", Status: domain.StatusDisabled})
	if err != nil {
		t.Fatal(err)
	}
	granted, err := e.db.Route.Create(&domain.Route{ModelPattern: "model-on", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	first, err := e.db.RouteMember.Create(&domain.RouteMember{RouteID: granted, ChannelID: up, Enabled: true, Weight: 100})
	if err != nil {
		t.Fatal(err)
	}
	second, err := e.db.RouteMember.Create(&domain.RouteMember{RouteID: granted, ChannelID: down, Enabled: true, Weight: 100})
	if err != nil {
		t.Fatal(err)
	}
	// A pair on a disabled channel and a whole model the policy does not grant:
	// neither may show up in a member's answer.
	if _, err := e.db.RouteMember.Create(&domain.RouteMember{RouteID: granted, ChannelID: retired, Enabled: true, Weight: 100}); err != nil {
		t.Fatal(err)
	}
	other, err := e.db.Route.Create(&domain.Route{ModelPattern: "model-off", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.db.RouteMember.Create(&domain.RouteMember{RouteID: other, ChannelID: up, Enabled: true, Weight: 100}); err != nil {
		t.Fatal(err)
	}
	e.admin("PUT", "/admin/team/policies/1", TeamPolicy{ID: 1, Name: "Limited", AllModels: true,
		MemberIDs: []int64{first, second}, MaxKeys: 5, RPM: 60}, 200)

	if _, err := e.db.Exec(`INSERT INTO model_health(channel_id,model,ok,latency_ms,probed_at,source) VALUES
		(?, 'model-on', 1, 400, datetime('now'), 'probe'),
		(?, 'model-on', 0, 0, datetime('now'), 'probe')`, up, down); err != nil {
		t.Fatal(err)
	}
	if _, err := e.db.Exec(`INSERT INTO probe_results(task_id,channel_id,model,ok,latency_ms,probed_at) VALUES
		(1, ?, 'model-on', 1, 400, datetime('now','-2 hours')),
		(1, ?, 'model-on', 1, 400, datetime('now','-3 hours')),
		(1, ?, 'model-on', 1, 400, datetime('now','-4 hours')),
		(1, ?, 'model-on', 1, 300, datetime('now','-5 hours')),
		(1, ?, 'model-on', 0, 0, datetime('now','-6 hours')),
		(2, ?, 'model-off', 1, 100, datetime('now','-2 hours'))`, up, up, up, up, down, up); err != nil {
		t.Fatal(err)
	}

	var items []memberModelHealthView
	raw := alice.request("GET", "/me/model-availability", nil, 200)
	if err := json.Unmarshal(raw, &items); err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("items=%+v, want only the granted model", items)
	}
	got := items[0]
	if got.Model != "model-on" || got.Upstreams != 2 || got.Probed != 2 || got.Healthy != 1 {
		t.Fatalf("upstream state: %+v", got)
	}
	if got.Samples != 5 || got.OKSamples != 4 {
		t.Fatalf("probe history: %+v", got)
	}
	if got.Availability < 0.799 || got.Availability > 0.801 {
		t.Fatalf("availability=%v, want 0.8", got.Availability)
	}
	// Weighted by samples: upstream A averages (400+400+400+300)/4 = 375 over
	// four samples, upstream B averages 0 over one; (375*4 + 0*1)/5 = 300.
	if got.AvgLatencyMS != 300 {
		t.Fatalf("avg latency=%d, want 300", got.AvgLatencyMS)
	}
	if got.LastProbedAt == "" {
		t.Fatal("last probe time missing")
	}
	for _, secret := range []string{"up-a", "down-b", "retired-c", "\"channel", "base_url"} {
		if strings.Contains(string(raw), secret) {
			t.Fatalf("an upstream detail leaked: %s", secret)
		}
	}

	// A model nobody has probed is reported as unmeasured, not as healthy.
	if _, err := e.db.Exec(`DELETE FROM probe_results; DELETE FROM model_health;`); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(alice.request("GET", "/me/model-availability", nil, 200), &items); err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Upstreams != 2 || items[0].Probed != 0 || items[0].Samples != 0 || items[0].Availability != 0 {
		t.Fatalf("unprobed model: %+v", items)
	}

	// A member on a policy that grants nothing sees nothing at all — being a
	// member is not itself an authorization.
	bob := e.member("health-bob", "member")
	e.admin("POST", "/admin/team/policies", TeamPolicy{Name: "Nothing", MaxKeys: 1, RPM: 60}, 200)
	var policies []TeamPolicy
	if err := json.Unmarshal(e.admin("GET", "/admin/team/policies", nil, 200), &policies); err != nil {
		t.Fatal(err)
	}
	emptyPolicy := int64(0)
	for _, policy := range policies {
		if policy.Name == "Nothing" {
			emptyPolicy = policy.ID
		}
	}
	if emptyPolicy == 0 {
		t.Fatal("the empty policy was not created")
	}
	e.admin("PATCH", "/admin/team/users/"+strconv.FormatInt(bob.userID, 10), map[string]any{"policy_id": emptyPolicy}, 200)
	// An admin write drops the member's sessions on purpose, so the new policy is
	// only observable after signing in again.
	bob.login("health-bob", "member-password-123")
	if err := json.Unmarshal(bob.request("GET", "/me/model-availability", nil, 200), &items); err != nil {
		t.Fatal(err)
	}
	if len(items) != 0 {
		t.Fatalf("ungranted member saw %+v", items)
	}

	// The store refuses to answer without an account, like every other
	// member-scoped read.
	if _, _, err := e.db.MemberModelStats(0, nil, nil); err == nil {
		t.Fatal("stats answered without a user")
	}
}

var _ = store.MemberModelStat{}
