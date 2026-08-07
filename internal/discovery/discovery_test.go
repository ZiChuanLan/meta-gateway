package discovery_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lan/meta-gateway/internal/adapters"
	"github.com/lan/meta-gateway/internal/crypto"
	"github.com/lan/meta-gateway/internal/discovery"
	"github.com/lan/meta-gateway/internal/domain"
	"github.com/lan/meta-gateway/internal/store"
)

func TestRefreshSuccessAndFailurePreservesState(t *testing.T) {
	body := `{"data":[{"id":"model-b"},{"id":"model-a"}]}`
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer service-secret" {
			t.Fatal("missing bearer credential")
		}
		_, _ = io.WriteString(w, body)
	}))
	defer upstream.Close()
	db, service, channelID := setupService(t, upstream.URL, "new-api")
	result, err := service.Refresh(t.Context(), channelID)
	if err != nil {
		t.Fatal(err)
	}
	if result.Adapter != "new-api" || strings.Join(result.Models, ",") != "model-a,model-b" || result.CreatedRoutes != 2 {
		t.Fatalf("result=%+v", result)
	}
	body = `{"data":[{"id":123}]}`
	if _, err := service.Refresh(t.Context(), channelID); err == nil {
		t.Fatal("expected malformed payload failure")
	}
	models, _ := db.DiscoveredModel.List(&channelID)
	channel, _ := db.Channel.GetByID(channelID)
	if len(models) != 2 || channel.ModelsCSV != "model-a,model-b" {
		t.Fatalf("failed refresh changed state: models=%+v channel=%+v", models, channel)
	}
}

func TestProbeChecksConnectivityWithoutChangingDiscoveryState(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"data":[{"id":"model-a"}]}`)
	}))
	defer upstream.Close()
	db, service, channelID := setupService(t, upstream.URL, "openai-compatible")

	result, err := service.Probe(t.Context(), channelID)
	if err != nil {
		t.Fatal(err)
	}
	if result.ChannelID != channelID || result.Adapter != "openai-compatible" || strings.Join(result.Models, ",") != "model-a" {
		t.Fatalf("probe=%+v", result)
	}
	models, err := db.DiscoveredModel.List(&channelID)
	if err != nil {
		t.Fatal(err)
	}
	channel, err := db.Channel.GetByID(channelID)
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 0 || channel.ModelsCSV != "" {
		t.Fatalf("probe mutated discovery: models=%+v channel=%+v", models, channel)
	}
}

func TestProbeRetriesTransientTransportFailure(t *testing.T) {
	var calls int
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			// Flaky public site: drop the first connection mid-flight.
			hijacker, ok := w.(http.Hijacker)
			if !ok {
				http.Error(w, "no hijack", http.StatusInternalServerError)
				return
			}
			conn, _, _ := hijacker.Hijack()
			_ = conn.Close()
			return
		}
		_, _ = io.WriteString(w, `{"data":[{"id":"model-a"}]}`)
	}))
	defer upstream.Close()

	db, service, channelID := setupService(t, upstream.URL, "openai-compatible")
	result, err := service.Probe(t.Context(), channelID)
	if err != nil {
		t.Fatalf("probe should succeed after one retry: %v", err)
	}
	if calls != 2 {
		t.Fatalf("expected 2 attempts (1 dropped + 1 retry), got %d", calls)
	}
	if strings.Join(result.Models, ",") != "model-a" {
		t.Fatalf("models=%v", result.Models)
	}
	overviews, overviewErr := db.Channel.ListOverviews(time.Now())
	if overviewErr != nil {
		t.Fatal(overviewErr)
	}
	if len(overviews) != 1 || !overviews[0].LastProbeOK {
		t.Fatalf("last_probe_ok should be true after retry: %+v", overviews)
	}
}

func TestRefreshAllContinuesAndOrdersByChannelID(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"data":[]}`)
	}))
	defer upstream.Close()
	db, service, firstID := setupService(t, upstream.URL, "openai-compatible")
	badID, err := db.Channel.Create(&domain.Channel{Name: "bad", BaseURL: upstream.URL, Status: domain.StatusEnabled, TypeHint: "unsupported"})
	if err != nil {
		t.Fatal(err)
	}
	summary, err := service.RefreshAll(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(summary.Items) != 2 || summary.Items[0].ChannelID != firstID || summary.Items[1].ChannelID != badID || summary.SuccessCount != 1 || summary.FailureCount != 1 {
		t.Fatalf("summary=%+v", summary)
	}
}

