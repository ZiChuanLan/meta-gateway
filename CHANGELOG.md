# Changelog

All notable changes to Meta Gateway are documented here. Versions follow
[SemVer](https://semver.org/); each entry lands together with its git tag and
Docker image (`zichuanlan/meta-gateway:<version>`).

## [v2.6.0] — 2026-09-12

### Added

- The 一键更新 button now works out of the box — no socket mount needed in
  the gateway container. Compose ships an idle watchtower executor on the
  project network (ports never published; it holds the Docker socket so the
  gateway container does not have to): it runs NO periodic updates and only
  wakes when the console button triggers it — pull, recreate, verify, done.
  Removing the service (`docker compose stop watchtower`) returns to the
  command-line update path. The direct socket handoff remains as the
  alternative mode when the socket is mounted into the gateway itself.

## [v2.5.9] — 2026-09-12

### Changed

- Bulk selection on the 连接 and 模型 pages is now opt-in instead of a
  permanent checkbox column: right-click a row (or use the row menu) and pick
  **批量选择** — the checkbox column and the bulk action bar appear, and
  **完成** exits the mode and clears the selection.

### Changed (dashboard)

- The cockpit no longer shows two near-identical recent-request feeds: the
  最新活动 column merged into 最近代理日志, which now also carries per-request
  token usage; the 24 小时模型用量 ranking takes the full width.

## [v2.5.8] — 2026-09-12

### Added

- One-click container update. With the Docker socket mounted (opt-in in
  compose — the socket carries roughly host-root power, so it stays off by
  default), 检查更新 gains a 一键更新到 {version} button next to the found
  release: the gateway pulls the new image, hands off to a successor
  container that recreates the final one with the original name/ports/volume
  bindings, and restarts — a few seconds of downtime, automatic rollback to
  the current version if the handoff fails, data volumes untouched. The
  update endpoint is admin-token gated, validates the target against the
  cached latest release, lands in the audit log, and only ever touches its
  own container and image. Without the socket the panel keeps the
  copy-command fallback.

## [v2.5.7] — 2026-09-12

### Added

- Bulk selection on the two remaining list pages. 连接 (channels): checkbox
  column with select-all-page, then 同步模型 / 启用 / 停用 over the selection
  in one pass. 模型 (routing): checkbox column with 启用选中 / 停用选中 /
  删除选中 (delete confirms first; member bindings go with the route).
- The upstream-change reminder now recognises work already done: a pending
  新增 whose model is already wired into the channel's routing is badged
  已接入, excluded from the summary count, covered by the one-click 忽略无影响
  bulk action, and resolved automatically by the nightly sweep.

## [v2.5.6] — 2026-09-12

### Fixed

- Renaming a model — alias, 统一名称, or any member mapping — no longer breaks
  relaying with `502 proxy: upstream credential unavailable`. The multi-key
  feature filters the key pool by which key actually lists the requested
  model, but it filtered on the public request name while a key's recorded
  set contains upstream names: a mapped name matched nothing and starved the
  pool even though the channel worked fine for listing. Key-pool selection
  now resolves against the effective upstream name (the member/route
  mapping's real model), and when no key's set claims the name at all
  (custom names, fresh renames before the next sync) the pool fails open with
  the model-blind selection — a wrong-group key just draws a missable 404
  upstream and failover moves on, instead of misreporting a naming problem as
  an auth failure.

## [v2.5.5] — 2026-09-12

### Added

- Live trace（日志 → 实时）overhaul. Running rows now tick every second
  client-side and streams stay visible for their whole life: a streaming
  response keeps its running state (with first-byte latency and a ~1/s byte
  progress) until the client has received everything, instead of flipping to
  success at response headers. New requests appear the moment they enter the
  gateway (the registry publishes on admission, not after routing).
- The live table carries real context: 客户端 (authenticated downstream key
  name), protocol + stream marker per channel, a failover chain
  (A✗ → B✗ → C with per-round failure reasons), TTFT, transferred bytes and
  final token counts.
- Operator interrupts now explain themselves: when a request interrupted from
  the console is followed within 15 seconds by a new request for the same
  model from the same client key, the new row is badged **疑似重试**（likely
  client retry）and links the interrupted request — the gateway cannot stop
  clients from auto-retrying, but the view finally says so. The interrupt
  toast says the same. Plus 全部中止（interrupt all in-flight）and a pause
  toggle that freezes the view for inspection and folds buffered frames back
  in on resume.
- Channel-level **非流式请求超时（秒）**（non-stream timeout, advanced edit
  field, `non_stream_timeout_seconds`, 0 = global 5-minute default): slow
  deep-reasoning upstreams can raise their own budget. Streaming requests are
  exempt; the global 2-minute stream idle guard is unchanged.
- Manual-sync channels now treat the checklist as the routing state:
  **unchecking a model removes its binding outright** (and a route the removal
  empties), re-checking adopts it fresh — the list no longer accumulates
  parked rows. Alias mappings are deliberate configuration and still park,
  and auto-sync channels keep the park semantic (reconcile respects parked
  members there; a deleted one would be re-adopted on the next sync).
  清理已停用（N）remains for legacy parked bindings and auto channels: one
  click deletes every parked binding on the channel and removes routes the
  cleanup empties.
- Channel-level **流式策略**（stream policy, advanced edit field,
  `stream_policy`). 跟随客户端 by default; 强制流式 serves non-streaming
  clients from an upstream stream the gateway aggregates into one completion
  (delta content, tool-call arguments and usage merge by frame); 强制非流式
  serves streaming clients from a non-streaming upstream answer replayed as a
  single-chunk SSE stream. The override applies to OpenAI-shaped chat
  exchanges — native Anthropic passthrough and the Responses API are exempt —
  and the non-stream budget follows the upstream's actual mode: an aggregated
  stream is not capped at five minutes, a synthesized answer is.

