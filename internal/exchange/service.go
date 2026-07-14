package exchange

import (
	"context"
	"crypto/subtle"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/lan/meta-gateway/internal/crypto"
	"github.com/lan/meta-gateway/internal/discovery"
	"github.com/lan/meta-gateway/internal/store"
)

type DiscoveryRefresher interface {
	Refresh(context.Context, int64) (*discovery.RefreshResult, error)
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

type ImportResult struct {
	CreatedCount    int                `json:"created_count"`
	UpdatedCount    int                `json:"updated_count"`
	AdoptedCount    int                `json:"adopted_count"`
	ChannelIDs      []int64            `json:"channel_ids"`
	Discovery       []DiscoveryOutcome `json:"discovery"`
	DiscoveryOK     int                `json:"discovery_success_count"`
	DiscoveryFailed int                `json:"discovery_failure_count"`
}

type Service struct {
	store     *store.ExchangeStore
	enc       *crypto.Encrypter
	discovery DiscoveryRefresher
	now       func() time.Time
}

func NewService(db *store.DB, enc *crypto.Encrypter, refresher DiscoveryRefresher) *Service {
	return &Service{store: db.Exchange, enc: enc, discovery: refresher, now: time.Now}
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

func (s *Service) Import(ctx context.Context, data []byte) (*ImportResult, error) {
	items, err := Parse(data)
	if err != nil {
		return nil, err
	}
	candidates, err := s.store.LegacyCandidates(ctx)
	if err != nil {
		return nil, formatError(ErrorInternal)
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
		adoptChannelID, adoptCredentialID, matchErr := s.matchLegacy(item.BaseURL, apiKey, candidates)
		clearBytes(apiKey)
		if matchErr != nil {
			return nil, matchErr
		}
		storeItems = append(storeItems, store.ExchangeImportItem{
			Name: item.Name, BaseURL: item.BaseURL, ModelsCSV: strings.Join(item.Models, ","),
			GroupName: item.Group, Priority: item.Priority, Weight: item.Weight,
			Status: item.Status, TypeHint: item.SiteTypeHint, SecretEnc: secretEnc,
			Fingerprint: fingerprint, AdoptChannelID: adoptChannelID,
			AdoptCredentialID: adoptCredentialID,
		})
	}
	persisted, err := s.store.Import(ctx, storeItems)
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
	result.Discovery = make([]DiscoveryOutcome, 0, len(result.ChannelIDs))
	for _, channelID := range result.ChannelIDs {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		_, refreshErr := s.discovery.Refresh(ctx, channelID)
		outcome := DiscoveryOutcome{ChannelID: channelID, Status: "success"}
		if refreshErr != nil {
			outcome.Status = "failed"
			outcome.Category = discoveryCategory(refreshErr)
			result.DiscoveryFailed++
		} else {
			result.DiscoveryOK++
		}
		result.Discovery = append(result.Discovery, outcome)
	}
	return result, nil
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
			if candidate.CredentialUses != 1 {
				return 0, 0, formatError(ErrorConflict)
			}
			matches = append(matches, match{candidate.ChannelID, candidate.CredentialID})
		}
	}
	if len(matches) > 1 {
		return 0, 0, formatError(ErrorConflict)
	}
	if len(matches) == 1 {
		return matches[0].channelID, matches[0].credentialID, nil
	}
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
