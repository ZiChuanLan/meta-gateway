package domain

import "testing"

func TestClassifyModelImageFamilies(t *testing.T) {
	cases := []struct {
		model      string
		kind       string
		format     string // expected primary input format
		wantImages int
	}{
		// grok2api's editor is JSON-only; multipart is rejected upstream.
		{"grok-imagine-image-edit", CapabilityKindImageEdit, "json", 8},
		{"grok-imagine-image", CapabilityKindImageGen, "json", 0},
		// OpenAI's gpt-image family serves both endpoints and both encodings.
		{"gpt-image-2", CapabilityKindImageEdit, "json", 16},
		{"gpt-image-1", CapabilityKindImageEdit, "json", 16},
		// Gemini's image models stay on the chat protocol.
		{"gemini-2.5-flash-image", CapabilityKindImageEdit, "json", 8},
		{"gemini-3.8-flash-high", CapabilityKindChat, "json", 0},
		{"dall-e-3", CapabilityKindImageGen, "json", 0},
		{"sora-2", CapabilityKindVideo, "json", 0},
		{"text-embedding-3-large", CapabilityKindEmbedding, "json", 0},
		{"whisper-1", CapabilityKindAudioSTT, "multipart", 0},
		{"tts-1", CapabilityKindAudioTTS, "json", 0},
		// Unrecognised models must stay chat rather than be handed an endpoint
		// they do not serve.
		{"some-random-model", CapabilityKindChat, "json", 0},
	}
	for _, tc := range cases {
		got := ClassifyModel(tc.model)
		if got.Kind != tc.kind {
			t.Errorf("ClassifyModel(%q).Kind = %q, want %q", tc.model, got.Kind, tc.kind)
		}
		if !got.HasInputFormat(tc.format) {
			t.Errorf("ClassifyModel(%q) input formats = %v, want %q", tc.model, got.InputFormats, tc.format)
		}
		if got.MaxInputImages != tc.wantImages {
			t.Errorf("ClassifyModel(%q).MaxInputImages = %d, want %d", tc.model, got.MaxInputImages, tc.wantImages)
		}
	}
}

func TestClassifyModelEndpointRouting(t *testing.T) {
	// The whole point of the registry: tell the caller which endpoint to hit.
	if c := ClassifyModel("grok-imagine-image-edit"); !c.HasEndpoint("/v1/images/edits") {
		t.Errorf("grok edit should expose /v1/images/edits, got %v", c.Endpoints)
	}
	if c := ClassifyModel("gemini-2.5-flash-image"); !c.HasEndpoint("/v1/chat/completions") {
		t.Errorf("gemini image should stay on /v1/chat/completions, got %v", c.Endpoints)
	}
	if c := ClassifyModel("gpt-image-2"); !c.HasEndpoint("/v1/images/generations") || !c.HasEndpoint("/v1/images/edits") {
		t.Errorf("gpt-image should expose both image endpoints, got %v", c.Endpoints)
	}
	// grok's editor must NOT advertise multipart: this build answers 415.
	if c := ClassifyModel("grok-imagine-image-edit"); c.HasInputFormat("multipart") {
		t.Errorf("grok edit must not advertise multipart")
	}
}

func TestClassifyModelAsyncVideo(t *testing.T) {
	c := ClassifyModel("sora-2")
	if !c.AsyncTask {
		t.Error("video models are submit + poll, expected async_task")
	}
	if c.SupportsStream {
		t.Error("video models should not advertise streaming")
	}
}

func TestInferProvider(t *testing.T) {
	cases := map[string]string{
		"gpt-4o":                "openai",
		"claude-sonnet-4-5":     "anthropic",
		"gemini-2.5-pro":        "google",
		"grok-4":                "xai",
		"deepseek-chat":         "deepseek",
		"openai/gpt-4o-mini":    "openai",
		"google/gemini-2.5-pro": "google",
		"mystery-model":         "",
		// Xiaomi's MiMo, including the org-prefixed reseller form.
		"mimo-v2.5":      "xiaomi",
		"mimo-v2.5-pro":  "xiaomi",
		"mimo-v2-flash":  "xiaomi",
		"mimo/mimo-v2.5": "xiaomi",
		// Tencent Hunyuan, spelled either long or short.
		"hunyuan-turbos": "tencent",
		"hy3":            "tencent",
		"hy3-free":       "tencent",
		"hy4-preview":    "tencent",
		"hy-mt2-plus":    "tencent",
		"Hy3":            "tencent",
		// Mistral AI's sibling lines, which do not share the flagship prefix.
		// Several arrive as mistral/<family>, so the org prefix is stripped
		// first and the family name is what actually has to match.
		"codestral-2508":                    "mistral",
		"mistral/codestral-latest":          "mistral",
		"mistralai/codestral-22b-instruct":  "mistral",
		"devstral-medium-latest":            "mistral",
		"magistral-small-latest":            "mistral",
		"ministral-8b-latest":               "mistral",
		"mistralai/mixtral-8x22b-v0.1":      "mistral",
		"voxtral-mini-tts-latest":           "mistral",
		"mistral/voxtral-small-latest":      "mistral",
		"gemma-4-31b-it":                    "google",
		"google/gemma-4-31b-it":             "google",
		"diffusiongemma-26b-a4b-it":         "google",
		"nemotron-3-ultra-free":             "nvidia",
		"nvidia/nemotron-3-super-120b-a12b": "nvidia",
		"nvidia/nvidia-nemotron-nano-9b-v2": "nvidia",
		"nvidia/nv-embedqa-mistral-7b-v2":   "nvidia",
		"nvidia/riva-translate-4b-v2":       "nvidia",
		"minimax/minimax-m2.7":              "minimax",
		"MiniMaxAI/MiniMax-M2.5":            "minimax",
		"MiniMax-M3":                        "minimax",
		"meituan/LongCat-2.0:free":          "meituan",
		"longcat-2.0-free":                  "meituan",
		"ling-3.0-flash-free":               "inclusionai",
		"inclusionai/ling-3.0-flash-sante":  "inclusionai",
	}
	for model, want := range cases {
		if got := InferProvider(model); got != want {
			t.Errorf("InferProvider(%q) = %q, want %q", model, got, want)
		}
	}
}

