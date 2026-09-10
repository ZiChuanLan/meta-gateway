# Changelog

All notable changes to Meta Gateway are documented here. Versions follow
[SemVer](https://semver.org/); each entry lands together with its git tag and
Docker image (`zichuanlan/meta-gateway:<version>`).

## [v2.5.0] — 2026-09-10

### Added

- Upstream model change maintenance: the Models page surfaces a change
  summary (added / possibly removed / impacted routes) and an expandable,
  filterable history (channel, change type, handling status, model name).
  Snapshots are compared per channel; the first successful sync establishes
  a baseline, a failed sync never counts as removal, and a model missing
  from one channel is reported as possibly removed — not as retired
  everywhere.
- The sync no longer silently deletes automatic members or routes when a
  model disappears: bindings stay in place (IDs, overrides, health state
  intact) so an operator can inspect and remap them.
- Replacement workflow: select changes → pick a target model from the
  current inventory (same-sync additions are shown as candidates, never
  auto-guessed as "newer versions") → choose affected members → preview on
  the server → confirm. Supports same-channel bulk replacement and an
  explicit cross-channel choice; only the upstream mapping changes, public
  model names, route names and other settings are preserved.
- Preview tokens are state fingerprints: applying with a stale selection
  (snapshot refresh, changed mapping, moved credential) is rejected with
  409, so a preview you looked at applies exactly what it showed. Bulk
  application rolls back atomically on failure.
- Ignoring a change only dismisses the reminder; reappearing models are
  tracked again, and outdated pending entries are resolved.

## [v2.4.0] — 2026-09-10

### Added

- Responses API translation: a client speaking OpenAI `/v1/responses` is now
  served by ANY channel — native passthrough when the upstream has the
  endpoint, an automatic one-shot chat/completions pivot on 404/405 for
  OpenAI-compatible channels without it, and the translation matrix routes
  Anthropic/Gemini channels through the chat pivot (`responses → anthropic / gemini` pairs).
  Streams reshape into the Responses SSE event contract (`response.created`,
  `output_text.delta`, `response.completed` …) and usage metering understands
  `response.usage`.
- Live request trace (admin API): `GET /admin/relay/live` streams in-memory
  request states over SSE (running → target channel/round → success/failed/
  canceled) and `POST /admin/relay/live/{request_id}/interrupt` cancels an
  in-flight upstream attempt.
- Console live-trace tab: the Logs page gains a "Live Trace" tab wired to
  `/admin/relay/live` — real-time rows (model, target channel, round, status,
  duration) with in-flight interrupt buttons, connection state, backoff
  reconnect, and a bounded window of settled requests.

## [v2.3.4] — 2026-09-10

### Fixed

- WebDAV scheduled sync no longer overwrites a key you saved or rotated in
  the console: incremental imports now treat a credential with a cleared
  `import_fingerprint` (the marker left by a manual secret edit) as locally
  owned and skip the backup value, while import-managed credentials keep
  their token-rotation semantics and empty creds are still backfilled

### Changed

- External check-in sends a browser `User-Agent` by default so
  Cloudflare-fronted sites stop rejecting the bare Go client UA with 403;
  per-credential custom headers still override it

## [v2.3.3] — 2026-09-06

### Fixed

- The logs page status column no longer shows a misaligned green dot: the
  colored status light and the status badge now share one vertically
  centered row, and the badge's redundant built-in dot is hidden
- The connection type picker no longer freezes when typing Chinese: the
  search box stayed mounted only while more than four options matched, so
  two letters unmounted the input mid-composition and stranded the IME.
  Whether the box appears is now decided once when the panel opens

### Maintenance

- CI is green again: restore gofmt struct-tag alignment (failing since
  v2.3.1) and remove a data race in the probe service tests that
  `go test -race` flagged

## [v2.3.2] — 2026-09-06

### Added

- Routing member rows: the channel name is clickable and jumps to that
  channel's model management page (`/models/channel/:id`), pre-filtered on
  the member's origin model via a `?model=` deep link (the route pattern
  when the member has no origin)

### Fixed

- The unify dialog's "strip owner prefix" badge no longer shows a hardcoded
  `deepseek-ai/` example: it names the prefix the group actually loses
  (`meta/`, `deepseek-ai/`, one badge per distinct prefix), and the rule
  checkbox label marks deepseek-ai/ as an example instead

## [v2.3.1] — 2026-09-06

### Added

- The runtime schedule fields (定时模型同步 / 探测计划) use the same
  preset picker as check-in instead of a raw cron input: off / hourly /
  every 3-12 hours / daily at a picked time / custom cron, with the empty
  (disabled) state spelled out instead of a blank text box

### Changed

