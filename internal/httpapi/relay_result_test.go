package httpapi

import (
	"context"
	"io"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/lan/meta-gateway/internal/proxy"
	"github.com/lan/meta-gateway/internal/relay"
)

func TestWriteUpstreamResultMapsInternalTimeouts(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want int
	}{
		{name: "deadline", err: context.DeadlineExceeded, want: 504},
		{name: "canceled", err: context.Canceled, want: 502},
		{name: "model too long", err: proxy.ErrModelTooLong, want: 400},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			writeUpstreamResult(recorder, context.Background(), "request", &relay.Result{Err: test.err}, false, nil, nil)
			if recorder.Code != test.want {
				t.Fatalf("status=%d, want %d", recorder.Code, test.want)
			}
		})
	}
}

func TestWriteUpstreamResultSuppressesClientCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	recorder := httptest.NewRecorder()
	writeUpstreamResult(recorder, ctx, "request", &relay.Result{Err: context.Canceled}, false, nil, nil)
	if recorder.Code != 200 {
		t.Fatalf("client cancellation wrote status=%d, want no response", recorder.Code)
	}
}

func TestWriteUpstreamResultNormalizesInvalidStatus(t *testing.T) {
	for _, status := range []int{0, 1000} {
		t.Run("status-"+strconv.Itoa(status), func(t *testing.T) {
			recorder := httptest.NewRecorder()
			writeUpstreamResult(recorder, context.Background(), "request", &relay.Result{
				StatusCode: status,
				Body:       io.NopCloser(strings.NewReader("")),
			}, false, nil, nil)
			if recorder.Code != 502 {
				t.Fatalf("status=%d, want 502", recorder.Code)
			}
		})
	}
}
