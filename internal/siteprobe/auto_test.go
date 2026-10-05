package siteprobe

import (
	"testing"
)

// The auto source is the reason an operator never types a probe URL for the
// common platforms: the site row already knows both facts the derivation needs.
func TestAutoSourceDerivesFromPlatform(t *testing.T) {
	cases := []struct {
		platform string
		baseURL  string
		kind     string
		url      string
		ok       bool
	}{
		{"new-api", "https://a.example/", SourceNewAPI, "https://a.example", true},
		{"new-api", "https://b.example/api/v1", SourceNewAPI, "https://b.example/api/v1", true},
		{"one-api", "https://c.example", SourceNewAPI, "https://c.example", true},
		{"sub2api", "https://d.example", SourceSub2APITransit, "https://d.example", true},
		{"openai-compatible", "https://e.example", "", "", false},
		{"new-api", "  ", "", "", false},
	}
	for _, testCase := range cases {
		kind, url, ok := AutoSource(testCase.platform, testCase.baseURL)
		if ok != testCase.ok || kind != testCase.kind || url != testCase.url {
			t.Errorf("AutoSource(%q, %q) = %q/%q ok=%v, want %q/%q ok=%v",
				testCase.platform, testCase.baseURL, kind, url, ok, testCase.kind, testCase.url, testCase.ok)
		}
	}
}

// A custom source beats the derivation: an operator who typed a URL is telling
// us the platform default is wrong for that site (a Kuma page on another host).
func TestResolveProbeSourceKeepsCustomURL(t *testing.T) {
	site := domainSite("new-api", "https://a.example", "uptime_kuma", "https://stat.example.com/status/ai", false)
	resolved := resolveProbeSource(site)
	if resolved.ProbeSourceKind != "uptime_kuma" || resolved.ProbeSourceURL != "https://stat.example.com/status/ai" {
		t.Fatalf("custom source overwritten: %+v", resolved)
	}
}

func TestResolveProbeSourceAutoFillsKindAndURL(t *testing.T) {
	site := domainSite("new-api", "https://a.example/", "", "", true)
	resolved := resolveProbeSource(site)
	if resolved.ProbeSourceKind != SourceNewAPI || resolved.ProbeSourceURL != "https://a.example" {
		t.Fatalf("auto source = %q/%q", resolved.ProbeSourceKind, resolved.ProbeSourceURL)
	}
	// Auto off with nothing configured stays unresolvable: the collector must
	// skip it, not guess.
	manual := domainSite("new-api", "https://a.example", "", "", false)
	if resolved := resolveProbeSource(manual); resolved.ProbeSourceURL != "" {
		t.Fatalf("auto-off site resolved to %q", resolved.ProbeSourceURL)
	}
	// An unknown platform with auto on resolves to nothing and is skipped.
	unknown := domainSite("openai-compatible", "https://e.example", "", "", true)
	if resolved := resolveProbeSource(unknown); resolved.ProbeSourceKind != "" {
		t.Fatalf("unknown platform resolved to %q", resolved.ProbeSourceKind)
	}
}
