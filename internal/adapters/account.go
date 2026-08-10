package adapters

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
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
	// NoUserHeader suppresses the compat user-id headers even when the
	// adapter defaults to sending them (a.{userHeader}). Some forks reject
	// the headers, others require them; callers retry with the opposite
	// setting on 401/403.
	NoUserHeader bool
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

// NewAPIKeyRequest describes a token to create on a New API family host.
// Quota is in the site's remain_quota unit (USD cents-equivalent); zero means
// unlimited when UnlimitedQuota is set.
type NewAPIKeyRequest struct {
	Name           string
	Group          string
	Quota          int64
	UnlimitedQuota bool
	ExpiredAtUnix  int64
	ModelLimits    string
}

// ModelPrice is one model's price on an upstream, mirroring the All API Hub
// modelPricing.ts normalization (source-verified):
//   - token billing:  inputUSD = model_ratio × (1e6 / quota_per_unit) × group_ratio
//     outputUSD = inputUSD × completion_ratio
//   - direct USD:     token_price_usd_per_million.input wins (no ratio semantics)
//   - per-call:       model_price × group_ratio (fixed price per request)
//   - legacy map:     quota-per-1M ÷ quota_per_unit
type ModelPrice struct {
	Model    string `json:"model"`
	Currency string `json:"currency,omitempty"`
	// PriceUSD is set by the account service after conversion (input price).
	PriceUSD float64 `json:"price_usd,omitempty"`
	// OutputUSD is input × completion_ratio for token-billed models.
	OutputUSD float64 `json:"output_usd,omitempty"`
	// Mode is the billing mode: "fixed" (per-call), "token", or "legacy".
	Mode string `json:"mode,omitempty"`
	// Ratio is the raw New-API model_ratio (per-token billing multiplier).
	Ratio float64 `json:"ratio,omitempty"`
	// CompletionRatio is the raw New-API completion_ratio (output × ratio).
	CompletionRatio float64 `json:"completion_ratio,omitempty"`
	// QuotaType mirrors New-API quota_type: 0 = token billing, 1 = per-call.
	QuotaType int `json:"quota_type,omitempty"`
	// ModelPrice is the raw model_price (per-call fixed price when QuotaType=1).
	ModelPrice float64 `json:"model_price,omitempty"`
	// TokenUSD is the direct USD/1M price when the site has no ratio semantics.
	TokenUSD *TokenUSDPerMillion `json:"token_usd,omitempty"`
	// GroupRatio is the user-group multiplier from the pricing response
	// (defaults to 1 when the site has no group ratios).
	GroupRatio float64 `json:"group_ratio,omitempty"`
	// QuotaPer1M is the raw quota price per 1M tokens (legacy map format).
	QuotaPer1M float64 `json:"quota_per_1m,omitempty"`
}

// TokenUSDPerMillion is a direct USD-per-1M-token price for sites without
// New-API ratio semantics (mirrors AAH token_price_usd_per_million).
type TokenUSDPerMillion struct {
	Input      float64 `json:"input,omitempty"`
	Output     float64 `json:"output,omitempty"`
	CacheRead  float64 `json:"cache_read,omitempty"`
	CacheWrite float64 `json:"cache_write,omitempty"`
}

