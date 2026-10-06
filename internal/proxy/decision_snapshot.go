package proxy

import (
	"encoding/json"
	"log"

	"github.com/lan/meta-gateway/internal/routing"
)

// decision_snapshot.go persists the explanation behind one selection round.

// insertDecisionSnapshot records the full explanation for an attempt: the
// candidates, their scores, the reasons rows were skipped, and the sticky /
// stable-first state. It survives even when the request later fails or the
// console is long gone, which is what makes "why did this go to that channel?"
// answerable after the fact. Errors carry whatever partial explanation the
// selector produced.
//
// Plugin decisions ride along in the same payload: a model rewritten by a hook
// is the reason this selection happened at all, and splitting the two across
// tables would leave the snapshot unable to explain itself. The extra key is
// additive, so older readers keep working.
//
// round is 1-based and matches the proxy_logs row this selection produces, so
// the log UI can show the decision behind EACH attempt.
func (s *Service) insertDecisionSnapshot(req Request, decision routing.Decision, round int) {
	snapshot := struct {
		routing.Explanation
		HookDecisions []HookDecision `json:"hook_decisions,omitempty"`
	}{Explanation: decision.Explanation}
	if len(req.HookDecisions) > 0 {
		snapshot.HookDecisions = req.HookDecisions
	}
	payload, err := json.Marshal(snapshot)
	if err != nil || len(payload) == 0 {
		return
	}
	selectedID := int64(0)
	if decision.Selected.Channel.ID > 0 {
		selectedID = decision.Selected.Channel.ID
	}
	if err := s.db.InsertDecisionSnapshot(req.RequestID, req.Model, decision.RouteID, selectedID, round, payload, s.now()); err != nil {
		log.Printf("proxy: decision snapshot request_id=%s: %v", req.RequestID, err)
	}
}
