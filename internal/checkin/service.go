// Package checkin orchestrates credential-scoped upstream check-in runs.
package checkin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/lan/meta-gateway/internal/adapters"
	"github.com/lan/meta-gateway/internal/crypto"
	"github.com/lan/meta-gateway/internal/domain"
	"github.com/lan/meta-gateway/internal/store"
	"github.com/lan/meta-gateway/internal/webhook"
)

const (
	SourceManual    = "manual"
	SourceScheduled = "scheduled"
	// SourceRefresh marks an on-demand session refresh triggered by an
	// upstream 401 during relaying (not a user action, not the daily job).
	SourceRefresh = "refresh"

	StatusSuccess = "success"
	StatusFailed  = "failed"
	StatusSkipped = "skipped"
)

type ErrorKind string

const (
	ErrorNotFound ErrorKind = "not_found"
	ErrorInternal ErrorKind = "internal"
)

type Error struct {
	Kind     ErrorKind
	Category string
}

func (e *Error) Error() string { return fmt.Sprintf("check-in failed: %s", e.Category) }

type RunResult struct {
	SiteID       int64     `json:"site_id"`
	CredentialID int64     `json:"credential_id"`
	Source       string    `json:"source"`
	Status       string    `json:"status"`
	Category     string    `json:"category"`
	Message      string    `json:"message"`
	Reward       string    `json:"reward,omitempty"`
	LatencyMs    int       `json:"latency_ms"`
	RanAt        time.Time `json:"ran_at"`
}

type RunSummary struct {
	Items        []RunResult `json:"items"`
	SuccessCount int         `json:"success_count"`
	FailureCount int         `json:"failure_count"`
	SkippedCount int         `json:"skipped_count"`
}

type Service struct {
	db       *store.DB
	enc      *crypto.Encrypter
	registry *adapters.Registry
	now      func() time.Time
	// notifier delivers checkin-failed alerts.
	notifier *webhook.Notifier

	runningMu sync.Mutex
	running   map[int64]struct{}
}

func New(db *store.DB, enc *crypto.Encrypter, registry *adapters.Registry) *Service {
	return &Service{
		db:       db,
		enc:      enc,
		registry: registry,
		now:      time.Now,
		running:  make(map[int64]struct{}),
	}
}

// SetNotifier attaches the alert notifier (checkin-failed events).
func (s *Service) SetNotifier(n *webhook.Notifier) {
	s.notifier = n
}

