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
	"time"

	"github.com/lan/meta-gateway/internal/config"
	"github.com/lan/meta-gateway/internal/crypto"
	"github.com/lan/meta-gateway/internal/domain"
	"github.com/lan/meta-gateway/internal/httpapi"
	"github.com/lan/meta-gateway/internal/store"
)

func setupServer(t *testing.T, upstreamURL string) (string, string, *store.DB) {
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
		HTTPAddr:                      ":0",
		DataDir:                       dir,
		AdminToken:                    "admin-secret",
		AdminTokens:                   []string{"admin-secret"},
		MasterKey:                     "integration-master-key-32b!!",
		ExchangeAllowSecretExport:     true,
		OutboundAllowCIDRs:            []string{"127.0.0.0/8", "::1/128"},
		OutboundConnectTimeout:        2 * time.Second,
		OutboundTLSHandshakeTimeout:   2 * time.Second,
		OutboundResponseHeaderTimeout: 2 * time.Second,
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

	return srv.URL, rawToken, db
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

	base, _, _ := setupServer(t, upstream.URL)
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

	base, token, _ := setupServer(t, upstream.URL)

	explainReq, _ := http.NewRequest(http.MethodGet, base+"/admin/routes/explain?model=gpt-test", nil)
	explainReq.Header.Set("Authorization", "Bearer admin-secret")
	explainResp, err := http.DefaultClient.Do(explainReq)
	if err != nil {
		t.Fatal(err)
	}
	var explanation map[string]any
	_ = json.NewDecoder(explainResp.Body).Decode(&explanation)
	explainResp.Body.Close()
	if explainResp.StatusCode != http.StatusOK || asInt64(t, explanation["route_id"]) <= 0 {
		t.Fatalf("explain status=%d body=%#v", explainResp.StatusCode, explanation)
	}
	candidates, _ := explanation["candidates"].([]any)
	if len(candidates) != 1 {
		t.Fatalf("unexpected explain candidates: %#v", explanation)
	}

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

func TestDiscoveryAdminEndpoints(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer upstream-secret" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		_, _ = io.WriteString(w, `{"data":[{"id":" discovered-b "},{"id":"discovered-a"},{"id":"discovered-a"}]}`)
	}))
	defer upstream.Close()
	base, _, _ := setupServer(t, upstream.URL)

	unauthorized, err := http.Post(base+"/admin/discovery/channels/1/refresh", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	unauthorized.Body.Close()
	if unauthorized.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthorized status=%d", unauthorized.StatusCode)
	}

	req, _ := http.NewRequest(http.MethodPost, base+"/admin/discovery/channels/1/probe", nil)
	req.Header.Set("Authorization", "Bearer admin-secret")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), `"latency_ms"`) || strings.Contains(string(body), "upstream-secret") {
		t.Fatalf("probe status=%d body=%s", resp.StatusCode, body)
	}
	var discoveredCount int
	if err := dbCount(base, "/admin/discovery/models?channel_id=1", &discoveredCount); err != nil {
		t.Fatal(err)
	}
	if discoveredCount != 0 {
		t.Fatalf("probe persisted %d discovered models", discoveredCount)
	}

	req, _ = http.NewRequest(http.MethodPost, base+"/admin/discovery/channels/1/refresh", nil)
	req.Header.Set("Authorization", "Bearer admin-secret")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), `"adapter":"openai-compatible"`) || strings.Contains(string(body), "upstream-secret") {
		t.Fatalf("refresh status=%d body=%s", resp.StatusCode, body)
	}

	req, _ = http.NewRequest(http.MethodGet, base+"/admin/discovery/models?channel_id=1", nil)
	req.Header.Set("Authorization", "Bearer admin-secret")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), "discovered-a") || strings.Contains(string(body), "upstream-secret") {
		t.Fatalf("models status=%d body=%s", resp.StatusCode, body)
	}

	createBody := strings.NewReader(`{"site_id":1,"credential_id":1,"name":"unsupported","base_url":"` + upstream.URL + `","status":"enabled","type_hint":"not-a-real-adapter"}`)
	req, _ = http.NewRequest(http.MethodPost, base+"/admin/channels", createBody)
	req.Header.Set("Authorization", "Bearer admin-secret")
	req.Header.Set("Content-Type", "application/json")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create unsupported channel status=%d", resp.StatusCode)
	}

	req, _ = http.NewRequest(http.MethodPost, base+"/admin/discovery/refresh", nil)
	req.Header.Set("Authorization", "Bearer admin-secret")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), `"success_count":1`) || !strings.Contains(string(body), `"failure_count":1`) || !strings.Contains(string(body), "unsupported_adapter") {
		t.Fatalf("full refresh status=%d body=%s", resp.StatusCode, body)
	}
	if strings.Contains(string(body), "upstream-secret") {
		t.Fatal("full refresh leaked secret")
	}
}

