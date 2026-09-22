// Package sitedetect implements the All API Hub site-detection chain
// (source-verified detectSiteType.ts): ① domain whitelist → ② root page
// <title> regex → ③ Sub2API endpoint shape → ④ New-API-family auth error
// text + compat header names. Returns a normalized family that maps onto the
// gateway's type_hint values.
package sitedetect

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// Result is the detected site classification.
type Result struct {
	// Family is the gateway type_hint: "new-api", "one-api", "sub2api",
	// "openai-compatible", or "" when nothing matched.
	Family string `json:"family,omitempty"`
	// SiteType is the AAH-style site name when matched (NEW_API, ONE_API…).
	SiteType string `json:"site_type,omitempty"`
	// TitleMatched reports whether the <title> rule matched.
	TitleMatched bool `json:"title_matched,omitempty"`
	// Evidence describes which chain step matched.
	Evidence string `json:"evidence,omitempty"`
	// Title is the fetched root page title (for diagnostics).
	Title string `json:"title,omitempty"`
}

type siteRule struct {
	name   string
	family string
	regex  *regexp.Regexp
	// compatHeaders are the user-id header names the platform sends on
	// /api/user/* (used for white-label detection via auth errors).
	compatHeaders []string
	// authErrorMarkers are substrings of the /api/user/self error message
	// that identify the family.
	authErrorMarkers []string
}

func makeTitleRegex(name string) *regexp.Regexp {
	escaped := regexp.QuoteMeta(name)
	// AAH does escaped.replace(/-/g, "[-_ ]?") — Go's QuoteMeta does not
	// escape a bare dash, so replacing the literal dash is equivalent.
	pattern := strings.ReplaceAll(escaped, "-", `[-_ ]?`)
	// Case-insensitive, like AAH's makeTitleRegex.
	return regexp.MustCompile(`(?i)\b` + pattern + `\b`)
}

// rules mirrors AAH accountSiteDefinitions/definitions.ts (source-verified
// list of 15 site types; families map onto gateway type_hints).
var rules = []siteRule{
	{name: "NEW_API", family: "new-api", regex: makeTitleRegex("new-api"), compatHeaders: []string{"New-API-User"}, authErrorMarkers: []string{"access token", "未登录", "Unauthorized"}},
	{name: "ONE_API", family: "one-api", regex: makeTitleRegex("one-api"), compatHeaders: []string{"One-API-User"}, authErrorMarkers: []string{"access token", "未登录"}},
	{name: "ONE_HUB", family: "new-api", regex: makeTitleRegex("one-hub")},
	{name: "DONE_HUB", family: "new-api", regex: makeTitleRegex("done-hub")},
	{name: "ANYROUTER", family: "new-api", regex: makeTitleRegex("anyrouter"), compatHeaders: []string{"New-API-User"}},
	{name: "VELOERA", family: "new-api", regex: makeTitleRegex("veloera")},
	{name: "V_API", family: "new-api", regex: makeTitleRegex("v-api"), compatHeaders: []string{"X-Api-User"}},
	{name: "VO_API", family: "new-api", regex: makeTitleRegex("vo-api")},
	{name: "SUPER_API", family: "new-api", regex: makeTitleRegex("super-api")},
	{name: "RIX_API", family: "new-api", regex: makeTitleRegex("rix-api"), compatHeaders: []string{"Rix-Api-User"}},
	{name: "NEO_API", family: "new-api", regex: makeTitleRegex("neo-api")},
	{name: "WONG_GONGYI", family: "new-api", regex: makeTitleRegex("wong")},
	{name: "SUB2API", family: "sub2api", regex: makeTitleRegex("sub2api"), authErrorMarkers: []string{"token required", "unauthorized", "invalid token"}},
	{name: "AIHUBMIX", family: "new-api", regex: makeTitleRegex("aihubmix")},
	{name: "SHAREDCHAT", family: "openai-compatible", regex: makeTitleRegex("sharedchat")},
}

// domainWhitelist mirrors AAH's hostnames-only sites.
var domainWhitelist = map[string]string{
	"aihubmix.com":      "new-api",
	"www.aihubmix.com":  "new-api",
	"new.sharedchat.cc": "openai-compatible",
}

// compatHeaderRegex builds a case-insensitive regex from a compat user-id
// header name (AAH COMPAT_USER_ID_HEADER_MESSAGE_RULES): the header name with
// dashes relaxed to [-_ ]?, matched against the /api/user/self error message.
// White-label forks reference their own header in the auth error text.
func compatHeaderRegex(header string) *regexp.Regexp {
	pattern := regexp.QuoteMeta(header)
	pattern = strings.ReplaceAll(pattern, "-", `[-_ ]?`)
	// Word boundary prevents substring false hits (e.g. "Rix-Api-User" must
	// not match the X-Api-User rule).
	return regexp.MustCompile(`(?i)\b` + pattern + `\b`)
}

