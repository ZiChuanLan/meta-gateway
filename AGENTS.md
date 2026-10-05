# AGENTS.md — Meta Gateway 仓库协作指南

本文件供 AI 编码代理（Codex / Claude / Cursor / WorkBuddy 等）阅读。目标是让你在**不读完全部源码**的情况下，也能安全、正确地改动这个仓库。

> 项目一句话：**Meta Gateway 是一个自托管的 AI 网关 + 管理控制台**。对下游暴露 OpenAI / Anthropic 兼容协议，对上游聚合多个渠道（站点）的账号与模型，做路由、故障转移、用量计费与审计。架构上坚持**透明 pass-through**：不改写请求语义、不注入提示词、不伪造响应。

---

## 1. 命令速查

### 后端（Go 1.26+）

```bash
go build ./...                 # 全量编译
go vet ./...                   # 静态检查
go test ./internal/...         # 后端测试（默认短模式）
go test ./internal/proxy/ -run TestXxx -v   # 单测定位
gofmt -l .                     # 格式化检查（CI 会卡）
go build -o bin/meta-gateway ./cmd/server   # 构建二进制
```

本地开发启动（最小必需环境变量）：

```bash
ADMIN_TOKEN=test MASTER_KEY=test-key-32-chars-long!!!!!!! METRICS_TOKEN=test \
  ./bin/meta-gateway
```

### 前端（Node 24+，位于 `web/`）

```bash
cd web
npm ci
npm run typecheck      # tsc -b --pretty false
npm test               # vitest（CI 下等价于 vitest run）
npm run build          # tsc -b && vite build
```

**`npm run build` 的产物写入 `internal/webui/dist`，由 `go:embed` 编译进二进制。**
改了 `web/src` 却没重建 dist，Go 侧跑的仍是旧 UI —— 这是本项目最高频的"改了没生效"原因。

**当前为统一控制台产物**：成员与管理员均从 `/console` 登录，`/app` 为兼容跳转，
`npm run build` 只构建 `internal/webui/dist`。成员复用共享页面与主题，由服务端账号角色隔离权限，
成员数据源只调用 `/me/*`，不得将管理接口权限下放给成员。`ADMIN_USERNAME` 默认 `admin`，
部署管理员密码沿用 `ADMIN_TOKEN`；网页保存的管理员用户名优先于 `ADMIN_USERNAME`，保存在 `operator_preferences`。未配置时环境变量仍为初始默认值；现有团队账号重名时优先，失败不得回退到部署管理员。网页改名需原管理口令/TOTP，禁止与团队账号冲突。

### 完整交付前的质量门（全绿才算完成，少跑一项就可能被 CI 挡下）

```bash
cd web && npm run lint && npx tsc -b && npx vitest run && npm run build && cd ..
gofmt -l . && go vet ./... && go build ./... && go test ./...
go run ./tools/docsgen && git diff --exit-code docs/reference   # 文档与代码一致
```

> **`npm run lint` 别省。** CI 的 Verify 步骤是 `npm run lint && npm run typecheck && npm test -- --run
> && npm run build`，一条 eslint **error**（例如测试文件里没被用到的 `within` 导入）就能让 CI 全红；
> 而 `release.yml` 的 `wait-for-ci` 会因此**拒绝发布**（tag 推上去了，镜像不会发）。2026-09-24 的
> v3.5.0 就是在推送前补跑 lint 时才拦下这条 —— 更早的清单里没有它，而 tsc / vitest 都不会报未使用的导入。

> Windows / 沙箱环境注意：`vite build` 默认 `emptyOutDir: true`，会先删掉旧的 `dist/`（含数十个
> 带 hash 的 chunk）。在带批量删除护栏的沙箱里，这一步有两种表现，**都是同一个根因、都要提权重跑**：
>
> 1. **报错中断**：`error during build:` 后面**没有任何错误详情**，`dist/assets` 被清空但新产物没写出来。
> 2. **静默挂死（更隐蔽，2026-09-24 实测）**：进程**永不退出、也不报任何错**，日志停在
>    `✓ 1834 modules transformed.` 之后再无下文，`rendering chunks` 永远不出现。护栏拦掉删除、
>    进程就在那里等。此时 `dist/` 里**仍是上一次构建的旧 chunk**（拿 `stat().st_mtime` 一看还是几十分钟前），
>    容易误判成"已经构建好了"。
>
> **判断口径**：健康构建只要 **~4s**（transform 阶段也就几秒）。如果 `transformed` 之后超过十几秒没动静，
> 就是被挂住了 —— 直接 kill 掉，改用 `dangerouslyDisableSandbox: true` 重跑。提权后实测 **4.03s** 完成。
> 别去动 `vite.config`（关 `emptyOutDir` 会留下陈旧 chunk，反而制造新的"改了没生效"）。
>
> 连带坑：挂死期间 `internal/webui/embed.go` 会锁住 `dist/assets`，此时跑 **任何** 依赖
> `internal/webui` 的 Go 测试（如 `go test ./internal/httpapi/`）都会报
> `pattern dist: open ...\dist\assets: The process cannot access the file because it is being used by
> another process.` —— 这是文件锁不是代码错，等构建结束后重跑即可。

### 测试开库：用 `store.OpenTest`，不要重放迁移（2026-10-05 重写）

**测试里开库一律用 `store.OpenTest(t.TempDir())`。** 它拷贝一份「每进程只迁移一次」的模板库
（`internal/store/template.go`：`sync.Once` 建模板 → 每测试文件拷贝，拷贝时**跳过 `Migrate`**）。

**只有断言迁移行为的测试才用 `store.Open`**：重开一个已经写入的目录、从旧 schema 升级、
断言某个迁移会重放。目前必须保留 `Open` 的位置：`internal/store/store_test.go` 的
`TestP0P2DatabaseUpgradesWithoutDataLoss` / `TestP0P4DatabaseUpgradesToCheckin…`、
`portal_retirement_test.go` 的重开测试，以及 `internal/backup/{service_test.go,team_restore_test.go}`
（`Restore` 写完库后重开）。

> `OpenTest` 遇到 dataDir 里**已有** `meta-gateway.db` 会直接报错（**不覆盖**）。这条守卫是刻意的：
> 这个改动最容易犯的错就是把「重开」误转成 `OpenTest`，而拷贝模板盖掉 fixture 会让测试
> **静默变样**。有守卫就只会大声失败——实测已抓到两处。

为什么必须这样：`-race` 会给纯 Go 版 SQLite（`modernc.org/sqlite`）插桩，而每个开新库的测试都要
重放全部迁移 —— 实测 **0.14s → 3.4s（25×）**。全仓约 **300 个开库点**，这笔固定成本就是 race 步骤的
绝大部分耗时，也是它长期压在超时悬崖上的原因：

| 时间 | race 步骤墙钟 | 结果 |
| :--- | :--- | :--- |
| 2026-10-05 前（每测试重放迁移） | 基线 863–1,112s；V4 提交 **1,304s / 1,720s** | 基线勉强过，V4 越过 per-package 20m 而红 |
| 只把预算加到 30m（未改测试） | **1,959s** | 仍然红 —— **加预算治不了成本** |
| 切模板库后（本地 Windows 实测） | **全量 472s**；httpapi 单包 **1,201.559s → 413.2s**，store 335.2s | 全绿 |

