package httpapi_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lan/meta-gateway/internal/config"
	"github.com/lan/meta-gateway/internal/crypto"
	"github.com/lan/meta-gateway/internal/httpapi"
	"github.com/lan/meta-gateway/internal/store"
)

func setupServer(t *testing.T, upstreamURL string) (string, string) {
	t.Helper()

	dir := t.TempDir()
	db, err := store.Open(dir)
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	enc, err := crypto.New("integration-master-key-32b!!")
	if err != nil {
		t.Fatalf("crypto: %v", err)
	}

	cfg := &config.Config{
		HTTPAddr:   ":0",
		DataDir:    dir,
		AdminToken: "admin-secret",
		MasterKey:  "integration-master-key-32b!!",
	}
	handler := httpapi.New(cfg, db, enc)

	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	adminJSON := func(method, path string, body any) (int, map[string]any) {
		t.Helper()
		var rdr io.Reader
		if body != nil {
			b, _ := json.Marshal(body)
			rdr = bytes.NewReader(b)
		}
		req, err := http.NewRequest(method, srv.URL+path, rdr)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer admin-secret")
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("%s %s: %v", method, path, err)
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		var out map[string]any
		if len(raw) > 0 {
			_ = json.Unmarshal(raw, &out)
		}
		if resp.StatusCode >= 300 {
			t.Fatalf("%s %s status %d body %s", method, path, resp.StatusCode, raw)
		}
		return resp.StatusCode, out
	}

	_, site := adminJSON("POST", "/admin/sites", map[string]string{
		"name":     "up",
		"base_url": upstreamURL,
		"platform": "openai-compatible",
		"status":   "enabled",
	})
	siteID := asInt64(t, site["id"])

	_, cred := adminJSON("POST", fmt.Sprintf("/admin/sites/%d/credentials", siteID), map[string]string{
		"kind":   "api_key",
		"secret": "upstream-secret",
	})
	credID := asInt64(t, cred["id"])

	_, ch := adminJSON("POST", "/admin/channels", map[string]any{
		"site_id":       siteID,
		"credential_id": credID,
		"name":          "primary",
		"base_url":      upstreamURL,
		"models_csv":    "gpt-test",
		"group_name":    "default",
		"priority":      0,
		"weight":        100,
		"status":        "enabled",
	})
	chID := asInt64(t, ch["id"])

	_, route := adminJSON("POST", "/admin/routes", map[string]any{
		"model_pattern": "gpt-test",
		"enabled":       true,
	})
	routeID := asInt64(t, route["id"])

	adminJSON("POST", fmt.Sprintf("/admin/routes/%d/members", routeID), map[string]any{
		"channel_id": chID,
		"priority":   0,
		"weight":     100,
		"enabled":    true,
	})

	_, key := adminJSON("POST", "/admin/downstream-keys", map[string]string{"name": "cli"})
	rawToken, _ := key["token"].(string)
	if rawToken == "" {
		t.Fatalf("expected raw downstream token, got %#v", key)
	}

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/admin/sites", nil)
	bad, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	bad.Body.Close()
	if bad.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", bad.StatusCode)
	}

	return srv.URL, rawToken
}

func asInt64(t *testing.T, v any) int64 {
	t.Helper()
	switch n := v.(type) {
	case float64:
		return int64(n)
	case int64:
		return n
	case json.Number:
		i, err := n.Int64()
		if err != nil {
			t.Fatalf("json number: %v", err)
		}
		return i
	default:
		t.Fatalf("expected numeric id, got %T %#v", v, v)
		return 0
	}
}

func TestHealthz(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	base, _ := setupServer(t, upstream.URL)
	resp, err := http.Get(base + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status %d", resp.StatusCode)
	}
	var body map[string]string
	json.NewDecoder(resp.Body).Decode(&body)
	if body["status"] != "ok" {
		t.Fatalf("body %#v", body)
	}
}

func TestChatCompletionsNonStreamAndStream(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer upstream-secret" {
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
		body, _ := io.ReadAll(r.Body)
		stream := strings.Contains(string(body), `"stream":true`)
		if stream {
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\ndata: [DONE]\n\n")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"id":"c1","object":"chat.completion","model":"gpt-test","choices":[{"message":{"role":"assistant","content":"ok"}}]}`)
	}))
	defer upstream.Close()

	base, token := setupServer(t, upstream.URL)

	req, _ := http.NewRequest(http.MethodGet, base+"/v1/models", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var models map[string]any
	json.NewDecoder(resp.Body).Decode(&models)
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("models status %d", resp.StatusCode)
	}
	data, _ := models["data"].([]any)
	if len(data) == 0 {
		t.Fatalf("expected models, got %#v", models)
	}

	reqBody := `{"model":"gpt-test","messages":[{"role":"user","content":"hi"}]}`
	req, _ = http.NewRequest(http.MethodPost, base+"/v1/chat/completions", strings.NewReader(reqBody))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("chat status %d body %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "chat.completion") {
		t.Fatalf("unexpected body %s", body)
	}

	reqBody = `{"model":"gpt-test","messages":[{"role":"user","content":"hi"}],"stream":true}`
	req, _ = http.NewRequest(http.MethodPost, base+"/v1/chat/completions", strings.NewReader(reqBody))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("stream status %d body %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "data:") || !strings.Contains(string(body), "[DONE]") {
		t.Fatalf("unexpected stream body %s", body)
	}

	req, _ = http.NewRequest(http.MethodGet, base+"/admin/proxy-logs", nil)
	req.Header.Set("Authorization", "Bearer admin-secret")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	logsBody, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("logs status %d", resp.StatusCode)
	}
	if strings.Contains(string(logsBody), "upstream-secret") {
		t.Fatal("proxy logs leaked secret")
	}
	var logs []map[string]any
	if err := json.Unmarshal(logsBody, &logs); err != nil {
		t.Fatalf("decode proxy logs: %v body %s", err, logsBody)
	}
	if len(logs) == 0 {
		t.Fatal("expected proxy logs after relay")
	}
	for _, entry := range logs {
		if model, _ := entry["model"].(string); model != "gpt-test" {
			t.Fatalf("unexpected model in log: %#v", entry)
		}
		if _, ok := entry["channel_id"]; !ok {
			t.Fatalf("proxy log missing channel_id: %#v", entry)
		}
		if _, ok := entry["status"]; !ok {
			t.Fatalf("proxy log missing status: %#v", entry)
		}
	}
}
