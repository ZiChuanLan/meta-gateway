package domain

import "strings"

// Capability kinds. A model has exactly one primary kind; the endpoints list
// carries anything else the model also serves.
const (
	CapabilityKindChat       = "chat"
	CapabilityKindImageGen   = "image_gen"
	CapabilityKindImageEdit  = "image_edit"
	CapabilityKindVideo      = "video"
	CapabilityKindEmbedding  = "embedding"
	CapabilityKindAudioTTS   = "audio_tts"
	CapabilityKindAudioSTT   = "audio_stt"
	CapabilityKindRerank     = "rerank"
	CapabilityKindModeration = "moderation"
)

// Capability sources. Manual rows are operator overrides and must never be
// overwritten by discovery auto-tagging or by a catalog sync. Catalog rows are
// owned by the sync service, so it may refresh them freely.
const (
	CapabilitySourceBuiltin   = "builtin"
	CapabilitySourceDiscovery = "discovery"
	CapabilitySourceManual    = "manual"
	CapabilitySourceCatalog   = "catalog"
)

// CatalogWritable reports whether a catalog sync may replace a row holding the
// given source. The rule is simply "not an operator override": every other
// source (builtin name heuristic, discovery auto-tag, an earlier catalog sync)
// is machine-generated and a real catalog entry is strictly better evidence.
func CatalogWritable(source string) bool {
	return NormalizeCapabilitySource(source) != CapabilitySourceManual
}

// ClassifierOwned reports whether a row's contents came from the built-in name
// classifier rather than from an operator or a curated catalog.
//
// Such a row holds no decision worth preserving, so re-deriving it is safe —
// and necessary, or improving a rule would never reach the models it already
// labelled. The other two sources must not be overwritten by it: a manual row
// is the operator's intent, and a catalog row carries limits, prices and
// curated modalities that the built-in guess does not know.
func ClassifierOwned(source string) bool {
	switch NormalizeCapabilitySource(source) {
	case CapabilitySourceBuiltin, CapabilitySourceDiscovery:
		return true
	default:
		return false
	}
}

// Capability describes *how to talk to* a model: which endpoint, which request
// encoding, how many input images it accepts, whether the call is async.
// It is the protocol counterpart of ModelMetadata (which describes what the
// model is: context window, vendor, price).
type Capability struct {
	Model             string   `json:"model"`
	Kind              string   `json:"kind"`
	Provider          string   `json:"provider"`
	Endpoints         []string `json:"endpoints"`
	InputFormats      []string `json:"input_formats"`
	InputModalities   []string `json:"input_modalities"`
	OutputModalities  []string `json:"output_modalities"`
	MaxInputImages    int      `json:"max_input_images"`
	SupportsStream    bool     `json:"supports_stream"`
	SupportsTools     bool     `json:"supports_tools"`
	SupportsJSONMode  bool     `json:"supports_json_mode"`
	AsyncTask         bool     `json:"async_task"`
	SizeOptions       string   `json:"size_options"`
	Source            string   `json:"source"`
	Notes             string   `json:"notes"`
	UpdatedAt         string   `json:"updated_at"`
	ResolvedByBuiltin bool     `json:"resolved_by_builtin,omitempty"`
}

// capabilityKinds is the canonical kind list.
var capabilityKinds = []string{
	CapabilityKindChat,
	CapabilityKindImageGen,
	CapabilityKindImageEdit,
	CapabilityKindVideo,
	CapabilityKindEmbedding,
	CapabilityKindAudioTTS,
	CapabilityKindAudioSTT,
	CapabilityKindRerank,
	CapabilityKindModeration,
}

// CapabilityKinds returns every supported kind.
func CapabilityKinds() []string {
	out := make([]string, len(capabilityKinds))
	copy(out, capabilityKinds)
	return out
}

