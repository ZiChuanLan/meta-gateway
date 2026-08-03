// Gemini adapter: translates between the OpenAI wire contract and the native
// Google Gemini API (generativelanguage.googleapis.com).
package adapters

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// DefaultGeminiBaseURL is the AI Studio (developer API) endpoint.
const DefaultGeminiBaseURL = "https://generativelanguage.googleapis.com/v1beta"

// GeminiGenerateContentPath builds the per-model endpoint used for both
// generateContent and streamGenerateContent.
func GeminiGenerateContentPath(model string) string {
	return "models/" + url.PathEscape(model) + ":generateContent"
}

func GeminiStreamGenerateContentPath(model string) string {
	return "models/" + url.PathEscape(model) + ":streamGenerateContent?alt=sse"
}

// GeminiForwardAdapter translates OpenAI requests to the native Gemini API.
type GeminiForwardAdapter struct{}

func (GeminiForwardAdapter) Name() string { return "gemini" }

func (GeminiForwardAdapter) IsFor(typeHint, platform string) bool {
	return CanonicalType(firstNonEmpty(typeHint, platform)) == "gemini"
}

func (GeminiForwardAdapter) BuildUpstreamURL(baseURL, upstreamPath string) (string, error) {
	base := strings.TrimSpace(baseURL)
	if base == "" {
		base = DefaultGeminiBaseURL
	}
	parsed, err := url.Parse(base)
	if err != nil || !parsed.IsAbs() || parsed.Host == "" || parsed.User != nil ||
		(parsed.Scheme != "http" && parsed.Scheme != "https") {
		return "", errors.New("invalid base URL")
	}
	rel := strings.Trim(strings.TrimSpace(upstreamPath), "/")
	if rel == "" {
		return "", errors.New("invalid upstream path")
	}
	parsed.RawQuery = ""
	parsed.Fragment = ""
	parsed.Path = strings.TrimRight(parsed.Path, "/") + "/" + rel
	return parsed.String(), nil
}

func (GeminiForwardAdapter) AuthHeaders(apiKey string) http.Header {
	headers := http.Header{}
	headers.Set("x-goog-api-key", apiKey)
	headers.Set("Content-Type", "application/json")
	return headers
}

func (GeminiForwardAdapter) TransformRequest(openAIPath string, body []byte) (string, []byte, error) {
	switch openAIPath {
	case "", "chat/completions":
		return chatToGemini(body)
	case "embeddings":
		return embeddingsToGemini(body)
	default:
		return "", nil, ErrUnsupportedPath
	}
}

func (GeminiForwardAdapter) TransformResponse(openAIPath string, body []byte) ([]byte, error) {
	switch openAIPath {
	case "", "chat/completions":
		return geminiToChat(body)
	case "embeddings":
		return geminiToEmbeddings(body)
	default:
		return nil, ErrUnsupportedPath
	}
}

func (GeminiForwardAdapter) WrapStream(openAIPath string, source io.ReadCloser) (io.ReadCloser, error) {
	if openAIPath != "" && openAIPath != "chat/completions" {
		return nil, ErrUnsupportedPath
	}
	return NewGeminiToOpenAIStream(source), nil
}

var _ ForwardAdapter = GeminiForwardAdapter{}

// ---- request conversion ----

type geminiContentPart struct {
	Text string `json:"text,omitempty"`
}

type geminiContent struct {
	Role  string              `json:"role,omitempty"`
	Parts []geminiContentPart `json:"parts"`
}

type geminiGenerationConfig struct {
	Temperature     *float64 `json:"temperature,omitempty"`
	TopP            *float64 `json:"topP,omitempty"`
	MaxOutputTokens *int     `json:"maxOutputTokens,omitempty"`
	StopSequences   []string `json:"stopSequences,omitempty"`
}

type geminiGenerateRequest struct {
	Contents          []geminiContent         `json:"contents"`
	SystemInstruction *geminiContent          `json:"systemInstruction,omitempty"`
	GenerationConfig  *geminiGenerationConfig `json:"generationConfig,omitempty"`
}

type openAIChatPayload struct {
	Model       string   `json:"model"`
	Stream      bool     `json:"stream"`
	Temperature *float64 `json:"temperature"`
	TopP        *float64 `json:"top_p"`
	MaxTokens   *int     `json:"max_tokens"`
	Stop        any      `json:"stop"`
	Messages    []struct {
		Role    string `json:"role"`
		Content any    `json:"content"`
	} `json:"messages"`
}