func TestInferProviderDoesNotSwallowUnrelatedHyNames(t *testing.T) {
	// "hy" is short enough to collide, which is why Hunyuan is matched by
	// generation or hyphen rather than by the bare prefix. These must stay
	// unknown instead of being filed as Tencent.
	for _, model := range []string{
		"hyperclovax-seed-text",
		"hyperbolic/",
		"hymm-1",
	} {
		if got := InferProvider(model); got == "tencent" {
			t.Errorf("InferProvider(%q) = tencent, want no Tencent attribution", model)
		}
	}
	// A reseller org prefix is stripped before matching, so this one is Meta's
	// model and not a hyperbolic/hy rule hit either.
	if got := InferProvider("hyperbolic/meta-llama/Llama-3.3-70B-Instruct"); got != "meta" {
		t.Errorf("org-prefixed llama = %q, want meta", got)
	}
}

func TestInferProviderKeepsShortPrefixesNarrow(t *testing.T) {
	// The new short prefixes ("ling-", "nv-", "riva-", "minimax") are narrow
	// on purpose. Each one exists because a bare-prefix version would have
	// over-matched, so pin the neighbours that must stay unattributed.
	for _, model := range []string{
		"labs-leanstral-1-5", // must not be read as inclusionai's "ling-"
		"rivage-7b",          // "riva-" requires the hyphen, "rivage" lacks it
	} {
		switch got := InferProvider(model); got {
		case "inclusionai", "nvidia", "minimax":
			t.Errorf("InferProvider(%q) = %q, want no such attribution", model, got)
		}
	}
	// "nvidia" as a literal prefix *is* the vendor, which is how the
	// double-prefixed nvidia/nvidia-nemotron-* rows get attributed.
	if got := InferProvider("nvidia-nemotron-nano-9b-v2"); got != "nvidia" {
		t.Errorf("nvidia-nemotron-nano-9b-v2 = %q, want nvidia", got)
	}
	// MiniMax's own Hailuo video line was already attributed; keep it that way
	// and make sure the new entry did not move it.
	if got := InferProvider("hailuo-02"); got != "minimax" {
		t.Errorf("hailuo-02 = %q, want minimax", got)
	}
	// minicpm stays OpenBMB even though "mini" now also starts a MiniMax rule.
	if got := InferProvider("minicpm-3-4b"); got != "openbmb" {
		t.Errorf("minicpm-3-4b = %q, want openbmb", got)
	}
}

func TestNormalizeCapabilityKindFallback(t *testing.T) {
	// Unknown kinds degrade to chat, never to an image endpoint.
	for _, in := range []string{"", "banana", "image_generation", "i2i", "t2v"} {
		got := NormalizeCapabilityKind(in)
		switch in {
		case "image_generation":
			if got != CapabilityKindImageGen {
				t.Errorf("%q -> %q", in, got)
			}
		case "i2i":
			if got != CapabilityKindImageEdit {
				t.Errorf("%q -> %q", in, got)
			}
		case "t2v":
			if got != CapabilityKindVideo {
				t.Errorf("%q -> %q", in, got)
			}
		default:
			if got != CapabilityKindChat {
				t.Errorf("%q -> %q, want chat", in, got)
			}
		}
	}
}

func TestCapabilityHelpers(t *testing.T) {
	c := Capability{
		Endpoints:        []string{"/v1/images/edits"},
		InputFormats:     []string{"json", "multipart"},
		InputModalities:  []string{"text", "image"},
		OutputModalities: []string{"image"},
		Kind:             CapabilityKindImageEdit,
	}
	if !c.CanAcceptImages() {
		t.Error("expected CanAcceptImages")
	}
	if !c.IsImageModel() {
		t.Error("expected IsImageModel")
	}
	if !c.HasEndpoint("/v1/images/edits") || c.HasEndpoint("/v1/images/generations") {
		t.Error("endpoint matching wrong")
	}
}

func TestCSVRoundTrip(t *testing.T) {
	joined := JoinCSV([]string{" json ", "multipart", "", "json"})
	if joined != "json,multipart" {
		t.Fatalf("JoinCSV = %q", joined)
	}
	got := SplitCSV(" a, b ,,c ")
	if len(got) != 3 || got[0] != "a" || got[2] != "c" {
		t.Fatalf("SplitCSV = %v", got)
	}
}
