package proxy

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/lan/meta-gateway/internal/crypto"
	"github.com/lan/meta-gateway/internal/domain"
	"github.com/lan/meta-gateway/internal/relay"
	"github.com/lan/meta-gateway/internal/routing"
	"github.com/lan/meta-gateway/internal/store"
)

// "Which of my four upstream keys served this call" is a question the log has
// to answer by name, not by hash. The attempt row stores the credential id of
// the key that actually went out (never the secret), and the list resolves the
// key's display name from its meta_json, so a rename shows through.
func TestAttemptLogNamesTheServingUpstreamKey(t *testing.T) {
	upstream := &queuedRelay{results: []*relay.Result{
		response(http.StatusOK, `{"ok":true}`),
		response(http.StatusOK, `{"ok":true}`),
	}}
	db, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	enc, err := crypto.New("attempt-key-log-master-key")
	if err != nil {
		t.Fatal(err)
	}
	siteID, _ := db.Site.Create(&domain.Site{Name: "site", Status: domain.StatusEnabled})

	primaryEnc, err := enc.Encrypt([]byte("sk-primary-secret"))
	if err != nil {
		t.Fatal(err)
	}
	backupEnc, err := enc.Encrypt([]byte("sk-backup-secret"))
	if err != nil {
		t.Fatal(err)
	}
	primaryID, err := db.Credential.Create(&domain.Credential{
		SiteID: siteID, Kind: "api_key", SecretEnc: []byte(primaryEnc),
		MetaJSON: `{"name":"primary"}`, Status: domain.StatusEnabled,
	})
	if err != nil {
		t.Fatal(err)
	}
	backupID, err := db.Credential.Create(&domain.Credential{
		SiteID: siteID, Kind: "api_key", SecretEnc: []byte(backupEnc),
		MetaJSON: `{"name":"backup"}`, Status: domain.StatusEnabled,
	})
	if err != nil {
		t.Fatal(err)
	}
	channelID, err := db.Channel.Create(&domain.Channel{
		SiteID: &siteID, CredentialID: &primaryID, Name: "ch",
		BaseURL: "https://up.example", Status: domain.StatusEnabled,
	})
	if err != nil {
		t.Fatal(err)
	}
	routeID, err := db.Route.Create(&domain.Route{ModelPattern: "model", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.RouteMember.Create(&domain.RouteMember{
		RouteID: routeID, ChannelID: channelID, Priority: 10, Weight: 100, Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}

	now := time.Date(2026, 7, 14, 0, 0, 0, 0, time.UTC)
	selector := routing.NewWithDependencies(db.RouteMember, fixedClock{now: now}, firstRandom{})
	service := New(selector, upstream, db, enc, 2, time.Minute)
	service.now = func() time.Time { return now }
	// Rotation on: the pool holds BOTH keys, so the row has to name the one that
	// actually went out rather than the one the channel is bound to.
	service.SetKeyPoolRotation(true)

	// Two requests: rotation advances the pool cursor, so the second call goes
	// out on the OTHER key. Both rows must name the key that served them — that
	// is what tells an id that rides with the key apart from "the first pool
	// entry" logged twice.
	send := func(requestID string) {
		t.Helper()
		result := service.ChatCompletions(context.Background(), Request{
			RequestID: requestID,
			Model:     "model",
			Body:      []byte(`{"model":"model","messages":[{"role":"user","content":"hi"}]}`),
		})
		if result.Err != nil || result.StatusCode != http.StatusOK {
			t.Fatalf("unexpected result: %+v", result)
		}
		defer result.Body.Close()
	}
	send("req-key-name-1")
	send("req-key-name-2")

	if len(upstream.auths) != 2 {
		t.Fatalf("upstream calls = %d, want 2", len(upstream.auths))
	}
	nameBySecret := map[string]string{"sk-primary-secret": "primary", "sk-backup-secret": "backup"}
	idBySecret := map[string]int64{"sk-primary-secret": primaryID, "sk-backup-secret": backupID}
	sentIDs := map[string]int64{}
	for index, header := range upstream.auths {
		secret := strings.TrimSpace(strings.TrimPrefix(header, "Bearer "))
		if _, ok := nameBySecret[secret]; !ok {
			t.Fatalf("call %d sent %q, want one of the pool secrets", index, header)
		}
		sentIDs[fmt.Sprintf("req-key-name-%d", index+1)] = idBySecret[secret]
	}
	if sentIDs["req-key-name-1"] == sentIDs["req-key-name-2"] {
		t.Fatalf("rotation did not switch keys between the two calls: %#v", upstream.auths)
	}

	logs, err := db.ProxyLog.ListFilter(store.ProxyLogFilter{Model: "model", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(logs) != 2 {
		t.Fatalf("log rows = %d, want 2", len(logs))
	}
	for _, row := range logs {
		wantID, ok := sentIDs[row.RequestID]
		if !ok {
			t.Fatalf("unexpected request_id %q in the log", row.RequestID)
		}
		if row.UpstreamKeyID != wantID {
			t.Fatalf("row %s logged upstream_key_id = %d, want %d (the key the upstream received)", row.RequestID, row.UpstreamKeyID, wantID)
		}
		wantName := "primary"
		if wantID == backupID {
			wantName = "backup"
		}
		if row.UpstreamKeyName != wantName {
			t.Fatalf("row %s resolved upstream_key_name = %q, want %q", row.RequestID, row.UpstreamKeyName, wantName)
		}
		if row.KeyFingerprint == "" {
			t.Fatalf("row %s has no key fingerprint", row.RequestID)
		}
		// The secret itself must not be in the row, in any field.
		encoded, err := json.Marshal(row)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(encoded), "sk-primary-secret") || strings.Contains(string(encoded), "sk-backup-secret") {
			t.Fatalf("log row leaked an upstream secret: %s", encoded)
		}
	}
}

// A key deleted after the request keeps its id in the row; the name simply
// resolves to empty and the console falls back to the fingerprint.
func TestAttemptLogKeyNameVanishesWithTheCredential(t *testing.T) {
	db, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	siteID, _ := db.Site.Create(&domain.Site{Name: "site", Status: domain.StatusEnabled})
	credentialID, err := db.Credential.Create(&domain.Credential{
		SiteID: siteID, Kind: "api_key", SecretEnc: []byte("enc"),
		MetaJSON: `{"name":"rotated-out"}`, Status: domain.StatusEnabled,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ProxyLog.Insert(&domain.ProxyLog{
		RequestID: "req-gone", Model: "m", Status: 200, Attempt: 1,
		KeyFingerprint: "abc123def456", UpstreamKeyID: credentialID,
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.Credential.Delete(credentialID); err != nil {
		t.Fatal(err)
	}
	logs, err := db.ProxyLog.ListFilter(store.ProxyLogFilter{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(logs) != 1 {
		t.Fatalf("log rows = %d, want 1", len(logs))
	}
	if logs[0].UpstreamKeyID != credentialID {
		t.Fatalf("upstream_key_id = %d, want %d (the id outlives the key)", logs[0].UpstreamKeyID, credentialID)
	}
	if logs[0].UpstreamKeyName != "" {
		t.Fatalf("upstream_key_name = %q, want empty for a deleted key", logs[0].UpstreamKeyName)
	}
	if logs[0].KeyFingerprint != "abc123def456" {
		t.Fatalf("fingerprint = %q, want the stored one", logs[0].KeyFingerprint)
	}
}
