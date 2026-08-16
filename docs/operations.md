# Operations

## Deployment

Meta Gateway supports one process with one SQLite database. Copy `.env.example`
to a private environment file, replace `ADMIN_TOKEN`, `MASTER_KEY`, and
`METRICS_TOKEN` with independent random values, then run:

```bash
docker compose up -d --build
docker compose ps
curl --fail http://127.0.0.1:4100/readyz
```

Compose has no fallback credentials. The image runs as UID/GID `10001` and
stores the database and backups under `/data`. A newly created named volume
inherits the correct ownership. Before upgrading an existing bind mount or old
volume, stop the service and make its data writable by UID/GID `10001`.

## Web Admin

The production Web Admin is embedded in the Go binary and served at
`/console/`. Extensionless nested paths such as `/console/routing` return the
SPA shell, while missing asset paths return `404` and never fall back to HTML.
HTML uses `Cache-Control: no-cache`; content-hashed assets use a one-year
immutable cache policy.

Connect with `ADMIN_TOKEN`, not `METRICS_TOKEN`. The token is sent only in the
`Authorization: Bearer` header. It remains in memory unless the operator opts
into tab-scoped `sessionStorage`; it is not stored in cookies, `localStorage`,
URLs, application configuration, or logs. A `401` response invalidates the UI
session. Credential secrets use password inputs, downstream token plaintexts
are stored MASTER_KEY-encrypted and re-viewable only through explicit reveal
calls (every reveal/rotate is audit-logged), and secret-bearing exports
are downloaded without a browser preview.

For a source build, generate the embedded distribution before compiling Go:

```bash
cd web
npm ci
npm run build
cd ..
go build -trimpath -o bin/meta-gateway ./cmd/server
```

Vite writes the production files to `internal/webui/dist`; `go:embed` then
packages that directory into the executable. The Dockerfile and CI perform the
same Node-before-Go build order.

## Outbound Policy

Public HTTP(S) upstreams need no exception. Private and special addresses are
denied by default. For a trusted internal service, configure the narrowest
exception:

```dotenv
OUTBOUND_ALLOW_HOSTS=llm.internal.example
OUTBOUND_ALLOW_CIDRS=10.24.8.15/32
```

Host exceptions are exact and do not include subdomains. CIDR exceptions apply
only to matching resolved addresses. Redirect targets and DNS answers are
checked on every connection. Environment proxy variables are ignored.

## Metrics And Readiness

`/healthz` is a process liveness check. `/readyz` returns `503` during draining
or when SQLite cannot be reached. `/metrics` uses Prometheus text format:

```bash
curl -H "Authorization: Bearer $METRICS_TOKEN" \
  http://127.0.0.1:4100/metrics
```

Only explicitly configured `TRUSTED_SCRAPER_CIDRS` may scrape without a token.
Do not reuse `ADMIN_TOKEN` as the metrics token.

## Audit Retention

Admin mutations, authentication failures, and Admin rate rejections produce
redacted append-only events. Cleanup runs daily using both configured ceilings:

- `AUDIT_RETENTION_DAYS=90`
- `AUDIT_RETENTION_ROWS=100000`

Set either value to `0` to disable only that dimension. Run the same policy on
demand with `POST /console/audit-events/cleanup`. There is no API to edit or
delete individual events.

## Backup

An authenticated `POST /console/backups` creates a consistent online snapshot in
`BACKUP_DIR`; `GET /console/backups` returns safe inventory metadata. The gateway
generates every filename and verifies SQLite integrity and schema before the
snapshot is published.

Copy completed backup files to separate storage according to the deployment's
retention policy. Protect them as sensitive encrypted configuration data.

## Restore

Stop the gateway and keep the original `MASTER_KEY` available. Restore accepts
only a generated backup basename under `BACKUP_DIR`:

```bash
docker compose stop meta-gateway
docker compose run --rm --no-deps meta-gateway \
  restore --from meta-gateway-YYYYMMDDTHHMMSSZ-xxxxxxxxxxxx.db
docker compose up -d meta-gateway
curl --fail http://127.0.0.1:4100/readyz
```

The command verifies the source, preserves the current database as a rollback
file, removes stale WAL/SHM files, atomically installs the snapshot, and opens
it through the normal Store path. If installation verification fails, it
restores the prior database. A different `MASTER_KEY` cannot decrypt restored
credentials; the key is never stored in backup inventory.

## Graceful Shutdown

Compose sends the normal termination signal and allows 20 seconds. The service
marks readiness unavailable, stops background work, and drains HTTP requests
within `SERVER_SHUTDOWN_TIMEOUT_SECONDS`. `WriteTimeout` remains disabled to
preserve long-running SSE; use an upstream proxy with streaming-aware limits.

## End-To-End Validation

Run the deterministic container E2E with explicit test secrets:

```bash
ADMIN_TOKEN=ci-admin-token \
MASTER_KEY=ci-master-key-at-least-32-characters \
METRICS_TOKEN=ci-metrics-token \
docker compose -f docker-compose.yml -f docker-compose.e2e.yml \
  up --build --abort-on-container-exit --exit-code-from e2e e2e
```

The CI workflow also restarts the gateway and runs `e2e-runner verify` against
the persisted volume. The E2E mock hostname is the only private destination
exception; a separate loopback target must remain blocked.