func dbCount(base, path string, count *int) error {
	req, _ := http.NewRequest(http.MethodGet, base+path, nil)
	req.Header.Set("Authorization", "Bearer admin-secret")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	var rows []map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&rows); err != nil {
		return err
	}
	*count = len(rows)
	return nil
}

func TestChannelAndRouteOperationalEndpoints(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" {
			_, _ = io.WriteString(w, `{"data":[{"id":"gpt-test"}]}`)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()
	base, _, db := setupServer(t, upstream.URL)
	if _, err := db.DiscoveredModel.Reconcile(t.Context(), store.ReconcileInput{
		ChannelID: 1,
		Models:    []string{"gpt-test"},
		Source:    "openai-compatible",
		LatencyMs: 12,
		CheckedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.RouteMember.RecordFailure(1, time.Now(), time.Minute, "transport"); err != nil {
		t.Fatal(err)
	}

	for _, path := range []string{"/admin/channels/overview", "/admin/routes/overview"} {
		req, _ := http.NewRequest(http.MethodGet, base+path, nil)
		req.Header.Set("Authorization", "Bearer admin-secret")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), "primary") || strings.Contains(string(body), "upstream-secret") {
			t.Fatalf("%s status=%d body=%s", path, resp.StatusCode, body)
		}
	}

	req, _ := http.NewRequest(http.MethodPost, base+"/admin/route-members/1/clear-health", nil)
	req.Header.Set("Authorization", "Bearer admin-secret")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), `"fail_count":0`) || strings.Contains(string(body), "transport") {
		t.Fatalf("clear health status=%d body=%s", resp.StatusCode, body)
	}
}

