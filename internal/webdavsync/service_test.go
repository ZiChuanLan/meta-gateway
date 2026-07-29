package webdavsync

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lan/meta-gateway/internal/exchange"
)

type fakeImporter struct {
	last     []byte
	lastMode string
	err      error
	res      *exchange.ImportResult
}

func (f *fakeImporter) ImportWithOptions(_ context.Context, data []byte, opts exchange.ImportOptions) (*exchange.ImportResult, error) {
	f.last = append([]byte(nil), data...)
	f.lastMode = opts.Mode
	if f.err != nil {
		return nil, f.err
	}
	if f.res != nil {
		return f.res, nil
	}
	return &exchange.ImportResult{CreatedCount: 1, ChannelIDs: []int64{1}}, nil
}

func TestServiceSyncPlainBackup(t *testing.T) {
	backup := `{"version":"2.0","accounts":[],"apiCredentialProfiles":{"version":3,"profiles":[{"name":"main","apiType":"openai","baseUrl":"https://api.example.com","apiKey":"secret"}]}}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		if !ok || user != "dav" || pass != "secret" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if !strings.HasSuffix(r.URL.Path, "/all-api-hub-backup/all-api-hub-1-0.json") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(backup))
	}))
	defer server.Close()

	importer := &fakeImporter{}
	service := NewService(Config{
		URL:      server.URL + "/webdav/",
		Username: "dav",
		Password: "secret",
		MaxBytes: 1 << 20,
	}, &Client{HTTP: server.Client(), MaxBytes: 1 << 20}, importer)

	result, err := service.Sync(context.Background(), SourceManual, SyncModeIncremental)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != StatusSuccess || result.Import == nil || result.Import.CreatedCount != 1 {
		t.Fatalf("result=%+v", result)
	}
	if string(importer.last) != backup {
		t.Fatalf("importer received unexpected body")
	}
	status := service.Status()
	if !status.Configured || status.Last == nil || status.Last.Status != StatusSuccess {
		t.Fatalf("status=%+v", status)
	}
}

func TestServiceSyncReplacePassesReplaceMode(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[{"name":"main","base_url":"https://api.example.com","key":"secret"}]`))
	}))
	defer server.Close()

	importer := &fakeImporter{}
	service := NewService(Config{
		URL: server.URL + "/file.json", Username: "u", Password: "p",
	}, &Client{HTTP: server.Client(), MaxBytes: 1 << 20}, importer)

	result, err := service.Sync(context.Background(), SourceManual, SyncModeReplace)
	if err != nil {
		t.Fatal(err)
	}
	if importer.lastMode != exchange.ImportModeReplace {
		t.Fatalf("mode=%q", importer.lastMode)
	}
	if !strings.Contains(result.Message, "full replace") {
		t.Fatalf("message=%q", result.Message)
	}
}

func TestServiceScheduledSyncUsesIncrementalMode(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[]`))
	}))
	defer server.Close()
	importer := &fakeImporter{}
	service := NewService(Config{
		URL: server.URL + "/file.json", Username: "u", Password: "p",
	}, &Client{HTTP: server.Client(), MaxBytes: 1024}, importer)
	if _, err := service.RunScheduled(context.Background()); err != nil {
		t.Fatal(err)
	}
	if importer.lastMode != exchange.ImportModeIncremental {
		t.Fatalf("scheduled mode=%q", importer.lastMode)
	}
}

func TestServiceRejectsUnknownSyncMode(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[]`))
	}))
	defer server.Close()
	service := NewService(Config{
		URL: server.URL + "/file.json", Username: "u", Password: "p",
	}, &Client{HTTP: server.Client(), MaxBytes: 1024}, &fakeImporter{})
	result, err := service.Sync(context.Background(), SourceManual, "unknown")
	if err == nil || result.Category != CategoryValidation {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestServiceSyncEncryptedBackup(t *testing.T) {
	plaintext := []byte(`{"version":"2.0","accounts":[],"apiCredentialProfiles":{"version":3,"profiles":[]}}`)
	envelope := mustEncryptEnvelope(t, "enc-pass", plaintext, 1000)
	raw, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(raw)
	}))
	defer server.Close()

	importer := &fakeImporter{}
	service := NewService(Config{
		URL:            server.URL + "/file.json",
		Username:       "u",
		Password:       "p",
		BackupPassword: "enc-pass",
	}, &Client{HTTP: server.Client(), MaxBytes: 1 << 20}, importer)

	result, err := service.Sync(context.Background(), SourceManual, SyncModeIncremental)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Encrypted || result.Status != StatusSuccess {
		t.Fatalf("result=%+v", result)
	}
	if string(importer.last) != string(plaintext) {
		t.Fatalf("decrypted payload not imported")
	}
}

func TestServiceAuthFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()
	service := NewService(Config{
		URL: server.URL + "/f.json", Username: "u", Password: "p",
	}, &Client{HTTP: server.Client(), MaxBytes: 1024}, &fakeImporter{})
	result, err := service.Sync(context.Background(), SourceManual, SyncModeIncremental)
	if err == nil || result.Category != CategoryAuthFailed {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestServiceTestConnectionSkipsImport(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"version":"2.0"}`))
	}))
	defer server.Close()
	importer := &fakeImporter{}
	service := NewService(Config{
		URL: server.URL + "/f.json", Username: "u", Password: "p",
	}, &Client{HTTP: server.Client(), MaxBytes: 1024}, importer)
	result, err := service.TestConnection(context.Background())
	if err != nil || result.Status != StatusSuccess {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if importer.last != nil {
		t.Fatal("test connection must not import")
	}
}

func TestServiceConfigIncomplete(t *testing.T) {
	service := NewService(Config{}, &Client{HTTP: http.DefaultClient}, &fakeImporter{})
	result, err := service.Sync(context.Background(), SourceManual, SyncModeIncremental)
	if err == nil || result.Category != CategoryConfigIncomplete {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestServiceSyncEncryptedUsesLoginPasswordFallback(t *testing.T) {
	// Same password used for WebDAV login and backup unlock (common operator setup).
	password := "shared-secret"
	plaintext := []byte(`{"version":"2.0","accounts":[],"apiCredentialProfiles":{"version":3,"profiles":[]}}`)
	envelope := mustEncryptEnvelope(t, password, plaintext, 1000)
	raw, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(raw)
	}))
	defer server.Close()

	importer := &fakeImporter{}
	service := NewService(Config{
		URL:      server.URL + "/file.json",
		Username: "u",
		Password: password,
		// BackupPassword intentionally empty — should fall back to Password.
	}, &Client{HTTP: server.Client(), MaxBytes: 1 << 20}, importer)

	result, err := service.Sync(context.Background(), SourceManual, SyncModeIncremental)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Encrypted || result.Status != StatusSuccess {
		t.Fatalf("result=%+v", result)
	}
	if string(importer.last) != string(plaintext) {
		t.Fatalf("decrypted payload not imported")
	}
}

func TestServiceSyncEncryptedMissingUnlockPassword(t *testing.T) {
	plaintext := []byte(`{"version":"2.0"}`)
	envelope := mustEncryptEnvelope(t, "real-unlock", plaintext, 1000)
	raw, _ := json.Marshal(envelope)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(raw)
	}))
	defer server.Close()
	service := NewService(Config{
		URL: server.URL + "/f.json", Username: "u", Password: "webdav-only",
	}, &Client{HTTP: server.Client(), MaxBytes: 1 << 20}, &fakeImporter{})
	result, err := service.Sync(context.Background(), SourceManual, SyncModeIncremental)
	if err == nil || result.Category != CategoryDecryptFailed {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if !strings.Contains(result.Message, "unlock password") {
		t.Fatalf("message=%q", result.Message)
	}
}