func (s *Service) RunCredential(ctx context.Context, credentialID int64, source string, requireScheduleEnabled bool) (RunResult, error) {
	if source != SourceManual && source != SourceScheduled && source != SourceRefresh {
		return RunResult{}, internalError("invalid_source")
	}
	started := s.now()
	credential, err := s.db.Credential.GetByID(credentialID)
	if err != nil {
		return RunResult{}, internalError("credential_lookup")
	}
	if credential == nil {
		return RunResult{}, &Error{Kind: ErrorNotFound, Category: "credential_not_found"}
	}
	site, err := s.db.Site.GetByID(credential.SiteID)
	if err != nil {
		return RunResult{}, internalError("site_lookup")
	}
	if site == nil {
		return RunResult{}, internalError("site_lookup")
	}

	if requireScheduleEnabled && !credential.CheckinEnabled {
		return s.persist(started, site.ID, credential.ID, source, StatusSkipped, "checkin_disabled", "scheduled check-in is disabled", "")
	}
	if site.Status != domain.StatusEnabled {
		return s.persist(started, site.ID, credential.ID, source, StatusSkipped, "site_disabled", "site is disabled", "")
	}
	if credential.Status != domain.StatusEnabled {
		return s.persist(started, site.ID, credential.ID, source, StatusSkipped, "credential_disabled", "credential is disabled", "")
	}
	kind := strings.ToLower(strings.TrimSpace(credential.Kind))
	if kind != "session" && kind != "access_token" {
		return s.persist(started, site.ID, credential.ID, source, StatusSkipped, "unsupported_credential_kind", "credential kind does not support check-in", "")
	}
	// Site-family gate (All API Hub profile table): known families such as
	// sub2api / aihubmix / sharedchat do not expose the New-API check-in API.
	// Unknown/custom platforms may opt into the same scheduler by registering
	// an adapter under their canonical platform name.
	profile := adapters.SiteProfileFor("", site.Platform)
	if !profile.Checkin {
		return s.persist(started, site.ID, credential.ID, source, StatusSkipped, "checkin_not_supported", "this site family does not support check-in", "")
	}
	adapter, ok := s.registry.ResolveCheckin(site.Platform)
	if !ok {
		return s.persist(started, site.ID, credential.ID, source, StatusSkipped, "unsupported", "check-in is not supported", "")
	}
	platformUserID, err := platformUserID(credential.MetaJSON)
	if err != nil {
		return s.persist(started, site.ID, credential.ID, source, StatusFailed, "invalid_metadata", "credential metadata is invalid", "")
	}
	if !s.acquire(credential.ID) {
		return s.persist(started, site.ID, credential.ID, source, StatusSkipped, "already_running", "check-in is already running", "")
	}
	defer s.release(credential.ID)

	plaintext, err := s.enc.Decrypt(string(credential.SecretEnc))
	if err != nil {
		zero(plaintext)
		plaintext = nil
	}
	cookiePlain, cookieErr := s.enc.Decrypt(string(credential.CookieEnc))
	if cookieErr != nil {
		zero(cookiePlain)
		cookiePlain = nil
	}
	if len(plaintext) == 0 && len(cookiePlain) == 0 {
		return s.persist(started, site.ID, credential.ID, source, StatusFailed, "credential_decrypt_failed", "credential secrets cannot be decrypted (re-enter the token or cookie after MASTER_KEY changes)", "")
	}
	// New-API family check-in usually needs a numeric user id header. Resolve via
	// /api/user/self when meta is missing so operators are not blocked after import.
	if platformUserID <= 0 && adapter.RequiresPlatformUserID() {
		resolvedID, resolveErr := s.resolvePlatformUserID(ctx, site, string(plaintext), string(cookiePlain), credential.AuthMode)
		if resolveErr != nil {
			zero(plaintext)
			zero(cookiePlain)
			status, category, message := normalizeAdapterError(resolveErr)
			if category == "" || category == "invalid_payload" {
				category = "user_id_unavailable"
				message = "could not resolve platform user id"
				status = StatusFailed
			}
			result, persistErr := s.persist(started, site.ID, credential.ID, source, status, category, message, "")
			if persistErr != nil {
				return RunResult{}, persistErr
			}
			return result, nil
		}
		if resolvedID <= 0 {
			zero(plaintext)
			zero(cookiePlain)
			return s.persist(started, site.ID, credential.ID, source, StatusFailed, "user_id_unavailable", "platform user id is required for check-in", "")
		}
		platformUserID = resolvedID
		_ = s.persistPlatformUserID(credential, platformUserID)
	}
	adapterResult, adapterErr := adapter.Checkin(ctx, adapters.CheckinInput{
		BaseURL:        site.BaseURL,
		Secret:         string(plaintext),
		Cookie:         string(cookiePlain),
		AuthMode:       checkinAuthMode(credential.AuthMode, len(plaintext) > 0, len(cookiePlain) > 0),
		PlatformUserID: platformUserID,
		CheckinPath:    checkinPathOverride(credential.MetaJSON),
		CheckinMethod:  checkinMethodOverride(credential.MetaJSON),
	})
	zero(plaintext)
	zero(cookiePlain)
	if adapterErr != nil {
		status, category, message := normalizeAdapterError(adapterErr)
		result, persistErr := s.persist(started, site.ID, credential.ID, source, status, category, message, "")
		if persistErr != nil {
			return RunResult{}, persistErr
		}
		// Checkin-failed alert (cooldown-protected; scheduled source only to
		// avoid manual-run noise).
		if status == StatusFailed && source == SourceScheduled && s.notifier != nil {
			s.notifier.SendAlert(ctx, webhook.AlertWarning, "签到失败", fmt.Sprintf("站点 %s 的每日签到失败：%s (%s)", site.Name, message, category))
		}
		if errors.Is(adapterErr, context.Canceled) || errors.Is(adapterErr, context.DeadlineExceeded) {
			return result, adapterErr
		}
		return result, nil
	}
	status := StatusSuccess
	if adapterResult.Outcome == adapters.CheckinSkipped {
		status = StatusSkipped
	}
	return s.persist(started, site.ID, credential.ID, source, status, adapterResult.Category, adapterResult.Message, adapterResult.Reward)
}

// RefreshForRelay re-establishes a session/access-token credential for the
// proxy's 401 refresh-retry path. It runs the same machinery as a manual
// check-in but with source=refresh (no schedule requirement, no alerting).
// ok = StatusSuccess.
func (s *Service) RefreshForRelay(ctx context.Context, credentialID int64) (bool, error) {
	res, err := s.RunCredential(ctx, credentialID, SourceRefresh, false)
	if err != nil {
		return false, err
	}
	return res.Status == StatusSuccess, nil
}

// LastScheduledRunAt reports the most recent scheduled check-in run from
// persisted logs. The scheduler uses it to seed per-day tracking so a restart
// after the daily tick triggers exactly one catch-up run.
func (s *Service) LastScheduledRunAt(ctx context.Context) (time.Time, error) {
	return s.db.CheckinLog.LastScheduledRunAt()
}

