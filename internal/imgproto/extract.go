package imgproto

import (
	"encoding/json"
	"regexp"
	"strings"
	"time"

	"github.com/lan/meta-gateway/internal/usage"
)

// ImageOut is one image pulled out of an upstream response. DataURL is
// normalized to a full data URI so the console can render it directly; URL is
// set when the upstream returned a remote link instead.
type ImageOut struct {
	DataURL       string `json:"data_url,omitempty"`
	URL           string `json:"url,omitempty"`
	RevisedPrompt string `json:"revised_prompt,omitempty"`
}

var (
	dataURIRe   = regexp.MustCompile(`data:image/[a-zA-Z0-9.+-]+;base64,[A-Za-z0-9+/=]+`)
	remoteURLRe = regexp.MustCompile(`https?://[^\s"'\)\]]+\.(?:png|jpe?g|webp|gif)(?:\?[^\s"'\)\]]*)?`)
	mdImageRe   = regexp.MustCompile(`!\[[^\]]*\]\(([^)\s]+)\)`)
)

// ExtractImages pulls images out of either response shape:
//
//   - /v1/images/* → {"data":[{"b64_json"|"url"|"revised_prompt"}]}
//   - /v1/chat/completions with an image model → inline data URIs or markdown
//     image links inside the assistant content.
func ExtractImages(body []byte) []ImageOut {
	if len(body) == 0 {
		return nil
	}
	var images []ImageOut

	// Shape 1: the native images response.
	var native struct {
		OutputFormat string `json:"output_format"`
		Data         []struct {
			B64JSON       string `json:"b64_json"`
			URL           string `json:"url"`
			RevisedPrompt string `json:"revised_prompt"`
			OutputFormat  string `json:"output_format"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &native); err == nil && len(native.Data) > 0 {
		for _, item := range native.Data {
			out := ImageOut{RevisedPrompt: item.RevisedPrompt}
			switch {
			case strings.TrimSpace(item.B64JSON) != "":
				format := item.OutputFormat
				if format == "" {
					format = native.OutputFormat
				}
				out.DataURL = normalizeDataURI(item.B64JSON, format)
			case strings.TrimSpace(item.URL) != "":
				out.URL = item.URL
			default:
				continue
			}
			images = append(images, out)
		}
		if len(images) > 0 {
			return images
		}
	}

	// Shape 2: a chat response carrying images inline.
	text := flattenChatContent(body)
	if text == "" {
		return nil
	}
	seen := map[string]bool{}
	add := func(value string, isData bool) {
		if value == "" || seen[value] {
			return
		}
		seen[value] = true
		if isData {
			images = append(images, ImageOut{DataURL: value})
		} else {
			images = append(images, ImageOut{URL: value})
		}
	}
	for _, match := range mdImageRe.FindAllStringSubmatch(text, -1) {
		target := match[1]
		if strings.HasPrefix(target, "data:") {
			add(target, true)
		} else {
			add(target, false)
		}
	}
	for _, match := range dataURIRe.FindAllString(text, -1) {
		add(match, true)
	}
	for _, match := range remoteURLRe.FindAllString(text, -1) {
		add(match, false)
	}
	return images
}

func normalizeDataURI(raw, format string) string {
	value := strings.TrimSpace(raw)
	if strings.HasPrefix(value, "data:") {
		return value
	}
	mimeType := "image/png"
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "jpeg", "jpg":
		mimeType = "image/jpeg"
	case "webp":
		mimeType = "image/webp"
	case "gif":
		mimeType = "image/gif"
	}
	return "data:" + mimeType + ";base64," + value
}

// flattenChatContent collects every text-ish piece of a chat completion
// response, including structured content parts, into one scanable string.
func flattenChatContent(body []byte) string {
	var resp struct {
		Choices []struct {
			Message struct {
				Content json.RawMessage `json:"content"`
			} `json:"message"`
			Text string `json:"text"`
		} `json:"choices"`
		Content json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return ""
	}
	var sb strings.Builder
	for _, choice := range resp.Choices {
		if choice.Text != "" {
			sb.WriteString(choice.Text)
			sb.WriteString("\n")
		}
		sb.WriteString(flattenContent(choice.Message.Content))
	}
	sb.WriteString(flattenContent(resp.Content))
	return sb.String()
}

func flattenContent(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	// Plain string content.
	var asString string
	if err := json.Unmarshal(raw, &asString); err == nil {
		return asString + "\n"
	}
	// Structured content parts.
	var parts []struct {
		Type     string `json:"type"`
		Text     string `json:"text"`
		ImageURL struct {
			URL string `json:"url"`
		} `json:"image_url"`
		InlineData struct {
			MimeType string `json:"mime_type"`
			Data     string `json:"data"`
		} `json:"inline_data"`
	}
	if err := json.Unmarshal(raw, &parts); err != nil {
		return ""
	}
	var sb strings.Builder
	for _, part := range parts {
		switch {
		case part.ImageURL.URL != "":
			sb.WriteString(part.ImageURL.URL + "\n")
		case part.InlineData.Data != "":
			mime := part.InlineData.MimeType
			if mime == "" {
				mime = "image/png"
			}
			sb.WriteString("data:" + mime + ";base64," + part.InlineData.Data + "\n")
		case part.Text != "":
			sb.WriteString(part.Text + "\n")
		}
	}
	return sb.String()
}

// ChatResponseFromImages renders images back as an OpenAI chat completion so a
// chat client that asked for an edit receives something it can display. Used by
// the chat-to-edit shim. Usage is included only when the upstream reports it.
func ChatResponseFromImages(model string, images []ImageOut, text string, reportedUsage ...usage.Tokens) []byte {
	var sb strings.Builder
	if strings.TrimSpace(text) != "" {
		sb.WriteString(strings.TrimSpace(text))
		sb.WriteString("\n\n")
	}
	for _, img := range images {
		src := img.DataURL
		if src == "" {
			src = img.URL
		}
		if src == "" {
			continue
		}
		sb.WriteString("![" + firstLine(img.RevisedPrompt, "image") + "](" + src + ")\n\n")
	}
	now := time.Now()
	response := map[string]any{
		"id":      "imgproto-" + now.Format("20060102150405.000000000"),
		"object":  "chat.completion",
		"created": now.Unix(),
		"model":   model,
		"choices": []map[string]any{
			{
				"index":         0,
				"finish_reason": "stop",
				"message":       map[string]any{"role": "assistant", "content": sb.String()},
			},
		},
	}
	if len(reportedUsage) > 0 && reportedUsage[0].Valid() {
		tokens := reportedUsage[0].Normalize()
		response["usage"] = map[string]any{
			"prompt_tokens": tokens.PromptTokens, "completion_tokens": tokens.CompletionTokens,
			"total_tokens": tokens.TotalTokens, "cache_read_tokens": tokens.CacheReadTokens,
			"cache_creation_tokens": tokens.CacheCreationTokens,
		}
	}
	body, err := json.Marshal(response)
	if err != nil {
		return []byte(`{"choices":[]}`)
	}
	return body
}

func firstLine(value, fallback string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return fallback
	}
	if idx := strings.IndexAny(trimmed, "\r\n"); idx >= 0 {
		trimmed = trimmed[:idx]
	}
	if len([]rune(trimmed)) > 60 {
		trimmed = string([]rune(trimmed)[:60]) + "…"
	}
	return trimmed
}
