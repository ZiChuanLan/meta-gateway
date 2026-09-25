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

	"github.com/lan/meta-gateway/internal/domain"
)

// Parse returns the importable items of a document, discarding row-level
// problems. Rows that cannot be imported are reported by ParseWithReport.
func Parse(data []byte) ([]Item, error) {
	report, err := ParseWithReport(data)
	if err != nil {
		return nil, err
	}
	return report.Items, nil
}

// ParseReport is the outcome of a tolerant parse: the rows that can be
// imported, plus the rows that were dropped and why.
type ParseReport struct {
	Items   []Item
	Skipped []SkippedItem
}

// ParseWithReport parses an import document row by row.
//
// Document-level problems (unknown shape, ambiguous sections, malformed
// envelope, zero importable rows) still fail the whole call, because they mean
// the file is not a usable backup. Row-level problems (an account with no
// usable credential, a duplicate identity, a bad base URL) never do: real AAH
// backups routinely contain one unusable row, and rejecting the whole file for
// it hid every good row behind an "unsupported format" message.
func ParseWithReport(data []byte) (ParseReport, error) {
	var root json.RawMessage
	if err := decodeStrict(data, &root); err != nil {
		return ParseReport{}, formatError(ErrorValidation)
	}
	trimmed := bytes.TrimSpace(root)
	if len(trimmed) == 0 {
		return ParseReport{}, formatError(ErrorValidation)
	}

	var rawItems []Item
	var skipped []SkippedItem
	var err error
	switch trimmed[0] {
	case '[':
		rawItems, skipped, err = parseNewAPIList(trimmed)
	case '{':
		rawItems, skipped, err = parseObject(trimmed)
	default:
		return ParseReport{}, formatError(ErrorUnsupported)
	}
	if err != nil {
		return ParseReport{}, err
	}

	items, normalizeSkipped, err := normalizeItems(rawItems)
	if err != nil {
		return ParseReport{}, err
	}
	skipped = append(skipped, normalizeSkipped...)
	if len(items) == 0 {
		// Nothing importable. When rows were dropped, tell the operator that the
		// document was recognized but every row was unusable, instead of blaming
		// the format.
		if len(skipped) > 0 {
			return ParseReport{Skipped: skipped}, formatError(ErrorNoEntries)
		}
		return ParseReport{}, formatError(ErrorValidation)
	}
	return ParseReport{Items: items, Skipped: skipped}, nil
}

func parseObject(data []byte) ([]Item, []SkippedItem, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return nil, nil, formatError(ErrorValidation)
	}
	_, canonical := fields["format"]
	_, channels := fields["channels"]
	_, listData := fields["data"]
	_, profiles := fields["apiCredentialProfiles"]
	_, accounts := fields["accounts"]
	if canonical {
		if channels || listData || profiles || accounts {
			return nil, nil, formatError(ErrorUnsupported)
		}
		return parseCanonical(data)
	}
	// All API Hub full-state backups declare a top-level version string and
	// carry credentials in apiCredentialProfiles.profiles (API keys) or
	// accounts[].account_info.access_token (site session tokens). Prefer
	// profiles when non-empty; otherwise fall back. The version value itself
	// is not gated: structure decides, and zero parsed items is an error.
	if isAAHV2Document(fields) {
		if channels || listData {
			return nil, nil, formatError(ErrorUnsupported)
		}
		return parseAAHV2(fields)
	}
	// An AAH backup whose sync data selection carries no credential section at
	// all (only preferences / tags / channel configs) is a real AAH backup; it
	// simply holds nothing this gateway can import.
	if looksLikeCredentiallessAAHBackup(fields) {
		return nil, nil, formatError(ErrorNoEntries)
	}
	// AAH legacy scoped snapshots nest the very same sections under `data`.
	// Read them instead of trying to coerce the object into a channel list.
	if raw, ok := fields["data"]; ok {
		var nested map[string]json.RawMessage
		if err := json.Unmarshal(raw, &nested); err == nil && nested != nil {
			merged := make(map[string]json.RawMessage, len(fields)+len(nested))
			for key, value := range fields {
				merged[key] = value
			}
			for key, value := range nested {
				merged[key] = value
			}
			if isAAHV2Document(merged) {
				return parseAAHV2(merged)
			}
		}
	}
	shapeCount := 0
	if channels {
		shapeCount++
	}
	if listData {
		shapeCount++
	}
	if shapeCount != 1 {
		return nil, nil, formatError(ErrorUnsupported)
	}
	if channels {
		return parseNewAPIList(fields["channels"])
	}
	return parseNewAPIList(fields["data"])
}

