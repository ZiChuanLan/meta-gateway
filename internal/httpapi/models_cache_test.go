package httpapi

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestModelsCacheGetOrComputeSingleFlightAndCopies(t *testing.T) {
	cache := newModelsCache(time.Minute)
	var calls atomic.Int32
	entered := make(chan struct{})
	release := make(chan struct{})
	compute := func() []string {
		calls.Add(1)
		close(entered)
		<-release
		return []string{"gpt-test"}
	}

	const workers = 8
	results := make(chan []string, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results <- cache.GetOrCompute(compute)
		}()
	}
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("compute was not called")
	}
	close(release)
	wg.Wait()
	close(results)
	for result := range results {
		if len(result) != 1 || result[0] != "gpt-test" {
			t.Fatalf("result=%v", result)
		}
		result[0] = "mutated"
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("compute calls=%d, want 1", got)
	}
	if result, ok := cache.Get(); !ok || len(result) != 1 || result[0] != "gpt-test" {
		t.Fatalf("cached result=%v ok=%v", result, ok)
	}
}

func TestModelsCacheInvalidationWinsAgainstInFlightRefresh(t *testing.T) {
	cache := newModelsCache(time.Minute)
	entered := make(chan struct{})
	release := make(chan struct{})
	done := make(chan []string, 1)
	go func() {
		done <- cache.GetOrCompute(func() []string {
			close(entered)
			<-release
			return []string{"stale-model"}
		})
	}()

	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("refresh did not start")
	}
	cache.Invalidate()
	close(release)
	<-done
	if result, ok := cache.Get(); ok {
		t.Fatalf("in-flight stale refresh repopulated cache: %v", result)
	}
}
