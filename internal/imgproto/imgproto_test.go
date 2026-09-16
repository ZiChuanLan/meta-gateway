package imgproto_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/lan/meta-gateway/internal/domain"
	"github.com/lan/meta-gateway/internal/imgproto"
)

func plan(t *testing.T, model string, mode imgproto.Mode) imgproto.Plan {
	t.Helper()
	cap := domain.ClassifyModel(model)
	cap.Model = model
	p, err := imgproto.PlanForModel(cap, mode)
	if err != nil {
		t.Fatalf("PlanForModel(%q, %q): %v", model, mode, err)
	}
	return p
}

// The three families must land on three different protocols — this is the
// entire reason the capability registry exists.
func TestPlanPerFamily(t *testing.T) {
	if p := plan(t, "grok-imagine-image-edit", imgproto.ModeAuto); p.Endpoint != imgproto.EndpointEdits || p.Format != imgproto.FormatJSON {
		t.Errorf("grok edit -> %s/%s, want images/edits json (multipart gets 415)", p.Endpoint, p.Format)
	}
	if p := plan(t, "gpt-image-2", imgproto.ModeAuto); p.Endpoint != imgproto.EndpointEdits || p.Format != imgproto.FormatMultipart {
		t.Errorf("gpt-image -> %s/%s, want images/edits multipart", p.Endpoint, p.Format)
	}
	if p := plan(t, "gpt-image-2", imgproto.ModeGenerate); p.Endpoint != imgproto.EndpointGenerations {
		t.Errorf("gpt-image generate -> %s", p.Endpoint)
	}
	if p := plan(t, "gemini-2.5-flash-image", imgproto.ModeAuto); !p.UsesChatProtocol || p.Endpoint != imgproto.EndpointChat {
		t.Errorf("gemini image -> %+v, want chat protocol", p)
	}
	if p := plan(t, "dall-e-3", imgproto.ModeGenerate); p.Endpoint != imgproto.EndpointGenerations {
		t.Errorf("dall-e -> %s", p.Endpoint)
	}
}

func TestPlanRejectsNonImageModel(t *testing.T) {
	cap := domain.ClassifyModel("gpt-4o")
	cap.Model = "gpt-4o"
	if _, err := imgproto.PlanForModel(cap, imgproto.ModeEdit); err == nil {
		t.Fatal("expected an error for a chat-only model")
	}
}

func TestPlanForRequestRespectsIntentAndReferences(t *testing.T) {
	for _, tc := range []struct {
		model    string
		mode     imgproto.Mode
		images   int
		endpoint string
		format   string
		invalid  bool
	}{
		{"gpt-image-2", imgproto.ModeAuto, 0, imgproto.EndpointGenerations, imgproto.FormatJSON, false},
		{"gpt-image-2", imgproto.ModeAuto, 1, imgproto.EndpointEdits, imgproto.FormatMultipart, false},
		{"gpt-image-2", imgproto.ModeGenerate, 0, imgproto.EndpointGenerations, imgproto.FormatJSON, false},
		{"gpt-image-2", imgproto.ModeEdit, 0, "", "", true},
		{"gpt-image-2", imgproto.ModeGenerate, 1, "", "", true},
		{"grok-imagine-image-edit", imgproto.ModeAuto, 0, "", "", true},
		{"grok-imagine-image-edit", imgproto.ModeEdit, 9, "", "", true},
		{"gemini-2.5-flash-image", imgproto.ModeAuto, 0, imgproto.EndpointChat, imgproto.FormatJSON, false},
		{"gemini-2.5-flash-image", imgproto.ModeAuto, 1, imgproto.EndpointChat, imgproto.FormatJSON, false},
		{"gpt-image-2", imgproto.Mode("typo"), 0, "", "", true},
	} {
		t.Run(tc.model+"/"+string(tc.mode)+"/"+fmt.Sprint(tc.images), func(t *testing.T) {
			plan, err := imgproto.PlanForRequest(domain.ClassifyModel(tc.model), tc.mode, tc.images)
			if (err != nil) != tc.invalid {
				t.Fatalf("plan=%+v err=%v", plan, err)
			}
			if !tc.invalid && (plan.Endpoint != tc.endpoint || plan.Format != tc.format) {
				t.Fatalf("plan=%+v", plan)
			}
		})
	}
}

