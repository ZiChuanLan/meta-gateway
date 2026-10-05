package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/lan/meta-gateway/internal/domain"
)

// mintCodes creates a batch and returns the ids and plaintext codes.
func mintCodes(t *testing.T, e *teamTestEnv, body map[string]any) ([]int64, []string) {
	t.Helper()
	raw := e.admin("POST", "/admin/team/codes", body, 201)
	var minted struct {
		Codes []struct {
			ID   int64  `json:"id"`
			Code string `json:"code"`
		} `json:"codes"`
	}
	if err := json.Unmarshal(raw, &minted); err != nil {
		t.Fatal(err)
	}
	ids := make([]int64, 0, len(minted.Codes))
	codes := make([]string, 0, len(minted.Codes))
	for _, item := range minted.Codes {
		ids = append(ids, item.ID)
		codes = append(codes, item.Code)
	}
	return ids, codes
}

// acceptWithCode registers an account with an invitation code.
func acceptWithCode(t *testing.T, e *teamTestEnv, code, username string, want int) []byte {
	t.Helper()
	b := e.browser()
	return b.request("POST", "/auth/accept", map[string]any{
		"username": username, "name": username, "password": "member-password-123", "token": code,
	}, want)
}

// A batch code is what an operator hands to a class: one code, N sign-ups, and
// an optional starting credit that lands in the new account.
func TestTeamBatchInviteCodes(t *testing.T) {
	e := newTeamTestEnv(t)
	e.enable()

	_, codes := mintCodes(t, e, map[string]any{
		"kind": "invite", "count": 3, "policy_id": 1, "max_uses": 2,
		"expires_in_hours": 24, "quota_tokens": 5000, "label": "class",
	})
	if len(codes) != 3 {
		t.Fatalf("minted %d codes, want 3", len(codes))
	}
	// Diagnostic: what the table actually holds, so a rejected accept can be
	// told apart from a code that was never stored the way it was returned.
	var storedHash string
	if err := e.db.QueryRow(`SELECT token_hash FROM team_invites ORDER BY id LIMIT 1`).Scan(&storedHash); err != nil {
		t.Fatal(err)
	}
	if want := teamHash(stripTeamCode(codes[0])); storedHash != want {
		t.Fatalf("stored hash %s != hash of the stripped code %s (code=%q)", storedHash, want, codes[0])
	}
	// A code may be pasted back in a different spelling (lower case, spaces
	// instead of dashes); every candidate must still land on the same row.
	for _, spelling := range []string{codes[0], strings.ToLower(codes[0]), strings.ReplaceAll(codes[0], "-", " ")} {
		matched := false
		for _, candidate := range teamCodeCandidates(spelling) {
			if candidate == storedHash {
				matched = true
				break
			}
		}
		if !matched {
			t.Fatalf("spelling %q does not resolve to the stored code", spelling)
		}
	}
	for _, code := range codes {
		if len(code) != 19 || strings.Count(code, "-") != 3 {
			t.Fatalf("code %q is not the readable XXXX-XXXX-XXXX-XXXX shape", code)
		}
	}

	// Two registrations per code; the credit arrives with the account.
	b := e.browser()
	code, body, _ := e.call(b.client, "POST", "/auth/accept", map[string]any{
		"username": "first", "name": "first", "password": "member-password-123", "token": codes[0],
	}, "", b.csrf)
	if code != 200 {
		t.Fatalf("accept=%d body=%s", code, body)
	}
	var created struct {
		User TeamUser
	}
	json.Unmarshal(body, &created)
	if created.User.QuotaTotalTokens != 5000 {
		t.Fatalf("signup credit = %d, want the code's 5000", created.User.QuotaTotalTokens)
	}
	acceptWithCode(t, e, codes[0], "second", 200)
	// Third use exceeds max_uses: the code is spent, not an error to retry.
	acceptWithCode(t, e, codes[0], "third", 400)

	// Revoking a code stops it immediately. The accept endpoint is rate limited
	// (5 attempts a minute), and every remaining check here is a real one — the
	// code-shape and spelling coverage lives in the pure-function assertions
	// above, which need no HTTP at all.
	ids, revoked := mintCodes(t, e, map[string]any{"kind": "invite", "count": 1, "policy_id": 1})
	e.admin("DELETE", "/admin/team/codes/"+strconv.FormatInt(ids[0], 10), nil, 200)
	acceptWithCode(t, e, revoked[0], "sixth", 400)

	// The board lists what exists, without ever echoing a code.
	list := e.admin("GET", "/admin/team/codes?kind=invite", nil, 200)
	if strings.Contains(string(list), codes[0]) {
		t.Fatal("the code list leaked a plaintext code")
	}
	var views []teamCodeView
	json.Unmarshal(list, &views)
	if len(views) < 4 {
		t.Fatalf("code board = %d rows, want at least 4", len(views))
	}
}

