package ratelimit

import (
	"testing"
	"time"
)

func TestLimiterIsolatesKeysAndRefills(t *testing.T) {
	now := time.Unix(100, 0)
	limiter := New(60, 1)
	limiter.now = func() time.Time { return now }
	if ok, _ := limiter.Allow(1); !ok {
		t.Fatal("first request denied")
	}
	if ok, retry := limiter.Allow(1); ok || retry != time.Second {
		t.Fatalf("second request ok=%v retry=%v", ok, retry)
	}
	if ok, _ := limiter.Allow(2); !ok {
		t.Fatal("unrelated key shared bucket")
	}
	now = now.Add(time.Second)
	if ok, _ := limiter.Allow(1); !ok {
		t.Fatal("token did not refill")
	}
}

func TestLimiterDisableAndCleanup(t *testing.T) {
	if ok, _ := New(0, 0).Allow(1); !ok {
		t.Fatal("zero limiter should be disabled")
	}
	now := time.Unix(100, 0)
	limiter := New(60, 1)
	limiter.now = func() time.Time { return now }
	limiter.Allow(1)
	now = now.Add(2 * time.Hour)
	if removed := limiter.Cleanup(time.Hour); removed != 1 {
		t.Fatalf("removed=%d", removed)
	}
}

func TestLimiterAutomaticallyCleansIdleKeys(t *testing.T) {
	now := time.Unix(100, 0)
	limiter := New(60, 1)
	limiter.now = func() time.Time { return now }
	limiter.Allow(1)
	now = now.Add(2 * time.Hour)
	limiter.Allow(2)
	if _, exists := limiter.buckets[1]; exists {
		t.Fatal("idle key survived automatic cleanup")
	}
	if _, exists := limiter.buckets[2]; !exists {
		t.Fatal("current key was not retained")
	}
}

func TestLimiterBoundsBucketMap(t *testing.T) {
	now := time.Unix(100, 0)
	limiter := New(60, 1)
	limiter.now = func() time.Time { return now }
	limiter.nextCleanup = now.Add(time.Hour)
	for i := 0; i < maxBuckets; i++ {
		limiter.buckets[int64(i)] = &bucket{tokens: 1, updated: now, lastSeen: now}
	}
	if ok, _ := limiter.Allow(maxBuckets + 1); !ok {
		t.Fatal("new bucket should be admitted")
	}
	if len(limiter.buckets) != maxBuckets {
		t.Fatalf("bucket count=%d, want %d", len(limiter.buckets), maxBuckets)
	}
}
