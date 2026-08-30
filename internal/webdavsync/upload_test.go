package webdavsync

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lan/meta-gateway/internal/exchange"
)

func TestEncryptDecryptRoundtrip(t *testing.T) {
	plaintext := []byte(`{"format":"meta-gateway-aah-exchange","items":[]}`)
	encrypted, err := EncryptEnvelope(plaintext, "unlock-pass", envelopeUploadIterations)
	if err != nil {
		t.Fatal(err)
	}
	envelope, ok := TryParseEncryptedEnvelope(encrypted)
	if !ok {
		t.Fatalf("produced envelope not recognized: %s", encrypted)
	}
	decrypted, err := DecryptEnvelope(envelope, "unlock-pass")
	if err != nil {
		t.Fatal(err)
	}
	if string(decrypted) != string(plaintext) {
		t.Fatalf("roundtrip mismatch: %s", decrypted)
	}
	if _, err := DecryptEnvelope(envelope, "wrong-pass"); err == nil {
		t.Fatal("expected wrong password to fail")
	}
}

func TestResolveUploadTarget(t *testing.T) {
	upload, inPlace, err := ResolveUploadTarget("https://dav.example.com/files/")
	if err != nil {
		t.Fatal(err)
	}
	if inPlace {
		t.Fatal("directory-style address must expand into its own folder, not upload in place")
	}
	if !strings.HasSuffix(upload, "/files/meta-gateway-backup/meta-gateway-1-0.json") {
		t.Fatalf("upload path: %s", upload)
	}
	same, inPlace, err := ResolveUploadTarget("https://dav.example.com/files/custom.json")
	if err != nil {
		t.Fatal(err)
	}
	if !inPlace {
		t.Fatal("explicit file address must be reported as in-place")
	}
	if same != "https://dav.example.com/files/custom.json" {
		t.Fatalf("explicit file must upload in place: %s", same)
	}
	if _, _, err := ResolveUploadTarget(""); err == nil {
		t.Fatal("expected empty url rejection")
	}
}

// unreachableImportURL is a connection no upload test may use: if the upload
// direction ever borrowed the import connection, every request would fail.
const unreachableImportURL = "http://127.0.0.1:1/import-direction-only/"

// uploadConfig wires only the upload direction against the test server while
// the import direction points at a dead address and a different account —
// proving the two connections are independent.
func uploadConfig(base, unlock string) Config {
	return Config{
		URL:                  unreachableImportURL,
		Username:             "import-user",
		Password:             "import-pass",
		BackupPassword:       "import-unlock",
		UploadURL:            base + "/webdav/",
		UploadUsername:       "dav",
		UploadPassword:       "secret",
		UploadBackupPassword: unlock,
		UploadEnabled:        true,
		MaxBytes:             1 << 20,
	}
}

type fakeExporter struct {
	mu    sync.Mutex
	env   *exchange.Envelope
	err   error
	calls int
}

func (f *fakeExporter) Export(_ context.Context, _ exchange.ExportRequest) (*exchange.Envelope, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	return f.env, nil
}

func sampleEnvelope() *exchange.Envelope {
	return &exchange.Envelope{
		Format: exchange.Format, Version: exchange.Version,
		ExportedAt: time.Now().UTC(), Importable: true,
		Items: []exchange.Item{{Name: "main", BaseURL: "https://api.example.com", APIKey: "k", Models: []string{}}},
	}
}

// PUT handler returning 409 once (missing collection) then accepting,
// exercising the MKCOL-ancestors retry path. GET serves getBody when set.
type uploadRecorder struct {
	mu          sync.Mutex
	accepted    []byte
	mkcols      []string
	failPut     bool
	putAttempts int
	getBody     []byte
}

