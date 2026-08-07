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
| `cmd/server` | Entry point, configuration, and server startup |
| `internal/config` | Environment parsing |
| `internal/domain` | Persisted entities and routing candidate facts |
| `internal/store` | SQLite migrations, CRUD, routing facts, and health state |
| `internal/routing` | Pure candidate evaluation, explain output, and weighted selection |
| `internal/proxy` | Retry orchestration, credentials, cooldown updates, and attempt logs |
| `internal/relay` | Context-bound upstream HTTP transport |
| `internal/adapters` | Stateless platform registry, model listing, and check-in capabilities |
| `internal/discovery` | Credential-aware refresh orchestration and redacted results |
| `internal/checkin` | Credential-scoped check-in orchestration and cron lifecycle |
| `internal/exchange` | Versioned formats, normalization, identity, import/export orchestration |
| `internal/httpapi` | Admin and `/v1` HTTP adapters |
| `internal/auth` | Admin and downstream-key authentication |
| `internal/crypto` | AES-GCM credential encryption and purpose-separated exchange HMAC identity |
| `internal/outbound` | Connect-time SSRF policy and shared HTTP clients |
| `internal/ratelimit` | Process-local token buckets for Admin and downstream keys |
| `internal/observability` | Readiness state and Prometheus text metrics |
| `internal/backup` | Confined online backup and offline restore workflow |
| `internal/webui` | Embedded Web Admin assets, cache policy, and bounded SPA fallback |
| `web` | React/TypeScript Web Admin source and browser-side Admin API client |

## Routing

Routes prefer an exact `model_pattern` match. If none exists, the longest enabled
wildcard pattern (`*` any run, `?` one rune) wins. `RouteMember` is the runtime
source of truth for priority and weight; Channel priority and weight are
compatibility/default metadata.

1. Load the enabled exact route and all member/channel/credential facts.
2. If no exact route, load the best wildcard route and its members.
3. Exclude disabled members/channels, unavailable credentials, members in
   cooldown, and channels already attempted by the request.
4. Choose the highest numeric priority tier with eligible members.
5. Select by positive weight inside the tier.
6. If every weight in the tier is zero, select uniformly.

`GET /console/routes/explain?model=<model>` uses the same evaluator and returns
stable reason codes without changing state.

## Retry And Streaming

The proxy service retries transport failures and upstream statuses 408, 429,
500, 502, 503, and 504. Ordinary 4xx responses are returned immediately. A
channel is attempted once per request, and retry count is bounded by
`RETRY_TIMES` and candidate exhaustion.

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

## Check-in And Scheduling

P5 treats check-in as a credential-scoped capability independent from model
discovery and routing. New API and One API session/access-token credentials use
`POST /api/user/checkin`; New API can also receive a positive
`platform_user_id` from credential metadata as `New-Api-User`.

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
non-Anthropic channel; the previous inline translation branches in `proxy.go`
were replaced by this composition (behavior unchanged). An `OnOpenAI` hook on
the composed adapter runs between the pivot step and the upstream transform
(system-prompt injection on translated requests). Adding a new client
protocol = one `SegmentConverter`; adding a new upstream platform stays one
`ForwardAdapter`.
