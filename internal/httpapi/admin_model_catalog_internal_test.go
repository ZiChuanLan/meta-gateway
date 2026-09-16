package httpapi

import (
	"testing"

	"github.com/lan/meta-gateway/internal/modelcatalog"
)

func TestModelCatalogURLsDropsUnknownSources(t *testing.T) {
	urls := modelCatalogURLs([]string{"litellm", "MODELS.DEV", "nope", ""})
	if len(urls) != 2 {
		t.Fatalf("urls = %v, want the two known sources", urls)
	}
	if urls["litellm"] != modelcatalog.DefaultURLs["litellm"] {
		t.Errorf("litellm url = %q", urls["litellm"])
	}
	// The lookup is case-folded so an operator's "Models.Dev" still resolves.
	if urls["models.dev"] != modelcatalog.DefaultURLs["models.dev"] {
		t.Errorf("models.dev url = %q", urls["models.dev"])
	}
	if len(modelCatalogURLs(nil)) != 0 {
		t.Error("no configured sources should produce no urls")
	}
}

func TestCatalogPolicyDefaultsToEveryGroup(t *testing.T) {
	policy := catalogPolicyRequest{}.policy()
	if !policy.Capabilities || !policy.Metadata || !policy.Prices {
		t.Fatalf("empty request = %+v, want everything on", policy)
	}
	off := false
	policy = catalogPolicyRequest{Prices: &off}.policy()
	if !policy.Capabilities || !policy.Metadata {
		t.Errorf("switching prices off must not touch the other groups: %+v", policy)
	}
	if policy.Prices {
		t.Error("prices should be off")
	}
	// An explicit false is honoured rather than treated as "unset".
	policy = catalogPolicyRequest{Capabilities: &off, Metadata: &off, Prices: &off}.policy()
	if policy.Capabilities || policy.Metadata || policy.Prices {
		t.Errorf("all-off request = %+v", policy)
	}
}
