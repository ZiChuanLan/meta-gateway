package exchange

import (
	"bytes"
	"encoding/json"
	"io"
	"net"
	"net/url"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/lan/meta-gateway/internal/adapters"
	"github.com/lan/meta-gateway/internal/domain"
)

func Parse(data []byte) ([]Item, error) {
	var root json.RawMessage
	if err := decodeStrict(data, &root); err != nil {
		return nil, formatError(ErrorValidation)
	}
	trimmed := bytes.TrimSpace(root)
	if len(trimmed) == 0 {
		return nil, formatError(ErrorValidation)
	}

	var items []Item
	var err error
	switch trimmed[0] {
	case '[':
		items, err = parseNewAPIList(trimmed)
	case '{':
		items, err = parseObject(trimmed)
	default:
		return nil, formatError(ErrorUnsupported)
	}
	if err != nil {
		return nil, err
	}
	return normalizeItems(items)
}

func parseObject(data []byte) ([]Item, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return nil, formatError(ErrorValidation)
	}
	_, canonical := fields["format"]
	_, channels := fields["channels"]
	_, listData := fields["data"]
	_, profiles := fields["apiCredentialProfiles"]
	_, accounts := fields["accounts"]
	if canonical {
		if channels || listData || profiles || accounts {
			return nil, formatError(ErrorUnsupported)
		}
		return parseCanonical(data)
	}
	// All API Hub V2 backups always declare version "2.0". Credentials may live in
	// apiCredentialProfiles.profiles (API keys) or accounts[].account_info.access_token
	// (site session tokens). Prefer profiles when non-empty; otherwise fall back.
	if isAAHV2Document(fields) {
		if channels || listData {
			return nil, formatError(ErrorUnsupported)
		}
		return parseAAHV2(fields)
	}
	shapeCount := 0
	if channels {
		shapeCount++
	}
	if listData {
		shapeCount++
	}
	if shapeCount != 1 {
		return nil, formatError(ErrorUnsupported)
	}
	if channels {
		return parseNewAPIList(fields["channels"])
	}
	return parseNewAPIList(fields["data"])
}

func isAAHV2Document(fields map[string]json.RawMessage) bool {
	raw, ok := fields["version"]
	if !ok {
		return false
	}
	var version string
	if json.Unmarshal(raw, &version) != nil || version != "2.0" {
		return false
	}
	_, hasProfiles := fields["apiCredentialProfiles"]
	_, hasAccounts := fields["accounts"]
	return hasProfiles || hasAccounts
}

func parseCanonical(data []byte) ([]Item, error) {
	type canonicalItem struct {
		Name         *string   `json:"name"`
		BaseURL      *string   `json:"base_url"`
		APIKey       *string   `json:"api_key"`
		Models       *[]string `json:"models"`
		Group        *string   `json:"group"`
		Priority     *int      `json:"priority"`
		Weight       *int      `json:"weight"`
		SiteTypeHint *string   `json:"site_type_hint"`
	}
	type canonicalEnvelope struct {
		Format     *string          `json:"format"`
		Version    *int             `json:"version"`
		ExportedAt *string          `json:"exported_at"`
		Importable *bool            `json:"importable"`
		Items      *[]canonicalItem `json:"items"`
	}
	var envelope canonicalEnvelope
	if err := decodeStrict(data, &envelope); err != nil || envelope.Format == nil ||
		envelope.Version == nil || envelope.ExportedAt == nil || envelope.Importable == nil ||
		envelope.Items == nil {
		return nil, formatError(ErrorValidation)
	}
	if *envelope.Format != Format || *envelope.Version != Version {
		return nil, formatError(ErrorUnsupported)
	}
	if !*envelope.Importable {
		return nil, formatError(ErrorValidation)
	}
	if _, err := time.Parse(time.RFC3339, *envelope.ExportedAt); err != nil {
		return nil, formatError(ErrorValidation)
	}
	items := make([]Item, 0, len(*envelope.Items))
	for _, raw := range *envelope.Items {
		if raw.Name == nil || raw.BaseURL == nil || raw.APIKey == nil || raw.Models == nil ||
			raw.Group == nil || raw.Priority == nil || raw.Weight == nil || raw.SiteTypeHint == nil {
			return nil, formatError(ErrorValidation)
		}
		items = append(items, Item{Name: *raw.Name, BaseURL: *raw.BaseURL, APIKey: *raw.APIKey,
			Models: *raw.Models, Group: *raw.Group, Priority: *raw.Priority,
			Weight: *raw.Weight, SiteTypeHint: *raw.SiteTypeHint})
	}
	return items, nil
}