func TestProxyLogsListFilters(t *testing.T) {
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
		HTTPAddr:                      ":0",
		DataDir:                       dir,
		AdminToken:                    "admin-secret",
		MasterKey:                     "integration-master-key-32b!!",
		OutboundAllowCIDRs:            []string{"127.0.0.0/8", "::1/128"},
		OutboundConnectTimeout:        2 * time.Second,
		OutboundTLSHandshakeTimeout:   2 * time.Second,
		OutboundResponseHeaderTimeout: 2 * time.Second,
	}
	srv := httptest.NewServer(httpapi.New(cfg, db, enc))
	t.Cleanup(srv.Close)

	// Seed via store so we control channel/site ids without full admin bootstrap.
	siteA, err := db.Site.Create(&domain.Site{
		Name: "a", BaseURL: "https://a.example.com", Platform: "openai-compatible", Status: domain.StatusEnabled,
	})
	if err != nil {
		t.Fatal(err)
	}
	siteB, err := db.Site.Create(&domain.Site{
		Name: "b", BaseURL: "https://b.example.com", Platform: "openai-compatible", Status: domain.StatusEnabled,
	})
	if err != nil {
		t.Fatal(err)
	}
	chA, err := db.Channel.Create(&domain.Channel{SiteID: &siteA, Name: "a", Status: domain.StatusEnabled, Weight: 100})
	if err != nil {
		t.Fatal(err)
	}
	chB, err := db.Channel.Create(&domain.Channel{SiteID: &siteB, Name: "b", Status: domain.StatusEnabled, Weight: 100})
	if err != nil {
		t.Fatal(err)
	}
	seed := []struct {
		req, model string
		channel    int64
		status     int
	}{
		{"req-a-ok", "gpt-a", chA, 200},
		{"req-a-fail", "gpt-a", chA, 502},
		{"req-b-ok", "gpt-b", chB, 200},
		{"req-b-fail", "gpt-other", chB, 500},
	}
	for _, row := range seed {
		if _, err := db.ProxyLog.Insert(&domain.ProxyLog{
			RequestID: row.req, ChannelID: row.channel, Model: row.model,
			Status: row.status, LatencyMs: 5, Attempt: 1,
		}); err != nil {
			t.Fatal(err)
		}
	}

	getLogs := func(path string) (int, []map[string]any, string) {
		t.Helper()
		req, err := http.NewRequest(http.MethodGet, srv.URL+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer admin-secret")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		var logs []map[string]any
		if len(raw) > 0 && resp.StatusCode == http.StatusOK {
			if err := json.Unmarshal(raw, &logs); err != nil {
				t.Fatalf("decode %s: %v body %s", path, err, raw)
			}
		}
		return resp.StatusCode, logs, string(raw)
	}
	requestIDs := func(logs []map[string]any) []string {
		out := make([]string, 0, len(logs))
		for _, l := range logs {
			if id, _ := l["request_id"].(string); id != "" {
				out = append(out, id)
			}
		}
		return out
	}

	status, logs, body := getLogs(fmt.Sprintf("/admin/proxy-logs?site_id=%d", siteA))
	if status != http.StatusOK {
		t.Fatalf("site filter status=%d body=%s", status, body)
	}
	if got := requestIDs(logs); strings.Join(got, ",") != "req-a-fail,req-a-ok" {
		t.Fatalf("site filter got %v", got)
	}

	status, logs, body = getLogs(fmt.Sprintf("/admin/proxy-logs?channel_id=%d", chB))
	if status != http.StatusOK {
		t.Fatalf("channel filter status=%d body=%s", status, body)
	}
	if got := requestIDs(logs); strings.Join(got, ",") != "req-b-fail,req-b-ok" {
		t.Fatalf("channel filter got %v", got)
	}

	status, logs, body = getLogs("/admin/proxy-logs?model=gpt-a")
	if status != http.StatusOK {
		t.Fatalf("model filter status=%d body=%s", status, body)
	}
	if got := requestIDs(logs); strings.Join(got, ",") != "req-a-fail,req-a-ok" {
		t.Fatalf("model filter got %v", got)
	}

	status, logs, body = getLogs("/admin/proxy-logs?status=failed")
	if status != http.StatusOK {
		t.Fatalf("failed filter status=%d body=%s", status, body)
	}
	if got := requestIDs(logs); strings.Join(got, ",") != "req-b-fail,req-a-fail" {
		t.Fatalf("failed filter got %v", got)
	}

	status, logs, body = getLogs("/admin/proxy-logs?limit=2")
	if status != http.StatusOK {
		t.Fatalf("limit status=%d body=%s", status, body)
	}
	if len(logs) != 2 {
		t.Fatalf("expected 2 logs, got %d", len(logs))
	}
	beforeID := asInt64(t, logs[1]["id"])
	status, logs, body = getLogs(fmt.Sprintf("/admin/proxy-logs?limit=2&before_id=%d", beforeID))
	if status != http.StatusOK {
		t.Fatalf("before_id status=%d body=%s", status, body)
	}
	if got := requestIDs(logs); strings.Join(got, ",") != "req-a-fail,req-a-ok" {
		t.Fatalf("before_id page got %v", got)
	}

	for _, badPath := range []string{
		"/admin/proxy-logs?site_id=0",
		"/admin/proxy-logs?channel_id=abc",
		"/admin/proxy-logs?before_id=-1",
		"/admin/proxy-logs?status=banana",
		"/admin/proxy-logs?limit=0",
		"/admin/proxy-logs?limit=9999",
	} {
		status, _, body = getLogs(badPath)
		if status != http.StatusBadRequest {
			t.Fatalf("%s expected 400 got %d body=%s", badPath, status, body)
		}
		if strings.Contains(body, "SQL") || strings.Contains(strings.ToLower(body), "sqlite") {
			t.Fatalf("%s leaked SQL detail: %s", badPath, body)
		}
	}
}
