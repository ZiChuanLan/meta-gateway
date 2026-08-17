package webdavsync

import (
	"strings"
	"time"

	"github.com/lan/meta-gateway/internal/store"
	cronlib "github.com/robfig/cron/v3"
)

// SettingsView is the redacted Admin settings payload (never includes secrets).
type SettingsView struct {
	Enabled           bool      `json:"enabled"`
	URL               string    `json:"url"`
	Username          string    `json:"username"`
	HasPassword       bool      `json:"has_password"`
	HasBackupPassword bool      `json:"has_backup_password"`
	CronExpr          string    `json:"cron"`
	Configured        bool      `json:"configured"`
	SchedulerArmed    bool      `json:"scheduler_armed"`
	Source            string    `json:"source"` // "database" | "env" | "none"
	UpdatedAt         time.Time `json:"updated_at,omitempty"`
	TargetURL         string    `json:"target_url,omitempty"`
}

// SettingsUpdate is the Admin PUT body. Empty password fields keep existing secrets.
type SettingsUpdate struct {
	Enabled             bool   `json:"enabled"`
	URL                 string `json:"url"`
	Username            string `json:"username"`
	Password            string `json:"password"`
	BackupPassword      string `json:"backup_password"`
	CronExpr            string `json:"cron"`
	ClearPassword       bool   `json:"clear_password"`
	ClearBackupPassword bool   `json:"clear_backup_password"`
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
		Enabled:           cfg.Enabled,
		URL:               cfg.URL,
		Username:          cfg.Username,
		HasPassword:       cfg.Password != "",
		HasBackupPassword: cfg.BackupPassword != "",
		CronExpr:          cfg.CronExpr,
		Configured:        configured(cfg),
		SchedulerArmed:    s.schedulerArmed(),
		Source:            source,
		UpdatedAt:         updatedAt,
	}
	if target, resolveErr := ResolveBackupURL(cfg.URL); resolveErr == nil {
		view.TargetURL = RedactedURL(target)
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
	url := strings.TrimSpace(update.URL)
	username := update.Username
	cron := strings.TrimSpace(update.CronExpr)
	if cron == "" {
		cron = "0 */6 * * *"
	}
	if url != "" {
		if _, resolveErr := ResolveBackupURL(url); resolveErr != nil {
			return nil, resolveErr
		}
	}
	// Validate cron expression when a non-default value is provided.
	if cron != "0 */6 * * *" {
		parser := cronlib.NewParser(cronlib.Minute | cronlib.Hour | cronlib.Dom | cronlib.Month | cronlib.Dow)
		if _, parseErr := parser.Parse(cron); parseErr != nil {
			return nil, Error{Category: CategoryValidation, Message: "invalid cron expression: " + parseErr.Error()}
		}
	}

	passwordEnc := current.PasswordEnc
	backupEnc := current.BackupPasswordEnc
	if update.ClearPassword {
		passwordEnc = ""
	} else if strings.TrimSpace(update.Password) != "" {
		enc, encErr := s.enc.Encrypt([]byte(update.Password))
		if encErr != nil {
			return nil, Error{Category: CategoryInternal, Message: "encrypt password failed"}
		}
		passwordEnc = enc
	}
	if update.ClearBackupPassword {
		backupEnc = ""
	} else if strings.TrimSpace(update.BackupPassword) != "" {
		enc, encErr := s.enc.Encrypt([]byte(update.BackupPassword))
		if encErr != nil {
			return nil, Error{Category: CategoryInternal, Message: "encrypt backup password failed"}
		}
		backupEnc = enc
	}

	next := &store.WebDAVSettings{
		HasOverride:       true,
		Enabled:           update.Enabled,
		URL:               url,
		Username:          username,
		PasswordEnc:       passwordEnc,
		BackupPasswordEnc: backupEnc,
		CronExpr:          cron,
	}
	if saveErr := s.settings.Save(next); saveErr != nil {
		return nil, Error{Category: CategoryInternal, Message: "save settings failed"}
	}
	// Refresh in-memory runtime config for immediate test/sync.
	s.reloadRuntimeFromDB()
	if err := s.applyScheduler(); err != nil {
		// Keep durable settings, the in-memory config, and the active schedule in
		// one state. Scheduler shutdown/races can still reject an otherwise valid
		// expression after the row was saved, so restore the previous row.
		_ = s.settings.Save(current)
		s.reloadRuntimeFromDB()
		_ = s.applyScheduler()
		return nil, Error{Category: CategoryInternal, Message: "apply scheduler settings failed"}
	}
	return s.SettingsView()
}

func configured(cfg Config) bool {
	return strings.TrimSpace(cfg.URL) != "" &&
		strings.TrimSpace(cfg.Username) != "" &&
		cfg.Password != ""
}

func (s *Service) schedulerArmed() bool {
	s.statusMu.RLock()
	defer s.statusMu.RUnlock()
	return s.schedulerState
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
				Enabled:  row.Enabled,
				URL:      row.URL,
				Username: row.Username,
				CronExpr: row.CronExpr,
				MaxBytes: env.MaxBytes,
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
	if configured(env) || env.Enabled || env.URL != "" || env.Username != "" {
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
	return row.Enabled ||
		strings.TrimSpace(row.URL) != "" ||
		strings.TrimSpace(row.Username) != "" ||
		row.PasswordEnc != "" ||
		row.BackupPasswordEnc != ""
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
