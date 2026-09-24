// Command jev-router is a meta-gateway sidecar plugin that turns TypeSafe's
// Jev into an automatic model router.
//
// A client sends the virtual model name "auto"; the gateway offers the request
// to this plugin's route hook before it selects a channel; the plugin asks Jev
// which of the models the gateway can actually route should answer, and returns
// that name. The gateway then routes normally, so billing, logging, failover,
// and cooldowns all apply to the real model.
//
// Why a plugin rather than a gateway feature: the judgment (which models exist,
// what each is good at, how to phrase the question) belongs to whoever runs the
// gateway, and it changes far more often than the gateway does.
//
// Protocol: see internal/plugins/hooks.go (the host side) and
// internal/proxy/hooks.go (the contract). Two properties matter here:
//
//   - Every failure declines the hook instead of guessing. A timeout, a 429, a
//     malformed answer, or a confidence below the configured floor all end in
//     "handled: false" (or the configured default model), so a Jev outage
//     degrades to an explicit error rather than a random model choice.
//   - The candidate set is intersected with the gateway's available_models.
//     Nothing is worse than routing to a model with no channel behind it.
//
// Build:
//
//	cd tools/plugins/jev-router
//	go build -o jev-router .
//
// Then in the meta-gateway Store: "Register a plugin" → http://127.0.0.1:9110
package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	// pluginID is the sidecar id; it also names the /v1/models entry.
	pluginID = "jev-router"
	// virtualModel is the name clients send. It matches the manifest's
	// route.match_models, so it also appears in /v1/models. It is namespaced
	// rather than a bare "auto": every router plugin wants to answer "auto",
	// and a client should be able to say which one it means.
	virtualModel = "auto-jev"

	defaultBaseURL        = "https://api.typesafe.ai"
	defaultModel          = "jev-latest"
	defaultMinConfidence  = 0.5
	defaultJevTimeoutMs   = 1200
	defaultSummaryRunes   = 2000
	defaultDeclineMessage = "no candidate model is routable"
	// pluginVersion is reported in the manifest and used as the release tag.
	pluginVersion = "1.0.0"
	// recentDecisions bounds the in-memory decision log the page renders.
	recentDecisions = 40
)

// config is the plugin's own settings, delivered by the gateway as a base64
// JSON object in X-Plugin-Config on every call.
type config struct {
	APIKey  string `json:"api_key"`
	BaseURL string `json:"base_url"`
	Model   string `json:"model"`
	// Candidates maps a model name to what it is good at: "gpt-5: math and
	// long reasoning". The description is what Jev's choice criteria carries,
	// so it decides with the operator's own words. Used when no scenarios are
	// configured.
	Candidates string `json:"candidates"`
	// Scenarios splits the shortlist by kind of work, one per line:
	//
	//	code (写代码、调试与重构): claude-sonnet-4, gpt-5
	//	simple: deepseek-chat
	//
	// The hint in parentheses is optional and is what Jev reads to tell the
	// scenarios apart. Routing then happens in two steps — which scenario, then
	// which model inside it — so a coding request only ever chooses between
	// coding models instead of being asked to weigh every model against every
	// other. A scenario with a single routable model costs no second call.
	Scenarios string `json:"scenarios"`
	// ScenarioInstructions overrides the question that picks the scenario.
	ScenarioInstructions string `json:"scenario_instructions"`
	// DefaultModel answers when Jev is unsure or unreachable. Empty means the
	// hook declines and the request fails with the gateway's own error.
	DefaultModel string `json:"default_model"`
	// MinConfidence is the floor below which the default model wins.
	MinConfidence *float64 `json:"min_confidence"`
	// JevTimeoutMs bounds the call to Jev (the manifest's hook timeout must be
	// larger, since it also covers the round trip to this plugin).
	JevTimeoutMs int `json:"jev_timeout_ms"`
	// Instructions overrides the default routing question.
	Instructions string `json:"instructions"`
	// SummaryRunes caps how much of the last user message is sent to Jev.
	SummaryRunes int `json:"summary_runes"`
}

func (c config) withDefaults() config {
	if strings.TrimSpace(c.BaseURL) == "" {
		c.BaseURL = defaultBaseURL
	}
	if strings.TrimSpace(c.Model) == "" {
		c.Model = defaultModel
	}
	if c.MinConfidence == nil {
		fallback := defaultMinConfidence
		c.MinConfidence = &fallback
	}
	if c.JevTimeoutMs <= 0 {
		c.JevTimeoutMs = defaultJevTimeoutMs
	}
	if c.SummaryRunes <= 0 {
		c.SummaryRunes = defaultSummaryRunes
	}
	if strings.TrimSpace(c.Instructions) == "" {
		c.Instructions = "Which model should answer this request?"
	}
	if strings.TrimSpace(c.ScenarioInstructions) == "" {
		c.ScenarioInstructions = "Which kind of work is this request asking for?"
	}
	return c
}

