package webdavsync

import (
	"strings"
	"time"

	"github.com/lan/meta-gateway/internal/store"
	cronlib "github.com/robfig/cron/v3"
)

// SettingsView is the redacted Admin settings payload (never includes secrets).
// The import direction (url/username/…) and the native upload direction
// (upload_*) are separate connections and may point at different drives.
type SettingsView struct {
	Enabled                 bool      `json:"enabled"`
	UploadEnabled           bool      `json:"upload_enabled"`
	URL                     string    `json:"url"`
	Username                string    `json:"username"`
	HasPassword             bool      `json:"has_password"`
	HasBackupPassword       bool      `json:"has_backup_password"`
	UploadURL               string    `json:"upload_url"`
	UploadUsername          string    `json:"upload_username"`
	HasUploadPassword       bool      `json:"has_upload_password"`
	HasUploadBackupPassword bool      `json:"has_upload_backup_password"`
	CronExpr                string    `json:"cron"`
	DownloadCron            string    `json:"download_cron"`
	UploadCron              string    `json:"upload_cron"`
	DownloadSchedulerArmed  bool      `json:"download_scheduler_armed"`
	UploadSchedulerArmed    bool      `json:"upload_scheduler_armed"`
	DownloadConfigured      bool      `json:"download_configured"`
	UploadConfigured        bool      `json:"upload_configured"`
	Configured              bool      `json:"configured"`
	SchedulerArmed          bool      `json:"scheduler_armed"`
	Source                  string    `json:"source"` // "database" | "env" | "none"
	UpdatedAt               time.Time `json:"updated_at,omitempty"`
	TargetURL               string    `json:"target_url,omitempty"`
	UploadTargetURL         string    `json:"upload_target_url,omitempty"`
}

// SettingsUpdate is the Admin PUT body. Empty password fields keep existing
// secrets — per direction. download_cron/upload_cron drive the per-direction
// schedules; the legacy cron field seeds both when the specifics are absent.
type SettingsUpdate struct {
	Enabled                   bool   `json:"enabled"`
	UploadEnabled             bool   `json:"upload_enabled"`
	URL                       string `json:"url"`
	Username                  string `json:"username"`
	Password                  string `json:"password"`
	BackupPassword            string `json:"backup_password"`
	UploadURL                 string `json:"upload_url"`
	UploadUsername            string `json:"upload_username"`
	UploadPassword            string `json:"upload_password"`
	UploadBackupPassword      string `json:"upload_backup_password"`
	CronExpr                  string `json:"cron"`
	DownloadCron              string `json:"download_cron"`
	UploadCron                string `json:"upload_cron"`
	ClearPassword             bool   `json:"clear_password"`
	ClearBackupPassword       bool   `json:"clear_backup_password"`
	ClearUploadPassword       bool   `json:"clear_upload_password"`
	ClearUploadBackupPassword bool   `json:"clear_upload_backup_password"`
}

// SettingsStore is the persistence surface for Admin WebDAV settings.
type SettingsStore interface {
	Get() (*store.WebDAVSettings, error)
	Save(*store.WebDAVSettings) error
}

func (s *Service) SettingsView() (*SettingsView, error) {
	if s == nil {
		return &SettingsView{Source: "none", CronExpr: "0 */6 * * *"}, nil
	}
	cfg, source, updatedAt, err := s.resolvedConfig()
	if err != nil {
		return nil, err
	}
	view := &SettingsView{
		Enabled:                 cfg.Enabled,
		UploadEnabled:           cfg.UploadEnabled,
		URL:                     cfg.URL,
		Username:                cfg.Username,
		HasPassword:             cfg.Password != "",
		HasBackupPassword:       cfg.BackupPassword != "",
		UploadURL:               cfg.UploadURL,
		UploadUsername:          cfg.UploadUsername,
		HasUploadPassword:       cfg.UploadPassword != "",
		HasUploadBackupPassword: cfg.UploadBackupPassword != "",
		CronExpr:                cfg.effectiveDownloadCron(),
		DownloadCron:            cfg.effectiveDownloadCron(),
		UploadCron:              cfg.effectiveUploadCron(),
		DownloadConfigured:      configuredDirection(cfg, DirectionDownload),
		UploadConfigured:        configuredDirection(cfg, DirectionUpload),
		Configured:              configured(cfg),
		SchedulerArmed:          s.schedulerArmed(),
		DownloadSchedulerArmed:  s.downloadSchedulerArmed(),
		UploadSchedulerArmed:    s.uploadSchedulerArmed(),
		Source:                  source,
		UpdatedAt:               updatedAt,
	}
	importConn := cfg.importConnection()
	uploadConn := cfg.uploadConnection()
	if target, resolveErr := ResolveBackupURL(importConn.URL); resolveErr == nil {
		view.TargetURL = RedactedURL(target)
	}
	if uploadTarget, _, resolveErr := ResolveUploadTarget(uploadConn.URL); resolveErr == nil {
		view.UploadTargetURL = RedactedURL(uploadTarget)
	}
	return view, nil
}

