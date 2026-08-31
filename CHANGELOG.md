# Changelog

All notable changes to Meta Gateway are documented here. Versions follow
[SemVer](https://semver.org/); each entry lands together with its git tag and
Docker image (`zichuanlan/meta-gateway:<version>`).

## [v2.1.2] — 2026-08-31

### Fixed

- Proxy log audit: every failover attempt row now shows the routing
  decision behind THAT attempt — snapshots carry an attempt number, the
  panel names the channel actually picked (`selected_channel_id`,
  highlighted) instead of repeating the last attempt's decision and
  the highest-priority candidate on every row
- Silent upstream failures no longer reach clients as empty replies:
  a 2xx chat completion with no choices / an empty message / a 2xx
  error object, and 200 streams that end or stall after only
  role/usage frames, are now retryable failures that fail over.
  The empty-reply failure is variant-scoped, so the channel's other
  names stay in the fallback walk; `content_filter` and tool-call
  responses still pass through untouched
- Error labels no longer disguise client cancellations as network
  errors: "cancelled (client gone/timeout, no retry)" and
  "empty reply" are their own classes now
- Routing decision panel renders cooldown reasons in amber and marks
  the picked channel

## [v2.1.1] — 2026-08-31

### Admin console

- Model list gains a status filter (enabled / disabled / all, default
  enabled) next to the channel filter, so shadow models left behind
  by name unification stay out of sight until wanted
- Toolbar keeps search + family/channel/status filters on one row at
  desktop widths (selects size to content, search absorbs the rest)

## [v2.1.0] — 2026-08-31

### Admin console

- Settings page reorganized into semantic groups (routing / health /
  governance / ops / maintenance tools) with headers matching the
  section nav; cards flow into balanced masonry columns
- Field relocations: model sync (discovery cron + default sync mode)
  now sits in the health group next to probing; the global outbound
  proxy moved to the renamed "Service & network" card, which also
  shows the build version
- Update check: the toggle lives in "Service & network" with a
  check-now button (`POST /admin/update-check/refresh`) and result
  readout

### Fixed

- Data race in the probe test fake under concurrent workers (caught by
  the CI race detector)

## [v2.0.2] — 2026-08-31

First tagged release.

### Relay core

- OpenAI-compatible relay (`/v1/*`) across multiple upstream channels with
  model routing, retry rounds, and cross-channel failover
- Same-key re-sends, key-pool rotation, stable-first grayscale pools,
  sticky sessions, latency/error-aware routing, and an in-flight
  concurrency guard
- Fault protection: consecutive-failure cooldown, channel auto-disable,
  and passive recovery probes

### Models

- Scheduled model discovery with per-channel auto/manual adoption modes
- Unify assistant for managing adopted model names across groups
- Scheduled model probing with optional auto-disable on repeated failures

### Operations

- Alert matrix (webhook / Bark / ServerChan / Telegram / SMTP) with
  proactive health sweeps and daily digests
- Scheduled check-ins, balance exchange, audit logs with retention,
  database maintenance (orphan GC + VACUUM), and TOTP/redemption tooling
- Prometheus metrics, health/ready endpoints, and structured request logs

### Admin console

- React console: overview telemetry, command palette, zh-CN/English UI,
  route animations, and dark mode
- Custom login/console backdrop with opt-in localStorage persistence
- Sidecar plugin host with an in-console market

### Security

- Encrypted upstream keys (MASTER_KEY), admin bearer auth, rate limits on
  relay and admin surfaces, and outbound SSRF guardrails
  (OUTBOUND_ALLOW_CIDRS)

### Backup & sync

- Online backups with retention, plus native WebDAV two-way sync and
  AAH 4.0 import — independent connections, schedules, and results per
  direction

### Deployment

- Multi-stage Dockerfile (non-root, amd64/arm64), docker compose files,
  CI covering lint/tests/e2e, and tagged releases with version-injected
  builds plus an opt-out update check