// candidate is one model the operator offers, with the reason to pick it.
type candidate struct {
	Model       string
	Description string
}

// parseCandidates reads the "model: description" line format. A bare model name
// is allowed and carries no description.
func parseCandidates(raw string) []candidate {
	var out []candidate
	seen := make(map[string]struct{})
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		model := line
		description := ""
		if index := strings.Index(line, ":"); index > 0 {
			// A model name never contains a colon; the first one separates the
			// name from the operator's description.
			model = strings.TrimSpace(line[:index])
			description = strings.TrimSpace(line[index+1:])
		}
		if model == "" {
			continue
		}
		if _, duplicate := seen[model]; duplicate {
			continue
		}
		seen[model] = struct{}{}
		out = append(out, candidate{Model: model, Description: description})
	}
	return out
}

// scenario is one named shortlist: requests that belong to it are answered by
// one of its own models.
type scenario struct {
	// Name is both the operator's label and the option Jev chooses between.
	Name string
	// Hint explains the name to Jev. Optional: "code" needs no gloss, an
	// internal name like "tier-2" does.
	Hint string
	// Models is the scenario's own shortlist, already narrowed to what the
	// gateway can route.
	Models []candidate
}

// parseScenarios reads scenarios from either representation:
//
//   - JSON, which is what the console's editor writes:
//     [{"name":"code","hint":"写代码","models":["claude-sonnet-4"]}]
//   - the line format, which stays supported because it is still the fastest
//     way to paste a shortlist straight into the API or a config file:
//     code (写代码): claude-sonnet-4
//
// Both feed the same structure, so nothing downstream has to know which one
// the operator used.
func parseScenarios(raw string) []scenario {
	candidates := scenarioSources(raw)
	var out []scenario
	seen := make(map[string]struct{})
	for _, parsed := range candidates {
		if _, duplicate := seen[parsed.Name]; duplicate {
			continue
		}
		seen[parsed.Name] = struct{}{}
		out = append(out, parsed)
	}
	return out
}

// scenarioSources collects candidate scenarios from whichever format the value
// is in. A JSON array that fails to decode falls through to the line parser: a
// typo in one entry should not silently erase every scenario.
func scenarioSources(raw string) []scenario {
	trimmed := strings.TrimSpace(raw)
	if strings.HasPrefix(trimmed, "[") {
		if decoded, ok := parseScenariosJSON(trimmed); ok {
			return decoded
		}
	}
	var out []scenario
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parsed, ok := parseScenarioLine(line)
		if !ok {
			continue
		}
		out = append(out, parsed)
	}
	return out
}

// parseScenariosJSON decodes the console editor's shape. Entries missing a name
// or any model are dropped rather than turned into an unusable scenario.
func parseScenariosJSON(raw string) ([]scenario, bool) {
	var items []struct {
		Name   string   `json:"name"`
		Hint   string   `json:"hint"`
		Models []string `json:"models"`
	}
	if err := json.Unmarshal([]byte(raw), &items); err != nil {
		return nil, false
	}
	out := make([]scenario, 0, len(items))
	for _, item := range items {
		name := strings.TrimSpace(item.Name)
		if name == "" {
			continue
		}
		models := make([]candidate, 0, len(item.Models))
		for _, model := range item.Models {
			if trimmed := strings.TrimSpace(model); trimmed != "" {
				models = append(models, candidate{Model: trimmed})
			}
		}
		if len(models) == 0 {
			continue
		}
		out = append(out, scenario{Name: name, Hint: strings.TrimSpace(item.Hint), Models: models})
	}
	return out, true
}

// parseScenarioLine reads one "name (hint): model, model" line.
//
// The hint is found first and removed, so a hint may contain colons — the
// colon that separates the name from the models is the first one left after
// the parentheses are gone, not the first one on the line.
func parseScenarioLine(line string) (scenario, bool) {
	rest := line
	hint := ""
	if open := strings.Index(rest, "("); open >= 0 {
		close := strings.Index(rest[open:], ")")
		if close < 0 {
			return scenario{}, false
		}
		hint = strings.TrimSpace(rest[open+1 : open+close])
		rest = rest[:open] + rest[open+close+1:]
	}
	name, modelsRaw, found := strings.Cut(rest, ":")
	if !found {
		return scenario{}, false
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return scenario{}, false
	}
	models := make([]candidate, 0, 4)
	for _, item := range strings.Split(modelsRaw, ",") {
		model := strings.TrimSpace(item)
		if model == "" {
			continue
		}
		models = append(models, candidate{Model: model})
	}
	if len(models) == 0 {
		return scenario{}, false
	}
	return scenario{Name: name, Hint: hint, Models: models}, true
}

