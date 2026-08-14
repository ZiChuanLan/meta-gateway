package httpapi_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestModelMetadataEndpoints(t *testing.T) {
	upstream := mockOpenAIUpstream(t)
	defer upstream.Close()
	serverURL, _, _ := setupRelay(t, upstream.URL, "openai-compatible")

	// Empty library.
	body := get(t, serverURL+"/admin/model-metadata")
	var list struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(body, &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Items) != 0 {
		t.Fatalf("fresh library = %d", len(list.Items))
	}

	// Upsert (create).
	put(t, serverURL+"/admin/model-metadata/gemini-2.5-flash", map[string]any{
		"context_window":    1000000,
		"input_modalities":  "text,image",
		"output_modalities": "text",
		"supports_thinking": 1,
		"vendor":            "Google",
	})
	body = get(t, serverURL+"/admin/model-metadata")
	json.Unmarshal(body, &list)
	if len(list.Items) != 1 {
		t.Fatalf("after upsert = %d rows", len(list.Items))
	}
	row := list.Items[0]
	if row["model_name"] != "gemini-2.5-flash" || row["vendor"] != "Google" {
		t.Fatalf("row = %+v", row)
	}

	// Upsert (update same row).
	put(t, serverURL+"/admin/model-metadata/gemini-2.5-flash", map[string]any{
		"context_window":    2000000,
		"supports_thinking": 0,
		"vendor":            "Google DeepMind",
	})
	body = get(t, serverURL+"/admin/model-metadata")
	json.Unmarshal(body, &list)
	if len(list.Items) != 1 {
		t.Fatalf("update duplicated rows: %d", len(list.Items))
	}
	if list.Items[0]["context_window"].(float64) != 2000000 {
		t.Fatalf("update not applied: %+v", list.Items[0])
	}

	// Slash-bearing model names survive URL round-trips unescaped.
	put(t, serverURL+"/admin/model-metadata/deepseek-ai%2Fdeepseek-v4-flash", map[string]any{
		"context_window": 128000,
		"vendor":         "DeepSeek",
	})
	body = get(t, serverURL+"/admin/model-metadata")
	json.Unmarshal(body, &list)
	if len(list.Items) != 2 {
		t.Fatalf("after slash upsert = %d rows", len(list.Items))
	}
	var slashRow map[string]any
	for _, item := range list.Items {
		if item["model_name"] == "deepseek-ai/deepseek-v4-flash" {
			slashRow = item
		}
	}
	if slashRow == nil {
		t.Fatalf("slash model stored unescaped: %+v", list.Items)
	}
	// Delete the slash row (encoded on the way out, decoded by the handler).
	delReq2, _ := http.NewRequest(http.MethodDelete, serverURL+"/admin/model-metadata/deepseek-ai%2Fdeepseek-v4-flash", nil)
	delReq2.Header.Set("Authorization", "Bearer admin-test")
	delResp2, err := http.DefaultClient.Do(delReq2)
	if err != nil {
		t.Fatal(err)
	}
	delResp2.Body.Close()
	if delResp2.StatusCode != http.StatusOK {
		t.Fatalf("slash delete status = %d", delResp2.StatusCode)
	}

	// Reject invalid thinking value.
	badReq, _ := http.NewRequest(http.MethodPut, serverURL+"/admin/model-metadata/gemini-2.5-flash",
		strings.NewReader(`{"supports_thinking":7}`))
	badReq.Header.Set("Authorization", "Bearer admin-test")
	badReq.Header.Set("Content-Type", "application/json")
	badResp, err := http.DefaultClient.Do(badReq)
	if err != nil {
		t.Fatal(err)
	}
	badResp.Body.Close()
	if badResp.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid thinking status = %d, want 400", badResp.StatusCode)
	}

	// Delete.
	delReq, _ := http.NewRequest(http.MethodDelete, serverURL+"/admin/model-metadata/gemini-2.5-flash", nil)
	delReq.Header.Set("Authorization", "Bearer admin-test")
	delResp, err := http.DefaultClient.Do(delReq)
	if err != nil {
		t.Fatal(err)
	}
	delResp.Body.Close()
	if delResp.StatusCode != http.StatusOK {
		t.Fatalf("delete status = %d", delResp.StatusCode)
	}
	body = get(t, serverURL+"/admin/model-metadata")
	json.Unmarshal(body, &list)
	if len(list.Items) != 0 {
		t.Fatalf("after delete = %d rows", len(list.Items))
	}
}
