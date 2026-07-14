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
)

const (
	SourceManual    = "manual"
	SourceScheduled = "scheduled"

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

func (s *Service) RunCredential(ctx context.Context, credentialID int64, source string, requireScheduleEnabled bool) (RunResult, error) {
	if source != SourceManual && source != SourceScheduled {
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
	if err != nil || len(plaintext) == 0 {
		zero(plaintext)
		return s.persist(started, site.ID, credential.ID, source, StatusFailed, "credential_unavailable", "credential is unavailable", "")
	}
	adapterResult, adapterErr := adapter.Checkin(ctx, adapters.CheckinInput{
		BaseURL:        site.BaseURL,
		Secret:         string(plaintext),
		PlatformUserID: platformUserID,
	})
	zero(plaintext)
	if adapterErr != nil {
		status, category, message := normalizeAdapterError(adapterErr)
		result, persistErr := s.persist(started, site.ID, credential.ID, source, status, category, message, "")
		if persistErr != nil {
			return RunResult{}, persistErr
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

func (s *Service) RunAll(ctx context.Context, source string) (*RunSummary, error) {
	credentials, err := s.db.Credential.ListCheckinEnabled()
	if err != nil {
		return nil, internalError("credential_list")
	}
	summary := &RunSummary{Items: make([]RunResult, 0, len(credentials))}
	for _, credential := range credentials {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		result, runErr := s.RunCredential(ctx, credential.ID, source, true)
		if runErr != nil {
			if errors.Is(runErr, context.Canceled) || errors.Is(runErr, context.DeadlineExceeded) {
				return nil, runErr
			}
			return nil, runErr
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
	var adapterErr *adapters.CheckinError
	if errors.As(err, &adapterErr) {
		return StatusFailed, string(adapterErr.Kind), "upstream check-in failed"
	}
	return StatusFailed, "upstream_failure", "upstream check-in failed"
}

func zero(value []byte) {
	for i := range value {
		value[i] = 0
	}
}

func internalError(category string) error {
	return &Error{Kind: ErrorInternal, Category: category}
}
