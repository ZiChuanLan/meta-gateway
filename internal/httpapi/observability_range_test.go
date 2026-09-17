package httpapi_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"testing"
	"time"
)

// status fetches a URL and returns its status code without failing on 4xx, so
// validation paths can be asserted.
func status(t *testing.T, url string) int {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, url, nil)
	req.Header.Set("Authorization", "Bearer admin-test")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode
}

// A malformed or inverted bound must be rejected, not ignored: silently
// dropping it turns "the last 15 minutes" into an unbounded table scan, and
// the operator never learns their window was not applied.
func TestObservabilityTimeRangeValidation(t *testing.T) {
	upstream := mockOpenAIUpstream(t)
	defer upstream.Close()
	serverURL, _, _ := setupRelay(t, upstream.URL, "openai-compatible")

	bad := []string{
		"/admin/usage/series?since=yesterday",
		"/admin/usage/series?until=nope",
		"/admin/usage/summary?since=2026-09-17",
		"/admin/usage/top-models?since=2026-09-17T00:00:00Z&until=2026-09-16T00:00:00Z",
		"/admin/proxy-logs?since=notatime",
		"/admin/usage?until=nope",
		"/admin/proxy-logs/latency-histogram?since=12:00",
	}
	for _, path := range bad {
		if code := status(t, serverURL+path); code != http.StatusBadRequest {
			t.Errorf("GET %s = %d, want 400", path, code)
		}
	}
}

// The windowed aggregates are what the console charts; an absent bound has to
// keep behaving like "everything" rather than 500 or return nothing.
func TestObservabilityWindowedAggregates(t *testing.T) {
	upstream := mockOpenAIUpstream(t)
	defer upstream.Close()
	serverURL, _, _ := setupRelay(t, upstream.URL, "openai-compatible")

	now := time.Now().UTC()
	since := url.QueryEscape(now.Add(-time.Hour).Format(time.RFC3339))
	until := url.QueryEscape(now.Format(time.RFC3339))

	var series struct {
		Since         time.Time `json:"since"`
		Until         time.Time `json:"until"`
		BucketSeconds int       `json:"bucket_seconds"`
		Requests      []int     `json:"requests"`
		Failed        []int     `json:"failed"`
		Tokens        []int64   `json:"tokens"`
		Cost          []float64 `json:"cost"`
	}
	body := get(t, serverURL+"/admin/usage/series?since="+since+"&until="+until+"&buckets=12")
	if err := json.Unmarshal(body, &series); err != nil {
		t.Fatalf("series: %v (%s)", err, body)
	}
	if series.BucketSeconds <= 0 {
		t.Fatalf("bucket_seconds = %d", series.BucketSeconds)
	}
	// Buckets are epoch-aligned so consecutive windows share boundaries.
	if series.Since.Unix()%int64(series.BucketSeconds) != 0 {
		t.Errorf("series start %s is not aligned to %ds", series.Since, series.BucketSeconds)
	}
	if len(series.Requests) != len(series.Failed) ||
		len(series.Requests) != len(series.Tokens) ||
		len(series.Requests) != len(series.Cost) {
		t.Fatalf("series arrays are not parallel: %d/%d/%d/%d",
			len(series.Requests), len(series.Failed), len(series.Tokens), len(series.Cost))
	}
	if len(series.Requests) == 0 {
		t.Fatal("series returned no buckets for a bounded window")
	}

	var models []struct {
		Model    string `json:"model"`
		Requests int    `json:"requests"`
	}
	body = get(t, serverURL+"/admin/usage/top-models?since="+since+"&limit=5")
	if err := json.Unmarshal(body, &models); err != nil {
		t.Fatalf("top models: %v (%s)", err, body)
	}

	var hist struct {
		Buckets    []int `json:"buckets"`
		Total      int   `json:"total"`
		Matched    int   `json:"matched"`
		SampleSize int   `json:"sample_size"`
	}
	body = get(t, serverURL+"/admin/proxy-logs/latency-histogram?since="+since+"&until="+until+"&sample=5000")
	if err := json.Unmarshal(body, &hist); err != nil {
		t.Fatalf("histogram: %v (%s)", err, body)
	}
	if len(hist.Buckets) != 11 {
		t.Fatalf("histogram has %d buckets, want 11", len(hist.Buckets))
	}
	if hist.SampleSize != 5000 {
		t.Errorf("sample_size = %d, want the requested 5000", hist.SampleSize)
	}
	// The sampled count can never exceed the rows the window held.
	if hist.Total > hist.Matched {
		t.Errorf("total %d > matched %d", hist.Total, hist.Matched)
	}
}