func parseNewAPIList(data []byte) ([]Item, error) {
	var records []map[string]json.RawMessage
	if err := json.Unmarshal(data, &records); err != nil || records == nil {
		return nil, formatError(ErrorValidation)
	}
	items := make([]Item, 0, len(records))
	for _, record := range records {
		name, ok := stringAlias(record, "name")
		if !ok {
			return nil, formatError(ErrorValidation)
		}
		baseURL, ok := stringAlias(record, "base_url", "baseUrl")
		if !ok {
			return nil, formatError(ErrorValidation)
		}
		key, ok := stringAlias(record, "key", "api_key", "apiKey")
		if !ok {
			return nil, formatError(ErrorValidation)
		}
		models, ok := listAlias(record, "models")
		if !ok {
			if hasAlias(record, "models") {
				return nil, formatError(ErrorValidation)
			}
			models = []string{}
		}
		groups, ok := listAlias(record, "group", "groups")
		if !ok {
			if hasAlias(record, "group", "groups") {
				return nil, formatError(ErrorValidation)
			}
			groups = []string{"default"}
		} else if len(groups) == 0 {
			groups = []string{"default"}
		}
		priority, ok := intAlias(record, "priority")
		if !ok {
			if hasAlias(record, "priority") {
				return nil, formatError(ErrorValidation)
			}
			priority = 0
		}
		weight, ok := intAlias(record, "weight")
		if !ok {
			if hasAlias(record, "weight") {
				return nil, formatError(ErrorValidation)
			}
			weight = 100
		}
		typeHint, typeOK := stringAlias(record, "type", "type_hint", "typeHint", "site_type_hint", "siteTypeHint")
		if !typeOK && hasAlias(record, "type", "type_hint", "typeHint", "site_type_hint", "siteTypeHint") {
			return nil, formatError(ErrorValidation)
		}
		status, err := newAPIStatus(record["status"])
		if err != nil {
			return nil, err
		}
		items = append(items, Item{Name: name, BaseURL: baseURL, APIKey: key,
			Models: models, Group: strings.Join(groups, ","), Priority: priority,
			Weight: weight, SiteTypeHint: typeHint, Status: status})
	}
	return items, nil
}

func parseAAHV2(fields map[string]json.RawMessage) ([]Item, error) {
	var version string
	if raw, ok := fields["version"]; !ok || json.Unmarshal(raw, &version) != nil || version != "2.0" {
		return nil, formatError(ErrorUnsupported)
	}

	// Collect from BOTH profiles and accounts when both exist.
	// Profiles carry api_key entries; accounts carry access_token/session entries
	// for check-in. Silently dropping one leaks data, especially in replace mode.
	var combined []Item

	if raw, ok := fields["apiCredentialProfiles"]; ok {
		var container struct {
			Profiles []map[string]json.RawMessage `json:"profiles"`
		}
		if err := json.Unmarshal(raw, &container); err != nil {
			return nil, formatError(ErrorValidation)
		}
		if container.Profiles != nil && len(container.Profiles) > 0 {
			for _, profile := range container.Profiles {
				item, err := parseAAHProfile(profile)
				if err != nil {
					return nil, err
				}
				combined = append(combined, item)
			}
		}
	}

	if raw, ok := fields["accounts"]; ok {
		items, err := parseAAHAccounts(raw)
		if err != nil {
			return nil, err
		}
		combined = append(combined, items...)
	}

	if len(combined) == 0 {
		return nil, formatError(ErrorValidation)
	}
	return combined, nil
}

