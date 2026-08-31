package proxy

import (
	"io"
	"strings"
	"testing"
	"time"
)

// silentReader never delivers data or EOF — it simulates an upstream that
// answers 200 and then hangs (half-open connection).
type silentReader struct{}

func (silentReader) Read([]byte) (int, error) {
	select {}
}

// TestPeekStreamStartTimeoutFailsOverOnSilence verifies that a stream that
// stays silent past the deadline is reported as a first-byte timeout (the
// candidate loop then fails over) instead of blocking forever.
func TestPeekStreamStartTimeoutFailsOverOnSilence(t *testing.T) {
	body := io.NopCloser(silentReader{})
	started := time.Now()
	_, _, err := peekStreamStartWithTimeout(body, 120*time.Millisecond)
	if err == nil {
		t.Fatal("expected first-byte timeout error")
	}
	if !strings.Contains(err.Error(), "first byte timeout") {
		t.Fatalf("unexpected error: %v", err)
	}
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("timeout took too long: %v", elapsed)
	}
}

// TestPeekStreamStartCommitsOnRoleHeader verifies the timeout wrapper does
// not delay normal streams: a leading role-header frame cannot be judged
// silent, but it must not stall the fast path — the peek keeps reading and
// commits as soon as content arrives.
func TestPeekStreamStartCommitsOnRoleHeader(t *testing.T) {
	body := io.NopCloser(strings.NewReader(
		"data: {\"choices\":[{\"delta\":{\"role\":\"assistant\"},\"index\":0}]}\n\n" +
			"data: {\"choices\":[{\"delta\":{\"content\":\"Hello\"},\"index\":0}]}\n\n"))
	started := time.Now()
	prefix, silent, err := peekStreamStartWithTimeout(body, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if silent {
		t.Fatal("content stream must not be silent")
	}
	if !strings.Contains(string(prefix), "Hello") {
		t.Fatalf("prefix must include the content frame, got: %s", prefix)
	}
	if time.Since(started) > time.Second {
		t.Fatal("fast path was slow")
	}
}