func TestExtractImagesPreservesOutputFormat(t *testing.T) {
	images := imgproto.ExtractImages([]byte(`{"output_format":"jpeg","data":[{"b64_json":"QUFBQQ=="},{"b64_json":"QUFBQQ==","output_format":"webp"}]}`))
	if len(images) != 2 || images[0].DataURL != "data:image/jpeg;base64,QUFBQQ==" || images[1].DataURL != "data:image/webp;base64,QUFBQQ==" {
		t.Fatalf("image output formats changed: %+v", images)
	}
}

// A registry override must beat the classifier — that is how an operator fixes
// a misdetected upstream.
func TestPlanHonorsRegistryOverride(t *testing.T) {
	cap := domain.ClassifyModel("weird-image-model")
	cap.Model = "weird-image-model"
	cap.Kind = domain.CapabilityKindImageEdit
	cap.Endpoints = []string{"/v1/images/edits"}
	cap.InputFormats = []string{"json"}
	p, err := imgproto.PlanForModel(cap, imgproto.ModeAuto)
	if err != nil {
		t.Fatal(err)
	}
	if p.Endpoint != imgproto.EndpointEdits || p.Format != imgproto.FormatJSON {
		t.Errorf("override -> %+v", p)
	}
	if p.Source != "registry" {
		t.Errorf("source = %q", p.Source)
	}
}