// A credit voucher tops up the signed-in member's own pool. It is an account
// operation, not a relay one, and one account cannot redeem the same code twice
// even when the code has uses left for other people.
func TestTeamCreditCodeRedemption(t *testing.T) {
	e := newTeamTestEnv(t)
	e.enable()
	alice := e.member("alice", "member")

	_, shared := mintCodes(t, e, map[string]any{"kind": "credit", "count": 1, "quota_tokens": 1000, "max_uses": 5})
	e.admin("PUT", "/admin/team/policies/1", TeamPolicy{ID: 1, Name: "P", Models: []string{}, MemberIDs: []int64{}, MaxKeys: 5, RPM: 120}, 200)

	raw := alice.request("POST", "/me/redeem", map[string]any{"code": shared[0]}, 200)
	var first struct {
		Granted        int64 `json:"granted"`
		QuotaTotal     int64 `json:"quota_total"`
		QuotaAvailable int64 `json:"quota_available"`
	}
	json.Unmarshal(raw, &first)
	if first.Granted != 1000 || first.QuotaTotal != 1000 || first.QuotaAvailable != 1000 {
		t.Fatalf("redeem = %+v, want a 1000-token grant", first)
	}
	// Second attempt by the same account: the unique redemption index refuses
	// it, and the pool must NOT grow.
	alice.request("POST", "/me/redeem", map[string]any{"code": shared[0]}, 409)
	me := alice.request("GET", "/me", nil, 200)
	var account struct {
		Credit teamCreditView `json:"credit"`
	}
	json.Unmarshal(me, &account)
	if account.Credit.Total != 1000 || account.Credit.Used != 0 {
		t.Fatalf("pool after a refused double redeem = %+v", account.Credit)
	}

	// A single-use voucher is spent by the first taker.
	_, single := mintCodes(t, e, map[string]any{"kind": "credit", "count": 1, "quota_tokens": 250})
	bob := e.member("bob", "member")
	bob.request("POST", "/me/redeem", map[string]any{"code": single[0]}, 200)
	carol := e.member("carol", "member")
	carol.request("POST", "/me/redeem", map[string]any{"code": single[0]}, 400)
	// An unknown or malformed code is refused the same way.
	carol.request("POST", "/me/redeem", map[string]any{"code": "NOPE-NOPE-NOPE-NOPE"}, 400)
	// A credit code is not a signup code.
	acceptWithCode(t, e, single[0], "dave", 400)
}