那次红直接挡住了发布：`release.yml` 的 `wait-for-ci` 拒绝为红的 CI 构建镜像（v4.0.0-beta.1 两次
都没发出镜像）。**race 步骤失败时先分清 `DATA RACE` 还是 `test timed out`**：后者是预算/成本问题，
不是代码问题。本地实测 httpapi 在 `-race` 下 **1201.559s**，以 1.6 秒之差撞上旧的 1,200s 上限——
耗时而非 race 报告，就是超时的签名。

**当前预算**：`-timeout 20m`（per-package）、job `timeout-minutes: 45`、`wait-for-ci` 轮询 80×30s。
注意**超时不消耗墙钟**——墙钟由实际测试时间决定，预算只是安全网，所以宁松勿紧；但也不要松到
掩盖真死锁。要砍的是成本，不是把上限抬高。

**Windows 本地跑 `-race` 要先给 C 编译器**：本机默认 `CGO_ENABLED=0`，且 bash 工具的环境 PATH 里
没有 WinGet 装的 MinGW-W64，不设就直接 `cgo: C compiler "gcc" not found`（几秒就退，看着像代码错）。

> 本文件早先给的写法是**坏的**：`$env:CC="$mingw\gcc.exe"` 里的通配符**不会展开**，`$env:CC` 会变成
> 一个字面含 `*WinLibs*` 的路径，报 `file does not exist`。必须先把真实路径解析出来：

```powershell
$gcc = (Get-ChildItem "$env:LOCALAPPDATA\Microsoft\WinGet\Packages\*WinLibs*\mingw64\bin\gcc.exe" |
        Select-Object -First 1).FullName
$env:CC = $gcc
$env:PATH = "$(Split-Path $gcc);$env:PATH"
$env:CGO_ENABLED = '1'
go test -race -timeout 30m ./...
```


后台调度器（alert / balance / health sweep、alert rules、daily summary、model catalog、DB GC、
probe、**site probe**、update check、discovery recovery loop）都由 `NewWithDependencies` 启动，各自往
`RegisterStopper` 注册停止回调。**测试里建 router 必须用 `NewTestRouter(t, cfg, db, enc)`**
（`httpapi_test` 里写 `httpapi.NewTestRouter`），它在该测试结束时 `StopBackground`；直接用 `New`
会让调度器活到进程退出（实测 44 个泄漏 router ≈ 458 个常驻 goroutine）。
`internal/httpapi/background_test.go` 里有一个不变量测试 + TestMain 兜底守着这条约定。

---

## 2. 目录地图

```
cmd/server          # 进程入口：装配配置、store、各 service、HTTP 路由
cmd/e2e-runner      # 端到端跑测（配合 cmd/e2e-mock 上游桩）
internal/
  domain/           # 纯数据结构与归一化（models.go），无 IO
  store/            # SQLite 持久化；NNN_*.sql 为按序迁移脚本
  httpapi/          # 管理面 REST 路由与鉴权（/admin/*、/v1/* 入口）
  proxy/            # 转发核心：鉴权、路由选型、重试、计费、健康度
  routing/          # 路由 ↔ 成员关系、会话粘性
  adapters/         # 上游协议适配器（openai / anthropic / gemini / responses …）
  outbound/         # 出网策略（代理、超时、TLS）
  relay/            # 裸转发通道（尽量零加工）
  discovery/        # 上游模型发现与采纳（model_sync_mode: auto|manual）
  probe/            # 渠道健康探测与候选评估（发真实 chat completion，消耗 token）
  siteprobe/        # 站点自公开探针数据源（Uptime Kuma 状态页 / New-API 价格表，零 token；
                    # 判定后走 route_members 的 auto_disable 动作，设计见 docs/site-probe-source.md）
  healthsweep/      # 健康度清扫
  usage/            # 用量与账单聚合
  financesweep/     # 余额/成本扫描
  ratelimit/        # 限流（注意 TRUSTED_PROXY_CIDRS 为空时 CDN IP 会糊在一起）
  account/          # 渠道账号同步桥
  checkin/          # 站点签到自动化
  auth/ totp/ crypto/   # 管理面鉴权、二次验证、MASTER_KEY 加解密
  backup/ exchange/ webdavsync/   # 备份与站点间迁移（AAH 兼容封套）
  alerts/ webhook/  # 告警与外发
  livetrace/        # 实时请求追踪
  observability/    # 指标与日志
  plugins/          # 插件市场与进程托管
  maintenance/      # GC 与数据清扫
  selfupdate/ updatecheck/   # 容器自更新
  config/ runtimeconfig/     # 配置读取与运行时设置
  webui/            # go:embed 前端产物（dist），不要手改
  spa/              # 控制台 SPA 文件服务、缓存与路径处理（独立测试）
web/                # 前端源码（React 19 + Vite + TS）
docs/               # 文档站源码（VitePress）+ 由 tools/docsgen 生成的参考层
tools/              # 仓库工具：docsgen（生成 docs/reference）、landing（启动页）、market-registry
```

前端结构要点：
- `web/src/features/**` 按业务域分片；`web/src/lib/**` 通用工具。
- i18n 文案集中在 `zh.ts` / `en.ts`，有 `parity.test.ts` **强制双语键一一对应**，缺一个即红。

---

## 3. 铁律（改动前必读）

以下每一条都对应过真实事故，违反会静默出 bug（测试不一定能抓到）。

### 3.1 管理后台表单回填源

编辑类抽屉（如 `EditChannelDialog`）的回填数据来自 `GET /admin/channels/overview`，即
`store.ChannelStore.ListOverviews`。**凡该 SELECT 投影缺失的列，保存时都会以零值回写。**

曾导致 `model_sync_mode` / `max_reasoning_effort` / `payload_rules` / `max_concurrent` /
`proxy_url` 五列"设置了、重开就没了"。

> **给 channel 表加新列时，务必同时补 `ListOverviews` 的 SELECT + Scan**，否则前端的勾选、开关会静默失效。

**同一条约束还适用于转发热路径的第二处投影。** `internal/store/route.go` 里有**两个**手写 channel 投影，两处都必须补列：

| 函数 | 用途 | 漏列的后果 |
| --- | --- | --- |
| `ChannelStore.ListOverviews` | 控制台渠道列表/编辑抽屉 | 表单回填成零值，保存即抹掉配置 |
| `RouteMemberStore.RoutingCandidates` | **实际转发选中的渠道** | 运行时读到零值，该列的功能完全不生效 |
| `RouteMemberStore.listCandidatesByRoute` | 路由详情页成员列表 | 与上者不一致，UI 与行为对不上 |

三处 SELECT 的列顺序必须与各自的 `Scan` 一一对应。2026-09-21 的「自定义端点映射」就踩过这个坑：migration 与 ListOverviews 都补了，但 `RoutingCandidates` 漏了，表现是「配了映射、请求仍走旧路径」——单测（`TestUpstreamMapTranslatesTypeSafeShapedUpstream`）才拦住。**加完列后跑一次 `go test ./internal/proxy/ -run TestUpstreamMap`。**