func setupService(t *testing.T, upstreamURL, platform string) (*store.DB, *discovery.Service, int64) {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	enc, _ := crypto.New("discovery-test-key")
	secret, _ := enc.Encrypt([]byte("service-secret"))
	siteID, _ := db.Site.Create(&domain.Site{Name: "site", BaseURL: upstreamURL, Platform: platform, Status: domain.StatusEnabled})
	credentialID, _ := db.Credential.Create(&domain.Credential{SiteID: siteID, Kind: "api_key", SecretEnc: []byte(secret), Status: domain.StatusEnabled})
	channelID, err := db.Channel.Create(&domain.Channel{SiteID: &siteID, CredentialID: &credentialID, Name: "channel", BaseURL: upstreamURL, Priority: 3, Weight: 20, Status: domain.StatusEnabled})
	if err != nil {
		t.Fatal(err)
	}
	return db, discovery.New(db, enc, adapters.NewRegistry(nil)), channelID
}

func TestProbeRecoversAutoDisabledChannel(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"data":[{"id":"model-a"}]}`)
	}))
	defer upstream.Close()
	db, service, channelID := setupService(t, upstream.URL, "openai-compatible")

	// Park the channel via the auto-disable circuit.
	if err := db.Channel.AutoDisable(channelID); err != nil {
		t.Fatal(err)
	}
	channel, err := db.Channel.GetByID(channelID)
	if err != nil || channel.Status != domain.StatusAutoDisabled {
		t.Fatalf("channel not auto-disabled: %+v err=%v", channel, err)
	}

	// A probe of an auto-disabled channel must succeed and restore it.
	result, err := service.Probe(t.Context(), channelID)
	if err != nil {
		t.Fatalf("recovery probe failed: %v", err)
	}
	if result.ChannelID != channelID {
		t.Fatalf("probe=%+v", result)
	}
	channel, err = db.Channel.GetByID(channelID)
	if err != nil || channel.Status != domain.StatusEnabled {
		t.Fatalf("channel not restored after healthy probe: %+v err=%v", channel, err)
	}
}

func TestRefreshAllProbesAutoDisabledChannels(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"data":[]}`)
	}))
	defer upstream.Close()
	db, service, channelID := setupService(t, upstream.URL, "openai-compatible")

	// A second, always-failing auto-disabled channel must still be included in
	// the probeable set (recovery candidate) without blocking others.
	failingID, err := db.Channel.Create(&domain.Channel{
		Name: "failing", BaseURL: "http://127.0.0.1:1", Status: domain.StatusAutoDisabled,
		TypeHint: "openai-compatible",
	})
	if err != nil {
		t.Fatal(err)
	}
	// A healthy auto-disabled channel with a real site/credential recovers.
	reference, _ := db.Channel.GetByID(channelID)
	recoveredID, err := db.Channel.Create(&domain.Channel{
		Name: "recovering", BaseURL: upstream.URL, Status: domain.StatusAutoDisabled,
		TypeHint: "openai-compatible", SiteID: reference.SiteID, CredentialID: reference.CredentialID,
	})
	if err != nil {
		t.Fatal(err)
	}

	summary, err := service.RefreshAll(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	ids := map[int64]bool{}
	for _, item := range summary.Items {
		ids[item.ChannelID] = true
	}
	for _, want := range []int64{channelID, failingID, recoveredID} {
		if !ids[want] {
			t.Fatalf("probeable set missing channel %d: %+v", want, summary.Items)
		}
	}
	// The healthy auto-disabled channel recovered; the failing one stayed parked.
	recovered, _ := db.Channel.GetByID(recoveredID)
	failing, _ := db.Channel.GetByID(failingID)
	if recovered.Status != domain.StatusEnabled {
		t.Fatalf("healthy auto-disabled channel not recovered: %+v", recovered)
	}
	if failing.Status != domain.StatusAutoDisabled {
		t.Fatalf("failing channel must stay parked: %+v", failing)
	}
}
