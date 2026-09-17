package proxy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/lan/meta-gateway/internal/imgproto"
)

var (
	// ErrMediaNotImage rejects a media fetch whose response is not an image.
	ErrMediaNotImage = errors.New("proxy: media response is not an image")
	// ErrMediaTooLarge rejects a media payload above the inline cap.
	ErrMediaTooLarge = errors.New("proxy: media payload too large")
)

// mediaFetchTimeout bounds the extra hop added by inlining. A slow media host
// must never stall a response the client is already waiting on.
const mediaFetchTimeout = 20 * time.Second

// FetchMedia downloads an image referenced by an upstream response so the
// chat-to-edit shim can hand the client a self-contained data URI instead of a
// link on a host the client has no route to.
//
// The request rides the same policy-enforced outbound client as channel
// traffic, so private destinations stay blocked and redirects are revalidated.
// Only image payloads within imgproto.MaxInlineImageBytes are returned.
func (s *Service) FetchMedia(ctx context.Context, rawURL string) ([]byte, string, error) {
	if s == nil || s.relay == nil {
		return nil, "", errors.New("proxy: media fetch unavailable")
	}
	if strings.TrimSpace(rawURL) == "" {
		return nil, "", errors.New("proxy: empty media url")
	}
	ctx, cancel := context.WithTimeout(ctx, mediaFetchTimeout)
	defer cancel()

	headers := http.Header{}
	headers.Set("Accept", "image/*")
	result := s.relay.ForwardWithHeaders(ctx, http.MethodGet, rawURL, headers, nil)
	if result == nil {
		return nil, "", errors.New("proxy: media fetch returned no result")
	}
	if result.Body != nil {
		defer result.Body.Close()
	}
	if result.Err != nil {
		return nil, "", result.Err
	}
	if result.StatusCode < 200 || result.StatusCode >= 300 {
		return nil, "", fmt.Errorf("proxy: media fetch status %d", result.StatusCode)
	}
	if result.Body == nil {
		return nil, "", errors.New("proxy: media fetch returned no body")
	}
	contentType := strings.TrimSpace(result.Header.Get("Content-Type"))
	if !strings.HasPrefix(strings.ToLower(contentType), "image/") {
		return nil, "", ErrMediaNotImage
	}
	data, err := io.ReadAll(io.LimitReader(result.Body, imgproto.MaxInlineImageBytes+1))
	if err != nil {
		return nil, "", err
	}
	if int64(len(data)) > imgproto.MaxInlineImageBytes {
		return nil, "", ErrMediaTooLarge
	}
	if len(data) == 0 {
		return nil, "", errors.New("proxy: media payload empty")
	}
	return data, contentType, nil
}