**补列还不够：列的顺序也必须与 `Scan` 完全一致。** 同一个功能第二次踩坑是另一种形态——两处投影的列都补了，但我把四个 `upstream_*` 列放在了 `stable_first` **前面**，而 `Scan` 里它们仍在后面。结果是列整体错位一格：`upstream_path_override` 读到了 `created_at`（时间戳字符串），`created_at` 变成零值。

这类错位**只在查询真的带出数据时才暴露**（空列表、零值列都看不出来），而且 `SELECT`/`Scan` 都是手写列表，编译器与 `go vet` 都不会报。所以：

- 改动任一投影后，必须肉眼把该函数的 `SELECT` 列表与 `Scan` 参数列表逐项对齐看一遍；
- `TestRouteMemberProjectionsCarryChannelColumns`（`internal/store/channel_projection_test.go`）把三处投影的全部列做往返断言，并显式检查「文本列里没有时间戳形状的值」——这正是错位的指纹。

> 手写 `SELECT` + 手写 `Scan` 是“列数对得上、语义错半格”的经典温床；新增/移动列后务必跑该测试。

新增表单字段的完整链路：
`store 迁移列 → ListOverviews 投影 + RoutingCandidates 投影 → domain 归一化 → httpapi 校验 → 前端类型/i18n/CSS → 测试`。

### 3.2 计费单价：两层「整层替换」优先级链

实现在 `internal/proxy/proxy_health.go` 的 `billingCost` 与 `priceLayer`（key 级单价已于 2026-09-13 移除）：

1. `route_members` 的单价列 + `price_tiers` / `price_schedule`（最具体：该路由 × 该渠道）
2. `model_metadata` 的单价列 + `price_tiers` / `price_schedule`（按模型名）

**命中判定看 `domain.PriceLayer.Priced()`，不是看扁平列是否非 0**：
`prompt > 0 || completion > 0 || per_request > 0 || 配了阶梯`。命中即停止下探；两层都不命中 → cost = 0（免费）。

> **坑一：层内不逐字段回退。** 只填了 completion 而 prompt 留 0 的那一层，prompt 会按 0 计（免费），
> 不会再去找下一层的 prompt 价。
>
> **坑二：只配了阶梯的层也是「已定价」。** 扁平列全 0 但有 `price_tiers` 时这一层算命中；
> 只检查扁平列的代码会把这笔请求当成免费（`price_tiers_test.go` 钉死这条）。

**层内取价**：配了阶梯时**阶梯压过扁平列**（`EffectivePrices`）。档位由
`inputTokens = promptTokens + cacheCreationTokens` 选出（缓存**读**不计入，它单独按 cache 价计），
与计费口径同一个量，所以档位与账单不可能对不上。请求超过所有显式上限时落到最后一档（最贵那档），不是免费。

**成本公式**（`billingCost`）：

```
成本 = ( input/1000 × pricePrompt
       + completion/1000 × priceCompletion
       + cacheRead/1000 × priceCache
       + pricePerRequest ) × ratio × multiplier
```

- `priceCache <= 0` 时回退到该层的 `pricePrompt`。
- `ratio` = `model_ratios`（控制台「计费倍率」，默认 1.0，可 0~1000），**不参与层选择**。
- `multiplier` = 时段倍率（`PriceWindow`），无匹配时段时为 1。**第一个匹配的时段生效**，不与后续时段相乘。
- 阶梯与时段都存在层自己的 JSON 列里（迁移 120），畸形值会被记录并丢弃后回退到扁平价，绝不让转发卡住。

**配额与计价正交，且有两套单位并行**：`quota_total_tokens` / `quota_used_tokens` 按 **token 数**扣；
`quota_total_cost` / `quota_used_cost` 按**金额**扣（迁移 113）。两者**同时生效，谁先耗尽谁拒绝请求**，
不是二选一。三处都有：`downstream_keys`、`key_groups`、`team_users`。

**成本展示一律读真实账单**：
- Keys 页 cost 列 = `UsageStore.CostByKey()`（按 key 求和）
- Logs 页 cost 列 = 按 request_id join `usage_records.cost`（`CostByRequestIDs`；
  `domain.ProxyLog.Cost` 仅 admin 列表填充）

`downstream_keys` 的三个 price 列仍在 DB 里但代码已停止读写（可逆，未删列）。

### 3.3 图像接口：不做重试的透明转发

`/v1/images/generations`（JSON，20MB 上限）、`/v1/images/edits`、`/v1/images/variations`
（multipart，30MB 上限）走**字节级原样转发**，并且**不做故障转移重试** —— 它们是非幂等写操作。
这条由 `internal/proxy/retry_safety_test.go` 钉死，改重试逻辑前先看它。

用量从响应体的 `usage` 字段解析（不是从请求）。

工作台通过 `imgproto.PlanForRequest` 按参考图数量解析自动模式：无图生成，有图编辑。
GPT-Image 生成使用 JSON，编辑上传使用 multipart；grok2api 编辑使用 JSON。
路由显式开启 `image_edit_shim` 后，带图聊天可转为编辑请求；继续复用正常转发的分组、计费与取消处理。
**图片数量和 Base64 字节数都不是 token，不得用来伪造缺失用量。**
编辑协议、限制和示例见 `docs/image-editing.md`；新增协议行为请覆盖真实 HTTP 状态、SSE 头、文件与用量。

### 3.4 导出/导入的 strict decode 陷阱

`exchange` 的 `canonicalEnvelope` 使用 `DisallowUnknownFields()`，而 `Envelope` 会序列化
`skipped`（omitempty）。**只要导出的 items 里有被跳过的渠道，自己的导出文件就再也导不回来。**

> **给 import 或 export 任一结构加可序列化字段时，必须同步加进对面那个 strict struct。**

解析必须**逐条容错**：`ParseWithReport` 返回 `ParseReport{Items, Skipped}`，子解析器返回跳过原因
而不是中断；全部跳过时返回 `ErrorNoEntries`（区别于 `ErrorUnsupported`）。

导入类错误 category 自成一套，**不要复用 `validation_error` / `unsupported_format`** —— 前端会把
后者渲染成"检查 Base URL 和凭据"的连接话术。专用 code：
`exchange_document_invalid|unsupported|empty|conflict`、`backup_unlock_required`、`decrypt_failed`。

加密封套（AAH 与 MG 共用）：PBKDF2-SHA256 / 250000 轮 + AES-256-GCM，base64 的 salt/iv/ct，
type 为 `all-api-hub-webdav-backup-encrypted`。

三条导入/导出通道要一起想：① `POST /admin/exchange/import`（明文，strict decode）
② `/admin/exchange/import-encrypted`（AAH 封套，`webdavsync.DecryptEnvelope`）
③ WebDAV 拉取（`webdavsync.runDownload` 内部同样调 `exchange.Service.Import`）。

