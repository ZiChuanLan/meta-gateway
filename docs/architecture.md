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
| `internal/adapters` | Stateless platform registry and upstream model listing |
| `internal/discovery` | Credential-aware refresh orchestration and redacted results |
| `internal/httpapi` | Admin and `/v1` HTTP adapters |
| `internal/auth` | Admin and downstream-key authentication |
| `internal/crypto` | AES-GCM credential encryption |

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

## Current Scope

P0-P4 cover repository bootstrap, Admin CRUD, encrypted credentials,
OpenAI-compatible Models and Chat Completions, SSE passthrough, multi-channel
routing, retry/cooldown, Explain, tracked migrations, and manually triggered
model discovery. Scheduled check-in, AAH exchange, broad hardening, and Web
Admin remain later phases.
