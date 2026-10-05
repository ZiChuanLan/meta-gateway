// Package spa serves a go:embed-ed single-page application bundle.
//
// The admin console uses it for SPA fallback, path normalization, cache policy
// and method handling. Keeping these independent of go:embed makes them
// testable with an in-memory filesystem.
package spa

import (
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strings"
)

// Handler serves files from fsys for requests beneath prefix, which must end
// with "/". shell names the SPA entry document inside fsys; it is a parameter
// because the bundler names the output after its source file.
//
// Unknown paths without a file extension fall back to the shell so that
// client-side routes survive a reload. Unknown paths that look like assets
// (i.e. carry an extension) stay 404s rather than being answered with HTML,
// which would turn a missing stylesheet into a confusing MIME error.
func Handler(fsys fs.FS, prefix, shell string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		name := strings.TrimPrefix(r.URL.Path, prefix)
		name = strings.TrimPrefix(path.Clean("/"+name), "/")
		if name == "." || name == "" {
			name = shell
		}
		file, statErr := fs.Stat(fsys, name)
		if statErr != nil || file.IsDir() {
			if path.Ext(name) != "" {
				http.NotFound(w, r)
				return
			}
			name = shell
		}
		content, readErr := fs.ReadFile(fsys, name)
		if readErr != nil {
			http.Error(w, "web UI unavailable", http.StatusInternalServerError)
			return
		}
		if contentType := mime.TypeByExtension(path.Ext(name)); contentType != "" {
			w.Header().Set("Content-Type", contentType)
		}
		if name == shell {
			// The shell must never be cached: it names the hashed asset files,
			// so a stale shell points at assets that no longer exist.
			w.Header().Set("Cache-Control", "no-cache")
		} else {
			// Everything else is content-addressed by the bundler.
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.WriteHeader(http.StatusOK)
		if r.Method == http.MethodGet {
			_, _ = w.Write(content)
		}
	})
}