func (s *Service) UpdateSettings(update SettingsUpdate) (*SettingsView, error) {
	if s == nil || s.settings == nil || s.enc == nil {
		return nil, Error{Category: CategoryInternal, Message: "settings unavailable"}
	}
	s.settingsMu.Lock()
	defer s.settingsMu.Unlock()
	current, err := s.settings.Get()
	if err != nil {
		return nil, Error{Category: CategoryInternal, Message: "load settings failed"}
	}
	if current == nil {
		current = &store.WebDAVSettings{}
	}
	importURL := strings.TrimSpace(update.URL)
	uploadURL := strings.TrimSpace(update.UploadURL)
	// Each direction owns its own address; validate both independently.
	if importURL != "" {
		if _, resolveErr := ResolveBackupURL(importURL); resolveErr != nil {
			return nil, resolveErr
		}
	}
	if uploadURL != "" {
		if _, _, resolveErr := ResolveUploadTarget(uploadURL); resolveErr != nil {
			return nil, resolveErr
		}
	}
	// Per-direction schedules. The legacy shared cron seeds both when the
	// specifics are absent (older clients), then the stored row, then default.
	downloadCron := strings.TrimSpace(update.DownloadCron)
	uploadCron := strings.TrimSpace(update.UploadCron)
	legacyCron := strings.TrimSpace(update.CronExpr)
	if downloadCron == "" {
		downloadCron = legacyCron
	}
	if uploadCron == "" {
		uploadCron = legacyCron
	}
	if downloadCron == "" {
		downloadCron = strings.TrimSpace(current.DownloadCron)
	}
	if uploadCron == "" {
		uploadCron = strings.TrimSpace(current.UploadCron)
	}
	if downloadCron == "" {
		downloadCron = "0 */6 * * *"
	}
	if uploadCron == "" {
		uploadCron = "0 */6 * * *"
	}
	// Validate cron expressions; the literal "off" stores an explicitly
	// disarmed schedule.
	parser := cronlib.NewParser(cronlib.Minute | cronlib.Hour | cronlib.Dom | cronlib.Month | cronlib.Dow)
	for name, expr := range map[string]string{"download_cron": downloadCron, "upload_cron": uploadCron} {
		if expr != "0 */6 * * *" && expr != "off" {
			if _, parseErr := parser.Parse(expr); parseErr != nil {
				return nil, Error{Category: CategoryValidation, Message: "invalid " + name + ": " + parseErr.Error()}
			}
		}
	}

	passwordEnc, pwErr := s.keepSecret(current.PasswordEnc, update.Password, update.ClearPassword)
	if pwErr != nil {
		return nil, pwErr
	}
	backupEnc, bpErr := s.keepSecret(current.BackupPasswordEnc, update.BackupPassword, update.ClearBackupPassword)
	if bpErr != nil {
		return nil, bpErr
	}
	uploadPasswordEnc, upErr := s.keepSecret(current.UploadPasswordEnc, update.UploadPassword, update.ClearUploadPassword)
	if upErr != nil {
		return nil, upErr
	}
	uploadBackupEnc, ubErr := s.keepSecret(current.UploadBackupPasswordEnc, update.UploadBackupPassword, update.ClearUploadBackupPassword)
	if ubErr != nil {
		return nil, ubErr
	}

	next := &store.WebDAVSettings{
		HasOverride:             true,
		Enabled:                 update.Enabled,
		UploadEnabled:           update.UploadEnabled,
		URL:                     importURL,
		Username:                update.Username,
		PasswordEnc:             passwordEnc,
		BackupPasswordEnc:       backupEnc,
		UploadURL:               uploadURL,
		UploadUsername:          update.UploadUsername,
		UploadPasswordEnc:       uploadPasswordEnc,
		UploadBackupPasswordEnc: uploadBackupEnc,
		CronExpr:                downloadCron,
		DownloadCron:            downloadCron,
		UploadCron:              uploadCron,
	}
	if saveErr := s.settings.Save(next); saveErr != nil {
		return nil, Error{Category: CategoryInternal, Message: "save settings failed"}
	}
	// Refresh in-memory runtime config for immediate test/sync.
	s.reloadRuntimeFromDB()
	if err := s.applySchedulers(); err != nil {
		// Keep durable settings, the in-memory config, and the active schedule in
		// one state. Scheduler shutdown/races can still reject an otherwise valid
		// expression after the row was saved, so restore the previous row.
		_ = s.settings.Save(current)
		s.reloadRuntimeFromDB()
		_ = s.applySchedulers()
		return nil, Error{Category: CategoryInternal, Message: "apply scheduler settings failed"}
	}
	return s.SettingsView()
}

// configured reports whether at least one direction has a usable connection.
func configured(cfg Config) bool {
	return configuredDirection(cfg, DirectionDownload) || configuredDirection(cfg, DirectionUpload)
}

// configuredDirection reports whether one direction owns a complete connection.
func configuredDirection(cfg Config, direction string) bool {
	return cfg.connectionFor(direction).complete()
}

