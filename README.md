# Meta Gateway

A production-oriented OpenAI-compatible relay gateway with multi-channel
routing, retries, encrypted credentials, discovery, check-in, exchange,
auditing, metrics, and online SQLite backups.

The embedded Web Admin is available at `http://127.0.0.1:4100/admin-ui/`
after the gateway starts.

## Quick Start

### Prerequisites

- Go 1.26.4+ (or Docker)
- Node.js 24+ for source builds
- SQLite (embedded, no external dependency)

### Using Go

```bash
# Clone and build
cd meta-gateway
cp .env.example .env
# Set ADMIN_TOKEN, MASTER_KEY, and METRICS_TOKEN to independent random values

cd web
npm ci
npm run build
cd ..
go build -o bin/meta-gateway ./cmd/server
ADMIN_TOKEN=my-admin-token MASTER_KEY=my-32-char-master-key-for-aes! ./bin/meta-gateway
```

Open `http://127.0.0.1:4100/admin-ui/` and connect with `ADMIN_TOKEN`. The
browser sends it only as an Admin Bearer token and retains it in memory or,
when requested, `sessionStorage`; it is never placed in cookies, URLs, or
`localStorage`.

### Using Docker Compose

```bash
cp .env.example .env
# Replace all required secret placeholders in .env.
docker compose up -d --build
```

### Verify

```bash
curl http://127.0.0.1:4100/healthz
# → {"status":"ok"}
```

## Configuration

| Env Variable | Default | Description |
| --- | --- | --- |
| `HTTP_ADDR` | `:4100` | Listen address |
| `DATA_DIR` | `./data` | SQLite storage directory |
| `ADMIN_TOKEN` | _(required)_ | Bearer token for admin endpoints |
| `MASTER_KEY` | _(required)_ | Encryption key for secrets at rest (32+ chars) |
| `METRICS_TOKEN` | _(required*)_ | Independent Bearer token for `/metrics` |
| `BACKUP_DIR` | `<DATA_DIR>/backups` | Confined online backup directory |
| `OUTBOUND_ALLOW_HOSTS` | empty | Exact trusted private upstream host exceptions |
| `OUTBOUND_ALLOW_CIDRS` | empty | Trusted private upstream network exceptions |
| `TRUSTED_PROXY_CIDRS` | empty | Peers allowed to supply forwarded client addresses |
| `TRUSTED_SCRAPER_CIDRS` | empty | Networks allowed to scrape without a metrics token |
| `RELAY_RATE_PER_MINUTE` / `RELAY_RATE_BURST` | `600` / `100` | Per-key relay limiter; rate `0` disables it |
| `ADMIN_RATE_PER_MINUTE` / `ADMIN_RATE_BURST` | `300` / `50` | Global Admin limiter; rate `0` disables it |
| `AUDIT_RETENTION_DAYS` / `AUDIT_RETENTION_ROWS` | `90` / `100000` | Audit ceilings; `0` disables that dimension |
| `RETRY_TIMES` | `2` | Maximum retries after the first upstream attempt |
| `COOLDOWN_SECONDS` | `30` | Fixed cooldown after a retryable member failure |
| `CHECKIN_ENABLED` | `false` | Start scheduled credential check-in |
| `CHECKIN_CRON` | `0 8 * * *` | Standard five-field check-in schedule |

`METRICS_TOKEN` may be empty only when `TRUSTED_SCRAPER_CIDRS` is configured.
See `.env.example` for all timeouts and body/header limits. Invalid security
settings fail startup.

## API Overview

### Health

```
GET /healthz → 200 {"status":"ok"}
```

### Admin (requires `Authorization: Bearer <ADMIN_TOKEN>`)

| Method | Path | Description |
| --- | --- | --- |
| GET | /admin/sites | List sites |
| POST | /admin/sites | Create site |
| GET | /admin/sites/{id} | Get site |
| PUT | /admin/sites/{id} | Update site |
| DELETE | /admin/sites/{id} | Delete site |
| GET | /admin/sites/{siteId}/credentials | List credentials for site |
| POST | /admin/sites/{siteId}/credentials | Create credential (encrypts secret) |
| DELETE | /admin/credentials/{id} | Delete credential |
| GET | /admin/channels | List channels |
| POST | /admin/channels | Create channel |
| GET | /admin/channels/{id} | Get channel |
| PUT | /admin/channels/{id} | Update channel |
| DELETE | /admin/channels/{id} | Delete channel |
| GET | /admin/routes | List routes |
| GET | /admin/routes/explain?model={model} | Explain candidate eligibility and priority |
| POST | /admin/routes | Create route |
| GET | /admin/routes/{id} | Get route |
| PUT | /admin/routes/{id} | Update route |
| DELETE | /admin/routes/{id} | Delete route |
| GET | /admin/routes/{routeId}/members | List route members |
| POST | /admin/routes/{routeId}/members | Create route member |
| PUT | /admin/route-members/{id} | Update route member |
| DELETE | /admin/route-members/{id} | Delete route member |
| GET | /admin/downstream-keys | List downstream keys |
| POST | /admin/downstream-keys | Create downstream key |
| DELETE | /admin/downstream-keys/{id} | Delete downstream key |
| GET | /admin/proxy-logs | List proxy logs |
| POST | /admin/discovery/channels/{id}/refresh | Refresh one channel's models and automatic routes |
| POST | /admin/discovery/refresh | Refresh all enabled channels with itemized results |
| GET | /admin/discovery/models?channel_id={id} | List durable discovered-model snapshots |
| PUT | /admin/credentials/{id}/checkin | Enable or disable scheduled check-in |
| POST | /admin/checkin/credentials/{id}/run | Run one credential check-in manually |
| POST | /admin/checkin/run | Run all check-in-enabled credentials |
| GET | /admin/checkin/logs | List and filter redacted check-in logs |
| POST | /admin/exchange/export | Export all or selected channels; secrets require explicit opt-in |
| POST | /admin/exchange/import | Atomically import canonical, New API, or AAH V2 channel assets |
| GET | /admin/audit-events | List append-only redacted audit events |
| POST | /admin/audit-events/cleanup | Apply configured retention now |
| GET | /admin/backups | List generated backup inventory |
| POST | /admin/backups | Create and verify an online SQLite backup |

