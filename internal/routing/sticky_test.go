package routing

import (
	"context"
	"testing"
	"time"

	"github.com/lan/meta-gateway/internal/domain"
)

func stickyCandidate(id, priority, weight int64) domain.RoutingCandidate {
	c := candidate(id, priority, weight)
	c.Channel.Name = "ch"
	return c
}

func TestStickyStoreBindLookupExpiry(t *testing.T) {
	now := time.Date(2026, 7, 14, 0, 0, 0, 0, time.UTC)
	clock := fakeClock{now: now}
	store := NewStickyStore(10*time.Minute, clock)

	if _, ok := store.Lookup("sess-1", now); ok {
		t.Fatal("empty store must not hit")
	}
	store.Bind("sess-1", 42, now)
	channelID, ok := store.Lookup("sess-1", now)
	if !ok || channelID != 42 {
		t.Fatalf("expected bound channel 42, got %d ok=%v", channelID, ok)
	}
	// TTL expiry: one nanosecond past the window the binding is gone.
	if _, ok := store.Lookup("sess-1", now.Add(10*time.Minute+time.Nanosecond)); ok {
		t.Fatal("binding must expire after TTL")
	}
	// Refresh keeps the binding alive.
	store.Bind("sess-1", 42, now.Add(9*time.Minute))
	if _, ok := store.Lookup("sess-1", now.Add(10*time.Minute)); !ok {
		t.Fatal("refreshed binding must survive within the new window")
	}
	if _, ok := store.Lookup("sess-1", now.Add(19*time.Minute+time.Nanosecond)); ok {
		t.Fatal("refreshed binding must expire after its window")
	}
}

func TestStickyStoreStatsAndSnapshot(t *testing.T) {
	now := time.Date(2026, 7, 14, 0, 0, 0, 0, time.UTC)
	store := NewStickyStore(time.Minute, fakeClock{now: now})
	store.Bind("a", 1, now)
	store.Bind("b", 2, now)
	store.RecordHit()
	store.RecordHit()
	store.RecordEscape()
	stats := store.Stats()
	if stats.BoundSessions != 2 || stats.Binds != 2 || stats.Hits != 2 || stats.Escapes != 1 {
		t.Fatalf("unexpected stats: %+v", stats)
	}
	snapshot := store.Snapshot(10)
	if len(snapshot) != 2 {
		t.Fatalf("expected 2 snapshot entries, got %d", len(snapshot))
	}
	// Expired entries are pruned from stats and snapshots.
	store2 := NewStickyStore(time.Minute, fakeClock{now: now})
	store2.Bind("old", 1, now.Add(-2*time.Minute))
	stats2 := store2.Stats()
	if stats2.BoundSessions != 0 {
		t.Fatalf("expired entry must be pruned, got %d", stats2.BoundSessions)
	}
}

func TestSelectStickyPrefersBoundChannel(t *testing.T) {
	now := time.Date(2026, 7, 14, 0, 0, 0, 0, time.UTC)
	repo := fakeRepo{route: &domain.Route{ID: 1}, candidates: []domain.RoutingCandidate{
		stickyCandidate(1, 10, 1), stickyCandidate(2, 10, 100),
	}}
	clock := fakeClock{now: now}
	store := NewStickyStore(time.Minute, clock)
	selector := NewWithDependencies(repo, clock, &fakeRandom{values: []int{1}})
	selector.SetSticky(store)

	// Without a session the heavy channel wins; with a bound session the
	// bound channel wins even though its weight is tiny.
	decision, err := selector.Select(context.Background(), "model", nil)
	if err != nil || decision.Selected.Channel.ID != 2 {
		t.Fatalf("no-session pick must use weights, got %+v err=%v", decision.Selected, err)
	}
	store.Bind("sess-1", 1, now)
	decision, err = selector.SelectSticky(context.Background(), "model", nil, "sess-1")
	if err != nil || decision.Selected.Channel.ID != 1 {
		t.Fatalf("sticky pick must prefer bound channel 1, got %+v err=%v", decision.Selected, err)
	}
	if !decision.StickyHit {
		t.Fatalf("expected sticky hit, got %+v", decision.Explanation)
	}
	if store.Stats().Hits != 1 {
		t.Fatalf("expected 1 hit, got %+v", store.Stats())
	}
}

func TestSelectStickyEscapesOnCooldown(t *testing.T) {
	now := time.Date(2026, 7, 14, 0, 0, 0, 0, time.UTC)
	cooling := stickyCandidate(1, 10, 100)
	until := now.Add(time.Hour)
	cooling.Member.CooldownUntil = &until
	repo := fakeRepo{route: &domain.Route{ID: 1}, candidates: []domain.RoutingCandidate{
		cooling, stickyCandidate(2, 10, 100),
	}}
	clock := fakeClock{now: now}
	store := NewStickyStore(time.Minute, clock)
	selector := NewWithDependencies(repo, clock, &fakeRandom{values: []int{0}})
	selector.SetSticky(store)
	store.Bind("sess-1", 1, now)

	decision, err := selector.SelectSticky(context.Background(), "model", nil, "sess-1")
	if err != nil || decision.Selected.Channel.ID != 2 {
		t.Fatalf("escape must pick the healthy channel, got %+v err=%v", decision.Selected, err)
	}
	if decision.StickyHit {
		t.Fatal("escape must not count as a hit")
	}
	if decision.StickyReason != string(ReasonCoolingDown) {
		t.Fatalf("expected cooldown escape reason, got %q", decision.StickyReason)
	}
	if store.Stats().Escapes != 1 {
		t.Fatalf("expected 1 escape, got %+v", store.Stats())
	}
}

