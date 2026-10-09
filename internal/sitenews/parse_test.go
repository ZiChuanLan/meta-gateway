package sitenews

import (
	"encoding/json"
	"testing"

	"github.com/lan/meta-gateway/internal/domain"
)

func siteFor(platform, base string) domain.Site {
	return domain.Site{ID: 1, Name: "site", BaseURL: base, Platform: platform}
}

// The layouts the fleet actually emits, and the ones that must not be guessed
// at: a wrong guess moves an old announcement to the top of the feed.
func TestParsePublished(t *testing.T) {
	cases := []struct {
		name  string
		value string
		want  string
	}{
		{"newapi milliseconds", "2026-10-08T14:22:58.312Z", "2026-10-08T14:22:58.312Z"},
		{"plain rfc3339", "2026-10-08T14:22:58Z", "2026-10-08T14:22:58Z"},
		{"offset folded to utc", "2026-10-08T22:22:58+08:00", "2026-10-08T14:22:58Z"},
		{"database timestamp", "2026-10-08 14:22:58", "2026-10-08T14:22:58Z"},
		{"no timezone", "2026-10-08T14:22:58", "2026-10-08T14:22:58Z"},
		{"garbage", "whenever", ""},
		{"empty", "   ", ""},
	}
	for _, tc := range cases {
		if got := parsePublished(tc.value); got != tc.want {
			t.Errorf("%s: parsePublished(%q) = %q, want %q", tc.name, tc.value, got, tc.want)
		}
	}
}

// The site's own id arrives as a number on the forks measured; a string or a
// missing field must not produce the literal "null" as an id, which would then
// be the key every row collides on.
func TestAnnouncementID(t *testing.T) {
	cases := map[string]string{
		"42":     "42",
		`"42"`:   "42",
		`" 42 "`: "42",
		"null":   "",
		`""`:     "",
		"":       "",
	}
	for raw, want := range cases {
		if got := announcementID(json.RawMessage(raw)); got != want {
			t.Errorf("announcementID(%s) = %q, want %q", raw, got, want)
		}
	}
}

// The board URL is derived from the platform, never typed by an operator.
func TestBoardURLEligibility(t *testing.T) {
	tests := []struct {
		platform string
		base     string
		want     string
	}{
		{"new-api", "https://x666.me", "https://x666.me/api/status"},
		{"new-api", "https://x666.me/", "https://x666.me/api/status"},
		{"one-api", "https://one.example", "https://one.example/api/status"},
		{"new-api", "https://sub.example/v1", "https://sub.example/v1/api/status"},
		// A board URL that was already pasted in (the probe source does this)
		// resolves back to the site root rather than doubling the path.
		{"new-api", "https://x666.me/api/status", "https://x666.me/api/status"},
		// Platforms whose public API is not New-API's are left alone.
		{"sub2api", "https://free.hiyo.top", ""},
		{"anyrouter", "https://anyrouter.top", ""},
		{"openai-compatible", "https://wb.example", ""},
		{"", "https://unknown.example", ""},
	}
	for _, tc := range tests {
		got, _, ok := boardURL(siteFor(tc.platform, tc.base))
		if tc.want == "" {
			if ok {
				t.Errorf("%s/%s: expected no board, got %s", tc.platform, tc.base, got)
			}
			continue
		}
		if !ok || got != tc.want {
			t.Errorf("%s/%s: boardURL = %q (ok=%v), want %q", tc.platform, tc.base, got, ok, tc.want)
		}
	}
}
