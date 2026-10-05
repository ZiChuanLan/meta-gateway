// Package webui serves the embedded Meta Gateway Admin application.
package webui

import (
	"embed"
	"io/fs"
	"net/http"

	"github.com/lan/meta-gateway/internal/spa"
)

//go:embed dist
var assets embed.FS

// Handler returns a handler for files beneath /console/ with SPA fallback.
func Handler() http.Handler {
	dist, err := fs.Sub(assets, "dist")
	if err != nil {
		panic("webui: embedded distribution unavailable")
	}
	return spa.Handler(dist, "/console/", "index.html")
}
