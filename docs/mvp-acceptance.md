# Backend Acceptance (P0-P7)

Evidence refreshed on 2026-07-14. Automated behavior is implemented in Go
unit and integration tests; Linux race behavior is enforced by CI.

## Foundation And Compatibility

- [x] Admin CRUD, Admin Bearer authentication, encrypted credentials, and
  one-time downstream key issuance
- [x] OpenAI-compatible models and chat completions, including SSE passthrough
  and downstream cancellation
- [x] Multi-channel priority/weight routing, retry, cooldown, Explain, and one
  metadata-only proxy log per attempt
- [x] Model discovery with atomic reconciliation and deterministic mixed-result
  refresh
- [x] Credential-scoped New API/One API check-in, redacted durable logs, and
  optional five-field cron scheduling
- [x] Versioned canonical, New API, and reduced AAH V2 exchange with HMAC
  identity, transactional persistence, and post-commit discovery

## P7 Security And Operations

- [x] One outbound policy covers adapter, discovery, check-in, exchange, and
  relay traffic
- [x] Tests block direct and DNS-resolved special addresses, revalidate
  redirects, strip cross-origin credentials, and prove exact host/CIDR
  exceptions do not permit unrelated private destinations
- [x] Environment HTTP proxies are disabled at the shared transport boundary
- [x] Trusted-proxy tests prevent untrusted forwarding headers from selecting
  the effective client identity
- [x] Relay rate accounting is per authenticated downstream key; Admin uses a
  separate global limiter with stable `429` and `Retry-After`
- [x] Configurable header/body/time limits retain `WriteTimeout=0` for SSE
- [x] `/healthz`, DB-backed `/readyz`, dedicated-token `/metrics`, structured
  redacted logs, and bounded metric labels are covered
- [x] Append-only Admin audit events cover authentication, mutation, and rate
  rejection; retention supports independent age and row ceilings
- [x] Online backup verifies integrity and generated path confinement; offline
  restore verifies installation, preserves rollback state, and removes stale
  WAL/SHM files
- [x] Backup/restore tests retain audit state, manual routes, and
  `manual_override` route members
- [x] Discovery and exchange tests retain manual and `manual_override` members
  during automatic reconciliation
- [x] Compose requires explicit Admin, master, and metrics secrets; the runtime
  image is non-root and probes `/readyz`
- [x] Linux CI runs format, vet, tests, build, `go test -race ./...`, Compose
  validation, and a deterministic container E2E
- [x] E2E creates resources only through HTTP APIs and verifies relay retry,
  SSE, discovery, check-in, versioned exchange, SSRF deny/allow behavior,
  manual-route protection, metrics auth, audit, online backup, and persistence
  after a gateway restart

## Verification Commands

```bash
test -z "$(gofmt -l .)"
go vet ./...
go test ./...
go build -trimpath ./cmd/server
docker compose config --quiet
docker compose -f docker-compose.yml -f docker-compose.e2e.yml \
  up --build --abort-on-container-exit --exit-code-from e2e e2e
```

Local Windows results are not authoritative for race behavior. The GitHub
Actions Linux job is the acceptance source for `go test -race ./...` and the
container execution. The same E2E mock, runner, server, and restart verification
also pass as separate local processes when Docker Hub is unavailable.
