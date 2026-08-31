// Package buildinfo carries the gateway's identity, injected at link time:
//
//	go build -ldflags="-X github.com/lan/meta-gateway/internal/buildinfo.Version=v0.1.0 ..."
//
// Unset values degrade to "dev" so source builds stay truthful.
package buildinfo

var (
	// Version is the release tag (e.g. "v0.1.0") or "dev".
	Version = "dev"
	// Commit is the short git SHA the binary was built from.
	Commit = "unknown"
)
