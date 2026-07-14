package checkin_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/lan/meta-gateway/internal/adapters"
	"github.com/lan/meta-gateway/internal/checkin"
	"github.com/lan/meta-gateway/internal/crypto"
	"github.com/lan/meta-gateway/internal/domain"
	"github.com/lan/meta-gateway/internal/store"
)

func TestRunCredentialSuccessAndEligibilitySkips(t *testing.T) {
	var requests atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Header.Get("Authorization") != "Bearer session-secret" || r.Header.Get("New-Api-User") != "42" {
			t.Errorf("headers auth=%q user=%q", r.Header.Get("Authorization"), r.Header.Get("New-Api-User"))
		}
		_, _ = io.WriteString(w, `{"success":true,"data":{"reward":"2.5"}}`)
	}))
	defer upstream.Close()
	db, service, siteID := setupService(t, upstream.URL, "new-api")

	sessionID := createCredential(t, db, siteID, "session", domain.StatusEnabled, true, `{"platform_user_id":42}`)
	result, err := service.RunCredential(t.Context(), sessionID, checkin.SourceManual, false)
	if err != nil || result.Status != checkin.StatusSuccess || result.Reward != "2.5" {
		t.Fatalf("result=%+v err=%v", result, err)
	}

	apiKeyID := createCredential(t, db, siteID, "api_key", domain.StatusEnabled, true, "")
	result, err = service.RunCredential(t.Context(), apiKeyID, checkin.SourceManual, false)
	if err != nil || result.Status != checkin.StatusSkipped || result.Category != "unsupported_credential_kind" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if requests.Load() != 1 {
		t.Fatalf("unexpected upstream requests: %d", requests.Load())
	}
	logs, err := db.CheckinLog.List(store.CheckinLogFilter{Limit: 10})
	if err != nil || len(logs) != 2 || strings.Contains(logs[0].Message+logs[1].Message, "session-secret") {
		t.Fatalf("logs=%+v err=%v", logs, err)
	}
}

func TestRunCredentialFailuresAreRedactedAndLogged(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = io.WriteString(w, "session-secret private response")
	}))
	defer upstream.Close()
	db, service, siteID := setupService(t, upstream.URL, "new-api")
	credentialID := createCredential(t, db, siteID, "session", domain.StatusEnabled, true, "")
	result, err := service.RunCredential(t.Context(), credentialID, checkin.SourceManual, false)
	if err != nil || result.Status != checkin.StatusFailed || result.Category != string(adapters.ErrorStatus) {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	logs, _ := db.CheckinLog.List(store.CheckinLogFilter{CredentialID: &credentialID})
	if len(logs) != 1 || strings.Contains(logs[0].Message, "secret") || strings.Contains(logs[0].Message, "private") {
		t.Fatalf("logs=%+v", logs)
	}

	badMetaID := createCredential(t, db, siteID, "session", domain.StatusEnabled, true, `{"platform_user_id":0}`)
	result, err = service.RunCredential(t.Context(), badMetaID, checkin.SourceManual, false)
	if err != nil || result.Category != "invalid_metadata" || result.Status != checkin.StatusFailed {
		t.Fatalf("metadata result=%+v err=%v", result, err)
	}
}

func TestRunCredentialPersistsIneligibleAndUnavailableResults(t *testing.T) {
	tests := []struct {
		name             string
		platform         string
		siteStatus       string
		credentialStatus string
		scheduleEnabled  bool
		requireSchedule  bool
		badCipher        bool
		wantStatus       string
		wantCategory     string
	}{
		{"schedule disabled", "new-api", domain.StatusEnabled, domain.StatusEnabled, false, true, false, checkin.StatusSkipped, "checkin_disabled"},
		{"site disabled", "new-api", domain.StatusDisabled, domain.StatusEnabled, true, false, false, checkin.StatusSkipped, "site_disabled"},
		{"credential disabled", "new-api", domain.StatusEnabled, domain.StatusDisabled, true, false, false, checkin.StatusSkipped, "credential_disabled"},
		{"unsupported platform", "openai-compatible", domain.StatusEnabled, domain.StatusEnabled, true, false, false, checkin.StatusSkipped, "unsupported"},
		{"bad ciphertext", "new-api", domain.StatusEnabled, domain.StatusEnabled, true, false, true, checkin.StatusFailed, "credential_unavailable"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var requests atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				requests.Add(1)
				_, _ = io.WriteString(w, `{"success":true}`)
			}))
			defer upstream.Close()
			db, service, siteID := setupService(t, upstream.URL, tc.platform)
			site, _ := db.Site.GetByID(siteID)
			site.Status = tc.siteStatus
			if err := db.Site.Update(site); err != nil {
				t.Fatal(err)
			}
			credentialID := createCredential(t, db, siteID, "session", tc.credentialStatus, tc.scheduleEnabled, "")
			if tc.badCipher {
				credential, _ := db.Credential.GetByID(credentialID)
				credential.SecretEnc = []byte("invalid")
				if err := db.Credential.Update(credential); err != nil {
					t.Fatal(err)
				}
			}
			result, err := service.RunCredential(t.Context(), credentialID, checkin.SourceManual, tc.requireSchedule)
			if err != nil || result.Status != tc.wantStatus || result.Category != tc.wantCategory {
				t.Fatalf("result=%+v err=%v", result, err)
			}
			if requests.Load() != 0 {
				t.Fatalf("ineligible target made %d upstream requests", requests.Load())
			}
			logs, err := db.CheckinLog.List(store.CheckinLogFilter{CredentialID: &credentialID})
			if err != nil || len(logs) != 1 || logs[0].Category != tc.wantCategory {
				t.Fatalf("logs=%+v err=%v", logs, err)
			}
		})
	}
}

