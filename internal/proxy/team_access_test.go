package proxy

import (
	"context"
	"net/http"
	"testing"

	"github.com/lan/meta-gateway/internal/domain"
)

type unauthorizedTeamHook struct{ offered []string }

func TestTeamGrantDoesNotDelegateArbitraryUpstreamEndpoints(t *testing.T) {
	upstream := &queuedRelay{}
	service, _, high, _ := setupProxy(t, upstream)
	for _, req := range []Request{
		{Model: "model", OpenAIPath: "admin/tokens", Body: []byte(`{"model":"model"}`)},
		{Model: "model", Body: []byte(`{"model":"model","upstream_path":"admin/tokens"}`)},
		{Model: "model", Body: []byte(`{"model":"model"}`), Headers: map[string]string{"X-Meta-Upstream-Path": "admin/tokens"}},
	} {
		req.TeamAccess = &domain.TeamAccess{UserID: 1, Models: []string{"model"}, MemberIDs: map[int64]bool{high: true}}
		result, _ := service.ForwardWithMeta(context.Background(), req)
		if result.StatusCode != http.StatusForbidden || len(upstream.calls) != 0 {
			t.Fatalf("endpoint override escaped: %d", result.StatusCode)
		}
	}
}

func (h *unauthorizedTeamHook) Wants(point HookPoint, _ string) bool { return point == HookRoute }
func (h *unauthorizedTeamHook) Decide(_ context.Context, input HookInput) *HookResult {
	h.offered = input.AvailableModels
	return &HookResult{Handled: true, Model: "forbidden-model", PluginID: "test"}
}

func TestTeamPluginCannotBroadenModelAuthorization(t *testing.T) {
	upstream := &queuedRelay{}
	service, _, high, _ := setupProxy(t, upstream)
	hook := &unauthorizedTeamHook{}
	service.SetInterceptor(hook)
	result, _ := service.ForwardWithMeta(context.Background(), Request{Model: "model", Body: []byte(`{"model":"model","messages":[]}`),
		TeamAccess: &domain.TeamAccess{UserID: 1, Models: []string{"model"}, MemberIDs: map[int64]bool{high: true}}})
	if result.StatusCode != http.StatusForbidden || len(upstream.calls) != 0 {
		t.Fatalf("unauthorized rewrite forwarded: status=%d calls=%v", result.StatusCode, upstream.calls)
	}
	for _, model := range hook.offered {
		if model != "model" {
			t.Fatalf("hook offered unauthorized model %s", model)
		}
	}
}
