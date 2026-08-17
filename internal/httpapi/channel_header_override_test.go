package httpapi_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/lan/meta-gateway/internal/domain"
)

func TestChannelHeaderOverrideRequiresJSONStringMap(t *testing.T) {
	base, _, _ := setupServer(t, "http://127.0.0.1:1")
	_, conn := postConnection(t, base, "admin-secret", map[string]any{
		"base_url": "https://api.example.com",
		"secret":   "sk-test",
	})
	path := fmt.Sprintf("/admin/channels/%d", conn.Channel.ID)

	for _, value := range []string{
		"Authorization: Bearer plaintext",
		`["X-Test"]`,
		`{"X-Test":123}`,
		`null`,
	} {
		status, body := adminJSONBody(t, base, "admin-secret", http.MethodPut, path, map[string]any{
			"header_override": value,
		})
		if status != http.StatusBadRequest {
			t.Fatalf("header_override=%v: status=%d body=%s", value, status, body)
		}
	}

	status, body := adminJSONBody(t, base, "admin-secret", http.MethodPut, path, map[string]any{
		"header_override": `{"X-Test":"ok"}`,
	})
	if status != http.StatusOK {
		t.Fatalf("valid override: status=%d body=%s", status, body)
	}
	var updated struct {
		HeaderOverride string `json:"header_override"`
	}
	if err := json.Unmarshal(body, &updated); err != nil {
		t.Fatal(err)
	}
	if updated.HeaderOverride != `{"X-Test":"ok"}` {
		t.Fatalf("header_override=%q", updated.HeaderOverride)
	}
}

func TestChannelPartialUpdatePreservesOmittedFields(t *testing.T) {
	base, _, _ := setupServer(t, "http://127.0.0.1:1")
	_, conn := postConnection(t, base, "admin-secret", map[string]any{
		"base_url": "https://api.example.com",
		"secret":   "sk-test",
	})
	path := fmt.Sprintf("/admin/channels/%d", conn.Channel.ID)
	payloadRules := `[{"name":"preserve","actions":[]}]`
	status, body := adminJSONBody(t, base, "admin-secret", http.MethodPut, path, map[string]any{
		"name":                 "preserved channel",
		"base_url":             "https://api.example.com/v1",
		"models_csv":           "model-a,model-b",
		"group_name":           "paid",
		"priority":             7,
		"weight":               42,
		"status":               "enabled",
		"type_hint":            "openai-compatible",
		"max_reasoning_effort": "medium",
		"payload_rules":        payloadRules,
		"max_concurrent":       3,
		"system_prompt":        "keep this prompt",
		"retry_config":         `{"status_codes":[429]}`,
		"tags":                 "alpha,beta",
		"stable_first":         true,
	})
	if status != http.StatusOK {
		t.Fatalf("seed channel settings: status=%d body=%s", status, body)
	}
	status, body = adminJSONBody(t, base, "admin-secret", http.MethodPut, path, map[string]any{
		"header_override": `{"X-Test":"ok"}`,
	})
	if status != http.StatusOK {
		t.Fatalf("partial channel update: status=%d body=%s", status, body)
	}
	var updated domain.Channel
	if err := json.Unmarshal(body, &updated); err != nil {
		t.Fatal(err)
	}
	if updated.Name != "preserved channel" ||
		updated.BaseURL != "https://api.example.com/v1" ||
		updated.ModelsCSV != "model-a,model-b" ||
		updated.GroupName != "paid" ||
		updated.Priority != 7 || updated.Weight != 42 ||
		updated.Status != "enabled" || updated.TypeHint != "openai-compatible" ||
		updated.MaxReasoningEffort != "medium" || updated.PayloadRules != payloadRules ||
		updated.MaxConcurrent != 3 || updated.SystemPrompt != "keep this prompt" ||
		updated.RetryConfig != `{"status_codes":[429]}` || updated.Tags != "alpha,beta" ||
		!updated.StableFirst {
		t.Fatalf("partial update overwrote omitted fields: %+v", updated)
	}
}
