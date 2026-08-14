package proxy

import (
	"context"
	"net/http"
	"testing"

	"github.com/lan/meta-gateway/internal/relay"
	"github.com/lan/meta-gateway/internal/store"
)

// fakeRefresher records calls and reports success/failure.
type fakeRefresher struct {
	calls int
	ok    bool
}

func (f *fakeRefresher) RefreshForRelay(_ context.Context, _ int64) (bool, error) {
	f.calls++
	return f.ok, nil
}

// TestRefreshRetry401 verifies the 401 → refresh → replay path: the
// refresher is called exactly once, the replay succeeds, and the log row
// carries the refresh_retry category.
func TestRefreshRetry401(t *testing.T) {
	upstream := &queuedRelay{results: []*relay.Result{
		response(http.StatusUnauthorized, `{"error":{"message":"session expired","code":"invalid_token"}}`),
		response(http.StatusOK, `{"id":"c1","object":"chat.completion","model":"model","choices":[]}`),
	}}
	service, db, highMember, _ := setupProxy(t, upstream)

	// The setup credential is kind=api_key (not refreshable); switch it to
	// session so the refresh path engages.
	member, err := db.RouteMember.GetByID(highMember)
	if err != nil {
		t.Fatal(err)
	}
	channel, err := db.Channel.GetByID(member.ChannelID)
	if err != nil {
		t.Fatal(err)
	}
	cred, err := db.Credential.GetByID(*channel.CredentialID)
	if err != nil {
		t.Fatal(err)
	}
	cred.Kind = "session"
	if err := db.Credential.Update(cred); err != nil {
		t.Fatal(err)
	}

	refresher := &fakeRefresher{ok: true}
	service.SetCredentialRefresher(refresher)

	result := service.ChatCompletions(context.Background(), Request{
		RequestID: "req-refresh",
		Model:     "model",
		Body:      []byte(`{}`),
	})
	if result == nil || result.Err != nil || result.StatusCode != http.StatusOK {
		t.Fatalf("replay result = %+v, want 200", result)
	}
	if refresher.calls != 1 {
		t.Fatalf("refresher calls = %d, want 1", refresher.calls)
	}
	if len(upstream.calls) != 2 {
		t.Fatalf("upstream calls = %d, want 2 (401 + replay)", len(upstream.calls))
	}

	// The attempt log must carry the refresh_retry attribution.
	logs, err := db.ProxyLog.ListFilter(store.ProxyLogFilter{})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, log := range logs {
		if log.RequestID == "req-refresh" && log.ErrorBrief == "refresh_retry" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("no refresh_retry log row: %+v", logs)
	}
}

// TestRefreshRetryFailureFallsThrough verifies a failed refresh keeps the
// normal 401 failover path (no replay, no refresh_retry attribution).
func TestRefreshRetryFailureFallsThrough(t *testing.T) {
	upstream := &queuedRelay{results: []*relay.Result{
		response(http.StatusUnauthorized, `{"error":{"message":"session expired","code":"invalid_token"}}`),
		response(http.StatusOK, `{"id":"c2","object":"chat.completion","model":"model","choices":[]}`),
	}}
	service, db, highMember, _ := setupProxy(t, upstream)
	member, _ := db.RouteMember.GetByID(highMember)
	channel, _ := db.Channel.GetByID(member.ChannelID)
	cred, _ := db.Credential.GetByID(*channel.CredentialID)
	cred.Kind = "session"
	_ = db.Credential.Update(cred)

	refresher := &fakeRefresher{ok: false} // refresh fails
	service.SetCredentialRefresher(refresher)

	result := service.ChatCompletions(context.Background(), Request{
		RequestID: "req-refresh-fail",
		Model:     "model",
		Body:      []byte(`{}`),
	})
	if result == nil || result.Err != nil || result.StatusCode != http.StatusOK {
		t.Fatalf("failover result = %+v, want 200 (second channel)", result)
	}
	if refresher.calls != 1 {
		t.Fatalf("refresher calls = %d, want 1 (attempted once)", refresher.calls)
	}
	logs, err := db.ProxyLog.ListFilter(store.ProxyLogFilter{})
	if err != nil {
		t.Fatal(err)
	}
	for _, log := range logs {
		if log.RequestID == "req-refresh-fail" && log.ErrorBrief == "refresh_retry" {
			t.Fatalf("refresh_retry logged despite failed refresh: %+v", log)
		}
	}
}

// TestRefreshRetryAPIKeyKindSkips verifies api_key credentials never trigger
// the refresh path even on 401.
func TestRefreshRetryAPIKeyKindSkips(t *testing.T) {
	upstream := &queuedRelay{results: []*relay.Result{
		response(http.StatusUnauthorized, `{"error":{"message":"invalid api key"}}`),
		response(http.StatusOK, `{"id":"c3","object":"chat.completion","model":"model","choices":[]}`),
	}}
	service, _, _, _ := setupProxy(t, upstream) // credential kind stays api_key
	refresher := &fakeRefresher{ok: true}
	service.SetCredentialRefresher(refresher)

	result := service.ChatCompletions(context.Background(), Request{
		RequestID: "req-refresh-skip",
		Model:     "model",
		Body:      []byte(`{}`),
	})
	if result == nil || result.Err != nil || result.StatusCode != http.StatusOK {
		t.Fatalf("failover result = %+v, want 200", result)
	}
	if refresher.calls != 0 {
		t.Fatalf("refresher calls = %d, want 0 (api_key not refreshable)", refresher.calls)
	}
}
