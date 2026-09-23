# Architecture

## Overview

Meta Gateway is an OpenAI-compatible relay that selects upstream channels by
exact model route, priority, weight, and shared cooldown state.

```text
Client
  -> HTTP adapter (auth, validation, response commitment)
  -> Proxy service (attempts, retry policy, logs, credentials)
  -> Routing selector (eligibility, priority tiers, weighted choice)
  -> Store repositories (SQLite)
  -> Relay transport (context-bound upstream HTTP)

Admin
  -> Discovery service (eligibility, credentials, deterministic summaries)
  -> Platform adapter (bounded upstream /v1/models request)
  -> Discovery reconciliation (snapshot, models_csv, routes, members)

Admin / Cron scheduler
  -> Check-in service (eligibility, decryption, in-process exclusion)
  -> Platform check-in adapter (bounded upstream /api/user/checkin request)
  -> Check-in audit log (redacted result, reward, latency, source)

Admin
  -> Exchange parser (strict/compatible shape validation and normalization)
  -> Exchange service (HMAC identity, encryption, legacy matching)
  -> Exchange repository (one Site/Credential/Channel transaction)
  -> Discovery service after commit (ordered, redacted outcomes)

Inbound HTTP
  -> request ID, trusted client identity, structured access log
  -> endpoint authentication and isolated rate limiter
  -> bounded Admin JSON or streaming-safe relay

Outbound HTTP
  -> shared URL, DNS/IP, redirect, and credential-forwarding policy

Operations
  -> liveness/readiness and protected low-cardinality metrics
  -> append-only redacted Admin audit events and retention cleanup
  -> verified online SQLite backup and offline rollback-safe restore
```

## Packages

| Package | Responsibility |
| --- | --- |
| `cmd/server` | Production entry point: config → crypto → store → services → HTTP wiring |
| `cmd/e2e-mock`, `cmd/e2e-runner` | Black-box e2e fake upstream and client (docker-compose.e2e) |
| **HTTP layer** | |
| `internal/httpapi` | Router/composition root plus all handlers. Admin surface is split per resource (`admin_sites/connections/credentials/channels/routes/keys/usage/rules/ops.go`, shared `admin_validation.go`); relay endpoints live in `relay.go` |
| `internal/auth` | Admin + session tokens, downstream-key bearer auth, scopes, model filters, expiry/IP checks |
| `internal/ratelimit` | Process-local token buckets for Admin, downstream keys, groups, models |
| **Relay core** | |
| `internal/proxy` | The forward engine, split by responsibility: `proxy_forward.go` (candidate loop `ForwardWithMeta`), `proxy_keypool.go` (API-key pools, per-key breakers), `proxy_classify.go` (retryability, error categories), `proxy_health.go` (member/channel/key bookkeeping, usage recording), `proxy_rewrite.go` (model rename, reasoning downgrade, system prompt, header overrides), `proxy_streams.go` (first-chunk peek, silent-SSE detection), `proxy_direct.go` (route-free channel smoke test), plus `circuit_breaker.go`, `channel_gate.go`, `payload_rules.go`, `prompt_guard.go`, `jsonpath.go` + `upstream_map.go` + `upstream_map_validate.go` (channel endpoint/field mapping) |
| `internal/routing` | Pure candidate evaluation (priority tiers, weighted/latency/adaptive, single-channel pin, sticky sessions), explain output |
| `internal/relay` | Thin context-bound upstream HTTP transport |
| `internal/adapters` | Stateless platform integrations: forward adapters + N×M translation registry, model-list, check-in, account adapters |
| **Upstream account lifecycle** | |
| `internal/account` | Account probe/finance/key sync against upstream platforms |
| `internal/checkin` | Credential-scoped check-in orchestration + cron scheduler with catch-up |
| `internal/discovery` | Model-list refresh, reconcile into routes/members, passive recovery |
| `internal/healthsweep` | Jittered periodic channel health probes (operational/degraded/error) |
| **Persistence** | |
| `internal/store` | SQLite: tracked filename-keyed migrations, repo-per-entity stores, hot-path caches (downstream keys, groups), usage recording, GC |
| `internal/domain` | Shared entity structs and status/category constants |
| **Ops & integrations** | |
| `internal/alerts` | Configurable alert-rule evaluation (60s tick) |
| `internal/financesweep` | Proactive balance/token sweeps + daily summary through the notifier |
| `internal/plugins` | Plugin catalog, market, sidecars, module enable gates |
| `internal/runtimeconfig` | Persisted admin overrides applied live to running services |
| `internal/webdavsync` | Encrypted WebDAV backup pull sync + scheduler |
| `internal/exchange` | Versioned AAH/New API import/export incl. secret round-trip |
| `internal/backup` | Online SQLite backup + offline restore |
| `internal/maintenance` | Cron-driven orphan GC/VACUUM + daily BalanceSweeper (balance snapshot, retention prunes) |
| **Foundations** | |
| `internal/config` | Environment parsing/validation |
| `internal/crypto` | Master-key AES-GCM, fingerprints |
| `internal/outbound` | SSRF policy + shared HTTP client + per-channel proxy hooks |
| `internal/webhook` | Multi-channel notifier (webhook/bark/serverchan/telegram/SMTP) |
| `internal/usage` | Token usage parsing from OpenAI/Anthropic bodies |
| `internal/sitedetect` | AAH-style site platform detection |
| `internal/totp` | TOTP secret/verify for admin 2FA |
| `internal/observability` | Readiness state, Prometheus text metrics |
| `internal/webui` | Embedded console assets (`//go:embed dist`, built from `web/`) |

