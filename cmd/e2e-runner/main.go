package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

type runner struct {
	base, admin, metrics, upstream, stateFile string
	client                                    *http.Client
}

type state struct {
	Token   string `json:"token"`
	RouteID int64  `json:"route_id"`
}

func main() {
	r := &runner{base: env("GATEWAY_URL"), admin: env("ADMIN_TOKEN"), metrics: env("METRICS_TOKEN"), upstream: env("UPSTREAM_URL"), stateFile: env("STATE_FILE"), client: &http.Client{Timeout: 15 * time.Second}}
	mode := "setup"
	if len(os.Args) == 2 {
		mode = os.Args[1]
	}
	var err error
	if mode == "verify" {
		err = r.verifyRestart()
	} else if mode == "sticky" {
		err = r.sticky()
	} else {
		err = r.setup()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "E2E failed:", err)
		os.Exit(1)
	}
	fmt.Println("E2E", mode, "passed")
}

func (r *runner) setup() error {
	if status, _, err := r.do(http.MethodGet, "/metrics", "", nil); err != nil || status != http.StatusUnauthorized {
		return fmt.Errorf("metrics without token: status=%d err=%v", status, err)
	}
	if status, body, err := r.do(http.MethodGet, "/metrics", r.metrics, nil); err != nil || status != http.StatusOK || !bytes.Contains(body, []byte("meta_gateway_ready 1")) {
		return fmt.Errorf("authorized metrics: status=%d err=%v", status, err)
	}

	site := r.adminJSON(http.MethodPost, "/admin/sites", map[string]any{"name": "e2e", "base_url": r.upstream + "/ok", "platform": "new-api", "status": "enabled"})
	siteID := number(site, "id")
	okCredential := r.adminJSON(http.MethodPost, fmt.Sprintf("/admin/sites/%d/credentials", siteID), map[string]any{"kind": "api_key", "secret": "e2e-upstream-secret", "status": "enabled"})
	okCredentialID := number(okCredential, "id")
	failCredential := r.adminJSON(http.MethodPost, fmt.Sprintf("/admin/sites/%d/credentials", siteID), map[string]any{"kind": "api_key", "secret": "e2e-upstream-secret", "status": "enabled"})
	failCredentialID := number(failCredential, "id")
	checkinCredential := r.adminJSON(http.MethodPost, fmt.Sprintf("/admin/sites/%d/credentials", siteID), map[string]any{"kind": "session", "secret": "e2e-upstream-secret", "meta_json": `{"platform_user_id":42}`, "status": "enabled"})
	checkinCredentialID := number(checkinCredential, "id")

	okChannel := r.adminJSON(http.MethodPost, "/admin/channels", map[string]any{"site_id": siteID, "credential_id": okCredentialID, "name": "e2e-ok", "base_url": r.upstream + "/ok", "models_csv": "e2e-model", "group_name": "e2e", "priority": 10, "weight": 100, "status": "enabled", "type_hint": "new-api"})
	okChannelID := number(okChannel, "id")
	failChannel := r.adminJSON(http.MethodPost, "/admin/channels", map[string]any{"site_id": siteID, "credential_id": failCredentialID, "name": "e2e-fail", "base_url": r.upstream + "/fail", "models_csv": "e2e-model", "group_name": "e2e", "priority": 20, "weight": 100, "status": "enabled", "type_hint": "new-api"})
	failChannelID := number(failChannel, "id")

	route := r.adminJSON(http.MethodPost, "/admin/routes", map[string]any{"model_pattern": "e2e-model", "enabled": true})
	routeID := number(route, "id")
	r.adminJSON(http.MethodPost, fmt.Sprintf("/admin/routes/%d/members", routeID), map[string]any{"channel_id": failChannelID, "priority": 20, "weight": 100, "enabled": true, "auto": false, "manual_override": true})
	r.adminJSON(http.MethodPost, fmt.Sprintf("/admin/routes/%d/members", routeID), map[string]any{"channel_id": okChannelID, "priority": 10, "weight": 100, "enabled": true, "auto": false, "manual_override": true})

	key := r.adminJSON(http.MethodPost, "/admin/downstream-keys", map[string]any{"name": "e2e"})
	token, _ := key["token"].(string)
	if token == "" {
		return fmt.Errorf("downstream token missing")
	}
	if err := r.relay(token, false); err != nil {
		return err
	}
	if err := r.relay(token, true); err != nil {
		return err
	}

	logs := r.adminArray(http.MethodGet, "/admin/proxy-logs", nil)
	if countAttempts(logs, "e2e-model") < 3 {
		return fmt.Errorf("retry logs missing: %#v", logs)
	}
	r.adminJSON(http.MethodPost, fmt.Sprintf("/admin/discovery/channels/%d/refresh", okChannelID), nil)
	members := r.adminArray(http.MethodGet, fmt.Sprintf("/admin/routes/%d/members", routeID), nil)
	if !manualMembersIntact(members, okChannelID, failChannelID) {
		return fmt.Errorf("manual route members changed: %#v", members)
	}

	checkin := r.adminJSON(http.MethodPost, fmt.Sprintf("/admin/checkin/credentials/%d/run", checkinCredentialID), nil)
	if checkin["status"] != "success" || fmt.Sprint(checkin["reward"]) != "7" {
		return fmt.Errorf("check-in failed: %#v", checkin)
	}
	exported := r.adminJSON(http.MethodPost, "/admin/exchange/export", map[string]any{"include_secrets": true, "channel_ids": []int64{okChannelID}})
	imported := r.adminJSON(http.MethodPost, "/admin/exchange/import", exported)
	if number(imported, "discovery_success_count") != 1 {
		return fmt.Errorf("exchange discovery failed: %#v", imported)
	}
	members = r.adminArray(http.MethodGet, fmt.Sprintf("/admin/routes/%d/members", routeID), nil)
	if !manualMembersIntact(members, okChannelID, failChannelID) {
		return fmt.Errorf("exchange changed manual members: %#v", members)
	}

	deniedSite := r.adminJSON(http.MethodPost, "/admin/sites", map[string]any{"name": "denied", "base_url": "http://127.0.0.1:8080", "platform": "new-api", "status": "enabled"})
	deniedSiteID := number(deniedSite, "id")
	deniedCredential := r.adminJSON(http.MethodPost, fmt.Sprintf("/admin/sites/%d/credentials", deniedSiteID), map[string]any{"kind": "api_key", "secret": "e2e-upstream-secret", "status": "enabled"})
	deniedChannel := r.adminJSON(http.MethodPost, "/admin/channels", map[string]any{"site_id": deniedSiteID, "credential_id": number(deniedCredential, "id"), "name": "denied", "base_url": "http://127.0.0.1:8080", "status": "enabled", "type_hint": "new-api"})
	if status, _, err := r.do(http.MethodPost, fmt.Sprintf("/admin/discovery/channels/%d/refresh", number(deniedChannel, "id")), r.admin, nil); err != nil || status != http.StatusBadGateway {
		return fmt.Errorf("SSRF denial: status=%d err=%v", status, err)
	}

	backup := r.adminJSON(http.MethodPost, "/admin/backups", map[string]any{"path": "ignored"})
	if backup["status"] != "success" || !strings.HasPrefix(fmt.Sprint(backup["name"]), "meta-gateway-") {
		return fmt.Errorf("backup failed: %#v", backup)
	}
	audits := r.adminArray(http.MethodGet, "/admin/audit-events?limit=100", nil)
	if len(audits) == 0 || bytes.Contains(mustJSON(audits), []byte("e2e-upstream-secret")) {
		return fmt.Errorf("audit events missing or leaked secret")
	}

	encoded, _ := json.Marshal(state{Token: token, RouteID: routeID})
	if err := os.WriteFile(r.stateFile, encoded, 0600); err != nil {
		return err
	}
	return nil
}