func TestRunCredentialConcurrentCollision(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(entered)
		<-release
		_, _ = io.WriteString(w, `{"success":true}`)
	}))
	defer upstream.Close()
	db, service, siteID := setupService(t, upstream.URL, "one-api")
	credentialID := createCredential(t, db, siteID, "access_token", domain.StatusEnabled, true, "")

	firstDone := make(chan error, 1)
	go func() {
		_, err := service.RunCredential(t.Context(), credentialID, checkin.SourceScheduled, true)
		firstDone <- err
	}()
	<-entered
	second, err := service.RunCredential(t.Context(), credentialID, checkin.SourceManual, false)
	if err != nil || second.Status != checkin.StatusSkipped || second.Category != "already_running" {
		t.Fatalf("second=%+v err=%v", second, err)
	}
	close(release)
	if err := <-firstDone; err != nil {
		t.Fatal(err)
	}
	logs, _ := db.CheckinLog.List(store.CheckinLogFilter{CredentialID: &credentialID})
	if len(logs) != 2 {
		t.Fatalf("logs=%+v", logs)
	}
}

func TestRunAllOrdersAndContinuesAfterUpstreamFailure(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "Bearer fail-secret" {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		_, _ = io.WriteString(w, `{"success":true}`)
	}))
	defer upstream.Close()
	db, service, siteID := setupService(t, upstream.URL, "new-api")
	firstID := createCredentialWithSecret(t, db, siteID, "session", true, "fail-secret")
	secondID := createCredentialWithSecret(t, db, siteID, "session", true, "session-secret")
	_ = createCredentialWithSecret(t, db, siteID, "api_key", true, "ignored-secret")

	summary, err := service.RunAll(t.Context(), checkin.SourceScheduled)
	if err != nil {
		t.Fatal(err)
	}
	if len(summary.Items) != 3 || summary.Items[0].CredentialID != firstID || summary.Items[1].CredentialID != secondID || summary.SuccessCount != 1 || summary.FailureCount != 1 || summary.SkippedCount != 1 {
		t.Fatalf("summary=%+v", summary)
	}
}

func setupService(t *testing.T, upstreamURL, platform string) (*store.DB, *checkin.Service, int64) {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	enc, err := crypto.New("checkin-test-key")
	if err != nil {
		t.Fatal(err)
	}
	siteID, err := db.Site.Create(&domain.Site{Name: "site", BaseURL: upstreamURL, Platform: platform, Status: domain.StatusEnabled})
	if err != nil {
		t.Fatal(err)
	}
	return db, checkin.New(db, enc, adapters.NewRegistry(nil)), siteID
}

func createCredential(t *testing.T, db *store.DB, siteID int64, kind, status string, enabled bool, metadata string) int64 {
	t.Helper()
	enc, _ := crypto.New("checkin-test-key")
	secret, err := enc.Encrypt([]byte("session-secret"))
	if err != nil {
		t.Fatal(err)
	}
	id, err := db.Credential.Create(&domain.Credential{SiteID: siteID, Kind: kind, SecretEnc: []byte(secret), MetaJSON: metadata, Status: status, CheckinEnabled: enabled})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func createCredentialWithSecret(t *testing.T, db *store.DB, siteID int64, kind string, enabled bool, plaintext string) int64 {
	t.Helper()
	enc, _ := crypto.New("checkin-test-key")
	secret, err := enc.Encrypt([]byte(plaintext))
	if err != nil {
		t.Fatal(err)
	}
	id, err := db.Credential.Create(&domain.Credential{SiteID: siteID, Kind: kind, SecretEnc: []byte(secret), Status: domain.StatusEnabled, CheckinEnabled: enabled})
	if err != nil {
		t.Fatal(err)
	}
	return id
}
