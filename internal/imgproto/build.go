package imgproto

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"mime"
	"mime/multipart"
	"net/textproto"
	"net/url"
	"path/filepath"
	"strings"
)

// ImageInput is one reference image. DataURL accepts either a data URI
// (data:image/png;base64,…) or a plain http(s) URL the upstream can fetch.
type ImageInput struct {
	DataURL string `json:"data_url"`
	Name    string `json:"name,omitempty"`
}

// Request carries everything needed to build an image call.
type Request struct {
	Mode           Mode
	Prompt         string
	Size           string
	N              int
	ResponseFormat string
	Images         []ImageInput
	Extra          map[string]any
}

type parsedImage struct {
	mime     string
	ext      string
	raw      []byte
	dataURL  string
	filename string
}

func parseImage(in ImageInput, index int) (parsedImage, error) {
	out := parsedImage{dataURL: strings.TrimSpace(in.DataURL)}
	imageURL := out.dataURL
	if imageURL == "" {
		return out, fmt.Errorf("image %d is empty", index)
	}
	if !strings.HasPrefix(imageURL, "data:") {
		parsed, err := url.Parse(imageURL)
		if err != nil || parsed.Host == "" || parsed.User != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") {
			return out, fmt.Errorf("image %d must be an image data URI or an HTTP(S) URL", index)
		}
		// Remote URL: nothing to decode, and multipart uploads are impossible
		// without fetching it — only the JSON path can carry it.
		out.mime = ""
		out.filename = strings.TrimSpace(in.Name)
		if out.filename == "" {
			out.filename = fmt.Sprintf("image%d", index)
		}
		return out, nil
	}
	comma := strings.Index(imageURL, ",")
	if comma < 0 {
		return out, fmt.Errorf("image %d is not a valid data URI", index)
	}
	meta := imageURL[5:comma]
	payload := imageURL[comma+1:]
	parts := strings.Split(meta, ";")
	out.mime = strings.TrimSpace(parts[0])
	if !strings.HasPrefix(strings.ToLower(out.mime), "image/") {
		return out, fmt.Errorf("image %d must have an image MIME type", index)
	}
	isBase64 := false
	for _, p := range parts[1:] {
		if strings.EqualFold(strings.TrimSpace(p), "base64") {
			isBase64 = true
		}
	}
	if !isBase64 {
		return out, fmt.Errorf("image %d is not base64 encoded", index)
	}
	raw, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		return out, fmt.Errorf("image %d decode: %w", index, err)
	}
	if len(raw) == 0 {
		return out, fmt.Errorf("image %d is empty", index)
	}
	out.raw = raw
	out.ext = extForMIME(out.mime)
	name := strings.TrimSpace(in.Name)
	if name == "" {
		name = fmt.Sprintf("image%d%s", index, out.ext)
	} else if filepath.Ext(name) == "" {
		name += out.ext
	}
	out.filename = name
	return out, nil
}

func extForMIME(m string) string {
	switch strings.ToLower(strings.TrimSpace(m)) {
	case "image/png":
		return ".png"
	case "image/jpeg", "image/jpg":
		return ".jpg"
	case "image/webp":
		return ".webp"
	case "image/gif":
		return ".gif"
	default:
		exts, _ := mime.ExtensionsByType(m)
		if len(exts) > 0 {
			return exts[0]
		}
		return ".png"
	}
}

// BuildBody renders the upstream request for a plan. contentType is
// "application/json" or the multipart boundary value.
func BuildBody(plan Plan, model string, req Request) ([]byte, string, error) {
	if strings.TrimSpace(req.Prompt) == "" {
		return nil, "", fmt.Errorf("prompt is required")
	}
	if plan.NeedsImages() && len(req.Images) == 0 {
		return nil, "", fmt.Errorf("image editing requires at least one reference image")
	}
	if plan.MaxImages > 0 && len(req.Images) > plan.MaxImages {
		return nil, "", fmt.Errorf("model accepts at most %d reference image(s), got %d", plan.MaxImages, len(req.Images))
	}
	if req.N < 0 {
		return nil, "", fmt.Errorf("image count must not be negative")
	}
	images := make([]parsedImage, 0, len(req.Images))
	for i, in := range req.Images {
		parsed, err := parseImage(in, i)
		if err != nil {
			return nil, "", err
		}
		images = append(images, parsed)
	}
	if plan.UsesChatProtocol {
		return buildChatBody(plan, model, req, images)
	}
	if plan.Format == FormatMultipart {
		return buildMultipartBody(plan, model, req, images)
	}
	return buildImageJSONBody(plan, model, req, images)
}