### Public (requires `Authorization: Bearer <DownstreamKey>`)

| Method | Path | Description |
| --- | --- | --- |
| GET | /v1/models | Available models from routes |
| POST | /v1/chat/completions | Chat completions (supports SSE) |

## Example: Create a Channel and Call /v1

```bash
# 1. Create a downstream key
curl -s -X POST http://127.0.0.1:4100/admin/downstream-keys \
  -H "Authorization: Bearer my-admin-token" \
  -H "Content-Type: application/json" \
  -d '{"name":"my-app"}' | jq .
# → {"id":1,"name":"my-app","token":"mg-abc123...","enabled":true,...}

# 2. Create a site
curl -s -X POST http://127.0.0.1:4100/admin/sites \
  -H "Authorization: Bearer my-admin-token" \
  -H "Content-Type: application/json" \
  -d '{"name":"OpenAI","base_url":"https://api.openai.com","platform":"openai"}' | jq .

# 3. Create a credential (secret is encrypted at rest)
curl -s -X POST http://127.0.0.1:4100/admin/sites/1/credentials \
  -H "Authorization: Bearer my-admin-token" \
  -H "Content-Type: application/json" \
  -d '{"kind":"api_key","secret":"sk-your-real-key"}' | jq .

# 4. Create a channel
curl -s -X POST http://127.0.0.1:4100/admin/channels \
  -H "Authorization: Bearer my-admin-token" \
  -H "Content-Type: application/json" \
  -d '{"name":"GPT-4","site_id":1,"credential_id":1,"base_url":"https://api.openai.com","models_csv":"gpt-4,gpt-4-turbo","status":"enabled"}' | jq .

# 5. Discover models and create exact automatic routes
curl -s -X POST http://127.0.0.1:4100/admin/discovery/channels/1/refresh \
  -H "Authorization: Bearer my-admin-token" \
  | jq .

# 6. Call v1/chat/completions
curl -s -X POST http://127.0.0.1:4100/v1/chat/completions \
  -H "Authorization: Bearer mg-abc123..." \
  -H "Content-Type: application/json" \
  -d '{"model":"gpt-4","messages":[{"role":"user","content":"Hello!"}]}'
```

## Architecture

See [docs/architecture.md](docs/architecture.md) for the full architecture overview.
See [docs/operations.md](docs/operations.md) for deployment, outbound security,
metrics, audit retention, backup, and restore procedures.

The Web Admin covers day-to-day asset, routing, discovery, check-in, audit,
backup, and exchange operations. Metrics collection and offline restore remain
CLI/operations workflows rather than browser actions.

Routing evaluates higher numeric priority first and uses weight only within the
selected priority tier. If all eligible weights in a tier are zero, selection
is uniform. Retryable transport failures and transient upstream responses move
to another eligible channel; ordinary client-error responses do not retry.

Model discovery currently supports OpenAI-compatible and New API platforms.
Set the site `platform` or channel `type_hint` to `openai-compatible`, `openai`,
or `new-api`, then trigger a refresh manually.

Credential check-in supports `session` and `access_token` credentials for New
API and One API sites. Scheduling is disabled by default; enable individual
credentials through the Admin API and then set `CHECKIN_ENABLED=true`. New API
credentials may set `meta_json` to `{"platform_user_id": 42}` when the upstream
requires the `New-Api-User` header.

Channel exchange uses a strict versioned canonical format and supports
documented New API and All API Hub V2 compatibility inputs. Secret-bearing
exports contain plaintext API keys and return `Cache-Control: no-store`; use
metadata-only export unless portability is required. See
[docs/aah-exchange-format.md](docs/aah-exchange-format.md) for the complete
format, defaults, idempotency, and security contract.

## Security Boundary

All adapter, discovery, check-in, exchange, and relay requests use one outbound
policy. Only HTTP(S) URLs without userinfo are accepted. DNS answers are checked
at connect time, redirects are revalidated, cross-origin credentials are
removed, and loopback/private/link-local/special addresses are denied by
default. Environment proxy variables are intentionally ignored.

Allow a trusted self-hosted upstream by its exact hostname and, when needed,
the narrowest possible CIDR. Host exceptions do not include subdomains.

## Backup And Restore

Create backups with `POST /admin/backups`; callers cannot provide a filesystem
path. Restore only while the service is stopped:

```bash
DATA_DIR=/data BACKUP_DIR=/data/backups \
  meta-gateway restore --from meta-gateway-YYYYMMDDTHHMMSSZ-xxxxxxxxxxxx.db
```

The restored service must use the original `MASTER_KEY`. Backups contain
encrypted credentials, never the key itself.
