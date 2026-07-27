package adapters

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const maxAccountResponseBytes = 2 << 20

// AccountInput is the shared auth payload for New API-family user endpoints.
type AccountInput struct {
	BaseURL        string
	Secret         string
	PlatformUserID int64
	UserHeader     bool
}

// AccountSelf is a redacted account probe result (no secrets).
type AccountSelf struct {
	PlatformUserID int64
	Username       string
	DisplayName    string
	Quota          *int64
	UsedQuota      *int64
}

// UpstreamAPIKey is one site-managed OpenAI-compatible token.
// Secret is plaintext only when the upstream returns it; callers must zero it after use.
type UpstreamAPIKey struct {
	ID   int64
	Name string
	// Group is the New API token group (empty = default).
	Group  string
	Secret string
	// Status mirrors upstream token status when provided (1 = enabled).
	Status int
}

// AccountAdapter probes user identity and lists API keys for relay attachment.
type AccountAdapter interface {
	Name() string
	ProbeSelf(context.Context, AccountInput) (AccountSelf, error)
	ListAPIKeys(context.Context, AccountInput, int, int) ([]UpstreamAPIKey, error)
	RevealAPIKey(context.Context, AccountInput, int64) (string, error)
}

// NewAPIAccountAdapter implements AccountAdapter for New API / One API style hosts.
type NewAPIAccountAdapter struct {
	name       string
	client     *http.Client
	userHeader bool
}

func NewNewAPIAccountAdapter(name string, client *http.Client, userHeader bool) *NewAPIAccountAdapter {
	if client == nil {
		client = &http.Client{Transport: &http.Transport{ResponseHeaderTimeout: 15 * time.Second}}
	}
	return &NewAPIAccountAdapter{name: name, client: client, userHeader: userHeader}
}

func (a *NewAPIAccountAdapter) Name() string { return a.name }