前端备份预览判定集中在 `web/src/lib/aahBackup.ts`（Exchange 页与 SetupWizard 共用），不要写内联版；
AAH 版本号是字符串且当前为 `"4.0"`，段可能嵌在 `data` 下，别写死 `"2.0"` 或只查顶层。

### 3.5 统一名称（unify）是批次 + op 回放状态机

`model_unify_batches` / `model_unify_ops` 记录 `route_created` / `member_created` /
`route_deleted` / `route_enabled`（`route_archived` 是删除语义之前的旧 op，仍可回放），
Undo 逆序回放并跳过已 undone 的 op。四条不变量：

1. 被合并的原名路由**直接删除**（连同它的成员），不再 disable：留一个 `enabled=0` 的旧名仍会
   在模型目录里显示成一行死名。删除前必须 `snapshotRouteRows` 快照整行（含全部成员列），
   重建 = `restoreRouteRows` 按原 id 原样插回，单条重建 = 把 op 置为 undone；
2. 撤销 `route_created` 前必须确认路由上没有手工成员或其他活跃批次的成员（`ensureRouteUndoable`），否则会级联删光；
3. 只在「该路由的每个成员所属渠道都被本组绑定到统一名」时才删（`deleteSupersededRoutes`），通配符与部分覆盖一律跳过；
4. 预览组只有在"全 mapped 且无 exposed originals（已启用的变体名路由）"时才可省略，否则重建过的原名永远无法再删除。

> 快照用 `PRAGMA table_info` 动态取列，所以以后给 `routes`/`route_members` 加列不需要改快照代码；
> 恢复时显式写回原 id（SQLite AUTOINCREMENT 不复用被删的 id），`routes.single_member_id`
> 与成员自带的 `route_id` 因此无需重映射。同名路由已存在时拒绝重建（`cannot rebuild`）。

### 3.6 零信任日志

`proxy_logs` 只存元数据，不落任何凭据明文。新增字段前问一句：这个字段会不会带上
`Authorization` / `Cookie` / `token` / `secret`？会的话就不能进日志、进审计、进错误响应体。

### 3.7 模型同步模式

`domain.ModelSyncMode`：`auto`（discovery 自动把探测到的模型采纳为路由 + 成员）vs
`manual`（只刷新候选快照，逐个人工采纳）。`NormalizeModelSyncMode` 把空值/未知值归一为
**manual**（安全默认）。`runtime_settings.default_model_sync_mode` 是新 channel 的继承来源。

### 3.7.1 建路由后的两件自动化（2026-09-23）

`POST /admin/routes` 成功之后，网关替操作员把「这个模型名」能回答的问题一并答掉：**怎么调它**（能力
注册表）与**哪些渠道到得了它**（路由成员）。两条路径各有一条不能碰的底线：

- **能力补齐**（`AdminHandler.bootstrapNewModel`）：内置分类器 `ModelCapability.AutoTag` **同步**跑
  （本地、幂等、跳过 manual/catalog 已拥有的行）；外部目录 `modelcatalog.Runner.Sync` **后台**只同步
  这一个模型（容量 32 的 `modelBootstraps` 通道 + `runModelBootstraps` 常驻消费）。
  **两半都不许让保存变慢或变失败**：队列满、没配目录 → 静默丢弃并 defer 到下一次计划扫描；worker 里
  一次失败的 sync 只记日志。写这条路径时不要把任何网络调用挪进 HTTP handler。
  ⚠️ **通配符（含 `*` / `?`）必须跳过**：`gpt-*` 是匹配器，不是可调用模型名，没有目录会收录它。
  worker 由 `router.go` 在**有 catalog 服务时无条件启动**（即使周期扫描被关掉），并注册 stopper。
- **一键挂载**（`POST /admin/routes/{id}/auto-match` → `store.AttachChannelsToRoute(id, pattern, ids, group)`）：
  把「确实提供该模型的启用渠道」挂进**指定分组**。三层语义别搞混：
  ① handler：`channel_ids` 为空 = **全部当前匹配**；② store：空列表 = **空操作**（这就是前者必须由
  handler 决定的原因，别顺手在 store 层加「空 = 全部」）；③ 前端：因此**全不选时不能发请求**（会挂上
  全部），确认按钮在 0 选中时必须禁用。
  目标分组沿用「添加通道」的语义（= 当前分组 tab），**不是永远 default**。交集保护与创建时同一套
  （`ChannelsWithModel`）：disabled / 未知 / 已不再提供该模型的 id 计入 `skipped`；**已在目标分组的
  渠道既不算 added 也不算 skipped**（纯空操作，连点两次不会重复挂载、也不会看起来像失败）；只在别的
  分组里的渠道仍会被挂进来（分组可叠加）。
- **匹配范围 `exact` / `related`**（2026-10-03 新增）：`ChannelsWithModel(pattern)` 保持原语义（普通名字
  精确匹配，`*` 由操作员自己写）；`ChannelsMatchingModel(pattern, ModelMatchRelated)` 额外接受「以 pattern
  开头的其他模型」（`pattern*`），并为命中的变体成员写 `mapping_json={"real":"<命中名>"}`——不改路由名、
  只改成员的转发名，否则挂上去的成员会把基础名发给根本不认它的上游（挂了个只会报错的成员）。
  三处参数：`POST /admin/routes/{id}/auto-match` 的 `match`、创建路由的 `auto_match_mode`、
  预览 `GET /admin/discovery/model-channels?match=related`。预览行会显示「命中 <模型名>」，
  所以宽松匹配的结果是可核对、可取消勾选的。

### 3.7.3 团队码与账户额度：哈希、占位列与双重限额

**码表就是 `team_invites`（一张表三种用途）**，`kind` = `invite|credit|recovery`：

- 短码 `XXXX-XXXX-XXXX-XXXX`（16 位， 80 bit）**哈希存的是去掉分组符、大写化后的原文**
  （`stripTeamCode`），所以去横线/小写/空格都能命中；旧的长 token 存原样哈希，继续兼容。
  **改码格式前先看 `teamCodeCandidates` 与 `stripTeamCode`**：只对“看起来像短码”（16 位且字符集属于
  `teamCodeAlphabet`）的值做宽松匹配，否则会把 base64url 的长 token 错误归一化。
- `credit` 码与策略无关，但 `policy_id` 是 NOT NULL + 外键，所以插入时填一个**占位列**（站点第一条策略，
  见 `createCodes` 注释）；不要因为“这列对 credit 无意义”就传 0。
- 同一账号重复兑同一个额度码由 `team_code_redemptions` 的 `(code_id,user_id)` 唯一索引拦；
  码的总次数由 `used_count < max_uses` 拦。两者都在同一个事务里，先过再入账。

**账户额度是第二重限额**（`team_users.quota_total_tokens/quota_used_tokens`，0 = 不限），新增或改动额度链路时两个点都要改：

1. 准入：`internal/httpapi/relay.go` 的 `ensureQuota`（额度随 `key.TeamAccess` 快照带出，不额外查库）；
2. 计费：`internal/store/usage.go` 的 `RecordUsage`（与 Key 配额**同一事务**累加）。

### 3.7.4 第三方登录：端点会分裂成两个视角