func TestSelectStickyEscapesOnExcluded(t *testing.T) {
	now := time.Date(2026, 7, 14, 0, 0, 0, 0, time.UTC)
	repo := fakeRepo{route: &domain.Route{ID: 1}, candidates: []domain.RoutingCandidate{
		stickyCandidate(1, 10, 100), stickyCandidate(2, 10, 100),
	}}
	clock := fakeClock{now: now}
	store := NewStickyStore(time.Minute, clock)
	selector := NewWithDependencies(repo, clock, &fakeRandom{values: []int{0}})
	selector.SetSticky(store)
	store.Bind("sess-1", 1, now)

	// Channel 1 was already attempted in this request: sticky must escape.
	decision, err := selector.SelectSticky(context.Background(), "model", map[int64]struct{}{1: {}}, "sess-1")
	if err != nil || decision.Selected.Channel.ID != 2 {
		t.Fatalf("escape must pick channel 2, got %+v err=%v", decision.Selected, err)
	}
	if decision.StickyReason != string(ReasonExcluded) {
		t.Fatalf("expected excluded escape reason, got %q", decision.StickyReason)
	}
}

func TestSelectStickyIgnoresExpiredBinding(t *testing.T) {
	now := time.Date(2026, 7, 14, 0, 0, 0, 0, time.UTC)
	repo := fakeRepo{route: &domain.Route{ID: 1}, candidates: []domain.RoutingCandidate{
		stickyCandidate(1, 10, 1), stickyCandidate(2, 10, 100),
	}}
	clock := fakeClock{now: now}
	store := NewStickyStore(5*time.Minute, clock)
	selector := NewWithDependencies(repo, clock, &fakeRandom{values: []int{1}})
	selector.SetSticky(store)
	store.Bind("sess-1", 1, now.Add(-10*time.Minute)) // bound before the store existed

	decision, err := selector.SelectSticky(context.Background(), "model", nil, "sess-1")
	if err != nil || decision.Selected.Channel.ID != 2 {
		t.Fatalf("expired binding must not stick, got %+v err=%v", decision.Selected, err)
	}
	if decision.StickyHit || decision.StickyReason != "" {
		t.Fatalf("expired binding must leave no trace, got %+v", decision.Explanation)
	}
}

func TestSelectStickyWithoutStoreIsNormal(t *testing.T) {
	now := time.Date(2026, 7, 14, 0, 0, 0, 0, time.UTC)
	repo := fakeRepo{route: &domain.Route{ID: 1}, candidates: []domain.RoutingCandidate{
		stickyCandidate(1, 10, 1), stickyCandidate(2, 10, 100),
	}}
	selector := NewWithDependencies(repo, fakeClock{now: now}, &fakeRandom{values: []int{1}})
	// No SetSticky call: sticky must be a no-op.
	decision, err := selector.SelectSticky(context.Background(), "model", nil, "sess-1")
	if err != nil || decision.Selected.Channel.ID != 2 {
		t.Fatalf("without store the pick must be normal, got %+v err=%v", decision.Selected, err)
	}
	if decision.StickyHit || decision.SessionKey != "sess-1" {
		t.Fatalf("unexpected sticky fields: %+v", decision.Explanation)
	}
}

func TestExplainWithSessionAnnotates(t *testing.T) {
	now := time.Date(2026, 7, 14, 0, 0, 0, 0, time.UTC)
	repo := fakeRepo{route: &domain.Route{ID: 1}, candidates: []domain.RoutingCandidate{
		stickyCandidate(1, 10, 100), stickyCandidate(2, 10, 100),
	}}
	clock := fakeClock{now: now}
	store := NewStickyStore(time.Minute, clock)
	selector := NewWithDependencies(repo, clock, &fakeRandom{values: []int{0}})
	selector.SetSticky(store)
	store.Bind("sess-1", 1, now)

	explanation, err := selector.ExplainWithSession(context.Background(), "model", "sess-1")
	if err != nil {
		t.Fatal(err)
	}
	if explanation.SessionKey != "sess-1" || explanation.StickyChannelID == nil || *explanation.StickyChannelID != 1 || !explanation.StickyHit {
		t.Fatalf("expected sticky annotations, got %+v", explanation)
	}
	// Explain must not mutate counters (no selection happened).
	if stats := store.Stats(); stats.Hits != 0 || stats.Escapes != 0 {
		t.Fatalf("explain must not count, got %+v", stats)
	}
}
