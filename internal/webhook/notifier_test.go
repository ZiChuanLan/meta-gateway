package webhook

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestSendAlertDeliversToWebhookAndCoalesces verifies the multi-channel alert
// path: webhook delivery, content-signature cooldown, and channel isolation.
func TestSendAlertDeliversToWebhookAndCoalesces(t *testing.T) {
	var hits atomicCounter
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.add(1)
		var payload map[string]any
		_ = json.NewDecoder(r.Body).Decode(&payload)
		if payload["title"] != "测试告警" || payload["level"] != "warning" {
			t.Errorf("payload=%v", payload)
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)

	notifier := New("", 0)
	notifier.SetAlertConfig(AlertConfig{
		WebhookURL:      server.URL,
		CooldownSeconds: 60,
	})

	ctx := context.Background()
	if !notifier.SendAlert(ctx, AlertWarning, "测试告警", "消息一") {
		t.Fatal("first alert should deliver")
	}
	if notifier.SendAlert(ctx, AlertWarning, "测试告警", "消息一") {
		t.Fatal("identical alert inside cooldown must be coalesced")
	}
	// Different message → different signature → delivered.
	if !notifier.SendAlert(ctx, AlertWarning, "测试告警", "消息二") {
		t.Fatal("different message must bypass the cooldown")
	}
	if got := hits.value(); got != 2 {
		t.Fatalf("webhook hits=%d want 2", got)
	}
}

// TestSendAlertNoChannelsIsNoOp verifies a notifier without any channel does
// not panic and reports non-delivery.
func TestSendAlertNoChannelsIsNoOp(t *testing.T) {
	notifier := New("", 0)
	if notifier.SendAlert(context.Background(), AlertInfo, "t", "m") {
		t.Fatal("no channels configured → nothing delivered")
	}
}

// TestSendAlertLegacyWebhookURLStillWorks verifies the legacy single-webhook
// URL participates in the alert matrix.
func TestSendAlertLegacyWebhookURLStillWorks(t *testing.T) {
	var hits atomicCounter
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.add(1)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)

	notifier := New(server.URL, 0) // legacy URL path
	if !notifier.SendAlert(context.Background(), AlertError, "t", "m") {
		t.Fatal("legacy webhook URL should deliver alerts")
	}
	if hits.value() != 1 {
		t.Fatalf("hits=%d want 1", hits.value())
	}
}

type atomicCounter struct {
	n int64
}

func (c *atomicCounter) add(d int64) {
	// test-only, single goroutine
	c.n += d
}

func (c *atomicCounter) value() int64 { return c.n }

// TestSendAlertFailedDeliveryDoesNotArmCooldown verifies a transient delivery
// failure does not swallow subsequent identical alerts inside the window.
func TestSendAlertFailedDeliveryDoesNotArmCooldown(t *testing.T) {
	notifier := New("", 0)
	notifier.SetAlertConfig(AlertConfig{
		WebhookURL:      "http://127.0.0.1:1/unreachable", // connection refused
		CooldownSeconds: 60,
	})
	ctx := context.Background()
	if notifier.SendAlert(ctx, AlertWarning, "boom", "first") {
		t.Fatal("failed delivery must report non-delivery")
	}
	if notifier.SendAlert(ctx, AlertWarning, "boom", "first") {
		t.Fatal("failed delivery must report non-delivery again")
	}
	// No delivery ever succeeded → nothing armed; the second attempt was
	// actually attempted (not coalesced). Verify via a fresh notifier state is
	// hard, so assert the observable contract: both returned false because the
	// server is down, not because a cooldown swallowed the second one.
	var hits atomicCounter
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.add(1)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)
	notifier.SetAlertConfig(AlertConfig{WebhookURL: server.URL, CooldownSeconds: 60})
	if !notifier.SendAlert(ctx, AlertWarning, "boom", "first") {
		t.Fatal("delivery to a healthy endpoint must succeed after failure")
	}
	if got := hits.value(); got != 1 {
		t.Fatalf("healthy hits=%d want 1", got)
	}
}
