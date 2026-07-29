package exchange

import (
	"context"
	"crypto/subtle"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/lan/meta-gateway/internal/crypto"
	"github.com/lan/meta-gateway/internal/discovery"
	"github.com/lan/meta-gateway/internal/domain"
	"github.com/lan/meta-gateway/internal/store"
)

type DiscoveryRefresher interface {
	Refresh(context.Context, int64) (*discovery.RefreshResult, error)
}

// KeySyncer pulls upstream sk- keys using a site user token (best-effort).
type KeySyncer interface {
	SyncKeys(ctx context.Context, channelID int64) (created, reused, masked int, category string, err error)
}

type ExportRequest struct {
	IncludeSecrets bool    `json:"include_secrets"`
	ChannelIDs     []int64 `json:"channel_ids,omitempty"`
}

type DiscoveryOutcome struct {
	ChannelID int64  `json:"channel_id"`
	Status    string `json:"status"`
	Category  string `json:"category,omitempty"`
}

type CapabilityItem struct {
	ChannelID         int64  `json:"channel_id"`
	CredentialKind    string `json:"credential_kind,omitempty"`
	CheckinCapable    bool   `json:"checkin_capable"`
	HasAPIKey         bool   `json:"has_api_key"`
	DiscoveryStatus   string `json:"discovery_status,omitempty"`
	DiscoveryCategory string `json:"discovery_category,omitempty"`
}

type ImportResult struct {
	CreatedCount        int                `json:"created_count"`
	UpdatedCount        int                `json:"updated_count"`
	AdoptedCount        int                `json:"adopted_count"`
	ChannelIDs          []int64            `json:"channel_ids"`
	Discovery           []DiscoveryOutcome `json:"discovery"`
	DiscoveryOK         int                `json:"discovery_success_count"`
	DiscoveryFailed     int                `json:"discovery_failure_count"`
	CheckinCapableCount int                `json:"checkin_capable_count"`
	MissingAPIKeyCount  int                `json:"missing_api_key_count"`
	RelayReadyCount     int                `json:"relay_ready_count"`
	KeySyncOK           int                `json:"key_sync_success_count"`
	KeySyncFailed       int                `json:"key_sync_failure_count"`
	KeySyncSkipped      int                `json:"key_sync_skipped_count"`
	KeySync             []KeySyncOutcome   `json:"key_sync,omitempty"`
	Items               []CapabilityItem   `json:"items"`
}

type KeySyncOutcome struct {
	ChannelID int64  `json:"channel_id"`
	Status    string `json:"status"`
	Category  string `json:"category,omitempty"`
	Created   int    `json:"created,omitempty"`
	Reused    int    `json:"reused,omitempty"`
	Masked    int    `json:"masked,omitempty"`
}

type Service struct {
	db        *store.DB
	store     *store.ExchangeStore
	enc       *crypto.Encrypter
	discovery DiscoveryRefresher
	keySync   KeySyncer
	now       func() time.Time
}

func NewService(db *store.DB, enc *crypto.Encrypter, refresher DiscoveryRefresher) *Service {
	return &Service{db: db, store: db.Exchange, enc: enc, discovery: refresher, now: time.Now}
}

// SetKeySyncer enables best-effort API key pull after import (optional).
func (s *Service) SetKeySyncer(syncer KeySyncer) {
	s.keySync = syncer
}

