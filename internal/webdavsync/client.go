package webdavsync

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/lan/meta-gateway/internal/outbound"
)

// Client performs read-only WebDAV GET downloads.
type Client struct {
	HTTP     *http.Client
	MaxBytes int64
}

func (c *Client) Download(ctx context.Context, targetURL, username, password string) ([]byte, error) {
	if c == nil || c.HTTP == nil {
		return nil, Error{Category: CategoryInternal, Message: "http client required"}
	}
	maxBytes := c.MaxBytes
	if maxBytes <= 0 {
		maxBytes = 10 << 20
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, targetURL, nil)
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
		return nil, Error{Category: CategoryUpstream, Message: "webdav body read failed"}
	}
	if int64(len(body)) > maxBytes {
		return nil, Error{Category: CategoryTooLarge, Message: "webdav backup exceeds size limit"}
	}
	return body, nil
}

// Probe performs a GET and discards the body after verifying status (capped).
func (c *Client) Probe(ctx context.Context, targetURL, username, password string) (latency time.Duration, err error) {
	started := time.Now()
	_, err = c.Download(ctx, targetURL, username, password)
	return time.Since(started), err
}
