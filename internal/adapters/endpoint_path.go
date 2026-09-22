package adapters

import (
	"errors"
	"net/url"
	"strings"
)

// JoinRawPath joins a base URL with a path WITHOUT inserting a /v1 segment.
//
// The OpenAI-compatible passthrough adapter joins <base>/v1/<path>, which is
// wrong for a provider whose API root is already something else: the Zhipu base
// `https://open.bigmodel.cn/api/paas/v4` would become the non-existent
// `/api/paas/v4/v1/...`. `JoinOpenAIPath` deliberately owns that /v1 rule, so
// this is the separate join used when a channel has an explicit endpoint mapping
// and therefore knows its own path layout.
//
// The base's own path is kept and the mapped path is appended verbatim. A path
// that repeats the base's last segment is not duplicated, so `base=…/v1` +
// `path=v1/models` and `base=…/v1` + `path=/models` both yield `…/v1/models` —
// the two ways an operator writes the same thing behave the same.
// SplitEndpointBaseURL splits a base URL that already carries its own endpoint
// path into a clean root plus an explicit endpoint override.
//
// It exists because operators coming from new-api's "Custom" channel type write
// the WHOLE upstream endpoint as the base URL
// (`https://api.typesafe.ai/v1/systemone`) instead of a root plus a separate
// endpoint field. new-api accepts that because it concatenates base + request
// path verbatim; the gateway builds `<base>/v1/<path>`, so the same input would
// become `/v1/systemone/v1/chat/completions`. Splitting at save time keeps the
// operator's one-field habit while the relay keeps its ordinary endpoint rule.
//
// A path whose last segment looks like an API version root (`/v1`, `/api/v3`,
// `/api/paas/v4`, `/v1beta`, `/openai/v1` …) is NOT an endpoint: those bases are
// already roots, and every existing cn/intl provider presets one this way
// (Zhipu, Doubao, Qianfan, Groq…). Splitting them would relocate the whole API
// root and silently break every path on the channel.
//
// The returned override is always in the absolute form (leading slash), so the
// relay appends it to the bare host instead of guessing a `/v1` slot.
func SplitEndpointBaseURL(rawBaseURL string) (base string, override string, err error) {
	trimmed := strings.TrimSpace(rawBaseURL)
	parsed, parseErr := url.Parse(trimmed)
	if parseErr != nil || !parsed.IsAbs() || parsed.Host == "" || parsed.User != nil ||
		(parsed.Scheme != "http" && parsed.Scheme != "https") {
		return "", "", errors.New("invalid base URL")
	}
	path := strings.Trim(parsed.Path, "/")
	if path == "" || isAPIRootPath(path) {
		return strings.TrimRight(trimmed, "/"), "", nil
	}
	parsed.Path = ""
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return strings.TrimRight(parsed.String(), "/"), "/" + path, nil
}

// isAPIRootPath reports whether a base URL path names an API root rather than a
// single endpoint. The rule is deliberately narrow (a version-looking last
// segment) so that only an unambiguous endpoint is ever split off.
func isAPIRootPath(path string) bool {
	segments := strings.Split(strings.Trim(path, "/"), "/")
	last := segments[len(segments)-1]
	if last == "" {
		return true
	}
	// v1, v2, v1beta, v1alpha, v4 …
	if len(last) >= 2 && (last[0] == 'v' || last[0] == 'V') && last[1] >= '0' && last[1] <= '9' {
		return true
	}
	return false
}

// SafeURL renders a URL for logs, response headers and audit events: scheme,
// host and path only. Query strings and fragments are dropped because they
// routinely carry credentials (`?api_key=…`, `?key=…`), and embedded userinfo is
// dropped for the same reason. sub2api's safeUpstreamURL makes the same cut
// before writing an upstream URL into a log or an event.
func SafeURL(rawURL string) string {
	trimmed := strings.TrimSpace(rawURL)
	if trimmed == "" {
		return ""
	}
	parsed, err := url.Parse(trimmed)
	if err != nil || parsed.Host == "" {
		return ""
	}
	parsed.RawQuery = ""
	parsed.Fragment = ""
	parsed.User = nil
	return parsed.String()
}