func (s *Service) Export(ctx context.Context, request ExportRequest) (*Envelope, error) {
	if err := validateChannelIDs(request.ChannelIDs); err != nil {
		return nil, err
	}
	rows, err := s.store.Export(ctx, request.ChannelIDs)
	if err != nil {
		return nil, formatError(ErrorInternal)
	}
	if len(request.ChannelIDs) > 0 && len(rows) != len(request.ChannelIDs) {
		return nil, formatError(ErrorNotFound)
	}
	items := make([]Item, 0, len(rows))
	for _, row := range rows {
		if row.CredentialID == 0 || row.SecretEnc == "" {
			return nil, formatError(ErrorInternal)
		}
		baseURL := row.BaseURL
		if baseURL == "" {
			baseURL = row.SiteBaseURL
		}
		baseURL, normalizeErr := NormalizeBaseURL(baseURL)
		if normalizeErr != nil {
			return nil, formatError(ErrorInternal)
		}
		typeHint := row.TypeHint
		if typeHint == "" {
			typeHint = row.Platform
		}
		item := Item{Name: strings.TrimSpace(row.Name), BaseURL: baseURL,
			Models: normalizeList([]string{row.ModelsCSV}), Group: normalizeGroup(row.GroupName),
			Priority: row.Priority, Weight: row.Weight, SiteTypeHint: normalizeType(typeHint)}
		if request.IncludeSecrets {
			plaintext, decryptErr := s.enc.Decrypt(row.SecretEnc)
			if decryptErr != nil {
				return nil, formatError(ErrorInternal)
			}
			item.APIKey = string(plaintext)
			clearBytes(plaintext)
		}
		items = append(items, item)
	}
	return &Envelope{Format: Format, Version: Version, ExportedAt: s.now().UTC(),
		Importable: request.IncludeSecrets, Items: items}, nil
}

// ImportMode controls how an AAH / exchange document is applied.
const (
	// ImportModeIncremental merges into existing connections (default).
	// Same fingerprint updates; access_token/session may merge by site/name.
	ImportModeIncremental = "incremental"
	// ImportModeReplace deletes all existing sites/channels/credentials first,
	// then imports the backup as the sole source of truth.
	ImportModeReplace = "replace"
)

// ImportOptions customizes Import behavior.
type ImportOptions struct {
	// Mode is incremental (default) or replace.
	Mode string
}

func (s *Service) Import(ctx context.Context, data []byte) (*ImportResult, error) {
	return s.ImportWithOptions(ctx, data, ImportOptions{Mode: ImportModeIncremental})
}

func (s *Service) ImportWithOptions(ctx context.Context, data []byte, opts ImportOptions) (*ImportResult, error) {
	mode := strings.ToLower(strings.TrimSpace(opts.Mode))
	if mode == "" {
		mode = ImportModeIncremental
	}
	if mode != ImportModeIncremental && mode != ImportModeReplace {
		return nil, formatError(ErrorValidation)
	}
	items, err := Parse(data)
	if err != nil {
		return nil, err
	}
	var candidates []store.ExchangeLegacyCandidate
	if mode == ImportModeIncremental {
		candidates, err = s.store.LegacyCandidates(ctx)
		if err != nil {
			return nil, formatError(ErrorInternal)
		}
	}
	storeItems := make([]store.ExchangeImportItem, 0, len(items))
	for _, item := range items {
		apiKey := []byte(item.APIKey)
		fingerprint := s.enc.ExchangeFingerprint(item.BaseURL, apiKey)
		secretEnc, encryptErr := s.enc.Encrypt(apiKey)
		if encryptErr != nil {
			clearBytes(apiKey)
			return nil, formatError(ErrorInternal)
		}
		var adoptChannelID, adoptCredentialID int64
		if mode == ImportModeIncremental {
			var matchErr error
			adoptChannelID, adoptCredentialID, matchErr = s.matchLegacy(item.BaseURL, apiKey, candidates)
			if matchErr != nil {
				clearBytes(apiKey)
				return nil, matchErr
			}
		}
		clearBytes(apiKey)
		storeItems = append(storeItems, store.ExchangeImportItem{
			Name: item.Name, BaseURL: item.BaseURL, ModelsCSV: strings.Join(item.Models, ","),
			GroupName: item.Group, Priority: item.Priority, Weight: item.Weight,
			Status: item.Status, TypeHint: item.SiteTypeHint, SecretEnc: secretEnc,
			Fingerprint: fingerprint, AdoptChannelID: adoptChannelID,
			AdoptCredentialID: adoptCredentialID,
			CredentialKind:    item.CredentialKind, MetaJSON: item.MetaJSON,
			CheckinEnabled: item.CheckinEnabled,
		})
	}
	persisted, err := s.store.ImportReplacing(ctx, storeItems, mode == ImportModeReplace)
	if err != nil {
		if errors.Is(err, store.ErrExchangeConflict) {
			return nil, formatError(ErrorConflict)
		}
		return nil, formatError(ErrorInternal)
	}
	result := &ImportResult{CreatedCount: len(persisted.CreatedChannelIDs),
		UpdatedCount: len(persisted.UpdatedChannelIDs), AdoptedCount: len(persisted.AdoptedChannelIDs)}
	result.ChannelIDs = persisted.ChannelIDs()
	sort.Slice(result.ChannelIDs, func(i, j int) bool { return result.ChannelIDs[i] < result.ChannelIDs[j] })
	// Channels are already committed. Post-steps are best-effort only.
	// Skip pure updates (duplicate re-import): operator already has those assets.
	postProcessIDs := append([]int64{}, persisted.CreatedChannelIDs...)
	postProcessIDs = append(postProcessIDs, persisted.AdoptedChannelIDs...)
	sort.Slice(postProcessIDs, func(i, j int) bool { return postProcessIDs[i] < postProcessIDs[j] })
	s.syncKeysAfterImport(ctx, result, postProcessIDs)
	s.discoverAfterImport(ctx, result, postProcessIDs)
	s.enrichImportCapabilities(result)
	return result, nil
}

