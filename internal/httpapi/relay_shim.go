package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/lan/meta-gateway/internal/imgproto"
	"github.com/lan/meta-gateway/internal/proxy"
	"github.com/lan/meta-gateway/internal/relay"
	"github.com/lan/meta-gateway/internal/usage"
)

const maxShimResponseBytes = 40 << 20

type shimChatRequest struct {
	Size     string `json:"size"`
	N        int    `json:"n"`
	Messages []struct {
		Content json.RawMessage `json:"content"`
	} `json:"messages"`
}

type shimParts struct {
	text   string
	images []imgproto.ImageInput
}

func parseShimContent(raw json.RawMessage) shimParts {
	var out shimParts
	if len(raw) == 0 {
		return out
	}
	if json.Unmarshal(raw, &out.text) == nil {
		return out
	}
	var parts []struct {
		Text     string `json:"text"`
		ImageURL struct {
			URL string `json:"url"`
		} `json:"image_url"`
	}
	if json.Unmarshal(raw, &parts) != nil {
		return out
	}
	var texts []string
	for _, part := range parts {
		if part.ImageURL.URL != "" {
			out.images = append(out.images, imgproto.ImageInput{DataURL: part.ImageURL.URL})
		}
		if part.Text != "" {
			texts = append(texts, part.Text)
		}
	}
	out.text = strings.TrimSpace(strings.Join(texts, "\n"))
	return out
}

// prepareImageEditShim only changes the wire body and endpoint. The normal
// relay path still owns routing, group restrictions, cancellation and billing.
func (h *RelayHandler) prepareImageEditShim(req proxy.Request) (proxy.Request, bool, error) {
	if req.OpenAIPath != "chat/completions" || h.db == nil || h.db.Route == nil || h.db.ModelCapability == nil {
		return req, false, nil
	}
	route, err := h.db.Route.GetByModel(req.Model)
	if err != nil || route == nil || !route.ImageEditShim {
		return req, false, nil
	}
	var request shimChatRequest
	if json.Unmarshal(req.Body, &request) != nil {
		return req, false, nil
	}
	var merged shimParts
	var texts []string
	for _, message := range request.Messages {
		parts := parseShimContent(message.Content)
		if parts.text != "" {
			texts = append(texts, parts.text)
		}
		merged.images = append(merged.images, parts.images...)
	}
	merged.text = strings.TrimSpace(strings.Join(texts, "\n"))
	if len(merged.images) == 0 || merged.text == "" {
		return req, false, nil
	}
	capability, _ := h.db.ModelCapability.Resolve(req.Model)
	plan, err := imgproto.PlanForModel(capability, imgproto.ModeEdit)
	if err != nil || plan.UsesChatProtocol || plan.Endpoint != imgproto.EndpointEdits {
		return req, false, nil
	}
	body, contentType, err := imgproto.BuildBody(plan, req.Model, imgproto.Request{
		Mode: imgproto.ModeEdit, Prompt: merged.text, Images: merged.images,
		Size: request.Size, N: request.N,
	})
	if err != nil {
		return req, false, err
	}
	req.Body, req.ContentType = body, contentType
	req.OpenAIPath, req.Stream = plan.Endpoint, false
	return req, true, nil
}

// imageEditChatResult wraps only successful image responses. Upstream failures
// retain their status, headers and body, and are never retried as chat calls.
func imageEditChatResult(result *relay.Result, model string, stream bool) (*relay.Result, usage.Tokens) {
	if result == nil || result.Err != nil || result.StatusCode < 200 || result.StatusCode >= 300 || result.Body == nil {
		return result, usage.Tokens{}
	}
	source := result.Body
	defer source.Close()
	result.Body = nil
	payload, err := io.ReadAll(io.LimitReader(source, maxShimResponseBytes+1))
	if err != nil {
		result.Err = err
		return result, usage.Tokens{}
	}
	if len(payload) > maxShimResponseBytes {
		result.Err = proxy.ErrResponseTooLarge
		return result, usage.Tokens{}
	}
	images := imgproto.ExtractImages(payload)
	if len(images) == 0 {
		result.Err = errors.New("upstream returned no image")
		return result, usage.Tokens{}
	}
	tokens := usage.ExtractFromJSONBody(payload)
	answer := imgproto.ChatResponseFromImages(model, images, "", tokens)
	result.Header = result.Header.Clone()
	if result.Header == nil {
		result.Header = make(http.Header)
	}
	result.Header.Del("Content-Length")
	result.Header.Del("Content-Encoding")
	result.Header.Set("Content-Type", "application/json")
	if stream {
		answer, err = imageEditChatStream(answer)
		if err != nil {
			result.Err = err
			return result, tokens
		}
		result.Header.Set("Content-Type", "text/event-stream")
	}
	result.Body = io.NopCloser(bytes.NewReader(answer))
	return result, tokens
}

// Image generation finishes before this compatibility stream begins. Emit
// valid chat chunks and a separate usage event using the provider's counts.
func imageEditChatStream(answer []byte) ([]byte, error) {
	var chat struct {
		ID      string          `json:"id"`
		Created int64           `json:"created"`
		Model   string          `json:"model"`
		Usage   json.RawMessage `json:"usage"`
		Choices []struct {
			Message json.RawMessage `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(answer, &chat); err != nil || len(chat.Choices) != 1 {
		return nil, errors.New("invalid image chat response")
	}
	var buffer bytes.Buffer
	for index := 0; index < 3; index++ {
		chunk := map[string]any{
			"id": chat.ID, "object": "chat.completion.chunk", "created": chat.Created, "model": chat.Model,
		}
		switch index {
		case 0:
			chunk["choices"] = []any{map[string]any{"index": 0, "delta": chat.Choices[0].Message, "finish_reason": nil}}
		case 1:
			chunk["choices"] = []any{map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": "stop"}}
		case 2:
			if len(chat.Usage) == 0 {
				continue
			}
			chunk["choices"], chunk["usage"] = []any{}, chat.Usage
		}
		encoded, err := json.Marshal(chunk)
		if err != nil {
			return nil, err
		}
		fmt.Fprintf(&buffer, "data: %s\n\n", encoded)
	}
	buffer.WriteString("data: [DONE]\n\n")
	return buffer.Bytes(), nil
}
