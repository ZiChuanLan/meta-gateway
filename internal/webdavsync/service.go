package webdavsync

import (
	"context"
	"encoding/json"
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

// notConfiguredMessage is reported when a direction has no complete connection
// of its own — the two directions never borrow each other's credentials.
const notConfiguredMessage = "configure this direction's WebDAV URL, username, and password in Admin (or WEBDAV_* env)"

// Sync directions. Download = pull the AAH/exchange backup and import it;
// Upload = push the native Meta Gateway exchange backup to the drive. The two
// directions are fully independent: own connection, own toggle, own schedule,
// own result.
const (
	DirectionDownload = "download"
	DirectionUpload   = "upload"
)

// Config is the effective runtime WebDAV sync settings.
// Source order: Admin DB settings (if any) then process env.
// The import direction (URL/Username/Password/BackupPassword) and the native
// upload direction (Upload*) each hold their own connection, so the two
// directions can live on different servers, accounts, and backup passwords.
type Config struct {
	Enabled       bool
	UploadEnabled bool
	URL           string
	Username      string
	Password      string
	// BackupPassword decrypts an imported backup and encrypts an uploaded one.
	BackupPassword       string
	UploadURL            string
	UploadUsername       string
	UploadPassword       string
	UploadBackupPassword string
	CronExpr             string
	DownloadCron         string
	UploadCron           string
	MaxBytes             int64
}

// Connection is one direction's WebDAV endpoint plus its credentials.
type Connection struct {
	URL            string
	Username       string
	Password       string
	BackupPassword string
}

// importConnection returns the download/import direction connection.
func (cfg Config) importConnection() Connection {
	return Connection{URL: cfg.URL, Username: cfg.Username, Password: cfg.Password, BackupPassword: cfg.BackupPassword}
}

// uploadConnection returns the native backup upload direction connection. It
// never falls back to the import connection: an empty address means the upload
// direction is not configured.
func (cfg Config) uploadConnection() Connection {
	return Connection{
		URL:            cfg.UploadURL,
		Username:       cfg.UploadUsername,
		Password:       cfg.UploadPassword,
		BackupPassword: cfg.UploadBackupPassword,
	}
}

// connectionFor resolves the connection backing a sync direction.
func (cfg Config) connectionFor(direction string) Connection {
	if direction == DirectionUpload {
		return cfg.uploadConnection()
	}
	return cfg.importConnection()
}

// complete reports whether the connection can actually reach a drive: URL,
// username, and login password all present.
func (c Connection) complete() bool {
	return strings.TrimSpace(c.URL) != "" &&
		strings.TrimSpace(c.Username) != "" &&
		strings.TrimSpace(c.Password) != ""
}

// effectiveDownloadCron / effectiveUploadCron resolve the per-direction
// schedule, falling back to the legacy shared CronExpr (env bootstrap).
func (cfg Config) effectiveDownloadCron() string {
	if c := strings.TrimSpace(cfg.DownloadCron); c != "" {
		return c
	}
	if c := strings.TrimSpace(cfg.CronExpr); c != "" {
		return c
	}
	return "0 */6 * * *"
}

func (cfg Config) effectiveUploadCron() string {
	if c := strings.TrimSpace(cfg.UploadCron); c != "" {
		return c
	}
	if c := strings.TrimSpace(cfg.CronExpr); c != "" {
		return c
	}
	return "0 */6 * * *"
}

// Importer is the exchange import surface used after download/decrypt.
type Importer interface {
	ImportWithOptions(ctx context.Context, data []byte, opts exchange.ImportOptions) (*exchange.ImportResult, error)
}

// Exporter builds the native Meta Gateway exchange backup for upload.
type Exporter interface {
	Export(ctx context.Context, request exchange.ExportRequest) (*exchange.Envelope, error)
}

// Sync modes for manual / scheduled WebDAV import.
const (
	SyncModeIncremental = "incremental"
	SyncModeReplace     = "replace"
)

// UploadResult is the redacted outcome of the native backup upload phase.
type UploadResult struct {
	Status    string `json:"status"`
	TargetURL string `json:"target_url,omitempty"`
	Bytes     int    `json:"bytes,omitempty"`
	Encrypted bool   `json:"encrypted,omitempty"`
	Category  string `json:"category,omitempty"`
	Message   string `json:"message,omitempty"`
}

// SyncResult is a redacted outcome for admin API and last-status.
type SyncResult struct {
	Status    string                 `json:"status"`
	Direction string                 `json:"direction,omitempty"`
	Source    string                 `json:"source"`
	FetchedAt time.Time              `json:"fetched_at"`
	TargetURL string                 `json:"target_url,omitempty"`
	Bytes     int                    `json:"bytes,omitempty"`
	Encrypted bool                   `json:"encrypted,omitempty"`
	Category  string                 `json:"category,omitempty"`
	Message   string                 `json:"message,omitempty"`
	Import    *exchange.ImportResult `json:"import,omitempty"`
	Upload    *UploadResult          `json:"upload,omitempty"`
	LatencyMS int64                  `json:"latency_ms,omitempty"`
}

// StatusView is returned by GET /admin/webdav/status.
type StatusView struct {
	Configured              bool        `json:"configured"`
	DownloadConfigured      bool        `json:"download_configured"`
	UploadConfigured        bool        `json:"upload_configured"`
	SchedulerArmed          bool        `json:"scheduler_armed"`
	DownloadSchedulerArmed  bool        `json:"download_scheduler_armed"`
	UploadSchedulerArmed    bool        `json:"upload_scheduler_armed"`
	TargetURL               string      `json:"target_url,omitempty"`
	UploadTargetURL         string      `json:"upload_target_url,omitempty"`
	Last                    *SyncResult `json:"last,omitempty"`
	LastDownload            *SyncResult `json:"last_download,omitempty"`
	LastUpload              *SyncResult `json:"last_upload,omitempty"`
	InProgress              bool        `json:"in_progress"`
	Source                  string      `json:"source,omitempty"`
	Enabled                 bool        `json:"enabled"`
	UploadEnabled           bool        `json:"upload_enabled"`
	URL                     string      `json:"url,omitempty"`
	Username                string      `json:"username,omitempty"`
	HasPassword             bool        `json:"has_password"`
	HasBackupPassword       bool        `json:"has_backup_password"`
	UploadURL               string      `json:"upload_url,omitempty"`
	UploadUsername          string      `json:"upload_username,omitempty"`
	HasUploadPassword       bool        `json:"has_upload_password"`
	HasUploadBackupPassword bool        `json:"has_upload_backup_password"`
	CronExpr                string      `json:"cron,omitempty"`
	DownloadCron            string      `json:"download_cron,omitempty"`
	UploadCron              string      `json:"upload_cron,omitempty"`
}

// Service downloads WebDAV backups, imports them, and uploads the native
// Meta Gateway exchange backup when enabled. The two directions run on
// independent schedules and report independent last-results.
type Service struct {
	env        Config
	cfg        Config
	cfgMu      sync.RWMutex
	client     *Client
	importer   Importer
	exporter   Exporter
	settings   SettingsStore
	enc        *crypto.Encrypter
	now        func() time.Time
	settingsMu sync.Mutex

	runMu             sync.Mutex
	running           bool
	statusMu          sync.RWMutex
	last              *SyncResult
	lastDownload      *SyncResult
	lastUpload        *SyncResult
	schedulerState    bool
	schedulerDownload *Scheduler
	schedulerUpload   *Scheduler
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
	if client.maxBytes() <= 0 {
		client.setMaxBytes(env.MaxBytes)
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

// SetExporter wires the exchange export surface used to build the native
// backup upload (optional; upload stays off until enabled in settings).
func (s *Service) SetExporter(ex Exporter) {
	if s == nil {
		return
	}
	s.exporter = ex
}

// AttachScheduler connects durable settings to the download scheduler only
// (tests / minimal wiring). The scheduler stays alive while disarmed so a
// later Admin update applies without a process restart.
func (s *Service) AttachScheduler(scheduler *Scheduler) error {
	return s.AttachSchedulers(scheduler, nil)
}

// AttachSchedulers connects the per-direction schedulers to durable settings.
func (s *Service) AttachSchedulers(download, upload *Scheduler) error {
	if s == nil {
		return nil
	}
	s.statusMu.Lock()
	s.schedulerDownload = download
	s.schedulerUpload = upload
	s.statusMu.Unlock()
	return s.applySchedulers()
}

func (s *Service) applySchedulers() error {
	s.statusMu.RLock()
	downloadScheduler := s.schedulerDownload
	uploadScheduler := s.schedulerUpload
	s.statusMu.RUnlock()
	if downloadScheduler == nil && uploadScheduler == nil {
		s.statusMu.Lock()
		s.schedulerState = false
		s.statusMu.Unlock()
		return nil
	}
	cfg := s.runtimeConfig()
	if downloadScheduler != nil {
		armed := cfg.Enabled && configuredDirection(cfg, DirectionDownload) && cfg.effectiveDownloadCron() != "off"
		if err := downloadScheduler.SetSchedule(cfg.effectiveDownloadCron(), armed); err != nil {
			s.refreshSchedulerState()
			return err
		}
		if err := downloadScheduler.Start(); err != nil {
			return err
		}
	}
	if uploadScheduler != nil {
		armed := cfg.UploadEnabled && configuredDirection(cfg, DirectionUpload) && cfg.effectiveUploadCron() != "off"
		if err := uploadScheduler.SetSchedule(cfg.effectiveUploadCron(), armed); err != nil {
			s.refreshSchedulerState()
			return err
		}
		if err := uploadScheduler.Start(); err != nil {
			return err
		}
	}
	s.refreshSchedulerState()
	return nil
}

func (s *Service) refreshSchedulerState() {
	s.statusMu.RLock()
	downloadScheduler := s.schedulerDownload
	uploadScheduler := s.schedulerUpload
	s.statusMu.RUnlock()
	armed := (downloadScheduler != nil && downloadScheduler.Armed()) ||
		(uploadScheduler != nil && uploadScheduler.Armed())
	s.statusMu.Lock()
	s.schedulerState = armed
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
	lastDownload := s.lastDownload
	lastUpload := s.lastUpload
	s.statusMu.RUnlock()
	view := StatusView{InProgress: s.runningSnapshot()}
	if viewSettings != nil {
		view.Configured = viewSettings.Configured
		view.DownloadConfigured = viewSettings.DownloadConfigured
		view.UploadConfigured = viewSettings.UploadConfigured
		view.SchedulerArmed = viewSettings.SchedulerArmed
		view.DownloadSchedulerArmed = viewSettings.DownloadSchedulerArmed
		view.UploadSchedulerArmed = viewSettings.UploadSchedulerArmed
		view.TargetURL = viewSettings.TargetURL
		view.UploadTargetURL = viewSettings.UploadTargetURL
		view.Source = viewSettings.Source
		view.Enabled = viewSettings.Enabled
		view.UploadEnabled = viewSettings.UploadEnabled
		view.URL = viewSettings.URL
		view.Username = viewSettings.Username
		view.HasPassword = viewSettings.HasPassword
		view.HasBackupPassword = viewSettings.HasBackupPassword
		view.UploadURL = viewSettings.UploadURL
		view.UploadUsername = viewSettings.UploadUsername
		view.HasUploadPassword = viewSettings.HasUploadPassword
		view.HasUploadBackupPassword = viewSettings.HasUploadBackupPassword
		view.CronExpr = viewSettings.CronExpr
		view.DownloadCron = viewSettings.DownloadCron
		view.UploadCron = viewSettings.UploadCron
	}
	if last != nil {
		copyResult := *last
		view.Last = &copyResult
	}
	if lastDownload != nil {
		copyResult := *lastDownload
		view.LastDownload = &copyResult
	}
	if lastUpload != nil {
		copyResult := *lastUpload
		view.LastUpload = &copyResult
	}
	return view
}

func (s *Service) runningSnapshot() bool {
	s.runMu.Lock()
	defer s.runMu.Unlock()
	return s.running
}

// TestConnection probes a direction's remote target without importing or
// uploading. download → GET the import file; upload → GET the upload target.
func (s *Service) TestConnection(ctx context.Context, direction string) (*SyncResult, error) {
	if direction == "" {
		direction = DirectionDownload
	}
	result := &SyncResult{Source: SourceManual, FetchedAt: s.now().UTC(), Status: StatusFailed, Direction: direction}
	cfg := s.runtimeConfig()
	conn := cfg.connectionFor(direction)
	if !conn.complete() {
		result.Category = CategoryConfigIncomplete
		result.Message = notConfiguredMessage
		s.remember(result)
		return result, Error{Category: result.Category, Message: result.Message}
	}
	if direction == DirectionUpload {
		return s.testUploadTarget(ctx, result, conn)
	}

	targetURL, err := ResolveBackupURL(conn.URL)
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

	started := s.now()
	body, err := s.client.Download(ctx, targetURL, conn.Username, conn.Password)
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
	if envelope, ok := TryParseEncryptedEnvelope(body); ok {
		result.Encrypted = true
		decryptPassword := strings.TrimSpace(conn.BackupPassword)
		if decryptPassword == "" {
			decryptPassword = strings.TrimSpace(conn.Password)
		}
		if _, decryptErr := DecryptEnvelope(envelope, decryptPassword); decryptErr != nil {
			result.Category = CategoryDecryptFailed
			result.Message = "backup unlock password required (not the WebDAV login password)"
			s.remember(result)
			return result, Error{Category: result.Category, Message: result.Message}
		}
	}
	result.Status = StatusSuccess
	result.Message = "webdav backup reachable"
	s.remember(result)
	return result, nil
}

// Sync executes one direction. mode (incremental/replace) applies to download.
func (s *Service) Sync(ctx context.Context, source, mode, direction string) (*SyncResult, error) {
	if source == "" {
		source = SourceManual
	}
	switch direction {
	case "", DirectionDownload:
		return s.runDownload(ctx, source, true, mode)
	case DirectionUpload:
		return s.runUpload(ctx, source)
	default:
		result := &SyncResult{Source: source, FetchedAt: s.now().UTC(), Status: StatusFailed, Direction: direction}
		result.Category = CategoryValidation
		result.Message = "direction must be download or upload"
		s.remember(result)
		return result, Error{Category: result.Category, Message: result.Message}
	}
}

// RunScheduled implements the download scheduler runner contract (always
// incremental). Returns skipped when the import direction is disabled.
func (s *Service) RunScheduled(ctx context.Context) (*SyncResult, error) {
	if !s.runtimeConfig().Enabled {
		result := &SyncResult{
			Status: StatusSkipped, Direction: DirectionDownload, Source: SourceScheduled,
			FetchedAt: s.now().UTC(),
			Message:   "scheduled webdav import disabled",
		}
		s.remember(result)
		return result, nil
	}
	return s.runDownload(ctx, SourceScheduled, true, SyncModeIncremental)
}

// RunScheduledUpload implements the upload scheduler runner contract.
func (s *Service) RunScheduledUpload(ctx context.Context) (*SyncResult, error) {
	if !s.runtimeConfig().UploadEnabled {
		result := &SyncResult{
			Status: StatusSkipped, Direction: DirectionUpload, Source: SourceScheduled,
			FetchedAt: s.now().UTC(),
			Message:   "scheduled webdav upload disabled",
		}
		s.remember(result)
		return result, nil
	}
	return s.runUpload(ctx, SourceScheduled)
}

// DownloadRunner / UploadRunner adapt the service to the Scheduler contract.
func DownloadRunner(svc *Service) ScheduledRunner { return downloadRunner{svc} }
func UploadRunner(svc *Service) ScheduledRunner   { return uploadRunner{svc} }

type downloadRunner struct{ svc *Service }

func (r downloadRunner) RunScheduled(ctx context.Context) (*SyncResult, error) {
	return r.svc.RunScheduled(ctx)
}

type uploadRunner struct{ svc *Service }

func (r uploadRunner) RunScheduled(ctx context.Context) (*SyncResult, error) {
	return r.svc.RunScheduledUpload(ctx)
}

// beginRun serializes directions against one shared run slot and seeds the
// result envelope. A busy slot returns (result, err) with release nil.
func (s *Service) beginRun(source, direction string) (*SyncResult, func(), error) {
	if s == nil {
		return nil, nil, Error{Category: CategoryInternal, Message: "service unavailable"}
	}
	s.runMu.Lock()
	if s.running {
		s.runMu.Unlock()
		result := &SyncResult{
			Status:    StatusSkipped,
			Direction: direction,
			Source:    source,
			FetchedAt: s.now().UTC(),
			Category:  CategoryBusy,
			Message:   "another webdav sync is already running",
		}
		return result, nil, Error{Category: CategoryBusy, Message: result.Message}
	}
	s.running = true
	s.runMu.Unlock()
	result := &SyncResult{Source: source, Direction: direction, FetchedAt: s.now().UTC(), Status: StatusFailed}
	release := func() {
		s.runMu.Lock()
		s.running = false
		s.runMu.Unlock()
	}
	return result, release, nil
}

func (s *Service) runDownload(ctx context.Context, source string, doImport bool, mode string) (*SyncResult, error) {
	result, release, busyErr := s.beginRun(source, DirectionDownload)
	if busyErr != nil {
		return result, busyErr
	}
	defer release()

	started := s.now()
	cfg := s.runtimeConfig()
	conn := cfg.importConnection()
	if !conn.complete() {
		result.Category = CategoryConfigIncomplete
		result.Message = notConfiguredMessage
		s.remember(result)
		return result, Error{Category: result.Category, Message: result.Message}
	}
	if !cfg.Enabled && doImport {
		result.Status = StatusSkipped
		result.Message = "webdav import disabled"
		s.remember(result)
		return result, nil
	}

	importMode := strings.ToLower(strings.TrimSpace(mode))
	if importMode == "" {
		importMode = SyncModeIncremental
	}
	if importMode != SyncModeIncremental && importMode != SyncModeReplace {
		result.Category = CategoryValidation
		result.Message = "sync mode must be incremental or replace"
		s.remember(result)
		return result, Error{Category: result.Category, Message: result.Message}
	}

	targetURL, err := ResolveBackupURL(conn.URL)
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

	body, err := s.client.Download(ctx, targetURL, conn.Username, conn.Password)
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
		decryptPassword := strings.TrimSpace(conn.BackupPassword)
		// Many operators only fill the WebDAV login password. When no dedicated
		// backup unlock password is stored, try the login password once.
		if decryptPassword == "" {
			decryptPassword = strings.TrimSpace(conn.Password)
		}
		decrypted, decryptErr := DecryptEnvelope(envelope, decryptPassword)
		if decryptErr != nil && strings.TrimSpace(conn.BackupPassword) == "" && strings.TrimSpace(conn.Password) != "" {
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
	var importErr error
	var importResult *exchange.ImportResult
	if importMode == SyncModeReplace {
		importResult, importErr = s.importer.ImportWithOptions(ctx, plaintext, exchange.ImportOptions{Mode: exchange.ImportModeReplace})
	} else {
		importResult, importErr = s.importer.ImportWithOptions(ctx, plaintext, exchange.ImportOptions{Mode: exchange.ImportModeIncremental})
	}
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
	if importMode == SyncModeReplace {
		result.Message = "webdav backup imported (full replace)"
	} else {
		result.Message = "webdav backup imported (incremental)"
	}
	s.remember(result)
	return result, nil
}

func (s *Service) runUpload(ctx context.Context, source string) (*SyncResult, error) {
	result, release, busyErr := s.beginRun(source, DirectionUpload)
	if busyErr != nil {
		return result, busyErr
	}
	defer release()

	started := s.now()
	cfg := s.runtimeConfig()
	conn := cfg.uploadConnection()
	if !conn.complete() {
		result.Category = CategoryConfigIncomplete
		result.Message = notConfiguredMessage
		s.remember(result)
		return result, Error{Category: result.Category, Message: result.Message}
	}
	if !cfg.UploadEnabled {
		result.Status = StatusSkipped
		result.Message = "webdav upload disabled"
		s.remember(result)
		return result, nil
	}

	up := s.upload(ctx, conn)
	result.LatencyMS = time.Since(started).Milliseconds()
	result.Status = up.Status
	result.TargetURL = up.TargetURL
	result.Bytes = up.Bytes
	result.Encrypted = up.Encrypted
	result.Category = up.Category
	result.Message = up.Message
	if up.Status == StatusSuccess && result.Message == "" {
		result.Message = "webdav backup uploaded"
	}
	result.Upload = up
	s.remember(result)
	if up.Status == StatusFailed {
		return result, Error{Category: up.Category, Message: up.Message}
	}
	return result, nil
}

// testUploadTarget probes the native backup target without writing anything:
// the address is reachable, or absent, which the first upload creates.
func (s *Service) testUploadTarget(ctx context.Context, result *SyncResult, conn Connection) (*SyncResult, error) {
	uploadURL, _, err := ResolveUploadTarget(conn.URL)
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
	result.TargetURL = RedactedURL(uploadURL)
	if _, dErr := s.client.Download(ctx, uploadURL, conn.Username, conn.Password); dErr != nil {
		var syncErr Error
		if errors.As(dErr, &syncErr) && syncErr.Category == CategoryNotFound {
			result.Status = StatusSuccess
			result.Message = "webdav reachable (backup file will be created on first upload)"
			s.remember(result)
			return result, nil
		}
		if errors.As(dErr, &syncErr) {
			result.Category = syncErr.Category
			result.Message = syncErr.Message
		} else {
			result.Category = CategoryUpstream
			result.Message = "webdav download failed"
		}
		s.remember(result)
		return result, Error{Category: result.Category, Message: result.Message}
	}
	result.Status = StatusSuccess
	result.Message = "webdav reachable"
	s.remember(result)
	return result, nil
}

// upload builds the native Meta Gateway exchange backup and PUTs it to the
// drive addressed by the upload direction's own connection. Failures are
// reported in the UploadResult; the caller decides whether that fails the
// whole sync.
func (s *Service) upload(ctx context.Context, conn Connection) *UploadResult {
	result := &UploadResult{Status: StatusFailed}
	uploadURL, inPlace, err := ResolveUploadTarget(conn.URL)
	if err != nil {
		var syncErr Error
		if errors.As(err, &syncErr) {
			result.Category = syncErr.Category
			result.Message = syncErr.Message
		} else {
			result.Category = CategoryValidation
			result.Message = "invalid webdav url"
		}
		return result
	}
	result.TargetURL = RedactedURL(uploadURL)
	// An explicit .json file address uploads in place: verify the remote
	// document first so an AAH (or unknown) backup is never overwritten.
	if inPlace {
		body, dlErr := s.client.Download(ctx, uploadURL, conn.Username, conn.Password)
		switch {
		case dlErr == nil:
			if !isCanonicalDocument(body) {
				result.Status = StatusSkipped
				result.Message = "remote file is not a Meta Gateway exchange backup; upload skipped"
				return result
			}
		default:
			var syncErr Error
			if errors.As(dlErr, &syncErr) && syncErr.Category == CategoryNotFound {
				// Absent file — safe to create on upload.
			} else {
				result.Status = StatusSkipped
				result.Category = CategoryUpstream
				result.Message = "cannot verify remote file before upload"
				if errors.As(dlErr, &syncErr) {
					result.Category = syncErr.Category
					result.Message = "cannot verify remote file before upload: " + syncErr.Message
				}
				return result
			}
		}
	}
	if s.exporter == nil {
		result.Category = CategoryInternal
		result.Message = "export service unavailable"
		return result
	}
	envelope, err := s.exporter.Export(ctx, exchange.ExportRequest{IncludeSecrets: true})
	if err != nil {
		result.Category = CategoryImportFailed
		result.Message = "backup export failed"
		return result
	}
	// An empty backup would make every later download fail validation, so
	// skip instead of pushing it.
	if envelope == nil || len(envelope.Items) == 0 {
		result.Status = StatusSkipped
		result.Message = "no channels with credentials to back up"
		return result
	}
	data, err := json.Marshal(envelope)
	if err != nil {
		result.Category = CategoryInternal
		result.Message = "backup serialize failed"
		return result
	}
	if password := strings.TrimSpace(conn.BackupPassword); password != "" {
		encrypted, encErr := EncryptEnvelope(data, password, envelopeUploadIterations)
		if encErr != nil {
			var syncErr Error
			result.Category = CategoryInternal
			result.Message = "encrypt backup failed"
			if errors.As(encErr, &syncErr) {
				result.Category = syncErr.Category
				result.Message = syncErr.Message
			}
			return result
		}
		data = encrypted
		result.Encrypted = true
	}
	result.Bytes = len(data)
	if err := s.client.Upload(ctx, uploadURL, conn.Username, conn.Password, data); err != nil {
		var syncErr Error
		if errors.As(err, &syncErr) {
			result.Category = syncErr.Category
			result.Message = syncErr.Message
		} else {
			result.Category = CategoryUpstream
			result.Message = "webdav upload failed"
		}
		return result
	}
	result.Status = StatusSuccess
	return result
}

func isCanonicalDocument(data []byte) bool {
	var probe struct {
		Format string `json:"format"`
	}
	if json.Unmarshal(data, &probe) != nil {
		return false
	}
	return probe.Format == exchange.Format
}

func (s *Service) remember(result *SyncResult) {
	if s == nil || result == nil {
		return
	}
	s.statusMu.Lock()
	copyResult := *result
	s.last = &copyResult
	if result.Direction == DirectionUpload {
		s.lastUpload = &copyResult
	} else {
		s.lastDownload = &copyResult
	}
	s.statusMu.Unlock()
}