// usableScenarios drops scenarios the gateway cannot serve at all, and narrows
// the rest to routable models. A scenario with nothing left is worse than
// useless: it is an option Jev could pick and then have to be overruled.
func usableScenarios(parsed []scenario, available map[string]struct{}) []scenario {
	out := make([]scenario, 0, len(parsed))
	for _, item := range parsed {
		kept := make([]candidate, 0, len(item.Models))
		for _, model := range item.Models {
			if _, ok := available[model.Model]; ok {
				kept = append(kept, model)
			}
		}
		if len(kept) == 0 {
			continue
		}
		item.Models = kept
		out = append(out, item)
	}
	return out
}

// hookInput mirrors the gateway's HookInput for the fields this plugin reads.
type hookInput struct {
	Hook            string            `json:"hook"`
	RequestID       string            `json:"request_id"`
	Model           string            `json:"model"`
	Stream          bool              `json:"stream"`
	Body            json.RawMessage   `json:"body"`
	Headers         map[string]string `json:"headers"`
	AvailableModels []string          `json:"available_models"`
}

// hookOutput is the answer the gateway applies.
type hookOutput struct {
	Handled    bool     `json:"handled"`
	Model      string   `json:"model,omitempty"`
	Reason     string   `json:"reason,omitempty"`
	Confidence *float64 `json:"confidence,omitempty"`
}

// decision is one recorded routing decision, for the plugin page.
type decision struct {
	At         time.Time `json:"at"`
	RequestID  string    `json:"request_id"`
	Client     string    `json:"client,omitempty"`
	Chosen     string    `json:"chosen"`
	Reason     string    `json:"reason"`
	Confidence *float64  `json:"confidence,omitempty"`
	Fallback   string    `json:"fallback,omitempty"`
	// Scenario is the scenario a scoped routing pass picked, when scenarios
	// are configured.
	Scenario  string `json:"scenario,omitempty"`
	LatencyMs int    `json:"latency_ms"`
	Prompt    string `json:"prompt,omitempty"`
}

// systemOneRequest is TypeSafe's evaluation payload. The API is strict: any key
// beyond state/model/questions is rejected with a 422, so this struct is built
// by hand rather than by decorating a richer one.
type systemOneRequest struct {
	State     any            `json:"state"`
	Model     string         `json:"model"`
	Questions map[string]any `json:"questions"`
}

type systemOneResponse struct {
	Model   string `json:"model"`
	Answers map[string]struct {
		Type          string             `json:"type"`
		Choice        string             `json:"choice"`
		Confidence    float64            `json:"confidence"`
		Probabilities map[string]float64 `json:"probabilities"`
	} `json:"answers"`
	Usage struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
}

type server struct {
	expectedKey string
	client      *http.Client

	mu        sync.Mutex
	decisions []decision
	calls     int64
	fallbacks int64
	failures  int64
}

// envOr returns a trimmed environment value, or the fallback when it is unset.
func envOr(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func main() {
	// A managed plugin is handed its address: the gateway reserves a free port,
	// sets META_GATEWAY_PLUGIN_ADDR, and health-checks that exact address. A
	// packaged plugin that fell back to a fixed port would listen where the
	// gateway is not looking and could never install.
	addr := flag.String("addr", envOr("META_GATEWAY_PLUGIN_ADDR", ":9110"), "listen address (default: $META_GATEWAY_PLUGIN_ADDR)")
	noKey := flag.Bool("no-key", false, "do not require X-Plugin-Key")
	keyFlag := flag.String("key", "", "expected X-Plugin-Key (default: $META_GATEWAY_PLUGIN_KEY)")
	dumpManifest := flag.Bool("dump-manifest", false, "print plugin.json and exit (used by the release build)")
	flag.Parse()

	if *dumpManifest {
		encoded, err := json.MarshalIndent(manifestJSON(), "", "  ")
		if err != nil {
			log.Fatal(err)
		}
		fmt.Println(string(encoded))
		return
	}

	expectedKey := strings.TrimSpace(*keyFlag)
	if expectedKey == "" {
		expectedKey = strings.TrimSpace(os.Getenv("META_GATEWAY_PLUGIN_KEY"))
	}
	requireKey := !*noKey && expectedKey != ""

	srv := &server{
		expectedKey: expectedKey,
		client:      &http.Client{Timeout: 30 * time.Second},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/plugin.json", srv.handleManifest)
	mux.HandleFunc("/healthz", srv.auth(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "ok")
	}, requireKey))
	mux.HandleFunc("/hooks/route", srv.auth(srv.handleRouteHook, requireKey))
	mux.HandleFunc("/api/state", srv.auth(srv.handleState, requireKey))
	mux.HandleFunc("/", srv.handlePage)

	log.Printf("%s listening on %s (requireKey=%v)", pluginID, *addr, requireKey)
	if err := http.ListenAndServe(*addr, mux); err != nil {
		log.Fatal(err)
	}
}

