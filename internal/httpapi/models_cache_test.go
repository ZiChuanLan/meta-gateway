package httpapi

import (
	"testing"
	"time"
)

func TestModelsCacheTTLAndInvalidation(t *testing.T) {
	now := time.Now()
	c := newModelsCache(time.Second)
	c.now = func() time.Time { return now }

	if _, ok := c.Get(); ok {
		t.Fatal("cold cache must miss")
	}
	c.Put([]string{"gpt-4", " claude-3 ", "gpt-4", ""})
	raw, ok := c.Get()
	if !ok || len(raw) != 2 || raw[0] != "gpt-4" || raw[1] != "claude-3" {
		t.Fatalf("cached raw=%v ok=%v (want dedup+trim of 2 ids)", raw, ok)
	}

	// TTL expiry forces a recompute.
	now = now.Add(2 * time.Second)
	if _, ok := c.Get(); ok {
		t.Fatal("expired cache must miss")
	}

	// Explicit invalidation (admin writes) also drops the entry.
	c.Put([]string{"gpt-4"})
	c.Invalidate()
	if _, ok := c.Get(); ok {
		t.Fatal("invalidated cache must miss")
	}
}