// NormalizeCapabilityKind maps an arbitrary kind string onto the canonical set.
// Unknown or empty input falls back to chat — the safe default, because
// treating an exotic model as a chat model only costs a wrong label, while
// treating a chat model as an image model routes traffic to an endpoint the
// model does not serve.
func NormalizeCapabilityKind(kind string) string {
	k := strings.ToLower(strings.TrimSpace(kind))
	switch k {
	case "image", "image_gen", "image_generation", "text2image", "t2i":
		return CapabilityKindImageGen
	case "image_edit", "edit", "image_editing", "i2i", "inpainting":
		return CapabilityKindImageEdit
	case "video", "video_gen", "text2video", "t2v":
		return CapabilityKindVideo
	case "embedding", "embeddings", "text_embedding":
		return CapabilityKindEmbedding
	case "tts", "audio_tts", "speech":
		return CapabilityKindAudioTTS
	case "stt", "audio_stt", "asr", "transcribe", "transcription":
		return CapabilityKindAudioSTT
	case "rerank", "reranker":
		return CapabilityKindRerank
	case "moderation", "moderations":
		return CapabilityKindModeration
	case "", "chat", "llm", "text", "chat_completion", "completion":
		return CapabilityKindChat
	default:
		return CapabilityKindChat
	}
}

// NormalizeCapabilitySource keeps only the known provenance values.
func NormalizeCapabilitySource(source string) string {
	switch strings.ToLower(strings.TrimSpace(source)) {
	case CapabilitySourceDiscovery:
		return CapabilitySourceDiscovery
	case CapabilitySourceManual:
		return CapabilitySourceManual
	case CapabilitySourceCatalog:
		return CapabilitySourceCatalog
	default:
		return CapabilitySourceBuiltin
	}
}

// HasEndpoint reports whether the capability lists the given path suffix,
// e.g. "/v1/images/edits".
func (c Capability) HasEndpoint(path string) bool {
	want := strings.TrimSpace(path)
	if want == "" {
		return false
	}
	for _, ep := range c.Endpoints {
		if strings.EqualFold(strings.TrimSpace(ep), want) {
			return true
		}
	}
	return false
}

// HasInputFormat reports whether a request encoding is supported ("json" or
// "multipart").
func (c Capability) HasInputFormat(format string) bool {
	want := strings.ToLower(strings.TrimSpace(format))
	if want == "" {
		return false
	}
	for _, f := range c.InputFormats {
		if strings.EqualFold(strings.TrimSpace(f), want) {
			return true
		}
	}
	return false
}

// CanAcceptImages reports whether the model takes image input at all.
func (c Capability) CanAcceptImages() bool {
	for _, m := range c.InputModalities {
		if strings.EqualFold(strings.TrimSpace(m), "image") {
			return true
		}
	}
	return false
}

// IsImageModel reports whether the primary kind is image generation or edit.
func (c Capability) IsImageModel() bool {
	return c.Kind == CapabilityKindImageGen || c.Kind == CapabilityKindImageEdit
}

// SplitCSV splits a comma separated column value into a clean slice.
func SplitCSV(raw string) []string {
	out := []string{}
	for _, part := range strings.Split(raw, ",") {
		p := strings.TrimSpace(part)
		if p == "" {
			continue
		}
		out = append(out, p)
	}
	return out
}

// JoinCSV renders a slice back into a comma separated column value.
func JoinCSV(items []string) string {
	out := []string{}
	seen := map[string]bool{}
	for _, item := range items {
		v := strings.TrimSpace(item)
		if v == "" || seen[strings.ToLower(v)] {
			continue
		}
		seen[strings.ToLower(v)] = true
		out = append(out, v)
	}
	return strings.Join(out, ",")
}

// ---------------------------------------------------------------------------
// Built-in classifier
// ---------------------------------------------------------------------------

