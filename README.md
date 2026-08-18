# Meta Gateway

A production-oriented OpenAI-compatible relay gateway with multi-channel
routing, retries, encrypted credentials, discovery, check-in, exchange,
auditing, metrics, and online SQLite backups.

Beyond the core relay path it ships with: alert rules (metric → webhook),
sensitive prompt-guard rules, error passthrough/rewrite rules, a plugin
market, admin TOTP two-factor, redemption codes for downstream quota,
per-model metadata and not-found blacklists, routing decision snapshots,
health history with availability summaries, and scheduled database
maintenance (orphan GC + VACUUM).

Simple usage metering records prompt/completion tokens from upstream `usage` fields, enforces optional per-token quotas, and shows estimated cost in Admin → Tokens.

The embedded Web Admin is available at `http://127.0.0.1:4100/console/`
after the gateway starts.

The Admin **Store** (`/console/#/store` or sidebar **Store**) manages optional **add-ons** (Exchange import/export + WebDAV, Check-in). Core surfaces — connections, models, tokens, logs, runtime, discovery, **audit**, and **backups** — are always available and are not store-gated.

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

Open `http://127.0.0.1:4100/console/` and connect with `ADMIN_TOKEN`. The
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
| `ADMIN_TOKENS` | empty | Extra comma-separated admin tokens for rotation |
| `MASTER_KEY` | _(required)_ | Encryption key for secrets at rest (32+ chars) |
| `EXCHANGE_ALLOW_SECRET_EXPORT` | `true` | Allow `include_secrets` on exchange export |
| `METRICS_TOKEN` | _(required*)_ | Independent Bearer token for `/metrics` |
| `BACKUP_DIR` | `<DATA_DIR>/backups` | Confined online backup directory |
| `BACKUP_RETENTION_COUNT` | `30` | Maximum verified backup snapshots retained on disk (0 disables pruning) |
| `OUTBOUND_ALLOW_HOSTS` | empty | Exact trusted private upstream host exceptions |
| `OUTBOUND_ALLOW_CIDRS` | empty | Trusted private upstream network exceptions |
| `OUTBOUND_MAX_IDLE_CONNS` | `512` | Total outbound idle connection ceiling |
| `OUTBOUND_MAX_IDLE_CONNS_PER_HOST` | `64` | Per-upstream-host idle connection ceiling (Go default is 2) |
| `SQLITE_MAX_OPEN_CONNS` | `4` | SQLite connection-pool ceiling (WAL allows concurrent readers; `1` fully serializes) |
| `TRUSTED_PROXY_CIDRS` | empty | Peers allowed to supply forwarded client addresses |
| `TRUSTED_SCRAPER_CIDRS` | empty | Networks allowed to scrape without a metrics token |
| `RELAY_RATE_PER_MINUTE` / `RELAY_RATE_BURST` | `600` / `100` | Per-key relay limiter; rate `0` disables it |
| `ADMIN_RATE_PER_MINUTE` / `ADMIN_RATE_BURST` | `300` / `50` | Global Admin limiter; rate `0` disables it |
| `AUDIT_RETENTION_DAYS` / `AUDIT_RETENTION_ROWS` | `90` / `100000` | Audit ceilings; `0` disables that dimension |
| `HEALTH_HISTORY_RETENTION_DAYS` | `90` | Channel health-history retention; `0` disables pruning |
| `BALANCE_HISTORY_RETENTION_DAYS` / `DECISION_SNAPSHOT_RETENTION_DAYS` | `90` / `7` | Balance and routing-decision history retention; `0` disables each pruner |
| `RETRY_TIMES` | `2` | Retry rounds: how many additional channels are attempted after the first upstream attempt (each round = one more channel) |
| `CHANNEL_RETRY_TIMES` | `1` | Same-key re-sends: how many times a retryable failure is re-sent on the same upstream key before moving to the next key/channel (0-5; network errors fail fast after these) |
| `KEY_POOL_ROTATION` | `true` | Rotate through the site's API keys when one fails; off = only the channel's bound key is used |
| `CHANNEL_AUTO_DISABLE_THRESHOLD` | `5` | Consecutive relay failures before a channel is auto-disabled (0 disables) |
| `RECOVERY_PROBE_ENABLED` / `RECOVERY_PROBE_INTERVAL_SECONDS` | `true` / `600` | Re-probe auto-disabled channels for recovery |
| `ROUTING_LATENCY_AWARE` / `ROUTING_ERROR_AWARE` / `ROUTING_CONCURRENCY_AWARE` | `true` | Routing signal toggles (latency, error history, concurrency load) |
| `ROUTING_CONCURRENCY_LIMIT` | `64` | Per-model in-flight ceiling for the concurrency signal |
| `STABLE_FIRST_ENABLED` / `STABLE_FIRST_DENOMINATOR` / `STABLE_FIRST_PROMOTE_REQUESTS` | `false` / `25` / `100` | Gray-release routing: a `stable_first` channel takes ~1/N of traffic until it accumulates enough successful requests, then is promoted |
| `HEALTH_SWEEP_ENABLED` / `HEALTH_SWEEP_INTERVAL_SECONDS` | `true` / `300` | Proactive health sweep (latency sampling) over channels; successful probes also refresh the connectivity verdict |
| `ALERT_CONFIG_JSON` | empty | Alert matrix JSON (bark / serverchan / telegram / SMTP + cooldown) |
| `ALERT_SWEEP_INTERVAL_SECONDS` / `ALERT_DAILY_SUMMARY_INTERVAL_SECONDS` | `0` / `0` | Alert evaluation and daily summary cadence (0 = off) |
| `CHECKIN_TZ` | empty | Timezone for `CHECKIN_CRON` (containers default to UTC; set e.g. `Asia/Shanghai`) |
| `RELAY_MODEL_RATE_PER_MINUTE` / `RELAY_MODEL_RATE_BURST` | `0` / `0` | Optional per-model relay limiter (0 disables) |
| `PLUGIN_CATALOG_URL` | empty | Extra plugin market registry URLs (comma-separated) |
| `CROSS_CHANNEL_FAILOVER_ENABLED` | `true` | Whether failed requests may move to another channel; disabled means only the first selected channel is tried |
| `COOLDOWN_SECONDS` | `30` | Fixed cooldown after a retryable member failure |
| `FAULT_PROTECTION_ENABLED` | `true` | Master switch for fixed cooldown and channel auto-disable; retries and failover remain active when off |
| `STICKY_ENABLED` / `STICKY_TTL_MINUTES` | `false` / `30` | Sticky-session routing toggle + binding TTL (hot-swappable) |
| `CHECKIN_ENABLED` | `false` | Start scheduled credential check-in |
| `CHECKIN_CRON` | `0 8 * * *` | Standard five-field check-in schedule |
| `PLUGINS_DIR` | `<DATA_DIR>/plugins` | Official module package directory |
| `WEBDAV_SYNC_ENABLED` | `false` | Enable read-only WebDAV backup pull |
| `WEBDAV_URL` / `WEBDAV_USERNAME` / `WEBDAV_PASSWORD` | empty | WebDAV bootstrap credentials |
| `WEBDAV_BACKUP_PASSWORD` | empty | Decrypt password for encrypted AAH envelopes |
| `WEBDAV_CRON` | `0 */6 * * *` | WebDAV pull schedule |
| `WEBDAV_MAX_BYTES` | `10485760` | Max WebDAV download size |

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
| GET | /console/sites | List sites |
| POST | /console/sites | Create site |
| GET | /console/sites/{id} | Get site |
| PUT | /console/sites/{id} | Update site |
| DELETE | /console/sites/{id} | Delete site |
| GET | /console/site-type?url=… | Detect upstream platform (AAH chain) |
| POST | /console/connections | One-shot create: site + credential + channel with rollback |
| GET | /console/sites/{siteId}/credentials | List credentials for site |
| POST | /console/sites/{siteId}/credentials | Create credential (encrypts secret) |
| POST | /console/sites/{siteId}/credentials/{id}/reveal | Decrypt and view a stored credential secret (audit-logged) |
| DELETE | /console/credentials/{id} | Delete credential |
| GET | /console/channels | List channels |
| GET | /console/channels/overview | Channel overviews with health/readiness |
| GET | /console/search?q=… | Global search across assets |
| POST | /console/channels | Create channel |
| POST | /console/channels/{id}/duplicate | Duplicate a channel |
| POST | /console/channels/{id}/ping | Connectivity ping |
| GET | /console/channels/{id} | Get channel |
| PUT | /console/channels/{id} | Update channel |
| DELETE | /console/channels/{id} | Delete channel |
| POST | /console/reset | Factory reset (wipes business data) |
| GET | /console/routes | List routes |
| GET | /console/routes/overview | Route overviews with members |
| GET | /console/routes/explain?model={model} | Explain candidate eligibility and priority |
| POST | /console/routes | Create route |
| GET | /console/routes/{id} | Get route |
| PUT | /console/routes/{id} | Update route |
| DELETE | /console/routes/{id} | Delete route |
| GET | /console/routes/{routeId}/members | List route members |
| POST | /console/routes/{routeId}/members | Create route member |
| PUT | /console/route-members/{id} | Update route member |
| POST | /console/route-members/{id}/clear-health | Clear member failure/cooldown state |
| DELETE | /console/route-members/{id} | Delete route member |
| GET | /console/downstream-keys | List downstream keys |
| POST | /console/downstream-keys | Create downstream key (plaintext stored encrypted) |
| PUT | /console/downstream-keys/{id} | Update downstream key |
| DELETE | /console/downstream-keys/{id} | Delete downstream key |
| POST | /console/downstream-keys/{id}/reveal | Re-view the stored plaintext token (audit-logged) |
| POST | /console/downstream-keys/{id}/rotate | Issue a new token, old one dies instantly (audit-logged) |
| GET | /console/usage/summary | Usage summary (requests/tokens/cost) |
| GET | /console/usage?limit=… | Usage records |
| GET/PUT | /console/ratios | Model cost ratios (1.0 = no markup) |
| GET/PUT/DELETE | /console/groups | Tenant groups (quotas / rate limits) |
| PATCH | /console/channels/tag/{tag} | Bulk channel operations by tag |
| GET | /console/sticky | Sticky-session routing stats |
| GET | /console/proxy-logs | List proxy logs |
| GET | /console/proxy-logs/latency-histogram | Latency distribution |
| GET | /console/decision-snapshot | Routing decision audit trail |
| GET/DELETE | /console/model-blocks | Model not-found blacklist |
| POST/GET/DELETE | /console/redemption-codes | Quota top-up vouchers |
| GET/PUT/DELETE | /console/model-metadata | Per-model capability annotations |
| GET | /console/health-history?channel_id=&hours= | Recent probe points |
| GET | /console/health-history/summary?hours= | Per-channel availability summaries |
| GET/POST/PUT/DELETE | /console/alert-rules | Alert rules (metric → webhook) |
| GET/POST/PUT/DELETE | /console/prompt-guards | Sensitive prompt guard rules |
| GET/POST/PUT/DELETE | /console/error-rules | Error passthrough/rewrite rules |
| POST/GET | /console/db/gc | Run / inspect database maintenance |
| GET/POST | /console/totp/status, /console/totp/setup, /console/totp/enable, /console/totp/disable | Admin TOTP two-factor |
| POST | /console/discovery/channels/{id}/refresh | Refresh one channel's models and automatic routes |
| POST | /console/discovery/refresh | Refresh all enabled channels with itemized results |
| GET | /console/discovery/models?channel_id={id} | List durable discovered-model snapshots |
| POST | /console/discovery/channels/{id}/probe | Probe models without persisting |
| POST | /console/channels/{id}/account/probe | Probe upstream account |
| POST | /console/channels/{id}/account/sync-keys | Sync sk- keys from upstream account |
| POST | /console/try/chat | Admin console chat probe |
| GET/POST | /console/plugins/* | Module catalog, install, enable, disable |
| GET/PUT | /console/webdav/* | Read-only WebDAV sync status and settings |
| GET/PUT/POST | /console/runtime-settings | Hot runtime overrides (retry, rates, check-in, audit) |
| PUT | /console/credentials/{id}/checkin | Enable or disable scheduled check-in |
| POST | /console/checkin/credentials/{id}/run | Run one credential check-in manually |
| POST | /console/checkin/run | Run all check-in-enabled credentials |
| GET | /console/checkin/logs | List and filter redacted check-in logs |
| POST | /console/exchange/export | Export all or selected channels; secrets require explicit opt-in |
| POST | /console/exchange/import | Atomically import canonical, New API, or AAH V2 channel assets |
| GET | /console/audit-events | List append-only redacted audit events |
| POST | /console/audit-events/cleanup | Apply configured retention now |
| GET | /console/backups | List generated backup inventory |
| POST | /console/backups | Create and verify an online SQLite backup |

### Public (requires `Authorization: Bearer <DownstreamKey>`)

| Method | Path | Description |
| --- | --- | --- |
| GET | /v1/models | Available models from routes |
| POST | /v1/chat/completions | Chat completions (supports SSE) |
| POST | /v1/completions | Text completions (OpenAI-compatible) |
| POST | /v1/embeddings | Embeddings |
| POST | /v1/responses | OpenAI Responses API (pass-through) |
| POST | /v1/messages | Anthropic Messages API (native clients) |
| POST | /v1/messages/count_tokens | Anthropic token counting |
| POST | /v1/images/generations, /v1/images/edits | Image generation (pass-through) |
| GET | /v1/dashboard/billing/credit_summary | Quota / credit summary for the key |
| POST | /v1/redemption/redeem | Redeem a quota top-up code |

Downstream key `scopes` are enforced: `relay` allows the full public surface;
otherwise use `models`, `chat`, `completions`, `embeddings`, `responses`,
and/or `messages`. Routes match
exact model names first, then the longest `*` / `?` wildcard pattern.

Connection type **Anthropic (Claude Official)** uses Anthropic auth headers and
`/v1/messages` on the wire. OpenAI chat clients still call `/v1/chat/completions`;
the gateway translates request and response for both non-stream and SSE traffic.

Connection type **Google Gemini (Official)** talks to
`generativelanguage.googleapis.com` (`x-goog-api-key`). OpenAI
`/v1/chat/completions` is translated to `generateContent` (non-stream and SSE),
and `/v1/embeddings` to `batchEmbedContents`. Model discovery lists Gemini models
via `GET /v1beta/models`.

Official modules (`exchange`, `checkin`, `operations`) are auto-installed on first
boot. Disable a module to hide its Admin API group; check-in scheduling also
requires the `checkin` module to be enabled.

## Example: Create a Channel and Call /v1

```bash
# 1. Create a downstream key
curl -s -X POST http://127.0.0.1:4100/console/downstream-keys \
  -H "Authorization: Bearer my-admin-token" \
  -H "Content-Type: application/json" \
  -d '{"name":"my-app"}' | jq .
# → {"id":1,"name":"my-app","token":"mg-abc123...","enabled":true,...}

# 2. Create a site
curl -s -X POST http://127.0.0.1:4100/console/sites \
  -H "Authorization: Bearer my-admin-token" \
  -H "Content-Type: application/json" \
  -d '{"name":"OpenAI","base_url":"https://api.openai.com","platform":"openai"}' | jq .

# 3. Create a credential (secret is encrypted at rest)
curl -s -X POST http://127.0.0.1:4100/console/sites/1/credentials \
  -H "Authorization: Bearer my-admin-token" \
  -H "Content-Type: application/json" \
  -d '{"kind":"api_key","secret":"sk-your-real-key"}' | jq .

# 4. Create a channel
curl -s -X POST http://127.0.0.1:4100/console/channels \
  -H "Authorization: Bearer my-admin-token" \
  -H "Content-Type: application/json" \
  -d '{"name":"GPT-4","site_id":1,"credential_id":1,"base_url":"https://api.openai.com","models_csv":"gpt-4,gpt-4-turbo","status":"enabled"}' | jq .

# 5. Discover models and create exact automatic routes
curl -s -X POST http://127.0.0.1:4100/console/discovery/channels/1/refresh \
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

Model discovery supports OpenAI-compatible, New API, Anthropic (official
`GET /v1/models`), and Gemini (official `GET /v1beta/models`) platforms.
Set the site `platform` or channel `type_hint` to `openai-compatible`, `openai`,
`new-api`, `anthropic`, or `gemini`, then trigger a refresh manually.

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

Create backups with `POST /console/backups`; callers cannot provide a filesystem
path. Restore only while the service is stopped:

```bash
DATA_DIR=/data BACKUP_DIR=/data/backups \
  meta-gateway restore --from meta-gateway-YYYYMMDDTHHMMSSZ-xxxxxxxxxxxx.db
```

The restored service must use the original `MASTER_KEY`. Backups contain
encrypted credentials, never the key itself.
