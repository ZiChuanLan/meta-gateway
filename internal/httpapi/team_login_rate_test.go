package httpapi

import (
	"context"
	"net/http/httptest"
	"net/netip"
	"testing"

	"github.com/lan/meta-gateway/internal/crypto"
	"github.com/lan/meta-gateway/internal/store"
)

// TestTeamLoginRateChargesSharedBucketOnlyForAdmittedRequests: a client that has
// already spent its own bucket keeps knocking, and those refused attempts must not
// also drain the shared ceiling — otherwise one client locks every other member
// out of signing in.
func TestTeamLoginRateChargesSharedBucketOnlyForAdmittedRequests(t *testing.T) {
	db, err := store.OpenTest(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	enc, err := crypto.New("team-login-rate-test-master-key-12345678")
	if err != nil {
		t.Fatal(err)
	}
	h := NewTeamHandler(db, enc)

	request := func(ip string) bool {
		r := httptest.NewRequest("POST", "/auth/login", nil)
		// ClientIP reads the resolver's own context value, which is what the
		// request path installs; that is the identity the buckets are keyed on.
		r = r.WithContext(context.WithValue(r.Context(), clientIPKey{}, netip.MustParseAddr(ip)))
		return h.admitLoginRate(httptest.NewRecorder(), r)
	}

	admitted := 0
	// Far more attempts than the shared burst (60), all from one source.
	for i := 0; i < 150; i++ {
		if request("203.0.113.9") {
			admitted++
		}
	}
	if admitted == 0 {
		t.Fatal("the first source was never admitted, so this proves nothing")
	}
	if request("203.0.113.10") {
		return
	}
	t.Fatal("one client's flood locked another client out of the login endpoint")
}
