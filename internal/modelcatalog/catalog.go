// Package modelcatalog turns third-party model indexes into the two rows the
// gateway actually keeps: a protocol capability (how to call the model) and
// model metadata (what the model is, including price).
//
// Two sources are supported because they are good at different things:
//
//   - LiteLLM's price list carries the request shape (mode, supported
//     endpoints, tool/streaming flags) and per-token prices for 4000+ ids.
//   - models.dev carries curated modality, context limit and per-million cost
//     for ~200 providers, including image and video models LiteLLM omits.
//
// Neither is authoritative for this deployment, so nothing here ever touches a
// manual override, and prices are only ever filled where the gateway has none.
package modelcatalog

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/lan/meta-gateway/internal/domain"
)

// Source identifiers.
const (
	SourceLiteLLM   = "litellm"
	SourceModelsDev = "models.dev"
)

// maxReportedSkips caps how many undecodable entries a source reports. A curated
// index can carry a handful of odd rows; flooding the operator with all of them
// would bury the one error that matters.
const maxReportedSkips = 5

// ParseResult is what a catalog parser produces: the entries it understood, plus
// a note for each entry it had to drop.
//
// Tolerance is per entry on purpose. These documents are third-party and change
// without notice; letting one malformed row abort the parse would silently cost
// thousands of usable models, which is a far worse failure than a gap.
type ParseResult struct {
	Entries []Entry
	Skipped []string
	// Overflow counts the dropped rows that are not named in Skipped, so a
	// truncated report cannot be mistaken for the whole story.
	Overflow int
}

// addSkip records a dropped entry, keeping the list bounded and deterministic.
func (r *ParseResult) addSkip(format string, args ...any) {
	r.Skipped = append(r.Skipped, fmt.Sprintf(format, args...))
}

// report sorts the skip list and trims it to the reporting cap, folding the
// remainder into a trailing count.
func (r *ParseResult) report() {
	sort.Strings(r.Skipped)
	if len(r.Skipped) <= maxReportedSkips {
		return
	}
	r.Overflow = len(r.Skipped) - maxReportedSkips
	r.Skipped = append(r.Skipped[:maxReportedSkips], fmt.Sprintf("…and %d more", r.Overflow))
}

// flexInt decodes a JSON number that a catalog entry may write as a string.
// LiteLLM's own schema example does exactly that, and treating a non-numeric
// value as "absent" is safe here: 0 means "no limit asserted", which suppresses
// the write rather than inventing one.
type flexInt int64

func (f *flexInt) UnmarshalJSON(data []byte) error {
	text := strings.Trim(strings.TrimSpace(string(data)), `"`)
	if text == "" || text == "null" {
		return nil
	}
	value, err := strconv.ParseInt(text, 10, 64)
	if err != nil {
		return nil
	}
	*f = flexInt(value)
	return nil
}

// flexFloat is flexInt's counterpart for the cost columns.
type flexFloat float64

func (f *flexFloat) UnmarshalJSON(data []byte) error {
	text := strings.Trim(strings.TrimSpace(string(data)), `"`)
	if text == "" || text == "null" {
		return nil
	}
	value, err := strconv.ParseFloat(text, 64)
	if err != nil {
		return nil
	}
	*f = flexFloat(value)
	return nil
}

// DefaultURLs are the upstream indexes. Both are plain JSON documents.
var DefaultURLs = map[string]string{
	SourceLiteLLM:   "https://raw.githubusercontent.com/BerriAI/litellm/main/model_prices_and_context_window.json",
	SourceModelsDev: "https://models.dev/api.json",
}

