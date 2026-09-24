package proxy

import (
	"log"
	"strings"

	"github.com/lan/meta-gateway/internal/domain"
)

// ProviderProfile is the built-in endpoint + field mapping of a provider whose
// wire contract is not OpenAI chat.
//
// It exists so that "pick the provider in the console and paste a key" is
// enough, for upstreams that are not OpenAI-shaped. The console used to offer
// the same knowledge as a one-click preset button that filled four raw JSON
// boxes: the operator then owned a protocol contract they could not verify, and
// the two other ways a channel comes into existence (an imported channel, a
// channel created through the API) never saw the button at all. The mapping
// behind a provider is a property of the provider, so it lives next to the
// adapters — and the console's mapping fields remain for overriding or
// inspecting what the profile filled.
//
// A profile fills a channel only where the operator left the mapping empty; see
// ApplyProviderProfile.
type ProviderProfile struct {
	// Type is the console provider value, stored as channel.type_hint or
	// site.platform.
	Type string
	// Aliases are alternative spellings that resolve to the same profile.
	Aliases []string
	// PathOverride replaces the OpenAI path; see NormalizeEndpointPath.
	PathOverride string
	// RequestMap / ResponseMap move fields between the OpenAI body and the
	// upstream's own shape (see UpstreamFieldMap).
	RequestMap  string
	ResponseMap string
	// MatchPaths are endpoint paths that identify this provider even when the
	// operator typed something else — a channel pointed straight at
	// `…/v1/systemone` while carrying the New API type, say. Only runtime
	// capabilities (ReasoningLevels) are matched this way; the save-time field
	// maps are not, because an operator who wrote their own map keeps it.
	MatchPaths []string
	// ReasoningLevels, when non-empty, is the subset of the gateway's effort
	// ladder this provider accepts. A request asking for a rung outside the set
	// is rewritten to the nearest accepted rung at or below it before
	// forwarding, instead of coming back as a 400.
	ReasoningLevels []string
}

// TypeSafe System One is not an OpenAI surface at all: `POST /v1/systemone`
// takes {state, model, questions} and answers {model, answers, usage}.
//
// Every entry below was measured against the live API on 2026-09-23:
//
//   - `questions` is a MAP keyed by question id, not an array;
//   - a typed answer comes back as a NUMBER under `answers.<id>.<type>`
//     (noul = 0.94), so it needs a template to reach OpenAI's string `content`;
//   - the request model is strict, so the body must be reduced to exactly the
//     keys it knows: a body carrying one extra field (even `temperature`) is
//     answered with 400 api_usage_error. The `keep` entry is what makes an
//     ordinary OpenAI client body acceptable — `messages` is read first, then
//     dropped along with everything else the client sent.
//
// Model discovery needs none of this: `GET /v1/models` is the conventional path
// and the save-time split of a pasted `…/v1/systemone` base URL lands the root
// on it (see SplitEndpointBaseURL). TypeSafe answers it with
// {"models":[{"name":"jev-latest"}]}, which the OpenAI adapter accepts.
//
// Scope note: this maps ONE question onto the call. System One scores a state
// against a question, so a multi-turn conversation is not expressible here — a
// client that sends a leading system message has that message become the state,
// and an operator who wants different semantics edits the mapping.
const (
	typesafeRequestMap = `[
  {"from":"messages.0.content","to":"state"},
  {"keep":["model","state"]},
  {"to":"questions.answer.type","value":{"str":"noul"}},
  {"to":"questions.answer.instructions","value":{"str":"Respond to the state."}}
]`

	typesafeResponseMap = `[
  {"to":"object","value":{"str":"chat.completion"}},
  {"to":"choices.0.message.role","value":{"str":"assistant"}},
  {"to":"choices.0.message.content","template":"{answers.answer.noul}"},
  {"to":"choices.0.finish_reason","value":{"str":"stop"}},
  {"from":"usage.input_tokens","to":"usage.prompt_tokens"},
  {"from":"usage.output_tokens","to":"usage.completion_tokens"}
]`
)

