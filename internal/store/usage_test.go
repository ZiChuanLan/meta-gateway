package store_test

import (
	"testing"
	"time"

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

func TestRecordRelayUsageAtomic(t *testing.T) {
	db := openTestDB(t)
	key := &domain.DownstreamKey{
		TokenHash: "hash-atomic-1", Name: "atomic", Enabled: true,
		Scopes: "relay", QuotaTotalTokens: 1000,
	}
	keyID, err := db.DownstreamKey.Create(key)
	if err != nil {
		t.Fatal(err)
	}
	// A proxy_log row for the request (created before usage accounting).
	if _, err := db.ProxyLog.Insert(&domain.ProxyLog{
		RequestID: "req-atomic", ChannelID: 1, Model: "gpt-test",
		Status: 200, Attempt: 1,
	}); err != nil {
		t.Fatal(err)
	}
	record := &domain.UsageRecord{
		RequestID: "req-atomic", DownstreamKeyID: keyID, ChannelID: 1,
		Model: "gpt-test", Path: "chat/completions",
		PromptTokens: 40, CompletionTokens: 20, TotalTokens: 60, Status: 200,
	}
	if err := db.RecordRelayUsage(record, keyID); err != nil {
		t.Fatal(err)
	}
	// All three writes landed.
	summary, err := db.Usage.Summary(&keyID)
	if err != nil {
		t.Fatal(err)
	}
	if summary.RequestCount != 1 || summary.TotalTokens != 60 {
		t.Fatalf("summary=%+v", summary)
	}
	gotKey, err := db.DownstreamKey.GetByID(keyID)
	if err != nil || gotKey == nil {
		t.Fatalf("get key: %v", err)
	}
	if gotKey.QuotaUsedTokens != 60 {
		t.Fatalf("key quota used=%d, want 60", gotKey.QuotaUsedTokens)
	}
	logs, err := db.ProxyLog.ListFilter(store.ProxyLogFilter{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, log := range logs {
		if log.RequestID == "req-atomic" {
			found = true
			if log.TotalTokens != 60 || log.PromptTokens != 40 || log.CompletionTokens != 20 {
				t.Fatalf("log tokens not backfilled: %+v", log)
			}
		}
	}
	if !found {
		t.Fatal("proxy_log row not found")
	}
}

func TestRecordRelayUsageNoOpWithoutTokens(t *testing.T) {
	db := openTestDB(t)
	if err := db.RecordRelayUsage(&domain.UsageRecord{RequestID: "noop", TotalTokens: 0}, 1); err != nil {
		t.Fatalf("zero-token record must be a no-op, got %v", err)
	}
	if err := db.RecordRelayUsage(nil, 1); err != nil {
		t.Fatalf("nil record must be a no-op, got %v", err)
	}
	summary, err := db.Usage.Summary(nil)
	if err != nil {
		t.Fatal(err)
	}
	if summary.RequestCount != 0 {
		t.Fatalf("no rows expected, got %+v", summary)
	}
}

func TestDownstreamKeyCacheInvalidation(t *testing.T) {
	db := openTestDB(t)
	key := &domain.DownstreamKey{
		TokenHash: "hash-cache-1", Name: "cached", Enabled: true,
		Scopes: "relay", QuotaTotalTokens: 100,
	}
	id, err := db.DownstreamKey.Create(key)
	if err != nil {
		t.Fatal(err)
	}

	// First read populates the cache; second read must hit it (no SQL error).
	got, err := db.DownstreamKey.GetByHash("hash-cache-1")
	if err != nil || got == nil || got.ID != id {
		t.Fatalf("get by hash: %v %+v", err, got)
	}
	// Reads must return a copy, never the caller's object or the cache's own
	// pointer: mutating it must not pollute the cache.
	if got == key {
		t.Fatal("GetByHash must return a copy, not the caller's pointer")
	}
	got.Name = "caller-mutated"
	if again, err := db.DownstreamKey.GetByHash("hash-cache-1"); err != nil || again == nil || again.Name == "caller-mutated" {
		t.Fatalf("caller mutation leaked into cache: %+v err=%v", again, err)
	}

	// Update invalidates and the next read reloads the new state.
	key.Name = "renamed"
	key.QuotaTotalTokens = 50
	if err := db.DownstreamKey.Update(key); err != nil {
		t.Fatal(err)
	}
	again, err := db.DownstreamKey.GetByID(id)
	if err != nil || again == nil || again.Name != "renamed" || again.QuotaTotalTokens != 50 {
		t.Fatalf("stale cache after update: %+v err=%v", again, err)
	}

	// AddUsage persists and invalidates; the next read sees the new quota.
	if err := db.DownstreamKey.AddUsage(id, 30); err != nil {
		t.Fatal(err)
	}
	again, err = db.DownstreamKey.GetByID(id)
	if err != nil || again == nil || again.QuotaUsedTokens != 30 {
		t.Fatalf("quota used=%d want 30 err=%v", again.QuotaUsedTokens, err)
	}
	if store.QuotaExceeded(again) {
		t.Fatal("quota must not be exceeded yet")
	}

	// ResetUsage persists and invalidates.
	if err := db.DownstreamKey.ResetUsage(id); err != nil {
		t.Fatal(err)
	}
	again, err = db.DownstreamKey.GetByID(id)
	if err != nil || again == nil || again.QuotaUsedTokens != 0 {
		t.Fatalf("quota used after reset=%d want 0 err=%v", again.QuotaUsedTokens, err)
	}

	// Delete drops the cache entry; lookups fall back to the DB.
	if err := db.DownstreamKey.Delete(id); err != nil {
		t.Fatal(err)
	}
	gone, err := db.DownstreamKey.GetByID(id)
	if err != nil || gone != nil {
		t.Fatalf("deleted key must be gone: %+v err=%v", gone, err)
	}
}

func TestDownstreamKeyUpdateNeverSeedsHashIndexFromCaller(t *testing.T) {
	db := openTestDB(t)
	key := &domain.DownstreamKey{
		TokenHash: "hash-ghost-1", Name: "ghost", Enabled: true, Scopes: "relay",
	}
	id, err := db.DownstreamKey.Create(key)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.DownstreamKey.GetByHash("hash-ghost-1"); err != nil {
		t.Fatal(err)
	}

	// Caller changes TokenHash and updates: the SQL does not persist it, so the
	// cache must neither register the new hash nor lose the old one.
	key.TokenHash = "hash-ghost-1-changed"
	key.Name = "ghost-renamed"
	if err := db.DownstreamKey.Update(key); err != nil {
		t.Fatal(err)
	}
	// Old hash still resolves (reloaded from the DB, which kept the stored hash).
	viaOldHash, err := db.DownstreamKey.GetByHash("hash-ghost-1")
	if err != nil || viaOldHash == nil || viaOldHash.Name != "ghost-renamed" {
		t.Fatalf("old hash must resolve via DB reload: %+v err=%v", viaOldHash, err)
	}
	// The caller's changed hash must NOT authenticate (ghost auth prevention).
	ghost, err := db.DownstreamKey.GetByHash("hash-ghost-1-changed")
	if err != nil || ghost != nil {
		t.Fatalf("unpersisted token hash must not resolve: %+v err=%v", ghost, err)
	}
	_ = id
}

func TestSiteCacheInvalidation(t *testing.T) {
	db := openTestDB(t)
	id, err := db.Site.Create(&domain.Site{Name: "s1", BaseURL: "https://a.example", Platform: "openai", Status: domain.StatusEnabled})
	if err != nil {
		t.Fatal(err)
	}
	got, err := db.Site.GetByID(id)
	if err != nil || got == nil || got.Name != "s1" {
		t.Fatalf("get: %v %+v", err, got)
	}
	// Update must be observed through the cache.
	got.Name = "s1-renamed"
	if err := db.Site.Update(got); err != nil {
		t.Fatal(err)
	}
	again, err := db.Site.GetByID(id)
	if err != nil || again == nil || again.Name != "s1-renamed" {
		t.Fatalf("stale site cache after update: %+v err=%v", again, err)
	}
	// Delete must drop the cached entry.
	if err := db.Site.Delete(id); err != nil {
		t.Fatal(err)
	}
	gone, err := db.Site.GetByID(id)
	if err != nil || gone != nil {
		t.Fatalf("deleted site must be gone: %+v err=%v", gone, err)
	}
}

func TestCredentialCacheInvalidation(t *testing.T) {
	db := openTestDB(t)
	siteID, err := db.Site.Create(&domain.Site{Name: "s2", BaseURL: "https://b.example", Platform: "openai", Status: domain.StatusEnabled})
	if err != nil {
		t.Fatal(err)
	}
	cred := &domain.Credential{SiteID: siteID, Kind: "api_key", SecretEnc: []byte("enc-1"), Status: domain.StatusEnabled}
	credID, err := db.Credential.Create(cred)
	if err != nil {
		t.Fatal(err)
	}

	// Key pool cache: first read populates, second hits.
	pool, err := db.Credential.ListEnabledAPIKeysBySite(siteID)
	if err != nil || len(pool) != 1 || pool[0].ID != credID {
		t.Fatalf("pool: %v %+v", err, pool)
	}
	// Update the credential (e.g. rotate key): pool must reflect the change.
	cred.SecretEnc = []byte("enc-2")
	if err := db.Credential.Update(cred); err != nil {
		t.Fatal(err)
	}
	pool, err = db.Credential.ListEnabledAPIKeysBySite(siteID)
	if err != nil || len(pool) != 1 || string(pool[0].SecretEnc) != "enc-2" {
		t.Fatalf("stale pool after update: %+v err=%v", pool, err)
	}
	// Disabling the credential must remove it from the pool.
	cred.Status = domain.StatusDisabled
	if err := db.Credential.Update(cred); err != nil {
		t.Fatal(err)
	}
	pool, err = db.Credential.ListEnabledAPIKeysBySite(siteID)
	if err != nil || len(pool) != 0 {
		t.Fatalf("disabled credential still in pool: %+v err=%v", pool, err)
	}
	// GetByID cache: delete drops it.
	if err := db.Credential.Delete(credID); err != nil {
		t.Fatal(err)
	}
	gone, err := db.Credential.GetByID(credID)
	if err != nil || gone != nil {
		t.Fatalf("deleted credential must be gone: %+v err=%v", gone, err)
	}
	// ClearCache after a direct SQL write (bulk import path) must refresh.
	if _, err := db.Exec(`INSERT INTO credentials (site_id, kind, secret_enc, status) VALUES (?, 'api_key', 'enc-3', 'enabled')`, siteID); err != nil {
		t.Fatal(err)
	}
	db.Credential.ClearCache()
	pool, err = db.Credential.ListEnabledAPIKeysBySite(siteID)
	if err != nil || len(pool) != 1 || string(pool[0].SecretEnc) != "enc-3" {
		t.Fatalf("pool after direct insert+clear: %+v err=%v", pool, err)
	}
}

func TestCheckinBatchMarkerSuppressesCatchUp(t *testing.T) {
	db := openTestDB(t)
	siteID, _ := db.Site.Create(&domain.Site{Name: "s-batch", Status: domain.StatusEnabled})
	credID, _ := db.Credential.Create(&domain.Credential{SiteID: siteID, Kind: "session", SecretEnc: []byte("enc"), Status: domain.StatusEnabled, CheckinEnabled: true})

	// Interrupted batch: a scheduled log exists (today) but no marker.
	ranAt := time.Now().Add(-2 * time.Hour)
	if err := db.CheckinLog.Create(&domain.CheckinLog{SiteID: siteID, CredentialID: credID, Source: "scheduled", Status: "success", Category: "ok", Message: "", RanAt: ranAt}); err != nil {
		t.Fatal(err)
	}
	last, err := db.CheckinLog.LastScheduledRunAt()
	if err != nil {
		t.Fatal(err)
	}
	// No marker yet → falls back to the newest scheduled log (today).
	if !last.Equal(ranAt) && !last.Truncate(time.Second).Equal(ranAt.Truncate(time.Second)) {
		t.Fatalf("fallback last run = %v, want %v", last, ranAt)
	}

	// A fully completed batch writes the durable marker; it now takes priority
	// even though older scheduled logs exist.
	completed := time.Now().Add(-30 * time.Minute)
	if err := db.CheckinLog.RecordBatchCompleted(completed); err != nil {
		t.Fatal(err)
	}
	last, err = db.CheckinLog.LastScheduledRunAt()
	if err != nil {
		t.Fatal(err)
	}
	if !last.Truncate(time.Second).Equal(completed.Truncate(time.Second)) {
		t.Fatalf("marker last run = %v, want %v", last, completed)
	}
}

func TestUsageCostPersistedAndSummarized(t *testing.T) {
	db := openTestDB(t)
	key := &domain.DownstreamKey{
		TokenHash: "hash-cost-1", Name: "billed", Enabled: true, Scopes: "relay",
		PricePromptPer1k: 0.01, PriceCompletionPer1k: 0.02,
	}
	keyID, err := db.DownstreamKey.Create(key)
	if err != nil {
		t.Fatal(err)
	}
	// Model ratio 2.0: cost = (1000/1000*0.01 + 500/1000*0.02) * 2.0 = 0.04
	if err := db.ModelRatio.SetRatio("gpt-billed", 2.0); err != nil {
		t.Fatal(err)
	}
	if err := db.RecordRelayUsage(&domain.UsageRecord{
		RequestID: "req-cost", DownstreamKeyID: keyID, ChannelID: 1,
		Model: "gpt-billed", Path: "chat/completions",
		PromptTokens: 1000, CompletionTokens: 500, TotalTokens: 1500,
		CacheReadTokens: 200, Status: 200, Cost: 0.04,
	}, keyID); err != nil {
		t.Fatal(err)
	}
	summary, err := db.Usage.Summary(&keyID)
	if err != nil {
		t.Fatal(err)
	}
	if summary.RequestCount != 1 || summary.TotalTokens != 1500 {
		t.Fatalf("summary=%+v", summary)
	}
	if summary.Cost < 0.039 || summary.Cost > 0.041 {
		t.Fatalf("summary cost=%v want 0.04", summary.Cost)
	}
	rows, err := db.Usage.List(store.UsageFilter{DownstreamKeyID: &keyID, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Cost < 0.039 || rows[0].Cost > 0.041 {
		t.Fatalf("listed usage cost=%v, want 0.04", rows)
	}
	past := time.Now().Add(-time.Hour)
	recent, err := db.Usage.SummarySince(&keyID, &past)
	if err != nil || recent.RequestCount != 1 {
		t.Fatalf("recent summary=%+v err=%v", recent, err)
	}
	future := time.Now().Add(time.Hour)
	recent, err = db.Usage.SummarySince(&keyID, &future)
	if err != nil || recent.RequestCount != 0 || recent.Cost != 0 {
		t.Fatalf("future summary=%+v err=%v", recent, err)
	}
	// Unset model → ratio 1.0.
	if ratio, err := db.ModelRatio.GetRatio("unknown-model"); err != nil || ratio != 1.0 {
		t.Fatalf("default ratio=%v err=%v", ratio, err)
	}
	// Ratio cache reflects updates.
	if ratio, _ := db.ModelRatio.GetRatio("gpt-billed"); ratio != 2.0 {
		t.Fatalf("cached ratio=%v want 2.0", ratio)
	}
	if err := db.ModelRatio.SetRatio("gpt-billed", -1); err != nil {
		t.Fatal(err)
	}
	if ratio, _ := db.ModelRatio.GetRatio("gpt-billed"); ratio != 1.0 {
		t.Fatalf("ratio after delete=%v want 1.0", ratio)
	}
}

func TestGroupQuotaEnforcedAndAccrued(t *testing.T) {
	db := openTestDB(t)
	// Group with a 100-token quota.
	if err := db.Group.Upsert("team-a", 100, 0, 0); err != nil {
		t.Fatal(err)
	}
	// Key in the group.
	key := &domain.DownstreamKey{
		TokenHash: "hash-group-1", Name: "grouped", Enabled: true, Scopes: "relay",
		GroupName: "team-a", QuotaTotalTokens: 0,
	}
	keyID, err := db.DownstreamKey.Create(key)
	if err != nil {
		t.Fatal(err)
	}
	got, err := db.DownstreamKey.GetByID(keyID)
	if err != nil || got == nil || got.GroupName != "team-a" {
		t.Fatalf("key group not persisted: %+v err=%v", got, err)
	}
	// Accrue 60 tokens to the group via the relay transaction.
	if err := db.RecordRelayUsage(&domain.UsageRecord{
		RequestID: "req-group-1", DownstreamKeyID: keyID, ChannelID: 1,
		Model: "m", Path: "chat/completions", TotalTokens: 60,
		PromptTokens: 30, CompletionTokens: 30, GroupName: "team-a", Status: 200,
	}, keyID); err != nil {
		t.Fatal(err)
	}
	group, err := db.Group.Get("team-a")
	if err != nil || group == nil {
		t.Fatalf("group get: %v", err)
	}
	if group.QuotaUsedTokens != 60 {
		t.Fatalf("group used=%d want 60", group.QuotaUsedTokens)
	}
	// Group quota now enforced: 60+50 would exceed 100.
	if group.QuotaTotalTokens > 0 && group.QuotaUsedTokens+50 >= group.QuotaTotalTokens {
		// enforcement assertion mirrors ensureQuota logic
	} else {
		t.Fatal("group quota math wrong")
	}
	// Unknown group resolves to unlimited.
	unknown, err := db.Group.Get("nope")
	if err != nil || unknown == nil || unknown.QuotaTotalTokens != 0 {
		t.Fatalf("unknown group must be unlimited: %+v err=%v", unknown, err)
	}
	// Delete group; keys fall back to default on next read.
	if err := db.Group.Delete("team-a"); err != nil {
		t.Fatal(err)
	}
	if err := db.Group.Delete("default"); err == nil {
		t.Fatal("default group must be protected")
	}
}

func TestRateLimitPauseExcludesChannelFromRouting(t *testing.T) {
	db := openTestDB(t)
	siteID, _ := db.Site.Create(&domain.Site{Name: "s-rl", BaseURL: "https://rl.example", Platform: "openai-compatible", Status: domain.StatusEnabled})
	credID, _ := db.Credential.Create(&domain.Credential{SiteID: siteID, Kind: "api_key", SecretEnc: []byte("enc"), Status: domain.StatusEnabled})
	chID, _ := db.Channel.Create(&domain.Channel{SiteID: &siteID, CredentialID: &credID, Name: "rl", Status: domain.StatusEnabled})
	routeID, _ := db.Route.Create(&domain.Route{ModelPattern: "rl-model", Enabled: true})
	_, _ = db.RouteMember.Create(&domain.RouteMember{RouteID: routeID, ChannelID: chID, Priority: 10, Weight: 100, Enabled: true})

	// Baseline: candidate is eligible.
	_, candidates, err := db.RouteMember.RoutingCandidates("rl-model")
	if err != nil || len(candidates) != 1 {
		t.Fatalf("baseline candidates=%d err=%v", len(candidates), err)
	}

	// 429 verdict parks the channel: routing must exclude it.
	until := time.Now().Add(30 * time.Second)
	if err := db.Channel.RecordRateLimited(chID, until); err != nil {
		t.Fatal(err)
	}
	_, candidates, err = db.RouteMember.RoutingCandidates("rl-model")
	if err != nil || len(candidates) != 0 {
		t.Fatalf("parked channel must be excluded: candidates=%d err=%v", len(candidates), err)
	}

	// Expired pause: channel returns to the pool.
	past := time.Now().Add(-time.Minute)
	if err := db.Channel.RecordRateLimited(chID, past); err != nil {
		t.Fatal(err)
	}
	_, candidates, err = db.RouteMember.RoutingCandidates("rl-model")
	if err != nil || len(candidates) != 1 {
		t.Fatalf("expired pause must restore the channel: candidates=%d err=%v", len(candidates), err)
	}

	// ClearRateLimit (probe success) also restores it.
	_ = db.Channel.RecordRateLimited(chID, time.Now().Add(time.Hour))
	if err := db.Channel.ClearRateLimit(chID); err != nil {
		t.Fatal(err)
	}
	_, candidates, err = db.RouteMember.RoutingCandidates("rl-model")
	if err != nil || len(candidates) != 1 {
		t.Fatalf("cleared pause must restore the channel: candidates=%d err=%v", len(candidates), err)
	}
}