// Entry is one model as described by one or more catalogs, normalized onto the
// gateway's own vocabulary.
//
// Every entry starts from the built-in name classifier (see NewEntry) so a
// catalog row can never be *less* complete than the heuristic it replaces; the
// parsers then overwrite whatever the catalog actually asserts.
//
// The asserted-group flags exist because the two sources describe different
// things: LiteLLM knows the request shape, models.dev knows the modalities and
// limits. A source that stays silent about a group must not be read as asserting
// a zero, so Merge copies a group only from the entry that claimed it.
type Entry struct {
	Model    string
	Sources  []string
	Provider string

	// Kind/Endpoints/InputFormats/MaxInputImages/SizeOptions and the boolean
	// request flags form the "shape" group.
	Kind             string
	Endpoints        []string
	InputFormats     []string
	MaxInputImages   int
	SizeOptions      string
	SupportsStream   bool
	SupportsTools    bool
	SupportsJSONMode bool
	AsyncTask        bool

	// Modalities form their own group: models.dev curates them, LiteLLM only
	// sometimes states them.
	InputModal []string
	OutputMod  []string

	// Limits.
	ContextWindow   int64
	MaxOutputTokens int64

	// SupportsThinking mirrors domain.ModelMetadata: -1 unknown, 0 no, 1 yes.
	SupportsThinking int

	// Prices are per 1k tokens, converted from the source's unit.
	PricePromptPer1k     float64
	PriceCompletionPer1k float64
	PriceCachePer1k      float64

	// Asserted groups. Only the first source to claim a group keeps it; the
	// context and output limits are tracked separately because LiteLLM often
	// knows one without the other.
	HasShape     bool
	HasModality  bool
	HasContext   bool
	HasMaxOutput bool
	HasThinking  bool
	HasPrice     bool
}

// NewEntry seeds an entry from the built-in name classifier. Catalogs describe
// the same model with different granularity, so starting from a complete
// baseline keeps the endpoint/encoding fields populated when a source is silent.
func NewEntry(model string, source string) Entry {
	inferred := domain.ClassifyModel(model)
	return Entry{
		Model:            strings.TrimSpace(model),
		Sources:          []string{source},
		Provider:         inferred.Provider,
		Kind:             inferred.Kind,
		Endpoints:        inferred.Endpoints,
		InputFormats:     inferred.InputFormats,
		InputModal:       inferred.InputModalities,
		OutputMod:        inferred.OutputModalities,
		MaxInputImages:   inferred.MaxInputImages,
		SupportsStream:   inferred.SupportsStream,
		SupportsThinking: -1,
		SizeOptions:      inferred.SizeOptions,
	}
}

// addSource appends a source id once, keeping a stable order.
func (e *Entry) addSource(source string) {
	for _, existing := range e.Sources {
		if existing == source {
			return
		}
	}
	e.Sources = append(e.Sources, source)
}

func (e *Entry) addSources(sources ...string) {
	for _, source := range sources {
		if strings.TrimSpace(source) == "" {
			continue
		}
		e.addSource(source)
	}
}

// mergeMissing folds a lower-priority entry into a higher-priority one: the
// receiver keeps every group it claimed, later entries only fill the groups it
// stayed silent about.
func (e *Entry) mergeMissing(other Entry) {
	e.addSources(other.Sources...)
	if e.Provider == "" {
		e.Provider = other.Provider
	}
	if other.HasShape && !e.HasShape {
		e.Kind = other.Kind
		e.Endpoints = other.Endpoints
		e.InputFormats = other.InputFormats
		e.MaxInputImages = other.MaxInputImages
		e.SupportsStream = other.SupportsStream
		e.SupportsTools = other.SupportsTools
		e.SupportsJSONMode = other.SupportsJSONMode
		e.AsyncTask = other.AsyncTask
		if other.SizeOptions != "" {
			e.SizeOptions = other.SizeOptions
		}
		e.HasShape = true
	}
	if other.HasModality {
		if !e.HasModality {
			e.InputModal = other.InputModal
			e.OutputMod = other.OutputMod
			e.HasModality = true
		} else {
			// Modalities are a set, not a choice, so they union instead of one
			// source overruling the other. LiteLLM's shape authority does not
			// extend to this column: its coarse list often omits pdf and video
			// that the curated catalog knows, and a sync that silently narrowed
			// a row would be a regression, not a refresh.
			e.InputModal = unionStrings(e.InputModal, other.InputModal)
			e.OutputMod = unionStrings(e.OutputMod, other.OutputMod)
		}
	}
	if other.HasContext && !e.HasContext {
		e.ContextWindow = other.ContextWindow
		e.HasContext = true
	}
	if other.HasMaxOutput && !e.HasMaxOutput {
		e.MaxOutputTokens = other.MaxOutputTokens
		e.HasMaxOutput = true
	}
	if other.HasThinking && !e.HasThinking {
		e.SupportsThinking = other.SupportsThinking
		e.HasThinking = true
	}
	if other.HasPrice && !e.HasPrice {
		e.PricePromptPer1k = other.PricePromptPer1k
		e.PriceCompletionPer1k = other.PriceCompletionPer1k
		e.PriceCachePer1k = other.PriceCachePer1k
		e.HasPrice = true
	}
}