func isAAHV2Document(fields map[string]json.RawMessage) bool {
	if !hasStringVersion(fields) {
		return false
	}
	_, hasProfiles := fields["apiCredentialProfiles"]
	_, hasAccounts := fields["accounts"]
	return hasProfiles || hasAccounts
}

// looksLikeCredentiallessAAHBackup reports an AAH full-state / selective backup
// that declares a version and at least one AAH-only section, but carries no
// credential section. It is a valid backup of nothing importable.
func looksLikeCredentiallessAAHBackup(fields map[string]json.RawMessage) bool {
	if !hasStringVersion(fields) {
		return false
	}
	for _, name := range []string{"preferences", "tagStore", "channelConfigs", "featureGuidance"} {
		if _, ok := fields[name]; ok {
			return true
		}
	}
	return false
}

func hasStringVersion(fields map[string]json.RawMessage) bool {
	raw, ok := fields["version"]
	if !ok {
		return false
	}
	var version string
	return json.Unmarshal(raw, &version) == nil
}

func parseCanonical(data []byte) ([]Item, []SkippedItem, error) {
	type canonicalItem struct {
		Name         *string   `json:"name"`
		BaseURL      *string   `json:"base_url"`
		APIKey       *string   `json:"api_key"`
		Models       *[]string `json:"models"`
		Group        *string   `json:"group"`
		Priority     *int      `json:"priority"`
		Weight       *int      `json:"weight"`
		SiteTypeHint *string   `json:"site_type_hint"`
		// CheckinEnabled is optional: our own export writes it when true, and
		// backups made before the field existed simply do not have it.
		CheckinEnabled *bool `json:"checkin_enabled"`
	}
	type canonicalEnvelope struct {
		Format     *string          `json:"format"`
		Version    *int             `json:"version"`
		ExportedAt *string          `json:"exported_at"`
		Importable *bool            `json:"importable"`
		Items      *[]canonicalItem `json:"items"`
		// Skipped is written by our own export for channels that could not be
		// exported. Accepting it here matters: without the field, decoding with
		// DisallowUnknownFields rejected every backup that skipped a channel,
		// so this gateway could not import its own export back.
		Skipped *[]SkippedChannel `json:"skipped"`
	}
	var envelope canonicalEnvelope
	if err := decodeStrict(data, &envelope); err != nil || envelope.Format == nil ||
		envelope.Version == nil || envelope.ExportedAt == nil || envelope.Importable == nil ||
		envelope.Items == nil {
		return nil, nil, formatError(ErrorValidation)
	}
	if *envelope.Format != Format || *envelope.Version != Version {
		return nil, nil, formatError(ErrorUnsupported)
	}
	if !*envelope.Importable {
		// Our own export sets importable=false when it carries no credentials
		// (a secrets-less channel listing). The document is perfectly well
		// formed — there is just nothing to import. Reporting a validation
		// failure here sent the operator looking for a corrupt file.
		return nil, nil, formatError(ErrorNoEntries)
	}
	if _, err := time.Parse(time.RFC3339, *envelope.ExportedAt); err != nil {
		return nil, nil, formatError(ErrorValidation)
	}
	items := make([]Item, 0, len(*envelope.Items))
	skipped := make([]SkippedItem, 0)
	for index, raw := range *envelope.Items {
		if raw.Name == nil || raw.BaseURL == nil || raw.APIKey == nil || raw.Models == nil ||
			raw.Group == nil || raw.Priority == nil || raw.Weight == nil || raw.SiteTypeHint == nil {
			skipped = append(skipped, SkippedItem{Index: index, Name: stringOrEmpty(raw.Name), Reason: SkipMissingField})
			continue
		}
		items = append(items, Item{Name: *raw.Name, BaseURL: *raw.BaseURL, APIKey: *raw.APIKey,
			Models: *raw.Models, Group: *raw.Group, Priority: *raw.Priority,
			Weight: *raw.Weight, SiteTypeHint: *raw.SiteTypeHint,
			CheckinEnabled: raw.CheckinEnabled != nil && *raw.CheckinEnabled})
	}
	return items, skipped, nil
}