func (s *Service) RunAll(ctx context.Context, source string) (*RunSummary, error) {
	credentials, err := s.db.Credential.ListCheckinEnabled()
	if err != nil {
		return nil, internalError("credential_list")
	}
	summary, err := runAll(ctx, credentials, source, s.RunCredential)
	if err != nil {
		// Interrupted (restart/stop): no durable batch-completed marker, so the
		// next start catches up the remaining credentials instead of assuming
		// today was fully handled.
		return nil, err
	}
	// The whole batch finished: persist a durable marker so a restart does not
	// re-run today's check-in (this is the authoritative signal, unlike the
	// per-credential logs that an interrupted batch already wrote). Manual
	// batches (the "check in now" button) must NOT write this marker: they are
	// not the daily tick, and writing one would suppress the scheduler's
	// catch-up after a restart.
	if source == SourceScheduled {
		if recordErr := s.db.CheckinLog.RecordBatchCompleted(s.now()); recordErr != nil {
			return nil, internalError("batch_state")
		}
	}
	return summary, nil
}

// runAll executes one credential at a time and never lets a single internal
// failure abort the batch. runOne returns the persisted result, or a non-ctx
// error when the attempt could not be recorded (e.g. a transient DB failure);
// such errors become a synthetic failed item and the loop continues.
func runAll(ctx context.Context, credentials []domain.Credential, source string, runOne func(context.Context, int64, string, bool) (RunResult, error)) (*RunSummary, error) {
	summary := &RunSummary{Items: make([]RunResult, 0, len(credentials))}
	for _, credential := range credentials {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		result, runErr := runOne(ctx, credential.ID, source, true)
		if runErr != nil {
			if errors.Is(runErr, context.Canceled) || errors.Is(runErr, context.DeadlineExceeded) {
				return nil, runErr
			}
			// Infra failure (e.g. transient DB error) with no persisted result:
			// record a synthetic failure and continue so one credential cannot
			// starve the rest of the daily batch.
			summary.Items = append(summary.Items, RunResult{
				SiteID:       credential.SiteID,
				CredentialID: credential.ID,
				Source:       source,
				Status:       StatusFailed,
				Category:     "internal",
				Message:      "check-in aborted before upstream call (internal error)",
				RanAt:        time.Now(),
			})
			summary.FailureCount++
			continue
		}
		summary.Items = append(summary.Items, result)
		switch result.Status {
		case StatusSuccess:
			summary.SuccessCount++
		case StatusFailed:
			summary.FailureCount++
		case StatusSkipped:
			summary.SkippedCount++
		}
	}
	return summary, nil
}

func (s *Service) persist(started time.Time, siteID, credentialID int64, source, status, category, message, reward string) (RunResult, error) {
	ranAt := s.now()
	latency := int(ranAt.Sub(started).Milliseconds())
	if latency < 0 {
		latency = 0
	}
	result := RunResult{
		SiteID: siteID, CredentialID: credentialID, Source: source,
		Status: status, Category: category, Message: message, Reward: reward,
		LatencyMs: latency, RanAt: ranAt,
	}
	logEntry := domain.CheckinLog{
		SiteID: result.SiteID, CredentialID: result.CredentialID, Source: result.Source,
		Status: result.Status, Category: result.Category, Message: result.Message,
		Reward: result.Reward, LatencyMs: result.LatencyMs, RanAt: result.RanAt,
	}
	if err := s.db.CheckinLog.Create(&logEntry); err != nil {
		return RunResult{}, internalError("log_persistence")
	}
	return result, nil
}

func (s *Service) acquire(credentialID int64) bool {
	s.runningMu.Lock()
	defer s.runningMu.Unlock()
	if _, exists := s.running[credentialID]; exists {
		return false
	}
	s.running[credentialID] = struct{}{}
	return true
}

func (s *Service) release(credentialID int64) {
	s.runningMu.Lock()
	delete(s.running, credentialID)
	s.runningMu.Unlock()
}

func (s *Service) resolvePlatformUserID(ctx context.Context, site *domain.Site, secret, cookie, mode string) (int64, error) {
	accountAdapter, ok := s.registry.ResolveAccount(site.Platform)
	if !ok {
		return 0, nil
	}
	self, err := accountAdapter.ProbeSelf(ctx, adapters.AccountInput{
		BaseURL:  site.BaseURL,
		Secret:   secret,
		Cookie:   cookie,
		AuthMode: checkinAuthMode(mode, secret != "", cookie != ""),
	})
	if err != nil {
		return 0, err
	}
	return self.PlatformUserID, nil
}