// providerRules maps a model-name prefix onto a provider slug. Longest prefix
// wins, so check order matters only for ties.
var providerRules = []struct {
	prefix   string
	provider string
}{
	{"gpt-", "openai"},
	{"chatgpt", "openai"},
	{"o1", "openai"},
	{"o3", "openai"},
	{"o4", "openai"},
	{"dall-e", "openai"},
	{"text-embedding", "openai"},
	{"omni-moderation", "openai"},
	{"sora", "openai"},
	{"claude", "anthropic"},
	{"gemini", "google"},
	{"imagen", "google"},
	{"veo", "google"},
	{"grok", "xai"},
	{"deepseek", "deepseek"},
	{"qwen", "alibaba"},
	{"qwq", "alibaba"},
	{"glm", "zhipu"},
	{"kimi", "moonshot"},
	{"moonshot", "moonshot"},
	{"minicpm", "openbmb"},
	{"yi-", "01ai"},
	{"baichuan", "baichuan"},
	{"ernie", "baidu"},
	{"hunyuan", "tencent"},
	// Hunyuan's short alias is "hy<generation>" / "hy-", enumerated rather than
	// written as a bare "hy" on purpose: that would also swallow unrelated
	// names such as hyperclova or hyperbolic/<org>/<model>. Same reasoning as
	// the o1/o3/o4 entries above; a new generation needs a new line.
	{"hy-", "tencent"},
	{"hy3", "tencent"},
	{"hy4", "tencent"},
	{"mimo", "xiaomi"},
	{"step-", "stepfun"},
	{"doubao", "bytedance"},
	{"seed", "bytedance"},
	{"kling", "kuaishou"},
	{"hailuo", "minimax"},
	{"flux", "blackforestlabs"},
	{"stable-diffusion", "stabilityai"},
	{"midjourney", "midjourney"},
	{"command-r", "cohere"},
	{"llama", "meta"},
	// Mistral AI's product lines. Only the flagship starts with "mistral", so
	// every other family is a distinct prefix and needs its own entry. They do
	// not shadow each other: matching is longest-prefix-wins, and no sibling
	// name extends the parent's.
	{"mistral", "mistral"},
	{"codestral", "mistral"},
	{"devstral", "mistral"},
	{"magistral", "mistral"},
	{"ministral", "mistral"},
	{"mixtral", "mistral"},
	{"voxtral", "mistral"},
	// Google's open-weight line. "diffusiongemma" is a separate spelling (it
	// does not begin with "gemma"), so it cannot ride on that entry.
	{"gemma", "google"},
	{"diffusiongemma", "google"},
	// NVIDIA publishes most models as nvidia/<family>. The org prefix is
	// stripped before matching, so each family name is listed explicitly —
	// including the literal "nvidia-" spelling that a few rows double up on.
	{"nvidia", "nvidia"},
	{"nemotron", "nvidia"},
	{"nv-", "nvidia"},
	{"riva-", "nvidia"},
	{"minimax", "minimax"},
	{"longcat", "meituan"},
	// "ling-" rather than a bare "ling", so the prefix cannot leak into names
	// such as labs-leanstral. Same reasoning as the hy/hy- entries above.
	{"ling-", "inclusionai"},
}

// InferProvider guesses the vendor from a model name. Empty when unknown.
func InferProvider(model string) string {
	name := strings.ToLower(strings.TrimSpace(model))
	// Strip an org prefix such as "openai/gpt-4o" or "google/gemini-2.5-flash".
	if idx := strings.LastIndex(name, "/"); idx >= 0 {
		name = name[idx+1:]
	}
	best := ""
	for _, rule := range providerRules {
		if strings.HasPrefix(name, rule.prefix) && len(rule.prefix) > len(best) {
			best = rule.prefix
		}
	}
	if best == "" {
		return ""
	}
	for _, rule := range providerRules {
		if rule.prefix == best {
			return rule.provider
		}
	}
	return ""
}

