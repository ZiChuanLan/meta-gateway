package store

import (
	"database/sql"
	"fmt"
	"strings"
	"sync"

	"github.com/lan/meta-gateway/internal/domain"
)

// CredentialStore provides CRUD operations for credentials.
//
// Credentials are read on every relay attempt (bound credential lookup plus
// the site-wide api_key pool), so reads are served from an in-process cache:
// by id and by the enabled api_key pool per site. Every write path
// invalidates the affected entries; bulk imports clear everything.
type CredentialStore struct {
	db *sql.DB

	mu   sync.RWMutex
	byID map[int64]*domain.Credential
	// bySiteKeys caches the enabled api_key pool per site (nil value = cached empty).
	bySiteKeys map[int64][]domain.Credential
	// bySiteModels caches per-credential discovered model sets per site
	// (site -> credential id -> model set). Nil map = not cached yet.
	bySiteModels map[int64]map[int64]map[string]struct{}
	generation   uint64
}

func newCredentialStore(db *sql.DB) *CredentialStore {
	return &CredentialStore{
		db:           db,
		byID:         make(map[int64]*domain.Credential),
		bySiteKeys:   make(map[int64][]domain.Credential),
		bySiteModels: make(map[int64]map[int64]map[string]struct{}),
	}
}

// ClearCache drops every cached credential and site key pool (used after bulk
// imports that write credentials outside this store).
func (s *CredentialStore) ClearCache() {
	s.mu.Lock()
	s.byID = make(map[int64]*domain.Credential)
	s.bySiteKeys = make(map[int64][]domain.Credential)
	s.bySiteModels = make(map[int64]map[int64]map[string]struct{})
	s.generation++
	s.mu.Unlock()
}

// cloneCredential returns a deep copy (SecretEnc/CookieEnc buffers included) so
// callers can never mutate the cached object through a shared pointer.
func cloneCredential(credential *domain.Credential) *domain.Credential {
	if credential == nil {
		return nil
	}
	copy := *credential
	copy.SecretEnc = append([]byte(nil), credential.SecretEnc...)
	copy.CookieEnc = append([]byte(nil), credential.CookieEnc...)
	return &copy
}

func (s *CredentialStore) invalidate(id int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.generation++
	if old, ok := s.byID[id]; ok {
		delete(s.bySiteKeys, old.SiteID)
		delete(s.bySiteModels, old.SiteID)
		delete(s.byID, id)
		return
	}
	// Unknown entry: clear every site pool conservatively (the pool depends on
	// status/kind/secret of every credential on the site).
	for siteID := range s.bySiteKeys {
		delete(s.bySiteKeys, siteID)
		delete(s.bySiteModels, siteID)
	}
}

func (s *CredentialStore) cachePutIfGeneration(credential *domain.Credential, generation uint64) {
	if credential == nil || credential.ID <= 0 {
		return
	}
	cloned := cloneCredential(credential)
	s.mu.Lock()
	if s.generation == generation {
		s.byID[cloned.ID] = cloned
	}
	s.mu.Unlock()
}

func (s *CredentialStore) ListBySite(siteID int64) ([]domain.Credential, error) {
	rows, err := s.db.Query(`SELECT id, site_id, kind, auth_mode, secret_enc, cookie_enc, meta_json, status, checkin_enabled, COALESCE(import_fingerprint, ''), COALESCE(models_csv, ''), created_at, updated_at FROM credentials WHERE site_id = ? ORDER BY id`, siteID)
	if err != nil {
		return nil, fmt.Errorf("credential list: %w", err)
	}
	defer rows.Close()

	var result []domain.Credential
	for rows.Next() {
		var r domain.Credential
		var secret, cookie string
		if err := rows.Scan(&r.ID, &r.SiteID, &r.Kind, &r.AuthMode, &secret, &cookie, &r.MetaJSON, &r.Status, &r.CheckinEnabled, &r.ImportFingerprint, &r.ModelsCSV, scanTime(&r.CreatedAt), scanTime(&r.UpdatedAt)); err != nil {
			return nil, fmt.Errorf("credential scan: %w", err)
		}
		r.SecretEnc = []byte(secret)
		r.CookieEnc = []byte(cookie)
		result = append(result, r)
	}
	return result, rows.Err()
}

