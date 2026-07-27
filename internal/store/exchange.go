package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

var ErrExchangeConflict = errors.New("exchange identity conflict")

type ExchangeExportRow struct {
	ChannelID    int64
	CredentialID int64
	Name         string
	BaseURL      string
	ModelsCSV    string
	GroupName    string
	Priority     int
	Weight       int
	Status       string
	TypeHint     string
	SecretEnc    string
	SiteBaseURL  string
	Platform     string
}

type ExchangeLegacyCandidate struct {
	ChannelID      int64
	CredentialID   int64
	CredentialUses int
	ChannelBaseURL string
	SiteBaseURL    string
	SecretEnc      string
}

type ExchangeImportItem struct {
	Name              string
	BaseURL           string
	ModelsCSV         string
	GroupName         string
	Priority          int
	Weight            int
	Status            string
	TypeHint          string
	SecretEnc         string
	Fingerprint       string
	AdoptChannelID    int64
	AdoptCredentialID int64
	CredentialKind    string
	MetaJSON          string
	CheckinEnabled    bool
}

type ExchangeImportResult struct {
	CreatedChannelIDs []int64
	UpdatedChannelIDs []int64
	AdoptedChannelIDs []int64
}

func (r ExchangeImportResult) ChannelIDs() []int64 {
	ids := make([]int64, 0, len(r.CreatedChannelIDs)+len(r.UpdatedChannelIDs)+len(r.AdoptedChannelIDs))
	ids = append(ids, r.CreatedChannelIDs...)
	ids = append(ids, r.UpdatedChannelIDs...)
	ids = append(ids, r.AdoptedChannelIDs...)
	return ids
}

type ExchangeStore struct {
	db *sql.DB
}

func (s *ExchangeStore) Export(ctx context.Context, channelIDs []int64) ([]ExchangeExportRow, error) {
	query := `SELECT c.id, COALESCE(c.credential_id, 0), c.name, c.base_url, c.models_csv, c.group_name,
		c.priority, c.weight, c.status, c.type_hint, COALESCE(cr.secret_enc, ''),
        COALESCE(si.base_url, ''), COALESCE(si.platform, '')
        FROM channels c
		LEFT JOIN credentials cr ON cr.id = c.credential_id
        LEFT JOIN sites si ON si.id = c.site_id
		WHERE 1 = 1`
	args := make([]any, 0, len(channelIDs))
	if len(channelIDs) > 0 {
		query += ` AND c.id IN (` + placeholders(len(channelIDs)) + `)`
		for _, id := range channelIDs {
			args = append(args, id)
		}
	}
	query += ` ORDER BY c.id`

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("exchange export query: %w", err)
	}
	defer rows.Close()
	result := make([]ExchangeExportRow, 0)
	for rows.Next() {
		var row ExchangeExportRow
		if err := rows.Scan(&row.ChannelID, &row.CredentialID, &row.Name, &row.BaseURL, &row.ModelsCSV,
			&row.GroupName, &row.Priority, &row.Weight, &row.Status, &row.TypeHint,
			&row.SecretEnc, &row.SiteBaseURL, &row.Platform); err != nil {
			return nil, fmt.Errorf("exchange export scan: %w", err)
		}
		result = append(result, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("exchange export rows: %w", err)
	}
	return result, nil
}

