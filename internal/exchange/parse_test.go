package exchange

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestParseSupportedShapes(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{"canonical", `{"format":"meta-gateway-aah-exchange","version":1,"exported_at":"2026-07-14T00:00:00Z","importable":true,"items":[{"name":"main","base_url":"HTTPS://API.EXAMPLE.COM:443/v1/","api_key":"secret","models":["b","a","a"],"group":"default","priority":0,"weight":100,"site_type_hint":"OpenAI"}]}`},
		{"new-api-array", `[{"name":"main","base_url":"https://api.example.com","key":"secret","models":"b,a","group":"default","priority":0,"weight":100,"status":1,"type":"new-api"}]`},
		{"new-api-wrapper", `{"channels":[{"name":"main","baseUrl":"https://api.example.com","apiKey":"secret"}]}`},
		{"aah-v2", `{"version":"2.0","accounts":[],"channelConfigs":{},"apiCredentialProfiles":{"version":3,"profiles":[{"name":"main","apiType":"openai","baseUrl":"https://api.example.com","apiKey":"secret"}]}}`},
		{"aah-v2-accounts-fallback", `{"version":"2.0","timestamp":1,"accounts":{"accounts":[{"id":"a1","site_name":"WONG","site_url":"https://wzw.pp.ua","site_type":"new-api","disabled":false,"authType":"access_token","account_info":{"id":"1","access_token":"site-secret","username":"u"},"checkIn":{"autoCheckInEnabled":true}}]},"apiCredentialProfiles":{"version":3,"profiles":[],"lastUpdated":1}}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			items, err := Parse([]byte(test.body))
			if err != nil || len(items) != 1 {
				t.Fatalf("items=%+v err=%v", items, err)
			}
			switch test.name {
			case "aah-v2-accounts-fallback":
				if items[0].BaseURL != "https://wzw.pp.ua" {
					t.Fatalf("base URL not normalized: %q", items[0].BaseURL)
				}
				if items[0].APIKey != "site-secret" || items[0].Name != "WONG" {
					t.Fatalf("account fields: %+v", items[0])
				}
				if items[0].CredentialKind != "access_token" {
					t.Fatalf("kind=%q", items[0].CredentialKind)
				}
				if items[0].MetaJSON != `{"platform_user_id":1}` {
					t.Fatalf("meta=%q", items[0].MetaJSON)
				}
				if !items[0].CheckinEnabled {
					t.Fatal("expected checkin enabled")
				}
			default:
				if items[0].BaseURL != "https://api.example.com" && items[0].BaseURL != "https://api.example.com/v1" {
					t.Fatalf("base URL not normalized: %q", items[0].BaseURL)
				}
			}
			if test.name == "canonical" && (len(items[0].Models) != 2 || items[0].Models[0] != "a") {
				t.Fatalf("models not normalized: %+v", items[0].Models)
			}
		})
	}
}

func TestParseRejectsUnsafeOrAmbiguousDocuments(t *testing.T) {
	tests := []string{
		`{"format":"meta-gateway-aah-exchange","version":1,"exported_at":"2026-07-14T00:00:00Z","importable":false,"items":[]}`,
		`{"format":"meta-gateway-aah-exchange","version":1,"exported_at":"2026-07-14T00:00:00Z","importable":true,"items":[],"extra":true}`,
		`{"channels":[],"data":[]}`,
		`[{"name":"main","base_url":"file:///tmp/x","key":"secret"}]`,
		`[{"name":"main","base_url":"https://user@example.com","key":"secret"}]`,
		`[{"name":"one","base_url":"https://example.com","key":"same"},{"name":"two","base_url":"https://example.com/","key":"same"}]`,
		`[{"name":"main","base_url":"https://example.com","key":"secret","priority":"high"}]`,
		`[{"name":"main","base_url":"https://example.com","key":"secret","models":42}]`,
		`[{"name":"main","base_url":"https://example.com","key":"secret","group":"a","groups":"b"}]`,
		`[{"name":"main","base_url":"https://example.com","key":"secret","type":42}]`,
		`[]`,
		`{"version":"2.0","accounts":{"accounts":[]},"apiCredentialProfiles":{"version":3,"profiles":[]}}`,
		`{} trailing`,
	}
	for _, body := range tests {
		if _, err := Parse([]byte(body)); err == nil {
			t.Fatalf("expected rejection: %s", body)
		}
	}
}

func TestParseDuplicateIdentityIsValidationError(t *testing.T) {
	_, err := Parse([]byte(`[{"name":"one","base_url":"https://example.com","key":"same"},{"name":"two","base_url":"https://example.com/","key":"same"}]`))
	var typed *Error
	if !errors.As(err, &typed) || typed.Kind != ErrorValidation {
		t.Fatalf("duplicate identity error=%v, want %s", err, ErrorValidation)
	}
}

func TestEnvelopeNeverSerializesEmptyAPIKey(t *testing.T) {
	data, err := json.Marshal(Envelope{Format: Format, Version: Version, Items: []Item{{Name: "metadata"}}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "api_key") {
		t.Fatalf("empty API key was serialized: %s", data)
	}
	var decoded map[string]any
	_ = json.Unmarshal(data, &decoded)
	item := decoded["items"].([]any)[0].(map[string]any)
	if _, ok := item["api_key"]; ok {
		t.Fatal("empty API key was serialized")
	}
}