// Merge combines entries describing the same model. The first entry wins field
// by field; later entries only fill gaps.
func Merge(entries ...Entry) Entry {
	var out Entry
	for _, entry := range entries {
		if out.Model == "" {
			out = entry
			out.Sources = nil
			out.addSources(entry.Sources...)
			continue
		}
		out.mergeMissing(entry)
	}
	return out
}

// dedupeStrings trims, lowercases, drops empties and removes duplicates while
// preserving order — catalogs list modalities inconsistently and repeats would
// otherwise reach the CSV columns.
func dedupeStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.ToLower(strings.TrimSpace(value))
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		out = append(out, trimmed)
	}
	return out
}

// unionStrings concatenates two lists, receiver first, dropping duplicates and
// empties. The receiver's order is preserved because it reflects the source
// that spoke first.
func unionStrings(first, second []string) []string {
	combined := make([]string, 0, len(first)+len(second))
	combined = append(combined, first...)
	combined = append(combined, second...)
	return dedupeStrings(combined)
}

// usdPerMillionToPer1k converts "dollars per million tokens" (models.dev) into
// the gateway's per-1k unit.
func usdPerMillionToPer1k(value float64) float64 {
	if value <= 0 {
		return 0
	}
	return value / 1000
}

// usdPerTokenToPer1k converts "dollars per token" (LiteLLM).
func usdPerTokenToPer1k(value float64) float64 {
	if value <= 0 {
		return 0
	}
	return value * 1000
}

// kindFromMode maps a LiteLLM `mode` onto a domain capability kind. Unknown
// modes return the empty string so callers can skip non-model entries.
func kindFromMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "chat", "completion", "responses":
		return domain.CapabilityKindChat
	case "image_generation":
		return domain.CapabilityKindImageGen
	case "image_edit":
		return domain.CapabilityKindImageEdit
	case "video_generation", "video":
		return domain.CapabilityKindVideo
	case "embedding":
		return domain.CapabilityKindEmbedding
	case "audio_speech":
		return domain.CapabilityKindAudioTTS
	case "audio_transcription":
		return domain.CapabilityKindAudioSTT
	case "rerank":
		return domain.CapabilityKindRerank
	case "moderation":
		return domain.CapabilityKindModeration
	default:
		return ""
	}
}

// endpointsForKind is the fallback endpoint set used when a catalog states a
// model's kind but not its endpoints. It mirrors the built-in classifier.
func endpointsForKind(kind string) []string {
	switch kind {
	case domain.CapabilityKindImageGen:
		return []string{"/v1/images/generations"}
	case domain.CapabilityKindImageEdit:
		return []string{"/v1/images/edits"}
	case domain.CapabilityKindEmbedding:
		return []string{"/v1/embeddings"}
	case domain.CapabilityKindAudioTTS:
		return []string{"/v1/audio/speech"}
	case domain.CapabilityKindAudioSTT:
		return []string{"/v1/audio/transcriptions"}
	case domain.CapabilityKindRerank:
		return []string{"/v1/rerank"}
	case domain.CapabilityKindModeration:
		return []string{"/v1/moderations"}
	default:
		return []string{"/v1/chat/completions"}
	}
}

// formatsForEndpoints lists the request encodings an endpoint pair accepts.
// Image endpoints take both JSON and multipart; everything else is JSON, except
// audio transcription which is multipart-only upstream.
func formatsForEndpoints(endpoints []string) []string {
	formats := []string{"json"}
	for _, endpoint := range endpoints {
		switch strings.ToLower(strings.TrimSpace(endpoint)) {
		case "/v1/images/generations", "/v1/images/edits":
			formats = append(formats, "multipart")
		case "/v1/audio/transcriptions":
			return []string{"multipart"}
		}
	}
	return dedupeStrings(formats)
}

// firstPositive returns the first strictly positive value, or zero when none is.
// Catalogs record the same limit under several keys with uneven coverage —
// LiteLLM writes max_tokens on many rows that have neither of the more precise
// fields — so a fallback chain beats picking one key and losing the rest.
func firstPositive(values ...int64) int64 {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
}

// boolToThinking projects a boolean capability onto domain.ModelMetadata's
// tri-state (-1 unknown, 0 no, 1 yes).
func boolToThinking(supported bool) int {
	if supported {
		return 1
	}
	return 0
}
