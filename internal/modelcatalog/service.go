package modelcatalog

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/lan/meta-gateway/internal/domain"
	"github.com/lan/meta-gateway/internal/store"
)

// maxDocumentBytes caps how much of an upstream index is read. Both documents
// are a few megabytes today; the cap keeps a hostile or broken response from
// exhausting memory.
const maxDocumentBytes = 32 << 20

// fetchTimeout bounds a single catalog download.
const fetchTimeout = 45 * time.Second

// Options configures a Service.
type Options struct {
	// Client performs the downloads. Callers pass the SSRF-policy client so the
	// console cannot be pointed at internal addresses.
	Client *http.Client
	// URLs overrides the source endpoints (empty uses DefaultURLs).
	URLs map[string]string
}

// Service owns the external-catalog sync: it downloads the indexes, merges them
// into one view per model, and applies that view to the registry.
type Service struct {
	db     *store.DB
	client *http.Client
	urls   map[string]string

	// syncMu serializes whole sync operations so a scheduled sweep and an
	// operator click cannot interleave into a half-written registry; cacheMu
	// guards only the snapshot.
	syncMu  sync.Mutex
	cacheMu sync.Mutex

	cachedAt time.Time
	cached   []Entry
}

// New builds a Service.
func New(db *store.DB, opts Options) *Service {
	client := opts.Client
	if client == nil {
		client = &http.Client{Timeout: fetchTimeout}
	}
	urls := DefaultURLs
	if len(opts.URLs) > 0 {
		urls = opts.URLs
	}
	return &Service{db: db, client: client, urls: urls}
}

// Sources lists the configured source ids in a stable order.
func (s *Service) Sources() []string {
	out := make([]string, 0, len(s.urls))
	for source := range s.urls {
		out = append(out, source)
	}
	sort.Strings(out)
	return out
}

// Fetch downloads and merges every configured source. A source that fails is
// reported but does not abort the others: a gateway whose network reaches only
// one index should still get that index.
func (s *Service) Fetch(ctx context.Context) ([]Entry, []string, error) {
	merged := map[string]Entry{}
	var problems []string
	succeeded := 0
	for _, source := range s.Sources() {
		raw, err := s.download(ctx, s.urls[source])
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s: %v", source, err))
			continue
		}
		var parsed ParseResult
		switch source {
		case SourceLiteLLM:
			parsed, err = ParseLiteLLM(raw)
		case SourceModelsDev:
			parsed, err = ParseModelsDev(raw)
		default:
			err = fmt.Errorf("unknown catalog source")
		}
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s: %v", source, err))
			continue
		}
		// Rows the parser could not read are surfaced but never fatal: the
		// point of per-entry tolerance is that a handful of odd rows in a
		// third-party index cannot cost the operator the other four thousand.
		if len(parsed.Skipped) > 0 {
			problems = append(problems, fmt.Sprintf("%s: %d row(s) skipped: %s",
				source, len(parsed.Skipped)+parsed.Overflow, strings.Join(parsed.Skipped, "; ")))
		}
		succeeded++
		for _, entry := range parsed.Entries {
			if existing, ok := merged[entry.Model]; ok {
				// LiteLLM parses first (alphabetical) and carries the request
				// shape; models.dev only fills what is still unknown.
				merged[entry.Model] = Merge(existing, entry)
				continue
			}
			merged[entry.Model] = entry
		}
	}
	if succeeded == 0 {
		return nil, problems, fmt.Errorf("every catalog source failed")
	}
	out := make([]Entry, 0, len(merged))
	for _, entry := range merged {
		out = append(out, entry)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Model < out[j].Model })
	s.cacheMu.Lock()
	s.cached, s.cachedAt = out, time.Now().UTC()
	s.cacheMu.Unlock()
	return out, problems, nil
}

// Cached returns the last fetched snapshot, if any, without hitting the network.
func (s *Service) Cached() ([]Entry, time.Time, bool) {
	s.cacheMu.Lock()
	defer s.cacheMu.Unlock()
	if s.cached == nil {
		return nil, time.Time{}, false
	}
	return s.cached, s.cachedAt, true
}

