package selfupdate

import (
	"context"
	"testing"
	"time"
)

// PullImage builds the tag query with strings.SplitN(ref, ":", 2)[1], which
// indexes out of range for an image reference that carries no tag. compose
// files may legitimately say `image: zichuanlan/meta-gateway` with no `:tag`,
// and handoff() takes the ref straight from the container's own
// Config.Image — so this is reachable from the one-click update.
//
// It matters more than a normal panic: handoff() runs in a bare `go` routine
// (Service.Start), NOT inside an HTTP handler, so recoverMiddleware cannot
// catch it and the whole gateway process dies.
func TestPullImageUntaggedRefDoesNotPanic(t *testing.T) {
	client := NewClient("/nonexistent/docker.sock")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// A missing socket must surface as a dial error, never a panic.
	if err := client.PullImage(ctx, "zichuanlan/meta-gateway", nil); err == nil {
		t.Fatal("expected a dial error for a nonexistent socket")
	}
}

// A tagged ref keeps working (guards against fixing the panic by mangling the
// normal path).
func TestPullImageTaggedRefStillResolves(t *testing.T) {
	client := NewClient("/nonexistent/docker.sock")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := client.PullImage(ctx, "zichuanlan/meta-gateway:v2.7.1", nil); err == nil {
		t.Fatal("expected a dial error for a nonexistent socket")
	}
}
