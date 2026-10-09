package discovery

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/lan/meta-gateway/internal/adapters"
	"github.com/lan/meta-gateway/internal/domain"
)

const (
	// keyTestConcurrency bounds how many keys are tried at once. A key test is a
	// public models listing, and these upstreams are volunteer-run: four at a
	// time reads a fleet of thirty keys in seconds without looking like a crawl.
	keyTestConcurrency = 4
	// keyTestTimeout bounds one key. A key that has not answered by now is dead
	// for practical purposes, and the console's batch button must return.
	keyTestTimeout = 25 * time.Second
	// keyTestSample is how many model names a result carries back. The console
	// shows the first few next to the count, which is enough to tell "this key
	// sees a different group" from "this key is fine".
	keyTestSample = 5
	// keyTestMessage caps a stored/returned upstream message. Bodies can be whole
	// HTML error pages; the console shows one line.
	keyTestMessage = 200
)

// KeyTestResult is one API key's answer to "does the upstream still take you".
//
// It is a diagnostic, not a state change: nothing is written, no channel is
// cooled down, and a failing key is reported rather than disabled. Acting on the
// answer (deleting the dead ones) stays the operator's decision.
type KeyTestResult struct {
	CredentialID int64 `json:"credential_id"`
	OK           bool  `json:"ok"`
	// Category is a domain.Category* value, the same vocabulary a failed probe
	// publishes, so the console translates it with one map.
	Category string `json:"category,omitempty"`
	// Error is the upstream's own message (or the transport's).
	Error string `json:"error,omitempty"`
	// ModelCount is how many models this key can see; Sample names the first few.
	ModelCount int      `json:"model_count"`
	Sample     []string `json:"sample,omitempty"`
	LatencyMs  int      `json:"latency_ms"`
}

// TestKeys lists models with each key the operator named, in the order given.
//
// Listing models is the cheapest honest liveness check: it is a GET, it costs no
// tokens, and it is exactly the request discovery already makes — so a key that
// passes here is a key the gateway can use, and a key that fails here is one
// discovery would have skipped. Keys that are disabled are tested too: that is
// how an operator decides whether one is worth turning back on.
func (s *Service) TestKeys(ctx context.Context, channelID int64, credentialIDs []int64) ([]KeyTestResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	channel, err := s.db.Channel.GetByID(channelID)
	if err != nil {
		return nil, internalError("channel_lookup")
	}
	if channel == nil {
		return nil, &Error{Kind: ErrorNotFound, Category: "channel_not_found"}
	}
	if channel.SiteID == nil {
		return nil, unavailableError("site_unavailable")
	}
	site, err := s.db.Site.GetByID(*channel.SiteID)
	if err != nil {
		return nil, internalError("site_lookup")
	}
	if site == nil {
		return nil, unavailableError("site_unavailable")
	}
	adapter, ok := s.registry.Resolve(channel.TypeHint, site.Platform)
	if !ok {
		return nil, unavailableError("unsupported_adapter")
	}
	baseURL := channel.BaseURL
	if baseURL == "" {
		baseURL = site.BaseURL
	}

	// No ids means "every key this channel can use" — the operator's 测活全部.
	if len(credentialIDs) == 0 {
		pool, poolErr := s.resolveAPIKeyPool(channel, site.ID)
		if poolErr != nil {
			return nil, poolErr
		}
		credentialIDs = make([]int64, 0, len(pool))
		for index := range pool {
			credentialIDs = append(credentialIDs, pool[index].ID)
		}
	}
	if len(credentialIDs) == 0 {
		return []KeyTestResult{}, nil
	}

	results := make([]KeyTestResult, len(credentialIDs))
	sem := make(chan struct{}, keyTestConcurrency)
	var wait sync.WaitGroup
	for index, id := range credentialIDs {
		wait.Add(1)
		go func(index int, id int64) {
			defer wait.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			results[index] = s.testOneKey(ctx, adapter, baseURL, site.ID, id)
		}(index, id)
	}
	wait.Wait()
	return results, nil
}

// testOneKey resolves one credential and lists models with it.
func (s *Service) testOneKey(ctx context.Context, adapter adapters.ModelAdapter, baseURL string, siteID, credentialID int64) KeyTestResult {
	result := KeyTestResult{CredentialID: credentialID}
	if ctx.Err() != nil {
		result.Error = ctx.Err().Error()
		return result
	}
	credential, err := s.db.Credential.GetByID(credentialID)
	if err != nil || credential == nil {
		result.Category = domain.CategoryCredentialUnavailable
		result.Error = "credential not found"
		return result
	}
	// A key belongs to one site. Testing another site's key against this channel
	// would answer a question nobody asked, so it is reported instead of tried.
	if credential.SiteID != siteID {
		result.Category = domain.CategoryCredentialUnavailable
		result.Error = "credential belongs to another site"
		return result
	}
	if len(credential.SecretEnc) == 0 {
		result.Category = domain.CategoryCredentialUnavailable
		result.Error = "credential has no secret"
		return result
	}
	plaintext, decryptErr := s.enc.Decrypt(string(credential.SecretEnc))
	if decryptErr != nil || len(plaintext) == 0 {
		result.Category = domain.CategoryCredentialUnavailable
		result.Error = "secret decrypt failed"
		return result
	}
	defer func() {
		for i := range plaintext {
			plaintext[i] = 0
		}
	}()

	attemptCtx, cancel := context.WithTimeout(ctx, keyTestTimeout)
	defer cancel()
	started := s.now()
	listed, listErr := adapter.ListModels(attemptCtx, baseURL, string(plaintext))
	result.LatencyMs = int(s.now().Sub(started).Milliseconds())
	if listErr != nil {
		result.Category = categoryForListError(listErr, credential.Kind)
		result.Error = trimmedMessage(listErr.Error())
		return result
	}
	// A key that lists nothing is not dead: an upstream with an empty catalogue
	// answers exactly like this, and the emptiness is the report.
	unique := make(map[string]struct{}, len(listed))
	for _, model := range listed {
		if name := strings.TrimSpace(model); name != "" {
			unique[name] = struct{}{}
		}
	}
	names := make([]string, 0, len(unique))
	for name := range unique {
		names = append(names, name)
	}
	sort.Strings(names)
	result.OK = true
	result.ModelCount = len(names)
	if len(names) > keyTestSample {
		result.Sample = names[:keyTestSample]
	} else {
		result.Sample = names
	}
	return result
}

// trimmedMessage keeps one line of an upstream message, so a whole HTML error
// page cannot arrive in the drawer.
func trimmedMessage(message string) string {
	trimmed := strings.TrimSpace(strings.ReplaceAll(message, "\n", " "))
	if len(trimmed) > keyTestMessage {
		return trimmed[:keyTestMessage] + "…"
	}
	return trimmed
}
