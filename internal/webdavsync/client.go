package webdavsync

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/lan/meta-gateway/internal/outbound"
)

// Client performs read-only WebDAV GET downloads.
type Client struct {
	HTTP     *http.Client
	MaxBytes int64
	mu       sync.RWMutex
}

const downloadTimeout = 120 * time.Second

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
