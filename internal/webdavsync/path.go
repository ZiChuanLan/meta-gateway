package webdavsync

import (
	"net/url"
	"path"
	"strings"
)

const (
	aahBackupFolderName = "all-api-hub-backup"
	aahBackupFileName   = "all-api-hub-1-0.json"
)

// ResolveBackupURL mirrors AAH ensureFilename: directory-style settings expand
// to the deterministic default backup file under all-api-hub-backup/.
func ResolveBackupURL(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", Error{Category: CategoryConfigIncomplete, Message: "webdav url is required"}
	}
	parsed, err := url.Parse(trimmed)
	if err != nil || !parsed.IsAbs() || parsed.Host == "" || parsed.User != nil {
		return "", Error{Category: CategoryValidation, Message: "webdav url is invalid"}
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", Error{Category: CategoryValidation, Message: "webdav url must be http or https"}
	}
	if isExplicitJSONFileURL(parsed) {
		return parsed.String(), nil
	}
	// Directory-style: append default AAH backup relative path.
	basePath := parsed.Path
	if basePath == "" {
		basePath = "/"
	}
	if !strings.HasSuffix(basePath, "/") {
		basePath += "/"
	}
	parsed.Path = path.Join(basePath, aahBackupFolderName, aahBackupFileName)
	// path.Join cleans trailing slashes; ensure leading slash for absolute path.
	if !strings.HasPrefix(parsed.Path, "/") {
		parsed.Path = "/" + parsed.Path
	}
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String(), nil
}

func isExplicitJSONFileURL(parsed *url.URL) bool {
	if parsed == nil {
		return false
	}
	cleanPath := strings.ToLower(parsed.Path)
	if strings.HasSuffix(cleanPath, ".json") {
		return true
	}
	// Fall back to raw string when path parse is odd but URL ends with .json.
	return strings.HasSuffix(strings.ToLower(parsed.String()), ".json")
}

// RedactedURL returns a log/API-safe URL without userinfo.
func RedactedURL(raw string) string {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Host == "" {
		return ""
	}
	parsed.User = nil
	return parsed.String()
}
