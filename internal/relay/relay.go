// Package relay proxies HTTP requests to upstream channels.
package relay

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

const maxModelsBodyBytes = 2 << 20

// Relay holds the configuration for forwarding requests.
type Relay struct {
	client *http.Client
}

// New creates a Relay with sensible timeouts.
func New() *Relay {
	return NewWithClient(&http.Client{Transport: &http.Transport{
		ResponseHeaderTimeout: 60 * time.Second,
		IdleConnTimeout:       90 * time.Second,
	}})
}

// NewWithClient creates a Relay using an injected outbound client.
func NewWithClient(client *http.Client) *Relay {
	if client == nil {
		return New()
	}
	return &Relay{client: client}
}

// Result captures the outcome of a relayed request.
type Result struct {
	StatusCode int
	Body       io.ReadCloser
	Header     http.Header
	LatencyMs  int
	// FirstByteMs is the time from relay start to the first streamed byte
	// (stream responses only; 0 when unknown). Populated by the proxy after
	// its first-chunk peek.
	FirstByteMs int
	Err         error
}

// ChatCompletions forwards a /v1/chat/completions request to the given upstream.
func (r *Relay) ChatCompletions(upstreamURL string, apiKey string, reqBody []byte, stream bool) *Result {
	return r.ChatCompletionsContext(context.Background(), upstreamURL, apiKey, reqBody, stream)
}

// ChatCompletionsContext forwards a request and propagates cancellation.
func (r *Relay) ChatCompletionsContext(ctx context.Context, upstreamURL string, apiKey string, reqBody []byte, stream bool) *Result {
	return r.ForwardContext(ctx, http.MethodPost, upstreamURL, apiKey, reqBody)
}

// ForwardContext sends an OpenAI-style Bearer-authenticated upstream request.
func (r *Relay) ForwardContext(ctx context.Context, method, upstreamURL, apiKey string, reqBody []byte) *Result {
	headers := http.Header{}
	headers.Set("Authorization", "Bearer "+apiKey)
	return r.ForwardWithHeaders(ctx, method, upstreamURL, headers, reqBody)
}

// ForwardWithHeaders sends an upstream request with caller-provided headers.
// Content-Type is set to application/json when a body is present and not already set.
func (r *Relay) ForwardWithHeaders(ctx context.Context, method, upstreamURL string, headers http.Header, reqBody []byte) *Result {
	start := time.Now()
	if method == "" {
		method = http.MethodPost
	}
	var bodyReader io.Reader
	if len(reqBody) > 0 || method == http.MethodPost || method == http.MethodPut || method == http.MethodPatch {
		bodyReader = bytes.NewReader(reqBody)
	}
	httpReq, err := http.NewRequestWithContext(ctx, method, upstreamURL, bodyReader)
	if err != nil {
		return &Result{Err: fmt.Errorf("relay: create request: %w", err)}
	}
	if bodyReader != nil {
		if headers == nil || headers.Get("Content-Type") == "" {
			httpReq.Header.Set("Content-Type", "application/json")
		}
	}
	for key, values := range headers {
		for _, value := range values {
			httpReq.Header.Add(key, value)
		}
	}

	resp, err := r.client.Do(httpReq)
	latency := int(time.Since(start).Milliseconds())
	if err != nil {
		return &Result{LatencyMs: latency, Err: fmt.Errorf("relay: do: %w", err)}
	}

	return &Result{
		StatusCode: resp.StatusCode,
		Body:       resp.Body,
		Header:     resp.Header.Clone(),
		LatencyMs:  latency,
	}
}

// Models fetches the list of models from the upstream.
func (r *Relay) Models(upstreamURL string, apiKey string) ([]byte, error) {
	baseURL := upstreamURL
	// If the URL already has a path, use as-is; otherwise append /v1/models
	modelsURL := baseURL + "/v1/models"

	req, err := http.NewRequest(http.MethodGet, modelsURL, nil)
	if err != nil {
		return nil, fmt.Errorf("relay: models request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)

	resp, err := r.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("relay: models do: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxModelsBodyBytes+1))
	if err != nil {
		return nil, fmt.Errorf("relay: read models: %w", err)
	}
	if len(body) > maxModelsBodyBytes {
		return nil, errors.New("relay: models response too large")
	}

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("relay: models status %d", resp.StatusCode)
	}

	return body, nil
}
