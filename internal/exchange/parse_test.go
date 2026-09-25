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
		{"aah-v2-profiles-and-accounts", `{"version":"2.0","apiCredentialProfiles":{"version":3,"profiles":[{"name":"main","apiType":"openai","baseUrl":"https://api.example.com","apiKey":"secret"}]},"accounts":{"accounts":[{"id":"a1","site_name":"WONG","site_url":"https://wzw.pp.ua","site_type":"new-api","disabled":false,"authType":"access_token","account_info":{"id":"1","access_token":"site-secret","username":"u"},"checkIn":{"autoCheckInEnabled":true}}]}}`},
		{"aah-v9-future", `{"version":"9.9","apiCredentialProfiles":{"version":3,"profiles":[{"name":"main","apiType":"openai","baseUrl":"https://api.example.com","apiKey":"secret"}]},"accounts":{"accounts":[{"id":"a1","site_name":"WONG","site_url":"https://wzw.pp.ua","site_type":"new-api","disabled":false,"authType":"access_token","account_info":{"id":"1","access_token":"site-secret","username":"u"},"checkIn":{"autoCheckInEnabled":true}}]}}`},
		{"aah-v4-full-state", `{"version":"4.0","timestamp":1788000000,"accounts":{"accounts":[{"id":"a1","site_name":"WONG","site_url":"https://wzw.pp.ua","site_type":"new-api","disabled":false,"authType":"access_token","account_info":{"id":"1","access_token":"site-secret","username":"u"},"checkIn":{"autoCheckInEnabled":true}}],"bookmarks":[],"pinnedAccountIds":[]},"apiCredentialProfiles":{"version":3,"profiles":[{"name":"main","apiType":"openai","baseUrl":"https://api.example.com","apiKey":"secret"}],"links":{}},"channelConfigs":{"schemaVersion":2,"configs":{}},"preferences":{"themeMode":"dark"},"tagStore":{"tagsById":{},"version":1}}`},
		{"aah-v3", `{"version":"3.0","apiCredentialProfiles":{"version":3,"profiles":[{"name":"main","apiType":"openai","baseUrl":"https://api.example.com","apiKey":"secret"}]}}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			items, err := Parse([]byte(test.body))
			if err != nil || len(items) == 0 {
				t.Fatalf("items=%+v err=%v", items, err)
			}
			switch test.name {
			case "aah-v2-accounts-fallback":
				if len(items) != 1 {
					t.Fatalf("expected 1 item, got %d", len(items))
				}
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
			case "aah-v2-profiles-and-accounts", "aah-v9-future", "aah-v4-full-state":
				if len(items) != 2 {
					t.Fatalf("expected 2 items (profile + account), got %d: %+v", len(items), items)
				}
				profile := items[0]
				account := items[1]
				if profile.Name != "main" || profile.APIKey != "secret" || profile.CredentialKind != "api_key" {
					t.Fatalf("profile fields: %+v", profile)
				}
				if account.Name != "WONG" || account.APIKey != "site-secret" || account.CredentialKind != "access_token" {
					t.Fatalf("account fields: %+v", account)
				}
				if !account.CheckinEnabled || account.MetaJSON != `{"platform_user_id":1}` {
					t.Fatalf("account checkin fields: %+v", account)
				}
			default:
				if len(items) != 1 {
					t.Fatalf("expected 1 item, got %d", len(items))
				}
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
		`[{"name":"main","base_url":"https://example.com","key":"secret","priority":"high"}]`,
		`[{"name":"main","base_url":"https://example.com","key":"secret","models":42}]`,
		`[{"name":"main","base_url":"https://example.com","key":"secret","group":"a","groups":"b"}]`,
		`[{"name":"main","base_url":"https://example.com","key":"secret","type":42}]`,
		`[{"name":"main","base_url":"https://example.com"}]`,
		`[]`,
		`{"version":"2.0","accounts":{"accounts":[]},"apiCredentialProfiles":{"version":3,"profiles":[]}}`,
		`{"version":2,"accounts":{"accounts":[{"id":"a1","site_name":"X","site_url":"https://x.example.com"}]}}`,
		`{} trailing`,
	}
	for _, body := range tests {
		if _, err := Parse([]byte(body)); err == nil {
			t.Fatalf("expected rejection: %s", body)
		}
	}
}

// A single unusable row must never take the whole document down: real AAH
// backups routinely carry an account that is in cookie auth mode or has no
// token yet, and rejecting the file for it made every good row invisible.
func TestParseSkipsUnusableRowsInsteadOfFailing(t *testing.T) {
	body := `{"version":"4.0","timestamp":1,` +
		`"apiCredentialProfiles":{"version":3,"profiles":[` +
		`{"name":"main","apiType":"openai","baseUrl":"https://api.example.com","apiKey":"secret"}]},` +
		`"accounts":{"accounts":[` +
		`{"id":"ok","site_name":"Good","site_url":"https://good.example.com","authType":"access_token","account_info":{"id":"1","access_token":"tok"}},` +
		`{"id":"cookie","site_name":"CookieOnly","site_url":"https://cookie.example.com","authType":"cookie","account_info":{"id":"2","access_token":"","username":"u"}},` +
		`{"id":"notoken","site_name":"NoToken","site_url":"https://notoken.example.com","account_info":{"id":"3"}}]}}`
	report, err := ParseWithReport([]byte(body))
	if err != nil {
		t.Fatalf("tolerant parse failed: %v", err)
	}
	if len(report.Items) != 2 {
		t.Fatalf("expected 2 importable rows, got %d: %+v", len(report.Items), report.Items)
	}
	if len(report.Skipped) != 2 {
		t.Fatalf("expected 2 skipped rows, got %+v", report.Skipped)
	}
	for _, item := range report.Skipped {
		if item.Reason != SkipMissingCredential {
			t.Fatalf("reason=%q want %q", item.Reason, SkipMissingCredential)
		}
		if item.Name == "" {
			t.Fatal("skipped row should keep its name for the report")
		}
	}
}

// A recognized backup whose every row is unusable reports "no importable
// entries" rather than "unsupported format".
func TestParseReportsNoEntriesForAllSkippedDocument(t *testing.T) {
	body := `{"version":"4.0","apiCredentialProfiles":{"version":3,"profiles":[` +
		`{"name":"main","apiType":"openai","baseUrl":"https://api.example.com","apiKey":""}]}}`
	_, err := Parse([]byte(body))
	var typed *Error
	if !errors.As(err, &typed) || typed.Kind != ErrorNoEntries {
		t.Fatalf("err=%v want %s", err, ErrorNoEntries)
	}
}

// An AAH backup that carries no credential section at all is still an AAH
// backup: it must not be reported as an unknown format.
func TestParseCredentiallessAahBackupReportsNoEntries(t *testing.T) {
	body := `{"version":"4.0","timestamp":1,"channelConfigs":{"schemaVersion":2,"configs":{}},` +
		`"preferences":{"themeMode":"dark"},"tagStore":{"tagsById":{},"version":1}}`
	_, err := Parse([]byte(body))
	var typed *Error
	if !errors.As(err, &typed) || typed.Kind != ErrorNoEntries {
		t.Fatalf("err=%v want %s", err, ErrorNoEntries)
	}
}

// Our own export writes `skipped` whenever a channel had no credential, so the
// parser must accept the field or the gateway cannot read its own backup.
func TestParseAcceptsOwnExportWithSkippedSection(t *testing.T) {
	body := `{"format":"meta-gateway-aah-exchange","version":1,"exported_at":"2026-07-14T00:00:00Z",` +
		`"importable":true,"items":[{"name":"main","base_url":"https://api.example.com","api_key":"secret",` +
		`"models":[],"group":"default","priority":0,"weight":100,"site_type_hint":"openai-compatible"}],` +
		`"skipped":[{"channel_id":7,"name":"no-key","reason":"no_credential"}]}`
	items, err := Parse([]byte(body))
	if err != nil || len(items) != 1 {
		t.Fatalf("items=%+v err=%v", items, err)
	}
}

// A secrets-less export is well formed but carries nothing importable: our own
// envelope says so via importable=false. That must read as "no credentials",
// not as a corrupt document.
func TestParseCredentiallessOwnExportReportsNoEntries(t *testing.T) {
	body := `{"format":"meta-gateway-aah-exchange","version":1,` +
		`"exported_at":"2026-07-14T00:00:00Z","importable":false,` +
		`"items":[{"name":"main","base_url":"https://api.example.com",` +
		`"models":["gpt-4"],"group":"default","priority":0,"weight":100,` +
		`"site_type_hint":"openai-compatible"}]}`
	_, err := Parse([]byte(body))
	var typed *Error
	if !errors.As(err, &typed) || typed.Kind != ErrorNoEntries {
		t.Fatalf("err=%v want %s", err, ErrorNoEntries)
	}
}

// Duplicate identities keep the first row and report the rest.
func TestParseDuplicateIdentityIsSkippedNotFatal(t *testing.T) {
	report, err := ParseWithReport([]byte(`[{"name":"one","base_url":"https://example.com","key":"same"},{"name":"two","base_url":"https://example.com/","key":"same"}]`))
	if err != nil {
		t.Fatalf("tolerant parse failed: %v", err)
	}
	if len(report.Items) != 1 || len(report.Skipped) != 1 {
		t.Fatalf("items=%d skipped=%+v", len(report.Items), report.Skipped)
	}
	if report.Skipped[0].Reason != SkipDuplicateIdentity {
		t.Fatalf("reason=%q want %q", report.Skipped[0].Reason, SkipDuplicateIdentity)
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

// AAH legacy scoped snapshots nest their sections under `data`; those rows are
// importable too.
func TestParseNestedDataAahSnapshot(t *testing.T) {
	body := `{"version":"4.0","timestamp":1,"data":{"accounts":{"accounts":[` +
		`{"id":"a1","site_name":"Nested","site_url":"https://nested.example.com","authType":"access_token",` +
		`"account_info":{"id":"1","access_token":"tok"}}]}}}`
	items, err := Parse([]byte(body))
	if err != nil || len(items) != 1 {
		t.Fatalf("items=%+v err=%v", items, err)
	}
	if items[0].Name != "Nested" || items[0].BaseURL != "https://nested.example.com" {
		t.Fatalf("item=%+v", items[0])
	}
}

// The check-in switch is part of our own document now: it must read back, and a
// file written before the field existed must still parse (absent = off).
func TestParseCanonicalCarriesCheckinFlag(t *testing.T) {
	body := `{"format":"meta-gateway-aah-exchange","version":1,"exported_at":"2026-07-14T00:00:00Z","importable":true,` +
		`"items":[{"name":"main","base_url":"https://api.example.com","api_key":"secret","models":[],"group":"default",` +
		`"priority":0,"weight":100,"site_type_hint":"openai-compatible","checkin_enabled":true}]}`
	items, err := Parse([]byte(body))
	if err != nil || len(items) != 1 {
		t.Fatalf("items=%+v err=%v", items, err)
	}
	if !items[0].CheckinEnabled {
		t.Fatal("checkin_enabled was dropped")
	}

	legacy := strings.Replace(body, `,"checkin_enabled":true`, "", 1)
	items, err = Parse([]byte(legacy))
	if err != nil || len(items) != 1 {
		t.Fatalf("legacy items=%+v err=%v", items, err)
	}
	if items[0].CheckinEnabled {
		t.Fatal("an absent checkin_enabled must mean off")
	}
}