团队成员的 GitHub / Linux.do 登录实现在 `internal/httpapi/team_oauth.go`（流程）与 `team_oauth_admin.go`（配置）。改动前必读：

- **三个端点不是一个视角**：`authorize_url` 由**浏览器**访问，`token_url` / `userinfo_url` 由**网关**访问。
  验收时把 authorize 指向 `127.0.0.1:4501`、把 token/userinfo 指向 `host.docker.internal:4501`
  才能同时成立（这是实测踩过的坑：全都写 host.docker.internal → 浏览器报 502）。
- **忽略容器环境代理**：`oauthHTTPClient()` 显式 `transport.Proxy = nil`，只跟随运行设置的全局代理。
  容器里的 `HTTP_PROXY=127.0.0.1:7897` 指向宿主，用它去访问 GitHub 会 `connection refused`
  （日志里的 `proxyconnect tcp`）。这与 `internal/outbound` 的 SSRF 契约一致（已有测试
  `TestClientIgnoresEnvironmentProxy` 守着同一件事）。
- **state + PKCE 存签名 cookie**（`meta-team-oauth`，HMAC 用 `enc.KeyMaterial()`），不依赖内存也不依赖会话；
  回调要求 state 匹配。
- **`/auth/oauth/{provider}/start` 与 callback 用 `publicAuthNavigate`（只校验 Origin + 限流，不要求 CSRF header）**：
  它们是顶层导航/重定向入口，浏览器不会带自定义 header；而且没有“把已有账户绑到别人会话”的路径
  （自动注册只会创建/复用**自己**的账户）。不要为了“更安全”给它们加回 CSRF cookie 检查，
  那只会把登录页逼回 JS 中转。
- **限流**：两个端点共用登录限流（burst 5 / 15 rpm / IP），所以一个用例里连续 start+callback 不要超过 5 次，
  否则测试会撞 429 —— 拆成独立测试环境。
- **密钥约定**：`client_secret` 空 = 保留已存的（与其它凭据字段一致）；
  **`client_id` 空 = 移除该 provider（密钥一并清除）**，这是控制台唯一的删除路径。
- `team_identities` 靠唯一索引 `(provider, subject)` 防并发双建；自动建号时若撞唯一索引，
  回退去读已存在的那条（不要报错给用户）。

### 3.8 自定义端点映射（`upstream_*` 列）
`channels` 上的四列让一个渠道可以脱离 OpenAI 形态：

| 列 | 作用 |
| --- | --- |
| `upstream_path_override` | 换掉端点路径（`systemone` → `/v1/systemone`；`/api/v3/x` → 原样绝对路径） |
| `upstream_path_map` | JSON `{"openai 路径":"上游路径"}`，键可 `*` 结尾，值可用 `{path}`/`{model}` |
| `upstream_request_map` | 请求体字段搬运 |
| `upstream_response_map` | 响应体字段搬运 |

实现全在 `internal/proxy/upstream_map.go`（引擎）与 `upstream_map_validate.go`（保存期校验），路径语言与 `payload_rules` 共用 `proxy/jsonpath.go`。三条不变量：

1. **映射生效时 URL 拼接换用 `adapters.JoinRawPath`**，不再走 `JoinOpenAIPath` 的 `/v1` 规则。映射路径**原样拼在 Base URL 之后**，不自动补 `/v1`：需要 `/v1` 的供应商请在映射值里自己写（如 `"/v1/models"`）。

**`JoinOpenAIPath` 的唯一规则（v3.5 修正）**：路径段永远是 API 根，不是某个端点的一部分——base 无路径时补 `/v1`，base 带路径时原样拼在其后。曾经只有「以 `/v1` 结尾」才不补，导致智谱 `/api/paas/v4`、火山 `/api/v3`、千帆 `/v2` 被拼成不存在的 `/api/paas/v4/v1/chat/completions`——**类型下拉里每个 cn 厂商预设都是坏的**，操作员只能去高级里手填端点。加一个厂商 base URL / 端点前，先跑 `go test ./internal/adapters/ -run TestJoinOpenAIPath` 并对照官方文档。
2. **响应映射跑在适配器转换之后**（`UpstreamMap.ReshapeResponse`）：map 的路径描述的是**客户端看到的文档**，不是上游原始报文。这样别家后端的 `choices[0].message.content` 与 OpenAI 上游的语义一致。
3. **全程 fail-open**：映射为空/畸形/源字段不存在 → 原样转发并在日志留一行；`from`/`to` 复制保留 JSON 类型（数字仍是数字），`template` 产出的永远是字符串。

字段映射不是 JSONata 那种通用引擎（无 `$`/算术/正则），故意只保留五种写法（`from`/`to`、`move`、`template`、`value`、`keep`）并与 payload_rules 共用 JSON-path 实现。`[]` 形式的「整包体路径」曾设计过但未实现，校验会明确拒绝并给出替代写法（响应方向还想换整个 body 的话，就在适配器层做，不是在这一列）。

**`keep`（顶层 body 白名单）是第五种，也是唯一一种「删」的写法**：`{"keep":["model","state"]}` 会把不在名单里的顶层键全部删掉。它存在的唯一理由是有些上游把请求模型校验得很严——**多一个未知字段就 400**（TypeSafe System One 实测连 `temperature` 都拒绝），而 OpenAI 形态的请求体必然带 `messages`。只靠删除表达不了这件事，因为「要删的集合」取决于客户端，「要保留的集合」才是操作员知道的。**条目按数组顺序生效**，所以典型写法是先 `from` 读需要的值、再 `keep` 删（`{"from":"messages.0.content","to":"state"}` 必须在 `{"keep":[…]}` 之前，否则源已经没了）。只支持顶层键（带 `.`/`[` 会被校验拒绝）。

### 3.8.1 供应商 profile：映射是供应商的属性

**非 OpenAI 供应商的映射不再由控制台按钮填，而是随供应商走**（`internal/proxy/provider_profile.go`）。2026-09-23 之前它是一键预设按钮：操作员点一下，4 个 JSON 框被填上，然后这份协议契约就归他维护了；而且另外两条渠道来源（导入、API 创建）根本看不到那个按钮。现在：