func parseAAHProfile(profile map[string]json.RawMessage) (Item, error) {
	name, nameOK := stringAlias(profile, "name")
	baseURL, urlOK := stringAlias(profile, "baseUrl", "base_url")
	key, keyOK := stringAlias(profile, "apiKey", "api_key")
	if !nameOK || !urlOK || !keyOK {
		return Item{}, formatError(ErrorValidation)
	}
	typeHint, typeOK := stringAlias(profile, "apiType")
	if !typeOK && hasAlias(profile, "apiType") {
		return Item{}, formatError(ErrorValidation)
	}
	return Item{
		Name: name, BaseURL: baseURL, APIKey: key,
		Models: []string{}, Group: "default", Priority: 0, Weight: 100,
		SiteTypeHint: typeHint, Status: domain.StatusEnabled,
		CredentialKind: "api_key",
	}, nil
}

func parseAAHAccounts(raw json.RawMessage) ([]Item, error) {
	// accounts may be a list or { "accounts": [...] } container from AAH V2.
	var asList []map[string]json.RawMessage
	if err := json.Unmarshal(raw, &asList); err == nil && asList != nil {
		return parseAAHAccountRecords(asList)
	}
	var container struct {
		Accounts []map[string]json.RawMessage `json:"accounts"`
	}
	if err := json.Unmarshal(raw, &container); err != nil || container.Accounts == nil {
		return nil, formatError(ErrorValidation)
	}
	return parseAAHAccountRecords(container.Accounts)
}

func parseAAHAccountRecords(records []map[string]json.RawMessage) ([]Item, error) {
	items := make([]Item, 0, len(records))
	for _, record := range records {
		// Skip explicitly disabled sites; they should not become relay channels.
		if disabled, ok := boolAlias(record, "disabled"); ok && disabled {
			continue
		}
		name, nameOK := stringAlias(record, "site_name", "name")
		baseURL, urlOK := stringAlias(record, "site_url", "baseUrl", "base_url")
		if !nameOK || !urlOK {
			return nil, formatError(ErrorValidation)
		}

		key, keyOK := "", false
		platformUserID := ""
		usedAccessToken := false
		if infoRaw, ok := record["account_info"]; ok {
			var info map[string]json.RawMessage
			if json.Unmarshal(infoRaw, &info) != nil {
				return nil, formatError(ErrorValidation)
			}
			if k, ok := stringAlias(info, "access_token"); ok && strings.TrimSpace(k) != "" {
				key, keyOK, usedAccessToken = k, true, true
			} else if k, ok := stringAlias(info, "apiKey", "api_key", "token"); ok {
				key, keyOK = k, true
			}
			if id, ok := stringAlias(info, "id"); ok {
				platformUserID = strings.TrimSpace(id)
			}
		}
		if !keyOK {
			if k, ok := stringAlias(record, "access_token"); ok && strings.TrimSpace(k) != "" {
				key, keyOK, usedAccessToken = k, true, true
			} else {
				key, keyOK = stringAlias(record, "apiKey", "api_key", "key")
			}
		}
		if !keyOK || strings.TrimSpace(key) == "" {
			return nil, formatError(ErrorValidation)
		}

		typeHint, typeOK := stringAlias(record, "site_type", "apiType", "type")
		if !typeOK && hasAlias(record, "site_type", "apiType", "type") {
			return nil, formatError(ErrorValidation)
		}

		// AAH access_token is a user credential for /api/user/* and check-in.
		kind := "api_key"
		if usedAccessToken {
			kind = "access_token"
		}
		if authType, ok := stringAlias(record, "authType", "auth_type"); ok {
			switch strings.ToLower(strings.TrimSpace(authType)) {
			case "access_token", "session":
				kind = strings.ToLower(strings.TrimSpace(authType))
			}
		}

		metaJSON := ""
		if platformUserID != "" {
			metaJSON = `{"platform_user_id":` + jsonNumberOrString(platformUserID) + `}`
		}

		checkinEnabled := false
		if checkInRaw, ok := record["checkIn"]; ok {
			var checkIn map[string]json.RawMessage
			if json.Unmarshal(checkInRaw, &checkIn) == nil {
				if enabled, ok := boolAlias(checkIn, "autoCheckInEnabled"); ok {
					checkinEnabled = enabled
				}
			}
		} else if usedAccessToken {
			// No checkIn block: still mark access tokens as check-in capable by default.
			checkinEnabled = true
		}

		items = append(items, Item{
			Name: name, BaseURL: baseURL, APIKey: key,
			Models: []string{}, Group: "default", Priority: 0, Weight: 100,
			SiteTypeHint: typeHint, Status: domain.StatusEnabled,
			CredentialKind: kind, MetaJSON: metaJSON, CheckinEnabled: checkinEnabled,
		})
	}
	return items, nil
}

