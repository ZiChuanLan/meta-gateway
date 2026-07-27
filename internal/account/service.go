// Package account probes upstream user identity and syncs site API keys for relay.
package account

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
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

func (e *Error) Error() string { return fmt.Sprintf("account failed: %s", e.Category) }

type ProbeResult struct {
	ChannelID      int64     `json:"channel_id"`
	CredentialID   int64     `json:"credential_id"`
	Username       string    `json:"username"`
	DisplayName    string    `json:"display_name,omitempty"`
	PlatformUserID int64     `json:"platform_user_id,omitempty"`
	Quota          *int64    `json:"quota,omitempty"`
	UsedQuota      *int64    `json:"used_quota,omitempty"`
	LatencyMs      int       `json:"latency_ms"`
	CheckedAt      time.Time `json:"checked_at"`
}

type SyncKeysRequest struct {
	// AttachToChannel defaults to true when omitted (nil). Set false to import keys without rebinding the channel.
	AttachToChannel *bool `json:"attach_to_channel"`
	// SplitByGroup defaults to false when omitted (nil). When true, creates one connection per token group.
	// Default is false so multiple keys stay aggregated as a site-level key pool on one connection.
	SplitByGroup *bool `json:"split_by_group"`
	MaxKeys      int   `json:"max_keys"`
}

type SyncKeyItem struct {
	Name         string `json:"name,omitempty"`
	Group        string `json:"group,omitempty"`
	CredentialID int64  `json:"credential_id,omitempty"`
	Enabled      bool   `json:"enabled,omitempty"`
	Status       string `json:"status"`
	Category     string `json:"category,omitempty"`
}

type SyncKeysResult struct {
	ChannelID          int64 `json:"channel_id"`
	Listed             int   `json:"listed"`
	CreatedCredentials int   `json:"created_credentials"`
	ReusedCredentials  int   `json:"reused_credentials"`
	SkippedMasked      int   `json:"skipped_masked"`
	// EmptyList means upstream returned zero tokens (not necessarily expired).
	EmptyList bool `json:"empty_list,omitempty"`
	// Category explains overall outcome for UI (optional).
	Category             string `json:"category,omitempty"`
	Message              string `json:"message,omitempty"`
	AttachedCredentialID int64  `json:"attached_credential_id,omitempty"`
	// Group-split connection outcomes (one channel per New API group).
	CreatedChannels int                `json:"created_channels,omitempty"`
	UpdatedChannels int                `json:"updated_channels,omitempty"`
	GroupChannels   []GroupChannelItem `json:"group_channels,omitempty"`
	Items           []SyncKeyItem      `json:"items"`
}

