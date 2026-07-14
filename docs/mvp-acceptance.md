# MVP Acceptance (P0-P5)

Evidence from local verification on 2026-07-14.

## C1 — Bootstrap

- [x] `go test ./...` passes (crypto, store, relay, httpapi integration)
- [x] Binary builds: `go build -o bin/meta-gateway ./cmd/server`
- [x] `GET /healthz` returns `{"status":"ok"}` (integration test + handler)
- [ ] Docker compose smoke (optional; image uses `golang:1.22-alpine`, local Go is 1.26 — verify when Docker available)

## C2 — Admin CRUD

- [x] Admin Bearer auth required; unauthorized calls fail (401) — integration test
- [x] Site / Credential / Channel / Route / RouteMember CRUD APIs present under `/admin/*`
- [x] Credential secrets encrypted with `MASTER_KEY` (AES-GCM); list responses use `has_secret` only
- [x] DownstreamKey create returns raw token once; list never returns hash/token

## C3 — Single-channel relay

- [x] Mock upstream non-stream chat completions via `/v1/chat/completions`
- [x] SSE passthrough when `stream=true`
- [x] Channel key resolved via encrypted credential

## C4 — Models

- [x] `GET /v1/models` returns enabled route patterns (fallback: channel `models_csv`)

## C5 — Proxy logs

- [x] ProxyLog written per relay attempt
- [x] Integration asserts logs do not contain upstream secret

## C6 — Packaging

- [x] Dockerfile multi-stage
- [x] docker-compose.yml + volume
- [x] `.env.example` documents `ADMIN_TOKEN` / `MASTER_KEY`

## P3 Evidence And Remaining Scope

- [x] Multi-channel priority tiers, weighted selection, retry, and cooldown (P3)
- [x] Authenticated route Explain endpoint with stable eligibility reasons (P3)
- [x] Tracked transactional SQLite migrations and P0-P2 upgrade coverage (P3)
- [x] Authenticated OpenAI-compatible and New API model discovery (P4)
- [x] Atomic discovery snapshots and automatic exact-route reconciliation (P4)
- [x] Mixed-result full refresh with redacted per-channel failures (P4)
- [x] Credential-scoped manual and scheduled New API / One API check-in (P5)
- [x] Redacted check-in logs, deterministic batches, and overlap exclusion (P5)
- [x] P0-P4 database upgrade defaults existing credentials to check-in disabled (P5)
- AAH import/export APIs (P6)