func (s *server) auth(next http.HandlerFunc, requireKey bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if requireKey && r.Header.Get("X-Plugin-Key") != s.expectedKey {
			http.Error(w, "bad key", http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

// handleManifest declares the single virtual model this plugin answers for.
//
// The hook timeout (2000ms) is deliberately larger than the plugin's own Jev
// budget (1200ms): the gateway's timer also covers the round trip to this
// process, and a hook that fires its own timeout is indistinguishable from an
// outage.
// manifestJSON is the plugin.json this service serves, in one place so the
// packaged copy (tools/plugins/jev-router/build-release.ps1 asks for it with
// -dump-manifest) can never drift from the served one.
func manifestJSON() map[string]any {
	return map[string]any{
		"id":           pluginID,
		"version":      pluginVersion,
		"name":         "Jev 自动选路",
		"description":  "用 TypeSafe Jev 为 " + virtualModel + " 的请求选模型：先判场景，再在该场景自己的候选里选。",
		"capabilities": []string{"admin_page"},
		"permissions":  []string{"relay:intercept"},
		"page_path":    "/",
		"health_path":  "/healthz",
		// What a MANAGED install runs: the host downloads the package, unpacks it
		// next to this file, and starts this entrypoint. Without it the plugin can
		// only ever be registered by hand against an already-running service.
		"entrypoint": "jev-router",
		"config_fields": []map[string]any{
			{"key": "api_key", "type": "secret", "label": "TypeSafe API Key", "required": true,
				"description": "api.typesafe.ai 的密钥，只保存在网关侧并以请求头下发给插件。"},
			{"key": "scenarios", "type": "model_groups", "label": "场景路由（推荐）",
				"description": "先用一次判断选场景，再在该场景的模型里选；场景只剩一个可路由模型时省掉第二次判断。模型列表来自本网关实际可路由的模型。",
				// Empty on purpose: a manifest cannot know this gateway's models, so
				// any example it shipped would name models that do not exist here.
				// The editor starts from the live list instead.
				"default": "[]"},
			{"key": "default_model", "type": "model", "label": "兜底模型",
				"description": "Jev 不确定或不可用时交给它；留空则拒绝本次钩子，让网关按原逻辑报错。"},
			{"key": "min_confidence", "type": "number", "label": "最低置信度", "default": defaultMinConfidence,
				"description": "低于该值改用兜底模型。0 = 永远相信 Jev 的选择。"},
			{"key": "base_url", "type": "string", "label": "API 根地址", "default": defaultBaseURL, "advanced": true},
			{"key": "model", "type": "string", "label": "裁判模型", "default": defaultModel, "advanced": true,
				"description": "用哪个 System One 模型做判断，默认 jev-latest。这是 TypeSafe 侧的模型名，不是网关的。"},
			{"key": "scenario_instructions", "type": "string", "label": "场景判断指令", "advanced": true,
				"default": "Which kind of work is this request asking for?"},
			{"key": "instructions", "type": "string", "label": "判断指令（未配场景时）", "advanced": true,
				"default": "Which model should answer this request?"},
			{"key": "candidates", "type": "text", "label": "候选模型（未配场景时）", "advanced": true,
				"description": "仅当场景为空时生效。每行一个：模型名: 它擅长什么；描述会作为 Jev 的选择依据。都留空则用网关当前可路由的全部模型。",
				"default":     "claude-sonnet-4: 长上下文代码生成与重构\ngpt-5: 复杂推理、数学与长链条分析\ndeepseek-chat: 简单问答，成本最低"},
			{"key": "jev_timeout_ms", "type": "number", "label": "判断超时(毫秒)", "default": defaultJevTimeoutMs, "advanced": true,
				"description": "单次判断的超时。必须小于钩子声明的 2000ms，因为网关的计时还包含到本插件的往返。"},
			{"key": "summary_runes", "type": "number", "label": "上下文截断(字符)", "default": defaultSummaryRunes, "advanced": true,
				"description": "发给 Jev 的上下文长度。只发最后一条用户消息：判断是关于最新请求的。"},
		},
		"hooks": map[string]any{
			"route": map[string]any{
				"path":         "/hooks/route",
				"match_models": []string{virtualModel},
				"timeout_ms":   2000,
				"priority":     10,
			},
		},
	}
}

func (s *server) handleManifest(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, manifestJSON())
}

// routeDecision is what one routing pass concluded.
type routeDecision struct {
	// Model is empty when the hook declines: the gateway then reports its own
	// routing error, which is more honest than a guessed model.
	Model      string
	Confidence *float64
	Reason     string
	// Fallback names the rule that answered instead of Jev ("" = Jev decided).
	Fallback string
	// Scenario is the scenario Jev picked, when scenarios are configured.
	Scenario string
}

func (s *server) handleRouteHook(w http.ResponseWriter, r *http.Request) {
	started := time.Now()
	raw, err := io.ReadAll(io.LimitReader(r.Body, 8<<20))
	if err != nil {
		writeJSON(w, hookOutput{Handled: false})
		return
	}
	var input hookInput
	if err := json.Unmarshal(raw, &input); err != nil {
		writeJSON(w, hookOutput{Handled: false})
		return
	}
	cfg := decodeConfig(r.Header.Get("X-Plugin-Config")).withDefaults()

	available := make(map[string]struct{}, len(input.AvailableModels))
	for _, model := range input.AvailableModels {
		available[model] = struct{}{}
	}
	prompt := summarize(input.Body, cfg.SummaryRunes)
	base := decision{
		At:        time.Now().UTC(),
		RequestID: input.RequestID,
		Client:    input.Headers["X-Meta-Client"],
		Prompt:    prompt,
	}

	// No key: nothing to ask. Decline rather than invent a model.
	if strings.TrimSpace(cfg.APIKey) == "" {
		s.record(base, "no_refresh", "缺少 TypeSafe API Key", started)
		writeJSON(w, hookOutput{Handled: false, Reason: "missing api key"})
		return
	}

	outcome := s.decide(r.Context(), cfg, input, prompt, available)
	base.Scenario = outcome.Scenario
	if outcome.Model == "" {
		s.record(base, outcome.Fallback, outcome.Reason, started)
		writeJSON(w, hookOutput{Handled: false, Reason: outcome.Reason})
		return
	}
	s.record(base, outcome.Fallback, outcome.Reason, started, outcome.Model, outcome.Confidence)
	writeJSON(w, hookOutput{
		Handled:    true,
		Model:      outcome.Model,
		Reason:     outcome.Reason,
		Confidence: outcome.Confidence,
	})
}

// decide picks the model for one request.
//
// Two shapes, chosen by whether the operator configured scenarios:
//
//   - Scoped: ask which KIND of work this is, then ask inside that scenario's
//     own shortlist. The second question is far narrower ("which of these
//     three coding models" rather than "which of these twelve"), and a
//     scenario with a single routable model costs no second call at all.
//   - Flat: one question over the whole shortlist.
func (s *server) decide(ctx context.Context, cfg config, input hookInput, prompt string, available map[string]struct{}) routeDecision {
	fallbackModel := routable(cfg.DefaultModel, available)
	state := map[string]any{
		"client":            input.Headers["X-Meta-Client"],
		"streaming":         input.Stream,
		"last_user_message": prompt,
	}

	scenarios := usableScenarios(parseScenarios(cfg.Scenarios), available)
	if len(scenarios) > 0 {
		return s.decideScoped(ctx, cfg, state, scenarios, available, fallbackModel)
	}

	candidates := selectCandidates(parseCandidates(cfg.Candidates), available)
	if len(candidates) == 0 {
		// No usable candidate: answer with the fallback if the gateway can
		// route it, otherwise decline and let routing speak for itself.
		return fallbackOrDecline(fallbackModel, "no_candidate", defaultDeclineMessage)
	}
	criteria := make(map[string]any, len(candidates))
	for _, item := range candidates {
		if item.Description == "" {
			criteria[item.Model] = nil
			continue
		}
		criteria[item.Model] = item.Description
	}
	chosen, confidence, reason, err := s.askChoice(ctx, cfg, state, cfg.Instructions, criteria)
	if err != nil {
		return fallbackOrDecline(fallbackModel, "jev_error", err.Error())
	}
	if _, ok := available[chosen]; !ok {
		// A stale shortlist or a hallucinated name: pick the fallback instead
		// of failing at routing with a model the client never sent.
		return fallbackOrDecline(fallbackModel, "unroutable_choice", "Jev chose "+chosen+", which has no route")
	}
	if confidence < *cfg.MinConfidence && fallbackModel != "" {
		return routeDecision{
			Model:      fallbackModel,
			Confidence: &confidence,
			Fallback:   "low_confidence",
			Reason:     fmt.Sprintf("low confidence (%.2f) on %s", confidence, chosen),
		}
	}
	return routeDecision{Model: chosen, Confidence: &confidence, Reason: reason}
}

// decideScoped runs the two-step shape: scenario first, then a model inside it.
func (s *server) decideScoped(ctx context.Context, cfg config, state map[string]any, scenarios []scenario, available map[string]struct{}, fallbackModel string) routeDecision {
	criteria := make(map[string]any, len(scenarios))
	for _, item := range scenarios {
		if item.Hint == "" {
			criteria[item.Name] = nil
			continue
		}
		criteria[item.Name] = item.Hint
	}
	chosen, confidence, reason, err := s.askChoice(ctx, cfg, state, cfg.ScenarioInstructions, criteria)
	if err != nil {
		return fallbackOrDecline(fallbackModel, "jev_error", err.Error())
	}
	matched, ok := findScenario(scenarios, chosen)
	if !ok {
		return fallbackOrDecline(fallbackModel, "unknown_scenario", "Jev chose "+chosen+", which is not a configured scenario")
	}
	if confidence < *cfg.MinConfidence && fallbackModel != "" {
		return routeDecision{
			Model:      fallbackModel,
			Confidence: &confidence,
			Fallback:   "low_confidence",
			Scenario:   matched.Name,
			Reason:     fmt.Sprintf("scenario %q is unclear (confidence %.2f)", matched.Name, confidence),
		}
	}
	if len(matched.Models) == 1 {
		// The scenario already decided: one model left, nothing to ask.
		return routeDecision{
			Model:      matched.Models[0].Model,
			Confidence: &confidence,
			Scenario:   matched.Name,
			Reason:     fmt.Sprintf("%s → %s; %s", matched.Name, matched.Models[0].Model, reason),
		}
	}
	modelCriteria := make(map[string]any, len(matched.Models))
	for _, item := range matched.Models {
		modelCriteria[item.Model] = nil
	}
	instructions := fmt.Sprintf("This request is %s. Which model should answer it?", describeScenario(matched))
	picked, pickedConfidence, pickedReason, err := s.askChoice(ctx, cfg, state, instructions, modelCriteria)
	if err != nil {
		return fallbackOrDecline(fallbackModel, "jev_error", err.Error())
	}
	if _, ok := available[picked]; !ok {
		return fallbackOrDecline(fallbackModel, "unroutable_choice", "Jev chose "+picked+", which has no route")
	}
	if pickedConfidence < *cfg.MinConfidence && fallbackModel != "" {
		return routeDecision{
			Model:      fallbackModel,
			Confidence: &pickedConfidence,
			Fallback:   "low_confidence",
			Scenario:   matched.Name,
			Reason:     fmt.Sprintf("low confidence (%.2f) on %s in %s", pickedConfidence, picked, matched.Name),
		}
	}
	return routeDecision{
		Model:      picked,
		Confidence: &pickedConfidence,
		Scenario:   matched.Name,
		Reason:     fmt.Sprintf("%s → %s; %s", matched.Name, picked, pickedReason),
	}
}

// describeScenario renders a scenario for the second question's instructions.
func describeScenario(item scenario) string {
	if strings.TrimSpace(item.Hint) == "" {
		return fmt.Sprintf("%q", item.Name)
	}
	return fmt.Sprintf("%q (%s)", item.Name, item.Hint)
}

func findScenario(scenarios []scenario, name string) (scenario, bool) {
	for _, item := range scenarios {
		if item.Name == name {
			return item, true
		}
	}
	return scenario{}, false
}

// fallbackOrDecline answers with the configured fallback, or declines when the
// gateway cannot route it either.
func fallbackOrDecline(fallbackModel, category, reason string) routeDecision {
	if fallbackModel != "" {
		return routeDecision{Model: fallbackModel, Fallback: category, Reason: reason}
	}
	return routeDecision{Fallback: category, Reason: reason}
}

// askChoice sends one choice question and returns the picked option, its
// confidence, and a readable reason built from the probability spread.
//
// The two-step shape calls this twice with different criteria; the flat shape
// calls it once. Nothing here knows which, so the scenario question and the
// model question cannot drift apart in how they are asked or interpreted.
func (s *server) askChoice(ctx context.Context, cfg config, state map[string]any, instructions string, criteria map[string]any) (string, float64, string, error) {
	payload := systemOneRequest{
		State: state,
		Model: cfg.Model,
		Questions: map[string]any{
			"choice": map[string]any{
				"type":         "choice",
				"instructions": instructions,
				"criteria":     criteria,
			},
		},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", 0, "", err
	}
	url := strings.TrimRight(cfg.BaseURL, "/") + "/v1/systemone"
	ctxWithTimeout, cancel := context.WithTimeout(ctx, time.Duration(cfg.JevTimeoutMs)*time.Millisecond)
	defer cancel()
	req, err := http.NewRequestWithContext(ctxWithTimeout, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return "", 0, "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(cfg.APIKey))
	resp, err := s.client.Do(req)
	if err != nil {
		return "", 0, "", err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", 0, "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", 0, "", fmt.Errorf("jev status %d: %s", resp.StatusCode, truncate(string(raw), 200))
	}
	var decoded systemOneResponse
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return "", 0, "", fmt.Errorf("jev decode: %w", err)
	}
	answer, ok := decoded.Answers["choice"]
	if !ok || strings.TrimSpace(answer.Choice) == "" {
		return "", 0, "", fmt.Errorf("jev answered without a choice")
	}
	return answer.Choice, answer.Confidence, describeChoice(answer.Choice, answer.Confidence, answer.Probabilities), nil
}

// describeChoice renders the runner-up so the operator can see a near miss.
func describeChoice(chosen string, confidence float64, probabilities map[string]float64) string {
	reason := fmt.Sprintf("Jev chose %s (confidence %.2f)", chosen, confidence)
	runnerUp, runnerUpValue := "", 0.0
	for model, value := range probabilities {
		if model == chosen {
			continue
		}
		if value > runnerUpValue {
			runnerUp, runnerUpValue = model, value
		}
	}
	if runnerUp != "" && runnerUpValue > 0 {
		reason += fmt.Sprintf("; next was %s at %.2f", runnerUp, runnerUpValue)
	}
	return reason
}

// selectCandidates keeps only candidates the gateway can actually route. When
// the operator configured none, every routable model becomes a candidate with
// no description — Jev judges on the name alone, which is still better than
// refusing to route.
func selectCandidates(configured []candidate, available map[string]struct{}) []candidate {
	if len(configured) == 0 {
		out := make([]candidate, 0, len(available))
		for model := range available {
			out = append(out, candidate{Model: model})
		}
		sort.Slice(out, func(i, j int) bool { return out[i].Model < out[j].Model })
		return out
	}
	out := make([]candidate, 0, len(configured))
	for _, item := range configured {
		if _, ok := available[item.Model]; ok {
			out = append(out, item)
		}
	}
	return out
}

// routable reports whether the gateway can route this model ("" when unusable).
func routable(model string, available map[string]struct{}) string {
	model = strings.TrimSpace(model)
	if model == "" {
		return ""
	}
	if _, ok := available[model]; !ok {
		return ""
	}
	return model
}

// summarize extracts the last user message from an OpenAI- or Anthropic-shaped
// chat body and truncates it. Sending the whole conversation would pay for
// tokens Jev does not need: the decision is about the newest request.
func summarize(body json.RawMessage, limit int) string {
	if len(body) == 0 {
		return ""
	}
	var payload struct {
		Messages []struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return truncate(string(body), limit)
	}
	for index := len(payload.Messages) - 1; index >= 0; index-- {
		message := payload.Messages[index]
		if message.Role != "user" {
			continue
		}
		return truncate(contentText(message.Content), limit)
	}
	return ""
}

// contentText accepts a plain string or a block array (Anthropic and newer
// OpenAI clients both send arrays).
func contentText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return text
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &blocks); err == nil {
		parts := make([]string, 0, len(blocks))
		for _, block := range blocks {
			if strings.TrimSpace(block.Text) != "" {
				parts = append(parts, block.Text)
			}
		}
		return strings.Join(parts, "\n")
	}
	return ""
}