func stringOrEmpty(value *string) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(*value)
}

func parseNewAPIList(data []byte) ([]Item, []SkippedItem, error) {
	var records []map[string]json.RawMessage
	if err := json.Unmarshal(data, &records); err != nil || records == nil {
		return nil, nil, formatError(ErrorValidation)
	}
	items := make([]Item, 0, len(records))
	skipped := make([]SkippedItem, 0)
	for index, record := range records {
		item, reason := parseNewAPIRecord(record)
		if reason != "" {
			skipped = append(skipped, SkippedItem{Index: index, Name: item.Name, Reason: reason})
			continue
		}
		items = append(items, item)
	}
	return items, skipped, nil
}

// parseNewAPIRecord reads one New API channel row. A non-empty reason means the
// row is dropped; it never fails the surrounding document.
func parseNewAPIRecord(record map[string]json.RawMessage) (Item, string) {
	name, ok := stringAlias(record, "name")
	if !ok {
		return Item{}, SkipMissingField
	}
	baseURL, ok := stringAlias(record, "base_url", "baseUrl")
	if !ok {
		return Item{Name: name}, SkipMissingField
	}
	key, ok := stringAlias(record, "key", "api_key", "apiKey")
	if !ok || strings.TrimSpace(key) == "" {
		return Item{Name: name}, SkipMissingCredential
	}
	models, ok := listAlias(record, "models")
	if !ok {
		if hasAlias(record, "models") {
			return Item{Name: name}, SkipInvalidItem
		}
		models = []string{}
	}
	groups, ok := listAlias(record, "group", "groups")
	if !ok {
		if hasAlias(record, "group", "groups") {
			return Item{Name: name}, SkipInvalidItem
		}
		groups = []string{"default"}
	} else if len(groups) == 0 {
		groups = []string{"default"}
	}
	priority, ok := intAlias(record, "priority")
	if !ok {
		if hasAlias(record, "priority") {
			return Item{Name: name}, SkipInvalidItem
		}
		priority = 0
	}
	weight, ok := intAlias(record, "weight")
	if !ok {
		if hasAlias(record, "weight") {
			return Item{Name: name}, SkipInvalidItem
		}
		weight = 100
	}
	typeHint, typeOK := stringAlias(record, "type", "type_hint", "typeHint", "site_type_hint", "siteTypeHint")
	if !typeOK && hasAlias(record, "type", "type_hint", "typeHint", "site_type_hint", "siteTypeHint") {
		return Item{Name: name}, SkipInvalidItem
	}
	status, err := newAPIStatus(record["status"])
	if err != nil {
		return Item{Name: name}, SkipInvalidItem
	}
	return Item{Name: name, BaseURL: baseURL, APIKey: key,
		Models: models, Group: strings.Join(groups, ","), Priority: priority,
		Weight: weight, SiteTypeHint: typeHint, Status: status}, ""
}