- The channel edit drawer moved 用户 Access Token / 用户 Cookie out of the
  main form into the advanced section, and only shows them for site
  families that can actually use them (New-API-family account surfaces;
  cookie-only for generic external check-in). Plain OpenAI-compatible
  relays, official provider APIs, and unsupported families no longer show
  the fields at all — unless a credential is already stored, so it stays
  clearable

## [v2.3.0] — 2026-09-06

### Added

- A guided picker for the per-channel model sync mode (auto vs on-demand) in
  the channel edit drawer, the add-channel dialog, and a quick auto/manual
  switch on the channel models page: mode cards with trade-offs, a preview
  of what the next sync will do, live "N models · M adopted" counters, the
  inherit-system-default marker, and a collapsible "how do the modes
  differ?" explainer
- `POST /admin/connections` accepts an optional `model_sync_mode`
  (`auto`/`manual`; empty inherits the system default)
- The channel models page telemetry now pairs model total with adopted,
  enabled, and aliased counts, and manual-mode channels with pending
  candidates show a "N not adopted yet" hint instead of a bare 0
- The add-route dialog can auto-match channels serving the model: it lists
  every enabled channel whose models.csv or discovery snapshot matches the
  pattern (`GET /admin/discovery/model-channels` previews the match) with
  per-channel checkboxes, all selected by default, and only the checked
  ones are attached as members (`auto_match_channel_ids` on
  `POST /admin/routes`). A route that already carries the pattern is
  reused — the checked channels attach to it — instead of failing with
  "already exists"
- The unify assistant can now re-unify restored originals: a group whose
  canonical route exists but whose original name is exposed again (restored
  from history) stays listed with an "N exposed originals" badge, and
  applying hides the duplicates once more
- The channel overview and list report the discovered candidate count
  (`discovered_model_count`) next to the adopted model count, so
  manual-mode channels read as "N of M adopted" instead of a bare 0

### Fixed

- The channel edit drawer no longer forgets the model sync mode:
  `ListOverviews` (the endpoint the form seeds from) omitted the
  `model_sync_mode` column — along with `max_reasoning_effort`,
  `payload_rules`, `max_concurrent`, and `proxy_url` — so the empty
  read-back normalized to "manual" and a saved auto-sync channel reopened
  as on-demand; the columns are now selected, scanned, and normalized, with
  a regression test covering the projection
- The channel list model column no longer shows a stark bold 0 for channels
  without models: synced-but-nothing-adopted renders a muted 0 with a
  tooltip pointing at the models page or auto sync, never-synced renders a
  muted dash (mirroring the latency column), and adopted counts stay bold
- Unify apply no longer leaves a silent dead alias: a pre-existing disabled
  route with the canonical name is re-enabled, recorded as its own
  undoable op
- Unify undo refuses to delete a created route that still carries members
  from another batch or added by hand, instead of cascading them away
- Unify history counts only the still-hidden originals per batch and keeps
  restored entries visible (greyed out) so a restore leaves a trace
- Jumping from the models page to a channel's model settings drawer no
  longer needs closing it twice: the deep-link effect is one-shot per
  navigation (a close committed before the router's param transition used
  to re-fire it with the stale `?channel=` URL) and closing strips the
  resurrected param
- Info tips in checkbox labels stay inline after the label instead of
  wrapping onto their own line

## [v2.2.0] — 2026-08-31

### Added

- Upstream error details on failed log rows: the real upstream error body or
  transport error string (UTF-8-safe, 600 bytes) is captured into
  `proxy_logs.error_detail` and rendered in the log expansion next to the
  routing decision panel, so a failure no longer needs guesswork to diagnose
- Consecutive transport failures (connection refused, TLS, timeouts) now
  count toward channel health: the first failure of a streak stays
  cooldown-free (pure jitter is still free) while a repeat inside the same
  streak earns the full cooldown, the channel consecutive-failure counter
  (auto-disable) accumulates, and the next success clears the streak

### Fixed

- An outbound header/TLS timeout no longer kills the request as
  "cancelled": with the client still waiting it is reclassified as a
  retryable transport failure, so the failover walk reaches the other
  channels instead of ending after the first slow upstream
- The retried mark on log rows now sits on the row that TRIGGERED the retry
  (a later attempt of the same request exists) instead of on the retried
  attempt itself — a 200 row no longer reads as "retried"
- When every member of a route is cooling, the selector now tries the
  least-bad cooling member (highest priority, earliest expiry) instead of
  failing the request outright: a sole-member route no longer
  self-inflicts an outage for the whole cooldown window, and a successful
  fallback attempt doubles as the natural recovery path. Disabled,
  absent-credential, and already-attempted members stay out of the
  fallback; a fully disabled fleet still fails fast
- Fault-protection settings hint now describes the transport-failure
  streak semantics instead of the old "jitter is never penalized" wording

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
