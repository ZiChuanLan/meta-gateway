package imgproto

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/url"
	"path"
	"strings"
)

// MaxInlineImageBytes caps the payload we are willing to embed in a chat
// response. Larger images keep their remote URL instead of inflating the
// answer with base64; the caller still receives a usable link.
const MaxInlineImageBytes = 12 << 20

// Fetcher loads a remote image for inlining. The implementation lives outside
// this package so every egress keeps going through the policy-enforced
// outbound client rather than a fresh socket.
type Fetcher func(ctx context.Context, rawURL string) (data []byte, contentType string, err error)

// InlineImages replaces remote http(s) image references with data URIs so a
// chat client can render an edit without reaching the upstream host itself.
// The upstream answers with links on its own domain, which a client configured
// for the gateway has no route or credential for; embedding the bytes removes
// that dependency.
//
// Failures are never fatal: when a fetch fails, returns a non-image, or the
// payload exceeds MaxInlineImageBytes, the original URL is kept.
func InlineImages(ctx context.Context, images []ImageOut, fetch Fetcher, maxBytes int64) []ImageOut {
	if len(images) == 0 || fetch == nil {
		return images
	}
	if maxBytes <= 0 {
		maxBytes = MaxInlineImageBytes
	}
	out := make([]ImageOut, len(images))
	copy(out, images)
	for index := range out {
		image := &out[index]
		if image.DataURL != "" || !inlineCandidate(image.URL) {
			continue
		}
		data, contentType, err := fetch(ctx, image.URL)
		if err != nil || len(data) == 0 || int64(len(data)) > maxBytes {
			continue
		}
		mime := imageMime(contentType, data, image.URL)
		if mime == "" {
			continue
		}
		image.DataURL = "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(data)
	}
	return out
}

// inlineCandidate reports whether the reference is an absolute http(s) URL we
// are willing to fetch. data URIs are already self-contained.
func inlineCandidate(raw string) bool {
	value := strings.TrimSpace(raw)
	if value == "" {
		return false
	}
	parsed, err := url.Parse(value)
	if err != nil {
		return false
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return false
	}
	return parsed.Host != ""
}

// imageMime prefers the declared content type, then sniffs the bytes, and only
// falls back to the URL's extension. A response that is not demonstrably an
// image is refused so a redirect to HTML never becomes a data URI.
func imageMime(contentType string, data []byte, sourceURL string) string {
	if mime := normalizeMime(contentType); mime != "" {
		return mime
	}
	if mime := normalizeMime(http.DetectContentType(data)); mime != "" {
		return mime
	}
	return mimeFromExtension(sourceURL)
}

func normalizeMime(value string) string {
	if index := strings.IndexByte(value, ';'); index >= 0 {
		value = value[:index]
	}
	value = strings.ToLower(strings.TrimSpace(value))
	if !strings.HasPrefix(value, "image/") {
		return ""
	}
	if value == "image/svg+xml" {
		// SVG carries script and is not something we inline blindly.
		return ""
	}
	return value
}

func mimeFromExtension(sourceURL string) string {
	parsed, err := url.Parse(strings.TrimSpace(sourceURL))
	if err != nil {
		return ""
	}
	switch strings.ToLower(path.Ext(parsed.Path)) {
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	case ".webp":
		return "image/webp"
	case ".gif":
		return "image/gif"
	default:
		return ""
	}
}
