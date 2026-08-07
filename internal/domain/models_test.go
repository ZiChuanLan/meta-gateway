package domain

import (
	"encoding/json"
	"testing"
)

func TestParseRetryConfigCompilesRegexes(t *testing.T) {
	cfg := ParseRetryConfig(`{
		"status_codes": [400, 418],
		"error_patterns": [
			{"pattern": "rate limit", "regex": true},
			{"pattern": "plain substring"},
			{"pattern": "ov[ae]rloaded", "regex": true},
			{"pattern": "(unclosed", "regex": true}
		]
	}`)
	if len(cfg.StatusCodes) != 2 || cfg.StatusCodes[0] != 400 || cfg.StatusCodes[1] != 418 {
		t.Fatalf("status codes not parsed: %+v", cfg.StatusCodes)
	}
	compiled := cfg.CompiledPatterns()
	if len(compiled) != 4 {
		t.Fatalf("compiled length=%d, want 4", len(compiled))
	}
	if compiled[0] == nil || !compiled[0].MatchString("you hit a rate limit") {
		t.Fatal("regex pattern 0 must be compiled and match")
	}
	if compiled[1] != nil {
		t.Fatal("substring pattern must have nil compiled entry")
	}
	if compiled[2] == nil || !compiled[2].MatchString("provider overloaded") {
		t.Fatal("regex pattern 2 must be compiled and match")
	}
	if compiled[3] != nil {
		t.Fatal("invalid regex must compile to nil, not fail parsing")
	}
}

func TestParseRetryConfigEmptyAndMalformed(t *testing.T) {
	if cfg := ParseRetryConfig(""); len(cfg.StatusCodes) != 0 || len(cfg.CompiledPatterns()) != 0 {
		t.Fatalf("empty config must be zero-valued, got %+v", cfg)
	}
	if cfg := ParseRetryConfig(`{not json`); len(cfg.StatusCodes) != 0 || len(cfg.CompiledPatterns()) != 0 {
		t.Fatalf("malformed config must be zero-valued, got %+v", cfg)
	}
}

func TestRetryConfigJSONRoundTripIgnoresCompiled(t *testing.T) {
	raw := `{"status_codes":[429],"error_patterns":[{"pattern":"busy","regex":true}]}`
	cfg := ParseRetryConfig(raw)
	reEncoded, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	// The compiled cache must never leak into JSON.
	if string(reEncoded) != raw {
		t.Fatalf("round-trip mismatch: got %s, want %s", reEncoded, raw)
	}
}
