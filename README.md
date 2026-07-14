# Meta Gateway

A lightweight relay gateway for LLM API access. Routes chat completion requests to upstream API providers with model-based routing, secret encryption, and request logging.

## Quick Start

### Prerequisites

- Go 1.26.4+ (or Docker)
- SQLite (embedded, no external dependency)

### Using Go

```bash
# Clone and build
cd meta-gateway
cp .env.example .env
# Edit .env: set ADMIN_TOKEN and MASTER_KEY to strong random values

go build -o bin/meta-gateway ./cmd/server
ADMIN_TOKEN=my-admin-token MASTER_KEY=my-32-char-master-key-for-aes! ./bin/meta-gateway
```

### Using Docker Compose

```bash
ADMIN_TOKEN=my-admin-token MASTER_KEY=my-32-char-master-key-for-aes! docker compose up -d --build
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
| `RETRY_TIMES` | `2` | Maximum retries after the first upstream attempt |
| `COOLDOWN_SECONDS` | `30` | Fixed cooldown after a retryable member failure |
| `CHECKIN_ENABLED` | `false` | Start scheduled credential check-in |
| `CHECKIN_CRON` | `0 8 * * *` | Standard five-field check-in schedule |

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