// LegacyCandidates returns only credentials not yet owned by exchange identity.
// URL normalization and constant-time secret comparison remain service concerns.
func (s *ExchangeStore) LegacyCandidates(ctx context.Context) ([]ExchangeLegacyCandidate, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT c.id, cr.id,
        (SELECT COUNT(*) FROM channels linked WHERE linked.credential_id = cr.id),
        c.base_url, COALESCE(si.base_url, ''), cr.secret_enc
        FROM channels c
        JOIN credentials cr ON cr.id = c.credential_id
        LEFT JOIN sites si ON si.id = c.site_id
        WHERE cr.import_fingerprint IS NULL OR cr.import_fingerprint = ''
        ORDER BY c.id`)
	if err != nil {
		return nil, fmt.Errorf("exchange legacy query: %w", err)
	}
	defer rows.Close()
	var result []ExchangeLegacyCandidate
	for rows.Next() {
		var candidate ExchangeLegacyCandidate
		if err := rows.Scan(&candidate.ChannelID, &candidate.CredentialID,
			&candidate.CredentialUses, &candidate.ChannelBaseURL,
			&candidate.SiteBaseURL, &candidate.SecretEnc); err != nil {
			return nil, fmt.Errorf("exchange legacy scan: %w", err)
		}
		result = append(result, candidate)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("exchange legacy rows: %w", err)
	}
	return result, nil
}

func (s *ExchangeStore) Import(ctx context.Context, items []ExchangeImportItem) (ExchangeImportResult, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ExchangeImportResult{}, fmt.Errorf("exchange import begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var result ExchangeImportResult
	for _, item := range items {
		channelID, outcome, err := importExchangeItem(ctx, tx, item)
		if err != nil {
			return ExchangeImportResult{}, err
		}
		switch outcome {
		case "created":
			result.CreatedChannelIDs = append(result.CreatedChannelIDs, channelID)
		case "updated":
			result.UpdatedChannelIDs = append(result.UpdatedChannelIDs, channelID)
		case "adopted":
			result.AdoptedChannelIDs = append(result.AdoptedChannelIDs, channelID)
		}
	}
	if err := tx.Commit(); err != nil {
		return ExchangeImportResult{}, fmt.Errorf("exchange import commit: %w", err)
	}
	return result, nil
}

func importExchangeItem(ctx context.Context, tx *sql.Tx, item ExchangeImportItem) (int64, string, error) {
	credentialID, channelID, found, err := fingerprintIdentity(ctx, tx, item.Fingerprint)
	if err != nil {
		return 0, "", err
	}
	if found {
		if item.AdoptChannelID != 0 || item.AdoptCredentialID != 0 {
			return 0, "", ErrExchangeConflict
		}
		if err := updateExchangeAsset(ctx, tx, credentialID, channelID, item); err != nil {
			return 0, "", err
		}
		return channelID, "updated", nil
	}

	if item.AdoptChannelID != 0 || item.AdoptCredentialID != 0 {
		if item.AdoptChannelID <= 0 || item.AdoptCredentialID <= 0 {
			return 0, "", ErrExchangeConflict
		}
		if err := adoptExchangeAsset(ctx, tx, item); err != nil {
			return 0, "", err
		}
		return item.AdoptChannelID, "adopted", nil
	}

	siteID, err := getOrCreateExchangeSite(ctx, tx, item)
	if err != nil {
		return 0, "", err
	}
	kind := item.CredentialKind
	if kind == "" {
		kind = "api_key"
	}
	checkinEnabled := 0
	if item.CheckinEnabled {
		checkinEnabled = 1
	}
	credentialResult, err := tx.ExecContext(ctx, `INSERT INTO credentials
        (site_id, kind, secret_enc, meta_json, status, checkin_enabled, import_fingerprint)
        VALUES (?, ?, ?, ?, ?, ?, ?)`, siteID, kind, item.SecretEnc, item.MetaJSON, item.Status, checkinEnabled, item.Fingerprint)
	if err != nil {
		return 0, "", fmt.Errorf("exchange credential create: %w", err)
	}
	credentialID, err = credentialResult.LastInsertId()
	if err != nil {
		return 0, "", fmt.Errorf("exchange credential id: %w", err)
	}
	channelResult, err := tx.ExecContext(ctx, `INSERT INTO channels
        (site_id, credential_id, name, base_url, models_csv, group_name, priority, weight, status, type_hint)
        VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, siteID, credentialID, item.Name,
		item.BaseURL, item.ModelsCSV, item.GroupName, item.Priority, item.Weight,
		item.Status, item.TypeHint)
	if err != nil {
		return 0, "", fmt.Errorf("exchange channel create: %w", err)
	}
	channelID, err = channelResult.LastInsertId()
	if err != nil {
		return 0, "", fmt.Errorf("exchange channel id: %w", err)
	}
	return channelID, "created", nil
}