- `ApplyProviderProfile(ch, providerType)` 在保存时按 `type_hint`（空则 `site.platform`）查 profile，**只填空位**；**已手写 request/response map 的渠道完全不动**（路径覆盖豁免这条闸——「粘完整端点 URL」的保存期拆分写的就是那个字段，属同一意图）。profile 自己先过一遍 `ValidateUpstreamMap`，所以不可能存进一份非法映射。
- 接线点两处：`httpapi.validateChannel`（create / clone / update 共用）与 `admin_connections.createConnection`。
- **新增一个非 OpenAI 供应商要动四处**：① Go `OpenAICompatibleBrands()` + `CanonicalType`（映射到 `openai-compatible`，让 `ListModels` / `ResolveForward` 命中）；② `providerProfiles` 注册 profile；③ 前端 `connectionTypes.ts` 的 `CONNECTION_TYPE_OPTIONS` + `PROVIDER_BASE_URLS`；④ 前端 `helpers.tsx` 的 `NO_USER_AUTH_TYPES`（否则抽屉里会多出无用的用户令牌字段）。
- `PROVIDER_BASE_URLS` 里**填文档里的端点而不是根地址**（Perplexity 与 TypeSafe 都这样）：保存时 `SplitEndpointBaseURL` 会拆成根 + 覆盖，这正是「模型清单与对话端点深度不同」也能同时可达的手法（TypeSafe：`GET /v1/models` 在裸主机上，`POST /v1/systemone` 在 `/v1` 下）。
- **模型清单解析必须容错**（`adapters.parseModelList`）：接受 `data`/`models` 两种容器与 `id`/`name` 两种字段名。只认 OpenAI 那一种形状会把一个完全配好的渠道判成 `invalid_payload`，而它在前端属于 **`upstream_shape` 类**（曾经归在 `config` 类，于是提示「检查 Base URL、连接类型和凭据状态」，把排查引向一个已经正确的 URL 与凭据）。**新增后端 category 时，回 `web/src/errorCatalog.ts` 确认它落在哪一类**——这张表是「通用兜底文案」与「具体原因」之间唯一的翻译层。
- **`custom` 是唯一「已知的未知」类型**，必须留在 `OpenAICompatibleBrands()` 与 `CanonicalType` 里。它表示「我自己接端点」（模型的 protocol family 未知，由字段映射来掰），所以默认落到 OpenAI 形态的 passthrough 最诚实。漏掉它 = 选完 Custom 就解析不到适配器、`discovery` 报 `unsupported_adapter`，也就是**唯一能手填映射的类型恰好拉不到模型**。⚠️ 但**未收录的手工 id 仍必须解析失败**（`TestRegistryAliasesAndPrecedence` 钉死这条）——那不是漏配，是刻意不让拼错的 type_hint 静默降级成 OpenAI。
- **profile 还带运行时能力**（`MatchPaths` + `ReasoningLevels`，2026-09-25）：`AcceptedReasoningLevels(typeHint, 解析后的端点路径)` 先按 `type_hint` 查，查不到再按端点后缀匹配（`v1/systemone` → TypeSafe）。它只做一件事：转发前把下游的 `reasoning_effort` 落到上游真认的档位（TypeSafe 只收 `none/low/medium/high/xhigh`，实测把 `max`/`minimal` 直接打成 `400 field ReasoningEffort invalid`）。⚠️ **保存期的字段映射刻意不适用端点匹配**——手写映射的主人是操作员，运行时只借能力、不碰映射。
- 与它并列的是操作员声明的天花板 `max_reasoning_effort`（`downgradeReasoningEffort`）：**留空或选 `max` = 不降档**——上游拒绝 `max` 时要填它支持的最高档（`xhigh`），这是「设了 max 还是 400」的常见根因。两套约束独立生效，改写记在 `mapped_reasoning_effort`（如 `max→xhigh`），降档不消耗 failover。
- **映射区只在「自己接的端点」上出现**（前端 `helpers.isCustomChannelType` + `EditChannelDialog.showEndpointMapSection`）：判定 `custom 类型 || 该行已有映射`。**第二条不可省**——保存时落下的 provider profile、从 base URL 拆出的端点覆盖都写在 `upstream_*` 列上，一旦把区块藏起来，操作员就既看不到也清不掉它们（不可逆）。改动这条判定时，`Channels.test.tsx` 里「普通渠道不渲染」那条会先断言高级区确实展开，别让「没点到按钮」冒充通过。

### 3.9 任意路径透传与端点级覆盖（v3.5）

`relay_custom.go` 注册的 `POST /v1/*` 兜底路由转发**未登记**的 `/v1` 路径。三条边界：

1. **已登记端点优先**：兜底路由注册在 `RelayHandler.Register` 最后，`/v1/chat/completions` 等永不被遮蔽。
2. **路径闭集白名单**（`adapters.IsSafeURLPathSuffix`，全部拼 URL 的地方共用）：每段仅 `[A-Za-z0-9_-]` 与 `.`、
   最多 8 段、每段 ≤128 字节、拒绝纯点段。默认拒绝而非“拒绝已知坏字符”，因为后者要穷举、漏一项就失效。
   query 不参与 URL 构造，因此不会透传。（sub2api `upstream_path_guard.go` 同理。）
3. **基础 URL 拆分**（`adapters.SplitEndpointBaseURL`）：版本号结尾的路径段（`/v1`、`/api/paas/v4`、`/v1beta`、
   `/openai/v1`）识别为 API 根，不拆；其余最后一个路径段视为端点，保存时落到 `upstream_path_override`。
   拆的是端点，不是根。

单模型级别改道理：「请求体 / payload 规则头的 `upstream_path`（或 `upstream_url`）」→ 发送前解析并覆盖
渠道映射结果（`adapters.EndpointOverrideURL`：url 形式优先，必须同 host，query/fragment 一律剥离）。

### 3.10 日志与响应里的真实上游 URL

`proxy_logs.upstream_url`（迁移 104）在每次 attempt 落库：`adapters.SafeURL(upstreamURL)` = scheme + host + path，
query/fragment/userinfo 全剔。成功响应头 `X-Meta-Upstream-URL`（`proxy.UpstreamURLEchoHeader`）同步回传。
日志页把该地址放在渠道名下方（`.log-upstream-url`），并用它回答“这次到底打到哪个端点”。

> ⚠️ FTS5 索引列是固定的：`logfts.go` 的 `ensureLogFTS` 会先 `PRAGMA table_info(proxy_logs_fts)` 比对
> `upstream_url`，缺列就 drop 表 + 触发器再重建。不做这一步的话，新触发器引用不存在的列会让
> **每一次 proxy_logs 写入失败**（日志静默丢失），且 FTS5 是编译期选项、失败不能中断启动。

### 3.11 文档站：参考层由代码生成，不手写

`docs/` 既是仓库里的 Markdown，也是 VitePress 站点的源（`docs/.vitepress/`）。站点发布到 GitHub Pages，
由 `.github/workflows/docs.yml` 负责；它与镜像发布完全解耦，改文档不会碰 release 流程。

**铁律：`docs/reference/*.md` 是生成物，绝不手改。**

```bash
go run ./tools/docsgen     # 重新生成 docs/reference
```

CI 会跑一遍生成器再 `git diff --exit-code docs/reference`。所以：

> **改了任何被参考层覆盖的事实时，必须跑一遍 `tools/docsgen` 并提交结果。**
> 环境变量（`internal/config/config.go`）、运行设置（`internal/runtimeconfig`）、
> 管理面与公开端点（`internal/httpapi` 的 chi 注册）、错误分类（`internal/proxy/proxy_classify.go`
> ∪ `web/src/errorCatalog.ts`）、数据表（**实际迁移一个临时库后读回 schema**，不是解析 SQL）、
> 连接类型（`adapters.OpenAICompatibleBrands()` ∪ `web/src/connectionTypes.ts`）、
> 供应商 profile（`proxy.Profiles()`）——全部在内。

几个维护要点：

