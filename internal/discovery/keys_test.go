package discovery_test

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/lan/meta-gateway/internal/adapters"
	"github.com/lan/meta-gateway/internal/crypto"
	"github.com/lan/meta-gateway/internal/discovery"
	"github.com/lan/meta-gateway/internal/domain"
	"github.com/lan/meta-gateway/internal/store"
)

// keyTestHarness builds one site with a set of keys and a channel at an httptest
// upstream that answers /v1/models per key (New-API style: 401 for a dead key).
type keyTestHarness struct {
	db         *store.DB
	service    *discovery.Service
	channelID  int64
	siteID     int64
	creds      map[string]int64
	upstream   *httptest.Server
	mu         sync.Mutex
	secretsGot []string
}

func newKeyTestHarness(t *testing.T, answers map[string]struct {
	body   string
	status int
}) *keyTestHarness {
	t.Helper()
	harness := &keyTestHarness{creds: map[string]int64{}}
	harness.upstream = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		secret := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		harness.mu.Lock()
		harness.secretsGot = append(harness.secretsGot, secret)
		harness.mu.Unlock()
		answer, ok := answers[secret]
		if !ok {
			http.Error(w, `{"error":{"message":"unknown key"}}`, http.StatusUnauthorized)
			return
		}
		if answer.status != 0 {
			w.WriteHeader(answer.status)
		}
		_, _ = io.WriteString(w, answer.body)
	}))
	t.Cleanup(harness.upstream.Close)

	db, err := store.OpenTest(filepath.Join(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	enc, _ := crypto.New("key-test")
	harness.db = db
	harness.siteID, _ = db.Site.Create(&domain.Site{
		Name: "site", BaseURL: harness.upstream.URL, Platform: "new-api", Status: domain.StatusEnabled,
	})
	for secret, answer := range answers {
		encrypted, _ := enc.Encrypt([]byte(secret))
		id, _ := db.Credential.Create(&domain.Credential{
			SiteID: harness.siteID, Kind: "api_key", SecretEnc: []byte(encrypted), Status: domain.StatusEnabled,
		})
		harness.creds[secret] = id
		_ = answer
	}
	channelID, err := db.Channel.Create(&domain.Channel{
		SiteID: &harness.siteID, Name: "channel", BaseURL: harness.upstream.URL,
		Status: domain.StatusEnabled, ModelSyncMode: domain.ModelSyncModeAuto,
	})
	if err != nil {
		t.Fatal(err)
	}
	harness.channelID = channelID
	harness.service = discovery.New(db, enc, adapters.NewRegistry(nil))
	return harness
}

func (h *keyTestHarness) contacted(secret string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, seen := range h.secretsGot {
		if seen == secret {
			return true
		}
	}
	return false
}

// The batch question the console asks: which of these keys still works. One
// answer per key, in the order asked, and the dead one says why.
func TestTestKeysReportsEachKeySeparately(t *testing.T) {
	harness := newKeyTestHarness(t, map[string]struct {
		body   string
		status int
	}{
		"good-key": {body: `{"data":[{"id":"m-2"},{"id":"m-1"},{"id":"m-1"}]}`},
		"dead-key": {body: `{"error":{"message":"invalid api key"}}`, status: http.StatusUnauthorized},
	})

	// Named order is the answer order, dead key first.
	deadID := harness.creds["dead-key"]
	goodID := harness.creds["good-key"]
	results, err := harness.service.TestKeys(t.Context(), harness.channelID, []int64{deadID, goodID})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 {
		t.Fatalf("results = %d, want 2", len(results))
	}
	if results[0].CredentialID != deadID || results[0].OK {
		t.Fatalf("first result = %+v, want the dead key reported as failed", results[0])
	}
	if results[0].Category != domain.CategoryUpstreamUnauthorized {
		t.Errorf("category = %q, want %q", results[0].Category, domain.CategoryUpstreamUnauthorized)
	}
	// The adapter deliberately carries no upstream body (zero-trust: a body can
	// echo the credential), so the structured status is the message the console
	// shows next to its translation of the category.
	if !strings.Contains(results[0].Error, "upstream_status") {
		t.Errorf("error = %q, want the adapter's structured status", results[0].Error)
	}
	if results[1].CredentialID != goodID || !results[1].OK {
		t.Fatalf("second result = %+v, want the good key reported as ok", results[1])
	}
	// De-duplicated, sorted, and the count matches what the key can see.
	if results[1].ModelCount != 2 {
		t.Errorf("model count = %d, want 2 (m-1 counted once)", results[1].ModelCount)
	}
	if strings.Join(results[1].Sample, ",") != "m-1,m-2" {
		t.Errorf("sample = %v, want the sorted names", results[1].Sample)
	}
}

// No ids means the channel's own pool: the button's 测活全部.
func TestTestKeysWithoutIDsUsesTheChannelPool(t *testing.T) {
	harness := newKeyTestHarness(t, map[string]struct {
		body   string
		status int
	}{
		"key-a": {body: `{"data":[{"id":"a"}]}`},
		"key-b": {body: `{"data":[{"id":"b"}]}`},
	})

	results, err := harness.service.TestKeys(t.Context(), harness.channelID, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 {
		t.Fatalf("results = %d, want both pool keys", len(results))
	}
	for _, result := range results {
		if !result.OK || result.ModelCount != 1 {
			t.Errorf("result = %+v, want ok with one model", result)
		}
	}
}

// A key of another site is not tried against this channel's upstream: the
// question would be about a different account, and the secret would leak to a
// host that never issued it.
func TestTestKeysRefusesAKeyFromAnotherSite(t *testing.T) {
	harness := newKeyTestHarness(t, map[string]struct {
		body   string
		status int
	}{
		"mine": {body: `{"data":[{"id":"a"}]}`},
	})
	enc, _ := crypto.New("key-test")
	otherSiteID, _ := harness.db.Site.Create(&domain.Site{
		Name: "other", BaseURL: harness.upstream.URL, Platform: "new-api", Status: domain.StatusEnabled,
	})
	foreign, _ := enc.Encrypt([]byte("foreign-secret"))
	foreignID, _ := harness.db.Credential.Create(&domain.Credential{
		SiteID: otherSiteID, Kind: "api_key", SecretEnc: []byte(foreign), Status: domain.StatusEnabled,
	})

	results, err := harness.service.TestKeys(t.Context(), harness.channelID, []int64{foreignID})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].OK {
		t.Fatalf("results = %+v, want the foreign key refused", results)
	}
	if !strings.Contains(results[0].Error, "another site") {
		t.Errorf("error = %q, want the reason stated", results[0].Error)
	}
	if harness.contacted("foreign-secret") {
		t.Error("the foreign key was sent to this channel's upstream")
	}
}

// Testing is a diagnostic: it must not record probe state, cool a channel down,
// or disable anything. The operator decides what to do with a dead key.
func TestTestKeysWritesNothing(t *testing.T) {
	harness := newKeyTestHarness(t, map[string]struct {
		body   string
		status int
	}{
		"good-key": {body: `{"data":[{"id":"m"}]}`},
		"dead-key": {body: `{"error":{"message":"nope"}}`, status: http.StatusForbidden},
	})
	countHistory := func() int {
		var count int
		if err := harness.db.QueryRow(`SELECT COUNT(*) FROM channel_health_history`).Scan(&count); err != nil {
			t.Fatal(err)
		}
		return count
	}
	// The probe columns on the channel row are what a Probe writes; read them
	// the way the store writes them instead of through a struct projection.
	probeState := func() string {
		var at, lastError string
		var ok int
		if err := harness.db.QueryRow(
			`SELECT COALESCE(last_probe_at,''), last_probe_ok, COALESCE(last_probe_error,'') FROM channels WHERE id=?`,
			harness.channelID,
		).Scan(&at, &ok, &lastError); err != nil {
			t.Fatal(err)
		}
		return fmt.Sprintf("%s|%d|%s", at, ok, lastError)
	}
	before := countHistory()
	stateBefore := probeState()

	if _, err := harness.service.TestKeys(t.Context(), harness.channelID, nil); err != nil {
		t.Fatal(err)
	}

	if after := countHistory(); after != before {
		t.Errorf("health history rows = %d, want unchanged %d", after, before)
	}
	if after := probeState(); after != stateBefore {
		t.Errorf("channel probe state = %q, want unchanged %q", after, stateBefore)
	}
	channelAfter, err := harness.db.Channel.GetByID(harness.channelID)
	if err != nil {
		t.Fatal(err)
	}
	if channelAfter.Status != domain.StatusEnabled {
		t.Errorf("channel status = %q, want enabled", channelAfter.Status)
	}
	// The keys keep their own status: a dead key is reported, not disabled.
	for _, id := range harness.creds {
		credential, err := harness.db.Credential.GetByID(id)
		if err != nil {
			t.Fatal(err)
		}
		if credential.Status != domain.StatusEnabled {
			t.Errorf("credential %d status = %q, want enabled", id, credential.Status)
		}
	}
}
