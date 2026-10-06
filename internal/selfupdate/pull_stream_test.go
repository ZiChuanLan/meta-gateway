package selfupdate

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

type pullTransport func(*http.Request) (*http.Response, error)

func (f pullTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestPullImageChecksStreamErrorsWithoutProgressCallback(t *testing.T) {
	for _, test := range []struct {
		name, body, want string
	}{
		{"error", "{\"status\":\"Pulling\"}\n{\"error\":\"manifest unknown\"}\n", "manifest unknown"},
		{"detail", "{\"errorDetail\":{\"message\":\"access denied\"}}\n", "access denied"},
		{"malformed", "{broken}\n", "invalid progress stream"},
		{"success", "{\"status\":\"Download complete\"}\n", ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			for _, withProgress := range []bool{false, true} {
				client := NewClient("")
				client.http.Transport = pullTransport(func(r *http.Request) (*http.Response, error) {
					return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(test.body)), Header: make(http.Header)}, nil
				})
				var progress func(string)
				if withProgress {
					progress = func(string) {}
				}
				err := client.PullImage(context.Background(), "zichuanlan/meta-gateway:latest", progress)
				if test.want == "" {
					if err != nil {
						t.Fatal(err)
					}
				} else if err == nil || !strings.Contains(err.Error(), test.want) {
					t.Fatalf("progress=%v: error=%v, want %q", withProgress, err, test.want)
				}
			}
		})
	}
}
