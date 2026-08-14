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

// TestPeekFirstChunkWithTimeoutFailsOverOnSilence verifies that a stream that
// stays silent past the deadline is reported as a first-byte timeout (the
// candidate loop then fails over) instead of blocking forever.
func TestPeekFirstChunkWithTimeoutFailsOverOnSilence(t *testing.T) {
	body := io.NopCloser(silentReader{})
	started := time.Now()
	_, err := peekFirstChunkWithTimeout(body, 120*time.Millisecond)
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

// TestPeekFirstChunkWithTimeoutFastPath verifies the timeout wrapper does not
// delay normal first chunks (data arrives → immediate return).
func TestPeekFirstChunkWithTimeoutFastPath(t *testing.T) {
	body := io.NopCloser(strings.NewReader("data: {\"role\":\"assistant\"}\n\n"))
	started := time.Now()
	first, err := peekFirstChunkWithTimeout(body, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(first), "assistant") {
		t.Fatalf("unexpected prefix: %s", first)
	}
	if time.Since(started) > time.Second {
		t.Fatal("fast path was slow")
	}
}
