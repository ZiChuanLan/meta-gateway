package httpapi

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lan/meta-gateway/internal/domain"
)

// TestTeamRouteModelNameIsPercentDecoded: the console percent-encodes a model
// name in the path, because names carry reserved characters (cn:auto). chi hands
// the handler the escaped segment, and an undecoded one lands the arrangement on
// a model the caller never named.
func TestTeamRouteModelNameIsPercentDecoded(t *testing.T) {
	e := newTeamTestEnv(t)
	e.enable()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"id":"test","choices":[{"message":{"role":"assistant","content":"ok"}}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`)
	}))
	t.Cleanup(upstream.Close)
	secret, _ := e.enc.Encrypt([]byte("upstream-secret"))
	site, _ := e.db.Site.Create(&domain.Site{Name: "Upstream", Status: domain.StatusEnabled})
	cred, _ := e.db.Credential.Create(&domain.Credential{SiteID: site, Kind: "api_key", SecretEnc: []byte(secret), Status: domain.StatusEnabled})
	channelID, err := e.db.Channel.Create(&domain.Channel{SiteID: &site, CredentialID: &cred, Name: "colon", BaseURL: upstream.URL, TypeHint: "openai", Status: domain.StatusEnabled})
	if err != nil {
		t.Fatal(err)
	}
	route, _ := e.db.Route.Create(&domain.Route{ModelPattern: "cn:auto", Enabled: true})
	member, _ := e.db.RouteMember.Create(&domain.RouteMember{RouteID: route, ChannelID: channelID, Priority: 10, Weight: 100, Enabled: true})
	e.admin("PUT", "/admin/team/policies/1", TeamPolicy{ID: 1, Name: "Granted", Models: []string{"cn:auto"},
		MemberIDs: []int64{member}, MaxKeys: 5, RPM: 120, AllowRouting: true}, 200)
	alice := e.member("alice", "member")

	alice.request("PUT", "/me/routes/cn%3Aauto", teamRouteOrderInput{Entries: []domain.TeamRouteEntry{{ID: member, Weight: 100}}}, 200)
	raw := string(alice.request("GET", "/me/routes/cn%3Aauto", nil, 200))
	if !strings.Contains(raw, `"model":"cn:auto"`) {
		t.Fatalf("escaped model name was not decoded: %s", raw)
	}
}
