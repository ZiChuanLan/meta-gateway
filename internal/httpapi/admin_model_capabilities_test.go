package httpapi_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestModelCapabilityEndpoints(t *testing.T) {
	upstream := mockOpenAIUpstream(t)
	defer upstream.Close()
	serverURL, _, _ := setupRelay(t, upstream.URL, "openai-compatible")

	// A fresh install carries no operator-authored row, and the kind
	// vocabulary is always returned so the console can render a picker without
	// hardcoding the list.
	//
	// It is not an *empty* registry: creating a route is what tells the
	// gateway to fill a model's row in, and the relay setup routes
	// gemini-2.5-flash — so the assertion is on provenance, not on the count.
	body := get(t, serverURL+"/admin/model-capabilities")
	var list struct {
		Items []map[string]any `json:"items"`
		Kinds []string         `json:"kinds"`
	}
	if err := json.Unmarshal(body, &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Kinds) == 0 {
		t.Fatal("expected kind vocabulary")
	}
	for _, item := range list.Items {
		if item["source"] == "manual" {
			t.Fatalf("fresh registry carries an operator row: %v", item)
		}
	}
	if item := capabilityRow(t, list.Items, "gemini-2.5-flash"); item["source"] != "discovery" {
		t.Fatalf("routing a model must bootstrap its capability row: %v", list.Items)
	}

	// Create.
	put(t, serverURL+"/admin/model-capabilities/grok-imagine-image-edit", map[string]any{
		"kind":              "image_edit",
		"endpoints":         []string{"/v1/images/edits"},
		"input_formats":     []string{"json"},
		"input_modalities":  []string{"text", "image"},
		"output_modalities": []string{"image"},
		"max_input_images":  8,
		"supports_stream":   false,
		"notes":             "grok2api: json only",
	})
	body = get(t, serverURL+"/admin/model-capabilities")
	if err := json.Unmarshal(body, &list); err != nil {
		t.Fatal(err)
	}
	created := capabilityRow(t, list.Items, "grok-imagine-image-edit")
	if created["source"] != "manual" {
		t.Errorf("explicit edits mark the row manual, got %v", created["source"])
	}

	// Partial update keeps untouched fields.
	put(t, serverURL+"/admin/model-capabilities/grok-imagine-image-edit", map[string]any{
		"max_input_images": 4,
	})
	body = get(t, serverURL+"/admin/model-capabilities")
	if err := json.Unmarshal(body, &list); err != nil {
		t.Fatal(err)
	}
	created = capabilityRow(t, list.Items, "grok-imagine-image-edit")
	if created["max_input_images"].(float64) != 4 {
		t.Fatalf("max_input_images = %v", created["max_input_images"])
	}
	if created["notes"] != "grok2api: json only" {
		t.Fatalf("notes lost on partial update: %v", created["notes"])
	}
	if created["kind"] != "image_edit" {
		t.Fatalf("kind lost on partial update: %v", created["kind"])
	}

	// Delete returns the model to classifier fallback.
	delReq, _ := http.NewRequest(http.MethodDelete,
		serverURL+"/admin/model-capabilities/grok-imagine-image-edit", nil)
	delReq.Header.Set("Authorization", "Bearer admin-test")
	delResp, err := http.DefaultClient.Do(delReq)
	if err != nil {
		t.Fatal(err)
	}
	_ = delResp.Body.Close()
	if delResp.StatusCode != http.StatusOK {
		t.Fatalf("delete = %d", delResp.StatusCode)
	}
}

// capabilityRow finds one registry row, so an assertion never silently reads a
// different model's row just because it happens to be first in the list.
func capabilityRow(t *testing.T, items []map[string]any, model string) map[string]any {
	t.Helper()
	for _, item := range items {
		if item["model"] == model {
			return item
		}
	}
	t.Fatalf("%s has no capability row: %v", model, items)
	return nil
}