// chatToGemini converts an OpenAI chat/completions body into a Gemini
// generateContent request, returning the upstream path and converted body.
func chatToGemini(body []byte) (string, []byte, error) {
	var payload openAIChatPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", nil, fmt.Errorf("gemini: decode chat request: %w", err)
	}
	model := strings.TrimSpace(payload.Model)
	if model == "" {
		return "", nil, errors.New("gemini: model is required")
	}

	req := geminiGenerateRequest{Contents: []geminiContent{}}
	var systemParts []geminiContentPart
	for _, message := range payload.Messages {
		role := strings.ToLower(strings.TrimSpace(message.Role))
		text := contentToText(message.Content)
		switch role {
		case "system":
			if text != "" {
				systemParts = append(systemParts, geminiContentPart{Text: text})
			}
		case "assistant":
			if text != "" {
				req.Contents = append(req.Contents, geminiContent{Role: "model", Parts: []geminiContentPart{{Text: text}}})
			}
		default: // user, tool, etc.
			if text != "" {
				req.Contents = append(req.Contents, geminiContent{Role: "user", Parts: []geminiContentPart{{Text: text}}})
			}
		}
	}
	if len(systemParts) > 0 {
		req.SystemInstruction = &geminiContent{Parts: systemParts}
	}

	config := &geminiGenerationConfig{}
	hasConfig := false
	if payload.Temperature != nil {
		config.Temperature = payload.Temperature
		hasConfig = true
	}
	if payload.TopP != nil {
		config.TopP = payload.TopP
		hasConfig = true
	}
	if payload.MaxTokens != nil && *payload.MaxTokens > 0 {
		config.MaxOutputTokens = payload.MaxTokens
		hasConfig = true
	}
	if stops, ok := openAIStops(payload.Stop); ok && len(stops) > 0 {
		config.StopSequences = stops
		hasConfig = true
	}
	if hasConfig {
		req.GenerationConfig = config
	}

	converted, err := json.Marshal(req)
	if err != nil {
		return "", nil, fmt.Errorf("gemini: encode request: %w", err)
	}
	path := GeminiGenerateContentPath(model)
	if payload.Stream {
		path = GeminiStreamGenerateContentPath(model)
	}
	return path, converted, nil
}

// contentToText flattens OpenAI message content (string or parts array) to text.
func contentToText(content any) string {
	switch value := content.(type) {
	case string:
		return value
	case []any:
		var builder strings.Builder
		for _, part := range value {
			if object, ok := part.(map[string]any); ok {
				if text, ok := object["text"].(string); ok {
					builder.WriteString(text)
				}
			}
		}
		return builder.String()
	case nil:
		return ""
	default:
		return ""
	}
}

func openAIStops(value any) ([]string, bool) {
	switch typed := value.(type) {
	case string:
		return []string{typed}, true
	case []any:
		stops := make([]string, 0, len(typed))
		for _, entry := range typed {
			if text, ok := entry.(string); ok && text != "" {
				stops = append(stops, text)
			}
		}
		return stops, len(stops) > 0
	default:
		return nil, false
	}
}

// ---- response conversion ----

type geminiGenerateResponse struct {
	Candidates []struct {
		Content      geminiContent `json:"content"`
		FinishReason string        `json:"finishReason"`
	} `json:"candidates"`
	UsageMetadata *struct {
		PromptTokenCount     int `json:"promptTokenCount"`
		CandidatesTokenCount int `json:"candidatesTokenCount"`
		TotalTokenCount      int `json:"totalTokenCount"`
	} `json:"usageMetadata"`
	ModelVersion string `json:"modelVersion"`
}

// geminiToChat converts a Gemini generateContent response into OpenAI
// chat.completion format.
func geminiToChat(body []byte) ([]byte, error) {
	var response geminiGenerateResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, fmt.Errorf("gemini: decode response: %w", err)
	}
	choices := make([]map[string]any, 0, len(response.Candidates))
	for _, candidate := range response.Candidates {
		var text strings.Builder
		for _, part := range candidate.Content.Parts {
			if part.Text != "" {
				text.WriteString(part.Text)
			}
		}
		choices = append(choices, map[string]any{
			"index": 0,
			"message": map[string]any{
				"role":    "assistant",
				"content": text.String(),
			},
			"finish_reason": mapGeminiFinishReason(candidate.FinishReason),
		})
	}
	outbound := map[string]any{
		"id":      "chatcmpl-gemini",
		"object":  "chat.completion",
		"created": nowUnix(),
		"model":   response.ModelVersion,
		"choices": choices,
	}
	if response.UsageMetadata != nil {
		prompt := response.UsageMetadata.PromptTokenCount
		completion := response.UsageMetadata.CandidatesTokenCount
		outbound["usage"] = map[string]any{
			"prompt_tokens":     prompt,
			"completion_tokens": completion,
			"total_tokens":      response.UsageMetadata.TotalTokenCount,
		}
	}
	return json.Marshal(outbound)
}

