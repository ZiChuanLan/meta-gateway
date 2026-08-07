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

const (
	// AnthropicAPIVersion is required by Anthropic Messages API.
	AnthropicAPIVersion = "2023-06-01"
	// DefaultAnthropicMaxTokens is used when chat/completions omits max_tokens.
	DefaultAnthropicMaxTokens = 4096
)

// AnthropicAuthHeaders builds headers for Anthropic official / Messages-compatible hosts.
func AnthropicAuthHeaders(apiKey string) http.Header {
	headers := make(http.Header)
	headers.Set("x-api-key", apiKey)
	headers.Set("anthropic-version", AnthropicAPIVersion)
	headers.Set("Content-Type", "application/json")
	return headers
}

// JoinAnthropicPath joins a base URL with an Anthropic API path (e.g. "messages", "models").
// Unlike OpenAI helpers, it does not force an extra /v1 segment when the base already
// ends with /v1; bare hosts get /v1/<path>.
func JoinAnthropicPath(baseURL, path string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || !parsed.IsAbs() || parsed.Host == "" || parsed.User != nil ||
		(parsed.Scheme != "http" && parsed.Scheme != "https") {
		return "", errors.New("invalid base URL")
	}
	parsed.RawQuery = ""
	parsed.Fragment = ""
	rel := strings.Trim(strings.TrimSpace(path), "/")
	if rel == "" {
		return "", errors.New("invalid base URL")
	}
	basePath := strings.TrimRight(parsed.Path, "/")
	lower := strings.ToLower(basePath)
	if lower == "/v1" || strings.HasSuffix(lower, "/v1") {
		parsed.Path = basePath + "/" + rel
	} else if basePath == "" {
		parsed.Path = "/v1/" + rel
	} else if strings.HasSuffix(lower, "/"+rel) || lower == "/"+rel {
		parsed.Path = basePath
	} else {
		parsed.Path = basePath + "/v1/" + rel
	}
	return parsed.String(), nil
}