func TestModelCapabilityResolveEndpoint(t *testing.T) {
	upstream := mockOpenAIUpstream(t)
	defer upstream.Close()
	serverURL, _, _ := setupRelay(t, upstream.URL, "openai-compatible")

	body := post(t, serverURL+"/admin/model-capabilities/resolve", map[string]any{
		"models": []string{"gpt-image-2", "grok-imagine-image-edit", "gemini-2.5-flash-image", "sora-2"},
	})
	var out struct {
		Items map[string]struct {
			Kind              string   `json:"kind"`
			Endpoints         []string `json:"endpoints"`
			InputFormats      []string `json:"input_formats"`
			MaxInputImages    int      `json:"max_input_images"`
			AsyncTask         bool     `json:"async_task"`
			ResolvedByBuiltin bool     `json:"resolved_by_builtin"`
		} `json:"items"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Items) != 4 {
		t.Fatalf("resolve = %d items", len(out.Items))
	}
	has := func(model, ep string) bool {
		for _, e := range out.Items[model].Endpoints {
			if e == ep {
				return true
			}
		}
		return false
	}
	if !has("grok-imagine-image-edit", "/v1/images/edits") {
		t.Error("grok edit should resolve to /v1/images/edits")
	}
	if !has("gemini-2.5-flash-image", "/v1/chat/completions") {
		t.Error("gemini image should resolve to /v1/chat/completions")
	}
	if !has("gpt-image-2", "/v1/images/generations") {
		t.Error("gpt-image should resolve to both image endpoints")
	}
	if !out.Items["sora-2"].AsyncTask {
		t.Error("sora should be async")
	}
	for name, item := range out.Items {
		if !item.ResolvedByBuiltin {
			t.Errorf("%s should be flagged as a classifier fallback", name)
		}
	}
}

// Auto-tag persists classifier output without touching existing rows.
func TestModelCapabilityAutoTagEndpoint(t *testing.T) {
	upstream := mockOpenAIUpstream(t)
	defer upstream.Close()
	serverURL, _, _ := setupRelay(t, upstream.URL, "openai-compatible")

	put(t, serverURL+"/admin/model-capabilities/gpt-image-2", map[string]any{
		"max_input_images": 1,
	})
	post(t, serverURL+"/admin/model-capabilities/auto-tag", map[string]any{
		"models": []string{"gpt-image-2", "grok-imagine-image-edit"},
	})

	body := get(t, serverURL+"/admin/model-capabilities")
	var list struct {
		Items []struct {
			Model          string `json:"model"`
			Source         string `json:"source"`
			MaxInputImages int    `json:"max_input_images"`
		} `json:"items"`
	}
	if err := json.Unmarshal(body, &list); err != nil {
		t.Fatal(err)
	}
	byModel := map[string]struct {
		Source         string
		MaxInputImages int
	}{}
	for _, item := range list.Items {
		byModel[item.Model] = struct {
			Source         string
			MaxInputImages int
		}{item.Source, item.MaxInputImages}
	}
	if got := byModel["gpt-image-2"]; got.MaxInputImages != 1 || got.Source != "manual" {
		t.Errorf("manual override clobbered: %+v", got)
	}
	if got := byModel["grok-imagine-image-edit"]; got.Source != "discovery" {
		t.Errorf("new model not tagged: %+v", got)
	}
}

func TestModelCapabilityRejectsEmptyEndpoints(t *testing.T) {
	upstream := mockOpenAIUpstream(t)
	defer upstream.Close()
	serverURL, _, _ := setupRelay(t, upstream.URL, "openai-compatible")

	req, _ := http.NewRequest(http.MethodPut,
		serverURL+"/admin/model-capabilities/broken-model",
		strings.NewReader(`{"endpoints":[]}`))
	req.Header.Set("Authorization", "Bearer admin-test")
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("empty endpoints = %d, want 400", resp.StatusCode)
	}
}