func TestBuildJSONBodyForGrok(t *testing.T) {
	p := plan(t, "grok-imagine-image-edit", imgproto.ModeEdit)
	body, ctype, err := imgproto.BuildBody(p, "grok-imagine-image-edit", imgproto.Request{
		Prompt: "make it purple",
		Images: []imgproto.ImageInput{{DataURL: "data:image/png;base64," + pngB64}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(ctype, "application/json") {
		t.Fatalf("content type = %q", ctype)
	}
	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded["prompt"] != "make it purple" {
		t.Errorf("prompt = %v", decoded["prompt"])
	}
	img, ok := decoded["image"].(map[string]any)
	if !ok {
		t.Fatalf("image = %#v, want an object for a single reference", decoded["image"])
	}
	if !strings.HasPrefix(img["url"].(string), "data:image/png;base64,") {
		t.Errorf("image url = %v", img["url"])
	}
}

func TestBuildMultipartBodyForGPTImage(t *testing.T) {
	p := plan(t, "gpt-image-2", imgproto.ModeEdit)
	body, ctype, err := imgproto.BuildBody(p, "gpt-image-2", imgproto.Request{
		Prompt: "add a hat",
		Size:   "1024x1024",
		Images: []imgproto.ImageInput{{DataURL: "data:image/png;base64," + pngB64}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(ctype, "multipart/form-data") {
		t.Fatalf("content type = %q", ctype)
	}
	text := string(body)
	for _, want := range []string{`name="model"`, `name="prompt"`, `name="image"`, "filename=\"image0.png\"", "gpt-image-2"} {
		if !strings.Contains(text, want) {
			t.Errorf("multipart body missing %s", want)
		}
	}
	if !strings.Contains(text, pngB64+"") {
		// The raw bytes are written, not the base64 text — check the decoder
		// actually produced bytes instead.
		if !strings.Contains(text, "\x89PNG") {
			t.Error("multipart body should carry decoded PNG bytes")
		}
	}
}

func TestBuildChatBodyForGemini(t *testing.T) {
	p := plan(t, "gemini-2.5-flash-image", imgproto.ModeEdit)
	body, _, err := imgproto.BuildBody(p, "gemini-2.5-flash-image", imgproto.Request{
		Prompt: "change the cube to green",
		Images: []imgproto.ImageInput{{DataURL: "data:image/png;base64," + pngB64}},
	})
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Messages []struct {
			Content []map[string]any `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded.Messages) != 1 || len(decoded.Messages[0].Content) != 2 {
		t.Fatalf("chat content = %+v", decoded.Messages)
	}
	types := []string{}
	for _, part := range decoded.Messages[0].Content {
		types = append(types, part["type"].(string))
	}
	if types[0] != "text" || types[1] != "image_url" {
		t.Errorf("content types = %v", types)
	}
}

func TestBuildRejectsBadDataURI(t *testing.T) {
	p := plan(t, "gpt-image-2", imgproto.ModeEdit)
	if _, _, err := imgproto.BuildBody(p, "gpt-image-2", imgproto.Request{
		Prompt: "edit the image",
		Images: []imgproto.ImageInput{{DataURL: "data:image/png,notbase64"}},
	}); err == nil {
		t.Fatal("expected an error for a non-base64 data URI")
	}
}

func TestExtractImagesNative(t *testing.T) {
	body := []byte(`{"data":[{"b64_json":"AAAA","revised_prompt":"a purple cube"},{"url":"https://x/y.png"}]}`)
	got := imgproto.ExtractImages(body)
	if len(got) != 2 {
		t.Fatalf("extracted %d", len(got))
	}
	if got[0].DataURL != "data:image/png;base64,AAAA" {
		t.Errorf("data url = %q", got[0].DataURL)
	}
	if got[0].RevisedPrompt != "a purple cube" {
		t.Errorf("revised prompt = %q", got[0].RevisedPrompt)
	}
	if got[1].URL != "https://x/y.png" {
		t.Errorf("url = %q", got[1].URL)
	}
}

func TestExtractImagesFromChatMarkdown(t *testing.T) {
	body := []byte(`{"choices":[{"message":{"content":"Here you go:\n\n![cube](data:image/png;base64,BBBB)\n"}}]}`)
	got := imgproto.ExtractImages(body)
	if len(got) != 1 || got[0].DataURL != "data:image/png;base64,BBBB" {
		t.Fatalf("extracted %+v", got)
	}
}

func TestExtractImagesFromStructuredChatParts(t *testing.T) {
	body := []byte(`{"choices":[{"message":{"content":[{"type":"text","text":"done"},{"type":"image_url","image_url":{"url":"data:image/png;base64,CCCC"}}]}}]}`)
	got := imgproto.ExtractImages(body)
	if len(got) != 1 || got[0].DataURL != "data:image/png;base64,CCCC" {
		t.Fatalf("extracted %+v", got)
	}
}

func TestExtractImagesFromInlineData(t *testing.T) {
	body := []byte(`{"choices":[{"message":{"content":[{"type":"inline_data","inline_data":{"mime_type":"image/jpeg","data":"DDDD"}}]}}]}`)
	got := imgproto.ExtractImages(body)
	if len(got) != 1 || got[0].DataURL != "data:image/jpeg;base64,DDDD" {
		t.Fatalf("extracted %+v", got)
	}
}

func TestChatResponseRoundTrip(t *testing.T) {
	out := imgproto.ChatResponseFromImages("gemini-2.5-flash-image",
		[]imgproto.ImageOut{{DataURL: "data:image/png;base64,EEEE"}}, "edited")
	var decoded struct {
		Choices []struct {
			Message struct{ Content string } `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(out, &decoded); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(decoded.Choices[0].Message.Content, "data:image/png;base64,EEEE") {
		t.Errorf("content = %q", decoded.Choices[0].Message.Content)
	}
	// And the shim's own output must be re-extractable by the same helper.
	if got := imgproto.ExtractImages(out); len(got) != 1 {
		t.Errorf("re-extracted %d images", len(got))
	}
}

// 1x1 transparent PNG.
const pngB64 = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8AAAwAB/AF+msuWAAAAAElFTkSuQmCC"
