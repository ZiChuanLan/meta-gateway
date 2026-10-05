package httpapi

import (
	"encoding/json"
	"testing"
)

// Double clicking or replaying the same voucher concurrently must credit once,
// independent of the frontend lock. Exercise the real HTTP/transaction path.
func TestConcurrentCreditRedemptionCreditsAccountOnlyOnce(t *testing.T) {
	e := newTeamTestEnv(t)
	e.enable()
	alice := e.member("concurrent-alice", "member")
	_, codes := mintCodes(t, e, map[string]any{"kind": "credit", "count": 1, "quota_tokens": 1000, "max_uses": 5})
	start := make(chan struct{})
	statuses := make(chan int, 2)
	for i := 0; i < 2; i++ {
		go func() {
			<-start
			status, _, _ := e.call(alice.client, "POST", "/me/redeem", map[string]string{"code": codes[0]}, "", alice.csrf)
			statuses <- status
		}()
	}
	close(start)
	first, second := <-statuses, <-statuses
	if !((first == 200 && second == 409) || (first == 409 && second == 200)) {
		t.Fatalf("concurrent statuses: %d %d", first, second)
	}
	var account struct{ Credit teamCreditView }
	if err := json.Unmarshal(alice.request("GET", "/me", nil, 200), &account); err != nil {
		t.Fatal(err)
	}
	if account.Credit.Total != 1000 {
		t.Fatalf("credited more than once: %+v", account.Credit)
	}
}
