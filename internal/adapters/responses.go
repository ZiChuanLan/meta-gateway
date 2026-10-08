// Responses API ↔ chat completions conversion. The gateway's internal pivot
// protocol is OpenAI chat/completions; the Responses wire contract (used by
// Codex and the OpenAI Agents/SDK tooling) is expressed through this converter.
// Text, image URLs and function tools are mapped; file IDs and
// response-stored state are intentionally out of scope for upstreams that do
// not natively speak the Responses protocol.
package adapters

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// chatConvMessage mirrors the chat messages shape used by this converter —
// the pivot format the composed adapters exchange.
type chatConvMessage struct {
	Role       string         `json:"role"`
	Content    any            `json:"content,omitempty"`
	ToolCalls  []chatConvCall `json:"tool_calls,omitempty"`
	ToolCallID string         `json:"tool_call_id,omitempty"`
}

type chatConvCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

// anthropicToolUseBlocks renders OpenAI tool_calls as Anthropic tool_use blocks.
// A nameless call is dropped — Anthropic rejects a tool_use without a name, and
// nothing could answer it anyway — and a missing id gets the "toolu_<name>"
// placeholder both directions of the Anthropic translation use.
func anthropicToolUseBlocks(calls []chatConvCall) []map[string]any {
	blocks := make([]map[string]any, 0, len(calls))
	for _, call := range calls {
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
	return blocks
}

// responsesRequest is the subset of the Responses request document the
// translator round-trips. Unknown fields are dropped, never forwarded
// half-way: a chat upstream would reject them anyway.
type responsesRequest struct {
	PreviousResponseID string          `json:"previous_response_id"`
	Conversation       json.RawMessage `json:"conversation"`
	Background         bool            `json:"background"`
	Model              string          `json:"model"`
	Instructions       any             `json:"instructions"`
	Input              json.RawMessage `json:"input"`
	MaxOutputTokens    *int            `json:"max_output_tokens"`
	Temperature        *float64        `json:"temperature"`
	TopP               *float64        `json:"top_p"`
	Stop               json.RawMessage `json:"stop"`
	Stream             bool            `json:"stream"`
	Tools              []responsesTool `json:"tools"`
	ToolChoice         json.RawMessage `json:"tool_choice"`
	ParallelCalls      *bool           `json:"parallel_tool_calls"`
	Reasoning          *struct {
		Effort  string `json:"effort"`
		Summary string `json:"summary"`
	} `json:"reasoning"`
	Text *struct {
		Format json.RawMessage `json:"format"`
	} `json:"text"`
}

type responsesTool struct {
	Strict      *bool           `json:"strict"`
	Type        string          `json:"type"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

// inputItem is one entry of the Responses "input" array.
type inputItem struct {
	Arguments *string         `json:"arguments"`
	Type      string          `json:"type"`
	Role      string          `json:"role"`
	Content   json.RawMessage `json:"content"`
	Name      string          `json:"name"`
	CallID    string          `json:"call_id"`
	Output    string          `json:"output"`
}

type inputPart struct {
	Type     string `json:"type"`
	Text     string `json:"text"`
	ImageURL string `json:"image_url"`
	Detail   string `json:"detail"`
	Refusal  string `json:"refusal"`
}

// ResponsesToChat converts a /v1/responses request into a chat/completions
// request. The translated body always carries stream_options.include_usage on
// streams so the gateway can meter tokens without buffering.
//
// It refuses what a chat upstream genuinely cannot express: a stored
// conversation (previous_response_id / conversation) or background execution,
// because that state lives on the upstream that produced it and the gateway
// cannot reconstruct it. Callers that can degrade should use
// ResponsesToChatReport, which reports the server-side tools it had to drop
// instead of failing the whole request.
func ResponsesToChat(body []byte) ([]byte, error) {
	converted, _, err := ResponsesToChatReport(body)
	return converted, err
}

// ResponsesToChatReport is ResponsesToChat plus the list of tools it dropped,
// formatted for a header or a log line.
//
// Dropping is the honest choice for a chat-only upstream: a server-side tool
// (web_search, file_search, mcp, ...) has no chat/completions equivalent, so the
// request cannot be forwarded with it. Refusing the whole request would be worse
// — the caller then gets nothing at all, and a coding agent's default toolset
// makes that the common case rather than the exception (Codex 0.155.1 sends
// web_search plus a namespace tool alongside its function tools, so every turn
// would fail). The drop is reported instead, and the caller surfaces it.
func ResponsesToChatReport(body []byte) ([]byte, []string, error) {
	var req responsesRequest
	if err := json.Unmarshal(body, &req); err != nil || strings.TrimSpace(req.Model) == "" {
		return nil, nil, fmt.Errorf("responses: invalid request (model required)")
	}
	if req.PreviousResponseID != "" || req.Background || (len(req.Conversation) > 0 && string(req.Conversation) != "null") {
		return nil, nil, fmt.Errorf("%w: stored conversation and background execution require a native Responses upstream", ErrUnsupportedFeature)
	}
	// Keep the function tools; note the rest. A forced tool_choice naming a
	// dropped tool cannot be honoured, and that one IS refused: the request would
	// reach the upstream asking for a tool it does not have.
	tools := make([]responsesTool, 0, len(req.Tools))
	var dropped []string
	for _, tool := range req.Tools {
		if tool.Type == "function" && strings.TrimSpace(tool.Name) != "" {
			tools = append(tools, tool)
			continue
		}
		label := tool.Type
		if label == "" {
			label = "unknown"
		}
		if name := strings.TrimSpace(tool.Name); name != "" {
			label += ":" + name
		}
		dropped = append(dropped, label)
	}
	if len(dropped) > 0 && toolChoiceNamesDropped(req.ToolChoice, dropped) {
		return nil, nil, fmt.Errorf("%w: tool_choice forces a tool that only a native Responses upstream can run", ErrUnsupportedFeature)
	}
	messages := make([]chatConvMessage, 0, 4)
	if instructions, ok := stringOrEmpty(req.Instructions); ok && strings.TrimSpace(instructions) != "" {
		messages = append(messages, chatConvMessage{Role: "system", Content: instructions})
	}
	if len(req.Input) > 0 && string(req.Input) != "null" {
		converted, err := responsesInputToMessages(req.Input)
		if err != nil {
			return nil, nil, err
		}
		messages = append(messages, converted...)
	}
	if len(messages) == 0 {
		return nil, nil, errors.New("responses: input is required")
	}

	out := map[string]any{
		"model":    req.Model,
		"messages": messages,
		"stream":   req.Stream,
	}
	if req.MaxOutputTokens != nil {
		out["max_tokens"] = *req.MaxOutputTokens
	}
	if req.Temperature != nil {
		out["temperature"] = *req.Temperature
	}
	if req.TopP != nil {
		out["top_p"] = *req.TopP
	}
	if len(req.Stop) > 0 && string(req.Stop) != "null" {
		out["stop"] = json.RawMessage(req.Stop)
	}
	if len(tools) > 0 {
		out["tools"] = responsesToolsToChat(tools)
	}
	if len(req.ToolChoice) > 0 && string(req.ToolChoice) != "null" {
		choice, err := responsesToolChoiceToChat(req.ToolChoice)
		if err != nil {
			return nil, nil, fmt.Errorf("%w: invalid tool_choice", ErrUnsupportedFeature)
		}
		out["tool_choice"] = choice
	}
	if req.ParallelCalls != nil {
		out["parallel_tool_calls"] = *req.ParallelCalls
	}
	if req.Reasoning != nil {
		if effort := strings.TrimSpace(req.Reasoning.Effort); effort != "" {
			out["reasoning_effort"] = effort
		}
	}
	if req.Text != nil && len(req.Text.Format) > 0 && string(req.Text.Format) != "null" {
		if format, ok := responsesTextFormat(req.Text.Format); ok {
			out["response_format"] = format
		}
	}
	if req.Stream {
		out["stream_options"] = map[string]any{"include_usage": true}
	}
	encoded, err := json.Marshal(out)
	if err != nil {
		return nil, nil, fmt.Errorf("responses: encode chat body: %w", err)
	}
	return encoded, dropped, nil
}

// toolChoiceNamesDropped reports whether tool_choice forces one of the tools the
// conversion had to drop.
func toolChoiceNamesDropped(raw json.RawMessage, dropped []string) bool {
	if len(raw) == 0 || string(raw) == "null" {
		return false
	}
	var choice struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(raw, &choice); err != nil || strings.TrimSpace(choice.Name) == "" {
		return false
	}
	for _, label := range dropped {
		if strings.HasSuffix(label, ":"+choice.Name) {
			return true
		}
	}
	return false
}

// responsesInputToMessages maps the Responses input (a string or an array of
// input items) onto chat messages. Function-call round-trips are preserved.
func responsesInputToMessages(raw json.RawMessage) ([]chatConvMessage, error) {
	raw = json.RawMessage(strings.TrimSpace(string(raw)))
	if len(raw) == 0 || string(raw) == "null" {
		return nil, errors.New("responses: input is required")
	}
	if raw[0] == '"' {
		var text string
		if err := json.Unmarshal(raw, &text); err != nil {
			return nil, errors.New("responses: invalid string input")
		}
		return []chatConvMessage{{Role: "user", Content: text}}, nil
	}
	var items []inputItem
	if err := json.Unmarshal(raw, &items); err != nil || items == nil {
		return nil, errors.New("responses: invalid input array")
	}
	messages := make([]chatConvMessage, 0, len(items))
	for _, item := range items {
		switch item.Type {
		case "message", "":
			role := item.Role
			if role == "system" || role == "developer" {
				role = "system"
			} else if role != "assistant" && role != "tool" {
				role = "user"
			}
			text, err := inputContentText(item.Content)
			if err != nil {
				return nil, err
			}
			messages = append(messages, chatConvMessage{Role: role, Content: text})
		case "function_call":
			arguments := "{}"
			if item.Arguments != nil {
				arguments = *item.Arguments
			} else if trimmed := strings.TrimSpace(item.Output); trimmed != "" && strings.HasPrefix(trimmed, "{") {
				arguments = trimmed
			} else if trimmed != "" {
				arguments = `{"result":` + strconvQuote(trimmed) + `}`
			}
			call := chatConvCall{ID: item.CallID, Type: "function"}
			call.Function.Name = item.Name
			call.Function.Arguments = arguments
			messages = append(messages, chatConvMessage{Role: "assistant", ToolCalls: []chatConvCall{call}})
		case "function_call_output":
			messages = append(messages, chatConvMessage{Role: "tool", Content: item.Output, ToolCallID: item.CallID})
		case "reasoning":
			// Reasoning/summary items carry no direct chat equivalent; skip
			// them instead of failing the request.
		default:
			return nil, fmt.Errorf("%w: input item %q", ErrUnsupportedFeature, item.Type)
		}
	}
	if len(messages) == 0 {
		return nil, errors.New("responses: input has no translatable content")
	}
	return messages, nil
}

func strconvQuote(value string) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

// inputContentText flattens a message item's content (string or part array)
// into plain text. Image parts are dropped (chat upstreams cannot be assumed
// to accept them under translation); empty text still yields a valid message.
func inputContentText(raw json.RawMessage) (any, error) {
	raw = json.RawMessage(strings.TrimSpace(string(raw)))
	if len(raw) == 0 || string(raw) == "null" {
		return "", nil
	}
	if raw[0] == '"' {
		var text string
		if err := json.Unmarshal(raw, &text); err != nil {
			return "", errors.New("responses: invalid message content")
		}
		return text, nil
	}
	var parts []inputPart
	if err := json.Unmarshal(raw, &parts); err != nil {
		return "", errors.New("responses: invalid message content parts")
	}
	var builder strings.Builder
	var content []map[string]any
	hasImage := false
	for _, part := range parts {
		switch part.Type {
		case "input_text", "text", "output_text":
			builder.WriteString(part.Text)
			content = append(content, map[string]any{"type": "text", "text": part.Text})
		case "refusal":
			builder.WriteString(part.Refusal)
			content = append(content, map[string]any{"type": "text", "text": part.Refusal})
		case "input_image":
			if part.ImageURL == "" {
				return nil, fmt.Errorf("%w: image file IDs require a native Responses upstream", ErrUnsupportedFeature)
			}
			image := map[string]any{"url": part.ImageURL}
			if part.Detail != "" {
				image["detail"] = part.Detail
			}
			content = append(content, map[string]any{"type": "image_url", "image_url": image})
			hasImage = true
		default:
			return nil, fmt.Errorf("%w: input content %q", ErrUnsupportedFeature, part.Type)
		}
	}
	if hasImage {
		return content, nil
	}
	return builder.String(), nil
}

func responsesToolsToChat(tools []responsesTool) []map[string]any {
	out := make([]map[string]any, 0, len(tools))
	for _, tool := range tools {
		if tool.Type != "function" || strings.TrimSpace(tool.Name) == "" {
			continue
		}
		function := map[string]any{"name": tool.Name}
		if tool.Strict != nil {
			function["strict"] = *tool.Strict
		}
		if strings.TrimSpace(tool.Description) != "" {
			function["description"] = tool.Description
		}
		if len(tool.Parameters) > 0 && string(tool.Parameters) != "null" {
			function["parameters"] = json.RawMessage(tool.Parameters)
		}
		out = append(out, map[string]any{"type": "function", "function": function})
	}
	return out
}

// responsesToolChoiceToChat maps the Responses tool_choice union onto the chat
// one. Both unions share the string forms; the function spec moves under
// "function" in chat.
func responsesToolChoiceToChat(raw json.RawMessage) (any, error) {
	trimmed := json.RawMessage(strings.TrimSpace(string(raw)))
	if len(trimmed) == 0 || string(trimmed) == "null" {
		return nil, errors.New("responses: empty tool_choice")
	}
	if trimmed[0] == '"' {
		var choice string
		if err := json.Unmarshal(trimmed, &choice); err != nil {
			return nil, err
		}
		return choice, nil
	}
	var spec struct {
		Type string `json:"type"`
		Name string `json:"name"`
	}
	if err := json.Unmarshal(trimmed, &spec); err != nil {
		return nil, err
	}
	if spec.Type != "function" || strings.TrimSpace(spec.Name) == "" {
		return nil, errors.New("responses: unsupported tool_choice")
	}
	return map[string]any{"type": "function", "function": map[string]any{"name": spec.Name}}, nil
}

// responsesTextFormat maps the Responses text.format union onto the chat
// response_format union. ok=false for formats with no chat equivalent
// ("text" — the chat default).
func responsesTextFormat(raw json.RawMessage) (any, bool) {
	var format struct {
		Type   string          `json:"type"`
		Name   string          `json:"name"`
		Schema json.RawMessage `json:"schema"`
		Strict *bool           `json:"strict"`
	}
	if err := json.Unmarshal(raw, &format); err != nil {
		return nil, false
	}
	switch format.Type {
	case "json_object":
		return map[string]any{"type": "json_object"}, true
	case "json_schema":
		schema := map[string]any{}
		if strings.TrimSpace(format.Name) != "" {
			schema["name"] = format.Name
		}
		if len(format.Schema) > 0 && string(format.Schema) != "null" {
			schema["schema"] = json.RawMessage(format.Schema)
		}
		if format.Strict != nil {
			schema["strict"] = *format.Strict
		}
		return map[string]any{"type": "json_schema", "json_schema": schema}, true
	default:
		return nil, false
	}
}

// chatCompletionResponse is the subset of the chat response the Responses
// translator reads.
type chatCompletionResponse struct {
	ID      string `json:"id"`
	Created int64  `json:"created"`
	Model   string `json:"model"`
	Choices []struct {
		Index        int             `json:"index"`
		FinishReason string          `json:"finish_reason"`
		Message      chatConvMessage `json:"message"`
	} `json:"choices"`
	Usage json.RawMessage `json:"usage"`
	Error json.RawMessage `json:"error"`
}

// ChatToResponses converts a chat completion response into the Responses
// response document. Tool calls become function_call output items; usage is
// mirrored under both the standard and metering-readable keys.
func ChatToResponses(chatBody []byte) ([]byte, error) {
	var chat chatCompletionResponse
	if err := json.Unmarshal(chatBody, &chat); err != nil {
		return nil, fmt.Errorf("responses: decode chat response: %w", err)
	}
	status := "completed"
	incomplete := ""
	if len(chat.Choices) > 0 && chat.Choices[0].FinishReason == "length" {
		incomplete = "length"
	}
	output := make([]any, 0, 2)
	for _, choice := range chat.Choices {
		message := choice.Message
		if len(message.ToolCalls) > 0 {
			for _, call := range message.ToolCalls {
				call.ID = strings.TrimSpace(call.ID)
				if call.ID == "" {
					call.ID = "fc_" + hex.EncodeToString(randomIDBytes(12))
				}
				output = append(output, map[string]any{
					"id":        call.ID,
					"call_id":   call.ID,
					"type":      "function_call",
					"name":      call.Function.Name,
					"arguments": call.Function.Arguments,
					"status":    "completed",
				})
			}
			continue
		}
		output = append(output, map[string]any{
			"id":     "msg_" + hex.EncodeToString(randomIDBytes(12)),
			"type":   "message",
			"status": "completed",
			"role":   "assistant",
			"content": []any{map[string]any{
				"type":        "output_text",
				"text":        messageContent(message.Content),
				"annotations": []any{},
			}},
		})
	}
	if len(output) == 0 {
		output = append(output, map[string]any{
			"id":     "msg_" + hex.EncodeToString(randomIDBytes(12)),
			"type":   "message",
			"status": "completed",
			"role":   "assistant",
			"content": []any{map[string]any{
				"type":        "output_text",
				"text":        "",
				"annotations": []any{},
			}},
		})
	}
	var usage map[string]any
	var usageFields struct {
		Prompt        int `json:"prompt_tokens"`
		Completion    int `json:"completion_tokens"`
		Total         int `json:"total_tokens"`
		PromptDetails *struct {
			Cached int `json:"cached_tokens"`
		} `json:"prompt_tokens_details"`
	}
	if len(chat.Usage) > 0 && string(chat.Usage) != "null" {
		_ = json.Unmarshal(chat.Usage, &usageFields)
		if usageFields.Total <= 0 {
			usageFields.Total = usageFields.Prompt + usageFields.Completion
		}
		usage = map[string]any{
			"input_tokens":  usageFields.Prompt,
			"output_tokens": usageFields.Completion,
			"total_tokens":  usageFields.Total,
		}
		if usageFields.PromptDetails != nil && usageFields.PromptDetails.Cached > 0 {
			usage["input_tokens_details"] = map[string]any{"cached_tokens": usageFields.PromptDetails.Cached}
			// Metering alias: the gateway usage tee reads cached reads under
			// prompt_tokens_details.cached_tokens.
			usage["prompt_tokens_details"] = map[string]any{"cached_tokens": usageFields.PromptDetails.Cached}
		}
	}
	response := map[string]any{
		"id":         "resp_" + hex.EncodeToString(randomIDBytes(16)),
		"object":     "response",
		"created_at": chat.Created,
		"status":     status,
		"model":      chat.Model,
		"output":     output,
	}
	if incomplete != "" {
		response["incomplete_details"] = map[string]any{"reason": incomplete}
	}
	if usage != nil {
		response["usage"] = usage
	}
	if len(chat.Error) > 0 && string(chat.Error) != "null" {
		response["error"] = json.RawMessage(chat.Error)
	}
	encoded, err := json.Marshal(response)
	if err != nil {
		return nil, fmt.Errorf("responses: encode response: %w", err)
	}
	return encoded, nil
}

func randomIDBytes(n int) []byte {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		for i := range buf {
			buf[i] = byte(time.Now().UnixNano() >> (i % 8 * 8))
		}
	}
	return buf
}

func messageContent(content any) string {
	switch value := content.(type) {
	case string:
		return value
	case nil:
		return ""
	case []any:
		var builder strings.Builder
		for _, part := range value {
			if text, ok := part.(string); ok {
				builder.WriteString(text)
			}
		}
		return builder.String()
	default:
		return ""
	}
}

func stringOrEmpty(value any) (string, bool) {
	switch typed := value.(type) {
	case string:
		return typed, true
	default:
		return "", false
	}
}