// Direct creation, pasted imports and bulk actions: the three things an
// operator needs when they already know who is being onboarded.
func TestTeamMemberAdministration(t *testing.T) {
	e := newTeamTestEnv(t)
	e.enable()

	// Create with a generated password, then sign in with it.
	raw := e.admin("POST", "/admin/team/users", map[string]any{"username": "direct", "name": "Direct", "policy_id": 1, "quota_tokens": 750}, 201)
	var made struct {
		User     TeamUser `json:"user"`
		Password string   `json:"password"`
	}
	json.Unmarshal(raw, &made)
	if made.Password == "" || made.User.QuotaTotalTokens != 750 {
		t.Fatalf("create = %+v", made)
	}
	e.admin("POST", "/admin/team/users", map[string]any{"username": "direct", "name": "Direct", "policy_id": 1}, 409)

	b := e.browser()
	b.request("POST", "/auth/login", map[string]string{"username": "direct", "password": made.Password}, 200)

	// Paste a list: one good row, one duplicate, one with an unusable username.
	importRaw := e.admin("POST", "/admin/team/users/import", map[string]any{
		"text":      "# team roster\nimported,Imported One\nDIRECT,duplicate\ntaken,Imported Two,secret-password-9,2k\n",
		"policy_id": 1,
	}, 200)
	var imported struct {
		Created []struct {
			ID       int64  `json:"id"`
			Username string `json:"username"`
			Password string `json:"password"`
		} `json:"created"`
		Failed []struct {
			Line   int    `json:"line"`
			Reason string `json:"reason"`
		} `json:"failed"`
	}
	json.Unmarshal(importRaw, &imported)
	if len(imported.Created) != 2 {
		t.Fatalf("import created %+v, want 2 accounts", imported.Created)
	}
	if len(imported.Failed) != 1 || imported.Failed[0].Line != 3 {
		t.Fatalf("import failures = %+v, want the duplicate on line 3", imported.Failed)
	}
	// The 2k shorthand becomes an absolute token count.
	var takenID int64
	for _, item := range imported.Created {
		if item.Username == "taken" {
			takenID = item.ID
		}
	}
	users := listTeamUsers(t, e)
	if quota := users["taken"]; quota != 2000 {
		t.Fatalf("imported quota = %d, want 2000 from \"2k\"", quota)
	}
	// The duplicate row is the existing account, untouched.
	if quota := users["direct"]; quota != 750 {
		t.Fatalf("duplicate import changed the existing account: %d", quota)
	}

	// Bulk pause ends live sessions, and it targets the account signed in above:
	// pausing a different member would prove nothing.
	directID := usersID(t, e, "direct")
	e.admin("POST", "/admin/team/users/bulk", map[string]any{"ids": []int64{directID}, "action": "pause"}, 200)
	code, _, _ := e.call(b.client, "GET", "/me", nil, "", "stale")
	if code != 401 {
		t.Fatalf("a paused member's session still works: %d", code)
	}
	b2 := e.browser()
	code, _, _ = e.call(b2.client, "POST", "/auth/login", map[string]string{"username": "direct", "password": made.Password}, "", "")
	if code == 200 {
		t.Fatal("a paused account may still sign in")
	}
	e.admin("POST", "/admin/team/users/bulk", map[string]any{"ids": []int64{directID}, "action": "resume"}, 200)
	b2.login("direct", made.Password)

	// Bulk policy change applies to every selected account.
	policiesRaw := e.admin("POST", "/admin/team/policies", TeamPolicy{Name: "Bulk", Models: []string{}, MemberIDs: []int64{}, MaxKeys: 3, RPM: 30}, 200)
	var bulk TeamPolicy
	json.Unmarshal(policiesRaw, &bulk)
	e.admin("POST", "/admin/team/users/bulk", map[string]any{"ids": []int64{directID, takenID}, "action": "policy", "policy_id": bulk.ID}, 200)
	for _, item := range teamUserRows(t, e) {
		if (item.ID == directID || item.ID == takenID) && item.PolicyID != bulk.ID {
			t.Fatalf("user %d kept policy %d", item.ID, item.PolicyID)
		}
	}

	// Bulk revoke ends sessions without touching the accounts.
	e.admin("POST", "/admin/team/users/bulk", map[string]any{"ids": []int64{directID}, "action": "revoke_sessions"}, 200)
	code, _, _ = e.call(b2.client, "GET", "/me", nil, "", "whatever")
	if code != 401 {
		t.Fatalf("revoked member session still works: %d", code)
	}

	// The owner is never a target, whatever a stale console sends.
	ownerID := usersID(t, e, "owner")
	e.admin("POST", "/admin/team/users/bulk", map[string]any{"ids": []int64{ownerID}, "action": "pause"}, 200)
	if item := userByID(t, e, ownerID); item == nil || item.Status != "active" {
		t.Fatal("bulk pause paused the owner account")
	}

	// Delete removes the account and neutralizes its keys.
	e.admin("POST", "/admin/team/users/bulk", map[string]any{"ids": []int64{directID}, "action": "delete"}, 200)
	if item := userByID(t, e, directID); item != nil {
		t.Fatal("deleted account is still listed")
	}
	var keys []struct {
		ID        int64  `json:"id"`
		TeamHint  string `json:"team_hint"`
		TokenHash string `json:"-"`
	}
	rawKeys := e.admin("GET", "/admin/downstream-keys", nil, 200)
	json.Unmarshal(rawKeys, &keys)
	if len(keys) != 0 {
		t.Fatalf("a deleted account left %d usable keys behind", len(keys))
	}
}

