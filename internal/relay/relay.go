// Package relay proxies HTTP requests to upstream channels.
package relay

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Relay holds the configuration for forwarding requests.
type Relay struct {
	client *http.Client
}

// New creates a Relay with sensible timeouts.
func New() *Relay {
	return &Relay{
		client: &http.Client{
			Transport: &http.Transport{
				Proxy:                 http.ProxyFromEnvironment,
				ResponseHeaderTimeout: 60 * time.Second,
				IdleConnTimeout:       90 * time.Second,
			},
		},
	}
}

// Result captures the outcome of a relayed request.
type Result struct {
	StatusCode int
	Body       io.ReadCloser
	Header     http.Header
	LatencyMs  int
	Err        error
}

// ChatCompletions forwards a /v1/chat/completions request to the given upstream.
func (r *Relay) ChatCompletions(upstreamURL string, apiKey string, reqBody []byte, stream bool) *Result {
	return r.ChatCompletionsContext(context.Background(), upstreamURL, apiKey, reqBody, stream)
}

// ChatCompletionsContext forwards a request and propagates cancellation.
func (r *Relay) ChatCompletionsContext(ctx context.Context, upstreamURL string, apiKey string, reqBody []byte, stream bool) *Result {
	start := time.Now()

	var bodyReader io.Reader
	if stream {
		bodyReader = bytes.NewReader(reqBody)
	} else {
		bodyReader = bytes.NewReader(reqBody)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, upstreamURL, bodyReader)
	if err != nil {
		return &Result{Err: fmt.Errorf("relay: create request: %w", err)}
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+apiKey)

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

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("relay: read models: %w", err)
	}

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("relay: models status %d: %s", resp.StatusCode, string(body))
	}

	return body, nil
}

// DecodeJSONRequestBody decodes a JSON request body safely (with max size limit).
func DecodeJSONRequestBody(r *http.Request, maxBytes int64, target interface{}) error {
	if maxBytes <= 0 {
		maxBytes = 10 * 1024 * 1024 // 10 MB default
	}
	r.Body = http.MaxBytesReader(nil, r.Body, maxBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(target); err != nil {
		return fmt.Errorf("relay: decode body: %w", err)
	}
	return nil
}