func fingerprintIdentity(ctx context.Context, tx *sql.Tx, fingerprint string) (int64, int64, bool, error) {
	var credentialID int64
	err := tx.QueryRowContext(ctx, `SELECT id FROM credentials WHERE import_fingerprint = ?`, fingerprint).Scan(&credentialID)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, 0, false, nil
	}
	if err != nil {
		return 0, 0, false, fmt.Errorf("exchange identity lookup: %w", err)
	}
	rows, err := tx.QueryContext(ctx, `SELECT id FROM channels WHERE credential_id = ? ORDER BY id`, credentialID)
	if err != nil {
		return 0, 0, false, fmt.Errorf("exchange identity channels: %w", err)
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return 0, 0, false, fmt.Errorf("exchange identity channel scan: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return 0, 0, false, fmt.Errorf("exchange identity channel rows: %w", err)
	}
	// Exactly one channel still bound to this fingerprint: normal update path.
	if len(ids) == 1 {
		return credentialID, ids[0], true, nil
	}
	// Multiple channels sharing one credential is a true identity conflict.
	if len(ids) > 1 {
		return 0, 0, false, ErrExchangeConflict
	}
	// Zero channels: orphan fingerprint. Common after "sync API keys" rebinds the
	// channel to a new api_key credential while the imported access_token keeps
	// import_fingerprint. Reattach to a site channel instead of failing re-import.
	channelID, attachErr := reattachOrphanFingerprint(ctx, tx, credentialID)
	if attachErr != nil {
		return 0, 0, false, attachErr
	}
	return credentialID, channelID, true, nil
}

// reattachOrphanFingerprint finds or creates a channel for a fingerprint credential
// that is no longer referenced by any channel.
func reattachOrphanFingerprint(ctx context.Context, tx *sql.Tx, credentialID int64) (int64, error) {
	var siteID int64
	var kind, status, metaJSON string
	var checkinEnabled int
	err := tx.QueryRowContext(ctx, `SELECT site_id, kind, status, COALESCE(meta_json, ''), checkin_enabled
		FROM credentials WHERE id = ?`, credentialID).Scan(&siteID, &kind, &status, &metaJSON, &checkinEnabled)
	if err != nil {
		return 0, fmt.Errorf("exchange orphan credential: %w", err)
	}
	// Prefer an existing channel on the same site (often the one rebound to api_key).
	var channelID int64
	err = tx.QueryRowContext(ctx, `SELECT id FROM channels WHERE site_id = ? ORDER BY id LIMIT 1`, siteID).Scan(&channelID)
	if err == nil && channelID > 0 {
		// Keep channel.credential_id if it already points at an api_key (relay path).
		// Only re-point when current bind is missing/disabled/non-api_key.
		var boundID sql.NullInt64
		var boundKind string
		_ = tx.QueryRowContext(ctx, `SELECT c.credential_id, COALESCE(cr.kind, '')
			FROM channels c LEFT JOIN credentials cr ON cr.id = c.credential_id
			WHERE c.id = ?`, channelID).Scan(&boundID, &boundKind)
		shouldRebind := !boundID.Valid || boundID.Int64 == 0
		if boundID.Valid && boundID.Int64 > 0 {
			k := strings.ToLower(strings.TrimSpace(boundKind))
			if k != "api_key" {
				shouldRebind = true
			}
		}
		if shouldRebind {
			if _, err := tx.ExecContext(ctx, `UPDATE channels SET credential_id = ?, updated_at = datetime('now') WHERE id = ?`,
				credentialID, channelID); err != nil {
				return 0, fmt.Errorf("exchange orphan rebind: %w", err)
			}
		}
		return channelID, nil
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("exchange orphan channel lookup: %w", err)
	}
	// No channel on site: create one bound to this credential.
	var siteBase, siteName, sitePlatform string
	if err := tx.QueryRowContext(ctx, `SELECT name, base_url, platform FROM sites WHERE id = ?`, siteID).
		Scan(&siteName, &siteBase, &sitePlatform); err != nil {
		return 0, fmt.Errorf("exchange orphan site: %w", err)
	}
	res, err := tx.ExecContext(ctx, `INSERT INTO channels
		(site_id, credential_id, name, base_url, models_csv, group_name, priority, weight, status, type_hint)
		VALUES (?, ?, ?, ?, '', 'default', 0, 100, ?, ?)`,
		siteID, credentialID, siteName, siteBase, status, sitePlatform)
	if err != nil {
		return 0, fmt.Errorf("exchange orphan channel create: %w", err)
	}
	return res.LastInsertId()
}