func teamUserRows(t *testing.T, e *teamTestEnv) []TeamUser {
	t.Helper()
	raw := e.admin("GET", "/admin/team/users", nil, 200)
	var items []TeamUser
	json.Unmarshal(raw, &items)
	return items
}

func userByID(t *testing.T, e *teamTestEnv, id int64) *TeamUser {
	t.Helper()
	for _, item := range teamUserRows(t, e) {
		if item.ID == id {
			found := item
			return &found
		}
	}
	return nil
}

func usersID(t *testing.T, e *teamTestEnv, username string) int64 {
	t.Helper()
	for _, item := range teamUserRows(t, e) {
		if item.Username == username {
			return item.ID
		}
	}
	t.Fatalf("user %q not found", username)
	return 0
}

// listTeamUsers maps username → granted quota, for assertions that care about
// the pool rather than the row.
func listTeamUsers(t *testing.T, e *teamTestEnv) map[string]int64 {
	t.Helper()
	out := map[string]int64{}
	for _, item := range teamUserRows(t, e) {
		out[item.Username] = item.QuotaTotalTokens
	}
	return out
}

// The account pool is enforced on top of the key quota: once it is spent, a
// valid key gets 402 rather than being served for free.
func TestTeamAccountQuotaBlocksRelay(t *testing.T) {
	e := newTeamTestEnv(t)
	e.enable()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"t","choices":[{"message":{"role":"assistant","content":"ok"}}],"usage":{"prompt_tokens":2,"completion_tokens":1,"total_tokens":3}}`))
	}))
	t.Cleanup(upstream.Close)
	secret, _ := e.enc.Encrypt([]byte("upstream-secret"))
	site, _ := e.db.Site.Create(&domain.Site{Name: "Up", Status: domain.StatusEnabled})
	cred, _ := e.db.Credential.Create(&domain.Credential{SiteID: site, Kind: "api_key", SecretEnc: []byte(secret), Status: domain.StatusEnabled})
	channel, _ := e.db.Channel.Create(&domain.Channel{SiteID: &site, CredentialID: &cred, Name: "up", BaseURL: upstream.URL, TypeHint: "openai", Status: domain.StatusEnabled})
	route, _ := e.db.Route.Create(&domain.Route{ModelPattern: "quota-model", Enabled: true})
	member, _ := e.db.RouteMember.Create(&domain.RouteMember{RouteID: route, ChannelID: channel, Priority: 10, Weight: 100, Enabled: true})
	e.admin("PUT", "/admin/team/policies/1", TeamPolicy{ID: 1, Name: "Q", Models: []string{"quota-model"}, MemberIDs: []int64{member}, MaxKeys: 5, RPM: 120}, 200)
	alice := e.member("alice", "member")
	_, token := alice.key("app")

	call := func() int {
		code, _, _ := e.call(http.DefaultClient, "POST", "/v1/chat/completions", map[string]any{"model": "quota-model", "messages": []any{map[string]string{"role": "user", "content": "hi"}}}, token, "")
		return code
	}
	// Unlimited by default: the first call goes through and books 3 tokens.
	if got := call(); got != 200 {
		t.Fatalf("unlimited account call = %d", got)
	}
	// Now cap the account below what it already spent.
	e.admin("PATCH", "/admin/team/users/"+strconv.FormatInt(alice.userID, 10), map[string]any{"quota_total_tokens": 1}, 200)
	if got := call(); got != http.StatusPaymentRequired {
		t.Fatalf("spent account call = %d, want 402", got)
	}
	// Raising the cap and resetting the counter is the "top this person up"
	// write the console offers; both fields land in one request.
	e.admin("PATCH", "/admin/team/users/"+strconv.FormatInt(alice.userID, 10), map[string]any{"quota_total_tokens": 1000, "quota_reset": true}, 200)
	if got := call(); got != 200 {
		t.Fatalf("topped-up account call = %d", got)
	}
	item := userByID(t, e, alice.userID)
	if item == nil || item.QuotaUsedTokens != 3 {
		t.Fatalf("account usage after the top-up call = %+v, want the 3 booked tokens", item)
	}
}

// The money budget is enforced at all three levels, alone and together with
// the token quota: a customer who buys "one dollar of usage" must stop when
// the dollar is spent, even though tokens are unlimited.
func TestMoneyQuotaRefusesRelayAtEveryLevel(t *testing.T) {
	e := newTeamTestEnv(t)
	e.enable()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"t","choices":[{"message":{"role":"assistant","content":"ok"}}],"usage":{"prompt_tokens":1000,"completion_tokens":1000,"total_tokens":2000}}`))
	}))
	t.Cleanup(upstream.Close)
	secret, _ := e.enc.Encrypt([]byte("upstream-secret"))
	site, _ := e.db.Site.Create(&domain.Site{Name: "Up", Status: domain.StatusEnabled})
	cred, _ := e.db.Credential.Create(&domain.Credential{SiteID: site, Kind: "api_key", SecretEnc: []byte(secret), Status: domain.StatusEnabled})
	channel, _ := e.db.Channel.Create(&domain.Channel{SiteID: &site, CredentialID: &cred, Name: "up", BaseURL: upstream.URL, TypeHint: "openai", Status: domain.StatusEnabled})
	route, _ := e.db.Route.Create(&domain.Route{ModelPattern: "cost-model", Enabled: true})
	member, _ := e.db.RouteMember.Create(&domain.RouteMember{RouteID: route, ChannelID: channel, Priority: 10, Weight: 100, Enabled: true})
	// Price the model at 1.00 per 1k prompt tokens: one call costs exactly 1.
	if err := e.db.ModelMetadata.Upsert(&domain.ModelMetadata{ModelName: "cost-model", PricePromptPer1k: 1, PriceCompletionPer1k: 0}); err != nil {
		t.Fatal(err)
	}
	e.admin("PUT", "/admin/team/policies/1", TeamPolicy{ID: 1, Name: "Cost", Models: []string{"cost-model"}, MemberIDs: []int64{member}, MaxKeys: 5, RPM: 120}, 200)
	alice := e.member("alice", "member")

	// A key-level spend budget: the first call books 1.00 and stops the second.
	keyID, token := alice.key("budget")
	e.admin("PUT", "/admin/downstream-keys/"+teamItoa(keyID), map[string]any{"quota_total_cost": 1.0}, 200)
	call := func() int {
		code, _, _ := e.call(http.DefaultClient, "POST", "/v1/chat/completions", map[string]any{"model": "cost-model", "messages": []any{map[string]string{"role": "user", "content": "hi"}}}, token, "")
		return code
	}
	if got := call(); got != 200 {
		t.Fatalf("first call = %d, want 200", got)
	}
	if got := call(); got != http.StatusPaymentRequired {
		t.Fatalf("call over the key's spend budget = %d, want 402", got)
	}
	// The billed amount is visible on the key, not only in usage_records.
	keys := e.admin("GET", "/admin/downstream-keys", nil, 200)
	var costView []struct {
		ID            int64   `json:"id"`
		QuotaUsedCost float64 `json:"quota_used_cost"`
	}
	json.Unmarshal(keys, &costView)
	if len(costView) != 1 || costView[0].QuotaUsedCost < 0.99 || costView[0].QuotaUsedCost > 1.01 {
		t.Fatalf("key spend view = %+v, want about 1.00", costView)
	}

	// Reset the key's counters and cap the GROUP instead: the key is free, the
	// tenant is not.
	e.admin("PUT", "/admin/downstream-keys/"+teamItoa(keyID), map[string]any{"quota_total_cost": 0, "reset_used": true}, 200)
	e.admin("PUT", "/admin/groups/paid", map[string]any{"quota_total_cost": 1.0}, 200)
	e.admin("PUT", "/admin/downstream-keys/"+teamItoa(keyID), map[string]any{"group_name": "paid"}, 200)
	if got := call(); got != 200 {
		t.Fatalf("first group-budgeted call = %d, want 200", got)
	}
	if got := call(); got != http.StatusPaymentRequired {
		t.Fatalf("call over the group's spend budget = %d, want 402", got)
	}

	// Finally the account pool: clear the group budget, cap the member's own
	// pool, and the same key is refused again.
	e.admin("PUT", "/admin/groups/paid", map[string]any{"quota_total_cost": 0}, 200)
	// The member's own pool starts fresh (their earlier calls are already in
	// it), and the key's counters are cleared so only the account can stop it.
	e.admin("PUT", "/admin/downstream-keys/"+teamItoa(keyID), map[string]any{"reset_used": true}, 200)
	// Read the member's view BEFORE the admin write: changing an account resets
	// its sessions by design, so a later /me would be a 401.
	me := alice.request("GET", "/me", nil, 200)
	var account struct {
		Credit   teamCreditView `json:"credit"`
		Currency struct {
			Symbol string  `json:"symbol"`
			Rate   float64 `json:"rate"`
		} `json:"currency"`
	}
	json.Unmarshal(me, &account)
	if account.Currency.Symbol != "$" || account.Currency.Rate != 1 {
		t.Fatalf("currency = %+v, want the defaults", account.Currency)
	}
	e.admin("PATCH", "/admin/team/users/"+teamItoa(alice.userID), map[string]any{"quota_total_cost": 1.0, "quota_reset": true}, 200)
	if got := call(); got != 200 {
		t.Fatalf("first account-budgeted call = %d, want 200", got)
	}
	if got := call(); got != http.StatusPaymentRequired {
		t.Fatalf("call over the account's spend budget = %d, want 402", got)
	}
	// The pool the relay refused on holds the spend, as the console reads it.
	users := e.admin("GET", "/admin/team/users", nil, 200)
	var pool []TeamUser
	json.Unmarshal(users, &pool)
	found := false
	for _, user := range pool {
		if user.ID != alice.userID {
			continue
		}
		found = true
		if user.QuotaTotalCost != 1 || user.QuotaUsedCost < 0.99 {
			t.Fatalf("account pool = %v/%v, want a spent 1.00 budget", user.QuotaUsedCost, user.QuotaTotalCost)
		}
	}
	if !found {
		t.Fatal("the member is missing from the admin list")
	}
}

