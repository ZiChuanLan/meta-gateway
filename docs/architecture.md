# Architecture

## Overview

Meta Gateway is a lightweight HTTP relay gateway for LLM API access. It routes incoming chat completion requests to upstream API providers based on configured routes and channels.

### Layer Diagram

```
Clients (curl / apps / Cursor / Claude Code)
        │  DownstreamKey Auth
        ▼
┌──────────────────────────────────────┐
│  HTTP Server (chi)                    │
│  /v1/*    →  relay + single-channel   │
│  /admin/* →  management CRUD          │
│  /healthz →  health check             │
└──────────────┬───────────────────────┘
               │
        Domain + Store (SQLite)
               │
        Upstream Sites / APIs
```

## Packages

| Package | Responsibility |
| --- | --- |
| `cmd/server` | Entry point, config loading, server startup |
| `internal/config` | Environment variable parsing |
| `internal/domain` | Domain models (Site, Channel, Route, etc.) |
| `internal/store` | SQLite database, migrations, CRUD operations |
| `internal/crypto` | AES-256-GCM secret encryption/decryption |
| `internal/auth` | Admin (static token) and DownstreamKey auth |
| `internal/httpapi` | HTTP route handlers (admin + relay) |
| `internal/relay` | HTTP client for proxying to upstreams |

## Data Flow — Chat Completions

1. Client sends `POST /v1/chat/completions` with DownstreamKey Bearer auth
2. DownstreamAuth middleware validates the token hash
3. Relay handler looks up the route matching `model` field
4. Finds first enabled `RouteMember` → gets `Channel`
5. Resolves the credential (decrypts the API key at runtime)
6. Forwards the request to the upstream's `/v1/chat/completions`
7. Writes a `ProxyLog` entry (without secrets)
8. Returns the upstream response (SSE passthrough if `stream=true`)

## Database

SQLite with WAL mode. Schema in `internal/store/001_init.sql`.

### Entities

- **Site** — upstream API provider
- **Credential** — encrypted API keys/sessions per site
- **Channel** — relay target (upstream URL + model group)
- **Route** — model pattern to channel mapping
- **RouteMember** — channel binding with priority/weight
- **DownstreamKey** — client authentication tokens (SHA-256 hashed)
- **ProxyLog** — relay request metadata (no secrets)

## Security

- Secrets encrypted with AES-256-GCM using `MASTER_KEY`
- Downstream tokens stored as SHA-256 hashes
- Admin routes protected by static `ADMIN_TOKEN`
- No secrets logged or exposed in API responses

## Scope

This implementation covers P0–P2:

- P0: Repo layout, healthz, SQLite, Docker
- P1: Domain models, Admin CRUD, secret encryption
- P2: Single-channel relay, `/v1/models`, `/v1/chat/completions` with SSE