- **新增供应商 profile 时**：`providerProfiles` 是私有注册表，生成器走 `proxy.Profiles()` 这个只读枚举器。
  不要为了文档去解析源码副本——那正是本仓库反复被咬的「第二份真相」。
- **两张表之间的差集是要暴露的缺陷，不是噪音。** 生成器首次运行就报出转发层产出的
  `adapter_request` / `content_blocked` / `unsupported_feature` / `unsupported_path` 四个分类
  在控制台分类表里没有对应项。遇到这类差集应当**修代码**（补分类），不是把差集删掉。
- **内链死链会让构建失败**（`markdown.deadLink: 'error'`）。指向不存在页面的链接进不了主干。
- **新增一页要同时改 `docs/.vitepress/docTree.ts`**：nav 与 sidebar 都从它派生，
  不允许在 `config.ts` 里另写一份页面清单。
- **前端构建链与文档站隔离**：vitepress 只在 `docs/package.json`，不进 `web/`，
  否则它会进入应用的 lint / tsc / vite 质量门。

---

## 4. 数据库与迁移

**2026-10-02：旧的单 Key 门户已撤销。** `/portal` 和 `PORTAL_*` 不再使用。
新的团队模式使用独立用户账户，`/me` 已变成用户主体 API，不能恢复旧的 Key 登录契约。
运行设置在 `team_settings`，默认 personal；切回 personal 撤销团队会话并拒绝团队 Key，
不得让 `user_id>0` 的 Key 降级成个人无限制凭据。使用说明见 `docs/team-mode.md`。
`107_portal_credentials.sql` 仅为历史迁移兼容而保留，禁止复用编号或为了清理 UI 删除旧库数据。
`108_team.sql` 引入团队模型。路由授权必须在最终候选、插件改选及故障转移上生效；
用户方案不得写回公共 route_members。修改后跑 `go test ./internal/httpapi ./internal/proxy -run TestTeam`。

- 引擎 SQLite，迁移是 `internal/store/NNN_*.sql`，按文件名的数字序执行。
- **加列**：新增一个 `NNN_描述.sql`，用 `ALTER TABLE ... ADD COLUMN ...`（SQLite 不支持
  `IF NOT EXISTS` 的 ADD COLUMN 组合写法，先查 `schema_migrations` 是否已应用）。
- **绝不删除 `schema_migrations` 表**：删了会在重启时重放迁移 → `duplicate column name` → 容器崩溃循环。
- 时间格式**因表而异**，写种子/修数脚本时混用会静默变成零值时间：
  - `YYYY-MM-DD HH:MM:SS`：`proxy_logs`、`usage_records`、`model_health`
  - RFC3339Nano：`balance_history`、`channel_health_history`、`discovered_models`、`audit_events`

---

## 5. 发布流程

1. 在 `CHANGELOG.md` 写好 `## [vX.Y.Z]` 段落；
2. commit（`feat(scope): ...` 英文，见 CONTRIBUTING.md）；
3. 打**轻量** tag（本仓库现有 tag 全是轻量，**不要加 `-a`**）；
4. `git push origin master && git push origin vX.Y.Z`。

**推 tag 才会触发 release workflow**（构建 Docker 镜像 + 发 GitHub Release）。只推分支不会发版。

---

## 6. 线上部署拓扑（2026-09-29 核对）

| | RN `192.129.128.178`（= EasyTier `10.144.144.6`，宿主名 `racknerd-3088ede`） | 阿里云 `43.108.52.153` |
|---|---|---|
| 角色 | **真正在服务的网关**（`mg.zichuanlan.top` 的流量终点） | 公网入口 + 一台**闲置**的自己的网关容器 |
| 目录 | `/opt/meta-gateway` | `/opt/meta-gateway` |
| 容器 | `meta-gateway-meta-gateway-1` :4100 + `meta-gateway-watchtower` | `meta-gateway-meta-gateway-1` :4100 + `meta-gateway-watchtower` |
| 数据卷 | `meta-gateway_meta-gateway-data` | `meta-gateway_meta-gateway-data` |
| 反代 | 无（公网 22 端口今日不可达，改走 EasyTier `ssh root@10.144.144.6`，密钥 `~/.ssh/mg_prod_ed25519`） | Caddy `/opt/caddy/Caddyfile`：`mg.zichuanlan.top { reverse_proxy 10.144.144.6:4100 }` |
| 域名 | 无（只作后置集群） | `mg.zichuanlan.top`（DNS 解析到此），另有 `mg.015201314.xyz` |

> 上一版的表格把两台的“反代/域名”反过来写了（把 nginx+mg.zichuanlan.top 记在 RN 上）。
> 2026-09-29 实测修正：`Resolve-DnsName mg.zichuanlan.top` → `43.108.52.153`，
> 公网响应带 `Via: 1.1 Caddy`，而该机 Caddyfile 把该域名反代到 EasyTier 内网 `10.144.144.6:4100`。
> **排查“网关行为”类问题时注意**：阿里云本机 `127.0.0.1:4100` 那个容器几乎无流量
> （`proxy_logs` 当天可能只有 4 行），真正的日志在 RN 上。

### 6.1 部署更新：watchtower 是 **HTTP API 模式**，不会自己轮询

2026-09-29 实测纠正（上一版写“推 latest 就自动更新”）：两台 watchtower 的配置里只有
`WATCHTOWER_HTTP_API_UPDATE=true` + `WATCHTOWER_HTTP_API_TOKEN`，**既没有 `--interval` 也没有 `--schedule`**，
而且 compose 未映射 8080 端口。所以推完镜像**不会自動上线**，必须显式触发（无端口映射 → 只能从宿主机打容器 IP）：

```bash
cd /opt/meta-gateway
TOKEN=$(grep -m1 '^WATCHTOWER_TOKEN=' .env | cut -d= -f2-); TOKEN=${TOKEN:-meta-gateway-local-update}
IP=$(docker inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' meta-gateway-watchtower)
curl -H "Authorization: Bearer $TOKEN" http://$IP:8080/v1/update
curl -s http://127.0.0.1:4100/healthz   # 等到 version 变成新 tag
```

取版本用 `/healthz` 的 `version`/`commit` 字段（例：`{"commit":"aad5039…","status":"ok","version":"v3.8.0"}`）——
这是判断“哪台跑的是哪个构建”最快的单一信号。

**主机的可达性**（2026-09-29 实测）：RN 从本机经 EasyTier 可达（`ssh -i ~/.ssh/mg_prod_ed25519 root@10.144.144.6`，
公网 22 不可达）；阿里云从本机 `ssh -i ~/.ssh/mg-aliyun root@43.108.52.153` 可达，但**阿里云上没有 RN 的 SSH key**
（`Permission denied (publickey)`），所以两台要分别从对应入口操作（阿里云那边只能碰它自己那台容器）。

### 6.2 手工更新（不经 watchtower）

```bash
# RN（真正的生产网关），从本机经 EasyTier 直连
ssh -i ~/.ssh/mg_prod_ed25519 root@10.144.144.6
cd /opt/meta-gateway && git pull --ff-only && \
  docker compose pull <svc> && \
  docker compose up -d --no-build --force-recreate <svc>
```