func (a *NewAPIAccountAdapter) ProbeSelf(ctx context.Context, input AccountInput) (AccountSelf, error) {
	endpoint, err := accountEndpoint(input.BaseURL, "/api/user/self")
	if err != nil {
		return AccountSelf{}, &Error{Kind: ErrorInvalidURL}
	}
	body, status, err := a.doJSON(ctx, http.MethodGet, endpoint, input)
	if err != nil {
		return AccountSelf{}, err
	}
	if status < 200 || status >= 300 {
		return AccountSelf{}, &Error{Kind: ErrorStatus, Status: status}
	}
	var payload struct {
		Success *bool `json:"success"`
		Data    struct {
			ID          any    `json:"id"`
			Username    string `json:"username"`
			DisplayName string `json:"display_name"`
			Quota       *int64 `json:"quota"`
			UsedQuota   *int64 `json:"used_quota"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return AccountSelf{}, &Error{Kind: ErrorPayload}
	}
	if payload.Success != nil && !*payload.Success {
		return AccountSelf{}, &Error{Kind: ErrorPayload}
	}
	userID, ok := coercePositiveInt64(payload.Data.ID)
	if !ok || strings.TrimSpace(payload.Data.Username) == "" {
		// Some hosts omit success and nest fields at the top level.
		var flat struct {
			ID          any    `json:"id"`
			Username    string `json:"username"`
			DisplayName string `json:"display_name"`
			Quota       *int64 `json:"quota"`
			UsedQuota   *int64 `json:"used_quota"`
		}
		if err := json.Unmarshal(body, &flat); err != nil || strings.TrimSpace(flat.Username) == "" {
			return AccountSelf{}, &Error{Kind: ErrorPayload}
		}
		userID, ok = coercePositiveInt64(flat.ID)
		if !ok {
			return AccountSelf{}, &Error{Kind: ErrorPayload}
		}
		return AccountSelf{
			PlatformUserID: userID,
			Username:       strings.TrimSpace(flat.Username),
			DisplayName:    strings.TrimSpace(flat.DisplayName),
			Quota:          flat.Quota,
			UsedQuota:      flat.UsedQuota,
		}, nil
	}
	return AccountSelf{
		PlatformUserID: userID,
		Username:       strings.TrimSpace(payload.Data.Username),
		DisplayName:    strings.TrimSpace(payload.Data.DisplayName),
		Quota:          payload.Data.Quota,
		UsedQuota:      payload.Data.UsedQuota,
	}, nil
}

func (a *NewAPIAccountAdapter) ListAPIKeys(ctx context.Context, input AccountInput, page, size int) ([]UpstreamAPIKey, error) {
	if page < 0 {
		page = 0
	}
	if size <= 0 {
		size = 100
	}
	if size > 100 {
		size = 100
	}
	query := url.Values{}
	query.Set("p", strconv.Itoa(page))
	query.Set("size", strconv.Itoa(size))
	endpoint, err := accountEndpoint(input.BaseURL, "/api/token/")
	if err != nil {
		return nil, &Error{Kind: ErrorInvalidURL}
	}
	endpoint += "?" + query.Encode()
	body, status, err := a.doJSON(ctx, http.MethodGet, endpoint, input)
	if err != nil {
		return nil, err
	}
	if status < 200 || status >= 300 {
		return nil, &Error{Kind: ErrorStatus, Status: status}
	}
	keys := parseTokenList(body)
	// Some New-API forks are 1-based for page index.
	if len(keys) == 0 && page == 0 {
		query.Set("p", "1")
		alt := strings.Split(endpoint, "?")[0] + "?" + query.Encode()
		body, status, err = a.doJSON(ctx, http.MethodGet, alt, input)
		if err == nil && status >= 200 && status < 300 {
			keys = parseTokenList(body)
		}
	}
	return keys, nil
}

func (a *NewAPIAccountAdapter) RevealAPIKey(ctx context.Context, input AccountInput, tokenID int64) (string, error) {
	if tokenID <= 0 {
		return "", &Error{Kind: ErrorInvalidURL}
	}
	endpoint, err := accountEndpoint(input.BaseURL, fmt.Sprintf("/api/token/%d/key", tokenID))
	if err != nil {
		return "", &Error{Kind: ErrorInvalidURL}
	}
	// All API Hub defaults to POST for secret reveal on some forks; others use GET.
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		body, status, reqErr := a.doJSON(ctx, method, endpoint, input)
		if reqErr != nil {
			continue
		}
		if status < 200 || status >= 300 {
			continue
		}
		if secret := parseRevealedKey(body); secret != "" {
			return secret, nil
		}
	}
	return "", &Error{Kind: ErrorPayload}
}

func parseRevealedKey(body []byte) string {
	// {"success":true,"data":"sk-..."}
	var asString struct {
		Success *bool  `json:"success"`
		Data    string `json:"data"`
		Key     string `json:"key"`
	}
	if err := json.Unmarshal(body, &asString); err == nil {
		if secret := strings.TrimSpace(asString.Data); secret != "" && !looksMasked(secret) {
			return secret
		}
		if secret := strings.TrimSpace(asString.Key); secret != "" && !looksMasked(secret) {
			return secret
		}
	}
	// {"data":{"key":"sk-...","full_key":"..."}}
	var asObject struct {
		Success *bool `json:"success"`
		Data    struct {
			Key     string `json:"key"`
			FullKey string `json:"full_key"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &asObject); err == nil {
		if secret := strings.TrimSpace(asObject.Data.FullKey); secret != "" && !looksMasked(secret) {
			return secret
		}
		if secret := strings.TrimSpace(asObject.Data.Key); secret != "" && !looksMasked(secret) {
			return secret
		}
	}
	// bare string body
	trimmed := strings.TrimSpace(string(body))
	if len(trimmed) >= 2 && trimmed[0] == '"' && trimmed[len(trimmed)-1] == '"' {
		var bare string
		if json.Unmarshal(body, &bare) == nil {
			trimmed = strings.TrimSpace(bare)
		}
	}
	if trimmed != "" && !strings.HasPrefix(trimmed, "{") && !looksMasked(trimmed) {
		return trimmed
	}
	return ""
}

func (a *NewAPIAccountAdapter) doJSON(ctx context.Context, method, endpoint string, input AccountInput) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, method, endpoint, nil)
	if err != nil {
		return nil, 0, &Error{Kind: ErrorInvalidURL}
	}
	req.Header.Set("Authorization", "Bearer "+input.Secret)
	req.Header.Set("Accept", "application/json")
	if a.userHeader || input.UserHeader {
		ApplyCompatUserIDHeaders(req.Header, input.PlatformUserID)
	}
	resp, err := a.client.Do(req)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, 0, err
		}
		return nil, 0, &Error{Kind: ErrorTransport}
	}
	defer resp.Body.Close()
	limited := io.LimitReader(resp.Body, maxAccountResponseBytes+1)
	body, err := io.ReadAll(limited)
	if err != nil {
		return nil, 0, &Error{Kind: ErrorTransport}
	}
	if len(body) > maxAccountResponseBytes {
		return nil, 0, &Error{Kind: ErrorTooLarge}
	}
	return body, resp.StatusCode, nil
}

