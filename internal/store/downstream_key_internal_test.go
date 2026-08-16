package store

import (
	"testing"

	"github.com/lan/meta-gateway/internal/domain"
)

// TestUsageBumpsCacheInPlace verifies the hot-path accounting writes update
// the cached DownstreamKey in place instead of invalidating it, so the next
// auth read still hits the in-process cache rather than reloading from
// SQLite (this was the "cache always misses" regression).
func TestUsageBumpsCacheInPlace(t *testing.T) {
	dir := t.TempDir()
	db, err := Open(dir)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	key := &domain.DownstreamKey{
		TokenHash:        "hash-cache-bump",
		Name:             "metered",
		Enabled:          true,
		Scopes:           "relay",
		QuotaTotalTokens: 1000,
	}
	id, err := db.DownstreamKey.Create(key)
	if err != nil {
		t.Fatal(err)
	}
	// Create warms the cache; confirm the entry exists.
	if _, ok := db.DownstreamKey.byHash["hash-cache-bump"]; !ok {
		t.Fatal("cache should be warm after Create")
	}

	// bumpCachedUsage (the RecordRelayUsage path): entry survives, value moves.
	db.DownstreamKey.bumpCachedUsage(id, 40)
	cached, ok := db.DownstreamKey.byHash["hash-cache-bump"]
	if !ok {
		t.Fatal("cache entry must survive bumpCachedUsage")
	}
	if cached.QuotaUsedTokens != 40 {
		t.Fatalf("cached used=%d want 40", cached.QuotaUsedTokens)
	}
	// byID and byHash share the same object.
	if byID := db.DownstreamKey.byID[id]; byID == nil || byID.QuotaUsedTokens != 40 {
		t.Fatalf("byID mirror not updated: %+v", byID)
	}

	// AddUsage path: entry survives, value accumulates.
	if err := db.DownstreamKey.AddUsage(id, 10); err != nil {
		t.Fatal(err)
	}
	cached, ok = db.DownstreamKey.byHash["hash-cache-bump"]
	if !ok {
		t.Fatal("cache entry must survive AddUsage")
	}
	if cached.QuotaUsedTokens != 50 {
		t.Fatalf("cached used=%d want 50", cached.QuotaUsedTokens)
	}

	// Readers observe the fresh quota without any SQL round-trip.
	got, err := db.DownstreamKey.GetByHash("hash-cache-bump")
	if err != nil || got == nil {
		t.Fatalf("get by hash: %v", err)
	}
	if got.QuotaUsedTokens != 50 {
		t.Fatalf("read used=%d want 50", got.QuotaUsedTokens)
	}
	if QuotaExceeded(got) {
		t.Fatal("should not exceed yet")
	}

	// Administrative writes still invalidate (fresh read from DB).
	if err := db.DownstreamKey.ResetUsage(id); err != nil {
		t.Fatal(err)
	}
	if _, ok := db.DownstreamKey.byHash["hash-cache-bump"]; ok {
		t.Fatal("ResetUsage must invalidate the cache entry")
	}
}

// TestDownstreamKeyPriceCachePer1kPersists pins the full store round-trip for
// the cache-token unit price: it was write-only for a whole feature cycle
// (never selected, never inserted) so listings always showed 0 and partial
// updates silently wiped the stored value.
func TestDownstreamKeyPriceCachePer1kPersists(t *testing.T) {
	dir := t.TempDir()
	db, err := Open(dir)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	key := &domain.DownstreamKey{
		TokenHash:            "hash-price-cache",
		Name:                 "priced",
		Enabled:              true,
		Scopes:               "relay",
		PricePromptPer1k:     0.1,
		PriceCompletionPer1k: 0.2,
		PriceCachePer1k:      0.05,
	}
	id, err := db.DownstreamKey.Create(key)
	if err != nil {
		t.Fatal(err)
	}
	for _, read := range []string{"by-id", "by-hash", "list"} {
		var got *domain.DownstreamKey
		switch read {
		case "by-id":
			got, err = db.DownstreamKey.GetByID(id)
		case "by-hash":
			got, err = db.DownstreamKey.GetByHash("hash-price-cache")
		case "list":
			keys, listErr := db.DownstreamKey.List()
			if listErr != nil {
				t.Fatal(listErr)
			}
			for i := range keys {
				if keys[i].ID == id {
					got = &keys[i]
				}
			}
		}
		if err != nil || got == nil {
			t.Fatalf("%s read: %+v err=%v", read, got, err)
		}
		if got.PriceCachePer1k != 0.05 {
			t.Fatalf("%s read price_cache = %v, want 0.05", read, got.PriceCachePer1k)
		}
	}
}

// TestGroupGetReturnsCopy guards the cache discipline: a caller mutating the
// returned group must never corrupt the cached entry other requests share.
func TestGroupGetReturnsCopy(t *testing.T) {
	dir := t.TempDir()
	db, err := Open(dir)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	if err := db.Group.Upsert("shared", 500, 0, 0); err != nil {
		t.Fatal(err)
	}
	first, err := db.Group.Get("shared")
	if err != nil || first == nil {
		t.Fatalf("get: %v", err)
	}
	first.QuotaUsedTokens = 9999
	first.QuotaTotalTokens = 1

	second, err := db.Group.Get("shared")
	if err != nil || second == nil {
		t.Fatalf("second get: %v", err)
	}
	if second.QuotaUsedTokens != 0 || second.QuotaTotalTokens != 500 {
		t.Fatalf("cached group polluted by caller mutation: %+v", second)
	}
}
