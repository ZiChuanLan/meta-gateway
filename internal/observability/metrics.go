package observability

import (
	"fmt"
	"io"
	"maps"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Registry struct {
	mu       sync.Mutex
	requests map[string]uint64
	duration map[string]float64
	rate     map[string]uint64
}

func NewRegistry() *Registry {
	return &Registry{requests: make(map[string]uint64), duration: make(map[string]float64), rate: make(map[string]uint64)}
}

func (r *Registry) ObserveHTTP(method, route string, status int, elapsed time.Duration) {
	key := labels(method, route, statusClass(status))
	r.mu.Lock()
	r.requests[key]++
	r.duration[key] += elapsed.Seconds()
	r.mu.Unlock()
}

func (r *Registry) RateLimited(scope string) {
	r.mu.Lock()
	r.rate[labels(scope)]++
	r.mu.Unlock()
}

func (r *Registry) WritePrometheus(w io.Writer, ready bool) error {
	r.mu.Lock()
	requests := maps.Clone(r.requests)
	durations := maps.Clone(r.duration)
	rate := maps.Clone(r.rate)
	r.mu.Unlock()
	if _, err := fmt.Fprintln(w, "# TYPE meta_gateway_ready gauge"); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "meta_gateway_ready %d\n", boolInt(ready)); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "# TYPE meta_gateway_http_requests_total counter"); err != nil {
		return err
	}
	for _, key := range slices.Sorted(maps.Keys(requests)) {
		parts := strings.Split(key, "\x00")
		fmt.Fprintf(w, "meta_gateway_http_requests_total{method=%s,route=%s,status_class=%s} %d\n", quote(parts[0]), quote(parts[1]), quote(parts[2]), requests[key])
	}
	if _, err := fmt.Fprintln(w, "# TYPE meta_gateway_http_request_duration_seconds_sum counter"); err != nil {
		return err
	}
	for _, key := range slices.Sorted(maps.Keys(durations)) {
		parts := strings.Split(key, "\x00")
		fmt.Fprintf(w, "meta_gateway_http_request_duration_seconds_sum{method=%s,route=%s,status_class=%s} %s\n", quote(parts[0]), quote(parts[1]), quote(parts[2]), strconv.FormatFloat(durations[key], 'f', 6, 64))
	}
	if _, err := fmt.Fprintln(w, "# TYPE meta_gateway_rate_limit_rejections_total counter"); err != nil {
		return err
	}
	for _, key := range slices.Sorted(maps.Keys(rate)) {
		fmt.Fprintf(w, "meta_gateway_rate_limit_rejections_total{scope=%s} %d\n", quote(key), rate[key])
	}
	return nil
}

func statusClass(status int) string {
	if status < 100 || status > 599 {
		return "unknown"
	}
	return strconv.Itoa(status/100) + "xx"
}
func labels(values ...string) string { return strings.Join(values, "\x00") }
func quote(value string) string      { return strconv.Quote(value) }
func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