func truncate(value string, limit int) string {
	runes := []rune(value)
	if limit <= 0 || len(runes) <= limit {
		return value
	}
	return string(runes[:limit]) + "…"
}

// decodeConfig reads the base64 JSON the gateway injects on every request.
func decodeConfig(header string) config {
	header = strings.TrimSpace(header)
	if header == "" {
		return config{}
	}
	decoded, err := base64.StdEncoding.DecodeString(header)
	if err != nil {
		return config{}
	}
	var cfg config
	if err := json.Unmarshal(decoded, &cfg); err != nil {
		return config{}
	}
	return cfg
}

func (s *server) record(base decision, fallback, reason string, started time.Time, chosenAndConfidence ...any) {
	entry := base
	entry.LatencyMs = int(time.Since(started).Milliseconds())
	if len(chosenAndConfidence) > 0 {
		entry.Chosen, _ = chosenAndConfidence[0].(string)
	}
	if len(chosenAndConfidence) > 1 {
		entry.Confidence, _ = chosenAndConfidence[1].(*float64)
	}
	entry.Reason = reason
	entry.Fallback = fallback
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	if fallback != "" {
		s.fallbacks++
	}
	if fallback == "jev_error" {
		s.failures++
	}
	s.decisions = append([]decision{entry}, s.decisions...)
	if len(s.decisions) > recentDecisions {
		s.decisions = s.decisions[:recentDecisions]
	}
}