func (r *runner) verifyRestart() error {
	encoded, err := os.ReadFile(r.stateFile)
	if err != nil {
		return err
	}
	var saved state
	if err := json.Unmarshal(encoded, &saved); err != nil {
		return err
	}
	if status, _, err := r.do(http.MethodGet, "/readyz", "", nil); err != nil || status != http.StatusOK {
		return fmt.Errorf("readiness after restart: status=%d err=%v", status, err)
	}
	if err := r.relay(saved.Token, false); err != nil {
		return fmt.Errorf("relay after restart: %w", err)
	}
	backups := r.adminArray(http.MethodGet, "/admin/backups", nil)
	if len(backups) == 0 {
		return fmt.Errorf("backup inventory lost after restart")
	}
	return nil
}

// sticky exercises sticky-session routing end to end: three same-session
// requests must land on one channel, a channel outage must escape to the
// survivor, and explain must report the sticky binding.
func (r *runner) sticky() error {
	site := r.adminJSON(http.MethodPost, "/admin/sites", map[string]any{"name": "sticky-site", "base_url": r.upstream + "/ok", "platform": "new-api", "status": "enabled"})
	siteID := number(site, "id")
	credential := r.adminJSON(http.MethodPost, fmt.Sprintf("/admin/sites/%d/credentials", siteID), map[string]any{"kind": "api_key", "secret": "e2e-upstream-secret", "status": "enabled"})
	credentialID := number(credential, "id")
	channelA := r.adminJSON(http.MethodPost, "/admin/channels", map[string]any{"site_id": siteID, "credential_id": credentialID, "name": "sticky-a", "base_url": r.upstream + "/ok", "models_csv": "sticky-model", "group_name": "e2e", "priority": 10, "weight": 100, "status": "enabled", "type_hint": "new-api"})
	channelAID := number(channelA, "id")
	channelB := r.adminJSON(http.MethodPost, "/admin/channels", map[string]any{"site_id": siteID, "credential_id": credentialID, "name": "sticky-b", "base_url": r.upstream + "/ok", "models_csv": "sticky-model", "group_name": "e2e", "priority": 10, "weight": 100, "status": "enabled", "type_hint": "new-api"})
	channelBID := number(channelB, "id")
	route := r.adminJSON(http.MethodPost, "/admin/routes", map[string]any{"model_pattern": "sticky-model", "enabled": true})
	routeID := number(route, "id")
	r.adminJSON(http.MethodPost, fmt.Sprintf("/admin/routes/%d/members", routeID), map[string]any{"channel_id": channelAID, "priority": 10, "weight": 100, "enabled": true, "auto": false, "manual_override": true})
	r.adminJSON(http.MethodPost, fmt.Sprintf("/admin/routes/%d/members", routeID), map[string]any{"channel_id": channelBID, "priority": 10, "weight": 100, "enabled": true, "auto": false, "manual_override": true})
	key := r.adminJSON(http.MethodPost, "/admin/downstream-keys", map[string]any{"name": "sticky"})
	token, _ := key["token"].(string)
	if token == "" {
		return fmt.Errorf("downstream token missing")
	}

	const session = "e2e-session-1"
	headers := map[string]string{"X-Meta-Session-Id": session}
	for i := 0; i < 3; i++ {
		if err := r.relayModel(token, false, "sticky-model", headers); err != nil {
			return fmt.Errorf("sticky relay #%d: %w", i+1, err)
		}
	}
	logs := r.adminArray(http.MethodGet, "/admin/proxy-logs?model=sticky-model&limit=10", nil)
	boundChannel, ok := singleSuccessfulChannel(logs)
	if !ok {
		return fmt.Errorf("sticky: expected 3 same-channel successes, got %#v", logs)
	}
	if countSuccessful(logs) != 3 {
		return fmt.Errorf("sticky: expected exactly 3 successful attempts before outage, got %#v", logs)
	}

	// Break the bound channel; the next same-session request must escape.
	failURL := r.upstream + "/fail"
	r.adminJSON(http.MethodPut, fmt.Sprintf("/admin/channels/%d", boundChannel), map[string]any{"base_url": failURL})
	if err := r.relayModel(token, false, "sticky-model", headers); err != nil {
		return fmt.Errorf("sticky escape relay: %w", err)
	}
	logs = r.adminArray(http.MethodGet, "/admin/proxy-logs?model=sticky-model&limit=10", nil)
	// Logs are newest-first: the escape request's successful attempt must be
	// the newest entry and land on the survivor, not the broken channel.
	if len(logs) == 0 || number(logs[0], "status") != 200 {
		return fmt.Errorf("sticky: latest attempt not successful: %#v", logs)
	}
	escaped := number(logs[0], "channel_id")
	if escaped == boundChannel {
		return fmt.Errorf("sticky: escape stayed on the broken channel %d: %#v", boundChannel, logs)
	}
	if !hasFailedAttempt(logs, boundChannel) {
		return fmt.Errorf("sticky: expected a failed attempt on broken channel %d: %#v", boundChannel, logs)
	}

	// Explain must carry the sticky binding for the session.
	status, body, err := r.do(http.MethodGet, "/admin/routes/explain?model=sticky-model&session="+session, r.admin, nil)
	if err != nil || status != http.StatusOK || !bytes.Contains(body, []byte(`"session_key":"`+session+`"`)) || !bytes.Contains(body, []byte(`"sticky_channel_id"`)) {
		return fmt.Errorf("sticky: explain missing session annotations: status=%d body=%s err=%v", status, body, err)
	}
	return nil
}