// AccountAdapter probes user identity and lists API keys for relay attachment.
type AccountAdapter interface {
	Name() string
	ProbeSelf(context.Context, AccountInput) (AccountSelf, error)
	ListTokenGroups(context.Context, AccountInput) ([]string, error)
	ListAPIKeys(context.Context, AccountInput, int, int) ([]UpstreamAPIKey, error)
	RevealAPIKey(context.Context, AccountInput, int64) (string, error)
	CreateAPIKey(context.Context, AccountInput, NewAPIKeyRequest) (UpstreamAPIKey, error)
	Pricing(context.Context, AccountInput) ([]ModelPrice, error)
	QuotaPerUnit(context.Context, AccountInput) (int64, error)
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

// QuotaPerUnit fetches the site's quota-per-unit conversion from /api/status
// (public endpoint). New API sites define 1 unit = N quota; prices are quoted
// in quota, so dividing by this value yields the price in site currency.
func (a *NewAPIAccountAdapter) QuotaPerUnit(ctx context.Context, input AccountInput) (int64, error) {
	endpoint, err := accountEndpoint(input.BaseURL, "/api/status")
	if err != nil {
		return 0, &Error{Kind: ErrorInvalidURL}
	}
	body, status, retryAfter, err := a.doJSON(ctx, http.MethodGet, endpoint, input)
	if err != nil {
		return 0, err
	}
	if status < 200 || status >= 300 {
		return 0, &Error{Kind: ErrorStatus, Status: status, RetryAfter: retryAfter}
	}
	var envelope struct {
		Success *bool `json:"success"`
		Data    struct {
			QuotaPerUnit int64 `json:"quota_per_unit"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return 0, &Error{Kind: ErrorPayload}
	}
	if envelope.Success != nil && !*envelope.Success {
		return 0, &Error{Kind: ErrorPayload}
	}
	if envelope.Data.QuotaPerUnit <= 0 {
		return 0, &Error{Kind: ErrorPayload}
	}
	return envelope.Data.QuotaPerUnit, nil
}

// Pricing fetches the site-wide model price table from the New-API family's
// /api/pricing endpoint. Values are either plain numbers (default currency) or
// {currency, price} objects.
func (a *NewAPIAccountAdapter) Pricing(ctx context.Context, input AccountInput) ([]ModelPrice, error) {
	endpoint, err := accountEndpoint(input.BaseURL, "/api/pricing")
	if err != nil {
		return nil, &Error{Kind: ErrorInvalidURL}
	}
	body, status, retryAfter, err := a.doJSON(ctx, http.MethodGet, endpoint, input)
	if err != nil {
		return nil, err
	}
	if status < 200 || status >= 300 {
		return nil, &Error{Kind: ErrorStatus, Status: status, RetryAfter: retryAfter}
	}
	var envelope struct {
		Success *bool           `json:"success"`
		Data    json.RawMessage `json:"data"`
		// GroupRatio is the top-level user-group multiplier map.
		GroupRatio map[string]float64 `json:"group_ratio"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, &Error{Kind: ErrorPayload}
	}
	if envelope.Success != nil && !*envelope.Success {
		return nil, &Error{Kind: ErrorPayload}
	}
	if len(envelope.Data) == 0 {
		return nil, nil
	}
	// Resolve the user-group multiplier: prefer "default", else the first
	// group ratio present, else 1 (mirrors AAH resolveGroupRatio).
	groupRatio := 1.0
	if ratio, ok := envelope.GroupRatio["default"]; ok && ratio > 0 {
		groupRatio = ratio
	} else {
		for _, ratio := range envelope.GroupRatio {
			if ratio > 0 {
				groupRatio = ratio
				break
			}
		}
	}
	var table map[string]json.RawMessage
	if err := json.Unmarshal(envelope.Data, &table); err == nil {
		// Legacy object form: {model: quota-per-1M} or {model: {currency, price}}.
		out := make([]ModelPrice, 0, len(table))
		for model, raw := range table {
			item := ModelPrice{Model: model}
			var plain float64
			if json.Unmarshal(raw, &plain) == nil {
				item.QuotaPer1M = plain
				item.Mode = "legacy"
				out = append(out, item)
				continue
			}
			var obj struct {
				Currency string  `json:"currency"`
				Price    float64 `json:"price"`
			}
			if json.Unmarshal(raw, &obj) == nil {
				item.Currency = obj.Currency
				item.PriceUSD = obj.Price
				item.Mode = "fixed"
				out = append(out, item)
			}
		}
		return out, nil
	}
	// New API v0.13+ list form: [{model_name, model_price, model_ratio, ...}]
	// with optional token_price_usd_per_million and top-level group_ratio.
	var list []struct {
		ModelName               string  `json:"model_name"`
		ModelPrice              float64 `json:"model_price"`
		ModelRatio              float64 `json:"model_ratio"`
		CompletionRatio         float64 `json:"completion_ratio"`
		QuotaType               int     `json:"quota_type"`
		Currency                string  `json:"currency"`
		TokenPriceUSDPerMillion *struct {
			Input      float64 `json:"input"`
			Output     float64 `json:"output"`
			CacheRead  float64 `json:"cache_read"`
			CacheWrite float64 `json:"cache_write"`
		} `json:"token_price_usd_per_million"`
	}
	if err := json.Unmarshal(envelope.Data, &list); err != nil || len(list) == 0 {
		return nil, &Error{Kind: ErrorPayload}
	}
	out := make([]ModelPrice, 0, len(list))
	for _, item := range list {
		name := strings.TrimSpace(item.ModelName)
		if name == "" {
			continue
		}
		// Unpriced (both 0 and no direct USD) → skip.
		var direct *TokenUSDPerMillion
		if item.TokenPriceUSDPerMillion != nil {
			direct = &TokenUSDPerMillion{
				Input:      item.TokenPriceUSDPerMillion.Input,
				Output:     item.TokenPriceUSDPerMillion.Output,
				CacheRead:  item.TokenPriceUSDPerMillion.CacheRead,
				CacheWrite: item.TokenPriceUSDPerMillion.CacheWrite,
			}
		}
		if item.ModelPrice <= 0 && item.ModelRatio <= 0 && (direct == nil || direct.Input <= 0) {
			continue
		}
		mode := "token"
		if item.QuotaType == 1 || item.ModelPrice > 0 && item.ModelRatio <= 0 {
			mode = "fixed"
		}
		out = append(out, ModelPrice{
			Model:           name,
			Currency:        strings.TrimSpace(item.Currency),
			PriceUSD:        item.ModelPrice, // fixed: site currency per request
			Ratio:           item.ModelRatio,
			CompletionRatio: item.CompletionRatio,
			QuotaType:       item.QuotaType,
			ModelPrice:      item.ModelPrice,
			TokenUSD:        direct,
			GroupRatio:      groupRatio,
			Mode:            mode,
		})
	}
	return out, nil
}

// ListTokenGroups returns every group the account may use, from the New-API
// family's /api/user/self/groups endpoint. This reports usable groups even
// when the account holds no tokens yet (token-list enumeration would be empty).
func (a *NewAPIAccountAdapter) ListTokenGroups(ctx context.Context, input AccountInput) ([]string, error) {
	endpoint, err := accountEndpoint(input.BaseURL, "/api/user/self/groups")
	if err != nil {
		return nil, &Error{Kind: ErrorInvalidURL}
	}
	body, status, retryAfter, err := a.doJSON(ctx, http.MethodGet, endpoint, input)
	if err != nil {
		return nil, err
	}
	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		// Same user-id header requirement as the token list on some forks.
		if retry, retryable := a.alternateUserHeaderInput(ctx, input); retryable {
			if body2, status2, _, err2 := a.doJSON(ctx, http.MethodGet, endpoint, retry); err2 == nil && status2 >= 200 && status2 < 300 {
				body, status = body2, status2
			}
		}
	}
	if status < 200 || status >= 300 {
		return nil, &Error{Kind: ErrorStatus, Status: status, RetryAfter: retryAfter}
	}
	var envelope struct {
		Success *bool           `json:"success"`
		Data    json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, &Error{Kind: ErrorPayload}
	}
	if envelope.Success != nil && !*envelope.Success {
		return nil, &Error{Kind: ErrorPayload}
	}
	// Canonical shape: data is a string array. Some forks nest {groups: [...]},
	// return an object array [{name: ...}], or an object map {groupName: {...}}
	// (e.g. Ark / 42w); a few put groups at the top level.
	var plain []string
	if err := json.Unmarshal(envelope.Data, &plain); err == nil {
		return plain, nil
	}
	var nested struct {
		Groups []string `json:"groups"`
	}
	if err := json.Unmarshal(envelope.Data, &nested); err == nil && len(nested.Groups) > 0 {
		return nested.Groups, nil
	}
	var objectMap map[string]json.RawMessage
	if err := json.Unmarshal(envelope.Data, &objectMap); err == nil && len(objectMap) > 0 {
		groups := make([]string, 0, len(objectMap))
		for name := range objectMap {
			groups = append(groups, name)
		}
		sort.Strings(groups)
		// Keep "default" first so the picker preselects it.
		for index, name := range groups {
			if name == "default" && index > 0 {
				groups = append(groups[:index], groups[index+1:]...)
				groups = append([]string{"default"}, groups...)
				break
			}
		}
		return groups, nil
	}
	var objects []struct {
		Name      string `json:"name"`
		GroupName string `json:"group_name"`
		Value     string `json:"value"`
	}
	if err := json.Unmarshal(envelope.Data, &objects); err == nil && len(objects) > 0 {
		groups := make([]string, 0, len(objects))
		for _, item := range objects {
			groups = append(groups, firstNonEmpty(item.Name, item.GroupName, item.Value))
		}
		return groups, nil
	}
	var topLevel struct {
		Groups []string `json:"groups"`
	}
	if err := json.Unmarshal(body, &topLevel); err == nil && len(topLevel.Groups) > 0 {
		return topLevel.Groups, nil
	}
	return nil, &Error{Kind: ErrorPayload}
}

func (a *NewAPIAccountAdapter) ProbeSelf(ctx context.Context, input AccountInput) (AccountSelf, error) {
	endpoint, err := accountEndpoint(input.BaseURL, "/api/user/self")
	if err != nil {
		return AccountSelf{}, &Error{Kind: ErrorInvalidURL}
	}
	body, status, retryAfter, err := a.doJSON(ctx, http.MethodGet, endpoint, input)
	if err != nil {
		return AccountSelf{}, err
	}
	if status < 200 || status >= 300 {
		return AccountSelf{}, &Error{Kind: ErrorStatus, Status: status, RetryAfter: retryAfter}
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
	body, status, retryAfter, err := a.doJSON(ctx, http.MethodGet, endpoint, input)
	if err != nil {
		return nil, err
	}
	if status == http.StatusUnauthorized || status == http.StatusForbidden || isTokenListRejected(body) {
		// Some forks gate /api/token/ on the user-id compat headers
		// (New-Api-User et al.) and reject requests without them; a few
		// reject the headers entirely (e.g. a stale stored user id). Retry
		// once with the opposite header configuration before giving up.
		if retry, retryable := a.alternateUserHeaderInput(ctx, input); retryable {
			if body2, status2, _, err2 := a.doJSON(ctx, http.MethodGet, endpoint, retry); err2 == nil &&
				status2 >= 200 && status2 < 300 && !isTokenListRejected(body2) {
				return a.listAPIKeysFromBody(ctx, endpoint, body2, page, retry)
			}
		}
	}
	if status < 200 || status >= 300 {
		return nil, &Error{Kind: ErrorStatus, Status: status, RetryAfter: retryAfter}
	}
	// Some New-API forks answer 200 with {"success":false} when the access
	// token is invalid/expired. Surface that as an auth failure instead of a
	// misleading "no tokens" empty list.
	if isTokenListRejected(body) {
		return nil, &Error{Kind: ErrorStatus, Status: http.StatusUnauthorized}
	}
	return a.listAPIKeysFromBody(ctx, endpoint, body, page, input)
}

// listAPIKeysFromBody parses a token-list body, retrying with the p=1 page
// index for forks that are 1-based.
func (a *NewAPIAccountAdapter) listAPIKeysFromBody(ctx context.Context, endpoint string, body []byte, page int, input AccountInput) ([]UpstreamAPIKey, error) {
	keys := parseTokenList(body)
	// Some New-API forks are 1-based for page index.
	if len(keys) == 0 && page == 0 {
		query := url.Values{}
		query.Set("p", "1")
		query.Set("size", "100")
		alt := strings.Split(endpoint, "?")[0] + "?" + query.Encode()
		if body2, status2, _, err2 := a.doJSON(ctx, http.MethodGet, alt, input); err2 == nil && status2 >= 200 && status2 < 300 {
			if isTokenListRejected(body2) {
				return nil, &Error{Kind: ErrorStatus, Status: http.StatusUnauthorized}
			}
			keys = parseTokenList(body2)
		}
	}
	return keys, nil
}

// alternateUserHeaderInput returns an AccountInput with the opposite user-id
// header configuration of the current one. Returns retryable=false when no
// useful alternative exists (e.g. headers are wanted but no user id is known
// and cannot be resolved).
func (a *NewAPIAccountAdapter) alternateUserHeaderInput(ctx context.Context, input AccountInput) (AccountInput, bool) {
	carried := !input.NoUserHeader && (a.userHeader || input.UserHeader)
	retry := input
	retry.NoUserHeader = carried
	retry.UserHeader = !carried
	if carried {
		return retry, true
	}
	if retry.PlatformUserID <= 0 {
		if self, selfErr := a.ProbeSelf(ctx, input); selfErr == nil && self.PlatformUserID > 0 {
			retry.PlatformUserID = self.PlatformUserID
			return retry, true
		}
		return retry, false
	}
	return retry, true
}

// isTokenListRejected reports whether a /api/token/ response body carries
// success:false, which means the upstream rejected the access token itself
// rather than simply having an empty token list.
func isTokenListRejected(body []byte) bool {
	var envelope struct {
		Success *bool  `json:"success"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return false
	}
	if envelope.Success != nil && !*envelope.Success {
		return true
	}
	return false
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
		body, status, _, reqErr := a.doJSON(ctx, method, endpoint, input)
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

// CreateAPIKey creates a token on a New API family host via POST /api/token/.
// The response carries the freshly generated sk- secret exactly once.
func (a *NewAPIAccountAdapter) CreateAPIKey(ctx context.Context, input AccountInput, request NewAPIKeyRequest) (UpstreamAPIKey, error) {
	endpoint, err := accountEndpoint(input.BaseURL, "/api/token/")
	if err != nil {
		return UpstreamAPIKey{}, &Error{Kind: ErrorInvalidURL}
	}
	payload := map[string]any{
		"name":                 strings.TrimSpace(request.Name),
		"remain_quota":         request.Quota,
		"expired_time":         request.ExpiredAtUnix,
		"model_limits_enabled": request.ModelLimits != "",
		"model_limits":         request.ModelLimits,
		"group":                strings.TrimSpace(request.Group),
		"unlimited_quota":      request.UnlimitedQuota,
	}
	if request.UnlimitedQuota {
		payload["remain_quota"] = -1
	}
	if request.ExpiredAtUnix == 0 {
		payload["expired_time"] = -1
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return UpstreamAPIKey{}, &Error{Kind: ErrorPayload}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(string(encoded)))
	if err != nil {
		return UpstreamAPIKey{}, &Error{Kind: ErrorInvalidURL}
	}
	req.Header.Set("Authorization", "Bearer "+input.Secret)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if !input.NoUserHeader && (a.userHeader || input.UserHeader) {
		ApplyCompatUserIDHeaders(req.Header, input.PlatformUserID)
	}
	resp, err := a.client.Do(req)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return UpstreamAPIKey{}, err
		}
		return UpstreamAPIKey{}, &Error{Kind: ErrorTransport}
	}
	defer resp.Body.Close()
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, maxAccountResponseBytes+1))
	if readErr != nil {
		return UpstreamAPIKey{}, &Error{Kind: ErrorTransport}
	}
	if len(body) > maxAccountResponseBytes {
		return UpstreamAPIKey{}, &Error{Kind: ErrorTooLarge}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return UpstreamAPIKey{}, &Error{Kind: ErrorStatus, Status: resp.StatusCode, RetryAfter: retryAfterFromHeader(resp.Header)}
	}
	var payload2 struct {
		Success *bool `json:"success"`
		Data    struct {
			ID        int64  `json:"id"`
			Name      string `json:"name"`
			Key       string `json:"key"`
			FullKey   string `json:"full_key"`
			Token     string `json:"token"`
			GroupName string `json:"group"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &payload2); err != nil {
		return UpstreamAPIKey{}, &Error{Kind: ErrorPayload}
	}
	if payload2.Success != nil && !*payload2.Success {
		return UpstreamAPIKey{}, &Error{Kind: ErrorStatus, Status: resp.StatusCode, RetryAfter: retryAfterFromHeader(resp.Header)}
	}
	secret := strings.TrimSpace(payload2.Data.FullKey)
	if secret == "" {
		secret = strings.TrimSpace(payload2.Data.Key)
	}
	if secret == "" {
		secret = strings.TrimSpace(payload2.Data.Token)
	}
	if looksMasked(secret) {
		secret = ""
	}
	return UpstreamAPIKey{
		ID:     payload2.Data.ID,
		Name:   strings.TrimSpace(payload2.Data.Name),
		Group:  strings.TrimSpace(payload2.Data.GroupName),
		Secret: secret,
		Status: 1,
	}, nil
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

func (a *NewAPIAccountAdapter) doJSON(ctx context.Context, method, endpoint string, input AccountInput) ([]byte, int, time.Duration, error) {
	req, err := http.NewRequestWithContext(ctx, method, endpoint, nil)
	if err != nil {
		return nil, 0, 0, &Error{Kind: ErrorInvalidURL}
	}
	req.Header.Set("Authorization", "Bearer "+input.Secret)
	req.Header.Set("Accept", "application/json")
	if !input.NoUserHeader && (a.userHeader || input.UserHeader) {
		ApplyCompatUserIDHeaders(req.Header, input.PlatformUserID)
	}
	resp, err := a.client.Do(req)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, 0, 0, err
		}
		return nil, 0, 0, &Error{Kind: ErrorTransport}
	}
	defer resp.Body.Close()
	limited := io.LimitReader(resp.Body, maxAccountResponseBytes+1)
	body, err := io.ReadAll(limited)
	if err != nil {
		return nil, 0, 0, &Error{Kind: ErrorTransport}
	}
	if len(body) > maxAccountResponseBytes {
		return nil, 0, 0, &Error{Kind: ErrorTooLarge}
	}
	return body, resp.StatusCode, retryAfterFromHeader(resp.Header), nil
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
