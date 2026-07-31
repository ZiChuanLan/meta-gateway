// Package runtimeconfig merges env bootstrap with Admin DB overrides and applies hot reloads.
package runtimeconfig

import (
	"fmt"
	"sync"
	"time"

	"github.com/lan/meta-gateway/internal/checkin"
	"github.com/lan/meta-gateway/internal/config"
	"github.com/lan/meta-gateway/internal/proxy"
	"github.com/lan/meta-gateway/internal/ratelimit"
	"github.com/lan/meta-gateway/internal/store"
	"github.com/robfig/cron/v3"
)

// Editable is the Admin-writable subset of gateway runtime parameters.
type Editable struct {
	RetryTimes         int    `json:"retry_times"`
	CooldownSeconds    int    `json:"cooldown_seconds"`
	CheckinEnabled     bool   `json:"checkin_enabled"`
	CheckinCron        string `json:"checkin_cron"`
	RelayRatePerMinute int    `json:"relay_rate_per_minute"`
	RelayRateBurst     int    `json:"relay_rate_burst"`
	AdminRatePerMinute int    `json:"admin_rate_per_minute"`
	AdminRateBurst     int    `json:"admin_rate_burst"`
	AuditRetentionDays int    `json:"audit_retention_days"`
	AuditRetentionRows int    `json:"audit_retention_rows"`
}

// Snapshot is the effective runtime view returned to Admin UI.
type Snapshot struct {
	Source       string     `json:"source"` // environment | admin_override
	HasOverride  bool       `json:"has_override"`
	Editable     Editable   `json:"editable"`
	EnvBootstrap Editable   `json:"env_bootstrap"`
	UpdatedAt    *time.Time `json:"updated_at,omitempty"`
	Note         string     `json:"note"`
	// Read-only process facts (not Admin-writable).
	ServerHTTPAddr string `json:"server_http_addr"`
	DataDir        string `json:"data_dir"`
}

// Appliers are optional hot-reload targets. Nil entries are skipped.
type Appliers struct {
	Proxy        *proxy.Service
	RelayLimiter *ratelimit.Limiter
	AdminLimiter *ratelimit.Limiter
	CheckinSched *checkin.Scheduler
	// CheckinAllowed reports whether the check-in module may run (plugin gate).
	// When nil, check-in enablement follows the editable flag alone.
	CheckinAllowed func() bool
	SetAudit       func(days, rows int)
	SetAuditLoop   func(days, rows int)
}

// Controller loads, validates, persists, and applies runtime settings.
type Controller struct {
	mu       sync.RWMutex
	env      Editable
	cfg      *config.Config
	store    *store.RuntimeSettingsStore
	appliers Appliers
	current  Editable
	source   string
	updated  *time.Time
}

func New(cfg *config.Config, settingsStore *store.RuntimeSettingsStore, appliers Appliers) *Controller {
	env := Editable{
		RetryTimes:         cfg.RetryTimes,
		CooldownSeconds:    int(cfg.Cooldown / time.Second),
		CheckinEnabled:     cfg.CheckinEnabled,
		CheckinCron:        cfg.CheckinCron,
		RelayRatePerMinute: cfg.RelayRatePerMinute,
		RelayRateBurst:     cfg.RelayRateBurst,
		AdminRatePerMinute: cfg.AdminRatePerMinute,
		AdminRateBurst:     cfg.AdminRateBurst,
		AuditRetentionDays: cfg.AuditRetentionDays,
		AuditRetentionRows: cfg.AuditRetentionRows,
	}
	c := &Controller{
		env:      env,
		cfg:      cfg,
		store:    settingsStore,
		appliers: appliers,
		current:  env,
		source:   "environment",
	}
	return c
}

// Bootstrap loads DB overrides (if any) and applies them once at process start.
func (c *Controller) Bootstrap() error {
	if c == nil {
		return nil
	}
	row, err := c.store.Get()
	if err != nil {
		return err
	}
	if row == nil || !row.HasOverride {
		c.applyLocked(c.env)
		return nil
	}
	editable := rowToEditable(row)
	if err := Validate(editable); err != nil {
		// Fall back to env if stored row is corrupt.
		c.applyLocked(c.env)
		return fmt.Errorf("runtime settings override invalid, using env: %w", err)
	}
	c.mu.Lock()
	c.current = editable
	c.source = "admin_override"
	if !row.UpdatedAt.IsZero() {
		updated := row.UpdatedAt
		c.updated = &updated
	}
	c.mu.Unlock()
	c.applyLocked(editable)
	return nil
}

func (c *Controller) Snapshot() Snapshot {
	c.mu.RLock()
	defer c.mu.RUnlock()
	note := "Editable values apply immediately (hot reload). Server listen address and data directory still require restart."
	if c.source == "environment" {
		note = "Using environment bootstrap. Save from Admin to override and hot-reload. Secrets are never editable here."
	}
	return Snapshot{
		Source:         c.source,
		HasOverride:    c.source == "admin_override",
		Editable:       c.current,
		EnvBootstrap:   c.env,
		UpdatedAt:      c.updated,
		Note:           note,
		ServerHTTPAddr: c.cfg.HTTPAddr,
		DataDir:        c.cfg.DataDir,
	}
}

