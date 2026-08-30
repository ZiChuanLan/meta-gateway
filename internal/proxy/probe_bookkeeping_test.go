package proxy

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/lan/meta-gateway/internal/relay"
)

// probeRequest pins a probe at the low-priority channel.
func probeRequest(channelID int64) Request {
	return Request{
		RequestID:       "probe-bookkeeping",
		Model:           "model",
		Body:            []byte(`{"model":"model","messages":[{"role":"user","content":"hi"}]}`),
		PreferChannelID: channelID,
		Probe:           true,
	}
}

// A probe success is the answer the operator asked for, not a health signal:
// it must not clear the member's cooldown marks or reset the channel's
// consecutive-failure counter.
func TestProbeSuccessLeavesBookkeepingUntouched(t *testing.T) {
	upstream := &queuedRelay{results: []*relay.Result{response(http.StatusOK, `{"ok":true}`)}}
	service, db, _, lowMemberID := setupProxy(t, upstream)
	member, err := db.RouteMember.GetByID(lowMemberID)
	if err != nil {
		t.Fatal(err)
	}
	lowChannelID := member.ChannelID
	now := time.Date(2026, 7, 14, 0, 0, 0, 0, time.UTC)

	// Pre-seed real-traffic bookkeeping: failure marks on the member (cooldown
	// already expired, so the pinned channel stays eligible) and a channel
	// relay failure.
	if err := db.RouteMember.RecordFailure(lowMemberID, now, time.Minute, "upstream_500"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE route_members SET cooldown_until = ? WHERE id = ?`,
		now.Add(-time.Minute).UTC().Format(time.RFC3339Nano), lowMemberID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Channel.RecordRelayFailure(lowChannelID); err != nil {
		t.Fatal(err)
	}

	result := service.ChatCompletions(context.Background(), probeRequest(lowChannelID))
	if result.Err != nil || result.StatusCode != http.StatusOK {
		t.Fatalf("probe result = %+v, want 200", result)
	}

	got, err := db.RouteMember.GetByID(lowMemberID)
	if err != nil {
		t.Fatal(err)
	}
	if got.FailCount == 0 {
		t.Error("probe success cleared the member's failure marks: probe traffic must not look like real traffic")
	}
	if got.CooldownUntil == nil {
		t.Error("probe success cleared the member's cooldown")
	}
	var channelFailures int
	if err := db.QueryRow(`SELECT consecutive_failures FROM channels WHERE id = ?`, lowChannelID).Scan(&channelFailures); err != nil {
		t.Fatal(err)
	}
	if channelFailures != 1 {
		t.Errorf("channel consecutive_failures = %d, want 1: a probe must not reset the relay counter", channelFailures)
	}
}

// A probe 4xx is information, not a fault: no member cooldown, no error marks.
func TestProbe4xxLeavesMemberBookkeepingUntouched(t *testing.T) {
	upstream := &queuedRelay{results: []*relay.Result{response(http.StatusBadRequest, `{"error":{"message":"bad request"}}`)}}
	service, db, _, lowMemberID := setupProxy(t, upstream)
	member, err := db.RouteMember.GetByID(lowMemberID)
	if err != nil {
		t.Fatal(err)
	}

	result := service.ChatCompletions(context.Background(), probeRequest(member.ChannelID))
	if result.StatusCode != http.StatusBadRequest {
		t.Fatalf("probe result status = %d, want 400", result.StatusCode)
	}

	got, err := db.RouteMember.GetByID(lowMemberID)
	if err != nil {
		t.Fatal(err)
	}
	if got.FailCount != 0 || got.CooldownUntil != nil || got.LastError != "" {
		t.Errorf("probe 4xx polluted member bookkeeping: fail_count=%d cooldown=%v last_error=%q",
			got.FailCount, got.CooldownUntil, got.LastError)
	}
}

// The one 4xx side effect probes keep: discovering a dead model name blacklists
// it. But even then the member itself must not be cooled down.
func TestProbeNotFoundBlacklistsModelWithoutCoolingMember(t *testing.T) {
	upstream := &queuedRelay{results: []*relay.Result{response(http.StatusNotFound, `{"error":{"message":"No such model: model"}}`)}}
	service, db, _, lowMemberID := setupProxy(t, upstream)
	member, err := db.RouteMember.GetByID(lowMemberID)
	if err != nil {
		t.Fatal(err)
	}

	result := service.ChatCompletions(context.Background(), probeRequest(member.ChannelID))
	if result.StatusCode != http.StatusNotFound {
		t.Fatalf("probe result status = %d, want 404", result.StatusCode)
	}

	blocked, err := db.IsModelBlocked(member.ChannelID, "model")
	if err != nil {
		t.Fatal(err)
	}
	if !blocked {
		t.Error("probe 404 did not blacklist the model; the discovery write must stay probe-visible")
	}

	got, err := db.RouteMember.GetByID(lowMemberID)
	if err != nil {
		t.Fatal(err)
	}
	if got.FailCount != 0 || got.CooldownUntil != nil {
		t.Errorf("probe 404 cooled the member: fail_count=%d cooldown=%v", got.FailCount, got.CooldownUntil)
	}
}