`httpapi` is the composition root and imports everything; nothing imports it
back. Services own their background loops and register stoppers with the
`httpapi` lifecycle registry.

## Web Admin (`web/src`)

| Path | Responsibility |
| --- | --- |
| `App.tsx` | Login flow + authenticated shell (sidebar, global search, routing) |
| `api/client.ts`, `api/types.ts` | Typed Admin API client (~110 endpoints) mirroring `internal/httpapi` |
| `i18n/` | `en.ts` + `zh.ts` dictionaries behind a parity test, provider in `index.tsx` |
| `features/` | One folder per page; `ops/` (per-panel files), `channels/`, `models/` hold dialogs, badges and pure helpers split out of the page components |
| `components/` | Design system (`ui.tsx`, `Drawer`, `ActionMenu`, `SecretRevealDialog`, charts, pickers) |
| `hooks/`, `lib/` | `useAdminMutation` (invalidation + pending state), pagination, pure formatting |
| `styles.css` | Single stylesheet, four breakpoint tiers (600/860/1100/1240) |

## Routing

Routes prefer an exact `model_pattern` match. If none exists, the longest enabled
wildcard pattern (`*` any run, `?` one rune) wins. `RouteMember` is the runtime
source of truth for priority and weight; Channel priority and weight are
compatibility/default metadata.

1. Load the enabled exact route and all member/channel/credential facts.
2. If no exact route, load the best wildcard route and its members.
3. Narrow members to the downstream key's bound group: a key may carry a
   `route_group_name`; members are filtered to `route_members.group_name`
   when the route defines that group, falling back to the `default` group
   when it does not (keys without a binding use `default`). The same
   channel may belong to several groups of one route — membership is
   unique per `(route_id, channel_id, group_name)`.
4. Exclude disabled members/channels, unavailable credentials, members in
   cooldown, and channels already attempted by the request.
5. Choose the highest numeric priority tier with eligible members.
6. Select by positive weight inside the tier.
7. If every weight in the tier is zero, select uniformly.

`GET /console/routes/explain?model=<model>` uses the same evaluator and returns
stable reason codes without changing state.

## Retry And Streaming

The proxy service retries transport failures and upstream statuses 408, 429,
500, 502, 503, and 504 with a full failure tally (member cooldown plus
channel consecutive-failure count). Upstream 4xx client errors are also
failed over to the next channel — channel capabilities are heterogeneous, so
one upstream may reject a request another accepts (e.g. a
`reasoning_effort` value one gateway supports and another does not) — but a
4xx only cools the member down; it never counts toward the channel
consecutive-failure tally or auto-disable, because the request itself may be
at fault. Local adapter/configuration errors are returned immediately, as
failover cannot help. A channel is attempted once per request, and retry count is bounded by `RETRY_TIMES` and candidate exhaustion. `CROSS_CHANNEL_FAILOVER_ENABLED=false` forces the request to stop after the first selected channel without changing the saved retry limit; same-channel API-key rotation remains enabled.

Retryable failure increments the member failure count and applies a fixed
cooldown. Success clears member failure state. Each upstream attempt writes one
ProxyLog row with request ID, channel, attempt number, latency, real upstream
status when available, and a redacted error category.

Downstream cancellation is propagated to upstream requests. The relay uses a
response-header timeout instead of a whole-request timeout, so an established
SSE stream can remain open until cancellation or upstream closure. No retry is
possible after response commitment.

