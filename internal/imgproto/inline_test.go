package imgproto_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/lan/meta-gateway/internal/imgproto"
)

var inlinePNG = []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a, 0x00, 0x00, 0x00, 0x0d, 0x49, 0x48, 0x44, 0x52}

func fetchOK(data []byte, contentType string) imgproto.Fetcher {
	return func(context.Context, string) ([]byte, string, error) { return data, contentType, nil }
}

// The upstream answers with a link on its own domain. A client configured for
// the gateway cannot open that link, so the bytes have to travel with the
// answer instead.
func TestInlineImagesEmbedsRemoteReference(t *testing.T) {
	images := []imgproto.ImageOut{{URL: "https://grok.example/v1/media/images/img_1.jpg", RevisedPrompt: "white background"}}
	got := imgproto.InlineImages(context.Background(), images, fetchOK(inlinePNG, "image/png"), 0)

	if len(got) != 1 {
		t.Fatalf("images=%d", len(got))
	}
	if !strings.HasPrefix(got[0].DataURL, "data:image/png;base64,") {
		t.Fatalf("data_url=%q", got[0].DataURL)
	}
	if got[0].RevisedPrompt != "white background" {
		t.Fatalf("revised_prompt lost: %q", got[0].RevisedPrompt)
	}
	// The caller's slice must not be mutated: the entry stays in the log/audit
	// path with its original upstream reference.
	if images[0].DataURL != "" {
		t.Fatalf("input mutated: %q", images[0].DataURL)
	}
}

func TestInlineImagesSniffsBytesWithoutContentType(t *testing.T) {
	images := []imgproto.ImageOut{{URL: "https://grok.example/media/thing"}}
	got := imgproto.InlineImages(context.Background(), images, fetchOK(inlinePNG, ""), 0)

	if !strings.HasPrefix(got[0].DataURL, "data:image/png;base64,") {
		t.Fatalf("data_url=%q", got[0].DataURL)
	}
}

func TestInlineImagesFallsBackToURL(t *testing.T) {
	cases := []struct {
		name   string
		images []imgproto.ImageOut
		fetch  imgproto.Fetcher
	}{
		{
			name:   "fetch error",
			images: []imgproto.ImageOut{{URL: "https://grok.example/a.png"}},
			fetch:  func(context.Context, string) ([]byte, string, error) { return nil, "", errors.New("boom") },
		},
		{
			name:   "empty payload",
			images: []imgproto.ImageOut{{URL: "https://grok.example/a.png"}},
			fetch:  fetchOK(nil, "image/png"),
		},
		{
			name:   "not an image",
			images: []imgproto.ImageOut{{URL: "https://grok.example/a"}},
			fetch:  fetchOK([]byte("<html></html>"), "text/html"),
		},
		{
			name:   "no usable mime",
			images: []imgproto.ImageOut{{URL: "https://grok.example/a"}},
			fetch:  fetchOK([]byte("plain text"), ""),
		},
		{
			name:   "nil fetcher",
			images: []imgproto.ImageOut{{URL: "https://grok.example/a.png"}},
			fetch:  nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := imgproto.InlineImages(context.Background(), tc.images, tc.fetch, 0)
			if got[0].DataURL != "" {
				t.Fatalf("expected url fallback, got %q", got[0].DataURL)
			}
			if got[0].URL != "https://grok.example/a.png" && got[0].URL != "https://grok.example/a" {
				t.Fatalf("url=%q", got[0].URL)
			}
		})
	}
}

func TestInlineImagesHonoursSizeCap(t *testing.T) {
	images := []imgproto.ImageOut{{URL: "https://grok.example/big.png"}}
	got := imgproto.InlineImages(context.Background(), images, fetchOK(inlinePNG, "image/png"), int64(len(inlinePNG)-1))

	if got[0].DataURL != "" {
		t.Fatalf("payload above the cap must keep the url, got %q", got[0].DataURL)
	}
}

func TestInlineImagesSkipsSelfContainedAndUnknownSchemes(t *testing.T) {
	called := 0
	fetch := func(context.Context, string) ([]byte, string, error) {
		called++
		return inlinePNG, "image/png", nil
	}
	images := []imgproto.ImageOut{
		{DataURL: "data:image/png;base64,QUFB"},
		{URL: "file:///etc/passwd"},
		{URL: "data:image/png;base64,QUFB"},
		{URL: ""},
	}
	got := imgproto.InlineImages(context.Background(), images, fetch, 0)

	if called != 0 {
		t.Fatalf("fetcher must not run for self-contained refs, called=%d", called)
	}
	if got[0].DataURL != "data:image/png;base64,QUFB" {
		t.Fatalf("existing data uri changed: %q", got[0].DataURL)
	}
	if got[2].URL != "data:image/png;base64,QUFB" || got[2].DataURL != "" {
		t.Fatalf("data uri referenced by URL field left alone: %+v", got[2])
	}
	if got[1].URL != "file:///etc/passwd" {
		t.Fatalf("non-http scheme should be left as-is: %q", got[1].URL)
	}
}

// SVG can carry script, so it is never inlined.
func TestInlineImagesRejectsSVG(t *testing.T) {
	images := []imgproto.ImageOut{{URL: "https://grok.example/x.svg"}}
	got := imgproto.InlineImages(context.Background(), images, fetchOK([]byte("<svg/>"), "image/svg+xml"), 0)

	if got[0].DataURL != "" {
		t.Fatalf("svg must not be inlined: %q", got[0].DataURL)
	}
}