// providerProfiles is the registry. Keys are matched after lower-casing and
// trimming, so the exact spelling in the console never matters.
var providerProfiles = []ProviderProfile{
	{
		Type:         "typesafe",
		Aliases:      []string{"typesafe-systemone", "typesafe-system-one", "systemone"},
		PathOverride: "v1/systemone",
		RequestMap:   typesafeRequestMap,
		ResponseMap:  typesafeResponseMap,
		// The System One surface validates reasoning_effort against this exact
		// list and rejects everything else: "field ReasoningEffort invalid,
		// should be one of: low, medium, high, xhigh, none" (live API,
		// 2026-09-25). `minimal` and `max` are gateway-ladder rungs it does not
		// know, so they are clamped to `none` and `xhigh` respectively.
		ReasoningLevels: []string{"none", "low", "medium", "high", "xhigh"},
		MatchPaths:      []string{"v1/systemone"},
	},
}

// LookupProviderProfile finds the profile for a provider value.
func LookupProviderProfile(providerType string) (ProviderProfile, bool) {
	key := strings.ToLower(strings.TrimSpace(providerType))
	if key == "" {
		return ProviderProfile{}, false
	}
	for _, profile := range providerProfiles {
		if key == profile.Type {
			return profile, true
		}
		for _, alias := range profile.Aliases {
			if key == alias {
				return profile, true
			}
		}
	}
	return ProviderProfile{}, false
}

// AcceptedReasoningLevels reports the effort rungs the upstream behind this
// channel understands.
//
// The channel's declared provider decides; when that names no profile, the
// resolved endpoint path does — the protocol lives at the endpoint, and an
// operator who pointed a New API channel at `…/v1/systemone` still needs the
// System One vocabulary. An empty result means "no opinion": values pass
// through untouched, which stays the default for every unknown provider.
func AcceptedReasoningLevels(providerType, endpointPath string) []string {
	if profile, ok := LookupProviderProfile(providerType); ok && len(profile.ReasoningLevels) > 0 {
		return profile.ReasoningLevels
	}
	normalized := NormalizeEndpointPath(endpointPath)
	if normalized == "" {
		return nil
	}
	for _, profile := range providerProfiles {
		for _, match := range profile.MatchPaths {
			pattern := NormalizeEndpointPath(match)
			// Suffix match: NormalizeEndpointPath keeps the leading slash, so a
			// pattern already ends on a segment boundary. `v1/systemone` also
			// describes the endpoint of a gateway that mounts it deeper
			// (`…/api/v1/systemone`) — the last segments identify a protocol.
			if pattern != "" && strings.HasSuffix(normalized, pattern) {
				return profile.ReasoningLevels
			}
		}
	}
	return nil
}

// ApplyProviderProfile fills a channel's endpoint / field mapping from the
// built-in profile of its provider. It reports whether it wrote anything.
//
// It fills only empty fields, and it stays out of the way of an operator who has
// written a field map of their own: a channel that already carries a request or
// response map is taken to be deliberately configured and is left completely
// alone. The path override is exempt from that gate because the save-time split
// of a pasted full endpoint URL (`…/v1/systemone`) writes that very field — the
// same intent, not a hand-written mapping — so a profile may still fill its own
// override there.
//
// The maps are pushed through ValidateUpstreamMap, so a profile can never store
// a mapping the admin API would have rejected; a broken one is reported at
// startup-time in tests (TestProviderProfilesAreValidMappings) instead of
// failing open at request time.
func ApplyProviderProfile(ch *domain.Channel, providerType string) bool {
	if ch == nil {
		return false
	}
	profile, ok := LookupProviderProfile(providerType)
	if !ok {
		return false
	}
	if strings.TrimSpace(ch.UpstreamRequestMap) != "" || strings.TrimSpace(ch.UpstreamResponseMap) != "" {
		return false
	}
	override, pathMap, requestMap, responseMap, err := ValidateUpstreamMap(
		firstNonEmpty(ch.UpstreamPathOverride, profile.PathOverride),
		ch.UpstreamPathMap,
		firstNonEmpty(ch.UpstreamRequestMap, profile.RequestMap),
		firstNonEmpty(ch.UpstreamResponseMap, profile.ResponseMap),
	)
	if err != nil {
		log.Printf("proxy: provider profile %s is not a valid mapping: %v", profile.Type, err)
		return false
	}
	if override == ch.UpstreamPathOverride && pathMap == ch.UpstreamPathMap &&
		requestMap == ch.UpstreamRequestMap && responseMap == ch.UpstreamResponseMap {
		return false
	}
	ch.UpstreamPathOverride = override
	ch.UpstreamPathMap = pathMap
	ch.UpstreamRequestMap = requestMap
	ch.UpstreamResponseMap = responseMap
	return true
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
