package httpapi_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lan/meta-gateway/internal/exchange"
)

func TestExchangeAdminRoundTripAndSecurity(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" || r.Header.Get("Authorization") != "Bearer upstream-secret" {
			http.Error(w, "rejected", http.StatusUnauthorized)
			return
		}
		_, _ = io.WriteString(w, `{"data":[{"id":"gpt-test"}]}`)
	}))
	defer upstream.Close()
	base, _, db := setupServer(t, upstream.URL)
	if _, err := db.Exec(`UPDATE route_members SET priority = 77, weight = 9, manual_override = 1 WHERE channel_id = 1`); err != nil {
		t.Fatal(err)
	}

	do := func(body string, authorized bool) (*http.Response, []byte) {
		t.Helper()
		req, _ := http.NewRequest(http.MethodPost, base+"/admin/exchange/export", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if authorized {
			req.Header.Set("Authorization", "Bearer admin-secret")
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return resp, raw
	}

	resp, _ := do(`{"include_secrets":true}`, false)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthorized status=%d", resp.StatusCode)
	}
	resp, portableRaw := do(`{"include_secrets":true,"channel_ids":[1]}`, true)
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Cache-Control") != "no-store" ||
		!bytes.Contains(portableRaw, []byte("upstream-secret")) || bytes.Contains(portableRaw, []byte("v1:")) {
		t.Fatalf("portable status=%d cache=%q body=%s", resp.StatusCode, resp.Header.Get("Cache-Control"), portableRaw)
	}
	var portable exchange.Envelope
	if err := json.Unmarshal(portableRaw, &portable); err != nil || !portable.Importable || len(portable.Items) != 1 {
		t.Fatalf("portable envelope=%+v err=%v", portable, err)
	}

	resp, metadataRaw := do(`{"include_secrets":false}`, true)
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Cache-Control") != "" ||
		bytes.Contains(metadataRaw, []byte("upstream-secret")) || bytes.Contains(metadataRaw, []byte("api_key")) ||
		bytes.Contains(metadataRaw, []byte("v1:")) {
		t.Fatalf("metadata status=%d cache=%q body=%s", resp.StatusCode, resp.Header.Get("Cache-Control"), metadataRaw)
	}

	importRequest, _ := http.NewRequest(http.MethodPost, base+"/admin/exchange/import", bytes.NewReader(portableRaw))
	importRequest.Header.Set("Authorization", "Bearer admin-secret")
	importRequest.Header.Set("Content-Type", "application/json")
	importResponse, err := http.DefaultClient.Do(importRequest)
	if err != nil {
		t.Fatal(err)
	}
	importRaw, _ := io.ReadAll(importResponse.Body)
	importResponse.Body.Close()
	if importResponse.StatusCode != http.StatusOK || !bytes.Contains(importRaw, []byte(`"adopted_count":1`)) ||
		!bytes.Contains(importRaw, []byte(`"discovery_success_count":1`)) || bytes.Contains(importRaw, []byte("upstream-secret")) {
		t.Fatalf("import status=%d body=%s", importResponse.StatusCode, importRaw)
	}
	var enabled, manualOverride bool
	var priority, weight int
	if err := db.QueryRow(`SELECT enabled, manual_override, priority, weight FROM route_members WHERE channel_id = 1`).Scan(
		&enabled, &manualOverride, &priority, &weight,
	); err != nil || !enabled || !manualOverride || priority != 77 || weight != 9 {
		t.Fatalf("manual route changed: enabled=%v override=%v priority=%d weight=%d err=%v",
			enabled, manualOverride, priority, weight, err)
	}

	metadataImport, _ := http.NewRequest(http.MethodPost, base+"/admin/exchange/import", bytes.NewReader(metadataRaw))
	metadataImport.Header.Set("Authorization", "Bearer admin-secret")
	metadataResponse, _ := http.DefaultClient.Do(metadataImport)
	metadataResponse.Body.Close()
	if metadataResponse.StatusCode != http.StatusBadRequest {
		t.Fatalf("metadata import status=%d", metadataResponse.StatusCode)
	}

	resp, _ = do(`{"include_secrets":true,"channel_ids":[99999]}`, true)
	if resp.StatusCode != http.StatusNotFound || resp.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("missing selected channel status=%d cache=%q", resp.StatusCode, resp.Header.Get("Cache-Control"))
	}
	resp, _ = do(`{"include_secrets":true,"unknown":1}`, true)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("unknown export field status=%d", resp.StatusCode)
	}
	resp, _ = do(`{"include_secrets":true} {}`, true)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("trailing export JSON status=%d", resp.StatusCode)
	}
}

func TestExchangeImportRejectsTrailingAndOversizedBodies(t *testing.T) {
	upstream := httptest.NewServer(http.NotFoundHandler())
	defer upstream.Close()
	base, _, _ := setupServer(t, upstream.URL)

	request := func(body io.Reader) int {
		req, _ := http.NewRequest(http.MethodPost, base+"/admin/exchange/import", body)
		req.Header.Set("Authorization", "Bearer admin-secret")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	if status := request(strings.NewReader(`[] trailing`)); status != http.StatusBadRequest {
		t.Fatalf("trailing import status=%d", status)
	}
	oversized := io.LimitReader(strings.NewReader(strings.Repeat("x", exchange.MaxBodyBytes+1)), exchange.MaxBodyBytes+1)
	if status := request(oversized); status != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized import status=%d", status)
	}
}
