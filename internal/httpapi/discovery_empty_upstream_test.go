package httpapi_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lan/meta-gateway/internal/config"
	"github.com/lan/meta-gateway/internal/crypto"
	"github.com/lan/meta-gateway/internal/httpapi"
	"github.com/lan/meta-gateway/internal/store"
)

// An upstream that answers 200 with an empty model list is a healthy channel
// with no models, and the console renders the result directly
// (`refresh.data.models.length`). A nil slice marshals to `"models": null`,
// which crashed that read and blanked the whole page — the array must survive
// the empty case.
func TestRefreshAndProbeKeepModelsArrayWhenUpstreamListsNothing(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"data":[]}`)
	}))
	defer upstream.Close()

	dataDir := t.TempDir()
	db, _ := store.Open(dataDir)
	defer db.Close()
	enc, _ := crypto.New("empty-refresh-master-key-32-chars!")
	cfg := &config.Config{
		AdminToken:         "admin-test",
		AdminTokens:        []string{"admin-test"},
		MetricsToken:       "metrics-test",
		BackupDir:          filepath.Join(dataDir, "backups"),
		MaxAdminBodyBytes:  1 << 20,
		AuditRetentionDays: 90,
		AuditRetentionRows: 100000,
		OutboundAllowCIDRs: []string{"127.0.0.1/32"},
	}
	server := httptest.NewServer(httpapi.NewTestRouter(t, cfg, db, enc))
	defer server.Close()

	var site struct{ ID int64 }
	json.Unmarshal(post(t, server.URL+"/admin/sites", map[string]any{
		"name": "empty-upstream", "base_url": upstream.URL, "platform": "openai-compatible", "status": "enabled",
	}), &site)
	var cred struct{ ID int64 }
	json.Unmarshal(post(t, server.URL+"/admin/sites/"+itoa(site.ID)+"/credentials", map[string]any{
		"kind": "api_key", "secret": "sk-empty", "status": "enabled",
	}), &cred)
	var channel struct{ ID int64 }
	json.Unmarshal(post(t, server.URL+"/admin/channels", map[string]any{
		"site_id": site.ID, "credential_id": cred.ID, "name": "empty-upstream",
		"base_url": upstream.URL, "type_hint": "openai-compatible", "status": "enabled",
	}), &channel)

	refresh := string(post(t, server.URL+"/admin/discovery/channels/"+itoa(channel.ID)+"/refresh", map[string]any{}))
	if !strings.Contains(refresh, `"models":[]`) {
		t.Fatalf("refresh must report an empty array, not null:\n%s", refresh)
	}
	probe := string(post(t, server.URL+"/admin/discovery/channels/"+itoa(channel.ID)+"/probe", map[string]any{}))
	if !strings.Contains(probe, `"models":[]`) {
		t.Fatalf("probe must report an empty array, not null:\n%s", probe)
	}
}