// Detect runs the full chain against baseURL with a short overall budget.
func Detect(ctx context.Context, client *http.Client, baseURL string) (*Result, error) {
	if client == nil {
		client = &http.Client{Timeout: 12 * time.Second}
	}
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		return &Result{}, nil
	}
	result := &Result{}

	// ① Domain whitelist.
	if family, ok := domainWhitelist[domainOf(baseURL)]; ok {
		result.Family = family
		result.SiteType = strings.ToUpper(family)
		result.Evidence = "domain-whitelist"
		return result, nil
	}

	// ② Root page <title>.
	title, err := fetchTitle(ctx, client, baseURL)
	if err == nil && title != "" {
		result.Title = title
		for _, rule := range rules {
			if rule.regex.MatchString(title) {
				result.Family = rule.family
				result.SiteType = rule.name
				result.TitleMatched = true
				result.Evidence = "title-regex"
				return result, nil
			}
		}
	}

	// ③ Sub2API endpoint shape: /api/v1/auth/me answers 401 with Sub2API's own JSON
	// envelope. Verified in source: `response.Error` writes
	// `{"code":<int>,"message":…}` (sub2api backend/internal/pkg/response/response.go)
	// and `AuthHandler.GetCurrentUser` calls `response.Unauthorized` for an
	// unauthenticated caller, so the observed body is
	// `{"code":401,"message":"User not authenticated"}`.
	//
	// Three details are load-bearing, each learned from a live false positive:
	//
	//   - `code` is an INTEGER there. Decoding it into a `Code string` silently
	//     failed on every real response, so the check could only ever match a
	//     hand-written test body; it is decoded as raw JSON instead.
	//   - The status must be 401. matching only the envelope shape also caught
	//     Moonshot, whose unrelated 404 body `{"code":5,…,"message":"没找到对象",…}`
	//     parses as the same shape.
	//   - `message` must be present, which excludes a bare `{"code":…}`.
	//
	// A bare 401 on this path is explicitly NOT enough: measured, DeepSeek answers
	// plain text "Authentication Fails (governor)" and used to be labelled sub2api.
	if body, status, err := fetch(ctx, client, baseURL+"/api/v1/auth/me"); err == nil && status == http.StatusUnauthorized {
		var authMe struct {
			Code    json.RawMessage `json:"code"`
			Message string          `json:"message"`
		}
		if jsonErr := json.Unmarshal(body, &authMe); jsonErr == nil && len(authMe.Code) > 0 && authMe.Message != "" {
			result.Family = "sub2api"
			result.SiteType = "SUB2API"
			result.Evidence = "sub2api-endpoint"
			return result, nil
		}
	}

	// ④ New-API family: /api/user/self auth error text + compat header names.
	body, status, err := fetch(ctx, client, baseURL+"/api/user/self")
	if err == nil && (status == http.StatusUnauthorized || status == http.StatusForbidden) {
		msg := string(body)
		lower := strings.ToLower(msg)
		// ④a Compat-header reverse inference: white-label forks reference
		// their own user-id header in the error message (AAH step 4).
		for _, rule := range rules {
			for _, header := range rule.compatHeaders {
				if compatHeaderRegex(header).MatchString(msg) {
					result.Family = rule.family
					result.SiteType = rule.name
					result.Evidence = "compat-header"
					return result, nil
				}
			}
		}
		// ④b Auth error markers.
		for _, rule := range rules {
			for _, marker := range rule.authErrorMarkers {
				if strings.Contains(lower, strings.ToLower(marker)) || strings.Contains(msg, marker) {
					result.Family = rule.family
					result.SiteType = rule.name
					result.Evidence = "auth-error-text"
					return result, nil
				}
			}
		}
		// ④c Structurally, not "any 401". A New-API-family host answers
		// /api/user/self with its own JSON envelope — `success` plus a `message`
		// (new-api's authHelper writes exactly `{"success":false,"message":…}`).
		//
		// The previous fallback was "any 401 on this path is New API", which is not
		// discriminating: measured, Zhipu answers 401 here with an `{"error":{…}}`
		// envelope and DeepSeek answers plain text `Authentication Fails
		// (governor)`. That rule labelled Zhipu as `new-api`, and because the
		// connection dialog applied detection on blur it silently replaced an
		// operator's explicit "智谱 GLM" pick — and its correct preset base URL.
		if status == http.StatusUnauthorized {
			var envelope struct {
				Success *bool  `json:"success"`
				Message string `json:"message"`
			}
			if jsonErr := json.Unmarshal(body, &envelope); jsonErr == nil && envelope.Success != nil && envelope.Message != "" {
				result.Family = "new-api"
				result.SiteType = "NEW_API"
				result.Evidence = "user-self-401"
				return result, nil
			}
		}
	}

	return result, nil
}

func domainOf(u string) string {
	u = strings.TrimPrefix(strings.TrimPrefix(u, "https://"), "http://")
	if idx := strings.IndexAny(u, "/:"); idx >= 0 {
		u = u[:idx]
	}
	return strings.ToLower(u)
}

func fetchTitle(ctx context.Context, client *http.Client, baseURL string) (string, error) {
	body, _, err := fetch(ctx, client, baseURL+"/")
	if err != nil {
		return "", err
	}
	head := string(body)
	if idx := strings.Index(strings.ToLower(head), "<title"); idx >= 0 {
		rest := head[idx:]
		start := strings.Index(rest, ">")
		end := strings.Index(rest, "</title>")
		if start >= 0 && end > start {
			return strings.TrimSpace(rest[start+1 : end]), nil
		}
	}
	return "", nil
}

func fetch(ctx context.Context, client *http.Client, url string) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("User-Agent", "meta-gateway-site-detect/1.0")
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	const maxDetectResponseBytes = 256 << 10
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxDetectResponseBytes+1))
	if err != nil {
		return nil, resp.StatusCode, err
	}
	if len(body) > maxDetectResponseBytes {
		return nil, resp.StatusCode, fmt.Errorf("site detect response too large")
	}
	return body, resp.StatusCode, nil
}