// jsonNumberOrString encodes platform user ids that may be numeric strings.
func jsonNumberOrString(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "0"
	}
	if _, err := strconv.ParseInt(raw, 10, 64); err == nil {
		return raw
	}
	b, _ := json.Marshal(raw)
	return string(b)
}

func boolAlias(record map[string]json.RawMessage, name string) (bool, bool) {
	raw, ok := record[name]
	if !ok {
		return false, false
	}
	var value bool
	if json.Unmarshal(raw, &value) != nil {
		return false, false
	}
	return value, true
}

func normalizeItems(items []Item) ([]Item, error) {
	if len(items) == 0 || len(items) > maxItems {
		return nil, formatError(ErrorValidation)
	}
	seen := make(map[string]struct{}, len(items))
	result := make([]Item, 0, len(items))
	for _, item := range items {
		item.Name = strings.TrimSpace(item.Name)
		item.APIKey = strings.TrimSpace(item.APIKey)
		item.Group = normalizeGroup(item.Group)
		item.Models = normalizeList(item.Models)
		item.SiteTypeHint = normalizeType(item.SiteTypeHint)
		item.Status = strings.ToLower(strings.TrimSpace(item.Status))
		if item.Status == "" {
			item.Status = domain.StatusEnabled
		}
		var err error
		item.BaseURL, err = NormalizeBaseURL(item.BaseURL)
		if err != nil || item.Name == "" || item.APIKey == "" || item.Group == "" ||
			len(item.Name) > 256 || len(item.APIKey) > 16384 || len(item.BaseURL) > 2048 ||
			len(item.Group) > 256 || len(item.SiteTypeHint) > 128 || len(item.Models) > 1000 ||
			item.Priority < 0 || item.Priority > 1_000_000 || item.Weight < 0 || item.Weight > 1_000_000 ||
			(item.Status != domain.StatusEnabled && item.Status != domain.StatusDisabled) {
			return nil, formatError(ErrorValidation)
		}
		for _, model := range item.Models {
			if len(model) > 256 {
				return nil, formatError(ErrorValidation)
			}
		}
		identity := item.BaseURL + "\x00" + item.APIKey
		if _, exists := seen[identity]; exists {
			return nil, formatError(ErrorValidation)
		}
		seen[identity] = struct{}{}
		result = append(result, item)
	}
	return result, nil
}

func NormalizeBaseURL(raw string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", formatError(ErrorValidation)
	}
	parsed.Scheme = strings.ToLower(parsed.Scheme)
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", formatError(ErrorValidation)
	}
	hostname := strings.ToLower(parsed.Hostname())
	if hostname == "" {
		return "", formatError(ErrorValidation)
	}
	port := parsed.Port()
	if (parsed.Scheme == "http" && port == "80") || (parsed.Scheme == "https" && port == "443") {
		port = ""
	}
	parsed.Host = hostname
	if strings.Contains(hostname, ":") {
		parsed.Host = "[" + hostname + "]"
	}
	if port != "" {
		if _, err := strconv.Atoi(port); err != nil {
			return "", formatError(ErrorValidation)
		}
		parsed.Host = net.JoinHostPort(hostname, port)
	}
	cleaned := path.Clean("/" + strings.TrimPrefix(parsed.EscapedPath(), "/"))
	if cleaned == "/" {
		parsed.Path, parsed.RawPath = "", ""
	} else {
		unescaped, err := url.PathUnescape(cleaned)
		if err != nil {
			return "", formatError(ErrorValidation)
		}
		parsed.Path, parsed.RawPath = unescaped, ""
	}
	return parsed.String(), nil
}

