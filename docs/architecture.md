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

## Routing

Routes match exact model names. `RouteMember` is the runtime source of truth
for priority and weight; Channel priority and weight are compatibility/default
metadata.

1. Load the enabled exact route and all member/channel/credential facts.
2. Exclude disabled members/channels, unavailable credentials, members in
   cooldown, and channels already attempted by the request.
3. Choose the highest numeric priority tier with eligible members.
4. Select by positive weight inside the tier.
5. If every weight in the tier is zero, select uniformly.

`GET /admin/routes/explain?model=<model>` uses the same evaluator and returns
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
in-process runs for the same credential. Existing credentials migrate with
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

## Current Scope

P0-P6 cover repository bootstrap, Admin CRUD, encrypted credentials,
OpenAI-compatible Models and Chat Completions, SSE passthrough, multi-channel
routing, retry/cooldown, Explain, tracked migrations, and manually triggered
model discovery, plus credential check-in, redacted audit logs, and optional
cron scheduling, plus secure versioned AAH/New API exchange. Broad P7
hardening and the P8 Web Admin remain later phases.
