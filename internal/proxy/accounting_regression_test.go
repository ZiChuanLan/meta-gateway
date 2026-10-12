package proxy

import (
	"math"
	"testing"

	"github.com/lan/meta-gateway/internal/crypto"
	"github.com/lan/meta-gateway/internal/domain"
	"github.com/lan/meta-gateway/internal/store"
	"github.com/lan/meta-gateway/internal/usage"
)

func accountingFixture(t *testing.T, price domain.ModelMetadata) (*Service, *store.DB, Request, int64) {
	t.Helper()
	db, err := store.OpenTest(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	enc, err := crypto.New("accounting-regression-key")
	if err != nil {
		t.Fatal(err)
	}
	svc, err := buildPoolService(db, enc)
	if err != nil {
		t.Fatal(err)
	}
	key, err := db.DownstreamKey.Create(&domain.DownstreamKey{Name: "accounting", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	ch, err := db.Channel.Create(&domain.Channel{Name: "accounting", Status: domain.StatusEnabled})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.ModelMetadata.Upsert(&price); err != nil {
		t.Fatal(err)
	}
	return svc, db, Request{RequestID: "accounting-request", Model: price.ModelName, DownstreamKeyID: key, OpenAIPath: "chat/completions"}, ch
}

func TestAccountingFixedFeeWithoutUsage(t *testing.T) {
	svc, db, req, ch := accountingFixture(t, domain.ModelMetadata{ModelName: "fixed", PricePerRequest: 0.05})
	svc.RecordUsage(req, ch, 200, usage.Tokens{})
	var cost float64
	var total int
	if err := db.QueryRow("SELECT cost,total_tokens FROM usage_records WHERE request_id=?", req.RequestID).Scan(&cost, &total); err != nil {
		t.Fatal(err)
	}
	if cost != 0.05 || total != 0 {
		t.Fatalf("cost=%v total=%d; want fixed fee without invented tokens", cost, total)
	}
	var spent float64
	var used int64
	if err := db.QueryRow("SELECT quota_used_cost,quota_used_tokens FROM downstream_keys WHERE id=?", req.DownstreamKeyID).Scan(&spent, &used); err != nil {
		t.Fatal(err)
	}
	if spent != 0.05 || used != 0 {
		t.Fatalf("key spend=%v tokens=%d", spent, used)
	}
	req.RequestID = "failed-request"
	svc.RecordUsage(req, ch, 503, usage.Tokens{})
	var count int
	if err := db.QueryRow("SELECT count(*) FROM usage_records WHERE request_id=?", req.RequestID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("failed request without usage must not incur a fixed fee")
	}
}

func TestAccountingProtocolCacheBuckets(t *testing.T) {
	cases := []struct {
		name, body    string
		want          float64
		prompt, total int
	}{
		{"OpenAI", `{"usage":{"prompt_tokens":1000,"completion_tokens":100,"total_tokens":1100,"prompt_tokens_details":{"cached_tokens":800}}}`, .48, 1000, 1100},
		{"Responses", `{"usage":{"input_tokens":1000,"output_tokens":100,"total_tokens":1100,"input_tokens_details":{"cached_tokens":800}}}`, .48, 1000, 1100},
		{"Gemini", `{"usageMetadata":{"promptTokenCount":1000,"candidatesTokenCount":100,"totalTokenCount":1100,"cachedContentTokenCount":800}}`, .48, 1000, 1100},
		{"Anthropic", `{"usage":{"input_tokens":200,"output_tokens":100,"cache_read_input_tokens":800,"cache_creation_input_tokens":50}}`, .53, 200, 300},
		{"Anthropic pivot", `{"usage":{"prompt_tokens":200,"completion_tokens":100,"total_tokens":300,"cache_read_tokens":800,"cache_creation_tokens":50}}`, .53, 200, 300},
		{"Cache-only Anthropic", `{"usage":{"cache_read_input_tokens":800}}`, .08, 0, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc, db, req, ch := accountingFixture(t, domain.ModelMetadata{ModelName: "cached", PricePromptPer1k: 1, PriceCompletionPer1k: 2, PriceCachePer1k: .1})
			tokens := usage.ExtractFromJSONBody([]byte(tc.body))
			svc.RecordUsage(req, ch, 200, tokens)
			var cost float64
			var prompt, total int
			if err := db.QueryRow("SELECT cost,prompt_tokens,total_tokens FROM usage_records WHERE request_id=?", req.RequestID).Scan(&cost, &prompt, &total); err != nil {
				t.Fatal(err)
			}
			if math.Abs(cost-tc.want) > 1e-9 {
				t.Fatalf("cost=%v want %v", cost, tc.want)
			}
			if prompt != tc.prompt || total != tc.total {
				t.Fatalf("original quota counters changed: prompt=%d total=%d", prompt, total)
			}
		})
	}
}

func TestAccountingExplicitRuleSnapshotAndEvidence(t *testing.T) {
	svc, db, req, ch := accountingFixture(t, domain.ModelMetadata{ModelName: "m", PricePromptPer1k: 9})
	input, output, cache, write := 1.0, 2.0, 0.0, 1.25
	r := domain.BillingRule{State: "explicit", Mode: "token", Input: &input, Output: &output, CacheRead: &cache, CacheWrite: &write, Timezone: "UTC", Source: "manual"}
	saved, err := db.SaveBillingRule("model", "m", r, 0)
	if err != nil {
		t.Fatal(err)
	}
	req.BillingSnapshot = svc.freezeBilling(req)
	if req.BillingSnapshot == nil {
		t.Fatal("explicit rule not frozen")
	}
	r.Input = &output
	if _, err = db.SaveBillingRule("model", "m", r, saved.Revision); err != nil {
		t.Fatal(err)
	}
	tokens := usage.ExtractFromJSONBody([]byte(`{"usage":{"prompt_tokens":1000,"completion_tokens":100,"total_tokens":1100,"prompt_tokens_details":{"cached_tokens":800}}}`))
	svc.RecordUsage(req, ch, 200, tokens)
	var cost float64
	var status, evidence string
	if err = db.QueryRow("SELECT cost,billing_status,billing_evidence FROM usage_records WHERE request_id=?", req.RequestID).Scan(&cost, &status, &evidence); err != nil {
		t.Fatal(err)
	}
	if cost != .4 || status != "settled" || evidence == "" {
		t.Fatalf("snapshot cost=%v status=%q evidence=%q", cost, status, evidence)
	}
	records, err := db.Usage.List(store.UsageFilter{})
	if err != nil || len(records) != 1 || records[0].BillingEvidence != evidence {
		t.Fatalf("explanation not readable: %v", err)
	}
	req.BillingSnapshot = nil
	req.RequestID = "free"
	if _, err = db.SaveBillingRule("model", "m", domain.BillingRule{State: "free", Timezone: "UTC", Source: "manual"}, saved.Revision+1); err != nil {
		t.Fatal(err)
	}
	svc.RecordUsage(req, ch, 200, usage.Tokens{})
	if err = db.QueryRow("SELECT cost,billing_status FROM usage_records WHERE request_id=?", req.RequestID).Scan(&cost, &status); err != nil {
		t.Fatal(err)
	}
	if cost != 0 || status != "explicitly_free" {
		t.Fatalf("free=%v %s", cost, status)
	}
}

func TestAccountingUnpricedSnapshotDoesNotFollowLaterPrice(t *testing.T) {
	svc, db, req, ch := accountingFixture(t, domain.ModelMetadata{ModelName: "m"})
	req.BillingSnapshot = svc.freezeBilling(req)
	if req.BillingSnapshot == nil {
		t.Fatal("unpriced snapshot missing")
	}
	fee := .05
	if _, err := db.SaveBillingRule("model", "m", domain.BillingRule{State: "explicit", Mode: "request", PerRequest: &fee, Timezone: "UTC", Source: "manual"}, 0); err != nil {
		t.Fatal(err)
	}
	svc.RecordUsage(req, ch, 200, usage.Tokens{PromptTokens: 100, TotalTokens: 100})
	var cost float64
	var status string
	if err := db.QueryRow("SELECT cost,billing_status FROM usage_records WHERE request_id=?", req.RequestID).Scan(&cost, &status); err != nil {
		t.Fatal(err)
	}
	if cost != 0 || status != "unpriced" {
		t.Fatalf("late rule repriced request: %v %s", cost, status)
	}
}
func TestAccountingExplicitTierFreeCacheAndFixedFee(t *testing.T) {
	svc, db, req, ch := accountingFixture(t, domain.ModelMetadata{ModelName: "m"})
	write := 1.0
	r := domain.BillingRule{State: "explicit", Mode: "token", Tiers: []domain.PriceTier{{Prompt: 1, Completion: 2, Cache: 0}}, CacheWrite: &write, Timezone: "UTC", Source: "manual"}
	if _, err := db.SaveBillingRule("model", "m", r, 0); err != nil {
		t.Fatal(err)
	}
	svc.RecordUsage(req, ch, 200, usage.Tokens{PromptTokens: 1000, CompletionTokens: 100, TotalTokens: 1100, CacheReadTokens: 800, PromptIncludesCacheRead: true})
	var cost float64
	var status string
	if err := db.QueryRow("SELECT cost,billing_status FROM usage_records WHERE request_id=?", req.RequestID).Scan(&cost, &status); err != nil {
		t.Fatal(err)
	}
	if cost != .4 || status != "settled" {
		t.Fatalf("free cache=%v %s", cost, status)
	}
	fee := .05
	r = domain.BillingRule{State: "explicit", Mode: "request", PerRequest: &fee, Timezone: "UTC", Source: "manual"}
	if _, err := db.SaveBillingRule("model", "m", r, 1); err != nil {
		t.Fatal(err)
	}
	req.RequestID = "fixed-with-cache"
	svc.RecordUsage(req, ch, 200, usage.Tokens{PromptTokens: 1000, TotalTokens: 1000, CacheReadTokens: 800, PromptIncludesCacheRead: true})
	if err := db.QueryRow("SELECT cost,billing_status FROM usage_records WHERE request_id=?", req.RequestID).Scan(&cost, &status); err != nil {
		t.Fatal(err)
	}
	if cost != .05 || status != "settled" {
		t.Fatalf("fixed cache=%v %s", cost, status)
	}
}