func singleSuccessfulChannel(logs []map[string]any) (int64, bool) {
	var channelID int64
	for _, entry := range logs {
		if number(entry, "status") != 200 {
			continue
		}
		current := number(entry, "channel_id")
		if channelID == 0 {
			channelID = current
		} else if current != channelID {
			return 0, false
		}
	}
	return channelID, channelID != 0
}

func countSuccessful(logs []map[string]any) int {
	count := 0
	for _, entry := range logs {
		if number(entry, "status") == 200 {
			count++
		}
	}
	return count
}

func hasFailedAttempt(logs []map[string]any, channelID int64) bool {
	for _, entry := range logs {
		if number(entry, "status") >= 400 && number(entry, "channel_id") == channelID {
			return true
		}
	}
	return false
}

func (r *runner) relay(token string, stream bool) error {
	return r.relayModel(token, stream, "e2e-model", nil)
}

func (r *runner) relayModel(token string, stream bool, model string, headers map[string]string) error {
	payload := map[string]any{"model": model, "messages": []map[string]string{{"role": "user", "content": "hello"}}, "stream": stream}
	status, body, err := r.doWithHeaders(http.MethodPost, "/v1/chat/completions", token, payload, headers)
	if err != nil || status != http.StatusOK || (stream && !bytes.Contains(body, []byte("[DONE]"))) || (!stream && !bytes.Contains(body, []byte("chat.completion"))) {
		return fmt.Errorf("relay stream=%v status=%d body=%s err=%v", stream, status, body, err)
	}
	return nil
}

