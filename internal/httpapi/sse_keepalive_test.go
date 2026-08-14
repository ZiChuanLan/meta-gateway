package httpapi

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// blockableReader is an io.Reader whose Read blocks until released. Used to
// simulate a silent upstream stream (no data, no EOF) in tests.
type blockableReader struct {
	release chan struct{}
	done    chan struct{}
	data    chan []byte
}

func newBlockableReader() *blockableReader {
	return &blockableReader{
		release: make(chan struct{}),
		done:    make(chan struct{}),
		data:    make(chan []byte, 8),
	}
}

func (r *blockableReader) Read(p []byte) (int, error) {
	select {
	case <-r.release:
		return 0, io.EOF
	case chunk := <-r.data:
		return copy(p, chunk), nil
	}
}

func (r *blockableReader) feed(chunk string) { r.data <- []byte(chunk) }
func (r *blockableReader) close()            { close(r.release) }

// TestSSEKeepaliveInjectedDuringSilence verifies that a streaming response
// which falls silent past the idle threshold receives ": keepalive" comment
// frames, and that real data flows through unchanged.
func TestSSEKeepaliveInjectedDuringSilence(t *testing.T) {
	body := newBlockableReader()
	rec := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = copySSEWithKeepalive(rec, body, 100*time.Millisecond)
	}()

	body.feed("data: {\"a\":1}\n\n")
	time.Sleep(250 * time.Millisecond) // two idle windows
	body.feed("data: {\"b\":2}\n\n")
	time.Sleep(150 * time.Millisecond) // another idle window
	body.close()
	<-done

	out := rec.Body.String()
	if !strings.Contains(out, "data: {\"a\":1}") || !strings.Contains(out, "data: {\"b\":2}") {
		t.Fatalf("data frames missing: %q", out)
	}
	count := strings.Count(out, ": keepalive\n\n")
	if count < 2 {
		t.Fatalf("keepalive frames = %d, want >= 2: %q", count, out)
	}
	if strings.Contains(out, "data: {") && !strings.HasPrefix(out, "data:") {
		t.Fatalf("keepalive corrupted the stream prefix: %q", out[:40])
	}
}

// TestSSEKeepaliveClientDisconnect verifies a write error surfaces promptly.
func TestSSEKeepaliveClientDisconnect(t *testing.T) {
	body := newBlockableReader()
	// A recorder that errors on first write.
	var failing http.ResponseWriter = errorWriter{}
	done := make(chan struct{})
	var err error
	go func() {
		defer close(done)
		_, err = copySSEWithKeepalive(failing, body, time.Hour)
	}()
	body.feed("data: x\n\n")
	<-done
	if err == nil {
		t.Fatal("expected write error")
	}
	body.close()
}

type errorWriter struct{}

func (errorWriter) Header() http.Header         { return http.Header{} }
func (errorWriter) WriteHeader(int)             {}
func (errorWriter) Write(p []byte) (int, error) { return 0, io.ErrClosedPipe }