func mapGeminiFinishReason(reason string) string {
	switch strings.ToUpper(strings.TrimSpace(reason)) {
	case "STOP":
		return "stop"
	case "MAX_TOKENS":
		return "length"
	case "SAFETY", "BLOCKLIST", "PROHIBITED_CONTENT", "SPII", "RECITATION", "OTHER":
		return "content_filter"
	default:
		return ""
	}
}

func nowUnix() int64 {
	return time.Now().Unix()
}

// ---- embeddings conversion ----

type geminiEmbeddingRequestItem struct {
	Content geminiContent `json:"content"`
}

type geminiBatchEmbedRequest struct {
	Requests []geminiEmbeddingRequestItem `json:"requests"`
}

type openAIEmbeddingsPayload struct {
	Model string `json:"model"`
	Input any    `json:"input"`
}

// embeddingsToGemini converts an OpenAI embeddings body into a Gemini
// batchEmbedContents request.
func embeddingsToGemini(body []byte) (string, []byte, error) {
	var payload openAIEmbeddingsPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", nil, fmt.Errorf("gemini: decode embeddings request: %w", err)
	}
	model := strings.TrimSpace(payload.Model)
	if model == "" {
		return "", nil, errors.New("gemini: model is required")
	}
	inputs := openAIEmbeddingInputs(payload.Input)
	if len(inputs) == 0 {
		return "", nil, errors.New("gemini: input is required")
	}
	requests := make([]geminiEmbeddingRequestItem, 0, len(inputs))
	for _, text := range inputs {
		requests = append(requests, geminiEmbeddingRequestItem{
			Content: geminiContent{Role: "user", Parts: []geminiContentPart{{Text: text}}},
		})
	}
	converted, err := json.Marshal(geminiBatchEmbedRequest{Requests: requests})
	if err != nil {
		return "", nil, fmt.Errorf("gemini: encode embeddings request: %w", err)
	}
	return "models/" + url.PathEscape(model) + ":batchEmbedContents", converted, nil
}

// openAIEmbeddingInputs normalizes OpenAI embedding input (string or array) to a
// list of strings.
func openAIEmbeddingInputs(input any) []string {
	switch typed := input.(type) {
	case string:
		if strings.TrimSpace(typed) != "" {
			return []string{typed}
		}
	case []any:
		inputs := make([]string, 0, len(typed))
		for _, entry := range typed {
			if text, ok := entry.(string); ok && strings.TrimSpace(text) != "" {
				inputs = append(inputs, text)
			}
		}
		return inputs
	}
	return nil
}

// geminiToEmbeddings converts a Gemini batchEmbedContents response into OpenAI
// embeddings format.
func geminiToEmbeddings(body []byte) ([]byte, error) {
	var response struct {
		Embeddings []struct {
			Values []float64 `json:"values"`
		} `json:"embeddings"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, fmt.Errorf("gemini: decode embeddings response: %w", err)
	}
	data := make([]map[string]any, 0, len(response.Embeddings))
	for index, embedding := range response.Embeddings {
		data = append(data, map[string]any{
			"object":    "embedding",
			"index":     index,
			"embedding": embedding.Values,
		})
	}
	outbound := map[string]any{
		"object": "list",
		"data":   data,
		"model":  "gemini-embedding",
	}
	return json.Marshal(outbound)
}

// GeminiModelAdapter lists models from the Gemini API (GET /v1beta/models).
type GeminiModelAdapter struct {
	name   string
	client *http.Client
}

func NewGeminiModelAdapter(name string, client *http.Client) *GeminiModelAdapter {
	return &GeminiModelAdapter{name: name, client: client}
}

func (a *GeminiModelAdapter) Name() string { return a.name }

func (a *GeminiModelAdapter) ListModels(ctx context.Context, baseURL, apiKey string) ([]string, error) {
	base := strings.TrimSpace(baseURL)
	if base == "" {
		base = DefaultGeminiBaseURL
	}
	endpoint := strings.TrimRight(base, "/") + "/models"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, &Error{Kind: ErrorInvalidURL}
	}
	req.Header.Set("x-goog-api-key", apiKey)
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
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if readErr != nil {
		return nil, &Error{Kind: ErrorTransport}
	}
	var payload struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, &Error{Kind: ErrorPayload}
	}
	models := make([]string, 0, len(payload.Models))
	for _, model := range payload.Models {
		name := strings.TrimPrefix(model.Name, "models/")
		if name != "" {
			models = append(models, name)
		}
	}
	return models, nil
}
