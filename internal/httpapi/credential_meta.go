package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"strings"

	"github.com/lan/meta-gateway/internal/adapters"
)

// normalizeCredentialMeta validates and canonicalizes a credential meta_json
// document sent by the admin API.
//
// Only keys the gateway interprets are normalized; every other key is carried
// through verbatim so store-owned fields (external check-in checkin_path /
// checkin_method / headers, api-key name / group / upstream_token_id) survive a
// frontend round-trip. `platform_user_id` is rewritten as a bare JSON number
// because the New-API family user-id headers are derived from it, and imports
// may carry it as a quoted string.
//
// Empty input (or a document whose only key was an explicit null
// platform_user_id) returns "" — "no metadata".
func normalizeCredentialMeta(raw string) (string, error) {
	if strings.TrimSpace(raw) == "" {
		return "", nil
	}
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.UseNumber()
	var fields map[string]json.RawMessage
	if err := decoder.Decode(&fields); err != nil || fields == nil {
		return "", errors.New("meta_json must be a JSON object")
	}
	// Reject trailing garbage after the object.
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return "", errors.New("meta_json must be a JSON object")
	}
	if rawUserID, ok := fields["platform_user_id"]; ok {
		var value any
		if err := json.Unmarshal(rawUserID, &value); err != nil {
			return "", errors.New("platform_user_id must be a positive integer")
		}
		if value == nil {
			delete(fields, "platform_user_id")
		} else {
			id, ok := adapters.CoercePlatformUserID(value)
			if !ok {
				return "", errors.New("platform_user_id must be a positive integer")
			}
			fields["platform_user_id"] = json.RawMessage(strconv.FormatInt(id, 10))
		}
	}
	if len(fields) == 0 {
		return "", nil
	}
	encoded, err := json.Marshal(fields)
	if err != nil {
		return "", errors.New("meta_json must be a JSON object")
	}
	return string(encoded), nil
}