func updateExchangeAsset(ctx context.Context, tx *sql.Tx, credentialID, channelID int64, item ExchangeImportItem) error {
	var siteID int64
	if err := tx.QueryRowContext(ctx, `SELECT site_id FROM credentials WHERE id = ?`, credentialID).Scan(&siteID); err != nil {
		return fmt.Errorf("exchange credential site: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE sites SET name = ?, base_url = ?, platform = ?, status = ?, updated_at = datetime('now') WHERE id = ?`,
		item.Name, item.BaseURL, item.TypeHint, item.Status, siteID); err != nil {
		return fmt.Errorf("exchange site update: %w", err)
	}
	kind := item.CredentialKind
	if kind == "" {
		kind = "api_key"
	}
	checkinEnabled := 0
	if item.CheckinEnabled {
		checkinEnabled = 1
	}
	if _, err := tx.ExecContext(ctx, `UPDATE credentials SET kind = ?, secret_enc = ?, meta_json = ?, status = ?, checkin_enabled = ?, updated_at = datetime('now') WHERE id = ?`,
		kind, item.SecretEnc, item.MetaJSON, item.Status, checkinEnabled, credentialID); err != nil {
		return fmt.Errorf("exchange credential update: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE channels SET site_id = ?, name = ?, base_url = ?, models_csv = ?, group_name = ?, priority = ?, weight = ?, status = ?, type_hint = ?, updated_at = datetime('now') WHERE id = ?`,
		siteID, item.Name, item.BaseURL, item.ModelsCSV, item.GroupName, item.Priority,
		item.Weight, item.Status, item.TypeHint, channelID); err != nil {
		return fmt.Errorf("exchange channel update: %w", err)
	}
	return nil
}

func adoptExchangeAsset(ctx context.Context, tx *sql.Tx, item ExchangeImportItem) error {
	var linkedCredentialID int64
	var uses int
	err := tx.QueryRowContext(ctx, `SELECT credential_id,
        (SELECT COUNT(*) FROM channels WHERE credential_id = ?)
        FROM channels WHERE id = ?`, item.AdoptCredentialID, item.AdoptChannelID).Scan(&linkedCredentialID, &uses)
	if errors.Is(err, sql.ErrNoRows) || linkedCredentialID != item.AdoptCredentialID || uses != 1 {
		return ErrExchangeConflict
	}
	if err != nil {
		return fmt.Errorf("exchange adoption verify: %w", err)
	}
	kind := item.CredentialKind
	if kind == "" {
		kind = "api_key"
	}
	checkinEnabled := 0
	if item.CheckinEnabled {
		checkinEnabled = 1
	}
	result, err := tx.ExecContext(ctx, `UPDATE credentials SET import_fingerprint = ?, kind = ?, secret_enc = ?, meta_json = ?, status = ?, checkin_enabled = ?, updated_at = datetime('now')
        WHERE id = ? AND (import_fingerprint IS NULL OR import_fingerprint = '')`,
		item.Fingerprint, kind, item.SecretEnc, item.MetaJSON, item.Status, checkinEnabled, item.AdoptCredentialID)
	if err != nil {
		return fmt.Errorf("exchange adoption credential: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("exchange adoption rows: %w", err)
	}
	if changed != 1 {
		return ErrExchangeConflict
	}
	return updateExchangeAsset(ctx, tx, item.AdoptCredentialID, item.AdoptChannelID, item)
}

func getOrCreateExchangeSite(ctx context.Context, tx *sql.Tx, item ExchangeImportItem) (int64, error) {
	rows, err := tx.QueryContext(ctx, `SELECT id FROM sites WHERE base_url = ? ORDER BY id`, item.BaseURL)
	if err != nil {
		return 0, fmt.Errorf("exchange site lookup: %w", err)
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return 0, fmt.Errorf("exchange site scan: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Close(); err != nil {
		return 0, fmt.Errorf("exchange site rows: %w", err)
	}
	if len(ids) >= 1 {
		// Prefer the oldest site when duplicates exist (legacy data), instead of failing import.
		return ids[0], nil
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO sites (name, base_url, platform, status) VALUES (?, ?, ?, ?)`, item.Name, item.BaseURL, item.TypeHint, item.Status)
	if err != nil {
		return 0, fmt.Errorf("exchange site create: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("exchange site id: %w", err)
	}
	return id, nil
}

func placeholders(count int) string {
	return strings.TrimSuffix(strings.Repeat("?,", count), ",")
}
