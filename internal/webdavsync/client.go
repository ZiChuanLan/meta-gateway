package webdavsync

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/lan/meta-gateway/internal/outbound"
)

// Client performs WebDAV GET downloads and PUT uploads.
type Client struct {
	HTTP     *http.Client
	MaxBytes int64
	mu       sync.RWMutex
}

const (
	downloadTimeout = 120 * time.Second
	uploadTimeout   = 120 * time.Second
)

func (c *Client) Download(ctx context.Context, targetURL, username, password string) ([]byte, error) {
	if c == nil || c.HTTP == nil {
		return nil, Error{Category: CategoryInternal, Message: "http client required"}
	}
	maxBytes := c.maxBytes()
	if maxBytes <= 0 {
		maxBytes = 10 << 20
	}
	// Enforce a total download deadline so slow body transfers don't block indefinitely.
	dlCtx, cancel := context.WithTimeout(ctx, downloadTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(dlCtx, http.MethodGet, targetURL, nil)
	if err != nil {
		return nil, Error{Category: CategoryValidation, Message: "invalid request"}
	}
	request.SetBasicAuth(username, password)
	request.Header.Set("Accept", "application/json, text/plain, */*")

	response, err := c.HTTP.Do(request)
	if err != nil {
		if errors.Is(err, outbound.ErrBlocked) || strings.Contains(err.Error(), outbound.ErrBlocked.Error()) {
			return nil, Error{Category: CategoryOutboundBlocked, Message: "webdav host blocked by outbound policy"}
		}
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, Error{Category: CategoryUpstream, Message: "webdav request canceled or timed out"}
		}
		return nil, Error{Category: CategoryUpstream, Message: "webdav download failed"}
	}
	defer response.Body.Close()

	switch {
	case response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden:
		return nil, Error{Category: CategoryAuthFailed, Message: "webdav authentication failed"}
	case response.StatusCode == http.StatusNotFound:
		return nil, Error{Category: CategoryNotFound, Message: "webdav backup file not found"}
	case response.StatusCode < 200 || response.StatusCode >= 300:
		return nil, Error{Category: CategoryUpstream, Message: "webdav download failed"}
	}

	limited := io.LimitReader(response.Body, maxBytes+1)
	body, err := io.ReadAll(limited)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, Error{Category: CategoryUpstream, Message: "webdav download timed out"}
		}
		return nil, Error{Category: CategoryUpstream, Message: "webdav body read failed"}
	}
	if err := dlCtx.Err(); err != nil {
		return nil, Error{Category: CategoryUpstream, Message: "webdav download timed out"}
	}
	if int64(len(body)) > maxBytes {
		return nil, Error{Category: CategoryTooLarge, Message: "webdav backup exceeds size limit"}
	}
	return body, nil
}

// Upload PUTs the backup document, creating missing parent collections once
// (many WebDAV servers return 409 when the target folder does not exist).
func (c *Client) Upload(ctx context.Context, targetURL, username, password string, body []byte) error {
	if c == nil || c.HTTP == nil {
		return Error{Category: CategoryInternal, Message: "http client required"}
	}
	uploadCtx, cancel := context.WithTimeout(ctx, uploadTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(uploadCtx, http.MethodPut, targetURL, bytes.NewReader(body))
	if err != nil {
		return Error{Category: CategoryValidation, Message: "invalid request"}
	}
	request.SetBasicAuth(username, password)
	request.Header.Set("Content-Type", "application/json")

	response, err := c.HTTP.Do(request)
	if err != nil {
		return uploadTransportError(err)
	}
	drainAndClose(response)
	switch {
	case response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden:
		return Error{Category: CategoryAuthFailed, Message: "webdav authentication failed"}
	case response.StatusCode == http.StatusConflict:
		// Parent collection missing: create ancestors, then retry once.
		if mkErr := c.mkcolAncestors(uploadCtx, targetURL, username, password); mkErr != nil {
			return mkErr
		}
		retry, err := http.NewRequestWithContext(uploadCtx, http.MethodPut, targetURL, bytes.NewReader(body))
		if err != nil {
			return Error{Category: CategoryValidation, Message: "invalid request"}
		}
		retry.SetBasicAuth(username, password)
		retry.Header.Set("Content-Type", "application/json")
		retryResponse, err := c.HTTP.Do(retry)
		if err != nil {
			return uploadTransportError(err)
		}
		drainAndClose(retryResponse)
		if retryResponse.StatusCode < 200 || retryResponse.StatusCode >= 300 {
			return Error{Category: CategoryUpstream, Message: "webdav upload failed"}
		}
		return nil
	case response.StatusCode < 200 || response.StatusCode >= 300:
		return Error{Category: CategoryUpstream, Message: "webdav upload failed"}
	}
	return nil
}

func uploadTransportError(err error) error {
	if errors.Is(err, outbound.ErrBlocked) || strings.Contains(err.Error(), outbound.ErrBlocked.Error()) {
		return Error{Category: CategoryOutboundBlocked, Message: "webdav host blocked by outbound policy"}
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return Error{Category: CategoryUpstream, Message: "webdav request canceled or timed out"}
	}
	return Error{Category: CategoryUpstream, Message: "webdav upload failed"}
}

func drainAndClose(response *http.Response) {
	if response == nil || response.Body == nil {
		return
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
	_ = response.Body.Close()
}

// mkcolAncestors creates each missing ancestor collection top-down. Non-auth
// failures are best-effort: the retried PUT is the authoritative check.
func (c *Client) mkcolAncestors(ctx context.Context, targetURL, username, password string) error {
	parsed, err := url.Parse(targetURL)
	if err != nil || parsed.Path == "" {
		return Error{Category: CategoryValidation, Message: "webdav url is invalid"}
	}
	segments := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	if len(segments) < 2 {
		return nil
	}
	for i := 1; i < len(segments); i++ {
		collection := *parsed
		collection.Path = "/" + strings.Join(segments[:i], "/")
		request, err := http.NewRequestWithContext(ctx, "MKCOL", collection.String(), nil)
		if err != nil {
			continue
		}
		request.SetBasicAuth(username, password)
		response, err := c.HTTP.Do(request)
		if err != nil {
			continue
		}
		drainAndClose(response)
		switch {
		case response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden:
			return Error{Category: CategoryAuthFailed, Message: "webdav authentication failed"}
		case response.StatusCode == http.StatusMethodNotAllowed:
			// Collection already exists — keep walking.
		}
	}
	return nil
}

func (c *Client) maxBytes() int64 {
	if c == nil {
		return 0
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.MaxBytes
}

func (c *Client) setMaxBytes(maxBytes int64) {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.MaxBytes = maxBytes
	c.mu.Unlock()
}