const (
	importWorkerCount      = 4
	importKeySyncTimeout   = 10 * time.Second
	importDiscoveryTimeout = 6 * time.Second
)

func (s *Service) syncKeysAfterImport(ctx context.Context, result *ImportResult, channelIDs []int64) {
	if s == nil || result == nil || s.keySync == nil || len(channelIDs) == 0 {
		return
	}
	type jobResult struct {
		outcome KeySyncOutcome
		ok      bool
		failed  bool
		skipped bool
	}
	jobs := make(chan int64)
	out := make(chan jobResult, len(channelIDs))
	var workers sync.WaitGroup
	workerN := importWorkerCount
	if workerN > len(channelIDs) {
		workerN = len(channelIDs)
	}
	for i := 0; i < workerN; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for channelID := range jobs {
				if err := ctx.Err(); err != nil {
					out <- jobResult{
						outcome: KeySyncOutcome{ChannelID: channelID, Status: "skipped", Category: "canceled"},
						skipped: true,
					}
					continue
				}
				// Only attempt when bound credential is a user token (missing API key path).
				if s.channelShouldDiscoverModels(channelID) {
					out <- jobResult{
						outcome: KeySyncOutcome{ChannelID: channelID, Status: "skipped", Category: "already_has_api_key"},
						skipped: true,
					}
					continue
				}
				syncCtx, cancel := context.WithTimeout(ctx, importKeySyncTimeout)
				created, reused, masked, category, syncErr := s.keySync.SyncKeys(syncCtx, channelID)
				cancel()
				outcome := KeySyncOutcome{
					ChannelID: channelID, Created: created, Reused: reused, Masked: masked, Category: category,
				}
				jr := jobResult{outcome: outcome}
				if syncErr != nil {
					outcome.Status = "failed"
					if category == "" {
						outcome.Category = "key_sync_failed"
					}
					jr.outcome = outcome
					jr.failed = true
				} else if created+reused > 0 {
					outcome.Status = "success"
					jr.outcome = outcome
					jr.ok = true
				} else {
					outcome.Status = "skipped"
					if outcome.Category == "" {
						if masked > 0 {
							outcome.Category = "keys_masked"
						} else {
							outcome.Category = "empty_token_list"
						}
					}
					jr.outcome = outcome
					jr.skipped = true
				}
				out <- jr
			}
		}()
	}
	go func() {
		for _, id := range channelIDs {
			jobs <- id
		}
		close(jobs)
		workers.Wait()
		close(out)
	}()
	result.KeySync = make([]KeySyncOutcome, 0, len(channelIDs))
	for jr := range out {
		result.KeySync = append(result.KeySync, jr.outcome)
		if jr.ok {
			result.KeySyncOK++
		} else if jr.failed {
			result.KeySyncFailed++
		} else {
			result.KeySyncSkipped++
		}
	}
	sort.Slice(result.KeySync, func(i, j int) bool {
		return result.KeySync[i].ChannelID < result.KeySync[j].ChannelID
	})
}