// download reads one index with a size cap.
func (s *Service) download(ctx context.Context, url string) ([]byte, error) {
	if strings.TrimSpace(url) == "" {
		return nil, errors.New("no url configured")
	}
	ctx, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "application/json")
	response, err := s.client.Do(request)
	if err != nil {
		return nil, err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("upstream returned HTTP %d", response.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, maxDocumentBytes+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > maxDocumentBytes {
		return nil, fmt.Errorf("document exceeds %d bytes", maxDocumentBytes)
	}
	return raw, nil
}

// Policy controls what an apply is allowed to write. Both switches default to
// true: a sync that changes nothing is not worth the network round trip, and
// every write stays additive (see Apply).
type Policy struct {
	Capabilities bool
	Metadata     bool
	Prices       bool
}

// DefaultPolicy is the policy used by the scheduled sweep.
var DefaultPolicy = Policy{Capabilities: true, Metadata: true, Prices: true}

// Runner is the narrow view of Service used by the background sweep, so the
// wiring stays testable.
type Runner interface {
	Sync(ctx context.Context, models []string, policy Policy) (*domain.CatalogSyncState, error)
}

// Sync fetches the catalogs, plans the changes for the given models and applies
// them, recording the outcome on the status board.
func (s *Service) Sync(ctx context.Context, models []string, policy Policy) (*domain.CatalogSyncState, error) {
	s.syncMu.Lock()
	defer s.syncMu.Unlock()
	entries, problems, err := s.Fetch(ctx)
	if err != nil {
		return nil, err
	}
	preview, err := s.plan(models, entries, policy, problems)
	if err != nil {
		return nil, err
	}
	// The same entries that produced the plan drive the writes, so what the
	// operator reviewed and what landed cannot diverge.
	state, err := s.apply(preview, entries)
	if err != nil {
		return nil, err
	}
	state.SyncedAt = time.Now().UTC().Format(time.RFC3339Nano)
	if err := s.db.ModelCatalog.SaveCatalogState(state); err != nil {
		return state, err
	}
	return state, nil
}

// Preview answers "what would a sync change" without writing anything. It
// reuses the last snapshot when one exists so an operator can inspect a plan
// after a scheduled sweep already downloaded the indexes.
func (s *Service) Preview(ctx context.Context, models []string, policy Policy) (*domain.CatalogPreview, error) {
	entries, problems, err := s.Fetch(ctx)
	if err != nil {
		return nil, err
	}
	return s.plan(models, entries, policy, problems)
}

// PreviewCached plans against the last snapshot. It fails when nothing has been
// fetched yet, which is the signal for the console to ask for a live preview.
func (s *Service) PreviewCached(models []string, policy Policy) (*domain.CatalogPreview, error) {
	entries, _, ok := s.Cached()
	if !ok {
		return nil, errors.New("no catalog snapshot yet")
	}
	return s.plan(models, entries, policy, nil)
}

// plan computes the per-model diff. It is the single place that decides what a
// sync would touch, so the preview an operator reads and the writes that follow
// can never disagree.
func (s *Service) plan(models []string, entries []Entry, policy Policy, problems []string) (*domain.CatalogPreview, error) {
	index := make(map[string]Entry, len(entries))
	for _, entry := range entries {
		index[entry.Model] = entry
	}
	preview := &domain.CatalogPreview{
		Sources:       s.Sources(),
		Errors:        problems,
		PricesEnabled: policy.Prices,
		Fetched:       true,
		Items:         make([]domain.CatalogPreviewItem, 0, len(models)),
	}
	seen := map[string]struct{}{}
	for _, raw := range models {
		model := strings.TrimSpace(raw)
		if model == "" {
			continue
		}
		if _, ok := seen[model]; ok {
			continue
		}
		seen[model] = struct{}{}
		preview.Requested++
		item := domain.CatalogPreviewItem{Model: model}
		entry, found := index[model]
		if found {
			item.Found = true
			item.Source = entry.Sources
			preview.Matched++
		} else {
			preview.Missing++
		}

		existingCap, err := s.db.ModelCapability.Get(model)
		if err != nil {
			return nil, err
		}
		item.CapabilityAction, item.CapabilitySource, item.CapabilityKind, item.CapabilityChanges =
			planCapability(found, entry, existingCap, policy.Capabilities)

		existingMeta, err := s.db.ModelMetadata.Get(model)
		if err != nil {
			return nil, err
		}
		item.MetadataAction, item.MetadataChanges = planMetadata(found, entry, existingMeta, policy.Metadata)
		item.PriceAction, item.PriceChanges = planPrices(found, entry, existingMeta, policy.Prices, policy.Metadata)

		preview.Items = append(preview.Items, item)
	}
	return preview, nil
}

// planCapability decides the capability write for one model. A manual row is
// never proposed for change — that is the operator's override, and silently
// reverting it would be the worst failure mode this feature has.
func planCapability(found bool, entry Entry, existing *domain.Capability, enabled bool) (string, string, string, []domain.CatalogFieldChange) {
	if !enabled {
		source := domain.CapabilitySourceBuiltin
		if existing != nil {
			source = existing.Source
		}
		return domain.CatalogActionDisabled, source, "", nil
	}
	if !found {
		source := domain.CapabilitySourceBuiltin
		if existing != nil {
			source = existing.Source
		}
		return domain.CatalogActionUnchanged, source, "", nil
	}
	if existing != nil && !domain.CatalogWritable(existing.Source) {
		return domain.CatalogActionSkipManual, existing.Source, existing.Kind, nil
	}
	next := capabilityFromEntry(entry)
	if existing == nil {
		return domain.CatalogActionCreate, domain.CapabilitySourceCatalog, next.Kind,
			capabilityDiff(domain.Capability{Model: entry.Model, Kind: next.Kind}, next)
	}
	changes := capabilityDiff(*existing, next)
	if len(changes) == 0 {
		return domain.CatalogActionUnchanged, existing.Source, existing.Kind, nil
	}
	return domain.CatalogActionRefresh, domain.CapabilitySourceCatalog, next.Kind, changes
}

// planMetadata decides the metadata write. Metadata has no source column, so
// the rule is positional instead: only fields the gateway currently leaves
// unset are filled. That makes every metadata write reversible by clearing the
// field again, and makes an operator-entered value authoritative by definition.
func planMetadata(found bool, entry Entry, existing *domain.ModelMetadata, enabled bool) (string, []domain.CatalogFieldChange) {
	if !enabled || !found {
		return domain.CatalogActionUnchanged, nil
	}
	current := domain.ModelMetadata{ModelName: entry.Model, SupportsThinking: -1}
	if existing != nil {
		current = *existing
	}
	next := mergeMetadata(current, entry)
	changes := metadataDiff(current, next)
	if len(changes) == 0 {
		return domain.CatalogActionUnchanged, nil
	}
	if existing == nil {
		return domain.CatalogActionCreate, changes
	}
	return domain.CatalogActionFill, changes
}

// planPrices is metadata's twin, kept separate because a price write changes
// what a request costs.
func planPrices(found bool, entry Entry, existing *domain.ModelMetadata, pricesEnabled, metadataEnabled bool) (string, []domain.CatalogFieldChange) {
	if !metadataEnabled {
		return domain.CatalogActionUnchanged, nil
	}
	if !pricesEnabled {
		return domain.CatalogActionDisabled, nil
	}
	if !found || !entry.HasPrice {
		return domain.CatalogActionUnchanged, nil
	}
	current := domain.ModelMetadata{ModelName: entry.Model, SupportsThinking: -1}
	if existing != nil {
		current = *existing
	}
	next := mergePrices(current, entry)
	changes := priceDiff(current, next)
	if len(changes) == 0 {
		return domain.CatalogActionUnchanged, nil
	}
	if existing == nil {
		return domain.CatalogActionCreate, changes
	}
	return domain.CatalogActionFill, changes
}

// apply executes a plan and returns the status board for it.
func (s *Service) apply(preview *domain.CatalogPreview, entries []Entry) (*domain.CatalogSyncState, error) {
	state := &domain.CatalogSyncState{
		Requested: preview.Requested,
		Matched:   preview.Matched,
		Missing:   preview.Missing,
		Sources:   preview.Sources,
		Errors:    preview.Errors,
	}
	index := make(map[string]Entry, len(entries))
	for _, entry := range entries {
		index[entry.Model] = entry
	}

	for _, item := range preview.Items {
		entry := index[item.Model]
		if item.CapabilityAction == domain.CatalogActionCreate || item.CapabilityAction == domain.CatalogActionRefresh {
			next := capabilityFromEntry(entry)
			if err := s.db.ModelCapability.Upsert(&next); err != nil {
				return state, err
			}
			state.Capabilities++
		}
		if item.CapabilityAction == domain.CatalogActionSkipManual {
			state.SkippedManual++
		}
		if !item.Found {
			continue
		}
		existing, err := s.db.ModelMetadata.Get(item.Model)
		if err != nil {
			return state, err
		}
		current := domain.ModelMetadata{ModelName: item.Model, SupportsThinking: -1}
		if existing != nil {
			current = *existing
		}
		next := mergeMetadata(current, entry)
		if item.PriceAction == domain.CatalogActionCreate || item.PriceAction == domain.CatalogActionFill {
			next = mergePrices(next, entry)
			state.Prices += len(item.PriceChanges)
		}
		if len(metadataDiff(current, next)) > 0 || len(priceDiff(current, next)) > 0 {
			if err := s.db.ModelMetadata.Upsert(&next); err != nil {
				return state, err
			}
			state.Metadata++
		}
	}
	return state, nil
}

// capabilityFromEntry projects a catalog entry onto a registry row.
func capabilityFromEntry(entry Entry) domain.Capability {
	return domain.Capability{
		Model:            entry.Model,
		Kind:             domain.NormalizeCapabilityKind(entry.Kind),
		Provider:         entry.Provider,
		Endpoints:        entry.Endpoints,
		InputFormats:     entry.InputFormats,
		InputModalities:  entry.InputModal,
		OutputModalities: entry.OutputMod,
		MaxInputImages:   entry.MaxInputImages,
		SupportsStream:   entry.SupportsStream,
		SupportsTools:    entry.SupportsTools,
		SupportsJSONMode: entry.SupportsJSONMode,
		AsyncTask:        entry.AsyncTask,
		SizeOptions:      entry.SizeOptions,
		Source:           domain.CapabilitySourceCatalog,
		Notes:            "synced from " + strings.Join(entry.Sources, "+"),
	}
}

// mergeMetadata fills only the metadata fields the gateway currently leaves
// unset. A group the catalogs never asserted is skipped entirely, so silence is
// never mistaken for a value; and because the range of a write is "empty -> a
// value", clearing a field is all it takes to let a later sync refill it.
func mergeMetadata(current domain.ModelMetadata, entry Entry) domain.ModelMetadata {
	if entry.HasContext && current.ContextWindow <= 0 && entry.ContextWindow > 0 {
		current.ContextWindow = entry.ContextWindow
	}
	if entry.HasModality {
		if current.InputModalities == "" && len(entry.InputModal) > 0 {
			current.InputModalities = domain.JoinCSV(entry.InputModal)
		}
		if current.OutputModalities == "" && len(entry.OutputMod) > 0 {
			current.OutputModalities = domain.JoinCSV(entry.OutputMod)
		}
	}
	if entry.HasThinking && current.SupportsThinking == -1 {
		current.SupportsThinking = entry.SupportsThinking
	}
	if strings.TrimSpace(current.Vendor) == "" && entry.Provider != "" {
		current.Vendor = entry.Provider
	}
	return current
}

// mergePrices fills only the per-1k prices that are currently zero, so an
// operator's own price always wins and a sync can never re-price a model.
func mergePrices(current domain.ModelMetadata, entry Entry) domain.ModelMetadata {
	if !entry.HasPrice {
		return current
	}
	if current.PricePromptPer1k == 0 && entry.PricePromptPer1k > 0 {
		current.PricePromptPer1k = entry.PricePromptPer1k
	}
	if current.PriceCompletionPer1k == 0 && entry.PriceCompletionPer1k > 0 {
		current.PriceCompletionPer1k = entry.PriceCompletionPer1k
	}
	if current.PriceCachePer1k == 0 && entry.PriceCachePer1k > 0 {
		current.PriceCachePer1k = entry.PriceCachePer1k
	}
	return current
}

// capabilityDiff lists the capability fields that differ.
func capabilityDiff(current, next domain.Capability) []domain.CatalogFieldChange {
	var changes []domain.CatalogFieldChange
	add := func(field, from, to string) {
		if from != to {
			changes = append(changes, domain.CatalogFieldChange{Field: field, From: from, To: to})
		}
	}
	add("kind", current.Kind, next.Kind)
	add("provider", current.Provider, next.Provider)
	add("endpoints", domain.JoinCSV(current.Endpoints), domain.JoinCSV(next.Endpoints))
	add("input_formats", domain.JoinCSV(current.InputFormats), domain.JoinCSV(next.InputFormats))
	add("input_modalities", domain.JoinCSV(current.InputModalities), domain.JoinCSV(next.InputModalities))
	add("output_modalities", domain.JoinCSV(current.OutputModalities), domain.JoinCSV(next.OutputModalities))
	if current.MaxInputImages != next.MaxInputImages {
		changes = append(changes, domain.CatalogFieldChange{
			Field: "max_input_images",
			From:  fmt.Sprint(current.MaxInputImages),
			To:    fmt.Sprint(next.MaxInputImages),
		})
	}
	if current.SupportsStream != next.SupportsStream {
		changes = append(changes, boolChange("supports_stream", current.SupportsStream, next.SupportsStream))
	}
	if current.SupportsTools != next.SupportsTools {
		changes = append(changes, boolChange("supports_tools", current.SupportsTools, next.SupportsTools))
	}
	if current.SupportsJSONMode != next.SupportsJSONMode {
		changes = append(changes, boolChange("supports_json_mode", current.SupportsJSONMode, next.SupportsJSONMode))
	}
	return changes
}

// metadataDiff lists the metadata fields that differ.
func metadataDiff(current, next domain.ModelMetadata) []domain.CatalogFieldChange {
	var changes []domain.CatalogFieldChange
	add := func(field, from, to string) {
		if from != to {
			changes = append(changes, domain.CatalogFieldChange{Field: field, From: from, To: to})
		}
	}
	if current.ContextWindow != next.ContextWindow {
		changes = append(changes, domain.CatalogFieldChange{
			Field: "context_window",
			From:  fmt.Sprint(current.ContextWindow),
			To:    fmt.Sprint(next.ContextWindow),
		})
	}
	add("input_modalities", current.InputModalities, next.InputModalities)
	add("output_modalities", current.OutputModalities, next.OutputModalities)
	if current.SupportsThinking != next.SupportsThinking {
		changes = append(changes, domain.CatalogFieldChange{
			Field: "supports_thinking",
			From:  fmt.Sprint(current.SupportsThinking),
			To:    fmt.Sprint(next.SupportsThinking),
		})
	}
	add("vendor", current.Vendor, next.Vendor)
	return changes
}

// priceDiff lists the price fields that differ.
func priceDiff(current, next domain.ModelMetadata) []domain.CatalogFieldChange {
	var changes []domain.CatalogFieldChange
	add := func(field string, from, to float64) {
		if from != to {
			changes = append(changes, domain.CatalogFieldChange{
				Field: field,
				From:  formatPrice(from),
				To:    formatPrice(to),
			})
		}
	}
	add("price_prompt_per_1k", current.PricePromptPer1k, next.PricePromptPer1k)
	add("price_completion_per_1k", current.PriceCompletionPer1k, next.PriceCompletionPer1k)
	add("price_cache_per_1k", current.PriceCachePer1k, next.PriceCachePer1k)
	return changes
}

func boolChange(field string, from, to bool) domain.CatalogFieldChange {
	return domain.CatalogFieldChange{Field: field, From: fmt.Sprint(from), To: fmt.Sprint(to)}
}

// formatPrice renders a per-1k price compactly; catalog prices are often
// fractions of a cent, so fixed notation with trailing zeros trimmed keeps the
// preview readable.
func formatPrice(value float64) string {
	if value == 0 {
		return "0"
	}
	text := fmt.Sprintf("%.7f", value)
	text = strings.TrimRight(text, "0")
	return strings.TrimRight(text, ".")
}
