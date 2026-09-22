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
