package webdavsync

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/lan/meta-gateway/internal/crypto"
	"github.com/lan/meta-gateway/internal/exchange"
)

const (
	SourceManual    = "manual"
	SourceScheduled = "scheduled"
	StatusSuccess   = "success"
	StatusFailed    = "failed"
	StatusSkipped   = "skipped"
)

// Config is the effective runtime WebDAV pull settings.
// Source order: Admin DB settings (if any) then process env.
type Config struct {
	Enabled        bool
	URL            string
	Username       string
	Password       string
	BackupPassword string
	CronExpr       string
	MaxBytes       int64
}

// Importer is the exchange import surface used after download/decrypt.
type Importer interface {
	Import(ctx context.Context, data []byte) (*exchange.ImportResult, error)
}

// SyncResult is a redacted outcome for admin API and last-status.
type SyncResult struct {
	Status    string                 `json:"status"`
	Source    string                 `json:"source"`
	FetchedAt time.Time              `json:"fetched_at"`
	TargetURL string                 `json:"target_url,omitempty"`
	Bytes     int                    `json:"bytes,omitempty"`
	Encrypted bool                   `json:"encrypted,omitempty"`
	Category  string                 `json:"category,omitempty"`
	Message   string                 `json:"message,omitempty"`
	Import    *exchange.ImportResult `json:"import,omitempty"`
	LatencyMS int64                  `json:"latency_ms,omitempty"`
}

// StatusView is returned by GET /admin/webdav/status.
type StatusView struct {
	Configured        bool        `json:"configured"`
	SchedulerArmed    bool        `json:"scheduler_armed"`
	TargetURL         string      `json:"target_url,omitempty"`
	Last              *SyncResult `json:"last,omitempty"`
	InProgress        bool        `json:"in_progress"`
	Source            string      `json:"source,omitempty"`
	Enabled           bool        `json:"enabled"`
	URL               string      `json:"url,omitempty"`
	Username          string      `json:"username,omitempty"`
	HasPassword       bool        `json:"has_password"`
	HasBackupPassword bool        `json:"has_backup_password"`
	CronExpr          string      `json:"cron,omitempty"`
}

// Service downloads AAH WebDAV backups and imports them read-only.
type Service struct {
	env      Config
	cfg      Config
	cfgMu    sync.RWMutex
	client   *Client
	importer Importer
	settings SettingsStore
	enc      *crypto.Encrypter
	now      func() time.Time

	runMu     sync.Mutex
	running   bool
	statusMu  sync.RWMutex
	last      *SyncResult
	scheduler bool
}

// NewService wires a read-only WebDAV pull service (env-only, tests).
func NewService(cfg Config, client *Client, importer Importer) *Service {
	return NewServiceWithSettings(cfg, client, importer, nil, nil)
}

// NewServiceWithSettings wires env bootstrap plus optional durable Admin settings.
func NewServiceWithSettings(env Config, client *Client, importer Importer, settings SettingsStore, enc *crypto.Encrypter) *Service {
	if client == nil {
		client = &Client{}
	}
	if env.MaxBytes <= 0 {
		env.MaxBytes = 10 << 20
	}
	if env.CronExpr == "" {
		env.CronExpr = "0 */6 * * *"
	}
	if client.MaxBytes <= 0 {
		client.MaxBytes = env.MaxBytes
	}
	service := &Service{
		env:      env,
		cfg:      env,
		client:   client,
		importer: importer,
		settings: settings,
		enc:      enc,
		now:      time.Now,
	}
	service.reloadRuntimeFromDB()
	return service
}

func (s *Service) SetSchedulerArmed(armed bool) {
	if s == nil {
		return
	}
	s.statusMu.Lock()
	s.scheduler = armed
	s.statusMu.Unlock()
}

func (s *Service) Configured() bool {
	if s == nil {
		return false
	}
	cfg := s.runtimeConfig()
	return configured(cfg)
}

func (s *Service) runtimeConfig() Config {
	if s == nil {
		return Config{}
	}
	s.cfgMu.RLock()
	defer s.cfgMu.RUnlock()
	return s.cfg
}

func (s *Service) Status() StatusView {
	if s == nil {
		return StatusView{}
	}
	viewSettings, _ := s.SettingsView()
	s.statusMu.RLock()
	last := s.last
	s.statusMu.RUnlock()
	view := StatusView{InProgress: s.runningSnapshot()}
	if viewSettings != nil {
		view.Configured = viewSettings.Configured
		view.SchedulerArmed = viewSettings.SchedulerArmed
		view.TargetURL = viewSettings.TargetURL
		view.Source = viewSettings.Source
		view.Enabled = viewSettings.Enabled
		view.URL = viewSettings.URL
		view.Username = viewSettings.Username
		view.HasPassword = viewSettings.HasPassword
		view.HasBackupPassword = viewSettings.HasBackupPassword
		view.CronExpr = viewSettings.CronExpr
	}
	if last != nil {
		copyResult := *last
		view.Last = &copyResult
	}
	return view
}

func (s *Service) runningSnapshot() bool {
	s.runMu.Lock()
	defer s.runMu.Unlock()
	return s.running
}

// TestConnection downloads the backup without importing.
func (s *Service) TestConnection(ctx context.Context) (*SyncResult, error) {
	return s.run(ctx, SourceManual, false)
}