// A credit voucher carrying money tops up the account's spend pool, and the
// operator's display currency is what the console and member app render.
func TestCreditCodeCarriesMoneyAndCurrencyIsConfigurable(t *testing.T) {
	e := newTeamTestEnv(t)
	e.enable()
	alice := e.member("alice", "member")

	_, codes := mintCodes(t, e, map[string]any{"kind": "credit", "count": 1, "quota_cost": 12.5, "max_uses": 3})
	raw := alice.request("POST", "/me/redeem", map[string]any{"code": codes[0]}, 200)
	var redeemed struct {
		CostGranted float64 `json:"cost_granted"`
		CostTotal   float64 `json:"cost_total"`
	}
	json.Unmarshal(raw, &redeemed)
	if redeemed.CostGranted != 12.5 || redeemed.CostTotal != 12.5 {
		t.Fatalf("redeem = %+v, want a 12.50 grant", redeemed)
	}
	// Money-only vouchers are valid; a voucher with neither face value is not.
	// A credit voucher with NEITHER face value is a mistake, not a code.
	e.admin("POST", "/admin/team/codes", map[string]any{"kind": "credit", "count": 1, "quota_tokens": 0, "quota_cost": 0}, 400)
	_ = codes

	// The console sets the display currency; the member app reads the same two
	// values, so a site that prices in yuan shows yuan everywhere.
	e.admin("PUT", "/admin/display-settings", map[string]any{"symbol": "¥", "rate": 7.2}, 200)
	view := e.admin("GET", "/admin/display-settings", nil, 200)
	var currency struct {
		Symbol string  `json:"symbol"`
		Rate   float64 `json:"rate"`
	}
	json.Unmarshal(view, &currency)
	if currency.Symbol != "¥" || currency.Rate != 7.2 {
		t.Fatalf("display settings = %+v", currency)
	}
	me := alice.request("GET", "/me", nil, 200)
	var account struct {
		Currency struct {
			Symbol string  `json:"symbol"`
			Rate   float64 `json:"rate"`
		} `json:"currency"`
	}
	json.Unmarshal(me, &account)
	if account.Currency.Symbol != "¥" || account.Currency.Rate != 7.2 {
		t.Fatalf("member app currency = %+v, want the console's ¥7.2", account.Currency)
	}
	// A nonsense rate is refused rather than stored.
	e.admin("PUT", "/admin/display-settings", map[string]any{"symbol": "$", "rate": 0}, 400)
}

// teamItoa keeps URL building in these tests terse. The external test package
// has its own itoa, which this internal package cannot see.
func teamItoa(value int64) string { return strconv.FormatInt(value, 10) }
