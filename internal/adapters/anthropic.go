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
	// A path that already carries its own version root ("v1/messages") must not
	// gain another one. This shape reaches here from the save-time split: an
	// operator who pastes a complete endpoint
	// (`https://api.anthropic.com/v1/messages`) gets base + `/v1/messages`
	// override, and the fallback below then produced `/v1/v1/messages`.
	// JoinRawPath, which the OpenAI adapter uses, has no /v1 rule and was never
	// affected; this joiner did.
	relRooted := isVersionSegment(strings.Split(rel, "/")[0])
	switch {
	case relRooted:
		parsed.Path = basePath + "/" + rel
	case lower == "/v1" || strings.HasSuffix(lower, "/v1"):
		parsed.Path = basePath + "/" + rel
	case basePath == "":
		parsed.Path = "/v1/" + rel
	case strings.HasSuffix(lower, "/"+rel) || lower == "/"+rel:
		parsed.Path = basePath
	default:
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
		Tools       json.RawMessage `json:"tools"`
		ToolChoice  json.RawMessage `json:"tool_choice"`
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
			// A tool-less turn keeps the plain string form every
			// Anthropic-compatible endpoint accepts; tool_calls force the block
			// array, which is the only shape that can carry them.
			if len(message.ToolCalls) == 0 {
				messages = append(messages, map[string]any{"role": "assistant", "content": text})
				continue
			}
			// OpenAI keeps an assistant turn's prose and its tool_calls in two
			// fields; Anthropic interleaves them as content blocks.
			blocks := make([]map[string]any, 0, len(message.ToolCalls)+1)
			if text != "" {
				blocks = append(blocks, map[string]any{"type": "text", "text": text})
			}
			for _, call := range message.ToolCalls {
				name := strings.TrimSpace(call.Function.Name)
				if name == "" {
					continue
				}
				id := strings.TrimSpace(call.ID)
				if id == "" {
					id = "toolu_" + name
				}
				blocks = append(blocks, map[string]any{
					"type": "tool_use", "id": id, "name": name,
					"input": json.RawMessage(jsonObjectOrEmpty(json.RawMessage(call.Function.Arguments))),
				})
			}
			if len(blocks) == 0 {
				blocks = append(blocks, map[string]any{"type": "text", "text": ""})
			}
			messages = append(messages, map[string]any{"role": "assistant", "content": blocks})
		case "tool", "function":
			// Anthropic carries a tool result as a tool_result block inside a
			// user message; without the pairing the upstream never sees what its
			// own tool call returned.
			messages = append(messages, map[string]any{
				"role": "user",
				"content": []map[string]any{{
					"type": "tool_result", "tool_use_id": message.ToolCallID, "content": text,
				}},
			})
		case "user", "":
			messages = append(messages, map[string]any{
				"role":    "user",
				"content": text,
			})
		default:
			// Unknown roles keep their text for best effort.
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
	// Tool declarations must reach the upstream, otherwise an Anthropic channel
	// answers a tool-wielding OpenAI client with prose it cannot act on.
	if tools := openAIToolsToAnthropic(incoming.Tools); len(tools) > 0 {
		outbound["tools"] = tools
		if choice := openAIToolChoiceToAnthropic(incoming.ToolChoice); choice != nil {
			outbound["tool_choice"] = choice
		}
	}
	return json.Marshal(outbound)
}

// openAIToolsToAnthropic maps OpenAI function tools onto Anthropic tool
// declarations (parameters → input_schema).
func openAIToolsToAnthropic(raw json.RawMessage) []map[string]any {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var tools []struct {
		Type     string `json:"type"`
		Function struct {
			Name        string          `json:"name"`
			Description string          `json:"description"`
			Parameters  json.RawMessage `json:"parameters"`
		} `json:"function"`
	}
	if err := json.Unmarshal(raw, &tools); err != nil {
		return nil
	}
	out := make([]map[string]any, 0, len(tools))
	for _, tool := range tools {
		name := strings.TrimSpace(tool.Function.Name)
		if name == "" {
			continue
		}
		schema := json.RawMessage(strings.TrimSpace(string(tool.Function.Parameters)))
		if len(schema) == 0 || !strings.HasPrefix(string(schema), "{") {
			schema = json.RawMessage(`{"type":"object","properties":{}}`)
		}
		entry := map[string]any{"name": name, "input_schema": schema}
		if description := strings.TrimSpace(tool.Function.Description); description != "" {
			entry["description"] = description
		}
		out = append(out, entry)
	}
	return out
}

// openAIToolChoiceToAnthropic maps the OpenAI tool_choice forms onto Anthropic's
// (auto / any / tool / none).
func openAIToolChoiceToAnthropic(raw json.RawMessage) map[string]any {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var named string
	if err := json.Unmarshal(raw, &named); err == nil {
		switch named {
		case "auto":
			return map[string]any{"type": "auto"}
		case "required", "any":
			return map[string]any{"type": "any"}
		case "none":
			return map[string]any{"type": "none"}
		default:
			return nil
		}
	}
	var object struct {
		Type     string `json:"type"`
		Function struct {
			Name string `json:"name"`
		} `json:"function"`
	}
	if err := json.Unmarshal(raw, &object); err != nil {
		return nil
	}
	if strings.TrimSpace(object.Function.Name) != "" {
		return map[string]any{"type": "tool", "name": object.Function.Name}
	}
	return nil
}

type chatMessage struct {
	Role       string          `json:"role"`
	Content    json.RawMessage `json:"content"`
	ToolCalls  []chatConvCall  `json:"tool_calls"`
	ToolCallID string          `json:"tool_call_id"`
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
			Type  string          `json:"type"`
			Text  string          `json:"text"`
			ID    string          `json:"id"`
			Name  string          `json:"name"`
			Input json.RawMessage `json:"input"`
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
	var toolCalls []map[string]any
	for _, part := range incoming.Content {
		switch part.Type {
		case "text", "":
			content.WriteString(part.Text)
		case "tool_use":
			// A tool_use block is the OpenAI tool_calls entry; dropping it
			// leaves the client with finish_reason=tool_calls and no call.
			name := strings.TrimSpace(part.Name)
			if name == "" {
				continue
			}
			id := strings.TrimSpace(part.ID)
			if id == "" {
				id = "toolu_" + name
			}
			toolCalls = append(toolCalls, map[string]any{
				"id": id, "type": "function",
				"function": map[string]any{"name": name, "arguments": jsonObjectOrEmpty(part.Input)},
			})
		}
	}
	finishReason := mapAnthropicStopReason(incoming.StopReason)
	if len(toolCalls) > 0 {
		// The mirror of the Anthropic rule: a turn carrying calls must say so.
		finishReason = "tool_calls"
	}
	message := map[string]any{"role": "assistant", "content": content.String()}
	if len(toolCalls) > 0 {
		message["tool_calls"] = toolCalls
		if content.Len() == 0 {
			message["content"] = nil
		}
	}
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
			"index":         0,
			"message":       message,
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
		return nil, &Error{Kind: ErrorStatus, Status: resp.StatusCode, RetryAfter: retryAfterFromHeader(resp.Header)}
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