func normalizeList(values []string) []string {
	seen := map[string]struct{}{}
	for _, value := range values {
		for _, part := range strings.Split(value, ",") {
			part = strings.TrimSpace(part)
			if part != "" {
				seen[part] = struct{}{}
			}
		}
	}
	result := make([]string, 0, len(seen))
	for value := range seen {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func normalizeGroup(value string) string {
	groups := normalizeList([]string{value})
	if len(groups) == 0 {
		return "default"
	}
	return strings.Join(groups, ",")
}

func normalizeType(value string) string {
	// Preserve brand labels when possible for UI, but collapse known aliases.
	// Discovery resolves brands via adapters.CanonicalType / Registry.Resolve.
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, "_", "-")
	switch value {
	case "", "openai", "openaicompat", "openai-compatible", "openai-compat":
		return "openai-compatible"
	case "newapi", "new-api":
		return "new-api"
	case "oneapi", "one-api":
		return "one-api"
	case "voapi":
		return "voapi"
	case "super-api", "superapi":
		return "super-api"
	case "rix-api", "rixapi":
		return "rix-api"
	case "neo-api", "neoapi":
		return "neo-api"
	default:
		// Keep original brand id (anyrouter, axonhub, …) for operator visibility.
		_ = adapters.CanonicalType(value)
		return value
	}
}

func decodeStrict(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return formatError(ErrorValidation)
	}
	return nil
}

func stringAlias(record map[string]json.RawMessage, names ...string) (string, bool) {
	var value string
	found := false
	for _, name := range names {
		raw, ok := record[name]
		if !ok {
			continue
		}
		var current string
		if json.Unmarshal(raw, &current) != nil || (found && current != value) {
			return "", false
		}
		value, found = current, true
	}
	return value, found
}

func listAlias(record map[string]json.RawMessage, names ...string) ([]string, bool) {
	var result []string
	found := false
	for _, name := range names {
		raw, ok := record[name]
		if !ok {
			continue
		}
		var values []string
		if json.Unmarshal(raw, &values) != nil {
			var value string
			if json.Unmarshal(raw, &value) != nil {
				return nil, false
			}
			values = []string{value}
		}
		normalized := normalizeList(values)
		if found && strings.Join(normalized, "\x00") != strings.Join(result, "\x00") {
			return nil, false
		}
		result, found = normalized, true
	}
	return result, found
}

func intAlias(record map[string]json.RawMessage, name string) (int, bool) {
	raw, ok := record[name]
	if !ok {
		return 0, false
	}
	var value int
	return value, json.Unmarshal(raw, &value) == nil
}

func hasAlias(record map[string]json.RawMessage, names ...string) bool {
	for _, name := range names {
		if _, ok := record[name]; ok {
			return true
		}
	}
	return false
}

func newAPIStatus(raw json.RawMessage) (string, error) {
	if len(raw) == 0 {
		return domain.StatusEnabled, nil
	}
	var number int
	if json.Unmarshal(raw, &number) == nil {
		switch number {
		case 1:
			return domain.StatusEnabled, nil
		case 2, 3:
			return domain.StatusDisabled, nil
		default:
			return "", formatError(ErrorValidation)
		}
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		text = strings.ToLower(strings.TrimSpace(text))
		if text == domain.StatusEnabled || text == domain.StatusDisabled {
			return text, nil
		}
	}
	return "", formatError(ErrorValidation)
}
