package account

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lan/meta-gateway/internal/adapters"
	"github.com/lan/meta-gateway/internal/crypto"
	"github.com/lan/meta-gateway/internal/domain"
	"github.com/lan/meta-gateway/internal/store"
	"github.com/lan/meta-gateway/internal/webhook"
)

// newFinanceService builds a service backed by a real test store, a site whose
// base URL is the mock upstream, and one access-token credential.
func newFinanceService(t *testing.T, baseURL string) (*Service, *store.DB, int64, int64) {
	t.Helper()
	db, err := store.OpenTest(filepath.Join(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	enc, err := crypto.New("finance-snapshot-test-key")
	if err != nil {
		t.Fatal(err)
	}
	svc := New(db, enc, adapters.NewRegistry(&http.Client{}))
	t.Cleanup(svc.Stop)
	siteID, err := db.Site.Create(&domain.Site{
		Name: "fin", BaseURL: baseURL, Platform: "new-api", Status: domain.StatusEnabled,
	})
	if err != nil {
		t.Fatal(err)
	}
	secretEnc, _ := enc.Encrypt([]byte("user-token"))
	credID, err := db.Credential.Create(&domain.Credential{
		SiteID: siteID, Kind: "access_token", SecretEnc: []byte(secretEnc), Status: domain.StatusEnabled,
	})
	if err != nil {
		t.Fatal(err)
	}
	return svc, db, siteID, credID
}

func addFinanceChannel(t *testing.T, db *store.DB, siteID, credID int64, baseURL, group, name string) int64 {
	t.Helper()
	id, err := db.Channel.Create(&domain.Channel{
		SiteID: &siteID, CredentialID: &credID, Name: name, BaseURL: baseURL,
		GroupName: group, Priority: 0, Weight: 100, Status: domain.StatusEnabled, TypeHint: "new-api",
	})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func financeItemByChannel(items []FinanceItem, channelID int64) *FinanceItem {
	for index := range items {
		if items[index].ChannelID == channelID {
			return &items[index]
		}
	}
	return nil
}

// TestFinanceSnapshotDedupesSameAccountGroup verifies that two channels on the
// same credential, site, base URL and upstream group are probed once but both
// reported, sharing the account id.
func TestFinanceSnapshotDedupesSameAccountGroup(t *testing.T) {
	var selfHits, pricingHits int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/user/self":
			atomic.AddInt32(&selfHits, 1)
			_, _ = w.Write([]byte(`{"success":true,"data":{"id":9,"username":"bob","quota":900000,"used_quota":1}}`))
		case "/api/pricing":
			atomic.AddInt32(&pricingHits, 1)
			_, _ = w.Write([]byte(`{"success":true,"data":[{"model_name":"ratio-model","model_ratio":0.5,"completion_ratio":2,"quota_type":0}]}`))
		case "/api/status":
			_, _ = w.Write([]byte(`{"success":true,"data":{"quota_per_unit":500000}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	svc, db, siteID, credID := newFinanceService(t, server.URL)
	first := addFinanceChannel(t, db, siteID, credID, server.URL, "default", "a")
	second := addFinanceChannel(t, db, siteID, credID, server.URL, "default", "b")

	items, err := svc.FinanceOverview(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 {
		t.Fatalf("want 2 channel items, got %+v", items)
	}
	for _, id := range []int64{first, second} {
		item := financeItemByChannel(items, id)
		if item == nil {
			t.Fatalf("missing item for channel %d", id)
		}
		if item.AccountID != credID {
			t.Fatalf("channel %d account id=%d want %d", id, item.AccountID, credID)
		}
		if !item.BalanceKnown || item.Balance != 900000 {
			t.Fatalf("channel %d balance=%+v", id, item)
		}
	}
	if got := atomic.LoadInt32(&selfHits); got != 1 {
		t.Fatalf("self endpoint hits=%d want 1", got)
	}
	if got := atomic.LoadInt32(&pricingHits); got != 1 {
		t.Fatalf("pricing endpoint hits=%d want 1", got)
	}
}

// TestFinanceSnapshotSeparateGroupsUseOwnRatio verifies that two groups on the
// same account are probed separately and never share a price.
func TestFinanceSnapshotSeparateGroupsUseOwnRatio(t *testing.T) {
	var pricingHits int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/user/self":
			_, _ = w.Write([]byte(`{"success":true,"data":{"id":9,"username":"bob","quota":900000}}`))
		case "/api/pricing":
			atomic.AddInt32(&pricingHits, 1)
			_, _ = w.Write([]byte(`{"success":true,"group_ratio":{"default":1,"vip":3},"data":[{"model_name":"ratio-model","model_ratio":0.5,"quota_type":0}]}`))
		case "/api/status":
			_, _ = w.Write([]byte(`{"success":true,"data":{"quota_per_unit":500000}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	svc, db, siteID, credID := newFinanceService(t, server.URL)
	def := addFinanceChannel(t, db, siteID, credID, server.URL, "default", "a")
	vip := addFinanceChannel(t, db, siteID, credID, server.URL, "vip", "b")

	items, err := svc.FinanceOverview(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defItem := financeItemByChannel(items, def)
	vipItem := financeItemByChannel(items, vip)
	if defItem == nil || vipItem == nil {
		t.Fatalf("missing items: %+v", items)
	}
	defQuote := defItem.Quotes["ratio-model"]
	vipQuote := vipItem.Quotes["ratio-model"]
	if defQuote.Values.Input == nil || vipQuote.Values.Input == nil {
		t.Fatalf("ratio quotes missing: def=%+v vip=%+v", defQuote, vipQuote)
	}
	if *defQuote.Values.Input != 1 {
		t.Fatalf("default input=%v want 1", *defQuote.Values.Input)
	}
	if *vipQuote.Values.Input != 3 {
		t.Fatalf("vip input=%v want 3", *vipQuote.Values.Input)
	}
	if got := atomic.LoadInt32(&pricingHits); got != 2 {
		t.Fatalf("pricing hits=%d want 2 (one per group)", got)
	}
}

// TestFinanceSnapshotMissingQuotaIsUnknownAndSilent verifies a missing quota is
// reported as unknown and never fires a low-balance alert.
func TestFinanceSnapshotMissingQuotaIsUnknownAndSilent(t *testing.T) {
	var alerts int32
	alertServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&alerts, 1)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(alertServer.Close)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/user/self":
			// No quota field at all: the site simply does not report one.
			_, _ = w.Write([]byte(`{"success":true,"data":{"id":9,"username":"bob"}}`))
		case "/api/pricing":
			_, _ = w.Write([]byte(`{"success":true,"data":[]}`))
		case "/api/status":
			_, _ = w.Write([]byte(`{"success":true,"data":{"quota_per_unit":500000}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	svc, db, siteID, credID := newFinanceService(t, server.URL)
	svc.SetNotifier(webhook.New(alertServer.URL, 0))
	addFinanceChannel(t, db, siteID, credID, server.URL, "default", "a")

	items, err := svc.FinanceOverview(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("items=%+v", items)
	}
	item := items[0]
	if item.BalanceKnown {
		t.Fatalf("missing quota must not be marked known: %+v", item)
	}
	if item.BalanceStatus != balanceStatusUnknown || item.Balance != 0 {
		t.Fatalf("want unknown zero balance, got %+v", item)
	}
	if got := atomic.LoadInt32(&alerts); got != 0 {
		t.Fatalf("a missing quota fired %d low-balance alert(s)", got)
	}
}

// TestFinanceSnapshotRefreshFailureKeepsLastKnown verifies a failed refresh
// preserves the previous balance and observation time instead of reporting a
// false zero.
func TestFinanceSnapshotRefreshFailureKeepsLastKnown(t *testing.T) {
	var failing atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/user/self":
			if failing.Load() {
				http.Error(w, "boom", http.StatusInternalServerError)
				return
			}
			_, _ = w.Write([]byte(`{"success":true,"data":{"id":9,"username":"bob","quota":1200000}}`))
		case "/api/pricing":
			_, _ = w.Write([]byte(`{"success":true,"data":[]}`))
		case "/api/status":
			_, _ = w.Write([]byte(`{"success":true,"data":{"quota_per_unit":500000}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	svc, db, siteID, credID := newFinanceService(t, server.URL)
	channelID := addFinanceChannel(t, db, siteID, credID, server.URL, "default", "a")

	if _, err := svc.FinanceOverview(context.Background()); err != nil {
		t.Fatal(err)
	}
	first := svc.snapshots[channelID]
	if !first.BalanceKnown || first.Balance != 1200000 {
		t.Fatalf("first reading=%+v", first)
	}

	failing.Store(true)
	svc.refresh(context.Background())

	second := svc.snapshots[channelID]
	if second.Balance != 1200000 || !second.BalanceKnown {
		t.Fatalf("failed refresh dropped the last balance: %+v", second)
	}
	if second.BalanceStatus != balanceStatusRefreshFailed {
		t.Fatalf("want refresh_failed, got %q", second.BalanceStatus)
	}
	if second.ObservedAt != first.ObservedAt {
		t.Fatalf("observed_at changed on failure: %q -> %q", first.ObservedAt, second.ObservedAt)
	}
	if second.RefreshError == "" {
		t.Fatalf("failed refresh must carry a fixed error code: %+v", second)
	}
}

// TestFinanceSnapshotReturnsBeforeSlowUpstream verifies the read path never
// waits on a slow site.
func TestFinanceSnapshotReturnsBeforeSlowUpstream(t *testing.T) {
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)
	t.Cleanup(func() { close(release) })

	svc, db, siteID, credID := newFinanceService(t, server.URL)
	addFinanceChannel(t, db, siteID, credID, server.URL, "default", "a")

	started := time.Now()
	snapshot, err := svc.FinanceSnapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("FinanceSnapshot waited %v on a slow upstream", elapsed)
	}
	if len(snapshot.Items) != 0 {
		t.Fatalf("cold snapshot should be empty, got %+v", snapshot.Items)
	}
	svc.Stop()
}

// TestFinanceSnapshotColdSingleFlight verifies concurrent cold readers trigger
// exactly one background refresh.
func TestFinanceSnapshotColdSingleFlight(t *testing.T) {
	var selfHits int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/user/self":
			atomic.AddInt32(&selfHits, 1)
			time.Sleep(150 * time.Millisecond)
			_, _ = w.Write([]byte(`{"success":true,"data":{"id":9,"username":"bob","quota":900000}}`))
		case "/api/pricing":
			_, _ = w.Write([]byte(`{"success":true,"data":[]}`))
		case "/api/status":
			_, _ = w.Write([]byte(`{"success":true,"data":{"quota_per_unit":500000}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	svc, db, siteID, credID := newFinanceService(t, server.URL)
	addFinanceChannel(t, db, siteID, credID, server.URL, "default", "a")

	for i := 0; i < 5; i++ {
		if _, err := svc.FinanceSnapshot(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	svc.refreshWG.Wait()
	if got := atomic.LoadInt32(&selfHits); got != 1 {
		t.Fatalf("cold reads triggered %d refreshes, want 1", got)
	}
}

// TestAccountServiceStopCancelsRefresh verifies Stop aborts an in-flight
// refresh and waits for it, so shutdown leaves nothing running.
func TestAccountServiceStopCancelsRefresh(t *testing.T) {
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)
	t.Cleanup(func() { close(release) })

	svc, db, siteID, credID := newFinanceService(t, server.URL)
	addFinanceChannel(t, db, siteID, credID, server.URL, "default", "a")

	if _, err := svc.FinanceSnapshot(context.Background()); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	svc.Stop()
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("Stop blocked %v on an in-flight refresh", elapsed)
	}
	// Must not start a new refresh after stopping.
	if _, err := svc.RefreshFinance(context.Background()); err != nil {
		t.Fatal(err)
	}
	if svc.isRefreshing() {
		t.Fatal("refresh started after Stop")
	}
}

// TestFinanceSnapshotLoadsPersistedOnRestart verifies a restarted service
// serves the last persisted observation before any refresh completes.
func TestFinanceSnapshotLoadsPersistedOnRestart(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/user/self":
			_, _ = w.Write([]byte(`{"success":true,"data":{"id":9,"username":"bob","quota":777000}}`))
		case "/api/pricing":
			_, _ = w.Write([]byte(`{"success":true,"data":[]}`))
		case "/api/status":
			_, _ = w.Write([]byte(`{"success":true,"data":{"quota_per_unit":500000}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	firstSvc, db, siteID, credID := newFinanceService(t, server.URL)
	channelID := addFinanceChannel(t, db, siteID, credID, server.URL, "default", "a")
	if _, err := firstSvc.FinanceOverview(context.Background()); err != nil {
		t.Fatal(err)
	}
	firstSvc.Stop()

	rows, err := db.LatestFinanceSnapshots()
	if err != nil || len(rows) != 1 {
		t.Fatalf("persisted rows=%d err=%v", len(rows), err)
	}

	enc, err := crypto.New("finance-snapshot-test-key")
	if err != nil {
		t.Fatal(err)
	}
	restarted := New(db, enc, adapters.NewRegistry(&http.Client{}))
	t.Cleanup(restarted.Stop)
	snapshot, err := restarted.FinanceSnapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	item := financeItemByChannel(snapshot.Items, channelID)
	if item == nil || item.Balance != 777000 || !item.BalanceKnown {
		t.Fatalf("restart did not serve the persisted snapshot: %+v", snapshot.Items)
	}
}

// TestRecordBalanceHistoryDedupesSharedAccount verifies one history row per
// account and that unknown readings are skipped.
func TestRecordBalanceHistoryDedupesSharedAccount(t *testing.T) {
	var quota *int64
	value := int64(800000)
	quota = &value
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/user/self":
			if quota == nil {
				_, _ = w.Write([]byte(`{"success":true,"data":{"id":9,"username":"bob"}}`))
				return
			}
			_, _ = fmt.Fprintf(w, `{"success":true,"data":{"id":9,"username":"bob","quota":%d}}`, *quota)
		case "/api/pricing":
			_, _ = w.Write([]byte(`{"success":true,"data":[]}`))
		case "/api/status":
			_, _ = w.Write([]byte(`{"success":true,"data":{"quota_per_unit":500000}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	svc, db, siteID, credID := newFinanceService(t, server.URL)
	addFinanceChannel(t, db, siteID, credID, server.URL, "default", "a")
	addFinanceChannel(t, db, siteID, credID, server.URL, "vip", "b")

	written, err := svc.RecordBalanceHistory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if written != 1 {
		t.Fatalf("shared account wrote %d history rows, want 1", written)
	}
	points, err := db.ListBalanceHistory(30)
	if err != nil || len(points) != 1 {
		t.Fatalf("history points=%d err=%v", len(points), err)
	}

	// An unknown reading is skipped, never written as a false zero.
	quota = nil
	svc.refresh(context.Background())
	written, err = svc.RecordBalanceHistory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if written != 0 {
		t.Fatalf("unknown readings wrote %d history rows, want 0", written)
	}
}

func TestFinanceSnapshotPartialRefreshRetainsQuoteAndReportsStale(t *testing.T) {
	var failed atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/user/self":
			fmt.Fprint(w, `{"success":true,"data":{"id":1,"username":"audit","quota":1200000}}`)
		case "/api/pricing":
			if failed.Load() {
				http.Error(w, "error", http.StatusServiceUnavailable)
				return
			}
			fmt.Fprint(w, `{"success":true,"data":[{"model_name":"m","quota_type":0,"model_ratio":1,"completion_ratio":2}]}`)
		case "/api/status":
			fmt.Fprint(w, `{"success":true,"data":{"quota_per_unit":500000}}`)
		}
	}))
	defer server.Close()
	svc, db, site, cred := newFinanceService(t, server.URL)
	ch := addFinanceChannel(t, db, site, cred, server.URL, "default", "c")
	if _, err := svc.FinanceOverview(context.Background()); err != nil {
		t.Fatal(err)
	}
	first := svc.snapshots[ch].Quotes["m"]
	failed.Store(true)
	svc.refresh(context.Background())
	item := svc.snapshots[ch]
	if item.RefreshError != "pricing_refresh_failed" || item.Quotes["m"].ID != first.ID || !item.BalanceKnown {
		t.Fatalf("partial=%+v", item)
	}
	item.ObservedAt = time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano)
	svc.snapshots[ch] = item
	items, _ := svc.snapshotItems()
	if items[0].BalanceStatus != balanceStatusStale {
		t.Fatalf("expired item looks current: %+v", items[0])
	}
}