## Database

SQLite runs in WAL mode. Embedded migrations are ordered, transactional, and
recorded in `schema_migrations`; each file executes once. P3 adds uniqueness for
exact model patterns and route/channel membership plus routing lookup indexes.
Back up an existing SQLite database before starting a P3 binary. If legacy data
contains duplicate exact routes or duplicate route/channel memberships, the
migration stops with a uniqueness error instead of silently choosing data to
delete.

Credentials are encrypted at rest, downstream keys are hashed, and no API or
ProxyLog field returns raw credential material.

P7 adds append-only audit events and backup inventory. Readiness performs a
bounded database probe. Online backup uses SQLite's backup API, verifies the
snapshot, and publishes it by atomic rename. Restoration is deliberately an
offline CLI operation and preserves the replaced database for rollback.

## Model Discovery

P4 resolves a channel adapter from `type_hint`, falling back to the attached
site's `platform`. OpenAI-compatible and New API registrations share the
OpenAI `GET /v1/models` protocol while retaining distinct source names.
Responses are bounded, strictly decoded, normalized, and sorted before any
database transaction starts.

A successful channel refresh atomically replaces `discovered_models`, updates
the canonical `channels.models_csv`, creates missing exact routes, and
reconciles automatic route members. Missing models disable only automatic,
non-overridden members. Existing routes and manual routing decisions remain
operator-owned. A transport, status, size, or payload error changes no state.

### Routing a model fills in what the gateway already knows

Creating a route is the moment a model name becomes callable, so it is also the
moment the gateway can answer the questions that name raises — how to call it
and which channels reach it — without asking the operator to run a second step:

- **Capabilities.** The built-in classifier is run inline (`AutoTag` is local and
  idempotent, and skips any row a manual entry or a curated catalog already
  owns). The external indexes are consulted for that one model *in the
  background* — a bounded queue (32) drained by `runModelBootstraps`, which
  logs and drops a failed sync because the periodic sweep is the safety net. A
  save must never wait on a download, and a full queue defers to the sweep
  rather than blocking. Wildcard patterns are skipped: `gpt-*` is a matcher, not
  a model id, and no catalog lists it.
- **Route members.** `POST /admin/routes/{id}/auto-match` attaches the enabled
  channels that verifiably serve the pattern (`ChannelsWithModel`) into a named
  member group, using the same intersection route creation uses, so a stale
  console selection can never invent a member. Inside that intersection,
  disabled / unknown / no-longer-serving ids are reported as `skipped`, while a
  channel already present in the target group is neither attached nor counted —
  re-running is a plain no-op. An *empty* `channel_ids` means "every current
  match" to the handler, whereas the store treats an empty request as no
  operation; the console therefore never posts an empty selection, because on
  the wire it reads as the opposite of "none".

## Check-in And Scheduling

P5 treats check-in as a credential-scoped capability independent from model
discovery and routing. New API and One API session/access-token credentials use
`POST /api/user/checkin`; New API can also receive a positive
`platform_user_id` from credential metadata as `New-Api-User`.