// ListEnabledAPIKeysBySite returns enabled api_key credentials that still hold ciphertext.
// Used as the site-level relay key pool (aggregation across many keys for one upstream).
// Results are cached per site and invalidated by any credential write.
func (s *CredentialStore) ListEnabledAPIKeysBySite(siteID int64) ([]domain.Credential, error) {
	s.mu.RLock()
	cached, ok := s.bySiteKeys[siteID]
	generation := s.generation
	s.mu.RUnlock()
	if ok {
		return cloneCredentialSlice(cached), nil
	}
	rows, err := s.db.Query(`
		SELECT id, site_id, kind, auth_mode, secret_enc, cookie_enc, meta_json, status, checkin_enabled,
		       COALESCE(import_fingerprint, ''), COALESCE(models_csv, ''), created_at, updated_at
		FROM credentials
		WHERE site_id = ?
		  AND status = 'enabled'
		  AND secret_enc <> ''
		  AND lower(kind) = 'api_key'
		ORDER BY id`, siteID)
	if err != nil {
		return nil, fmt.Errorf("credential api key pool list: %w", err)
	}
	defer rows.Close()

	var result []domain.Credential
	for rows.Next() {
		var row domain.Credential
		var secret, cookie string
		if err := rows.Scan(
			&row.ID, &row.SiteID, &row.Kind, &row.AuthMode, &secret, &cookie, &row.MetaJSON, &row.Status,
			&row.CheckinEnabled, &row.ImportFingerprint, &row.ModelsCSV, scanTime(&row.CreatedAt), scanTime(&row.UpdatedAt),
		); err != nil {
			return nil, fmt.Errorf("credential api key pool scan: %w", err)
		}
		row.SecretEnc = []byte(secret)
		row.CookieEnc = []byte(cookie)
		result = append(result, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("credential api key pool rows: %w", err)
	}
	s.cacheSiteKeysIfGeneration(siteID, result, generation)
	return result, nil
}

func (s *CredentialStore) cacheSiteKeysIfGeneration(siteID int64, credentials []domain.Credential, generation uint64) {
	s.mu.Lock()
	if s.generation == generation {
		s.bySiteKeys[siteID] = cloneCredentialSlice(credentials)
	}
	s.mu.Unlock()
}

// cloneModelSets deep-copies a per-site model-set cache entry.
func cloneModelSets(in map[int64]map[string]struct{}) map[int64]map[string]struct{} {
	if in == nil {
		return nil
	}
	out := make(map[int64]map[string]struct{}, len(in))
	for id, set := range in {
		if set == nil {
			continue
		}
		cloned := make(map[string]struct{}, len(set))
		for name := range set {
			cloned[name] = struct{}{}
		}
		out[id] = cloned
	}
	return out
}

// cloneCredentialSlice deep-copies a credential slice for cache storage.
func cloneCredentialSlice(in []domain.Credential) []domain.Credential {
	if in == nil {
		return nil
	}
	out := make([]domain.Credential, len(in))
	for i := range in {
		out[i] = *cloneCredential(&in[i])
	}
	return out
}

// ModelSetByCredential returns the discovered model set for one credential
// (map keyed by model name). Empty when none recorded yet.
func (s *CredentialStore) ModelSetByCredential(id int64) (map[string]struct{}, error) {
	rows, err := s.db.Query(`SELECT model_name FROM credential_models WHERE credential_id = ?`, id)
	if err != nil {
		return nil, fmt.Errorf("credential model set: %w", err)
	}
	defer rows.Close()
	set := make(map[string]struct{})
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("credential model set scan: %w", err)
		}
		set[name] = struct{}{}
	}
	return set, rows.Err()
}