func accountEndpoint(baseURL, suffix string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || !parsed.IsAbs() || parsed.Host == "" || parsed.User != nil ||
		(parsed.Scheme != "http" && parsed.Scheme != "https") {
		return "", errors.New("invalid base URL")
	}
	parsed.RawQuery = ""
	parsed.Fragment = ""
	parsed.RawPath = ""
	// Strip trailing /v1 so site base + /api/* resolve correctly after OpenAI-style imports.
	pathPart := strings.TrimRight(parsed.Path, "/")
	pathPart = strings.TrimSuffix(pathPart, "/v1")
	pathPart = strings.TrimRight(pathPart, "/")
	parsed.Path = pathPart + suffix
	return parsed.String(), nil
}

func parseTokenList(body []byte) []UpstreamAPIKey {
	var envelope struct {
		Success *bool           `json:"success"`
		Data    json.RawMessage `json:"data"`
		Items   json.RawMessage `json:"items"`
		Records json.RawMessage `json:"records"`
		Tokens  json.RawMessage `json:"tokens"`
	}
	raw := body
	if err := json.Unmarshal(body, &envelope); err == nil {
		if len(envelope.Data) > 0 {
			raw = envelope.Data
		} else if len(envelope.Items) > 0 {
			raw = envelope.Items
		} else if len(envelope.Records) > 0 {
			raw = envelope.Records
		} else if len(envelope.Tokens) > 0 {
			raw = envelope.Tokens
		}
	}
	// Array form.
	var asArray []map[string]any
	if err := json.Unmarshal(raw, &asArray); err == nil {
		return mapTokenRecords(asArray)
	}
	// Paginated / nested object form.
	var page map[string]json.RawMessage
	if err := json.Unmarshal(raw, &page); err == nil {
		for _, key := range []string{"items", "data", "records", "tokens", "list"} {
			chunk, ok := page[key]
			if !ok || len(chunk) == 0 {
				continue
			}
			var nested []map[string]any
			if json.Unmarshal(chunk, &nested) == nil && len(nested) > 0 {
				return mapTokenRecords(nested)
			}
		}
	}
	return nil
}

func mapTokenRecords(records []map[string]any) []UpstreamAPIKey {
	out := make([]UpstreamAPIKey, 0, len(records))
	for _, record := range records {
		id, _ := coercePositiveInt64(record["id"])
		name, _ := record["name"].(string)
		secret := firstString(record, "full_key", "key", "token", "secret", "api_key", "apiKey")
		if secret == "" {
			if nested, ok := record["token"].(map[string]any); ok {
				secret = firstString(nested, "full_key", "key", "token", "secret")
			}
		}
		status := 1
		if rawStatus, ok := coercePositiveInt64(record["status"]); ok {
			status = int(rawStatus)
		}
		if looksMasked(secret) {
			secret = ""
		}
		groupName := firstString(record, "group", "Group", "token_group", "tokenGroup")
		out = append(out, UpstreamAPIKey{
			ID:     id,
			Name:   strings.TrimSpace(name),
			Group:  strings.TrimSpace(groupName),
			Secret: strings.TrimSpace(secret),
			Status: status,
		})
	}
	return out
}

func firstString(record map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := record[key].(string); ok && strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func looksMasked(secret string) bool {
	trimmed := strings.TrimSpace(secret)
	if trimmed == "" {
		return false
	}
	if strings.Contains(trimmed, "*") || strings.Contains(trimmed, "…") || strings.Contains(trimmed, "...") {
		return true
	}
	// Typical masked form: sk-xxxx****yyyy
	if strings.HasPrefix(trimmed, "sk-") && strings.Count(trimmed, "x") >= 4 {
		return true
	}
	return false
}

func coercePositiveInt64(value any) (int64, bool) {
	switch typed := value.(type) {
	case float64:
		if typed <= 0 {
			return 0, false
		}
		return int64(typed), true
	case json.Number:
		parsed, err := typed.Int64()
		if err != nil || parsed <= 0 {
			return 0, false
		}
		return parsed, true
	case int64:
		if typed <= 0 {
			return 0, false
		}
		return typed, true
	case int:
		if typed <= 0 {
			return 0, false
		}
		return int64(typed), true
	case string:
		parsed, err := strconv.ParseInt(strings.TrimSpace(typed), 10, 64)
		if err != nil || parsed <= 0 {
			return 0, false
		}
		return parsed, true
	default:
		return 0, false
	}
}
