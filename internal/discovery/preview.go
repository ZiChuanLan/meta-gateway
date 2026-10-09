package discovery

import (
	"context"
	"errors"
	"net/url"
	"sort"
	"strings"

	"github.com/lan/meta-gateway/internal/adapters"
	"github.com/lan/meta-gateway/internal/domain"
)

// PreviewResult is what the create dialog's 获取模型 shows: the models an
// upstream would serve, plus which adapter answered.
type PreviewResult struct {
	Adapter string   `json:"adapter"`
	Models  []string `json:"models"`
}

// PreviewModels lists the models behind a base URL and a key that are not stored
// anywhere yet. The connection dialog needs this before the channel exists:
// asking the operator to create the channel first and discover after means every
// typo in the URL or the key leaves a half-configured channel (and a site, and a
// credential) behind, and adopting models from auto-discovery is exactly the step
// being configured.
//
// Read-only by construction: no probe record, no health history, no model
// adoption, no channel row. The adapter and the endpoint split are the ones the
// relay uses, so what the dialog lists is what a saved channel would have found.
func (s *Service) PreviewModels(ctx context.Context, baseURL, typeHint, secret string) (*PreviewResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		return nil, unavailableError(domain.CategoryInvalidBaseURL)
	}
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.User != nil {
		// Same category and status a probe reports for an unusable base URL, so
		// the dialog shows the console's existing wording for it rather than a
		// second, preview-only message.
		return nil, unavailableError(domain.CategoryInvalidBaseURL)
	}
	typeHint = strings.TrimSpace(typeHint)
	if typeHint == "" {
		typeHint = "openai-compatible"
	}
	if s.registry == nil {
		return nil, unavailableError("unsupported_adapter")
	}
	// The create path splits a pasted endpoint off the base URL before storing it
	// (base root + upstream_path_override), so the listing must run against the
	// same root a stored channel would carry — otherwise pasting
	// `https://api.typesafe.ai/v1/systemone` would list models from a URL no
	// channel ever uses.
	if root, _, splitErr := adapters.SplitEndpointBaseURL(baseURL); splitErr == nil && root != "" {
		baseURL = root
	}
	adapter, ok := s.registry.Resolve(typeHint, "")
	if !ok {
		return nil, unavailableError("unsupported_adapter")
	}
	secret = strings.TrimSpace(secret)
	if secret == "" {
		return nil, unavailableError(domain.CategoryCredentialUnavailable)
	}
	listed, listErr := adapter.ListModels(ctx, baseURL, secret)
	if listErr != nil {
		if errors.Is(listErr, context.Canceled) || errors.Is(listErr, context.DeadlineExceeded) {
			return nil, listErr
		}
		var adapterErr *adapters.Error
		if errors.As(listErr, &adapterErr) && adapterErr.Kind == adapters.ErrorInvalidURL {
			return nil, unavailableError(domain.CategoryInvalidBaseURL)
		}
		return nil, &Error{Kind: ErrorUpstream, Category: categoryForListError(listErr, "api_key")}
	}
	seen := make(map[string]struct{}, len(listed))
	models := make([]string, 0, len(listed))
	for _, model := range listed {
		model = strings.TrimSpace(model)
		if model == "" {
			continue
		}
		if _, dup := seen[model]; dup {
			continue
		}
		seen[model] = struct{}{}
		models = append(models, model)
	}
	sort.Strings(models)
	return &PreviewResult{Adapter: adapter.Name(), Models: models}, nil
}

// categoryForListError maps an adapter listing error onto the console's error
// catalogue. Probe and PreviewModels both report through it: the same upstream
// failure must read the same in a refresh and in the create dialog, and a second
// mapping written by hand is how those two drift apart.
//
// credentialKind is the kind of the credential that failed ("api_key" when the
// caller has only a raw key). An access token or session cookie answering 401/403
// on /v1/models is expected on many New API hosts and is a different message from
// a rejected API key.
func categoryForListError(listErr error, credentialKind string) string {
	category := domain.CategoryUpstreamFailure
	var adapterErr *adapters.Error
	if !errors.As(listErr, &adapterErr) {
		return category
	}
	category = string(adapterErr.Kind)
	// 401/403 on /v1/models with a user access_token is expected on many New API hosts.
	if adapterErr.Kind == adapters.ErrorStatus && (adapterErr.Status == 401 || adapterErr.Status == 403) {
		kind := strings.ToLower(strings.TrimSpace(credentialKind))
		if kind == "access_token" || kind == "session" {
			category = domain.CategoryUserTokenNotForModels
		} else {
			category = domain.CategoryUpstreamUnauthorized
		}
	}
	return category
}
