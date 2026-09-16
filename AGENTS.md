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

### 完整交付前的质量门（四项全绿才算完成）

```bash
cd web && npx tsc -b && npx vitest run && npx vite build && cd ..
go build ./... && go test ./...
```

> Windows / 沙箱环境注意：`vite build` 默认 `emptyOutDir: true`，会先删掉旧的 `dist/`（含数十个
> 带 hash 的 chunk）。在带批量删除护栏的沙箱里会被中断，表现为 `error during build:` 后面**没有任何
> 错误详情**，`dist/assets` 被清空但新产物没写出来。遇到此特征请提权重跑，不要误判为代码问题。

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
  probe/            # 渠道健康探测与候选评估
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
web/                # 前端源码（React 19 + Vite + TS）
docs/               # 设计文档
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

新增表单字段的完整链路：
`store 迁移列 → ListOverviews 投影 → domain 归一化 → httpapi 校验 → 前端类型/i18n/CSS → 测试`。

### 3.2 计费单价：两层「整层替换」优先级链

实现在 `internal/proxy/proxy_health.go` 的 `billingCost`（key 级单价已于 2026-09-13 移除）：

1. `route_members.price_*_per_1k`（最具体：该路由 × 该渠道）
2. `model_metadata.price_*_per_1k`（按模型名）

**命中判定**：该层 `prompt > 0 || completion > 0`，命中即停止下探；两层都全 0 → cost = 0（免费）。

> **坑：层内不逐字段回退。** 只填了 completion 而 prompt 留 0 的那一层，prompt 会按 0 计（免费），
> 不会再去找下一层的 prompt 价。

最后乘 `model_ratios.ratio`（控制台"计费倍率"，默认 1.0，可 0~1000），倍率不参与层选择。
cache-read 按 cache 单价（未设则按 prompt 价）；cache-creation 按 prompt 价。

**配额与计价正交**：`quota_total_tokens` / `quota_used_tokens` 按 **token 数**扣，与单价无关；
单价只影响 `usage_records.cost`。

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
`route_archived` / `route_enabled`，Undo 逆序回放并跳过已 undone 的 op。三条不变量：

1. 归档只 disable 不 delete，单条还原 = 把 op 置为 undone；
2. 撤销 `route_created` 前必须确认路由上没有手工成员或其他活跃批次的成员（`ensureRouteUndoable`），否则会级联删光；
3. 预览组只有在"全 mapped 且无 exposed originals（已启用的变体名路由）"时才可省略，否则还原过的原名永远无法再隐藏。

### 3.6 零信任日志

`proxy_logs` 只存元数据，不落任何凭据明文。新增字段前问一句：这个字段会不会带上
`Authorization` / `Cookie` / `token` / `secret`？会的话就不能进日志、进审计、进错误响应体。

### 3.7 模型同步模式

`domain.ModelSyncMode`：`auto`（discovery 自动把探测到的模型采纳为路由 + 成员）vs
`manual`（只刷新候选快照，逐个人工采纳）。`NormalizeModelSyncMode` 把空值/未知值归一为
**manual**（安全默认）。`runtime_settings.default_model_sync_mode` 是新 channel 的继承来源。

---

## 4. 数据库与迁移

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

## 6. 线上部署拓扑（2026-09-13 之后）

| | RN `192.129.128.178`（生产） | 阿里云 `43.108.52.153`（demo） |
|---|---|---|
| 目录 | `/opt/meta-gateway` | `/opt/meta-gateway` |
| 容器 | `meta-gateway-meta-gateway-1` :4100 + watchtower | 同左 |
| 数据卷 | `meta-gateway_meta-gateway-data` | `meta-gateway_meta-gateway-data` |
| 反代 | 宝塔 nginx（`/www/server/panel/vhost/nginx/*.conf`） | Caddy（`/opt/caddy/Caddyfile`） |
| 域名 | `mg.zichuanlan.top` | `mg.015201314.xyz` |

更新：

```bash
git pull --ff-only && \
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
```

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
- 操作菜单、选择器与弹层复用共享组件，遵守 `docs/console-interactions.md` 的定位、键盘和焦点约定。
- 编辑弹层传 `busy={pending}`，提交按钮显式声明类型；表单值为 `0`、`false` 或空字符串时，不得用 `||` 擦除其语义。
- 路由说明必须带当前分组，展示后端评估；固定成员、配置优先级和实际承接渠道不能混为一谈。
- 检查菜单屏幕边界与嵌套弹层，并同时验收桌面和手机。改动 Vite 产物后再构建 Go。
