package store

import (
	"database/sql"
	"errors"
	"strings"
	"time"
)

// Announcement is one line of the deployment's own voice: something the operator
// wants the people using this gateway to know.
//
// It exists because the alternative was for every member to be told nothing —
// a channel going down for maintenance, a price change, a model arriving only
// showed up as a sudden failure in their own logs.
type Announcement struct {
	ID        int64  `json:"id"`
	Title     string `json:"title"`
	Body      string `json:"body"`
	Tone      string `json:"tone"`
	Pinned    bool   `json:"pinned"`
	Enabled   bool   `json:"enabled"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

// ErrAnnouncementNotFound is returned by Update/Delete for a missing row so the
// handler can answer 404 instead of reporting a database failure.
var ErrAnnouncementNotFound = errors.New("announcement not found")

const announcementColumns = `id,title,body,tone,pinned,enabled,created_at,updated_at`

func scanAnnouncement(row interface{ Scan(...any) error }) (Announcement, error) {
	var a Announcement
	err := row.Scan(&a.ID, &a.Title, &a.Body, &a.Tone, &a.Pinned, &a.Enabled, &a.CreatedAt, &a.UpdatedAt)
	return a, err
}

func normalizeAnnouncementTone(tone string) string {
	switch strings.ToLower(strings.TrimSpace(tone)) {
	case "warn", "warning":
		return "warn"
	default:
		return "info"
	}
}

// Announcements lists every announcement, pinned first and newest first within
// each group. includeDisabled is the operator's view; members get only the
// enabled ones.
func (db *DB) Announcements(includeDisabled bool) ([]Announcement, error) {
	query := `SELECT ` + announcementColumns + ` FROM announcements`
	if !includeDisabled {
		query += ` WHERE enabled=1`
	}
	query += ` ORDER BY pinned DESC, id DESC`
	rows, err := db.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Announcement{}
	for rows.Next() {
		a, err := scanAnnouncement(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// SaveAnnouncement creates (ID 0) or updates an announcement.
func (db *DB) SaveAnnouncement(a Announcement) (Announcement, error) {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	a.Title = strings.TrimSpace(a.Title)
	a.Body = strings.TrimSpace(a.Body)
	a.Tone = normalizeAnnouncementTone(a.Tone)
	if a.ID == 0 {
		res, err := db.Exec(
			`INSERT INTO announcements (title,body,tone,pinned,enabled,created_at,updated_at)
			 VALUES (?,?,?,?,?,?,?)`,
			a.Title, a.Body, a.Tone, a.Pinned, a.Enabled, now, now,
		)
		if err != nil {
			return a, err
		}
		id, err := res.LastInsertId()
		if err != nil {
			return a, err
		}
		a.ID, a.CreatedAt, a.UpdatedAt = id, now, now
		return a, nil
	}
	res, err := db.Exec(
		`UPDATE announcements SET title=?,body=?,tone=?,pinned=?,enabled=?,updated_at=? WHERE id=?`,
		a.Title, a.Body, a.Tone, a.Pinned, a.Enabled, now, a.ID,
	)
	if err != nil {
		return a, err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return a, err
	}
	if affected == 0 {
		return a, ErrAnnouncementNotFound
	}
	a.UpdatedAt = now
	stored, err := db.announcementByID(a.ID)
	if err != nil {
		return a, err
	}
	return stored, nil
}

// DeleteAnnouncement removes one announcement, reporting a missing row as such.
func (db *DB) DeleteAnnouncement(id int64) error {
	res, err := db.Exec(`DELETE FROM announcements WHERE id=?`, id)
	if err != nil {
		return err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return ErrAnnouncementNotFound
	}
	return nil
}

func (db *DB) announcementByID(id int64) (Announcement, error) {
	a, err := scanAnnouncement(db.QueryRow(`SELECT `+announcementColumns+` FROM announcements WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return a, ErrAnnouncementNotFound
	}
	return a, err
}