func buildImageJSONBody(plan Plan, model string, req Request, images []parsedImage) ([]byte, string, error) {
	body := map[string]any{"model": model}
	if strings.TrimSpace(req.Prompt) != "" {
		body["prompt"] = req.Prompt
	}
	if strings.TrimSpace(req.Size) != "" {
		body["size"] = req.Size
	}
	if req.N > 0 {
		body["n"] = req.N
	}
	if format := strings.TrimSpace(req.ResponseFormat); format != "" {
		body["response_format"] = format
	}
	for k, v := range req.Extra {
		body[k] = v
	}
	if len(images) > 0 {
		// grok2api's editor keys a single reference under the singular "image"
		// as an object, but multiple references under the PLURAL "images"
		// array. Sending an array under "image" is rejected with
		// 400 图片编辑 JSON 请求无效, so the plural field is not cosmetic.
		if len(images) == 1 {
			body["image"] = map[string]any{"url": images[0].dataURL}
		} else {
			list := make([]map[string]any, 0, len(images))
			for _, img := range images {
				list = append(list, map[string]any{"url": img.dataURL})
			}
			body["images"] = list
		}
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		return nil, "", fmt.Errorf("encode image json: %w", err)
	}
	return encoded, "application/json", nil
}

func buildChatBody(plan Plan, model string, req Request, images []parsedImage) ([]byte, string, error) {
	parts := []map[string]any{}
	if strings.TrimSpace(req.Prompt) != "" {
		parts = append(parts, map[string]any{"type": "text", "text": req.Prompt})
	}
	for _, img := range images {
		parts = append(parts, map[string]any{
			"type":      "image_url",
			"image_url": map[string]any{"url": img.dataURL},
		})
	}
	if len(parts) == 0 {
		return nil, "", fmt.Errorf("nothing to send: prompt and images are both empty")
	}
	body := map[string]any{
		"model":    model,
		"stream":   false,
		"messages": []map[string]any{{"role": "user", "content": parts}},
	}
	for k, v := range req.Extra {
		body[k] = v
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		return nil, "", fmt.Errorf("encode chat json: %w", err)
	}
	return encoded, "application/json", nil
}

func buildMultipartBody(plan Plan, model string, req Request, images []parsedImage) ([]byte, string, error) {
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	writeField := func(name, value string) error {
		if value == "" {
			return nil
		}
		return writer.WriteField(name, value)
	}
	if err := writeField("model", model); err != nil {
		return nil, "", err
	}
	if err := writeField("prompt", req.Prompt); err != nil {
		return nil, "", err
	}
	if err := writeField("size", strings.TrimSpace(req.Size)); err != nil {
		return nil, "", err
	}
	if req.N > 0 {
		if err := writeField("n", fmt.Sprintf("%d", req.N)); err != nil {
			return nil, "", err
		}
	}
	// Providers choose their native default when no format was requested.
	// GPT-Image always returns base64 and rejects legacy response_format.
	if err := writeField("response_format", strings.TrimSpace(req.ResponseFormat)); err != nil {
		return nil, "", err
	}

	// OpenAI takes a single file under "image" and repeats "image[]" when
	// several references are supplied. Mixing the two names confuses upstreams,
	// so pick one and stay with it.
	fieldName := "image"
	if len(images) > 1 {
		fieldName = "image[]"
	}
	for _, img := range images {
		if len(img.raw) == 0 {
			return nil, "", fmt.Errorf("multipart upload needs local bytes; %q is a remote URL", img.filename)
		}
		header := make(textproto.MIMEHeader)
		header.Set("Content-Disposition",
			fmt.Sprintf(`form-data; name=%q; filename=%q`, fieldName, img.filename))
		header.Set("Content-Type", img.mime)
		part, err := writer.CreatePart(header)
		if err != nil {
			return nil, "", fmt.Errorf("multipart part: %w", err)
		}
		if _, err := part.Write(img.raw); err != nil {
			return nil, "", fmt.Errorf("multipart write: %w", err)
		}
	}
	if err := writer.Close(); err != nil {
		return nil, "", err
	}
	return buf.Bytes(), writer.FormDataContentType(), nil
}
