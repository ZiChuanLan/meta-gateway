// Package discovery orchestrates authenticated upstream model refreshes.
package discovery

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/lan/meta-gateway/internal/adapters"
	"github.com/lan/meta-gateway/internal/crypto"
	"github.com/lan/meta-gateway/internal/domain"
	"github.com/lan/meta-gateway/internal/store"
)

type ErrorKind string

const (
	ErrorNotFound    ErrorKind = "not_found"
	ErrorUnavailable ErrorKind = "unavailable"
	ErrorUpstream    ErrorKind = "upstream"
	ErrorInternal    ErrorKind = "internal"
)

type Error struct {
	Kind     ErrorKind
	Category string
}

func (e *Error) Error() string { return fmt.Sprintf("discovery failed: %s", e.Category) }

type RefreshResult struct {
	ChannelID       int64    `json:"channel_id"`
	Adapter         string   `json:"adapter"`
	Models          []string `json:"models"`
	CreatedRoutes   int      `json:"created_routes"`
	CreatedMembers  int      `json:"created_members"`
	EnabledMembers  int      `json:"enabled_members"`
	DisabledMembers int      `json:"disabled_members"`
}

type RefreshItem struct {
	ChannelID int64          `json:"channel_id"`
	Result    *RefreshResult `json:"result,omitempty"`
	Error     string         `json:"error,omitempty"`
}

type RefreshSummary struct {
	Items        []RefreshItem `json:"items"`
	SuccessCount int           `json:"success_count"`
	FailureCount int           `json:"failure_count"`
}

type Service struct {
	db       *store.DB
	enc      *crypto.Encrypter
	registry *adapters.Registry
	now      func() time.Time
}

func New(db *store.DB, enc *crypto.Encrypter, registry *adapters.Registry) *Service {
	return &Service{db: db, enc: enc, registry: registry, now: time.Now}
}

func (s *Service) Refresh(ctx context.Context, channelID int64) (*RefreshResult, error) {
	channel, err := s.db.Channel.GetByID(channelID)
	if err != nil {
		return nil, internalError("channel_lookup")
	}
	if channel == nil {
		return nil, &Error{Kind: ErrorNotFound, Category: "channel_not_found"}
	}
	if channel.Status != domain.StatusEnabled {
		return nil, unavailableError("channel_disabled")
	}
	if channel.SiteID == nil {
		return nil, unavailableError("site_unavailable")
	}
	site, err := s.db.Site.GetByID(*channel.SiteID)
	if err != nil {
		return nil, internalError("site_lookup")
	}
	if site == nil || site.Status != domain.StatusEnabled {
		return nil, unavailableError("site_unavailable")
	}
	adapter, ok := s.registry.Resolve(channel.TypeHint, site.Platform)
	if !ok {
		return nil, unavailableError("unsupported_adapter")
	}
	if channel.CredentialID == nil {
		return nil, unavailableError("credential_unavailable")
	}
	credential, err := s.db.Credential.GetByID(*channel.CredentialID)
	if err != nil {
		return nil, internalError("credential_lookup")
	}
	if credential == nil || credential.Status != domain.StatusEnabled || len(credential.SecretEnc) == 0 || credential.SiteID != site.ID {
		return nil, unavailableError("credential_unavailable")
	}
	plaintext, err := s.enc.Decrypt(string(credential.SecretEnc))
	if err != nil {
		return nil, unavailableError("credential_unavailable")
	}
	baseURL := channel.BaseURL
	if baseURL == "" {
		baseURL = site.BaseURL
	}
	started := s.now()
	models, err := adapter.ListModels(ctx, baseURL, string(plaintext))
	for i := range plaintext {
		plaintext[i] = 0
	}
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, err
		}
		category := "upstream_failure"
		var adapterErr *adapters.Error
		if errors.As(err, &adapterErr) {
			if adapterErr.Kind == adapters.ErrorInvalidURL {
				return nil, unavailableError("invalid_base_url")
			}
			category = string(adapterErr.Kind)
		}
		return nil, &Error{Kind: ErrorUpstream, Category: category}
	}
	checkedAt := s.now()
	latency := int(checkedAt.Sub(started).Milliseconds())
	reconciled, err := s.db.DiscoveredModel.Reconcile(ctx, store.ReconcileInput{
		ChannelID: channel.ID,
		Models:    models,
		Source:    adapter.Name(),
		LatencyMs: latency,
		CheckedAt: checkedAt,
	})
	if err != nil {
		return nil, internalError("persistence_failure")
	}
	return &RefreshResult{
		ChannelID: channel.ID, Adapter: adapter.Name(), Models: models,
		CreatedRoutes: reconciled.CreatedRoutes, CreatedMembers: reconciled.CreatedMembers,
		EnabledMembers: reconciled.EnabledMembers, DisabledMembers: reconciled.DisabledMembers,
	}, nil
}

func (s *Service) RefreshAll(ctx context.Context) (*RefreshSummary, error) {
	channels, err := s.db.Channel.ListEnabled()
	if err != nil {
		return nil, internalError("channel_list")
	}
	sort.Slice(channels, func(i, j int) bool { return channels[i].ID < channels[j].ID })
	summary := &RefreshSummary{Items: make([]RefreshItem, 0, len(channels))}
	for _, channel := range channels {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		result, refreshErr := s.Refresh(ctx, channel.ID)
		item := RefreshItem{ChannelID: channel.ID, Result: result}
		if refreshErr != nil {
			var discoveryErr *Error
			if errors.As(refreshErr, &discoveryErr) {
				item.Error = discoveryErr.Category
			} else {
				item.Error = "refresh_failed"
			}
			summary.FailureCount++
		} else {
			summary.SuccessCount++
		}
		summary.Items = append(summary.Items, item)
	}
	return summary, nil
}

func unavailableError(category string) error {
	return &Error{Kind: ErrorUnavailable, Category: category}
}

func internalError(category string) error {
	return &Error{Kind: ErrorInternal, Category: category}
}