func parseAAHV2(fields map[string]json.RawMessage) ([]Item, []SkippedItem, error) {
	// Collect from BOTH profiles and accounts when both exist.
	// Profiles carry api_key entries; accounts carry access_token/session entries
	// for check-in. Silently dropping one leaks data, especially in replace mode.
	var combined []Item
	var skipped []SkippedItem

	if raw, ok := fields["apiCredentialProfiles"]; ok {
		var container struct {
			Profiles []map[string]json.RawMessage `json:"profiles"`
		}
		if err := json.Unmarshal(raw, &container); err != nil {
			return nil, nil, formatError(ErrorValidation)
		}
		for index, profile := range container.Profiles {
			item, reason := parseAAHProfile(profile)
			if reason != "" {
				skipped = append(skipped, SkippedItem{Index: index, Name: item.Name, Reason: reason})
				continue
			}
			combined = append(combined, item)
		}
	}

	if raw, ok := fields["accounts"]; ok {
		items, accountSkipped, err := parseAAHAccounts(raw)
		if err != nil {
			return nil, nil, err
		}
		combined = append(combined, items...)
		skipped = append(skipped, accountSkipped...)
	}

	if len(combined) == 0 && len(skipped) == 0 {
		return nil, nil, formatError(ErrorNoEntries)
	}
	return combined, skipped, nil
}

// parseAAHProfile reads one AAH API credential profile row.
func parseAAHProfile(profile map[string]json.RawMessage) (Item, string) {
	name, nameOK := stringAlias(profile, "name")
	baseURL, urlOK := stringAlias(profile, "baseUrl", "base_url")
	key, keyOK := stringAlias(profile, "apiKey", "api_key")
	if !nameOK || !urlOK {
		return Item{Name: name}, SkipMissingField
	}
	if !keyOK || strings.TrimSpace(key) == "" {
		return Item{Name: name}, SkipMissingCredential
	}
	typeHint, typeOK := stringAlias(profile, "apiType")
	if !typeOK && hasAlias(profile, "apiType") {
		return Item{Name: name}, SkipInvalidItem
	}
	return Item{
		Name: name, BaseURL: baseURL, APIKey: key,
		Models: []string{}, Group: "default", Priority: 0, Weight: 100,
		SiteTypeHint: typeHint, Status: domain.StatusEnabled,
		CredentialKind: "api_key",
	}, ""
}

func parseAAHAccounts(raw json.RawMessage) ([]Item, []SkippedItem, error) {
	// accounts may be a list or { "accounts": [...] } container from AAH V2.
	var asList []map[string]json.RawMessage
	if err := json.Unmarshal(raw, &asList); err == nil && asList != nil {
		return parseAAHAccountRecords(asList)
	}
	var container struct {
		Accounts []map[string]json.RawMessage `json:"accounts"`
	}
	if err := json.Unmarshal(raw, &container); err != nil || container.Accounts == nil {
		return nil, nil, formatError(ErrorValidation)
	}
	return parseAAHAccountRecords(container.Accounts)
}

