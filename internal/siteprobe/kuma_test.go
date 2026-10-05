package siteprobe

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestResolveKumaSource(t *testing.T) {
	cases := []struct {
		url  string
		base string
		slug string
		ok   bool
	}{
		{"https://stat.hxi.me/status/ai", "https://stat.hxi.me", "ai", true},
		{"https://stat.hxi.me/status-page/ai/", "https://stat.hxi.me", "ai", true},
		{"https://status.example.com/my-page", "https://status.example.com", "my-page", true},
		{"https://stat.hxi.me/", "", "", false},
		{"", "", "", false},
		{"not-a-url", "", "", false},
	}
	for _, testCase := range cases {
		base, slug, err := ResolveKumaSource(testCase.url)
		if testCase.ok != (err == nil) {
			t.Errorf("ResolveKumaSource(%q) error = %v, want ok=%v", testCase.url, err, testCase.ok)
			continue
		}
		if base != testCase.base || slug != testCase.slug {
			t.Errorf("ResolveKumaSource(%q) = %q/%q, want %q/%q", testCase.url, base, slug, testCase.base, testCase.slug)
		}
	}
}

// The heartbeat shape is what Uptime Kuma really returns (captured from a live
// public status page): ping is null on every down beat, pending/maintenance
// beats exist, and the timestamp carries no zone.
func TestFetchKumaComputesAvailabilityFromHeartbeats(t *testing.T) {
	server := httptest.NewServer(kumaHandler(t, 8, 2, map[string]string{
		"44": "z-ai/glm-5.2",
		"6":  "yang-api",
	}))
	defer server.Close()

	source, err := FetchKuma(context.Background(), server.Client(), server.URL+"/status/ai", 24*time.Hour)
	if err != nil {
		t.Fatalf("FetchKuma: %v", err)
	}
	if len(source.Monitors) != 2 {
		t.Fatalf("monitors = %d, want 2", len(source.Monitors))
	}
	byName := map[string]KumaMonitor{}
	for _, monitor := range source.Monitors {
		byName[monitor.Name] = monitor
	}
	model := byName["z-ai/glm-5.2"]
	// 8 up + 2 down + 1 pending + 1 maintenance: the last two must not count.
	if model.Samples != 10 || model.UpCount != 8 {
		t.Fatalf("model monitor = %d/%d, want 8/10 (pending and maintenance excluded)", model.UpCount, model.Samples)
	}
	if model.Ratio != 0.8 {
		t.Fatalf("ratio = %v, want 0.8", model.Ratio)
	}
	if model.GroupName != "GLM" {
		t.Fatalf("group = %q, want GLM", model.GroupName)
	}
	if model.WeakEvidence {
		t.Fatal("a keyword monitor verifies content and must not be weak evidence")
	}
	// Down beats carry no ping, so the average must come from the up beats only.
	if model.AvgPingMS == nil || *model.AvgPingMS != 1000 {
		t.Fatalf("avg ping = %v, want 1000 (measured over up beats only)", model.AvgPingMS)
	}

	line := byName["yang-api"]
	if !line.WeakEvidence {
		t.Fatal("an http monitor only proves the endpoint answers; it must be flagged")
	}
	if line.GroupName != "北京检测节点" {
		t.Fatalf("line monitor group = %q", line.GroupName)
	}
}

func TestFetchKumaFailsOnSomethingThatIsNotAStatusPage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<html><body>not a status page</body></html>"))
	}))
	defer server.Close()
	if _, err := FetchKuma(context.Background(), server.Client(), server.URL+"/status/ai", 0); err == nil {
		t.Fatal("a non-Kuma page must fail the round instead of producing empty data")
	}
}

func TestComputeMonitorWithNoCountableBeatsReportsNoData(t *testing.T) {
	// Pending and maintenance beats only: the monitor has nothing to say, which
	// must be "no samples" (and therefore no verdict), never "0%".
	monitor := computeMonitor("1", "m", "keyword", "g", decodeBeats(beatsJSON(0, 0, 3, 2)), time.Now().Add(-24*time.Hour))
	if monitor.Samples != 0 || monitor.Ratio != 0 {
		t.Fatalf("monitor = %+v, want no countable samples", monitor)
	}
}

// kumaHandler serves a status page plus heartbeats in the shape a live Kuma
// instance returns, with `up` up beats and `down` down beats per monitor, plus
// one pending and one maintenance beat.
func kumaHandler(t *testing.T, up, down int, monitors map[string]string) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/status-page/ai":
			groupA := ""
			for id, name := range monitors {
				if name == "yang-api" {
					continue
				}
				groupA += fmt.Sprintf(`{"id":%s,"name":%q,"sendUrl":0,"type":"keyword"},`, id, name)
			}
			body := fmt.Sprintf(`{"config":{"slug":"ai","title":"AI","autoRefreshInterval":300},
				"incidents":[],
				"publicGroupList":[
					{"id":2,"name":"北京检测节点","weight":1,"monitorList":[{"id":6,"name":"yang-api","sendUrl":0,"type":"http"}]},
					{"id":6,"name":"GLM","weight":8,"monitorList":[%s]}]}`, trimTrailingComma(groupA))
			_, _ = w.Write([]byte(body))
		case r.URL.Path == "/api/status-page/heartbeat/ai":
			list := ""
			for id := range monitors {
				list += fmt.Sprintf(`%q:%s,`, id, beatsJSON(up, down, 1, 1))
			}
			_, _ = w.Write([]byte("{" + fmt.Sprintf(`"heartbeatList":{%s},"uptimeList":{}`, trimTrailingComma(list)) + "}"))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}
}

// beatsJSON renders heartbeat entries: up beats with a ping, down beats with a
// null ping (exactly what Kuma stores), plus pending and maintenance beats.
func beatsJSON(up, down, pending, maintenance int) string {
	now := time.Now().UTC()
	timestamp := func(index int) string {
		return now.Add(-time.Duration(index) * time.Minute).Format("2006-01-02 15:04:05.000")
	}
	parts := make([]string, 0, up+down+pending+maintenance)
	index := 0
	add := func(status int, ping string) {
		parts = append(parts, fmt.Sprintf(`{"status":%d,"time":%q,"msg":"","ping":%s}`, status, timestamp(index), ping))
		index++
	}
	for i := 0; i < up; i++ {
		add(1, "1000")
	}
	for i := 0; i < down; i++ {
		add(0, "null")
	}
	for i := 0; i < pending; i++ {
		add(2, "null")
	}
	for i := 0; i < maintenance; i++ {
		add(3, "null")
	}
	return "[" + strings.Join(parts, ",") + "]"
}

func decodeBeats(raw string) []kumaBeat {
	var beats []kumaBeat
	if err := json.Unmarshal([]byte(raw), &beats); err != nil {
		panic(err)
	}
	return beats
}

func trimTrailingComma(value string) string {
	if len(value) > 0 && value[len(value)-1] == ',' {
		return value[:len(value)-1]
	}
	return value
}
