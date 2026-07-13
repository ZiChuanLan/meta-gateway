# MVP Acceptance

## C1 — Bootstrap

- [x] `go test ./...` passes
- [x] Binary builds: `go build -o bin/meta-gateway ./cmd/server`
- [x] Container builds: `docker compose build`
- [x] `GET /healthz` returns 200

## C2 — Admin CRUD

- [x] Admin Bearer auth required; unauthorized calls fail (401)
- [x] Site CRUD: create, list, get, update, delete
- [x] Channel CRUD: create with encrypted credential, list, get, update, delete
- [x] Route + RouteMember CRUD
- [x] DownstreamKey create/list/delete

## C3 — Single-channel relay

- [x] With one enabled channel + downstream key, `POST /v1/chat/completions` works
- [x] SSE passthrough when `stream=true`
- [x] Non-stream response passthrough

## C4 — Models

- [x] `GET /v1/models` returns configured models from routes

## C5 — Proxy logs

- [x] ProxyLog written with request metadata
- [x] No raw API keys in logs

## C6 — Docker

- [x] Dockerfile with multi-stage build
- [x] docker-compose.yml with persistence volume
- [x] `.env.example` documents required variables

## Configuration

See README.md for env vars. Minimal required:

- `ADMIN_TOKEN` — any string for Bearer auth
- `MASTER_KEY` — 32+ char string for AES-256-GCM