// GroupChannelItem is one connection ensured for a token group.
type GroupChannelItem struct {
	Group        string `json:"group"`
	ChannelID    int64  `json:"channel_id"`
	CredentialID int64  `json:"credential_id"`
	Name         string `json:"name"`
	Status       string `json:"status"` // created | reused | updated
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

func (s *Service) Probe(ctx context.Context, channelID int64) (*ProbeResult, error) {
	started := s.now()
	resolved, err := s.resolveUserTarget(channelID)
	if err != nil {
		return nil, err
	}
	self, err := resolved.adapter.ProbeSelf(ctx, resolved.input)
	zeroString(&resolved.input.Secret)
	if err != nil {
		return nil, mapAdapterError(err)
	}
	if self.PlatformUserID > 0 {
		_ = s.persistPlatformUserID(resolved.credential, self.PlatformUserID)
	}
	checkedAt := s.now()
	latency := int(checkedAt.Sub(started).Milliseconds())
	if latency < 0 {
		latency = 0
	}
	return &ProbeResult{
		ChannelID:      channelID,
		CredentialID:   resolved.credential.ID,
		Username:       self.Username,
		DisplayName:    self.DisplayName,
		PlatformUserID: self.PlatformUserID,
		Quota:          self.Quota,
		UsedQuota:      self.UsedQuota,
		LatencyMs:      latency,
		CheckedAt:      checkedAt,
	}, nil
}

func (s *Service) SyncKeys(ctx context.Context, channelID int64, request SyncKeysRequest) (*SyncKeysResult, error) {
	if request.MaxKeys <= 0 {
		request.MaxKeys = 20
	}
	if request.MaxKeys > 100 {
		request.MaxKeys = 100
	}
	attachToChannel := true
	if request.AttachToChannel != nil {
		attachToChannel = *request.AttachToChannel
	}

	resolved, err := s.resolveUserTarget(channelID)
	if err != nil {
		return nil, err
	}
	// Many New-API forks require New-Api-User (and siblings) for /api/token.
	// Resolve user id from /api/user/self when meta is empty.
	if resolved.input.PlatformUserID <= 0 {
		if self, selfErr := resolved.adapter.ProbeSelf(ctx, resolved.input); selfErr == nil && self.PlatformUserID > 0 {
			resolved.input.PlatformUserID = self.PlatformUserID
			_ = s.persistPlatformUserID(resolved.credential, self.PlatformUserID)
		}
	}
	keys, err := resolved.adapter.ListAPIKeys(ctx, resolved.input, 0, request.MaxKeys)
	if err != nil {
		zeroString(&resolved.input.Secret)
		return nil, mapAdapterError(err)
	}
	result := &SyncKeysResult{
		ChannelID: channelID,
		Listed:    len(keys),
		Items:     make([]SyncKeyItem, 0, len(keys)),
	}
	if len(keys) == 0 {
		result.EmptyList = true
		result.Category = "empty_token_list"
		result.Message = "upstream returned no API tokens (site may hide keys, use a different list API, or account has none)"
		zeroString(&resolved.input.Secret)
		return result, nil
	}

	existing, err := s.db.Credential.ListBySite(resolved.site.ID)
	if err != nil {
		zeroString(&resolved.input.Secret)
		return nil, internalError("credential_list")
	}

	var attachID int64
	for index, key := range keys {
		if index >= request.MaxKeys {
			break
		}
		name := strings.TrimSpace(key.Name)
		if name == "" {
			name = fmt.Sprintf("token-%d", key.ID)
		}
		secret := strings.TrimSpace(key.Secret)
		if secret == "" && key.ID > 0 {
			revealed, revealErr := resolved.adapter.RevealAPIKey(ctx, resolved.input, key.ID)
			if revealErr == nil {
				secret = strings.TrimSpace(revealed)
			}
		}
		if secret == "" {
			result.SkippedMasked++
			result.Items = append(result.Items, SyncKeyItem{
				Name: name, Group: key.Group, Status: "skipped_masked", Category: "key_masked",
			})
			continue
		}

		matchedID, matchErr := s.findMatchingAPIKey(existing, secret)
		if matchErr != nil {
			zeroString(&secret)
			zeroString(&resolved.input.Secret)
			return nil, matchErr
		}
		if matchedID > 0 {
			result.ReusedCredentials++
			// Best-effort: refresh meta name/group on reuse when empty.
			if matched, getErr := s.db.Credential.GetByID(matchedID); getErr == nil && matched != nil {
				metaPayload := map[string]any{"name": name, "group": key.Group}
				if key.ID > 0 {
					metaPayload["upstream_token_id"] = key.ID
				}
				if metaBytes, metaErr := json.Marshal(metaPayload); metaErr == nil {
					matched.MetaJSON = string(metaBytes)
					_ = s.db.Credential.Update(matched)
				}
			}
			result.Items = append(result.Items, SyncKeyItem{
				Name: name, Group: key.Group, CredentialID: matchedID,
				Enabled: true, Status: "reused", Category: "existing_api_key",
			})
			if attachID == 0 {
				attachID = matchedID
			}
			zeroString(&secret)
			continue
		}

		encSecret, encErr := s.enc.Encrypt([]byte(secret))
		zeroString(&secret)
		if encErr != nil {
			zeroString(&resolved.input.Secret)
			return nil, internalError("encryption_failed")
		}
		metaPayload := map[string]any{
			"name":  name,
			"group": key.Group,
		}
		if key.ID > 0 {
			metaPayload["upstream_token_id"] = key.ID
		}
		metaBytes, _ := json.Marshal(metaPayload)
		// New API token status: 1 = enabled, 2 = disabled. Default enabled when unknown.
		status := domain.StatusEnabled
		if key.Status == 2 {
			status = domain.StatusDisabled
		}
		created := &domain.Credential{
			SiteID:    resolved.site.ID,
			Kind:      "api_key",
			SecretEnc: []byte(encSecret),
			MetaJSON:  string(metaBytes),
			Status:    status,
		}
		newID, createErr := s.db.Credential.Create(created)
		if createErr != nil {
			zeroString(&resolved.input.Secret)
			return nil, internalError("credential_create")
		}
		created.ID = newID
		existing = append(existing, *created)
		result.CreatedCredentials++
		result.Items = append(result.Items, SyncKeyItem{
			Name: name, Group: key.Group, CredentialID: newID,
			Enabled: status == domain.StatusEnabled,
			Status:  "created", Category: "api_key_imported",
		})
		if attachID == 0 {
			attachID = newID
		}
	}
	zeroString(&resolved.input.Secret)

	if attachID == 0 && result.SkippedMasked > 0 && result.CreatedCredentials == 0 && result.ReusedCredentials == 0 {
		result.Category = "keys_masked"
		result.Message = "upstream listed tokens but secrets were masked and could not be revealed; paste an sk- manually or use a site that exposes full keys"
	} else if attachID > 0 {
		result.Category = "keys_attached"
		result.Message = "API key ready for relay"
	}

	if attachToChannel && attachID > 0 {
		channel := resolved.channel
		// Only re-point when current bind is missing or not an api_key.
		shouldAttach := channel.CredentialID == nil
		if channel.CredentialID != nil {
			bound, getErr := s.db.Credential.GetByID(*channel.CredentialID)
			if getErr != nil {
				return nil, internalError("credential_lookup")
			}
			if bound == nil || !strings.EqualFold(bound.Kind, "api_key") {
				shouldAttach = true
			} else {
				attachID = bound.ID
			}
		}
		if shouldAttach {
			channel.CredentialID = &attachID
			if updateErr := s.db.Channel.Update(channel); updateErr != nil {
				return nil, internalError("channel_update")
			}
		}
		result.AttachedCredentialID = attachID
	}

	// Default: aggregate keys on the site (one connection, multi-key pool). Opt-in to split.
	splitByGroup := false
	if request.SplitByGroup != nil {
		splitByGroup = *request.SplitByGroup
	}
	if splitByGroup {
		if splitErr := s.ensureGroupChannels(resolved, result); splitErr != nil {
			return nil, splitErr
		}
		if result.CreatedChannels > 0 || result.UpdatedChannels > 0 {
			result.Category = "keys_split_by_group"
			result.Message = fmt.Sprintf(
				"API keys ready; connections by group: created %d, updated %d",
				result.CreatedChannels, result.UpdatedChannels,
			)
		}
	}
	return result, nil
}

// ensureGroupChannels creates or reuses one channel per New API token group on the site.
// Same model can later hang both group channels as route members (priority/weight).
func (s *Service) ensureGroupChannels(resolved *resolvedTarget, result *SyncKeysResult) error {
	if resolved == nil || result == nil || resolved.site == nil {
		return nil
	}
	// group -> preferred enabled api_key credential id (first seen wins).
	groupToCredential := map[string]int64{}
	for _, item := range result.Items {
		if item.CredentialID <= 0 {
			continue
		}
		if item.Status != "created" && item.Status != "reused" {
			continue
		}
		groupKey := normalizeTokenGroup(item.Group)
		if _, exists := groupToCredential[groupKey]; exists {
			continue
		}
		// Prefer enabled credentials.
		cred, err := s.db.Credential.GetByID(item.CredentialID)
		if err != nil || cred == nil {
			continue
		}
		if cred.Status != domain.StatusEnabled {
			continue
		}
		groupToCredential[groupKey] = item.CredentialID
	}
	if len(groupToCredential) == 0 {
		return nil
	}

	allChannels, err := s.db.Channel.List()
	if err != nil {
		return internalError("channel_list")
	}
	siteChannels := make([]domain.Channel, 0)
	for _, channel := range allChannels {
		if channel.SiteID != nil && *channel.SiteID == resolved.site.ID {
			siteChannels = append(siteChannels, channel)
		}
	}

	siteName := strings.TrimSpace(resolved.site.Name)
	if siteName == "" {
		siteName = "site"
	}
	typeHint := strings.TrimSpace(resolved.channel.TypeHint)
	if typeHint == "" {
		typeHint = strings.TrimSpace(resolved.site.Platform)
	}
	if typeHint == "" {
		typeHint = "openai-compatible"
	}

	result.GroupChannels = make([]GroupChannelItem, 0, len(groupToCredential))
	for groupKey, credentialID := range groupToCredential {
		channelName := siteName + " · " + groupKey
		existing := findChannelForGroup(siteChannels, groupKey, credentialID)
		if existing != nil {
			changed := false
			if existing.CredentialID == nil || *existing.CredentialID != credentialID {
				existing.CredentialID = &credentialID
				changed = true
			}
			if strings.TrimSpace(existing.GroupName) != groupKey {
				existing.GroupName = groupKey
				changed = true
			}
			// Keep a readable name if still generic.
			if strings.TrimSpace(existing.Name) == "" || existing.Name == siteName {
				existing.Name = channelName
				changed = true
			}
			status := "reused"
			if changed {
				if updateErr := s.db.Channel.Update(existing); updateErr != nil {
					return internalError("channel_update")
				}
				status = "updated"
				result.UpdatedChannels++
			}
			result.GroupChannels = append(result.GroupChannels, GroupChannelItem{
				Group: groupKey, ChannelID: existing.ID, CredentialID: credentialID,
				Name: existing.Name, Status: status,
			})
			continue
		}

		siteID := resolved.site.ID
		credID := credentialID
		created := &domain.Channel{
			SiteID:       &siteID,
			CredentialID: &credID,
			Name:         channelName,
			BaseURL:      "", // inherit site base URL
			GroupName:    groupKey,
			Priority:     0,
			Weight:       100,
			Status:       domain.StatusEnabled,
			TypeHint:     typeHint,
		}
		newID, createErr := s.db.Channel.Create(created)
		if createErr != nil {
			return internalError("channel_create")
		}
		created.ID = newID
		siteChannels = append(siteChannels, *created)
		result.CreatedChannels++
		result.GroupChannels = append(result.GroupChannels, GroupChannelItem{
			Group: groupKey, ChannelID: newID, CredentialID: credentialID,
			Name: channelName, Status: "created",
		})
	}
	return nil
}

func normalizeTokenGroup(group string) string {
	trimmed := strings.TrimSpace(group)
	if trimmed == "" {
		return "default"
	}
	return trimmed
}

func findChannelForGroup(channels []domain.Channel, groupKey string, credentialID int64) *domain.Channel {
	// 1) Exact group_name match on this site.
	for index := range channels {
		if normalizeTokenGroup(channels[index].GroupName) == groupKey {
			return &channels[index]
		}
	}
	// 2) Already bound to this credential.
	for index := range channels {
		if channels[index].CredentialID != nil && *channels[index].CredentialID == credentialID {
			return &channels[index]
		}
	}
	// 3) Name suffix " · group" from a previous split.
	suffix := " · " + groupKey
	for index := range channels {
		if strings.HasSuffix(channels[index].Name, suffix) {
			return &channels[index]
		}
	}
	return nil
}

type resolvedTarget struct {
	channel    *domain.Channel
	site       *domain.Site
	credential *domain.Credential
	adapter    adapters.AccountAdapter
	input      adapters.AccountInput
}

func (s *Service) resolveUserTarget(channelID int64) (*resolvedTarget, error) {
	channel, err := s.db.Channel.GetByID(channelID)
	if err != nil {
		return nil, internalError("channel_lookup")
	}
	if channel == nil {
		return nil, &Error{Kind: ErrorNotFound, Category: "channel_not_found"}
	}
	if channel.SiteID == nil {
		return nil, unavailable("site_unavailable")
	}
	site, err := s.db.Site.GetByID(*channel.SiteID)
	if err != nil {
		return nil, internalError("site_lookup")
	}
	if site == nil || site.Status != domain.StatusEnabled {
		return nil, unavailable("site_unavailable")
	}
	credential, err := s.pickUserCredential(site.ID, channel.CredentialID)
	if err != nil {
		return nil, err
	}
	adapter, ok := s.registry.ResolveAccount(firstNonEmpty(channel.TypeHint, site.Platform))
	if !ok {
		adapter, ok = s.registry.ResolveAccount(site.Platform)
	}
	if !ok {
		return nil, unavailable("unsupported_adapter")
	}
	platformUserID, metaErr := platformUserID(credential.MetaJSON)
	if metaErr != nil {
		return nil, unavailable("invalid_metadata")
	}
	plaintext, decErr := s.enc.Decrypt(string(credential.SecretEnc))
	if decErr != nil || len(plaintext) == 0 {
		zero(plaintext)
		return nil, unavailable("credential_unavailable")
	}
	baseURL := channel.BaseURL
	if baseURL == "" {
		baseURL = site.BaseURL
	}
	return &resolvedTarget{
		channel:    channel,
		site:       site,
		credential: credential,
		adapter:    adapter,
		input: adapters.AccountInput{
			BaseURL:        baseURL,
			Secret:         string(plaintext),
			PlatformUserID: platformUserID,
		},
	}, nil
}

func (s *Service) pickUserCredential(siteID int64, preferredID *int64) (*domain.Credential, error) {
	if preferredID != nil {
		cred, err := s.db.Credential.GetByID(*preferredID)
		if err != nil {
			return nil, internalError("credential_lookup")
		}
		if cred != nil && cred.SiteID == siteID && isUserKind(cred.Kind) &&
			cred.Status == domain.StatusEnabled && len(cred.SecretEnc) > 0 {
			return cred, nil
		}
	}
	list, err := s.db.Credential.ListBySite(siteID)
	if err != nil {
		return nil, internalError("credential_list")
	}
	for index := range list {
		cred := list[index]
		if isUserKind(cred.Kind) && cred.Status == domain.StatusEnabled && len(cred.SecretEnc) > 0 {
			return &cred, nil
		}
	}
	return nil, unavailable("user_credential_unavailable")
}

func (s *Service) findMatchingAPIKey(existing []domain.Credential, secret string) (int64, error) {
	secretBytes := []byte(secret)
	for index := range existing {
		cred := existing[index]
		if !strings.EqualFold(cred.Kind, "api_key") || cred.Status != domain.StatusEnabled || len(cred.SecretEnc) == 0 {
			continue
		}
		plaintext, err := s.enc.Decrypt(string(cred.SecretEnc))
		if err != nil {
			continue
		}
		equal := subtle.ConstantTimeCompare(plaintext, secretBytes) == 1
		zero(plaintext)
		if equal {
			return cred.ID, nil
		}
	}
	return 0, nil
}

func (s *Service) persistPlatformUserID(credential *domain.Credential, userID int64) error {
	if userID <= 0 || credential == nil {
		return nil
	}
	current, err := platformUserID(credential.MetaJSON)
	if err != nil || current == userID {
		return err
	}
	meta := map[string]any{}
	if strings.TrimSpace(credential.MetaJSON) != "" {
		if err := json.Unmarshal([]byte(credential.MetaJSON), &meta); err != nil {
			meta = map[string]any{}
		}
	}
	meta["platform_user_id"] = userID
	encoded, err := json.Marshal(meta)
	if err != nil {
		return err
	}
	credential.MetaJSON = string(encoded)
	return s.db.Credential.Update(credential)
}

func mapAdapterError(err error) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	var adapterErr *adapters.Error
	if errors.As(err, &adapterErr) {
		switch adapterErr.Kind {
		case adapters.ErrorInvalidURL:
			return unavailable("invalid_base_url")
		case adapters.ErrorStatus:
			if adapterErr.Status == 401 || adapterErr.Status == 403 {
				return &Error{Kind: ErrorUpstream, Category: "upstream_unauthorized"}
			}
			return &Error{Kind: ErrorUpstream, Category: "upstream_status"}
		case adapters.ErrorTransport:
			return &Error{Kind: ErrorUpstream, Category: "upstream_failure"}
		case adapters.ErrorPayload:
			return &Error{Kind: ErrorUpstream, Category: "invalid_payload"}
		case adapters.ErrorTooLarge:
			return &Error{Kind: ErrorUpstream, Category: "response_too_large"}
		}
	}
	return &Error{Kind: ErrorUpstream, Category: "upstream_failure"}
}

func platformUserID(raw string) (int64, error) {
	if strings.TrimSpace(raw) == "" {
		return 0, nil
	}
	decoder := json.NewDecoder(bytes.NewBufferString(raw))
	decoder.UseNumber()
	var metadata struct {
		PlatformUserID *json.Number `json:"platform_user_id"`
	}
	if err := decoder.Decode(&metadata); err != nil {
		return 0, err
	}
	if metadata.PlatformUserID == nil {
		return 0, nil
	}
	id, err := metadata.PlatformUserID.Int64()
	if err != nil || id <= 0 {
		return 0, errors.New("platform_user_id must be a positive integer")
	}
	return id, nil
}

func isUserKind(kind string) bool {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "access_token", "session":
		return true
	default:
		return false
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func unavailable(category string) error {
	return &Error{Kind: ErrorUnavailable, Category: category}
}

func internalError(category string) error {
	return &Error{Kind: ErrorInternal, Category: category}
}

func zero(value []byte) {
	for i := range value {
		value[i] = 0
	}
}

func zeroString(value *string) {
	if value == nil {
		return
	}
	// Overwrite then clear.
	b := []byte(*value)
	zero(b)
	*value = ""
}
