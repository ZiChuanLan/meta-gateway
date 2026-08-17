package store

import (
	"database/sql"
	"fmt"
	"sync"

	"github.com/lan/meta-gateway/internal/domain"
)

// SiteStore provides CRUD operations for sites.
//
// Sites are near-static configuration read on every relay attempt
// (resolveForward / resolveUpstreamURL), so reads are served from an
// in-process cache invalidated by every write path.
type SiteStore struct {
	db *sql.DB
	// onDelete is wired by DB so cascaded credentials cannot remain in the
	// credential hot-path cache after a site is removed.
	onDelete func()

	mu   sync.RWMutex
	byID map[int64]*domain.Site
	// generation changes after every durable write/cache clear. A cache miss
	// may only publish the row it read while this value is unchanged; otherwise
	// an older query could repopulate a value that a concurrent write removed.
	generation uint64
}

func newSiteStore(db *sql.DB) *SiteStore {
	return &SiteStore{db: db, byID: make(map[int64]*domain.Site)}
}

// ClearCache drops every cached site (used after bulk imports that write
// sites outside this store).
func (s *SiteStore) ClearCache() {
	s.mu.Lock()
	s.byID = make(map[int64]*domain.Site)
	s.generation++
	s.mu.Unlock()
}

func (s *SiteStore) cachePutIfGeneration(site *domain.Site, generation uint64) {
	if site == nil || site.ID <= 0 {
		return
	}
	cloned := *site
	s.mu.Lock()
	if s.generation == generation {
		s.byID[cloned.ID] = &cloned
	}
	s.mu.Unlock()
}

func (s *SiteStore) invalidate(id int64) {
	s.mu.Lock()
	delete(s.byID, id)
	s.generation++
	s.mu.Unlock()
}

func (s *SiteStore) List() ([]domain.Site, error) {
	rows, err := s.db.Query(`SELECT id, name, base_url, platform, status, created_at, updated_at FROM sites ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("site list: %w", err)
	}
	defer rows.Close()

	var result []domain.Site
	for rows.Next() {
		var r domain.Site
		if err := rows.Scan(&r.ID, &r.Name, &r.BaseURL, &r.Platform, &r.Status, scanTime(&r.CreatedAt), scanTime(&r.UpdatedAt)); err != nil {
			return nil, fmt.Errorf("site list scan: %w", err)
		}
		result = append(result, r)
	}
	return result, rows.Err()
}

func (s *SiteStore) GetByID(id int64) (*domain.Site, error) {
	var generation uint64
	if id > 0 {
		s.mu.RLock()
		cached, ok := s.byID[id]
		generation = s.generation
		s.mu.RUnlock()
		if ok {
			cloned := *cached
			return &cloned, nil
		}
	}
	row := s.db.QueryRow(`SELECT id, name, base_url, platform, status, created_at, updated_at FROM sites WHERE id = ?`, id)
	var r domain.Site
	if err := row.Scan(&r.ID, &r.Name, &r.BaseURL, &r.Platform, &r.Status, scanTime(&r.CreatedAt), scanTime(&r.UpdatedAt)); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("site get: %w", err)
	}
	s.cachePutIfGeneration(&r, generation)
	return &r, nil
}

func (s *SiteStore) Create(site *domain.Site) (int64, error) {
	res, err := s.db.Exec(`INSERT INTO sites (name, base_url, platform, status) VALUES (?, ?, ?, ?)`,
		site.Name, site.BaseURL, site.Platform, site.Status)
	if err != nil {
		return 0, fmt.Errorf("site create: %w", err)
	}
	id, err := res.LastInsertId()
	if err == nil {
		site.ID = id
		s.invalidate(id)
		_, _ = s.GetByID(id)
	}
	return id, err
}

func (s *SiteStore) Update(site *domain.Site) error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("site update begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.Exec(`UPDATE sites SET name=?, base_url=?, platform=?, status=?, updated_at=datetime('now') WHERE id=?`,
		site.Name, site.BaseURL, site.Platform, site.Status, site.ID); err != nil {
		return fmt.Errorf("site update: %w", err)
	}
	// Metapi-style cascade: disabling a site disables all of its enabled
	// channels (a dead site's channels must not linger in the routing pool).
	if site.Status == domain.StatusDisabled {
		if _, err = tx.Exec(`UPDATE channels SET status=?, updated_at=datetime('now') WHERE site_id=? AND status=?`,
			domain.StatusDisabled, site.ID, domain.StatusEnabled); err != nil {
			return fmt.Errorf("site update cascade channels: %w", err)
		}
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("site update commit: %w", err)
	}
	s.invalidate(site.ID)
	_, _ = s.GetByID(site.ID)
	return nil
}

func (s *SiteStore) Delete(id int64) error {
	// Channels cascade from sites (ON DELETE CASCADE).
	// Empty routes without remaining members are cleaned to avoid ghost models.
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("site delete begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`DELETE FROM sites WHERE id = ?`, id); err != nil {
		return fmt.Errorf("site delete: %w", err)
	}
	if _, err := tx.Exec(`DELETE FROM routes WHERE id NOT IN (SELECT DISTINCT route_id FROM route_members)`); err != nil {
		return fmt.Errorf("site delete empty routes: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("site delete commit: %w", err)
	}
	s.invalidate(id)
	if s.onDelete != nil {
		s.onDelete()
	}
	return nil
}