func (s *Service) persistPlatformUserID(credential *domain.Credential, userID int64) error {
	if credential == nil || userID <= 0 {
		return nil
	}
	current, err := platformUserID(credential.MetaJSON)
	if err != nil {
		return err
	}
	if current == userID {
		return nil
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
	if err := ensureJSONEOF(decoder); err != nil {
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

func ensureJSONEOF(decoder *json.Decoder) error {
	var extra any
	err := decoder.Decode(&extra)
	if errors.Is(err, io.EOF) {
		return nil
	}
	if err == nil {
		return errors.New("multiple JSON values")
	}
	return err
}

func normalizeAdapterError(err error) (status, category, message string) {
	if errors.Is(err, context.Canceled) {
		return StatusFailed, "canceled", "check-in was canceled"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return StatusFailed, "deadline_exceeded", "check-in deadline exceeded"
	}
	var checkinErr *adapters.CheckinError
	if errors.As(err, &checkinErr) {
		category := string(checkinErr.Kind)
		if category == string(adapters.ErrorPayload) {
			category = "invalid_payload"
		}
		if category == string(adapters.ErrorStatus) {
			category = "upstream_status"
		}
		message := strings.TrimSpace(checkinErr.Message)
		if message == "" {
			switch category {
			case "invalid_payload":
				message = "upstream response was not a valid check-in result (not JSON or missing success)"
			case "upstream_status":
				if checkinErr.Status > 0 {
					message = fmt.Sprintf("upstream returned HTTP %d", checkinErr.Status)
				} else {
					message = "upstream returned an error status"
				}
			case "transport":
				message = "could not reach upstream check-in endpoint (DNS, TLS, or network)"
			case "invalid_base_url", "invalid_url":
				message = "site base URL is invalid for check-in"
			case "response_too_large":
				message = "upstream check-in response was too large"
			default:
				message = "upstream check-in failed"
			}
		} else if category == "upstream_status" && checkinErr.Status > 0 {
			// Adapter message already includes "HTTP N …"; avoid "HTTP N: HTTP N …".
			code := fmt.Sprintf("%d", checkinErr.Status)
			if !strings.Contains(message, code) {
				message = fmt.Sprintf("HTTP %d: %s", checkinErr.Status, message)
			}
		}
		return StatusFailed, category, message
	}
	var modelErr *adapters.Error
	if errors.As(err, &modelErr) {
		// ProbeSelf failures while resolving platform user id.
		if modelErr.Kind == adapters.ErrorStatus && (modelErr.Status == 401 || modelErr.Status == 403) {
			return StatusFailed, "upstream_unauthorized", fmt.Sprintf(
				"account probe HTTP %d while resolving user id — token may be expired",
				modelErr.Status,
			)
		}
		if modelErr.Status > 0 {
			return StatusFailed, string(modelErr.Kind), fmt.Sprintf(
				"could not resolve platform user id (HTTP %d)",
				modelErr.Status,
			)
		}
		return StatusFailed, string(modelErr.Kind), "could not resolve platform user id"
	}
	return StatusFailed, "upstream_failure", "upstream check-in failed (unknown error type)"
}

func checkinAuthMode(mode string, hasSecret, hasCookie bool) adapters.AuthMode {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "cookie":
		if hasCookie {
			return adapters.AuthCookie
		}
	case "auto":
		// auto is advisory at check-in time: the check-in request itself is a
		// state-changing POST (not idempotent), so no automatic cookie retry;
		// pick the strongest present credential, preferring the token.
		if hasSecret {
			return adapters.AuthAccessToken
		}
		if hasCookie {
			return adapters.AuthCookie
		}
	case "access_token":
		if hasSecret {
			return adapters.AuthAccessToken
		}
	}
	if hasSecret {
		return adapters.AuthAccessToken
	}
	return adapters.AuthCookie
}

// checkinPathOverride / checkinMethodOverride read the optional endpoint
// overrides external check-in sites declare in credential meta
// ({"checkin_path": "/api/checkin/spin", "checkin_method": "POST"}). Only
// the external adapter consumes them; New-API adapters ignore the fields.
func checkinPathOverride(metaJSON string) string {
	var meta struct {
		CheckinPath string `json:"checkin_path"`
	}
	if err := json.Unmarshal([]byte(metaJSON), &meta); err != nil {
		return ""
	}
	return strings.TrimSpace(meta.CheckinPath)
}

func checkinMethodOverride(metaJSON string) string {
	var meta struct {
		CheckinMethod string `json:"checkin_method"`
	}
	if err := json.Unmarshal([]byte(metaJSON), &meta); err != nil {
		return ""
	}
	return strings.TrimSpace(meta.CheckinMethod)
}

func zero(value []byte) {
	for i := range value {
		value[i] = 0
	}
}

func internalError(category string) error {
	return &Error{Kind: ErrorInternal, Category: category}
}