func (s *server) handleState(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	decisionsCopy := append([]decision(nil), s.decisions...)
	calls, fallbacks, failures := s.calls, s.fallbacks, s.failures
	s.mu.Unlock()
	writeJSON(w, map[string]any{
		"plugin":    pluginID,
		"virtual":   virtualModel,
		"calls":     calls,
		"fallbacks": fallbacks,
		"failures":  failures,
		"decisions": decisionsCopy,
	})
}

func (s *server) handlePage(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	decisionsCopy := append([]decision(nil), s.decisions...)
	calls, fallbacks, failures := s.calls, s.fallbacks, s.failures
	s.mu.Unlock()

	var rows strings.Builder
	if len(decisionsCopy) == 0 {
		rows.WriteString(`<tr><td colspan="6" class="muted">还没有请求经过自动选路。</td></tr>`)
	}
	for _, item := range decisionsCopy {
		confidence := "-"
		if item.Confidence != nil {
			confidence = strconv.FormatFloat(*item.Confidence, 'f', 2, 64)
		}
		chosen := item.Chosen
		if chosen == "" {
			chosen = `<span class="muted">未改写</span>`
		}
		fallback := ""
		if item.Fallback != "" {
			fallback = `<span class="tag">` + escape(item.Fallback) + `</span>`
		}
		scenario := item.Scenario
		if scenario == "" {
			scenario = `<span class="muted">—</span>`
		} else {
			scenario = escape(scenario)
		}
		rows.WriteString("<tr><td class=\"mono\">" + item.At.Format("15:04:05") + "</td><td class=\"mono\">" +
			escape(shorten(item.RequestID, 18)) + "</td><td class=\"mono\">" + scenario + "</td><td class=\"mono\">" + chosen + "</td><td>" +
			escape(shorten(item.Reason, 90)) + fallback + "</td><td class=\"num\">" + confidence +
			"</td></tr>")
	}
	fmt.Fprintf(w, `<!doctype html>
<html lang="zh">
<head>
<meta charset="utf-8">
<title>Jev 自动选路</title>
<style>
  :root { color-scheme: light; }
  body { font-family: ui-sans-serif, system-ui, -apple-system, "Segoe UI", sans-serif; margin: 0; padding: 24px 28px 40px; background: #f7f4ef; color: #1c1a17; }
  h1 { font-size: 17px; margin: 0 0 4px; letter-spacing: .01em; }
  .sub { color: #6f675e; font-size: 12.5px; margin-bottom: 18px; }
  .stats { display: flex; gap: 26px; margin-bottom: 18px; }
  .stat { background: #fffcf8; border: 1px solid #e6e0d6; padding: 10px 16px; min-width: 96px; }
  .stat .k { color: #6f675e; font-size: 11px; letter-spacing: .06em; text-transform: uppercase; }
  .stat .v { font-size: 20px; font-variant-numeric: tabular-nums; margin-top: 2px; }
  code { background: #efebe4; padding: 1px 5px; border-radius: 3px; font-size: 12px; }
  table { width: 100%%; border-collapse: collapse; background: #fffcf8; border: 1px solid #e6e0d6; font-size: 12.5px; }
  th { text-align: left; font-weight: 600; color: #6f675e; font-size: 11px; letter-spacing: .06em; text-transform: uppercase; padding: 8px 10px; border-bottom: 1px solid #e6e0d6; }
  td { padding: 7px 10px; border-bottom: 1px dashed #ece6dc; vertical-align: top; }
  tr:last-child td { border-bottom: 0; }
  .mono { font-family: ui-monospace, SFMono-Regular, Menlo, monospace; font-size: 11.5px; }
  .num { font-variant-numeric: tabular-nums; }
  .muted { color: #8a8178; }
  .tag { display: inline-block; margin-left: 6px; padding: 0 5px; font-size: 10.5px; background: #f0e2d0; color: #7a5a2c; letter-spacing: .03em; }
</style>
</head>
<body>
<h1>Jev 自动选路</h1>
<p class="sub">客户端发送 <code>model: "%s"</code> 时，本插件先用 Jev 判断请求属于哪个场景，再在该场景的候选里选一个；网关随后按真实模型正常选路、计费与故障转移。</p>
<div class="stats">
  <div class="stat"><div class="k">判断次数</div><div class="v">%d</div></div>
  <div class="stat"><div class="k">降级</div><div class="v">%d</div></div>
  <div class="stat"><div class="k">Jev 失败</div><div class="v">%d</div></div>
</div>
<table>
  <thead><tr><th>时间</th><th>请求</th><th>场景</th><th>选中</th><th>依据</th><th>置信度</th></tr></thead>
  <tbody>%s</tbody>
</table>
<script>setTimeout(function(){ location.reload(); }, 5000);</script>
</body>
</html>`, virtualModel, calls, fallbacks, failures, rows.String())
}

func shorten(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	return value[:limit] + "…"
}

func escape(value string) string {
	replacer := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;")
	return replacer.Replace(value)
}

func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
}
