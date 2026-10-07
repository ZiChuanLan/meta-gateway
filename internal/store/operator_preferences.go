package store

import "strings"

type OperatorPreferences struct {
	AdminUsername string `json:"admin_username"`
	// UpdateChannel is the column the console's channel switch used to write.
	// That switch is gone: the channel a deployment receives is its own image
	// tag (see selfupdate.TrackingChannel), and the update check now reads it
	// from there instead of from a preference the console could set. The column
	// stays so an older row is not lost and a rollback does not have to migrate.
	UpdateChannel string `json:"update_channel"`
}

func (db *DB) OperatorPreferences() (OperatorPreferences, error) {
	var p OperatorPreferences
	err := db.QueryRow(`SELECT admin_username,update_channel FROM operator_preferences WHERE id=1`).Scan(&p.AdminUsername, &p.UpdateChannel)
	return p, err
}
func (db *DB) OperatorUsername(fallback string) (string, error) {
	p, err := db.OperatorPreferences()
	if err != nil {
		return "", err
	}
	if p.AdminUsername != "" {
		return p.AdminUsername, nil
	}
	fallback = strings.ToLower(strings.TrimSpace(fallback))
	if fallback == "" {
		fallback = "admin"
	}
	return fallback, nil
}