// ClassifyModel infers a capability from the model name alone. It is the
// fallback used when no registry row exists, and the seed used to auto-tag
// models the first time discovery sees them.
//
// The rules are deliberately conservative: a model we cannot recognise stays a
// plain chat model rather than being handed an endpoint it does not serve.
func ClassifyModel(model string) Capability {
	name := strings.ToLower(strings.TrimSpace(model))
	base := name
	if idx := strings.LastIndex(base, "/"); idx >= 0 {
		base = base[idx+1:]
	}
	cap := Capability{
		Model:            strings.TrimSpace(model),
		Kind:             CapabilityKindChat,
		Provider:         InferProvider(model),
		Endpoints:        []string{"/v1/chat/completions"},
		InputFormats:     []string{"json"},
		InputModalities:  []string{"text"},
		OutputModalities: []string{"text"},
		SupportsStream:   true,
		Source:           CapabilitySourceBuiltin,
	}

	switch {
	case strings.Contains(base, "embedding"):
		cap.Kind = CapabilityKindEmbedding
		cap.Endpoints = []string{"/v1/embeddings"}
		cap.SupportsStream = false
		cap.OutputModalities = []string{"embedding"}
		return cap

	case strings.Contains(base, "rerank"):
		cap.Kind = CapabilityKindRerank
		cap.Endpoints = []string{"/v1/rerank"}
		cap.SupportsStream = false
		cap.OutputModalities = []string{"score"}
		return cap

	case strings.Contains(base, "moderation"):
		cap.Kind = CapabilityKindModeration
		cap.Endpoints = []string{"/v1/moderations"}
		cap.SupportsStream = false
		return cap

	case strings.Contains(base, "whisper"), strings.Contains(base, "-asr"),
		strings.Contains(base, "transcribe"):
		cap.Kind = CapabilityKindAudioSTT
		cap.Endpoints = []string{"/v1/audio/transcriptions"}
		cap.InputFormats = []string{"multipart"}
		cap.InputModalities = []string{"audio"}
		cap.SupportsStream = false
		return cap

	case strings.Contains(base, "tts"), strings.Contains(base, "speech"):
		cap.Kind = CapabilityKindAudioTTS
		cap.Endpoints = []string{"/v1/audio/speech"}
		cap.OutputModalities = []string{"audio"}
		cap.SupportsStream = false
		return cap

	// Video: async submit + poll by nature.
	case strings.Contains(base, "sora"), strings.Contains(base, "veo"),
		strings.Contains(base, "kling"), strings.Contains(base, "hailuo"),
		strings.Contains(base, "runway"), strings.Contains(base, "video"):
		cap.Kind = CapabilityKindVideo
		cap.Endpoints = []string{"/v1/videos"}
		cap.OutputModalities = []string{"video"}
		cap.AsyncTask = true
		cap.SupportsStream = false
		cap.InputModalities = []string{"text", "image"}
		return cap
	}

	// ---- image models -----------------------------------------------------
	isImage := strings.Contains(base, "image") || strings.Contains(base, "dall-e") ||
		strings.Contains(base, "imagen") || strings.Contains(base, "flux") ||
		strings.Contains(base, "stable-diffusion") || strings.Contains(base, "midjourney") ||
		strings.Contains(base, "seedream") || strings.Contains(base, "-sd")
	if !isImage {
		return cap
	}
	cap.OutputModalities = []string{"image"}

	switch {
	// grok2api's image editor: JSON only, multipart is rejected with 415.
	case strings.Contains(base, "grok") && strings.Contains(base, "edit"):
		cap.Kind = CapabilityKindImageEdit
		cap.Endpoints = []string{"/v1/images/edits"}
		cap.InputFormats = []string{"json"}
		cap.InputModalities = []string{"text", "image"}
		cap.MaxInputImages = 8
		cap.SupportsStream = false
		cap.Notes = "grok2api: /v1/images/edits accepts application/json only (multipart -> 415)"
		return cap

	case strings.Contains(base, "grok"):
		cap.Kind = CapabilityKindImageGen
		cap.Endpoints = []string{"/v1/images/generations"}
		cap.InputModalities = []string{"text"}
		cap.SupportsStream = false
		return cap

	// Gemini's image family stays on the chat protocol: image in, image out.
	case strings.Contains(base, "gemini"), strings.Contains(base, "imagen"):
		cap.Kind = CapabilityKindImageEdit
		cap.Endpoints = []string{"/v1/chat/completions"}
		cap.InputFormats = []string{"json"}
		cap.InputModalities = []string{"text", "image"}
		cap.OutputModalities = []string{"text", "image"}
		cap.MaxInputImages = 8
		cap.Notes = "Gemini image models take/return images over /v1/chat/completions, not /v1/images/*"
		return cap

	// OpenAI's gpt-image family serves both endpoints and both encodings.
	case strings.Contains(base, "gpt-image"):
		cap.Kind = CapabilityKindImageEdit
		cap.Endpoints = []string{"/v1/images/generations", "/v1/images/edits"}
		cap.InputFormats = []string{"json", "multipart"}
		cap.InputModalities = []string{"text", "image"}
		cap.MaxInputImages = 16
		cap.SupportsStream = false
		cap.SizeOptions = "auto,1024x1024,1024x1536,1536x1024"
		return cap

	case strings.Contains(base, "dall-e"):
		cap.Kind = CapabilityKindImageGen
		cap.Endpoints = []string{"/v1/images/generations"}
		cap.InputModalities = []string{"text"}
		cap.SupportsStream = false
		cap.SizeOptions = "1024x1024,1792x1024,1024x1792"
		return cap

	default:
		cap.Kind = CapabilityKindImageGen
		cap.Endpoints = []string{"/v1/images/generations"}
		cap.InputModalities = []string{"text"}
		cap.SupportsStream = false
		return cap
	}
}
