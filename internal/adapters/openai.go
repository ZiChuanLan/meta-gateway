package adapters

import (
	"bytes"
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
	return parseModelList(body)
}

// parseModelList reads the model names out of a `GET /models` body.
//
// OpenAI's {"data":[{"id":"gpt-4"}]} is the shape everything is written
// against, but it is not the only list an upstream that means to be
// OpenAI-compatible actually serves. Accepting only that one turned a working
// upstream into `invalid_payload`, which the console reported as "check Base
// URL, connection type and credentials" — so the operator re-typed the URL and
// rotated the key while the real cause sat in a JSON key name. Measured shapes:
//
//	{"data":[{"id":"…"}]}       OpenAI and every fork of it
//	{"data":[{"name":"…"}]}     FastAPI-style re-implementations
//	{"models":[{"name":"…"}]}   TypeSafe System One (verified 2026-09-23)
//	{"models":["…"]}            flat name lists
//
// The union is deliberately still "this is a model list", not "any JSON": a
// body that matches none of the shapes, or one whose entries carry no usable
// name ({"data":[{"id":123}]}), is still an ErrorPayload so a genuinely broken
// upstream keeps failing loudly. An empty but well-formed list stays a
// successful answer — a credential may simply expose no models.
func parseModelList(body []byte) ([]string, error) {
	var payload struct {
		Data   json.RawMessage `json:"data"`
		Models json.RawMessage `json:"models"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, &Error{Kind: ErrorPayload}
	}
	// `data` first: when an upstream carries both, the OpenAI one is the
	// surface clients are supposed to read.
	for _, raw := range []json.RawMessage{payload.Data, payload.Models} {
		names, ok := decodeModelItems(raw)
		if !ok {
			continue
		}
		unique := make(map[string]struct{}, len(names))
		for _, name := range names {
			unique[name] = struct{}{}
		}
		models := make([]string, 0, len(unique))
		for model := range unique {
			models = append(models, model)
		}
		sort.Strings(models)
		return models, nil
	}
	return nil, &Error{Kind: ErrorPayload}
}

// decodeModelItems reads one model-list container. ok is false when the node is
// absent/unparseable, or when it carries entries but none of them names a model
// — both mean "this is not a model list", which the caller turns into
// ErrorPayload.
func decodeModelItems(raw json.RawMessage) ([]string, bool) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || string(trimmed) == "null" {
		return nil, false
	}
	// A flat list of names is unambiguous, so try it first: it either decodes
	// or it does not.
	var flat []string
	if err := json.Unmarshal(trimmed, &flat); err == nil {
		names := make([]string, 0, len(flat))
		for _, name := range flat {
			if cleaned := strings.TrimSpace(name); cleaned != "" {
				names = append(names, cleaned)
			}
		}
		return names, true
	}
	var items []struct {
		ID   any `json:"id"`
		Name any `json:"name"`
	}
	if err := json.Unmarshal(trimmed, &items); err != nil {
		return nil, false
	}
	names := make([]string, 0, len(items))
	for _, item := range items {
		// `id` wins when both are present: it is the OpenAI field, and an
		// upstream that carries a display `name` next to it still keys on id.
		for _, candidate := range []any{item.ID, item.Name} {
			if name, isString := candidate.(string); isString {
				if cleaned := strings.TrimSpace(name); cleaned != "" {
					names = append(names, cleaned)
					break
				}
			}
		}
	}
	if len(items) > 0 && len(names) == 0 {
		return nil, false
	}
	return names, true
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
