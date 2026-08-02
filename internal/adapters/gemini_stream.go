// Gemini SSE → OpenAI chat.completion.chunk stream converter.
package adapters

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// GeminiToOpenAIStream converts the Gemini streamGenerateContent SSE stream
// (one complete JSON object per "data:" line) into OpenAI chat.completion.chunk
// SSE so OpenAI-compatible clients can stream through Gemini channels.
type GeminiToOpenAIStream struct {
	source io.ReadCloser
	reader *bufio.Reader

	pending bytes.Buffer

	model     string
	created   int64
	roleSent  bool
	done      bool
	closed    bool
	sourceErr error
}

func NewGeminiToOpenAIStream(source io.ReadCloser) *GeminiToOpenAIStream {
	return &GeminiToOpenAIStream{
		source:  source,
		reader:  bufio.NewReader(source),
		model:   "gemini",
		created: nowUnix(),
	}
}

func (s *GeminiToOpenAIStream) Read(p []byte) (int, error) {
	if s.closed {
		return 0, io.EOF
	}
	if s.done && s.pending.Len() == 0 {
		return 0, io.EOF
	}
	if s.pending.Len() > 0 {
		n, _ := s.pending.Read(p)
		if s.pending.Len() == 0 {
			s.pending.Reset()
		}
		return n, nil
	}
	for s.pending.Len() == 0 && !s.done {
		if err := s.pullEvent(); err != nil {
			if err == io.EOF {
				s.writeDone()
				break
			}
			s.sourceErr = err
			s.done = true
			return 0, err
		}
	}
	if s.pending.Len() == 0 && s.done {
		return 0, io.EOF
	}
	n, _ := s.pending.Read(p)
	if s.pending.Len() == 0 {
		s.pending.Reset()
	}
	return n, nil
}

func (s *GeminiToOpenAIStream) Close() error {
	if s.closed {
		return nil
	}
	s.closed = true
	if s.source != nil {
		return s.source.Close()
	}
	return nil
}

// pullEvent reads one SSE line, parses the Gemini JSON payload, and enqueues
// the corresponding OpenAI chunk.
func (s *GeminiToOpenAIStream) pullEvent() error {
	line, err := s.reader.ReadString('\n')
	if err != nil && line == "" {
		return err
	}
	line = strings.TrimSpace(line)
	if line == "" {
		return nil
	}
	if strings.HasPrefix(line, "data:") {
		line = strings.TrimSpace(strings.TrimPrefix(line, "data:"))
	}
	if line == "[DONE]" {
		s.writeDone()
		return nil
	}
	var payload struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"content"`
			FinishReason string `json:"finishReason"`
		} `json:"candidates"`
		UsageMetadata *struct {
			PromptTokenCount     int `json:"promptTokenCount"`
			CandidatesTokenCount int `json:"candidatesTokenCount"`
			TotalTokenCount      int `json:"totalTokenCount"`
		} `json:"usageMetadata"`
		ModelVersion string `json:"modelVersion"`
	}
	if err := json.Unmarshal([]byte(line), &payload); err != nil {
		// Not a JSON payload line (e.g. comments) — skip.
		return nil
	}
	if payload.ModelVersion != "" {
		s.model = payload.ModelVersion
	}
	finishReason := ""
	if len(payload.Candidates) > 0 {
		candidate := payload.Candidates[0]
		for _, part := range candidate.Content.Parts {
			if part.Text != "" {
				s.writeTextDelta(part.Text)
			}
		}
		finishReason = mapGeminiFinishReason(candidate.FinishReason)
	}
	var usage map[string]any
	if payload.UsageMetadata != nil {
		prompt := payload.UsageMetadata.PromptTokenCount
		completion := payload.UsageMetadata.CandidatesTokenCount
		usage = map[string]any{
			"prompt_tokens":     prompt,
			"completion_tokens": completion,
			"total_tokens":      payload.UsageMetadata.TotalTokenCount,
		}
	}
	if finishReason != "" || usage != nil {
		s.writeChunk(finishReason, usage)
	}
	return nil
}

// writeTextDelta emits a content delta chunk, prefixed by the role chunk.
func (s *GeminiToOpenAIStream) writeTextDelta(text string) {
	if !s.roleSent {
		s.roleSent = true
		s.writeChunkPayload(map[string]any{
			"choices": []any{
				map[string]any{
					"index": 0,
					"delta": map[string]any{"role": "assistant"},
				},
			},
		})
	}
	s.writeChunkPayload(map[string]any{
		"choices": []any{
			map[string]any{
				"index": 0,
				"delta": map[string]any{"content": text},
			},
		},
	})
}

// writeChunk emits a chunk carrying finish_reason and/or usage.
func (s *GeminiToOpenAIStream) writeChunk(finishReason string, usage map[string]any) {
	delta := map[string]any{}
	if finishReason != "" {
		delta["finish_reason"] = finishReason
	}
	if usage != nil {
		delta["usage"] = usage
	}
	s.writeChunkPayload(map[string]any{
		"choices": []any{
			map[string]any{
				"index": 0,
				"delta": delta,
			},
		},
	})
}

func (s *GeminiToOpenAIStream) writeChunkPayload(chunk map[string]any) {
	if s.done {
		return
	}
	payload := map[string]any{
		"id":      "chatcmpl-gemini",
		"object":  "chat.completion.chunk",
		"created": s.created,
		"model":   s.model,
	}
	for key, value := range chunk {
		payload[key] = value
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		s.sourceErr = fmt.Errorf("gemini stream: encode chunk: %w", err)
		s.done = true
		return
	}
	s.pending.WriteString("data: ")
	s.pending.Write(encoded)
	s.pending.WriteString("\n\n")
}

func (s *GeminiToOpenAIStream) writeDone() {
	if s.done {
		return
	}
	s.done = true
	s.pending.WriteString("data: [DONE]\n\n")
}
