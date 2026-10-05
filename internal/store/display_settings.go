package store

import (
	"fmt"
	"strings"
	"time"
)

// DisplaySettings is the site's money presentation: a symbol and a rate.
//
// Amounts are stored in the ledger's unit (USD — the same number
// `usage_records.cost` holds); these two values only decide how they are
// printed. Changing the rate never rewrites a stored amount, so history stays
// comparable while the operator can show their own currency.
type DisplaySettings struct {
	CurrencySymbol string    `json:"currency_symbol"`
	CurrencyRate   float64   `json:"currency_rate"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// DefaultCurrencySymbol is what an unconfigured site shows.
const DefaultCurrencySymbol = "$"

// DisplaySettingsStore reads and writes the single display row.
type DisplaySettingsStore struct{ db *DB }

// Get returns the settings, falling back to defaults when the row is missing or
// unusable. It never fails: a display preference must not be able to block a
// request, and the page it feeds has to render regardless.
func (s *DisplaySettingsStore) Get() DisplaySettings {
	out := DisplaySettings{CurrencySymbol: DefaultCurrencySymbol, CurrencyRate: 1}
	var symbol string
	var rate float64
	row := s.db.QueryRow(`SELECT currency_symbol, currency_rate, updated_at FROM site_display_settings WHERE id = 1`)
	if err := row.Scan(&symbol, &rate, scanTime(&out.UpdatedAt)); err != nil {
		return out
	}
	if trimmed := strings.TrimSpace(symbol); trimmed != "" && len(trimmed) <= 8 {
		out.CurrencySymbol = trimmed
	}
	if rate > 0 && rate <= 100000 {
		out.CurrencyRate = rate
	}
	return out
}

// Save validates and persists the settings.
func (s *DisplaySettingsStore) Save(settings DisplaySettings) error {
	symbol := strings.TrimSpace(settings.CurrencySymbol)
	if symbol == "" {
		symbol = DefaultCurrencySymbol
	}
	if len(symbol) > 8 {
		return fmt.Errorf("currency symbol is too long")
	}
	if settings.CurrencyRate <= 0 || settings.CurrencyRate > 100000 {
		return fmt.Errorf("currency rate must be between 0 and 100000")
	}
	_, err := s.db.Exec(`
		INSERT INTO site_display_settings (id, currency_symbol, currency_rate, updated_at)
		VALUES (1, ?, ?, datetime('now'))
		ON CONFLICT(id) DO UPDATE SET
			currency_symbol = excluded.currency_symbol,
			currency_rate = excluded.currency_rate,
			updated_at = datetime('now')`,
		symbol, settings.CurrencyRate)
	if err != nil {
		return fmt.Errorf("display settings save: %w", err)
	}
	return nil
}
