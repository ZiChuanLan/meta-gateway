package siteprobe

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// SourceConfig is the per-site settings blob stored in
// sites.probe_source_config. It exists so a source can be trusted differently
// from site to site: an operator may be happy to let one site's own status page
// park its members automatically while keeping another site's page as
// read-only evidence.
type SourceConfig struct {
	// AutoApply turns "collect and show" into "collect and act": after each
	// round the verdict is evaluated and the routing changes are applied
	// without asking. Off unless an operator turns it on for this site.
	AutoApply bool `json:"auto_apply"`
	// Policy overrides the default thresholds when AutoApply is on.
	Policy Policy `json:"policy"`
}

// ParseSourceConfig decodes a stored config. An empty or malformed value is not
// an error at read time: the collector must keep working on a config blob an
// older build wrote, and the safe interpretation of "unreadable" is "do not
// act" — never "act with defaults".
func ParseSourceConfig(raw string) (SourceConfig, bool) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		// Still normalized: callers compare or re-encode the policy, and a zero
		// policy would read as "threshold 0, no samples" rather than "defaults".
		return SourceConfig{Policy: Policy{}.withDefaults()}, true
	}
	var parsed SourceConfig
	if err := json.Unmarshal([]byte(trimmed), &parsed); err != nil {
		return SourceConfig{}, false
	}
	parsed.Policy = parsed.Policy.withDefaults()
	return parsed, true
}

// ValidateSourceConfig is the write-side gate: it rejects a blob the reader
// would have to interpret. Called by the admin endpoint before storing, so the
// only way a bad config reaches the database is a direct write.
func ValidateSourceConfig(raw string) error {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil
	}
	var probe map[string]json.RawMessage
	if err := json.Unmarshal([]byte(trimmed), &probe); err != nil {
		return errors.New("probe source config must be a JSON object")
	}
	for key := range probe {
		if key != "auto_apply" && key != "policy" {
			return fmt.Errorf("unknown probe source config key %q", key)
		}
	}
	var parsed SourceConfig
	decoder := json.NewDecoder(strings.NewReader(trimmed))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&parsed); err != nil {
		return fmt.Errorf("probe source config: %w", err)
	}
	policy := parsed.Policy
	if policy.RatioThreshold < 0 || policy.RatioThreshold > 1 {
		return errors.New("policy.ratio_threshold must be between 0 and 1")
	}
	if policy.MinSamples < 0 || policy.MinSamples > 1000 {
		return errors.New("policy.min_samples must be between 0 and 1000")
	}
	if policy.LowRounds < 0 || policy.LowRounds > 10 {
		return errors.New("policy.low_rounds must be between 0 and 10")
	}
	if policy.HighRounds < 0 || policy.HighRounds > 10 {
		return errors.New("policy.high_rounds must be between 0 and 10")
	}
	return nil
}

// EncodeSourceConfig renders a config for storage, with every value filled in so
// what is stored is exactly what the collector will use.
func EncodeSourceConfig(config SourceConfig) (string, error) {
	config.Policy = config.Policy.withDefaults()
	encoded, err := json.Marshal(config)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}