### Fixed

- Saving the edit-connection dialog no longer stomps the model sync mode
  changed in the model-management drawer. The drawer persists the toggle with
  an immediate single-field PATCH, but the still-open dialog used to rewrite
  it with its stale seeded value on save; the field is now sent only when the
  operator actually moved the picker inside the dialog.
- A request whose handler exited through an unhandled path no longer lingers
  as a phantom running row in the live view; the registry settles it on
  release.

## [v2.5.4] — 2026-09-12

### Added

- Upstream model-change maintenance gains confidence signals and automation.
  Each pending removal now tracks how many consecutive complete syncs have
  lacked the model and is badged **已确认缺失**（Confirmed missing）once that
  reaches three, with the summary showing the confirmed count; snapshots taken
  while only some of a channel's API keys answered flag the row **部分 Key
  未响应**（possible false positive）and do not advance the counter; a model
  re-disappearing within 30 days increments a **反复上下线 ×N** churn counter
  instead of stacking history rows; and when the relay's upstream itself
  reported the channel × model as not found at request time (the existing
  model-block blacklist), the row shows **运行时观测到不可用** with the
  timestamp. Pending removals also display how many days they have gone
  unhandled.
- Handling is faster on both ends: pending additions offer a **去接入**
  deep link straight into the channel's model page with the model pre-filtered
  in search, and a **忽略无影响（N）** bulk action ignores every pending
  removal with no impacted route member in one click.
- Alert rules gain two gauges, `model_change_removed` and
  `model_change_affected_routes`, so upstream churn can notify through the
  existing webhook/bark/serverchan/telegram/smtp rules — recommended rule:
  `model_change_affected_routes > 0`.
- Two upkeep knobs (daily sweep, zero disables): `MODEL_CHANGE_RETENTION_DAYS`
  (default 90) prunes finished model-change entries;
  `MODEL_CHANGE_AUTO_IGNORE_DAYS` (default 0 = off) auto-ignores harmless
  pending removals — candidate-list churn with no route impact — after N days.
- The channel model manager's status filter（全部 / 已启用 / 已禁用）now carries
  a live count badge on every option（total / enabled / disabled）, and the
  control is styled as a toolbar-height segmented switch aligned with the
  search and custom-model inputs.

### Fixed

- The console update check no longer claims "已是最新版本" (up to date) on dev
  builds. The comparison only parses dotted-numeric versions, so a binary
  built without the version ldflags could never see an update even when a
  newer release existed on GitHub; the panel now reports the build as a dev
  build and shows the latest release it found instead.

### Changed

- `docker compose build` forwards the VERSION and COMMIT build args
  (`VERSION=v2.5.4 docker compose build`), so locally built images can carry
  the real release tag in the console title and 当前版本 row instead of the
  buildinfo default "dev".

## [v2.5.3] — 2026-09-11

### Added

- The channel model manager (edit connection → 管理, or the `/models/channel/:id`
  page) can now narrow the candidate list by adoption state: **全部 / 已启用 /
  已禁用**（All / Enabled / Disabled）. The filter composes with search and bulk
  mode, updates the vendor-group counts, and shows an empty-state line when
  nothing matches. Previously the panel only offered search and bulk select, so
  parked models could not be isolated from adopted ones.

## [v2.5.2] — 2026-09-11

### Fixed

- Opening the console's live view could freeze every model at once. The live
  tab (Logs → live, SSE `/admin/relay/live`) subscribed through
  `livetrace.Registry.Subscribe`, which replayed the retained snapshot onto a
  subscriber channel sized for the live queue (32) while retention keeps up to
  50 finished requests. Past 32 served requests the replay blocked on the 33rd
  send **with the registry mutex held**; because every relay request calls
  `Attempt`/`Begin`/`Finish` — all of which take that mutex — the first
  live-view connection stalled all traffic. Requests were still accepted and
  model lists still loaded, but no model could answer, streaming or not, and
  the process never recovered on its own: only a restart cleared it. The
  subscriber queue is now sized for the snapshot plus the same live headroom
  the publisher tolerates, so replay can never block.
- The SSE handler treats a closed subscriber channel as terminal. After an
  overflow drop it read from the closed channel forever, replaying zero-value
  frames to the browser in a tight loop instead of returning so the client
  could reconnect and re-receive the snapshot.

### Changed

- Container images build the web and Go stages on the build platform and
  cross-compile per target architecture instead of running the whole toolchain
  under QEMU emulation. Multi-architecture releases no longer inherit QEMU's
  flakiness — `go mod download` died under emulation on 2026-09-10, which left
  v2.5.1 with a tag but no published image and no GitHub release — and release
  builds are substantially faster.
- GitHub releases are published with that version's CHANGELOG section as the
  release body plus the compare link, instead of the generated
  "Full Changelog" line on its own.

## [v2.5.1] — 2026-09-10

### Added

- Model discovery merges every enabled API key's list instead of stopping
  at the first usable key: a New API host with one key per group now syncs
  all groups' models (sorted, de-duplicated) instead of only whichever key
  answered first. Keys that fail keep their previously recorded set, so a
  transiently broken key never blanks its models.
- Per-key visibility is snapshotted (`credential_models`) on every
  successful refresh, and the relay's key pool filters each key by its
  discovered set when no manual `models_csv` allowlist exists: a request
  for a codex-group model only uses keys that actually list that model,
  shared models still rotate across the pool; manual allowlists keep
  precedence over learned sets.
- Upstream key creation is offered again even when the site already has
  keys (one key per group is the common setup), and the key drawer shows
  how many models each key synced.

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
