package httpapi

import (
	"testing"

	"github.com/lan/meta-gateway/internal/domain"
)

func TestGroupRateLimiterUnlimitedWhenDisabled(t *testing.T) {
	l := newGroupRateLimiter()
	group := &domain.KeyGroup{Name: "free", RatePerMinute: 0}
	for i := 0; i < 100; i++ {
		if ok, _ := l.Allow(group); !ok {
			t.Fatalf("unlimited group must always allow (attempt %d)", i)
		}
	}
}

func TestGroupRateLimiterExhausts(t *testing.T) {
	l := newGroupRateLimiter()
	group := &domain.KeyGroup{Name: "bursty", RatePerMinute: 60, RateBurst: 2}
	allowed := 0
	for i := 0; i < 10; i++ {
		if ok, _ := l.Allow(group); ok {
			allowed++
		}
	}
	if allowed != 2 {
		t.Fatalf("burst=2 group allowed %d requests, want 2", allowed)
	}
	// Config change rebuilds the bucket lazily.
	group.RatePerMinute = 6000
	group.RateBurst = 100
	if ok, _ := l.Allow(group); !ok {
		t.Fatal("rebuilt bucket must allow")
	}
}

func TestGroupRateLimiterIsolatedPerGroup(t *testing.T) {
	l := newGroupRateLimiter()
	a := &domain.KeyGroup{Name: "a", RatePerMinute: 60, RateBurst: 1}
	b := &domain.KeyGroup{Name: "b", RatePerMinute: 60, RateBurst: 1}
	if ok, _ := l.Allow(a); !ok {
		t.Fatal("a first must allow")
	}
	if ok, _ := l.Allow(b); !ok {
		t.Fatal("b must have its own bucket")
	}
	if ok, _ := l.Allow(a); ok {
		t.Fatal("a exhausted must deny")
	}
}