// Sync downloads, optionally decrypts, and imports the AAH backup.
func (s *Service) Sync(ctx context.Context, source string) (*SyncResult, error) {
	if source == "" {
		source = SourceManual
	}
	return s.run(ctx, source, true)
}

// RunScheduled implements the scheduler runner contract.
func (s *Service) RunScheduled(ctx context.Context) (*SyncResult, error) {
	return s.Sync(ctx, SourceScheduled)
}

func (s *Service) run(ctx context.Context, source string, doImport bool) (*SyncResult, error) {
	if s == nil {
		return nil, Error{Category: CategoryInternal, Message: "service unavailable"}
	}
	s.runMu.Lock()
	if s.running {
		s.runMu.Unlock()
		result := &SyncResult{
			Status:    StatusSkipped,
			Source:    source,
			FetchedAt: s.now().UTC(),
			Category:  CategoryBusy,
			Message:   "another webdav sync is already running",
		}
		return result, Error{Category: CategoryBusy, Message: result.Message}
	}
	s.running = true
	s.runMu.Unlock()
	defer func() {
		s.runMu.Lock()
		s.running = false
		s.runMu.Unlock()
	}()

	started := s.now()
	result := &SyncResult{Source: source, FetchedAt: started.UTC(), Status: StatusFailed}

	cfg := s.runtimeConfig()
	if !configured(cfg) {
		result.Category = CategoryConfigIncomplete
		result.Message = "configure WebDAV URL, username, and password in Admin or WEBDAV_* env"
		s.remember(result)
		return result, Error{Category: result.Category, Message: result.Message}
	}

	targetURL, err := ResolveBackupURL(cfg.URL)
	if err != nil {
		var syncErr Error
		if errors.As(err, &syncErr) {
			result.Category = syncErr.Category
			result.Message = syncErr.Message
		} else {
			result.Category = CategoryValidation
			result.Message = "invalid webdav url"
		}
		s.remember(result)
		return result, Error{Category: result.Category, Message: result.Message}
	}
	result.TargetURL = RedactedURL(targetURL)

	body, err := s.client.Download(ctx, targetURL, cfg.Username, cfg.Password)
	result.LatencyMS = time.Since(started).Milliseconds()
	if err != nil {
		var syncErr Error
		if errors.As(err, &syncErr) {
			result.Category = syncErr.Category
			result.Message = syncErr.Message
		} else {
			result.Category = CategoryUpstream
			result.Message = "webdav download failed"
		}
		s.remember(result)
		return result, Error{Category: result.Category, Message: result.Message}
	}
	result.Bytes = len(body)

	plaintext := body
	if envelope, ok := TryParseEncryptedEnvelope(body); ok {
		result.Encrypted = true
		decryptPassword := strings.TrimSpace(cfg.BackupPassword)
		// Many operators only fill the WebDAV login password. When no dedicated
		// backup unlock password is stored, try the login password once.
		if decryptPassword == "" {
			decryptPassword = strings.TrimSpace(cfg.Password)
		}
		decrypted, decryptErr := DecryptEnvelope(envelope, decryptPassword)
		if decryptErr != nil && strings.TrimSpace(cfg.BackupPassword) == "" && strings.TrimSpace(cfg.Password) != "" {
			// Login password was tried as unlock password and failed — ask for the real unlock password.
			decryptErr = Error{
				Category: CategoryDecryptFailed,
				Message:  "backup unlock password required (not the WebDAV login password)",
			}
		}
		if decryptErr != nil {
			var syncErr Error
			if errors.As(decryptErr, &syncErr) {
				result.Category = syncErr.Category
				result.Message = syncErr.Message
				if syncErr.Message == "backup password required" {
					result.Message = "backup unlock password required (not the WebDAV login password)"
				}
			} else {
				result.Category = CategoryDecryptFailed
				result.Message = "decrypt failed"
			}
			s.remember(result)
			return result, Error{Category: result.Category, Message: result.Message}
		}
		plaintext = decrypted
		result.Bytes = len(plaintext)
	}

	if !doImport {
		result.Status = StatusSuccess
		result.Message = "webdav backup reachable"
		s.remember(result)
		return result, nil
	}

	if s.importer == nil {
		result.Category = CategoryInternal
		result.Message = "import service unavailable"
		s.remember(result)
		return result, Error{Category: result.Category, Message: result.Message}
	}
	importResult, importErr := s.importer.Import(ctx, plaintext)
	if importErr != nil {
		result.Category = CategoryImportFailed
		result.Message = "backup import failed"
		var exchangeErr *exchange.Error
		if errors.As(importErr, &exchangeErr) && exchangeErr != nil {
			switch exchangeErr.Kind {
			case exchange.ErrorValidation, exchange.ErrorUnsupported:
				result.Category = CategoryInvalidBackup
				result.Message = "backup is not a supported import document"
			case exchange.ErrorConflict:
				result.Category = CategoryImportFailed
				result.Message = "import identity conflict"
			}
		}
		s.remember(result)
		return result, Error{Category: result.Category, Message: result.Message}
	}
	result.Import = importResult
	result.Status = StatusSuccess
	result.Message = "webdav backup imported"
	s.remember(result)
	return result, nil
}

func (s *Service) remember(result *SyncResult) {
	if s == nil || result == nil {
		return
	}
	s.statusMu.Lock()
	copyResult := *result
	s.last = &copyResult
	s.statusMu.Unlock()
}
