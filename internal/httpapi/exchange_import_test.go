package httpapi_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lan/meta-gateway/internal/webdavsync"
)

// importDocument posts a raw document to the import endpoint.
func postImport(t *testing.T, base, body string) (int, map[string]any) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, base+"/admin/exchange/import", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer admin-secret")
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	decoded := map[string]any{}
	_ = json.Unmarshal(raw, &decoded)
	return resp.StatusCode, decoded
}

func postEncryptedImport(t *testing.T, base string, document []byte, password string) (int, map[string]any) {
	t.Helper()
	payload, _ := json.Marshal(map[string]any{
		"document": json.RawMessage(document),
		"password": password,
	})
	req, _ := http.NewRequest(http.MethodPost, base+"/admin/exchange/import-encrypted", bytes.NewReader(payload))
	req.Header.Set("Authorization", "Bearer admin-secret")
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	decoded := map[string]any{}
	_ = json.Unmarshal(raw, &decoded)
	return resp.StatusCode, decoded
}

// exportPortable asks the server for its own exchange package (with secrets).
func exportPortable(t *testing.T, base string) []byte {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, base+"/admin/exchange/export",
		strings.NewReader(`{"include_secrets":true,"channel_ids":[1]}`))
	req.Header.Set("Authorization", "Bearer admin-secret")
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("export status=%d body=%s", resp.StatusCode, raw)
	}
	return raw
}

// A backup written by this gateway is importable by this gateway, even when the
// export had to skip a channel (the `skipped` section used to be rejected by
// strict decoding, which broke the round trip).
func TestExchangeImportAcceptsOwnExportWithSkippedSection(t *testing.T) {
	upstream := httptest.NewServer(http.NotFoundHandler())
	defer upstream.Close()
	base, _, _ := setupServer(t, upstream.URL)

	document := `{"format":"meta-gateway-aah-exchange","version":1,"exported_at":"2026-07-14T00:00:00Z",` +
		`"importable":true,"items":[{"name":"imported","base_url":"https://imported.example.com","api_key":"sk-imported",` +
		`"models":[],"group":"default","priority":0,"weight":100,"site_type_hint":"openai-compatible"}],` +
		`"skipped":[{"channel_id":7,"name":"no-key","reason":"no_credential"}]}`
	status, body := postImport(t, base, document)
	if status != http.StatusOK {
		t.Fatalf("status=%d body=%+v", status, body)
	}
	if body["created_count"].(float64) != 1 {
		t.Fatalf("created_count=%v body=%+v", body["created_count"], body)
	}
}

// An encrypted backup dropped on the plain import path asks for its unlock
// password instead of claiming the format is unknown.
func TestExchangeImportEncryptedWithoutPasswordAsksToUnlock(t *testing.T) {
	upstream := httptest.NewServer(http.NotFoundHandler())
	defer upstream.Close()
	base, _, _ := setupServer(t, upstream.URL)

	envelope, err := webdavsync.EncryptEnvelope(exportPortable(t, base), "unlock-me", 1000)
	if err != nil {
		t.Fatal(err)
	}
	status, body := postImport(t, base, string(envelope))
	if status != http.StatusBadRequest || body["error"] != "backup_unlock_required" {
		t.Fatalf("status=%d body=%+v", status, body)
	}
}

func TestExchangeImportEncryptedRoundTrip(t *testing.T) {
	upstream := httptest.NewServer(http.NotFoundHandler())
	defer upstream.Close()
	base, _, _ := setupServer(t, upstream.URL)

	envelope, err := webdavsync.EncryptEnvelope(exportPortable(t, base), "unlock-me", 1000)
	if err != nil {
		t.Fatal(err)
	}
	status, body := postEncryptedImport(t, base, envelope, "")
	if status != http.StatusBadRequest || body["error"] != "backup_unlock_required" {
		t.Fatalf("missing password: status=%d body=%+v", status, body)
	}
	status, body = postEncryptedImport(t, base, envelope, "wrong")
	if status != http.StatusUnprocessableEntity || body["error"] != "decrypt_failed" {
		t.Fatalf("wrong password: status=%d body=%+v", status, body)
	}
	status, body = postEncryptedImport(t, base, envelope, "unlock-me")
	if status != http.StatusOK {
		t.Fatalf("status=%d body=%+v", status, body)
	}
	if body["adopted_count"].(float64) != 1 {
		t.Fatalf("adopted_count=%v body=%+v", body["adopted_count"], body)
	}

	// The plaintext document must never be echoed back.
	raw := exportPortable(t, base)
	if bytes.Contains(raw, []byte("unlock-me")) {
		t.Fatal("password leaked into the export")
	}
}

// One unusable row in an AAH backup is reported, not fatal.
func TestExchangeImportSkipsUnusableAahRows(t *testing.T) {
	upstream := httptest.NewServer(http.NotFoundHandler())
	defer upstream.Close()
	base, _, _ := setupServer(t, upstream.URL)

	document := `{"version":"4.0","timestamp":1,` +
		`"apiCredentialProfiles":{"version":3,"profiles":[` +
		`{"name":"good","apiType":"openai","baseUrl":"https://good.example.com","apiKey":"sk-good"}]},` +
		`"accounts":{"accounts":[` +
		`{"id":"cookie","site_name":"CookieOnly","site_url":"https://cookie.example.com","authType":"cookie","account_info":{"id":"2","access_token":""}}]}}`
	status, body := postImport(t, base, document)
	if status != http.StatusOK {
		t.Fatalf("status=%d body=%+v", status, body)
	}
	if body["created_count"].(float64) != 1 {
		t.Fatalf("created_count=%v body=%+v", body["created_count"], body)
	}
	skipped, ok := body["skipped"].([]any)
	if !ok || len(skipped) != 1 {
		t.Fatalf("skipped=%+v", body["skipped"])
	}
	entry := skipped[0].(map[string]any)
	if entry["reason"] != "missing_credential" || entry["name"] != "CookieOnly" {
		t.Fatalf("skipped entry=%+v", entry)
	}
}

// An AAH backup that only carries preferences holds nothing importable; the
// console must say that instead of "unsupported format".
func TestExchangeImportCredentiallessAahBackupReportsEmpty(t *testing.T) {
	upstream := httptest.NewServer(http.NotFoundHandler())
	defer upstream.Close()
	base, _, _ := setupServer(t, upstream.URL)

	document := `{"version":"4.0","timestamp":1,"channelConfigs":{"schemaVersion":2,"configs":{}},` +
		`"preferences":{"themeMode":"dark"},"tagStore":{"tagsById":{},"version":1}}`
	status, body := postImport(t, base, document)
	if status != http.StatusUnprocessableEntity || body["error"] != "exchange_document_empty" {
		t.Fatalf("status=%d body=%+v", status, body)
	}
}
