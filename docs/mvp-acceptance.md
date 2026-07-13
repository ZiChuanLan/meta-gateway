# MVP Acceptance (P0–P2)

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

## Out of scope for this child

- Multi-channel priority/weight/retry (parent P3)
- Model discovery / checkin (P4–P5)
- AAH import/export APIs (P6)