The numeric user id is resolved in this order: credential `meta_json` first
(`{"platform_user_id":1544}`, written by an AAH import or typed into the
connection editor's **User ID** field), then `/api/user/self` when empty. The
admin API accepts it as a number or a quoted string and canonicalizes it to a
bare JSON number. When neither source yields one, the run is recorded as
`user_id_unavailable` (not `upstream_status`) so the operator is pointed at the
field to fill in rather than at the upstream's probe reply.

Generic external check-in sites (platform `external-checkin`, e.g. 薄荷公益站
https://up.x666.me) are cookie-authenticated and not New-API-family: the
adapter POSTs (or GETs) a configurable `checkin_path` (default
`/api/checkin/spin`, stored in credential `meta_json`) with Origin/Referer
derived from the site URL, and treats HTTP 2xx as success unless the JSON
body says `success:false` (with the usual already-signed-in markers). They
run under the exact same scheduler, eligibility gates, audit log and alerts as
New-API credentials.

Manual single-target execution ignores only the per-credential scheduling flag.
All other eligibility rules still apply. Batch execution selects
`checkin_enabled` credentials in ID order and persists one redacted audit row
for every selected attempt, including unsupported, disabled, failed, and
concurrent-run skips. Network work never runs inside a database transaction.

The optional process-local scheduler uses one strict five-field cron expression
and the same service instance as Admin HTTP. That shared instance prevents two
in-process runs for the same credential. The expression is interpreted in the
`CHECKIN_TZ` timezone (IANA name, e.g. `Asia/Shanghai`) when set; otherwise the
process local timezone is used, which is UTC inside the default container image
(no `TZ`, no tzdata) — set `CHECKIN_TZ` to avoid an 8-hour shift for operators
in UTC+8. The timezone database is embedded in the binary, so named zones
resolve in any container.

The scheduler catches up a missed daily tick: on start or schedule re-enable it
runs once immediately when today's fire time already passed and no scheduled
run is recorded for today (seeded from `checkin_logs`). A fresh install with no
history never surprise-runs. A batch tolerates per-credential internal failures
(transient DB errors) as synthetic failed items; only cancellation aborts the
remaining credentials.

Existing credentials migrate with
check-in disabled, and `CHECKIN_ENABLED` defaults to false, so an upgrade cannot
silently introduce external requests.

## Channel Exchange

P6 adds a versioned exchange boundary for canonical Meta Gateway documents,
New API channel lists, and reduced All API Hub V2 credential profiles. Parsing,
URL/list normalization, range checks, and duplicate detection complete before
database mutation. A purpose-separated HMAC identifies normalized URL plus API
key without storing plaintext or relying on randomized ciphertext.

Each imported identity owns one dedicated Credential/Channel. Existing shared
CRUD credentials remain valid, but legacy adoption requires one unambiguous
channel and constant-time secret equality. Site, Credential, and Channel writes
use one dedicated repository transaction. Discovery runs only after commit and
continues with redacted per-channel outcomes; manual routing protection remains
inside the existing reconciliation service.

## Runtime Security And Operations

Private, loopback, link-local, metadata, and other special upstream addresses
are denied by default. DNS is validated during connection and redirects are
rechecked. Exact hostname and CIDR exceptions support explicitly trusted
self-hosted upstreams. Environment proxy variables are disabled because
proxy-side DNS would bypass this guarantee.

Forwarded client addresses are accepted only from configured proxy networks.
Relay limits are isolated by authenticated downstream key while Admin uses a
separate global limiter. Metrics use fixed-cardinality labels and a credential
separate from Admin. Logs, errors, metrics, and audits exclude raw URLs,
headers, bodies, keys, ciphertext, and database or crypto details.

`/healthz` reports liveness. `/readyz` also requires a ready lifecycle state
and usable SQLite connection. Server `WriteTimeout` remains zero so established
SSE streams survive until cancellation or upstream closure. Shutdown marks
readiness false before draining.

## Current Scope

P0-P7 cover repository bootstrap, Admin CRUD, encrypted credentials,
OpenAI-compatible Models and Chat Completions, SSE passthrough, multi-channel
routing, retry/cooldown, Explain, tracked migrations, and manually triggered
model discovery, plus credential check-in, redacted audit logs, and optional
cron scheduling, secure versioned AAH/New API exchange, SSRF enforcement,
trusted identities, rate limits, observability, audit retention, online backup,
offline restore, hardened containers, and Linux race-test CI. P8 adds an
embedded React Web Admin under `/console/` that consumes the existing
authenticated Admin contracts without changing their security or ownership
rules. Metrics collection and offline restore deliberately remain operational
interfaces outside the browser application.

## Forward Adapters (platform translation)

The relay path speaks the OpenAI wire contract to clients. Channels that natively
speak OpenAI (`openai-compatible`, `new-api`, `one-api`, and relay brands) are
forwarded verbatim by the default passthrough adapter. Native platforms are
translated through per-platform **forward adapters** registered in
`internal/adapters`:

| Platform | Adapter | Translation |
| --- | --- | --- |
| OpenAI-compatible | `OpenAIPassthroughAdapter` (default) | none — verbatim passthrough |
| Anthropic | `AnthropicForwardAdapter` | OpenAI chat ⇄ Messages API (`x-api-key`, `anthropic-version`); Anthropic SSE → OpenAI chunks |
| Gemini | `GeminiForwardAdapter` | OpenAI chat ⇄ `generateContent` / `streamGenerateContent` (`x-goog-api-key`); embeddings ⇄ `batchEmbedContents` |

The `ForwardAdapter` interface (`internal/adapters/forward.go`) covers: channel
matching (`IsFor`), upstream URL building, request transformation
(`TransformRequest`), response transformation (`TransformResponse`), SSE stream
wrapping (`WrapStream`), upstream auth headers (`AuthHeaders`), and provider
usage extraction (`ExtractUsage`). `proxy` resolves the adapter per channel via
`Registry.ResolveForward(typeHint, platform)` and falls back to the passthrough
adapter. Usage accounting runs on the converted OpenAI-style body (non-stream)
or the final SSE chunk (stream), so native channels report real token usage.

Adding a channel platform = one adapter implementation + one registration line.

## Channel Endpoint And Field Mapping (non-OpenAI upstreams)

The forward adapters assume an OpenAI-shaped upstream under a `/v1` root. Two
upstream families break that assumption, and one channel-level mechanism covers
both (`internal/proxy/upstream_map.go`, columns `channels.upstream_*`):

- **Wrong path root**: `JoinOpenAIPath` inserts `/v1`, so a Zhipu base
  (`https://open.bigmodel.cn/api/paas/v4`) became the non-existent
  `/api/paas/v4/v1/models`. `upstream_path_override` / `upstream_path_map`
  replaces the path; when a mapping is present the URL is built with
  `adapters.JoinRawPath` instead, so the base keeps its own root.
- **Wrong protocol**: TypeSafe's `POST /v1/systemone` takes
  `{state, model, questions}` and answers `{answers:{…}}` rather than OpenAI
  chat. `upstream_request_map` / `upstream_response_map` move fields between the
  two shapes with the payload_rules path language.

Order matters in both directions. Request: adapter `TransformRequest` → field
maps → upstream. Response: adapter `TransformResponse` → field maps → client, so
map paths always describe the client-facing document.

A channel with no mapping takes exactly the old path (the engine exits on
`Empty()`), so the feature is additive. Every map is fail-open: a malformed or
non-matching mapping forwards the original bytes and logs one line. The admin API
validates the grammar on save (`proxy.ValidateUpstreamMap`) because the runtime
failure mode of a typo is a silent no-op, which is indistinguishable from "the
upstream rejected it" to an operator.

### Provider profiles

The mapping of a non-OpenAI provider is a property of the provider, so it ships
with the provider (`internal/proxy/provider_profile.go`) instead of as a console
preset button the operator has to click and then verify by hand. Saving a channel
whose type resolves to a profile fills the four mapping columns in, and only
where the operator left them empty — a channel already carrying a request or
response map is treated as hand-built and left alone. The console's mapping
fields therefore act as an override and an inspection surface.

The console provider list carries the documented endpoint for each preset
(`web/src/connectionTypes.ts`), and `SplitEndpointBaseURL` peels a pasted
endpoint back to a root plus an override at save time, so a provider whose
`/models` and chat endpoint live at different depths (TypeSafe: `GET /v1/models`,
`POST /v1/systemone`) is reachable in both directions.

TypeSafe is the worked example, and every detail of it was measured against the
live API rather than inferred:

- `questions` is a map keyed by question id, not an array;
- a typed answer comes back as a number (`answers.<id>.noul`), so it needs a
  `template` entry to reach OpenAI's string `content`;
- its request model is strict — a body carrying one extra key (even
  `temperature`) is answered `400 api_usage_error`. An OpenAI-shaped body always
  brings `messages`, so the request map uses the `keep` form (a top-level body
  allowlist) to reduce the envelope to `{model, state, questions}`.

`keep` is a general primitive, not a TypeSafe special case: any upstream that
validates its own request model strictly needs it, and deletion alone cannot
express it because the set to delete depends on the client. Entries apply in
array order, which is why a profile reads `messages.0.content` before keeping —
the read has to happen while the node still exists.

Scope limit worth stating: this maps *one* question onto the call. System One
scores a state against a question, so a multi-turn conversation is not
expressible through this mapping, and a client that sends a leading system
message has that message become the state.

### Custom paths and per-request endpoints

Three additions make a one-field setup possible, the way new-api's Custom channel
and sub2api's passthrough make it possible:

- **`POST /v1/<unregistered>` passthrough** (`internal/httpapi/relay_custom.go`).
  The `/v1` surface is a fixed list, so an upstream speaking its own protocol had
  no entry point; now an unregistered path is forwarded verbatim to
  `<base>/<same path>`, body and response untouched. Registered endpoints win
  because the fallback is registered last. The path passes a closed allowlist
  (`adapters.IsSafeURLPathSuffix`: `[A-Za-z0-9_-]` plus `.` per segment, ≤8
  segments, ≤128 bytes each, no dots-only segment) for the same reason sub2api's
  `upstream_path_guard.go` exists: a client-controlled string concatenated into an
  upstream URL must not be able to change that URL's structure. Any query string
  is dropped, never forwarded.
- **Base URL splitting** (`adapters.SplitEndpointBaseURL`). An operator who
  pastes the whole endpoint (`https://api.typesafe.ai/v1/systemone`) gets it split
  at save time into a root plus `upstream_path_override`, so `<base>/v1/<path>`
  cannot double the version segment. The split is deliberately narrow
  (`carriesEndpoint`): only a version segment that is NOT last (`/v1/systemone`)
  or a trailing surface name the gateway itself routes (`/chat/completions`, which
  is how Perplexity is configured — its documented base has no `/v1` and
  `/v1/chat/completions` 404s). Everything else stays whole, because a base path
  segment is not necessarily an endpoint: `/api/paas/v4` is already an API root,
  while `/ok` or `/prefix` is a MOUNT PREFIX the upstream serves `/v1/...` under.
  Splitting a mount prefix drops `/v1/<path>` from every request on the channel;
  that regression shipped in v3.4.1 and was caught by the Compose E2E, so
  `TestMountPrefixBaseURLKeepsTheV1Root` now reproduces the contract locally
  instead of leaving a ~20 minute feedback loop as its only defence. `JoinOpenAIPath`
  resolves the same three base shapes and shares `isAPIRootPath` with the split;
  `JoinAnthropicPath` additionally treats an override whose first segment is a
  version as already-absolute (`/v1/messages`), because the split produces exactly
  that and the previous fallback appended a second `/v1` — reachable by hand before
  v3.4.1 and automatic after it.
- **Per-request endpoints** (`upstream_path` / `upstream_url`, from the request
  body or a payload-rule header). Resolved after the payload rules and before the
  send, so one model can be retargeted to another endpoint without a channel per
  endpoint. `adapters.EndpointOverrideURL` requires the URL form to stay on the
  channel's host: the request carries the channel's API key, so a caller-chosen
  host would be a credential-exfiltration primitive.

Every call records `proxy_logs.upstream_url` (migration
`104_proxy_log_upstream_url.sql`), rendered by `adapters.SafeURL` as scheme +
host + path with query/fragment/userinfo stripped — query strings routinely carry
keys, which is why sub2api's `safeUpstreamURL` cuts them before logging. The
serving URL is also echoed to the client in `X-Meta-Upstream-URL`.

> The FTS5 index over `proxy_logs` has a fixed column list. `logfts.go`
> compares `PRAGMA table_info(proxy_logs_fts)` against the expected columns and
> drops + rebuilds once when one is missing: a trigger referencing a column the
> index lacks would make every log INSERT fail, and FTS5 being a compile-time
> option means the failure must not be fatal to startup.

## Intermediate-Format Conversion Chain (pivot)

## Intermediate-Format Conversion Chain (pivot)

Client protocols (the downstream wire contract) and upstream platforms are
connected through a **pivot**: the internal OpenAI chat/completions format.
Every downstream protocol implements a `SegmentConverter`
(`internal/adapters/intermediate.go`) with four pieces: request-to-pivot
(`ToOpenAI`), pivot-to-response (`FromOpenAI`), path mapping (`PivotPath`), and
OpenAI-SSE-to-protocol stream wrapping (`WrapOpenAIStream`).

```text
client protocol --ToOpenAI--> OpenAI pivot --TransformRequest--> upstream format
upstream format --TransformResponse--> OpenAI pivot --FromOpenAI--> client protocol
```

`ComposeForwardAdapter` pairs a downstream segment with an upstream
`ForwardAdapter`; the upstream adapter keeps its own URL building, auth
headers, and stream reshaping, so no N×M conversion matrix is needed:

| Client protocol | Upstream platform | Adapter |
| --- | --- | --- |
| OpenAI | any | upstream adapter unchanged |
| Anthropic (`/v1/messages`) | Anthropic-native | verbatim passthrough (`messages` path) |
| Anthropic (`/v1/messages`) | OpenAI / Gemini | `ComposeForwardAdapter{AnthropicDownstreamSegment, upstream}` |

`proxy` composes automatically when `DownstreamProtocol=anthropic` meets a
non-Anthropic channel; the previous inline translation branches in the proxy
package were replaced by this composition (behavior unchanged). An `OnOpenAI`
hook on the composed adapter runs between the pivot step and the upstream
transform (system-prompt injection on translated requests). Adding a new client
protocol = one `SegmentConverter`; adding a new upstream platform stays one
`ForwardAdapter`.
