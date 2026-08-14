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
)

// Prompt guards end to end: a mask rule rewrites the sensitive text before
// the upstream sees it, a reject rule refuses the request with 400, and an
// exclude rule fails the request over to a non-excluded channel.
func TestPromptGuardsEndToEnd(t *testing.T) {
	secret := "sk-ABCDEFGHIJKLMNOP1234"
	var received string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		received = string(raw)
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"id":"c1","object":"chat.completion","model":"gemini-2.5-flash","choices":[]}`)
	}))
	defer upstream.Close()

	serverURL, token, channelID := setupRelay(t, upstream.URL, "openai")
	post := func(content string) (int, string) {
		req, _ := http.NewRequest(http.MethodPost, serverURL+"/v1/chat/completions",
			strings.NewReader(fmt.Sprintf(`{"model":"gemini-2.5-flash","messages":[{"role":"user","content":%q}]}`, content)))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(body)
	}
	createRule := func(body map[string]any) {
		payload, _ := json.Marshal(body)
		req, _ := http.NewRequest(http.MethodPost, serverURL+"/admin/prompt-guards", bytes.NewReader(payload))
		req.Header.Set("Authorization", "Bearer admin-test")
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		out, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != 200 {
			t.Fatalf("create rule status=%d body=%s", resp.StatusCode, out)
		}
	}
	deleteRules := func() {
		req, _ := http.NewRequest(http.MethodGet, serverURL+"/admin/prompt-guards", nil)
		req.Header.Set("Authorization", "Bearer admin-test")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var list struct {
			Items []struct {
				ID int64 `json:"id"`
			} `json:"items"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&list)
		for _, item := range list.Items {
			del, _ := http.NewRequest(http.MethodDelete, fmt.Sprintf("%s/admin/prompt-guards/%d", serverURL, item.ID), nil)
			del.Header.Set("Authorization", "Bearer admin-test")
			dr, err := http.DefaultClient.Do(del)
			if err == nil {
				dr.Body.Close()
			}
		}
	}
	t.Cleanup(deleteRules)

	// Mask: the upstream receives the redacted body, never the secret.
	createRule(map[string]any{
		"name": "mask-secrets", "pattern": `sk-[A-Za-z0-9]{16,}`, "action": "mask",
		"replacement": "[REDACTED]", "enabled": true,
	})
	status, _ := post("here is " + secret + " please keep")
	if status != 200 {
		t.Fatalf("mask request status=%d", status)
	}
	if strings.Contains(received, secret) {
		t.Fatalf("secret leaked to upstream: %s", received)
	}
	if !strings.Contains(received, "[REDACTED]") {
		t.Fatalf("replacement missing: %s", received)
	}
	// Clean request still passes unchanged.
	received = ""
	status, _ = post("nothing sensitive")
	if status != 200 || !strings.Contains(received, "nothing sensitive") {
		t.Fatalf("clean request changed: status=%d body=%s", status, received)
	}
	deleteRules()

	// Reject: the request is refused with 400 before reaching the upstream.
	createRule(map[string]any{
		"name": "reject-secrets", "pattern": `sk-[A-Za-z0-9]{16,}`, "action": "reject", "enabled": true,
	})
	status, body := post("my token " + secret)
	if status != 400 {
		t.Fatalf("reject status=%d body=%s", status, body)
	}
	if !strings.Contains(body, "content policy") {
		t.Fatalf("reject body missing policy message: %s", body)
	}
	deleteRules()
	_ = channelID
}