func (r *runner) adminJSON(method, path string, body any) map[string]any {
	status, raw, err := r.do(method, path, r.admin, body)
	if err != nil || status < 200 || status >= 300 {
		panic(fmt.Sprintf("%s %s: status=%d body=%s err=%v", method, path, status, raw, err))
	}
	var result map[string]any
	if err := json.Unmarshal(raw, &result); err != nil {
		panic(err)
	}
	return result
}

func (r *runner) adminArray(method, path string, body any) []map[string]any {
	status, raw, err := r.do(method, path, r.admin, body)
	if err != nil || status < 200 || status >= 300 {
		panic(fmt.Sprintf("%s %s: status=%d body=%s err=%v", method, path, status, raw, err))
	}
	var result []map[string]any
	if err := json.Unmarshal(raw, &result); err != nil {
		panic(err)
	}
	return result
}

func (r *runner) do(method, path, token string, body any) (int, []byte, error) {
	return r.doWithHeaders(method, path, token, body, nil)
}

func (r *runner) doWithHeaders(method, path, token string, body any, headers map[string]string) (int, []byte, error) {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return 0, nil, err
		}
		reader = bytes.NewReader(encoded)
	}
	request, err := http.NewRequest(method, r.base+path, reader)
	if err != nil {
		return 0, nil, err
	}
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	response, err := r.client.Do(request)
	if err != nil {
		return 0, nil, err
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(response.Body)
	return response.StatusCode, raw, err
}

func number(value map[string]any, key string) int64 {
	switch item := value[key].(type) {
	case float64:
		return int64(item)
	case json.Number:
		result, _ := item.Int64()
		return result
	case string:
		result, _ := strconv.ParseInt(item, 10, 64)
		return result
	default:
		return 0
	}
}

func countAttempts(logs []map[string]any, model string) int {
	count := 0
	for _, entry := range logs {
		if entry["model"] == model {
			count++
		}
	}
	return count
}

func manualMembersIntact(members []map[string]any, channelIDs ...int64) bool {
	found := make(map[int64]bool)
	for _, member := range members {
		channelID := number(member, "channel_id")
		for _, expected := range channelIDs {
			if channelID == expected && member["enabled"] == true && member["manual_override"] == true && member["auto"] == false {
				found[expected] = true
			}
		}
	}
	return len(found) == len(channelIDs)
}

func mustJSON(value any) []byte {
	encoded, _ := json.Marshal(value)
	return encoded
}

func env(key string) string {
	value := os.Getenv(key)
	if value == "" {
		panic(key + " is required")
	}
	return value
}