func (u *uploadRecorder) handler(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		if !ok || user != "dav" || pass != "secret" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch {
		case r.Method == http.MethodGet:
			u.mu.Lock()
			body := append([]byte(nil), u.getBody...)
			u.mu.Unlock()
			if len(body) == 0 {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			_, _ = w.Write(body)
		case r.Method == http.MethodPut:
			body := make([]byte, r.ContentLength)
			_, _ = io.ReadFull(r.Body, body)
			u.mu.Lock()
			defer u.mu.Unlock()
			u.putAttempts++
			if u.failPut && u.putAttempts == 1 {
				w.WriteHeader(http.StatusConflict)
				return
			}
			u.accepted = body
			w.WriteHeader(http.StatusCreated)
		case r.Method == "MKCOL":
			u.mu.Lock()
			u.mkcols = append(u.mkcols, r.URL.Path)
			u.mu.Unlock()
			w.WriteHeader(http.StatusCreated)
		default:
			t.Logf("unexpected method %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}
}

func TestServiceSyncUploadsEncryptedNativeBackup(t *testing.T) {
	recorder := &uploadRecorder{failPut: true}
	server := httptest.NewServer(recorder.handler(t))
	defer server.Close()

	exporter := &fakeExporter{env: sampleEnvelope()}
	service := NewService(uploadConfig(server.URL, "unlock-pass"),
		&Client{HTTP: server.Client(), MaxBytes: 1 << 20}, &fakeImporter{})
	service.SetExporter(exporter)

	result, err := service.Sync(context.Background(), SourceManual, "", DirectionUpload)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != StatusSuccess || result.Direction != DirectionUpload {
		t.Fatalf("result=%+v", result)
	}
	if result.Upload == nil || result.Upload.Status != StatusSuccess || !result.Upload.Encrypted {
		t.Fatalf("upload=%+v", result.Upload)
	}
	if !strings.HasSuffix(result.Upload.TargetURL, "/webdav/meta-gateway-backup/meta-gateway-1-0.json") {
		t.Fatalf("upload target=%s", result.Upload.TargetURL)
	}
	envelope, ok := TryParseEncryptedEnvelope(recorder.accepted)
	if !ok {
		t.Fatalf("uploaded body is not an encrypted envelope: %.80s", recorder.accepted)
	}
	decrypted, err := DecryptEnvelope(envelope, "unlock-pass")
	if err != nil {
		t.Fatal(err)
	}
	if !isCanonicalDocument(decrypted) {
		t.Fatalf("uploaded plaintext is not canonical: %.80s", decrypted)
	}
	if len(recorder.mkcols) == 0 {
		t.Fatal("expected MKCOL ancestors after 409")
	}
}

func TestServiceSyncUploadSkipsNonNativeRemoteFile(t *testing.T) {
	aahBackup := `{"version":"2.0","accounts":[],"apiCredentialProfiles":{"version":3,"profiles":[{"name":"main","apiType":"openai","baseUrl":"https://api.example.com","apiKey":"secret"}]}}`
	recorder := &uploadRecorder{getBody: []byte(aahBackup)}
	server := httptest.NewServer(recorder.handler(t))
	defer server.Close()

	importer := &fakeImporter{}
	exporter := &fakeExporter{env: sampleEnvelope()}
	cfg := uploadConfig(server.URL, "")
	cfg.UploadURL = server.URL + "/webdav/all-api-hub-1-0.json"
	service := NewService(cfg, &Client{HTTP: server.Client(), MaxBytes: 1 << 20}, importer)
	service.SetExporter(exporter)

	result, err := service.Sync(context.Background(), SourceManual, "", DirectionUpload)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != StatusSkipped || result.Upload == nil || result.Upload.Status != StatusSkipped {
		t.Fatalf("upload must be skipped: %+v", result)
	}
	if len(recorder.accepted) != 0 {
		t.Fatal("AAH backup must not be overwritten by native upload")
	}
	if exporter.calls != 0 {
		t.Fatal("export must not run when upload target holds a non-native document")
	}
}

func TestServiceSyncUploadCreatesMissingRemoteFile(t *testing.T) {
	recorder := &uploadRecorder{}
	server := httptest.NewServer(recorder.handler(t))
	defer server.Close()

	exporter := &fakeExporter{env: sampleEnvelope()}
	service := NewService(uploadConfig(server.URL, ""),
		&Client{HTTP: server.Client(), MaxBytes: 1 << 20}, &fakeImporter{})
	service.SetExporter(exporter)

	result, err := service.Sync(context.Background(), SourceManual, "", DirectionUpload)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != StatusSuccess || result.Upload == nil || result.Upload.Status != StatusSuccess {
		t.Fatalf("result=%+v", result)
	}
	if len(recorder.accepted) == 0 || !isCanonicalDocument(recorder.accepted) {
		t.Fatalf("uploaded body must be a plaintext canonical exchange (no backup password): %.80s", recorder.accepted)
	}
}

func TestServiceSyncUploadFailureFailsSync(t *testing.T) {
	recorder := &uploadRecorder{}
	server := httptest.NewServer(recorder.handler(t))
	defer server.Close()

	exporter := &fakeExporter{err: errors.New("boom")}
	service := NewService(uploadConfig(server.URL, ""),
		&Client{HTTP: server.Client(), MaxBytes: 1 << 20}, &fakeImporter{})
	service.SetExporter(exporter)

	result, err := service.Sync(context.Background(), SourceManual, "", DirectionUpload)
	if err == nil {
		t.Fatalf("expected failure, got %+v", result)
	}
	if result.Status != StatusFailed {
		t.Fatalf("result=%+v", result)
	}
}

func TestServiceSyncNoDirectionEnabled(t *testing.T) {
	recorder := &uploadRecorder{}
	server := httptest.NewServer(recorder.handler(t))
	defer server.Close()

	importer := &fakeImporter{}
	exporter := &fakeExporter{env: sampleEnvelope()}
	// Both connections complete but both direction toggles off: each sync is a
	// no-op skip, nothing is downloaded or uploaded.
	cfg := uploadConfig(server.URL, "")
	cfg.UploadEnabled = false
	service := NewService(cfg, &Client{HTTP: server.Client(), MaxBytes: 1 << 20}, importer)
	service.SetExporter(exporter)
	download, err := service.Sync(context.Background(), SourceManual, "", DirectionDownload)
	if err != nil {
		t.Fatal(err)
	}
	if download.Status != StatusSkipped || download.Import != nil {
		t.Fatalf("download=%+v", download)
	}
	upload, err := service.Sync(context.Background(), SourceManual, "", DirectionUpload)
	if err != nil {
		t.Fatal(err)
	}
	if upload.Status != StatusSkipped || upload.Upload != nil {
		t.Fatalf("upload=%+v", upload)
	}
	if exporter.calls != 0 {
		t.Fatal("exporter must not run when no direction is enabled")
	}
}

func TestServiceSyncUploadOnlySkipsDownloadAndImport(t *testing.T) {
	// The user-facing case: back up locally without pulling the AAH backup.
	// The import file exists remotely, but download+import is disabled.
	aahBackup := `{"version":"2.0","accounts":[],"apiCredentialProfiles":{"version":3,"profiles":[{"name":"main","apiType":"openai","baseUrl":"https://api.example.com","apiKey":"secret"}]}}`
	var gets atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/webdav/all-api-hub-backup/all-api-hub-1-0.json", func(w http.ResponseWriter, r *http.Request) {
		gets.Add(1)
		_, _ = w.Write([]byte(aahBackup))
	})
	recorder := &uploadRecorder{}
	mux.Handle("/webdav/meta-gateway-backup/", recorder.handler(t))
	server := httptest.NewServer(mux)
	defer server.Close()

	importer := &fakeImporter{}
	exporter := &fakeExporter{env: sampleEnvelope()}
	// Import connection points at the same server (where the AAH file lives) so
	// the gets counter proves download+import stays disabled; the upload
	// direction owns its own connection and target.
	cfg := uploadConfig(server.URL, "")
	cfg.URL = server.URL + "/webdav/"
	cfg.Username = "dav"
	cfg.Password = "secret"
	service := NewService(cfg, &Client{HTTP: server.Client(), MaxBytes: 1 << 20}, importer)
	service.SetExporter(exporter)

	result, err := service.Sync(context.Background(), SourceManual, "", DirectionUpload)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != StatusSuccess || result.Import != nil {
		t.Fatalf("result=%+v", result)
	}
	if result.Message != "webdav backup uploaded" {
		t.Fatalf("message=%q", result.Message)
	}
	if result.Upload == nil || result.Upload.Status != StatusSuccess {
		t.Fatalf("upload=%+v", result.Upload)
	}
	if gets.Load() != 0 {
		t.Fatal("download+import file must not be fetched when download is disabled")
	}
	if importer.last != nil {
		t.Fatal("importer must not run when download is disabled")
	}
}

func TestServiceTestConnectionUploadOnlyProbesUploadTarget(t *testing.T) {
	recorder := &uploadRecorder{}
	server := httptest.NewServer(recorder.handler(t))
	defer server.Close()

	// Only the upload connection exists: the download direction must report
	// config-incomplete instead of borrowing the upload address/credentials.
	service := NewService(Config{
		UploadURL:      server.URL + "/webdav/",
		UploadUsername: "dav",
		UploadPassword: "secret",
		UploadEnabled:  true,
		MaxBytes:       1 << 20,
	}, &Client{HTTP: server.Client(), MaxBytes: 1 << 20}, &fakeImporter{})

	download, err := service.TestConnection(context.Background(), DirectionDownload)
	if err == nil || download.Category != CategoryConfigIncomplete {
		t.Fatalf("download must report config incomplete: %+v err=%v", download, err)
	}

	result, err := service.TestConnection(context.Background(), DirectionUpload)
	if err != nil || result.Status != StatusSuccess {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if !strings.Contains(result.Message, "created on first upload") {
		t.Fatalf("message=%q", result.Message)
	}
	if !strings.HasSuffix(result.TargetURL, "/webdav/meta-gateway-backup/meta-gateway-1-0.json") {
		t.Fatalf("target=%s", result.TargetURL)
	}
}

func TestScheduledUploadRunnerRunsAndSkips(t *testing.T) {
	recorder := &uploadRecorder{}
	server := httptest.NewServer(recorder.handler(t))
	defer server.Close()

	exporter := &fakeExporter{env: sampleEnvelope()}
	service := NewService(uploadConfig(server.URL, ""),
		&Client{HTTP: server.Client(), MaxBytes: 1 << 20}, &fakeImporter{})
	service.SetExporter(exporter)

	result, err := UploadRunner(service).RunScheduled(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != StatusSuccess || result.Direction != DirectionUpload || result.Source != SourceScheduled {
		t.Fatalf("result=%+v", result)
	}
	if len(recorder.accepted) == 0 {
		t.Fatal("scheduled upload must PUT the backup")
	}

	cfg := uploadConfig(server.URL, "")
	cfg.UploadEnabled = false
	disabled := NewService(cfg, &Client{HTTP: server.Client(), MaxBytes: 1 << 20}, &fakeImporter{})
	skipped, err := UploadRunner(disabled).RunScheduled(context.Background())
	if err != nil || skipped.Status != StatusSkipped || skipped.Direction != DirectionUpload {
		t.Fatalf("skipped=%+v err=%v", skipped, err)
	}
	if exporter.calls > 1 {
		t.Fatalf("exporter ran %d times, expected 1", exporter.calls)
	}
}
