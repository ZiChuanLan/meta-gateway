package proxy

import (
	"fmt"
	"log"
	"net/http"

	"github.com/lan/meta-gateway/internal/relay"
	"github.com/lan/meta-gateway/internal/store"
)

// forward_guards.go is the forwarding layer's half of the prompt-guard engine
// (prompt_guard.go owns rule matching): it decides WHEN each rule set runs and
// what a hit means for the attempt loop. It lives here rather than in the
// engine file so the engine stays free of relay results and request state.

// globalPromptGuards evaluates the channel-independent rules once per request and
// returns the enabled rule set for the per-channel pass.
//
// Once per request, not per attempt: re-running them for every channel retry did
// repeated DB reads and could mask or exclude differently after the first
// attempt. A "reject" ends the request; an "exclude" list retires those channels
// for the whole request; masking rewrites req.Body in place.
func (s *Service) globalPromptGuards(req *Request) (rules []store.PromptGuardRule, exclude []int64, reject *relay.Result) {
	if req.OpenAIPath != "chat/completions" || s.db == nil || s.db.PromptGuard == nil {
		return nil, nil, nil
	}
	loaded, err := s.db.PromptGuard.ListEnabled()
	if err != nil {
		return nil, nil, nil
	}
	rules = loaded
	guarded, hit, guardErr := ApplyPromptGuards(req.Body, promptGuardRulesForChannel(loaded, 0))
	if guardErr != nil {
		log.Printf("proxy: prompt guard eval model=%s: %v", req.Model, guardErr)
		return rules, nil, nil
	}
	if hit == nil {
		return rules, nil, nil
	}
	switch hit.Action {
	case "reject":
		return rules, nil, &relay.Result{StatusCode: http.StatusBadRequest, Err: fmt.Errorf("%w: %s", ErrGuardRejected, hit.Message)}
	case "exclude":
		log.Printf("proxy: prompt guard %q excludes channels %v for request (request_id=%s)", hit.Rule, hit.Exclude, req.RequestID)
		return rules, hit.Exclude, nil
	default:
		req.Body = guarded
		log.Printf("proxy: prompt guard %q masked request body (request_id=%s)", hit.Rule, req.RequestID)
		return rules, nil, nil
	}
}

// channelPromptGuards applies the rules scoped to one channel and reports what
// the attempt loop must do with the outcome: a replacement body, a terminal
// rejection, or "skip this channel" (the rule excluded the channel itself, which
// is a routing decision the caller owns because it has the excluded set).
func channelPromptGuards(rules []store.PromptGuardRule, channelID int64, model string, body []byte) (masked []byte, reject *relay.Result, exclude bool) {
	scoped := promptGuardRulesForChannel(rules, channelID)
	if len(scoped) == 0 {
		return body, nil, false
	}
	guarded, hit, err := ApplyPromptGuards(body, scoped)
	if err != nil {
		log.Printf("proxy: scoped prompt guard eval model=%s channel=%d: %v", model, channelID, err)
		return body, nil, false
	}
	if hit == nil {
		return body, nil, false
	}
	switch hit.Action {
	case "reject":
		return body, &relay.Result{StatusCode: http.StatusBadRequest, Err: fmt.Errorf("%w: %s", ErrGuardRejected, hit.Message)}, false
	case "exclude":
		return body, nil, true
	default:
		return guarded, nil, false
	}
}