// keepSecret resolves one stored secret column: a newly typed plaintext wins, an
// explicit clear empties it, and an absent value keeps what is already stored.
func (s *Service) keepSecret(currentEnc, plaintext string, clear bool) (string, error) {
	if clear {
		return "", nil
	}
	if strings.TrimSpace(plaintext) == "" {
		return currentEnc, nil
	}
	enc, err := s.enc.Encrypt([]byte(plaintext))
	if err != nil {
		return "", Error{Category: CategoryInternal, Message: "encrypt secret failed"}
	}
	return enc, nil
}

func (s *Service) schedulerArmed() bool {
	s.statusMu.RLock()
	defer s.statusMu.RUnlock()
	return s.schedulerState
}

func (s *Service) downloadSchedulerArmed() bool {
	s.statusMu.RLock()
	defer s.statusMu.RUnlock()
	return s.schedulerDownload != nil && s.schedulerDownload.Armed()
}

func (s *Service) uploadSchedulerArmed() bool {
	s.statusMu.RLock()
	defer s.statusMu.RUnlock()
	return s.schedulerUpload != nil && s.schedulerUpload.Armed()
}

func (s *Service) resolvedConfig() (Config, string, time.Time, error) {
	env := s.env
	if s.settings != nil && s.enc != nil {
		row, err := s.settings.Get()
		if err != nil {
			return Config{}, "", time.Time{}, err
		}
		// Prefer database when any field is present (operator has saved UI settings).
		if row.HasOverride || rowHasOperatorInput(row) {
			cfg := Config{
				Enabled:        row.Enabled,
				UploadEnabled:  row.UploadEnabled,
				URL:            row.URL,
				Username:       row.Username,
				UploadURL:      row.UploadURL,
				UploadUsername: row.UploadUsername,
				CronExpr:       row.CronExpr,
				DownloadCron:   row.DownloadCron,
				UploadCron:     row.UploadCron,
				MaxBytes:       env.MaxBytes,
			}
			if row.PasswordEnc != "" {
				plain, decErr := s.enc.Decrypt(row.PasswordEnc)
				if decErr != nil {
					return Config{}, "", time.Time{}, Error{Category: CategoryInternal, Message: "decrypt password failed"}
				}
				cfg.Password = string(plain)
			}
			if row.BackupPasswordEnc != "" {
				plain, decErr := s.enc.Decrypt(row.BackupPasswordEnc)
				if decErr != nil {
					return Config{}, "", time.Time{}, Error{Category: CategoryInternal, Message: "decrypt backup password failed"}
				}
				cfg.BackupPassword = string(plain)
			}
			if row.UploadPasswordEnc != "" {
				plain, decErr := s.enc.Decrypt(row.UploadPasswordEnc)
				if decErr != nil {
					return Config{}, "", time.Time{}, Error{Category: CategoryInternal, Message: "decrypt upload password failed"}
				}
				cfg.UploadPassword = string(plain)
			}
			if row.UploadBackupPasswordEnc != "" {
				plain, decErr := s.enc.Decrypt(row.UploadBackupPasswordEnc)
				if decErr != nil {
					return Config{}, "", time.Time{}, Error{Category: CategoryInternal, Message: "decrypt upload backup password failed"}
				}
				cfg.UploadBackupPassword = string(plain)
			}
			if cfg.CronExpr == "" {
				cfg.CronExpr = "0 */6 * * *"
			}
			if cfg.MaxBytes <= 0 {
				cfg.MaxBytes = 10 << 20
			}
			return cfg, "database", row.UpdatedAt, nil
		}
	}
	// Env fallback for bootstrap / compose-only setups.
	if configured(env) || env.Enabled || env.UploadEnabled ||
		env.URL != "" || env.UploadURL != "" || env.Username != "" || env.UploadUsername != "" {
		cfg := env
		if cfg.CronExpr == "" {
			cfg.CronExpr = "0 */6 * * *"
		}
		if cfg.MaxBytes <= 0 {
			cfg.MaxBytes = 10 << 20
		}
		return cfg, "env", time.Time{}, nil
	}
	return Config{CronExpr: "0 */6 * * *", MaxBytes: 10 << 20}, "none", time.Time{}, nil
}

func rowHasOperatorInput(row *store.WebDAVSettings) bool {
	if row == nil {
		return false
	}
	return row.Enabled || row.UploadEnabled ||
		strings.TrimSpace(row.URL) != "" ||
		strings.TrimSpace(row.Username) != "" ||
		row.PasswordEnc != "" ||
		row.BackupPasswordEnc != "" ||
		strings.TrimSpace(row.UploadURL) != "" ||
		strings.TrimSpace(row.UploadUsername) != "" ||
		row.UploadPasswordEnc != "" ||
		row.UploadBackupPasswordEnc != ""
}

func (s *Service) reloadRuntimeFromDB() {
	cfg, _, _, err := s.resolvedConfig()
	if err != nil {
		return
	}
	s.cfgMu.Lock()
	s.cfg = cfg
	s.cfgMu.Unlock()
	if s.client != nil && cfg.MaxBytes > 0 {
		s.client.setMaxBytes(cfg.MaxBytes)
	}
}
