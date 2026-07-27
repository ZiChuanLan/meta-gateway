package adapters

import (
	"net/http"
	"strconv"
)

// CompatUserIDHeaderNames are the fan-out headers used by New-API family forks
// (aligned with All API Hub buildCompatUserIdHeaders, clean-room).
var CompatUserIDHeaderNames = []string{
	"New-Api-User",
	"New-API-User",
	"Veloera-User",
	"X-Api-User",
	"voapi-user",
	"User-id",
	"Rix-Api-User",
	"neo-api-user",
}

// ApplyCompatUserIDHeaders sets all known user-id headers when id > 0.
func ApplyCompatUserIDHeaders(header http.Header, platformUserID int64) {
	if header == nil || platformUserID <= 0 {
		return
	}
	value := strconv.FormatInt(platformUserID, 10)
	for _, name := range CompatUserIDHeaderNames {
		header.Set(name, value)
	}
}
