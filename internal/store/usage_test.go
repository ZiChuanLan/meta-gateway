package store_test

import (
	"testing"

	"github.com/lan/meta-gateway/internal/domain"
	"github.com/lan/meta-gateway/internal/store"
)

func TestUsageAndQuota(t *testing.T) {
	db := openTestDB(t)
	key := &domain.DownstreamKey{
		TokenHash:        "hash-usage-1",
		Name:             "metered",
		Enabled:          true,
		Scopes:           "relay",
		QuotaTotalTokens: 100,
	}
	id, err := db.DownstreamKey.Create(key)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Usage.Insert(&domain.UsageRecord{
		RequestID:        "r1",
		DownstreamKeyID:  id,
		ChannelID:        1,
		Model:            "gpt-test",
		Path:             "chat/completions",
		PromptTokens:     40,
		CompletionTokens: 20,
		TotalTokens:      60,
		Status:           200,
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.DownstreamKey.AddUsage(id, 60); err != nil {
		t.Fatal(err)
	}
	got, err := db.DownstreamKey.GetByID(id)
	if err != nil || got == nil {
		t.Fatalf("get key: %v", err)
	}
	if got.QuotaUsedTokens != 60 {
		t.Fatalf("used=%d", got.QuotaUsedTokens)
	}
	if store.QuotaExceeded(got) {
		t.Fatal("should not exceed yet")
	}
	if err := db.DownstreamKey.AddUsage(id, 50); err != nil {
		t.Fatal(err)
	}
	got, _ = db.DownstreamKey.GetByID(id)
	if !store.QuotaExceeded(got) {
		t.Fatal("expected quota exceeded")
	}
	summary, err := db.Usage.Summary(&id)
	if err != nil {
		t.Fatal(err)
	}
	if summary.TotalTokens != 60 || summary.RequestCount != 1 {
		t.Fatalf("summary=%+v", summary)
	}
}
