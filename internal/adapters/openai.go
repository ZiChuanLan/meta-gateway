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
	"strings"
	"time"
)

const maxModelResponseBytes = 2 << 20

type ErrorKind string

const (
	ErrorInvalidURL ErrorKind = "invalid_url"
	ErrorTransport  ErrorKind = "transport"
	ErrorStatus     ErrorKind = "upstream_status"
	ErrorTooLarge   ErrorKind = "response_too_large"
	ErrorPayload    ErrorKind = "invalid_payload"
)

// Error intentionally contains no URL, response body, or credential material.
type Error struct {
	Kind   ErrorKind
	Status int
	// RetryAfter is the upstream Retry-After hint (seconds) when the failure
	// is a rate limit; 0 when unknown.
	RetryAfter time.Duration
}

func (e *Error) Error() string {
	if e.Status != 0 {
		return fmt.Sprintf("model discovery failed: %s (%d)", e.Kind, e.Status)
	}
	return fmt.Sprintf("model discovery failed: %s", e.Kind)
}

type OpenAIModelAdapter struct {
	name   string
	client *http.Client
}

func NewOpenAIModelAdapter(name string, client *http.Client) *OpenAIModelAdapter {
	if client == nil {
		client = &http.Client{Transport: &http.Transport{ResponseHeaderTimeout: 15 * time.Second}}
	}
	return &OpenAIModelAdapter{name: name, client: client}
}

func (a *OpenAIModelAdapter) Name() string { return a.name }

func (a *OpenAIModelAdapter) ListModels(ctx context.Context, baseURL, apiKey string) ([]string, error) {
	endpoint, err := modelEndpoint(baseURL)
	if err != nil {
		return nil, &Error{Kind: ErrorInvalidURL}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, &Error{Kind: ErrorInvalidURL}
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Accept", "application/json")
	resp, err := a.client.Do(req)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, err
		}
		return nil, &Error{Kind: ErrorTransport}
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &Error{Kind: ErrorStatus, Status: resp.StatusCode, RetryAfter: retryAfterFromHeader(resp.Header)}
	}

	limited := io.LimitReader(resp.Body, maxModelResponseBytes+1)
	body, err := io.ReadAll(limited)
	if err != nil {
		return nil, &Error{Kind: ErrorTransport}
	}
	if len(body) > maxModelResponseBytes {
		return nil, &Error{Kind: ErrorTooLarge}
	}
	var payload struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(body, &payload); err != nil || len(payload.Data) == 0 || string(payload.Data) == "null" {
		return nil, &Error{Kind: ErrorPayload}
	}
	var items []struct {
		ID any `json:"id"`
	}
	if err := json.Unmarshal(payload.Data, &items); err != nil || items == nil {
		return nil, &Error{Kind: ErrorPayload}
	}
	unique := make(map[string]struct{}, len(items))
	for _, item := range items {
		id, ok := item.ID.(string)
		if !ok {
			return nil, &Error{Kind: ErrorPayload}
		}
		id = strings.TrimSpace(id)
		if id != "" {
			unique[id] = struct{}{}
		}
	}
	models := make([]string, 0, len(unique))
	for model := range unique {
		models = append(models, model)
	}
	sort.Strings(models)
	return models, nil
}

func modelEndpoint(baseURL string) (string, error) {
	return JoinOpenAIPath(baseURL, "models")
}

// JoinOpenAIPath joins an OpenAI-compatible base URL with a path under /v1.
//
// Two base shapes are in the wild and they behave differently on purpose:
//
//	https://api.deepseek.com      → /v1/chat/completions   (no path: the
//	                               conventional /v1 root is added)
//	…/v1, …/openai/v1, …/v1beta   → the path is appended under that root
//	…/api/paas/v4, …/api/v3, …/v2 → appended under the vendor's own root
//
// The last two cases are the same rule: a path segment is only ever an API ROOT,
// never a piece of one endpoint. A provider whose base is `/api/paas/v4` or
// `/api/v3` serves `/chat/completions` UNDER it, so inserting another `/v1`
// produced the non-existent `/api/paas/v4/v1/chat/completions` — measured, not
// theorised: every Zhipu/Volcengine/Qianfan preset in the type dropdown was
// unusable, which is why an operator had to fall back to hand-written endpoint
// overrides.
//
// A base with NO path is the one genuinely ambiguous shape. It keeps the /v1
// root, because that is what the OpenAI-compatible world overwhelmingly serves.
// A provider whose documented base has no /v1 is expressed by pasting the
// documented ENDPOINT instead (`https://api.perplexity.ai/chat/completions`),
// which SplitEndpointBaseURL turns into root + endpoint override. That is a
// deliberate choice over a host allowlist: a host list is unverifiable for
// self-hosted mirrors and proxy domains, while a documented path is checkable
// and visible to the operator in the endpoint preview.
func JoinOpenAIPath(baseURL, path string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || !parsed.IsAbs() || parsed.Host == "" || parsed.User != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return "", errors.New("invalid base URL")
	}
	parsed.RawQuery = ""
	parsed.Fragment = ""
	rel := strings.Trim(strings.TrimSpace(path), "/")
	if rel == "" {
		return "", errors.New("invalid base URL")
	}
	basePath := strings.TrimRight(parsed.Path, "/")
	if basePath == "" {
		parsed.Path = "/v1/" + rel
	} else {
		parsed.Path = basePath + "/" + rel
	}
	return parsed.String(), nil
}
