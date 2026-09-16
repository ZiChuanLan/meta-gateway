package httpapi

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/iotest"

	"github.com/lan/meta-gateway/internal/relay"
	"github.com/lan/meta-gateway/internal/usage"
)

func TestInterruptedImageStreamDoesNotEstimateTokensFromBase64(t *testing.T) {
	frame := "data: {\"type\":\"image_edit.partial_image\",\"b64_json\":\"" + strings.Repeat("QUFB", 200) + "\"}\n\n"
	result := &relay.Result{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(io.MultiReader(strings.NewReader(frame), iotest.ErrReader(io.ErrUnexpectedEOF))),
	}
	recorded := false
	writeUpstreamResult(httptest.NewRecorder(), context.Background(), "interrupted-image", result, true,
		func(tokens usage.Tokens, status, firstByteMS int, bytesSent int64) {
			recorded = true
			if bytesSent == 0 {
				t.Fatal("the partial image was not copied")
			}
			if tokens.Valid() {
				t.Fatalf("base64 image bytes were billed as tokens: %+v", tokens)
			}
		}, nil, nil, false)
	if !recorded {
		t.Fatal("missing accounting callback")
	}
}
