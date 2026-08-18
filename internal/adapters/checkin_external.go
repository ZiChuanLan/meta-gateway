package adapters

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	pathpkg "path"
	"strings"
	"time"
)

// ExternalCheckinAdapter serves generic external check-in sites that are not
// part of the New-API family (e.g. 薄荷公益站 up.x666.me, qd.x666.me): a
// cookie-authenticated POST (or GET) to a configurable path, with Origin and
// Referer headers derived from the site URL so the upstream accepts the
// request. Success is determined the same way the New-API adapter does it:
// HTTP 2xx + JSON success != false.
type ExternalCheckinAdapter struct {
	name   string
	client *http.Client
}

func NewExternalCheckinAdapter(name string, client *http.Client) *ExternalCheckinAdapter {
	if client == nil {
		client = &http.Client{Transport: &http.Transport{ResponseHeaderTimeout: 15 * time.Second}}
	}
	return &ExternalCheckinAdapter{name: name, client: client}
}

func (a *ExternalCheckinAdapter) Name() string { return a.name }

func (a *ExternalCheckinAdapter) RequiresPlatformUserID() bool { return false }

// DefaultExternalCheckinPath is used when the credential meta does not declare
// a checkin_path. It matches the 薄荷公益站 convention.
const DefaultExternalCheckinPath = "/api/checkin/spin"

func (a *ExternalCheckinAdapter) Checkin(ctx context.Context, input CheckinInput) (CheckinResult, error) {
	if strings.TrimSpace(input.Cookie) == "" {
		return CheckinResult{}, &CheckinError{Kind: ErrorPayload, Message: "external check-in requires a cookie"}
	}
	base, err := url.Parse(strings.TrimSpace(input.BaseURL))
	if err != nil || !base.IsAbs() || base.Host == "" || base.User != nil ||
		(base.Scheme != "http" && base.Scheme != "https") {
		return CheckinResult{}, &CheckinError{Kind: ErrorInvalidURL}
	}
	base.RawQuery = ""
	base.Fragment = ""
	base.RawPath = ""
	origin := strings.TrimRight(base.Scheme+"://"+base.Host, "/")
	method := strings.ToUpper(strings.TrimSpace(input.CheckinMethod))
	if method == "" {
		method = http.MethodPost
	}
	if method != http.MethodPost && method != http.MethodGet {
		return CheckinResult{}, &CheckinError{Kind: ErrorPayload, Message: "unsupported check-in method"}
	}
	path := strings.TrimSpace(input.CheckinPath)
	if path == "" {
		path = DefaultExternalCheckinPath
	}
	if !strings.HasPrefix(path, "/") || strings.Contains(path, "\\") ||
		strings.Contains(path, "?") || strings.Contains(path, "#") || strings.Contains(path, "%") ||
		pathpkg.Clean(path) != path {
		return CheckinResult{}, &CheckinError{Kind: ErrorInvalidURL, Message: "invalid external check-in path"}
	}
	base.Path = strings.TrimRight(base.Path, "/") + path
	endpoint := base.String()
	req, err := http.NewRequestWithContext(ctx, method, endpoint, nil)
	if err != nil {
		return CheckinResult{}, &CheckinError{Kind: ErrorInvalidURL}
	}
	req.Header.Set("Cookie", input.Cookie)
	req.Header.Set("Accept", "application/json")
	// Browser-like Origin/Referer so server-side CSRF / hotlink checks pass.
	req.Header.Set("Origin", origin)
	req.Header.Set("Referer", origin+"/")

	resp, err := a.client.Do(req)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return CheckinResult{}, err
		}
		return CheckinResult{}, &CheckinError{Kind: ErrorTransport}
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return CheckinResult{}, &CheckinError{
			Kind:    ErrorStatus,
			Status:  resp.StatusCode,
			Message: explainCheckinHTTPStatus(resp.StatusCode),
		}
	}

	limited := io.LimitReader(resp.Body, maxCheckinResponseBytes+1)
	body, err := io.ReadAll(limited)
	if err != nil {
		return CheckinResult{}, &CheckinError{Kind: ErrorTransport}
	}
	if len(body) > maxCheckinResponseBytes {
		return CheckinResult{}, &CheckinError{Kind: ErrorTooLarge}
	}
	var payload struct {
		Success *bool  `json:"success"`
		Message string `json:"message"`
		Data    struct {
			Reward any `json:"reward"`
		} `json:"data"`
	}
	// External sites are more varied: a 2xx without a machine-readable body is
	// still a successful check-in from the gateway's point of view.
	if err := json.Unmarshal(body, &payload); err != nil || payload.Success == nil {
		return CheckinResult{
			Outcome:  CheckinSuccess,
			Category: "checked_in",
			Message:  "HTTP " + resp.Status + " (non-JSON response)",
		}, nil
	}
	if *payload.Success {
		return CheckinResult{
			Outcome:  CheckinSuccess,
			Category: "checked_in",
			Message:  "check-in succeeded",
			Reward:   rewardString(payload.Data.Reward),
		}, nil
	}
	if alreadyCheckedIn(payload.Message) {
		return CheckinResult{Outcome: CheckinSuccess, Category: "already_checked_in", Message: "already checked in"}, nil
	}
	detail := strings.TrimSpace(payload.Message)
	if detail == "" {
		detail = "upstream rejected check-in without a message"
	}
	return CheckinResult{}, &CheckinError{
		Kind:    ErrorPayload,
		Message: redactCheckinDetail(detail),
	}
}
