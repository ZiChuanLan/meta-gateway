package checkin

import (
	"testing"
	"time"
)

// {{today}} is the whole point of the body field: a campaign takes the day as an
// argument, so a body frozen at configuration time would claim the wrong day from
// tomorrow on.
func TestCheckinBodyExpandsToday(t *testing.T) {
	meta := `{"checkin_body":"{\"date\":\"{{today}}\"}","checkin_path":"/x"}`
	if got := checkinBodyOverride(meta, "2026-10-15"); got != `{"date":"2026-10-15"}` {
		t.Fatalf("body = %s", got)
	}
	// Every occurrence, not just the first.
	if got := checkinBodyOverride(`{"checkin_body":"{{today}}|{{today}}"}`, "2026-10-15"); got != "2026-10-15|2026-10-15" {
		t.Fatalf("repeated placeholder = %s", got)
	}
	// Nothing configured, and an unparsable meta, both mean "no body".
	if got := checkinBodyOverride(`{}`, "2026-10-15"); got != "" {
		t.Fatalf("empty meta = %q", got)
	}
	if got := checkinBodyOverride(`not json`, "2026-10-15"); got != "" {
		t.Fatalf("broken meta = %q", got)
	}
	// Without the placeholder the body is sent as written (validation happens on
	// save; this reader must not second-guess it).
	if got := checkinBodyOverride(`{"checkin_body":"{\"a\":1}"}`, "2026-10-15"); got != `{"a":1}` {
		t.Fatalf("static body = %s", got)
	}
}

// The day belongs to the check-in timezone, not to UTC: a run at 00:20 in
// Shanghai is still the previous day in UTC, and claiming that day would claim the
// wrong one.
func TestTodayFollowsTheCheckinTimezone(t *testing.T) {
	service := New(nil, nil, nil)
	shanghai, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatalf("load location: %v", err)
	}
	service.SetLocation(shanghai)
	service.now = func() time.Time {
		return time.Date(2026, 10, 15, 16, 30, 0, 0, time.UTC) // 2026-10-16 00:30 in Shanghai
	}
	if got := service.today(); got != "2026-10-16" {
		t.Fatalf("today = %s, want the Shanghai date", got)
	}
	service.SetLocation(time.UTC)
	if got := service.today(); got != "2026-10-15" {
		t.Fatalf("today = %s, want the UTC date", got)
	}
	// A nil location must not panic: it falls back to the process timezone.
	service.SetLocation(nil)
	if got := service.today(); got == "" {
		t.Fatal("today returned an empty date")
	}
}