// Update validates, persists, and hot-applies Admin overrides.
func (c *Controller) Update(next Editable) (Snapshot, error) {
	if err := Validate(next); err != nil {
		return Snapshot{}, err
	}
	row := &store.RuntimeSettingsRow{
		HasOverride:        true,
		RetryTimes:         next.RetryTimes,
		CooldownSeconds:    next.CooldownSeconds,
		CheckinEnabled:     next.CheckinEnabled,
		CheckinCron:        next.CheckinCron,
		RelayRatePerMinute: next.RelayRatePerMinute,
		RelayRateBurst:     next.RelayRateBurst,
		AdminRatePerMinute: next.AdminRatePerMinute,
		AdminRateBurst:     next.AdminRateBurst,
		AuditRetentionDays: next.AuditRetentionDays,
		AuditRetentionRows: next.AuditRetentionRows,
	}
	// Apply first so invalid scheduler cron fails before DB write? Prefer validate cron in Validate.
	if err := c.applyWithError(next); err != nil {
		return Snapshot{}, err
	}
	if err := c.store.Save(row); err != nil {
		// Best-effort: settings already applied in memory; surface persistence error.
		return Snapshot{}, err
	}
	now := time.Now().UTC()
	c.mu.Lock()
	c.current = next
	c.source = "admin_override"
	c.updated = &now
	c.mu.Unlock()
	return c.Snapshot(), nil
}

// ClearOverride removes Admin override and re-applies env bootstrap.
func (c *Controller) ClearOverride() (Snapshot, error) {
	row := &store.RuntimeSettingsRow{HasOverride: false}
	if err := c.store.Save(row); err != nil {
		return Snapshot{}, err
	}
	if err := c.applyWithError(c.env); err != nil {
		return Snapshot{}, err
	}
	c.mu.Lock()
	c.current = c.env
	c.source = "environment"
	c.updated = nil
	c.mu.Unlock()
	return c.Snapshot(), nil
}

func (c *Controller) applyLocked(values Editable) {
	_ = c.applyWithError(values)
}

// ResyncCheckin re-applies the current check-in schedule using the latest editable
// settings and CheckinAllowed gate. Call when the checkin add-on is toggled.
func (c *Controller) ResyncCheckin() error {
	c.mu.RLock()
	values := c.current
	c.mu.RUnlock()
	if c.appliers.CheckinSched == nil {
		return nil
	}
	enabled := values.CheckinEnabled
	if c.appliers.CheckinAllowed != nil && !c.appliers.CheckinAllowed() {
		enabled = false
	}
	return c.appliers.CheckinSched.SetSchedule(values.CheckinCron, enabled)
}

func (c *Controller) applyWithError(values Editable) error {
	if c.appliers.Proxy != nil {
		c.appliers.Proxy.SetRetryPolicy(values.RetryTimes, time.Duration(values.CooldownSeconds)*time.Second)
	}
	if c.appliers.RelayLimiter != nil {
		c.appliers.RelayLimiter.SetLimits(values.RelayRatePerMinute, values.RelayRateBurst)
	}
	if c.appliers.AdminLimiter != nil {
		c.appliers.AdminLimiter.SetLimits(values.AdminRatePerMinute, values.AdminRateBurst)
	}
	if c.appliers.SetAudit != nil {
		c.appliers.SetAudit(values.AuditRetentionDays, values.AuditRetentionRows)
	}
	if c.appliers.SetAuditLoop != nil {
		c.appliers.SetAuditLoop(values.AuditRetentionDays, values.AuditRetentionRows)
	}
	if c.appliers.CheckinSched != nil {
		enabled := values.CheckinEnabled
		if c.appliers.CheckinAllowed != nil && !c.appliers.CheckinAllowed() {
			enabled = false
		}
		if err := c.appliers.CheckinSched.SetSchedule(values.CheckinCron, enabled); err != nil {
			return err
		}
	}
	return nil
}

func rowToEditable(row *store.RuntimeSettingsRow) Editable {
	return Editable{
		RetryTimes:         row.RetryTimes,
		CooldownSeconds:    row.CooldownSeconds,
		CheckinEnabled:     row.CheckinEnabled,
		CheckinCron:        row.CheckinCron,
		RelayRatePerMinute: row.RelayRatePerMinute,
		RelayRateBurst:     row.RelayRateBurst,
		AdminRatePerMinute: row.AdminRatePerMinute,
		AdminRateBurst:     row.AdminRateBurst,
		AuditRetentionDays: row.AuditRetentionDays,
		AuditRetentionRows: row.AuditRetentionRows,
	}
}

// Validate enforces the same bounds as env loading for Admin-writable fields.
func Validate(values Editable) error {
	if values.RetryTimes < 0 || values.RetryTimes > 100 {
		return fmt.Errorf("retry_times must be between 0 and 100")
	}
	if values.CooldownSeconds < 0 || values.CooldownSeconds > 86400 {
		return fmt.Errorf("cooldown_seconds must be between 0 and 86400")
	}
	if values.RelayRatePerMinute < 0 || values.RelayRatePerMinute > 1_000_000 {
		return fmt.Errorf("relay_rate_per_minute out of range")
	}
	if values.RelayRateBurst < 0 || values.RelayRateBurst > 1_000_000 {
		return fmt.Errorf("relay_rate_burst out of range")
	}
	if values.AdminRatePerMinute < 0 || values.AdminRatePerMinute > 1_000_000 {
		return fmt.Errorf("admin_rate_per_minute out of range")
	}
	if values.AdminRateBurst < 0 || values.AdminRateBurst > 1_000_000 {
		return fmt.Errorf("admin_rate_burst out of range")
	}
	if values.AuditRetentionDays < 0 || values.AuditRetentionDays > 36500 {
		return fmt.Errorf("audit_retention_days out of range")
	}
	if values.AuditRetentionRows < 0 || values.AuditRetentionRows > 10_000_000 {
		return fmt.Errorf("audit_retention_rows out of range")
	}
	cronExpr := values.CheckinCron
	if cronExpr == "" {
		cronExpr = "0 8 * * *"
	}
	parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
	if _, err := parser.Parse(cronExpr); err != nil {
		return fmt.Errorf("checkin_cron is invalid")
	}
	return nil
}
