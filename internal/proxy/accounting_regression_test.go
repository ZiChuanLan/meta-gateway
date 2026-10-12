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
