package discovery_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

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
