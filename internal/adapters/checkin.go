package adapters

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const maxCheckinResponseBytes = 2 << 20

type CheckinOutcome string

const (
	CheckinSuccess CheckinOutcome = "success"
	CheckinSkipped CheckinOutcome = "skipped"
)

type CheckinInput struct {
	BaseURL        string
	Secret         string
	PlatformUserID int64
}

type CheckinResult struct {
	Outcome  CheckinOutcome
	Category string
	Message  string
	Reward   string
}

type CheckinAdapter interface {
	Name() string
	// RequiresPlatformUserID reports whether the upstream expects user-id headers
	// (New API family). One API typically does not.
	RequiresPlatformUserID() bool
	Checkin(context.Context, CheckinInput) (CheckinResult, error)
}

type CheckinError struct {
	Kind   ErrorKind
	Status int
	// Message is a safe operator-facing detail (never secrets). Empty means use a generic fallback.
	Message string
}

func (e *CheckinError) Error() string {
	if e == nil {
		return "check-in failed"
	}
	if strings.TrimSpace(e.Message) != "" {
		if e.Status != 0 {
			return fmt.Sprintf("check-in failed: %s (%d): %s", e.Kind, e.Status, e.Message)
		}
		return fmt.Sprintf("check-in failed: %s: %s", e.Kind, e.Message)
	}
	if e.Status != 0 {
		return fmt.Sprintf("check-in failed: %s (%d)", e.Kind, e.Status)
	}
	return fmt.Sprintf("check-in failed: %s", e.Kind)
}

type JSONCheckinAdapter struct {
	name       string
	client     *http.Client
	userHeader bool
}

func NewJSONCheckinAdapter(name string, client *http.Client, userHeader bool) *JSONCheckinAdapter {
	if client == nil {
		client = &http.Client{Transport: &http.Transport{ResponseHeaderTimeout: 15 * time.Second}}
	}
	return &JSONCheckinAdapter{name: name, client: client, userHeader: userHeader}
}

func (a *JSONCheckinAdapter) Name() string { return a.name }

func (a *JSONCheckinAdapter) RequiresPlatformUserID() bool { return a.userHeader }

func (a *JSONCheckinAdapter) Checkin(ctx context.Context, input CheckinInput) (CheckinResult, error) {
	endpoint, err := checkinEndpoint(input.BaseURL)
	if err != nil {
		return CheckinResult{}, &CheckinError{Kind: ErrorInvalidURL}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, nil)
	if err != nil {
		return CheckinResult{}, &CheckinError{Kind: ErrorInvalidURL}
	}
	req.Header.Set("Authorization", "Bearer "+input.Secret)
	req.Header.Set("Accept", "application/json")
	if a.userHeader {
		ApplyCompatUserIDHeaders(req.Header, input.PlatformUserID)
	}

	resp, err := a.client.Do(req)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return CheckinResult{}, err
		}
		return CheckinResult{}, &CheckinError{Kind: ErrorTransport}
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return CheckinResult{Outcome: CheckinSkipped, Category: "unsupported", Message: "check-in is not supported"}, nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return CheckinResult{}, &CheckinError{Kind: ErrorStatus, Status: resp.StatusCode}
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
	if err := json.Unmarshal(body, &payload); err != nil || payload.Success == nil {
		return CheckinResult{}, &CheckinError{
			Kind:    ErrorPayload,
			Message: "upstream body was not valid check-in JSON (missing success field)",
		}
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
	if unsupportedCheckin(payload.Message) {
		return CheckinResult{Outcome: CheckinSkipped, Category: "unsupported", Message: "check-in is not supported"}, nil
	}
	// success:false with an unrecognized message — surface the upstream text so operators can act.
	detail := strings.TrimSpace(payload.Message)
	if detail == "" {
		detail = "upstream rejected check-in without a message"
	}
	return CheckinResult{}, &CheckinError{
		Kind:    ErrorPayload,
		Message: redactCheckinDetail(detail),
	}
}

func checkinEndpoint(baseURL string) (string, error) {
	// Same base normalization as account self/token probes: strip trailing /v1 so
	// OpenAI-style site URLs still hit /api/user/checkin on the site root.
	return accountEndpoint(baseURL, "/api/user/checkin")
}

func rewardString(value any) string {
	switch v := value.(type) {
	case string:
		return strings.TrimSpace(v)
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	case json.Number:
		return v.String()
	default:
		return ""
	}
}

func alreadyCheckedIn(message string) bool {
	lower := strings.ToLower(strings.TrimSpace(message))
	for _, marker := range []string{
		"already checked in", "already signed", "already sign in",
		"\u4eca\u65e5\u5df2\u7b7e\u5230", "\u4eca\u5929\u5df2\u7b7e\u5230",
		"\u4eca\u5929\u5df2\u7ecf\u7b7e\u5230", "\u4eca\u65e5\u5df2\u7ecf\u7b7e\u5230",
		"\u5df2\u7ecf\u7b7e\u5230", "\u5df2\u7b7e\u5230", "\u91cd\u590d\u7b7e\u5230", "\u7b7e\u5230\u8fc7",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

// redactCheckinDetail keeps upstream failure text useful while avoiding long dumps / obvious secrets.
func redactCheckinDetail(message string) string {
	trimmed := strings.TrimSpace(message)
	if trimmed == "" {
		return ""
	}
	const maxLen = 240
	if len(trimmed) > maxLen {
		trimmed = trimmed[:maxLen] + "…"
	}
	// Drop common secret-looking fragments if an upstream echoes them.
	lower := strings.ToLower(trimmed)
	for _, marker := range []string{"bearer ", "sk-", "access_token", "session-secret"} {
		if strings.Contains(lower, marker) {
			return "upstream rejected check-in (detail redacted)"
		}
	}
	return trimmed
}

func unsupportedCheckin(message string) bool {
	lower := strings.ToLower(strings.TrimSpace(message))
	for _, marker := range []string{
		"invalid url (post /api/user/checkin)",
		"checkin endpoint not found",
		"check-in is not supported",
		"checkin is not supported",
		"does not support checkin",
		"not support checkin",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}
