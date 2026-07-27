package webdavsync

import "fmt"

const (
	CategoryConfigIncomplete = "config_incomplete"
	CategoryValidation       = "validation_error"
	CategoryOutboundBlocked  = "outbound_blocked"
	CategoryAuthFailed       = "auth_failed"
	CategoryNotFound         = "not_found"
	CategoryTooLarge         = "body_too_large"
	CategoryDecryptFailed    = "decrypt_failed"
	CategoryInvalidBackup    = "invalid_backup"
	CategoryImportFailed     = "import_failed"
	CategoryBusy             = "sync_in_progress"
	CategoryUpstream         = "upstream_failure"
	CategoryInternal         = "internal_error"
)

// Error is a stable, redacted failure for admin API and logs.
type Error struct {
	Category string
	Message  string
}

func (e Error) Error() string {
	if e.Message == "" {
		return e.Category
	}
	return fmt.Sprintf("%s: %s", e.Category, e.Message)
}

func (e Error) Is(target error) bool {
	other, ok := target.(Error)
	if !ok {
		return false
	}
	return e.Category == other.Category
}
