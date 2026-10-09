package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// SiteAnnouncement is one notice an upstream site published, as the gateway
// read it. It is a quotation: the site owns the text, the site id and the
// published timestamp; the gateway only adds when it first saw the row.
type SiteAnnouncement struct {
	ID       int64  `json:"id"`
	SiteID   int64  `json:"site_id"`
	SiteName string `json:"site_name"`
	// UpstreamID is the site's own announcement id, kept as text because a fork
	// may number them however it likes; it is what makes repeated fetches
	// idempotent.
	UpstreamID  string `json:"upstream_id"`
	Content     string `json:"content"`
	Extra       string `json:"extra,omitempty"`
	Kind        string `json:"kind,omitempty"`
	PublishedAt string `json:"published_at"`
	// FirstSeenAt is when this gateway first read the row, which is the only
	// "is it new to me" signal that does not depend on the site's clock.
	FirstSeenAt string `json:"first_seen_at"`
	FetchedAt   string `json:"fetched_at"`
}

const siteAnnouncementColumns = `a.id,a.site_id,COALESCE(s.name,''),a.upstream_id,a.content,
	a.extra,a.kind,a.published_at,a.first_seen_at,a.fetched_at`

func scanSiteAnnouncement(row interface{ Scan(...any) error }) (SiteAnnouncement, error) {
	var item SiteAnnouncement
	err := row.Scan(&item.ID, &item.SiteID, &item.SiteName, &item.UpstreamID, &item.Content,
		&item.Extra, &item.Kind, &item.PublishedAt, &item.FirstSeenAt, &item.FetchedAt)
	return item, err
}

// UpsertSiteAnnouncements stores what one site published, in one transaction.
//
// It returns how many rows were new to this gateway: a site republishes its
// whole board on every call, so "rows written" would always equal "rows read"
// and say nothing about whether anything happened.
func (db *DB) UpsertSiteAnnouncements(siteID int64, items []SiteAnnouncement, now time.Time) (int, error) {
	if siteID <= 0 || len(items) == 0 {
		return 0, nil
	}
	stamp := now.UTC().Format(time.RFC3339Nano)
	tx, err := db.Begin()
	if err != nil {
		return 0, fmt.Errorf("site news: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	known := make(map[string]bool, len(items))
	rows, err := tx.Query(`SELECT upstream_id FROM site_announcements WHERE site_id=?`, siteID)
	if err != nil {
		return 0, fmt.Errorf("site news: read known ids: %w", err)
	}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return 0, fmt.Errorf("site news: scan known id: %w", err)
		}
		known[id] = true
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return 0, fmt.Errorf("site news: read known ids: %w", err)
	}
	_ = rows.Close()

	added := 0
	for _, item := range items {
		upstreamID := strings.TrimSpace(item.UpstreamID)
		content := strings.TrimSpace(item.Content)
		if upstreamID == "" || content == "" {
			// An announcement with no id cannot be deduped, and one with no text
			// has nothing to show: skipping is better than a row that changes
			// identity on every fetch.
			continue
		}
		published := strings.TrimSpace(item.PublishedAt)
		if !known[upstreamID] {
			added++
		}
		// first_seen_at is written on insert only — it is the one column a later
		// fetch must not touch. published_at falls back to the fetch stamp on
		// insert, but on conflict an empty reading keeps what is stored: `?` is
		// bound twice because the VALUES clause and the update branch need to tell
		// "no readable date" apart from a real one.
		if _, err := tx.Exec(
			`INSERT INTO site_announcements
			   (site_id,upstream_id,content,extra,kind,published_at,first_seen_at,fetched_at)
			 VALUES (?,?,?,?,?,COALESCE(NULLIF(?,''),?),?,?)
			 ON CONFLICT(site_id, upstream_id) DO UPDATE SET
			   content=excluded.content, extra=excluded.extra, kind=excluded.kind,
			   published_at=CASE WHEN ?='' THEN site_announcements.published_at ELSE excluded.published_at END,
			   fetched_at=excluded.fetched_at`,
			siteID, upstreamID, content, strings.TrimSpace(item.Extra), strings.TrimSpace(item.Kind),
			published, stamp, stamp, stamp, published,
		); err != nil {
			return 0, fmt.Errorf("site news: upsert announcement %s: %w", upstreamID, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("site news: commit: %w", err)
	}
	return added, nil
}

// SiteAnnouncements lists the feed, newest published first. A site that
// republishes an old announcement keeps its original published_at, so the order
// stays the order a reader expects.
func (db *DB) SiteAnnouncements(limit int) ([]SiteAnnouncement, error) {
	if limit <= 0 {
		limit = 30
	}
	rows, err := db.Query(`SELECT `+siteAnnouncementColumns+`
		FROM site_announcements a LEFT JOIN sites s ON s.id = a.site_id
		ORDER BY a.published_at DESC, a.id DESC
		LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("site news: list: %w", err)
	}
	defer rows.Close()
	out := []SiteAnnouncement{}
	for rows.Next() {
		item, err := scanSiteAnnouncement(rows)
		if err != nil {
			return nil, fmt.Errorf("site news: scan: %w", err)
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

// SiteNewsStatus reports what the feed can cover: how many sites publish a
// notice board the gateway can read, how many of them have said something, and
// when the gateway last read any of them.
func (db *DB) SiteNewsStatus() (reported int, lastFetched string, err error) {
	row := db.QueryRow(`SELECT COUNT(DISTINCT site_id), COALESCE(MAX(fetched_at),'') FROM site_announcements`)
	if err := row.Scan(&reported, &lastFetched); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, "", fmt.Errorf("site news: status: %w", err)
	}
	return reported, lastFetched, nil
}
