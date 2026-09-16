// Package imgproto answers one question — "how do I actually ask this model
// for an image?" — and builds the request body for the answer.
//
// The upstream families disagree on both the endpoint and the wire format:
//
//	gpt-image-*  /v1/images/generations + /v1/images/edits   json or multipart
//	grok-*-edit  /v1/images/edits                            json only (415 on multipart)
//	gemini-*-img /v1/chat/completions                        json, image in / image out
//
// Everything here is driven by the capability registry (internal/domain), with
// the built-in classifier as the fallback. The image workbench and the
// chat-to-edit shim share this package so they can never disagree.
package imgproto

import (
	"fmt"
	"strings"

	"github.com/lan/meta-gateway/internal/domain"
)

// Endpoint paths, expressed relative to the upstream OpenAI root.
const (
	EndpointGenerations = "images/generations"
	EndpointEdits       = "images/edits"
	EndpointChat        = "chat/completions"
)

// Request encodings.
const (
	FormatJSON      = "json"
	FormatMultipart = "multipart"
)

// Mode is the caller's intent.
type Mode string

const (
	ModeGenerate Mode = "generate"
	ModeEdit     Mode = "edit"
	ModeAuto     Mode = "auto"
)

// ParseMode normalizes a caller-supplied mode string.
func ParseMode(raw string) Mode {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "generate", "gen", "t2i", "text2image":
		return ModeGenerate
	case "edit", "i2i", "image2image":
		return ModeEdit
	case "", "auto":
		return ModeAuto
	default:
		return Mode(strings.ToLower(strings.TrimSpace(raw)))
	}
}

// Plan is the resolved answer: which endpoint, which encoding, and why.
type Plan struct {
	Model     string `json:"model"`
	Endpoint  string `json:"endpoint"`
	Format    string `json:"format"`
	Mode      Mode   `json:"mode"`
	MaxImages int    `json:"max_images"`
	// UsesChatProtocol marks the Gemini-style path where the image comes back
	// inline in a chat response rather than through /v1/images/*.
	UsesChatProtocol bool   `json:"uses_chat_protocol"`
	Source           string `json:"source"` // "registry" | "classifier"
	Notes            string `json:"notes"`
}

// PlanForModel resolves the protocol for one model + intent.
//
// The model's advertised endpoints win. Explicit intents must be supported;
// an edit-only model cannot generate without a reference image.
func PlanForModel(cap domain.Capability, mode Mode) (Plan, error) {
	source := "registry"
	if cap.ResolvedByBuiltin {
		source = "classifier"
	}
	plan := Plan{
		Model:     cap.Model,
		MaxImages: cap.MaxInputImages,
		Source:    source,
		Notes:     cap.Notes,
	}

	hasGen := cap.HasEndpoint("/v1/" + EndpointGenerations)
	hasEdit := cap.HasEndpoint("/v1/" + EndpointEdits)
	hasChat := cap.HasEndpoint("/v1/" + EndpointChat)

	switch mode {
	case ModeGenerate:
		switch {
		case hasGen:
			plan.Endpoint, plan.Mode = EndpointGenerations, ModeGenerate
		case hasEdit:
			return plan, fmt.Errorf("model %q requires a reference image for editing", cap.Model)
		case hasChat && cap.IsImageModel():
			plan.Endpoint, plan.Mode, plan.UsesChatProtocol = EndpointChat, ModeGenerate, true
		default:
			return plan, fmt.Errorf("model %q does not advertise an image generation endpoint", cap.Model)
		}
	case ModeEdit:
		switch {
		case hasEdit:
			plan.Endpoint, plan.Mode = EndpointEdits, ModeEdit
		case hasChat && cap.IsImageModel() && cap.CanAcceptImages():
			plan.Endpoint, plan.Mode, plan.UsesChatProtocol = EndpointChat, ModeEdit, true
		case hasGen:
			return plan, fmt.Errorf("model %q serves generation but not editing", cap.Model)
		default:
			return plan, fmt.Errorf("model %q does not advertise an image editing endpoint", cap.Model)
		}
	case ModeAuto:
		switch {
		case hasEdit:
			plan.Endpoint, plan.Mode = EndpointEdits, ModeEdit
		case hasGen:
			plan.Endpoint, plan.Mode = EndpointGenerations, ModeGenerate
		case hasChat && cap.IsImageModel():
			plan.Endpoint, plan.Mode, plan.UsesChatProtocol = EndpointChat, ModeEdit, true
		default:
			return plan, fmt.Errorf("model %q has no image endpoint (kind=%s)", cap.Model, cap.Kind)
		}
	default:
		return plan, fmt.Errorf("unsupported image mode %q", mode)
	}

	// Encoding: multipart only when the model advertises it AND the chat path
	// is not in play. grok2api answers 415 for multipart, which is exactly why
	// the registry exists.
	if plan.Endpoint != EndpointEdits || !cap.HasInputFormat(FormatMultipart) {
		plan.Format = FormatJSON
	} else {
		plan.Format = FormatMultipart
	}
	return plan, nil
}

// NeedsImages reports whether the planned call requires reference images.
func (p Plan) NeedsImages() bool {
	return p.Mode == ModeEdit
}

// PlanForRequest resolves auto from the actual inputs and validates intent
// before any upstream request is sent. Preview and execution share this path.
func PlanForRequest(cap domain.Capability, mode Mode, imageCount int) (Plan, error) {
	if imageCount < 0 {
		return Plan{}, fmt.Errorf("image count must not be negative")
	}
	if mode == ModeAuto {
		mode = ModeGenerate
		if imageCount > 0 {
			mode = ModeEdit
		}
	}
	plan, err := PlanForModel(cap, mode)
	if err != nil {
		return plan, err
	}
	if plan.NeedsImages() && imageCount == 0 {
		return plan, fmt.Errorf("image editing requires at least one reference image")
	}
	if mode == ModeGenerate && imageCount > 0 {
		return plan, fmt.Errorf("use edit or auto mode when supplying reference images")
	}
	if plan.MaxImages > 0 && imageCount > plan.MaxImages {
		return plan, fmt.Errorf("model accepts at most %d reference image(s), got %d", plan.MaxImages, imageCount)
	}
	return plan, nil
}