func (s *Service) discoverAfterImport(ctx context.Context, result *ImportResult, channelIDs []int64) {
	if s == nil || result == nil || s.discovery == nil {
		return
	}
	// Prefer channels that have/gained an API key after key sync.
	result.Discovery = make([]DiscoveryOutcome, 0, len(channelIDs))
	if len(channelIDs) == 0 {
		return
	}
	type jobResult struct {
		outcome DiscoveryOutcome
		ok      bool
		failed  bool
	}
	jobs := make(chan int64)
	out := make(chan jobResult, len(channelIDs))
	var workers sync.WaitGroup
	workerN := importWorkerCount
	if workerN > len(channelIDs) {
		workerN = len(channelIDs)
	}
	for i := 0; i < workerN; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for channelID := range jobs {
				if err := ctx.Err(); err != nil {
					out <- jobResult{
						outcome: DiscoveryOutcome{ChannelID: channelID, Status: "skipped", Category: "canceled"},
						failed:  true,
					}
					continue
				}
				if !s.channelShouldDiscoverModels(channelID) {
					out <- jobResult{
						outcome: DiscoveryOutcome{ChannelID: channelID, Status: "skipped", Category: "needs_api_key"},
						failed:  true,
					}
					continue
				}
				refreshCtx, cancel := context.WithTimeout(ctx, importDiscoveryTimeout)
				_, refreshErr := s.discovery.Refresh(refreshCtx, channelID)
				cancel()
				outcome := DiscoveryOutcome{ChannelID: channelID, Status: "success"}
				jr := jobResult{outcome: outcome, ok: true}
				if refreshErr != nil {
					outcome.Status = "failed"
					if errors.Is(refreshErr, context.DeadlineExceeded) || errors.Is(refreshCtx.Err(), context.DeadlineExceeded) {
						outcome.Category = "discovery_timeout"
					} else {
						outcome.Category = discoveryCategory(refreshErr)
					}
					jr = jobResult{outcome: outcome, failed: true}
				}
				out <- jr
			}
		}()
	}
	go func() {
		for _, id := range channelIDs {
			jobs <- id
		}
		close(jobs)
		workers.Wait()
		close(out)
	}()
	for jr := range out {
		result.Discovery = append(result.Discovery, jr.outcome)
		if jr.ok {
			result.DiscoveryOK++
		} else {
			result.DiscoveryFailed++
		}
	}
	sort.Slice(result.Discovery, func(i, j int) bool {
		return result.Discovery[i].ChannelID < result.Discovery[j].ChannelID
	})
}

func (s *Service) channelShouldDiscoverModels(channelID int64) bool {
	if s.db == nil {
		return true
	}
	channel, err := s.db.Channel.GetByID(channelID)
	if err != nil || channel == nil || channel.CredentialID == nil {
		return false
	}
	credential, err := s.db.Credential.GetByID(*channel.CredentialID)
	if err != nil || credential == nil {
		return false
	}
	kind := strings.ToLower(strings.TrimSpace(credential.Kind))
	// Only OpenAI-compatible API keys are expected to succeed on /v1/models.
	return kind == "api_key" || kind == ""
}