// IsAnthropicFamily reports whether a type hint / platform uses Anthropic protocol.
func IsAnthropicFamily(typeHint, platform string) bool {
	return CanonicalType(firstNonEmpty(typeHint, platform)) == "anthropic"
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

// ChatToAnthropicMessages converts an OpenAI chat/completions body into Anthropic Messages JSON.
func ChatToAnthropicMessages(openaiBody []byte) ([]byte, error) {
	var incoming struct {
		Model       string          `json:"model"`
		Messages    []chatMessage   `json:"messages"`
		MaxTokens   *int            `json:"max_tokens"`
		Temperature *float64        `json:"temperature"`
		TopP        *float64        `json:"top_p"`
		Stream      bool            `json:"stream"`
		Stop        json.RawMessage `json:"stop"`
		System      json.RawMessage `json:"system"`
	}
	if err := json.Unmarshal(openaiBody, &incoming); err != nil {
		return nil, fmt.Errorf("anthropic: decode chat body: %w", err)
	}
	if strings.TrimSpace(incoming.Model) == "" {
		return nil, errors.New("anthropic: model is required")
	}

	systemParts := make([]string, 0, 2)
	if len(incoming.System) > 0 && string(incoming.System) != "null" {
		var systemText string
		if err := json.Unmarshal(incoming.System, &systemText); err == nil {
			if text := strings.TrimSpace(systemText); text != "" {
				systemParts = append(systemParts, text)
			}
		}
	}

	messages := make([]map[string]any, 0, len(incoming.Messages))
	for _, message := range incoming.Messages {
		role := strings.ToLower(strings.TrimSpace(message.Role))
		text := messageContentText(message.Content)
		switch role {
		case "system", "developer":
			if text != "" {
				systemParts = append(systemParts, text)
			}
		case "assistant":
			messages = append(messages, map[string]any{
				"role":    "assistant",
				"content": text,
			})
		case "user", "":
			messages = append(messages, map[string]any{
				"role":    "user",
				"content": text,
			})
		default:
			// tool / function roles are not mapped in v1; keep as user text for best effort.
			messages = append(messages, map[string]any{
				"role":    "user",
				"content": text,
			})
		}
	}
	if len(messages) == 0 {
		return nil, errors.New("anthropic: messages are required")
	}

	maxTokens := DefaultAnthropicMaxTokens
	if incoming.MaxTokens != nil && *incoming.MaxTokens > 0 {
		maxTokens = *incoming.MaxTokens
	}

	outbound := map[string]any{
		"model":      incoming.Model,
		"messages":   messages,
		"max_tokens": maxTokens,
		"stream":     incoming.Stream,
	}
	if len(systemParts) > 0 {
		outbound["system"] = strings.Join(systemParts, "\n\n")
	}
	if incoming.Temperature != nil {
		outbound["temperature"] = *incoming.Temperature
	}
	if incoming.TopP != nil {
		outbound["top_p"] = *incoming.TopP
	}
	if len(incoming.Stop) > 0 && string(incoming.Stop) != "null" {
		var stopOne string
		var stopMany []string
		if err := json.Unmarshal(incoming.Stop, &stopOne); err == nil && stopOne != "" {
			outbound["stop_sequences"] = []string{stopOne}
		} else if err := json.Unmarshal(incoming.Stop, &stopMany); err == nil && len(stopMany) > 0 {
			outbound["stop_sequences"] = stopMany
		}
	}
	return json.Marshal(outbound)
}

type chatMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

func messageContentText(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var asString string
	if err := json.Unmarshal(raw, &asString); err == nil {
		return asString
	}
	var parts []map[string]any
	if err := json.Unmarshal(raw, &parts); err == nil {
		var builder strings.Builder
		for _, part := range parts {
			if typ, _ := part["type"].(string); typ == "text" {
				if text, ok := part["text"].(string); ok {
					builder.WriteString(text)
				}
			}
		}
		return builder.String()
	}
	return strings.TrimSpace(string(raw))
}

func AnthropicMessagesToChat(anthropicBody []byte) ([]byte, error) {
	var incoming struct {
		ID      string `json:"id"`
		Model   string `json:"model"`
		Role    string `json:"role"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		StopReason string `json:"stop_reason"`
		Usage      struct {
			InputTokens              int `json:"input_tokens"`
			OutputTokens             int `json:"output_tokens"`
			CacheReadInputTokens     int `json:"cache_read_input_tokens"`
			CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(anthropicBody, &incoming); err != nil {
		return nil, fmt.Errorf("anthropic: decode messages response: %w", err)
	}
	var content strings.Builder
	for _, part := range incoming.Content {
		if part.Type == "text" {
			content.WriteString(part.Text)
		}
	}
	finishReason := mapAnthropicStopReason(incoming.StopReason)
	usage := map[string]any{
		"prompt_tokens":     incoming.Usage.InputTokens,
		"completion_tokens": incoming.Usage.OutputTokens,
		"total_tokens":      incoming.Usage.InputTokens + incoming.Usage.OutputTokens,
	}
	if incoming.Usage.CacheReadInputTokens > 0 {
		usage["cache_read_tokens"] = incoming.Usage.CacheReadInputTokens
	}
	if incoming.Usage.CacheCreationInputTokens > 0 {
		usage["cache_creation_tokens"] = incoming.Usage.CacheCreationInputTokens
	}
	outbound := map[string]any{
		"id":      incoming.ID,
		"object":  "chat.completion",
		"created": nowUnix(),
		"model":   incoming.Model,
		"choices": []map[string]any{{
			"index": 0,
			"message": map[string]any{
				"role":    "assistant",
				"content": content.String(),
			},
			"finish_reason": finishReason,
		}},
		"usage": usage,
	}
	return json.Marshal(outbound)
}

// AnthropicModelAdapter lists models from Anthropic-compatible GET /v1/models.
type AnthropicModelAdapter struct {
	name   string
	client *http.Client
}

func NewAnthropicModelAdapter(name string, client *http.Client) *AnthropicModelAdapter {
	if client == nil {
		client = &http.Client{Transport: &http.Transport{ResponseHeaderTimeout: 15 * time.Second}}
	}
	return &AnthropicModelAdapter{name: name, client: client}
}

func (a *AnthropicModelAdapter) Name() string { return a.name }

func (a *AnthropicModelAdapter) ListModels(ctx context.Context, baseURL, apiKey string) ([]string, error) {
	endpoint, err := JoinAnthropicPath(baseURL, "models")
	if err != nil {
		return nil, &Error{Kind: ErrorInvalidURL}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, &Error{Kind: ErrorInvalidURL}
	}
	req.Header.Set("x-api-key", apiKey)
	req.Header.Set("anthropic-version", AnthropicAPIVersion)
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
		return nil, &Error{Kind: ErrorStatus, Status: resp.StatusCode}
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxModelResponseBytes+1))
	if err != nil {
		return nil, &Error{Kind: ErrorTransport}
	}
	if len(body) > maxModelResponseBytes {
		return nil, &Error{Kind: ErrorTooLarge}
	}
	var payload struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		// Some Anthropic-compatible hosts wrap differently; try models array.
		var alt struct {
			Models []struct {
				ID string `json:"id"`
			} `json:"models"`
		}
		if err2 := json.Unmarshal(body, &alt); err2 != nil || len(alt.Models) == 0 {
			return nil, &Error{Kind: ErrorPayload}
		}
		unique := make(map[string]struct{}, len(alt.Models))
		for _, item := range alt.Models {
			id := strings.TrimSpace(item.ID)
			if id != "" {
				unique[id] = struct{}{}
			}
		}
		return sortedKeys(unique), nil
	}
	if len(payload.Data) == 0 {
		return nil, &Error{Kind: ErrorPayload}
	}
	unique := make(map[string]struct{}, len(payload.Data))
	for _, item := range payload.Data {
		id := strings.TrimSpace(item.ID)
		if id != "" {
			unique[id] = struct{}{}
		}
	}
	if len(unique) == 0 {
		return nil, &Error{Kind: ErrorPayload}
	}
	return sortedKeys(unique), nil
}

func sortedKeys(values map[string]struct{}) []string {
	out := make([]string, 0, len(values))
	for key := range values {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}
