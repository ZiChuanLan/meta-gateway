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
// The base can be a bare host, an API root, or a MOUNT PREFIX, and the join
// differs for the last two:
//
//	https://api.deepseek.com      → /v1/chat/completions  (no path: the
//	                              conventional /v1 root is added)
//	…/v1, …/openai/v1, …/v1beta   → the path goes UNDER that root
//	…/api/paas/v4, …/api/v3, …/v2 → also UNDER that root (the vendor's own)
//	…/ok, …/proxy/upstream        → /ok/v1/chat/completions (mount prefix: the
//	                              /v1 root still belongs between the two)
//
// A base whose last segment is version-shaped is already an API root, so the path
// is appended directly. Otherwise the segment is a mount prefix shared with the
// platform's other surfaces, so the conventional /v1 root goes after it.
//
// Two simpler rules were each wrong, and the second shipped before CI caught it —
// both are recorded here so neither is reintroduced:
//
//   - Looking only for a `/v1` SUFFIX broke every vendor with a differently named
//     root, e.g. the Zhipu preset requested `/api/paas/v4/v1/chat/completions`.
//   - Treating ANY path as an API root broke mount-prefix channels: a channel
//     whose upstream serves `/ok/v1/chat/completions` was sent to
//     `/ok/chat/completions`.
//
// A pasted complete endpoint never reaches the non-root branch below: it is peeled
// off by SplitEndpointBaseURL at save time (see carriesEndpoint).
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
	switch {
	case basePath == "":
		parsed.Path = "/v1/" + rel
	case isAPIRootPath(basePath):
		parsed.Path = basePath + "/" + rel
	default:
		parsed.Path = basePath + "/v1/" + rel
	}
	return parsed.String(), nil
}