（compose 同时有 `build:` 和 `image:`，加 `--no-build` 就用拉取的镜像，不必本地构建。）

迁移/换角色的铁律：
1. **`.env` 的 `MASTER_KEY` 必须随库一起搬**，否则所有 `secret_enc` / `cookie_enc` / `token_enc` /
   `*_password_enc` 全部解不开；顺带统一 `ADMIN_TOKEN`。校验手法：两台 `--resolve` 打同一个
   reveal 接口，返回串应字节级一致。
2. **切换顺序：先切 DNS（或先把旧主机反代桥接到新主机），再清理旧主机数据。**
3. 恢复数据卷后必须 `chown -R 10001:10001 /d`（应用以 uid 10001 运行）。

> 目录名不代表角色。动手前先用 `docker ps` + 反代目标 + 各库 `channels`/`downstream_keys`
> 计数确认真实身份。

---

## 7. 管理面 API 速用

```bash
# 登录换取 session_token
curl -s -X POST https://<host>/admin/session -H 'Content-Type: application/json' \
  -d '{"token":"<管理口令>"}'

# 最近代理日志（排障第一现场）
curl -s -H "Authorization: Bearer <session_token>" \
  "https://<host>/admin/proxy-logs?limit=50"

# 按维度钻取：client token / 渠道 / 模型 / 仅失败 / 上游 request id
curl -s -H "Authorization: Bearer <session_token>" \
  "https://<host>/admin/proxy-logs?downstream_key_id=3&status=failed"
```

日志行本身携带链路要素：`downstream_key_id`（客户令牌）、`path`（中继端点相对名，
客户端入口 = `/v1/` + path）、`route_id` / `route_pattern`、`channel_id`、`upstream_url`
（实际打到的地址）、`upstream_model`、`upstream_key_id`（服务本次尝试的上游凭据 id，
列表接口会附带解析出的 `upstream_key_name`）、`key_fingerprint`（同一把上游密钥的 sha256
前缀，密钥本体从不落库）——控制台日志行的展开区就是据此拼出的链路，排查时可直接按这些字段筛。

排查"上游报错"类问题时，先看 `proxy_logs` 里的 upstream status 与 body ——
本项目是透明网关，**多数"网关的 bug"其实是上游协议不匹配**（例如某上游的 `/v1/images/edits`
只收 JSON、multipart 直接 415）。

---

## 8. 给 AI 代理的行为约定

- 改前端后必须跑 `tsc -b` + `vitest run` + `vite build`，并确认 `internal/webui/dist` 更新。
- 加 i18n 文案必须 zh / en 双语同步，否则 parity 测试红。
- 新增持久化字段走 3.1 的完整链路，不要只加一半。
- 不要为了"让测试通过"放宽 `retry_safety_test.go` 这类不变量测试。
- 不确定某条规则是否仍适用时，以**代码和迁移文件**为准，其次才是本文件；发现本文件过时请顺手修正。

### 控制台交互

- 完整界面主题见 `docs/ui-themes.md`，「设置 → 外观」使用 `web/src/themes/registry.ts` 注册表。两套包共用业务状态与稳定的内容宿主；不要把主题切换实现为重挂整个路由树或另起一套 API/认证逻辑。经典包的 CSS 与动画名称已隔离，改布局须验收两个包。

- 视觉与工作区设计见 `docs/visual-redesign.md`。前端样式位于 `web/src/styles/`：颜色/字号/材质改 `tokens.css`，共享控件改 `system.css`，导航改 `shell.css`，工作区布局改 `workspaces.css`，登录布局改 `login.css`，入场/品牌动效改 `motion.css`；不要把新主题继续堆进旧 `web/src/styles.css`。入场时长集中在 `lib/entranceMotion.ts`，重播不能触碰认证状态；动效须支持跳过、减少动态效果和隐藏页面暂停。
- 业务子组件的主要操作通过 `PageActions` 放入页头。导航与页面框架复用 `ConsoleShell`；手机导航使用共享 Drawer。
- **界面入口可配置**（`web/src/lib/topBar.ts` + 设置 → 外观 →「自定义界面入口」）：两类——
  顶栏控件 `TOP_BAR_CONTROLS`（search / update / theme / language，两个主题包的 `Chrome.tsx` 各自读
  `useTopBarControls()`）与**导航项**（按 path 记录在 `hiddenNav`，`App.tsx` 用它过滤渲染的导航）。
  preference 存 localStorage（`meta-gateway.topbar-items`），带**旧格式迁移**：早期存的是扁平
  `{checkin,update,theme,language}`，读取时把 `checkin` 映射成 `hiddenNav:["/checkins"]`，其余键仍是控件。
  顶栏的入口图标与导航标签**同源**（导航项来自 `web/src/lib/chromeNav.ts`，App 与面板共用一份），
  改标签只改 i18n，不在两处重复写死。退出登录不参与开关（它是控制台唯一一处登出）。
  ⚠️ **一个页面只有一个入口**：签到的顶栏快捷按钮已删除——它和导航里的「签到」指向同一页，
  两个副本反而各自需要一个开关才不含糊。别再为某个页面另加顶栏按钮；要入口换位置就改导航/控件。
  隐藏永远不等于删除：路由照旧挂载，`paletteNav` 始终列全部页面，面板还有「恢复默认」。新增入口时
  三处同步：`TOP_BAR_CONTROLS` / `CHROME_NAV_ITEMS` + 两个 Chrome + 双语 `appearance.chrome.*` 文案。
- **注册表/商店页叫「拓展」（`/console/store`）**：签到与交换已是内置功能，**不是可开关的扩展**
  （`internal/plugins/service.go` 的 `officialCatalog` 为空，`RetireLegacyModules` 启动时清掉
  `exchange` / `checkin` / `operations` / `cliproxyapi` 的旧记录，路由里也不再挂 `requirePluginEnabled`）。
  拓展页只做三件事：注册 sidecar、浏览插件市场、管理已安装插件。别再为签到/交换加回
  「扩展开关」或 store 门控——那个开关反复被启动引导撤销，本来就是装饰。
- **「模型能力注册表」是模型页「模型工具」里的弹窗**（`web/src/features/models/CapabilityRegistry.tsx`），
  不是工作台 tab —— 它描述的是模型清单里的协议数据，工作台只保留「图像 / 文字」两个跑测 tab。
  移动这类入口时，顺手 `grep` 一遍提到旧位置的文案（`workbench.image.noModels` 这类 hint 也是入口）。
- 操作菜单、选择器与弹层复用共享组件，遵守 `docs/console-interactions.md` 的定位、键盘和焦点约定。
- 编辑弹层传 `busy={pending}`，提交按钮显式声明类型；表单值为 `0`、`false` 或空字符串时，不得用 `||` 擦除其语义。
- 路由说明必须带当前分组，展示后端评估；固定成员、配置优先级和实际承接渠道不能混为一谈。
- 检查菜单屏幕边界与嵌套弹层，并同时验收桌面和手机。改动 Vite 产物后再构建 Go。