// IsSafeURLPathSuffix reports whether a "/a/b" style path can be appended to an
// upstream base URL. It is a closed allowlist (default-deny), not a denylist of
// known-bad characters: only ASCII word characters plus '-' and '.' are allowed
// per segment, at most eight segments of at most 128 bytes, and no dots-only
// segment. Any percent-escape is therefore rejected too, because '%' is not in
// the list — verified against the router, which hands the escaped form over
// unchanged rather than decoding it first (`/v1/%2E%2E%2Fsystemone` → 404, never
// an outbound request).
//
// It exists for the same reason sub2api's upstream_path_guard.go does: the
// gateway concatenates a client- or rule-controlled string into an upstream URL,
// and that string must not be able to change the URL's structure (no traversal,
// no extra path, no query, no fragment).
func IsSafeURLPathSuffix(path string) bool {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return false
	}
	segments := strings.Split(strings.Trim(trimmed, "/"), "/")
	if len(segments) == 0 || len(segments) > 8 {
		return false
	}
	for _, segment := range segments {
		if segment == "" || len(segment) > 128 {
			return false
		}
		dotsOnly := true
		for i := 0; i < len(segment); i++ {
			b := segment[i]
			switch {
			case b >= 'a' && b <= 'z', b >= 'A' && b <= 'Z', b >= '0' && b <= '9', b == '_', b == '-':
				dotsOnly = false
			case b == '.':
			default:
				return false
			}
		}
		if dotsOnly {
			return false
		}
	}
	return true
}

// EndpointOverrideURL resolves a per-request endpoint override against a channel
// base URL.
//
// Two forms are accepted, in this order:
//
//	urlOverride   a complete URL. It must stay on the base's host, because the
//	              request will carry the channel's API key: allowing another host
//	              would let a downstream client exfiltrate the credential by
//	              naming its own server.
//	pathOverride  a path that replaces the base's path (same host, same scheme).
//
// An empty override returns the base unchanged. Anything invalid is an error, so
// the caller rejects the request instead of forwarding it somewhere unintended.
func EndpointOverrideURL(baseURL, pathOverride, urlOverride string) (string, error) {
	base, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || !base.IsAbs() || base.Host == "" {
		return "", errors.New("invalid base URL")
	}
	if trimmedURL := strings.TrimSpace(urlOverride); trimmedURL != "" {
		override, parseErr := url.Parse(trimmedURL)
		if parseErr != nil || !override.IsAbs() || override.Host == "" || override.User != nil {
			return "", errors.New("invalid upstream url override")
		}
		if !strings.EqualFold(override.Host, base.Host) || !strings.EqualFold(override.Scheme, base.Scheme) {
			return "", errors.New("upstream url override must stay on the channel host")
		}
		override.RawQuery = ""
		override.Fragment = ""
		return override.String(), nil
	}
	trimmedPath := strings.Trim(strings.TrimSpace(pathOverride), "/")
	if trimmedPath == "" {
		return base.String(), nil
	}
	if !IsSafeURLPathSuffix(trimmedPath) {
		return "", errors.New("invalid upstream path override")
	}
	base.Path = "/" + trimmedPath
	base.RawQuery = ""
	base.Fragment = ""
	return base.String(), nil
}

func JoinRawPath(baseURL, path string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || !parsed.IsAbs() || parsed.Host == "" || parsed.User != nil ||
		(parsed.Scheme != "http" && parsed.Scheme != "https") {
		return "", errors.New("invalid base URL")
	}
	parsed.RawQuery = ""
	parsed.Fragment = ""
	rel := strings.Trim(strings.TrimSpace(path), "/")
	if rel == "" {
		return "", errors.New("invalid upstream path")
	}
	basePath := strings.TrimRight(parsed.Path, "/")
	// Avoid a doubled leading segment: base "/v1" + path "v1/models".
	firstSegment := rel
	if idx := strings.IndexByte(rel, '/'); idx >= 0 {
		firstSegment = rel[:idx]
	}
	if basePath != "" && strings.EqualFold(basePath[strings.LastIndex(basePath, "/")+1:], firstSegment) {
		rel = strings.TrimPrefix(rel, firstSegment)
		rel = strings.TrimPrefix(rel, "/")
		if rel == "" {
			parsed.Path = basePath
			return parsed.String(), nil
		}
	}
	parsed.Path = basePath + "/" + rel
	return parsed.String(), nil
}