// ModelSetsBySite returns per-credential discovered model sets for every
// enabled API key on the site. Read on the relay path, so results are cached
// per site and invalidated by any credential write (same generation as the
// key pool). An absent entry means "no set recorded yet".
func (s *CredentialStore) ModelSetsBySite(siteID int64) (map[int64]map[string]struct{}, error) {
	s.mu.RLock()
	cached, ok := s.bySiteModels[siteID]
	generation := s.generation
	s.mu.RUnlock()
	if ok {
		return cloneModelSets(cached), nil
	}
	rows, err := s.db.Query(`
		SELECT c.id, c.site_id, c.kind, c.auth_mode, c.secret_enc, c.cookie_enc, c.meta_json, c.status, c.checkin_enabled,
		       COALESCE(c.import_fingerprint, ''), COALESCE(c.models_csv, ''), c.created_at, c.updated_at,
		       m.model_name
		FROM credentials c
		LEFT JOIN credential_models m ON m.credential_id = c.id
		WHERE c.site_id = ?
		  AND c.status = 'enabled'
		  AND c.secret_enc <> ''
		  AND lower(c.kind) = 'api_key'
		ORDER BY c.id, m.model_name`, siteID)
	if err != nil {
		return nil, fmt.Errorf("credential model sets list: %w", err)
	}
	defer rows.Close()
	result := make(map[int64]map[string]struct{})
	for rows.Next() {
		var r domain.Credential
		var secret, cookie string
		var model sql.NullString
		if err := rows.Scan(&r.ID, &r.SiteID, &r.Kind, &r.AuthMode, &secret, &cookie, &r.MetaJSON, &r.Status, &r.CheckinEnabled, &r.ImportFingerprint, &r.ModelsCSV, scanTime(&r.CreatedAt), scanTime(&r.UpdatedAt), &model); err != nil {
			return nil, fmt.Errorf("credential model sets scan: %w", err)
		}
		if model.Valid {
			set, ok := result[r.ID]
			if !ok {
				set = make(map[string]struct{})
				result[r.ID] = set
			}
			set[model.String] = struct{}{}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	if s.generation == generation {
		s.bySiteModels[siteID] = cloneModelSets(result)
	}
	s.mu.Unlock()
	return result, nil
}

// InvalidateModelSetFor drops the cached per-site model sets for one credential
// (used after discovery rewrites a credential's recorded models).
func (s *CredentialStore) InvalidateModelSetFor(credentialID int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if cred, ok := s.byID[credentialID]; ok {
		delete(s.bySiteModels, cred.SiteID)
	} else {
		// Unknown credential: clear conservatively (the site relation is not cached).
		for siteID := range s.bySiteModels {
			delete(s.bySiteModels, siteID)
		}
	}
}

func (s *CredentialStore) GetByID(id int64) (*domain.Credential, error) {
	var generation uint64
	if id > 0 {
		s.mu.RLock()
		cached, ok := s.byID[id]
		generation = s.generation
		s.mu.RUnlock()
		if ok {
			return cloneCredential(cached), nil
		}
	}
	row := s.db.QueryRow(`SELECT id, site_id, kind, auth_mode, secret_enc, cookie_enc, meta_json, status, checkin_enabled, COALESCE(import_fingerprint, ''), COALESCE(models_csv, ''), created_at, updated_at FROM credentials WHERE id = ?`, id)
	var r domain.Credential
	var secret, cookie string
	if err := row.Scan(&r.ID, &r.SiteID, &r.Kind, &r.AuthMode, &secret, &cookie, &r.MetaJSON, &r.Status, &r.CheckinEnabled, &r.ImportFingerprint, &r.ModelsCSV, scanTime(&r.CreatedAt), scanTime(&r.UpdatedAt)); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("credential get: %w", err)
	}
	r.SecretEnc = []byte(secret)
	r.CookieEnc = []byte(cookie)
	s.cachePutIfGeneration(&r, generation)
	return &r, nil
}

func (s *CredentialStore) Create(c *domain.Credential) (int64, error) {
	res, err := s.db.Exec(`INSERT INTO credentials (site_id, kind, auth_mode, secret_enc, cookie_enc, meta_json, status, checkin_enabled, import_fingerprint, models_csv) VALUES (?, ?, ?, ?, ?, ?, ?, ?, NULLIF(?, ''), ?)`,
		c.SiteID, c.Kind, normalizeAuthMode(c.AuthMode), string(c.SecretEnc), string(c.CookieEnc), c.MetaJSON, c.Status, c.CheckinEnabled, c.ImportFingerprint, c.ModelsCSV)
	if err != nil {
		return 0, fmt.Errorf("credential create: %w", err)
	}
	id, err := res.LastInsertId()
	if err == nil {
		c.ID = id
		c.AuthMode = normalizeAuthMode(c.AuthMode)
		s.invalidate(id)
		_, _ = s.GetByID(id)
	}
	return id, err
}

func (s *CredentialStore) SetCheckinEnabled(id int64, enabled bool) error {
	result, err := s.db.Exec(`UPDATE credentials SET checkin_enabled=?, updated_at=datetime('now') WHERE id=?`, enabled, id)
	if err != nil {
		return fmt.Errorf("credential checkin update: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("credential checkin rows: %w", err)
	}
	if n == 0 {
		return sql.ErrNoRows
	}
	s.invalidate(id)
	return nil
}

func (s *CredentialStore) ListCheckinEnabled() ([]domain.Credential, error) {
	rows, err := s.db.Query(`SELECT id, site_id, kind, auth_mode, secret_enc, cookie_enc, meta_json, status, checkin_enabled, COALESCE(import_fingerprint, ''), COALESCE(models_csv, ''), created_at, updated_at FROM credentials WHERE checkin_enabled = 1 AND status = 'enabled' AND lower(kind) IN ('session', 'access_token') AND (secret_enc <> '' OR cookie_enc <> '') ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("credential checkin list: %w", err)
	}
	defer rows.Close()
	var result []domain.Credential
	for rows.Next() {
		var r domain.Credential
		var secret, cookie string
		if err := rows.Scan(&r.ID, &r.SiteID, &r.Kind, &r.AuthMode, &secret, &cookie, &r.MetaJSON, &r.Status, &r.CheckinEnabled, &r.ImportFingerprint, &r.ModelsCSV, scanTime(&r.CreatedAt), scanTime(&r.UpdatedAt)); err != nil {
			return nil, fmt.Errorf("credential checkin scan: %w", err)
		}
		r.SecretEnc = []byte(secret)
		r.CookieEnc = []byte(cookie)
		result = append(result, r)
	}
	return result, rows.Err()
}

func (s *CredentialStore) Update(c *domain.Credential) error {
	_, err := s.db.Exec(`UPDATE credentials SET kind=?, auth_mode=?, secret_enc=?, cookie_enc=?, meta_json=?, status=?, models_csv=?, import_fingerprint=NULLIF(?, ''), updated_at=datetime('now') WHERE id=?`,
		c.Kind, normalizeAuthMode(c.AuthMode), string(c.SecretEnc), string(c.CookieEnc), c.MetaJSON, c.Status, c.ModelsCSV, c.ImportFingerprint, c.ID)
	if err != nil {
		return fmt.Errorf("credential update: %w", err)
	}
	c.AuthMode = normalizeAuthMode(c.AuthMode)
	s.invalidate(c.ID)
	_, _ = s.GetByID(c.ID)
	return nil
}

func normalizeAuthMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "cookie", "auto":
		return strings.ToLower(strings.TrimSpace(mode))
	default:
		return "access_token"
	}
}

func (s *CredentialStore) Delete(id int64) error {
	_, err := s.db.Exec(`DELETE FROM credentials WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("credential delete: %w", err)
	}
	s.invalidate(id)
	return nil
}