func (s *Service) enrichImportCapabilities(result *ImportResult) {
	if result == nil || s.db == nil {
		return
	}
	discoveryByChannel := make(map[int64]DiscoveryOutcome, len(result.Discovery))
	for _, outcome := range result.Discovery {
		discoveryByChannel[outcome.ChannelID] = outcome
	}
	result.Items = make([]CapabilityItem, 0, len(result.ChannelIDs))
	for _, channelID := range result.ChannelIDs {
		item := CapabilityItem{ChannelID: channelID}
		if outcome, ok := discoveryByChannel[channelID]; ok {
			item.DiscoveryStatus = outcome.Status
			item.DiscoveryCategory = outcome.Category
			if outcome.Status == "success" {
				result.RelayReadyCount++
			}
		}
		channel, err := s.db.Channel.GetByID(channelID)
		if err != nil || channel == nil || channel.SiteID == nil {
			result.Items = append(result.Items, item)
			continue
		}
		creds, err := s.db.Credential.ListBySite(*channel.SiteID)
		if err != nil {
			result.Items = append(result.Items, item)
			continue
		}
		hasAPIKey := false
		checkinCapable := false
		boundKind := ""
		for _, cred := range creds {
			kind := strings.ToLower(strings.TrimSpace(cred.Kind))
			if channel.CredentialID != nil && cred.ID == *channel.CredentialID {
				boundKind = kind
			}
			if cred.Status != domain.StatusEnabled || len(cred.SecretEnc) == 0 {
				continue
			}
			if kind == "api_key" {
				hasAPIKey = true
			}
			if kind == "access_token" || kind == "session" {
				checkinCapable = true
			}
		}
		item.CredentialKind = boundKind
		item.HasAPIKey = hasAPIKey
		item.CheckinCapable = checkinCapable
		if checkinCapable {
			result.CheckinCapableCount++
		}
		if !hasAPIKey {
			result.MissingAPIKeyCount++
		}
		result.Items = append(result.Items, item)
	}
}

func (s *Service) matchLegacy(baseURL string, apiKey []byte, candidates []store.ExchangeLegacyCandidate) (int64, int64, error) {
	type match struct{ channelID, credentialID int64 }
	var matches []match
	for _, candidate := range candidates {
		candidateURL := candidate.ChannelBaseURL
		if candidateURL == "" {
			candidateURL = candidate.SiteBaseURL
		}
		normalized, err := NormalizeBaseURL(candidateURL)
		if err != nil || normalized != baseURL {
			continue
		}
		plaintext, err := s.enc.Decrypt(candidate.SecretEnc)
		if err != nil {
			continue
		}
		equal := subtle.ConstantTimeCompare(plaintext, apiKey) == 1
		clearBytes(plaintext)
		if equal {
			// Only adopt a dedicated legacy credential (exactly one channel).
			// Shared credentials are left alone; import creates a new dedicated asset.
			if candidate.CredentialUses != 1 {
				continue
			}
			matches = append(matches, match{candidate.ChannelID, candidate.CredentialID})
		}
	}
	if len(matches) == 1 {
		return matches[0].channelID, matches[0].credentialID, nil
	}
	// Zero or multiple ambiguous matches: create a new dedicated asset.
	return 0, 0, nil
}

func validateChannelIDs(ids []int64) error {
	seen := make(map[int64]struct{}, len(ids))
	for _, id := range ids {
		if id <= 0 {
			return formatError(ErrorValidation)
		}
		if _, exists := seen[id]; exists {
			return formatError(ErrorValidation)
		}
		seen[id] = struct{}{}
	}
	return nil
}

func discoveryCategory(err error) string {
	var typed *discovery.Error
	if errors.As(err, &typed) {
		return typed.Category
	}
	return "refresh_failed"
}

func clearBytes(value []byte) {
	for i := range value {
		value[i] = 0
	}
}
