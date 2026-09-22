package proxy

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// ValidateUpstreamMap validates the custom-endpoint / field-mapping columns of a
// channel and canonicalizes the empty forms ("{}", "[]") to "" so the stored row
// has exactly one representation of "no mapping". It is called from the admin
// API on create and update; the engine itself stays fail-open at runtime because
// a row can always be edited directly in SQLite.
//
// Validation is strict on purpose. The failure mode of a typo in a mapping is a
// silent no-op at request time (an absent source is skipped, a bad target is
// logged and dropped), which is indistinguishable from "the upstream rejected
// it" to the operator. Rejecting at save time is the only place the mistake can
// be reported.
func ValidateUpstreamMap(pathOverride, pathMapJSON, requestMapJSON, responseMapJSON string) (string, string, string, string, error) {
	override := strings.Trim(strings.TrimSpace(pathOverride), "/")
	if strings.ContainsAny(override, " \t\r\n") {
		return "", "", "", "", errors.New("upstream_path_override must not contain whitespace")
	}

	pathMap := strings.TrimSpace(pathMapJSON)
	if pathMap == "{}" {
		pathMap = ""
	}
	if pathMap != "" {
		var parsed map[string]string
		if err := json.Unmarshal([]byte(pathMap), &parsed); err != nil {
			return "", "", "", "", errors.New("upstream_path_map must be a JSON object of {\"<openai path>\":\"<upstream path>\"}")
		}
		clean := make(map[string]string, len(parsed))
		keys := make([]string, 0, len(parsed))
		for from, to := range parsed {
			key := strings.Trim(strings.TrimSpace(from), "/")
			target := strings.TrimSpace(to)
			if key == "" {
				return "", "", "", "", errors.New("upstream_path_map has an empty key")
			}
			if target == "" {
				return "", "", "", "", fmt.Errorf("upstream_path_map[%q] has an empty target", from)
			}
			if strings.ContainsAny(target, "?#") {
				return "", "", "", "", fmt.Errorf("upstream_path_map[%q] target must be a path without query or fragment", from)
			}
			clean[key] = target
			keys = append(keys, key)
		}
		// Re-encode with sorted keys so an unchanged mapping round-trips to the
		// same string (diffable rows, stable console display).
		var buf strings.Builder
		buf.WriteString("{")
		sortedKeys := append([]string(nil), keys...)
		sortStrings(sortedKeys)
		for i, key := range sortedKeys {
			if i > 0 {
				buf.WriteString(",")
			}
			encodedKey, _ := json.Marshal(key)
			encodedValue, _ := json.Marshal(clean[key])
			buf.Write(encodedKey)
			buf.WriteString(":")
			buf.Write(encodedValue)
		}
		buf.WriteString("}")
		pathMap = buf.String()
	}

	requestMap, err := validateFieldMaps("upstream_request_map", requestMapJSON)
	if err != nil {
		return "", "", "", "", err
	}
	responseMap, err := validateFieldMaps("upstream_response_map", responseMapJSON)
	if err != nil {
		return "", "", "", "", err
	}
	return override, pathMap, requestMap, responseMap, nil
}

func validateFieldMaps(column, raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "[]" {
		return "", nil
	}
	if trimmed == "" {
		return "", nil
	}
	var entries []UpstreamFieldMap
	if err := json.Unmarshal([]byte(trimmed), &entries); err != nil {
		return "", fmt.Errorf("%s must be a JSON array of field maps", column)
	}
	if len(entries) == 0 {
		return "", nil
	}
	for i, entry := range entries {
		from := strings.TrimSpace(entry.From)
		to := strings.TrimSpace(entry.To)
		switch {
		case from == "" && entry.Value == nil && entry.Template == "":
			return "", fmt.Errorf("%s[%d]: needs a from path, a value or a template", column, i)
		case from != "" && entry.Value != nil:
			return "", fmt.Errorf("%s[%d]: from and value are mutually exclusive", column, i)
		case from != "" && entry.Template != "":
			return "", fmt.Errorf("%s[%d]: from and template are mutually exclusive", column, i)
		case entry.Value != nil && entry.Template != "":
			return "", fmt.Errorf("%s[%d]: value and template are mutually exclusive", column, i)
		}
		if from != "" {
			if err := rejectFramedPath(column+"["+itoa(i)+"].from", from); err != nil {
				return "", err
			}
			if err := ValidateJSONPath(from); err != nil {
				return "", fmt.Errorf("%s[%d].from: %w", column, i, err)
			}
		}
		if to != "" {
			if err := rejectFramedPath(column+"["+itoa(i)+"].to", to); err != nil {
				return "", err
			}
			if err := ValidateJSONPath(to); err != nil {
				return "", fmt.Errorf("%s[%d].to: %w", column, i, err)
			}
		}
		if entry.Template != "" {
			if _, err := TemplateReferences(entry.Template); err != nil {
				return "", fmt.Errorf("%s[%d].template: %w", column, i, err)
			}
		}
		if entry.Value != nil {
			if _, err := entry.Value.ToAny(); err != nil {
				return "", fmt.Errorf("%s[%d].value: %w", column, i, err)
			}
		}
	}
	encoded, err := json.Marshal(entries)
	if err != nil {
		return "", fmt.Errorf("%s could not be normalized", column)
	}
	return string(encoded), nil
}

// ValidatePathOverrideForAdapter is intentionally NOT applied at save time: the
// admin handler has no adapter registry, and a mapping on a non-OpenAI channel
// degrades to "path rewritten, body unchanged", which the operator can see in
// the request log. The console's field hint states that the feature targets
// OpenAI-compatible channels.

func sortStrings(values []string) {
	// Insertion sort over short slices: the key count of a path map is tiny
	// (usually 1-3), so avoiding the sort package keeps this dependency-free.
	for i := 1; i < len(values); i++ {
		for j := i; j > 0 && values[j] < values[j-1]; j-- {
			values[j], values[j-1] = values[j-1], values[j]
		}
	}
}

// itoa is strconv.Itoa without pulling strconv into this file's import list.
func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	negative := value < 0
	if negative {
		value = -value
	}
	var digits []byte
	for value > 0 {
		digits = append(digits, byte('0'+value%10))
		value /= 10
	}
	if negative {
		digits = append(digits, '-')
	}
	for i, j := 0, len(digits)-1; i < j; i, j = i+1, j-1 {
		digits[i], digits[j] = digits[j], digits[i]
	}
	return string(digits)
}