func parseAAHAccountRecords(records []map[string]json.RawMessage) ([]Item, []SkippedItem, error) {
	items := make([]Item, 0, len(records))
	skipped := make([]SkippedItem, 0)
	for index, record := range records {
		// Skip explicitly disabled sites; they should not become relay channels.
		if disabled, ok := boolAlias(record, "disabled"); ok && disabled {
			continue
		}
		name, nameOK := stringAlias(record, "site_name", "name")
		baseURL, urlOK := stringAlias(record, "site_url", "baseUrl", "base_url")
		if !nameOK || !urlOK {
			skipped = append(skipped, SkippedItem{Index: index, Name: name, Reason: SkipMissingField})
			continue
		}

		key, keyOK := "", false
		platformUserID := ""
		usedAccessToken := false
		if infoRaw, ok := record["account_info"]; ok {
			var info map[string]json.RawMessage
			if json.Unmarshal(infoRaw, &info) != nil {
				skipped = append(skipped, SkippedItem{Index: index, Name: name, Reason: SkipInvalidItem})
				continue
			}
			if k, ok := stringAlias(info, "access_token"); ok && strings.TrimSpace(k) != "" {
				key, keyOK, usedAccessToken = k, true, true
			} else if k, ok := stringAlias(info, "apiKey", "api_key", "token"); ok && strings.TrimSpace(k) != "" {
				key, keyOK = k, true
			}
			if id, ok := stringAlias(info, "id"); ok {
				platformUserID = strings.TrimSpace(id)
			}
		}
		if !keyOK {
			if k, ok := stringAlias(record, "access_token"); ok && strings.TrimSpace(k) != "" {
				key, keyOK, usedAccessToken = k, true, true
			} else if k, ok := stringAlias(record, "apiKey", "api_key", "key"); ok && strings.TrimSpace(k) != "" {
				key, keyOK = k, true
			}
		}
		// Accounts in cookie / none auth mode legitimately carry no token. They
		// are not relay-usable, but they must not take the rest of the backup
		// down with them.
		if !keyOK {
			skipped = append(skipped, SkippedItem{Index: index, Name: name, Reason: SkipMissingCredential})
			continue
		}

		typeHint, typeOK := stringAlias(record, "site_type", "apiType", "type")
		if !typeOK && hasAlias(record, "site_type", "apiType", "type") {
			skipped = append(skipped, SkippedItem{Index: index, Name: name, Reason: SkipInvalidItem})
			continue
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
	return items, skipped, nil
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

// normalizeItems validates and de-duplicates parsed rows. Rows that fail
// validation are reported instead of failing the document.
func normalizeItems(items []Item) ([]Item, []SkippedItem, error) {
	if len(items) > maxItems {
		return nil, nil, formatError(ErrorValidation)
	}
	seen := make(map[string]struct{}, len(items))
	result := make([]Item, 0, len(items))
	skipped := make([]SkippedItem, 0)
	for index, item := range items {
		normalized, reason := normalizeItem(item)
		if reason != "" {
			skipped = append(skipped, SkippedItem{Index: index, Name: normalized.Name, Reason: reason})
			continue
		}
		identity := normalized.BaseURL + "\x00" + normalized.APIKey
		if _, exists := seen[identity]; exists {
			skipped = append(skipped, SkippedItem{Index: index, Name: normalized.Name, Reason: SkipDuplicateIdentity})
			continue
		}
		seen[identity] = struct{}{}
		result = append(result, normalized)
	}
	return result, skipped, nil
}

// normalizeItem trims and validates one row. A non-empty reason means the row
// is dropped.
func normalizeItem(item Item) (Item, string) {
	item.Name = strings.TrimSpace(item.Name)
	item.APIKey = strings.TrimSpace(item.APIKey)
	item.Group = normalizeGroup(item.Group)
	item.Models = normalizeList(item.Models)
	item.SiteTypeHint = normalizeType(item.SiteTypeHint)
	item.Status = strings.ToLower(strings.TrimSpace(item.Status))
	if item.Status == "" {
		item.Status = domain.StatusEnabled
	}
	baseURL, err := NormalizeBaseURL(item.BaseURL)
	if err != nil {
		return item, SkipInvalidBaseURL
	}
	item.BaseURL = baseURL
	if item.Name == "" || item.Group == "" {
		return item, SkipMissingField
	}
	if item.APIKey == "" {
		return item, SkipMissingCredential
	}
	if len(item.Name) > 256 || len(item.APIKey) > 16384 || len(item.BaseURL) > 2048 ||
		len(item.Group) > 256 || len(item.SiteTypeHint) > 128 || len(item.Models) > 1000 ||
		item.Priority < 0 || item.Priority > 1_000_000 || item.Weight < 0 || item.Weight > 1_000_000 ||
		(item.Status != domain.StatusEnabled && item.Status != domain.StatusDisabled) {
		return item, SkipInvalidItem
	}
	for _, model := range item.Models {
		if len(model) > 256 {
			return item, SkipInvalidItem
		}
	}
	return item, ""
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
		// Keep original brand id (anyrouter, axonhub, ...) for operator visibility.
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
