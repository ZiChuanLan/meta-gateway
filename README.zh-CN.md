# Meta Gateway

生产级 OpenAI 兼容转发网关，支持多渠道路由、重试、加密凭证、模型发现、签到（check-in）、资产交换（exchange）、审计、指标与在线 SQLite 备份。

除核心转发链路外，还内置：告警规则（指标 → Webhook）、敏感提示词防护规则、错误透传/改写规则、插件市场、管理端 TOTP 双因素认证、下游密钥额度兑换码、模型元数据与模型未找到黑名单、路由决策快照、健康历史与可用性汇总，以及定时数据库维护（孤儿数据 GC + VACUUM）。

用量计量会记录上游 `usage` 字段中的提示词/补全 tokens，可对每个令牌实施可选额度，并在管理后台“令牌”页展示估算费用。

网关启动后，内置 Web 管理后台位于 `http://127.0.0.1:4100/console/`。

管理后台的**商店**（`/console/#/store` 或侧边栏**商店**）用于管理可选**插件**（Exchange 导入/导出 + WebDAV、Check-in）。核心功能——连接、模型、令牌、日志、运行时、发现、**审计**和**备份**——始终可用，不受商店门控。

## 快速开始

### 前置要求

- Go 1.26.4+（或 Docker）
- Node.js 24+（源码构建时需要）
- SQLite（内嵌，无需外部依赖）

### 使用 Go 构建

```bash
# 克隆并构建
cd meta-gateway
cp .env.example .env
# 将 ADMIN_TOKEN、MASTER_KEY、METRICS_TOKEN 设置为互不相同的随机值

cd web
npm ci
npm run build
cd ..
go build -o bin/meta-gateway ./cmd/server
ADMIN_TOKEN=my-admin-token MASTER_KEY=my-32-char-master-key-for-aes! ./bin/meta-gateway
```

打开 `http://127.0.0.1:4100/console/` 并使用 `ADMIN_TOKEN` 连接。浏览器仅将其作为 Admin Bearer 令牌发送，保存在内存中，或在需要时存入 `sessionStorage`；绝不会写入 Cookie、URL 或 `localStorage`。

### 使用 Docker Compose

```bash
cp .env.example .env
# 替换 .env 中所有必填密钥占位符。
docker compose up -d --build
```

### 验证

```bash
curl http://127.0.0.1:4100/healthz
# → {"status":"ok"}
```

## 配置

| 环境变量 | 默认值 | 说明 |
| --- | --- | --- |
| `HTTP_ADDR` | `:4100` | 监听地址 |
| `DATA_DIR` | `./data` | SQLite 存储目录 |
| `ADMIN_TOKEN` | _(必填)_ | 管理端接口 Bearer 令牌 |
| `ADMIN_TOKENS` | 空 | 额外逗号分隔的管理令牌（用于轮换） |
| `MASTER_KEY` | _(必填)_ | 静态加密密钥（32+ 字符） |
| `EXCHANGE_ALLOW_SECRET_EXPORT` | `true` | 是否允许交换导出时包含密钥 |
| `METRICS_TOKEN` | _(按需必填)_ | `/metrics` 的独立 Bearer 令牌 |
| `BACKUP_DIR` | `<DATA_DIR>/backups` | 受控的在线备份目录 |
| `OUTBOUND_ALLOW_HOSTS` | 空 | 受信任的私有上游主机白名单（精确主机名） |
| `OUTBOUND_ALLOW_CIDRS` | 空 | 受信任的私有上游网段白名单 |
| `OUTBOUND_MAX_IDLE_CONNS` | `512` | 出站空闲连接总数上限 |
| `OUTBOUND_MAX_IDLE_CONNS_PER_HOST` | `64` | 每个上游主机空闲连接上限（Go 默认为 2） |
| `SQLITE_MAX_OPEN_CONNS` | `4` | SQLite 连接池上限（WAL 支持并发读；`1` 为完全串行） |
| `TRUSTED_PROXY_CIDRS` | 空 | 允许提供转发客户端地址的对端 |
| `TRUSTED_SCRAPER_CIDRS` | 空 | 允许免令牌抓取指标的网络 |
| `RELAY_RATE_PER_MINUTE` / `RELAY_RATE_BURST` | `600` / `100` | 每密钥转发限流；速率 `0` 关闭 |
| `ADMIN_RATE_PER_MINUTE` / `ADMIN_RATE_BURST` | `300` / `50` | 全局管理端限流；速率 `0` 关闭 |
| `AUDIT_RETENTION_DAYS` / `AUDIT_RETENTION_ROWS` | `90` / `100000` | 审计保留上限；`0` 关闭对应维度 |
| `RETRY_TIMES` | `2` | 重试轮数：首次尝试失败后额外尝试多少个渠道（每轮 = 一个渠道） |
| `CHANNEL_RETRY_TIMES` | `1` | 同密钥重发次数：可重试失败在同一上游密钥上重发多少次后才切换下一个密钥/渠道（0-5；网络错误在这些次数后快速失败） |
| `KEY_POOL_ROTATION` | `true` | 某个密钥失败时轮换站点内的其他 API 密钥；关闭 = 仅使用渠道绑定的密钥 |
| `CROSS_CHANNEL_FAILOVER_ENABLED` | `true` | 失败的请求是否可以切换到其他渠道；关闭时只尝试第一个选中的渠道 |
| `COOLDOWN_SECONDS` | `30` | 可重试成员失败后的固定冷却时间 |
| `PROGRESSIVE_COOLDOWN_ENABLED` | `true` | 连续失败后逐级升级成员冷却（`COOLDOWN_LEVEL2/3/4_SECONDS`），而非固定暂停 |
| `BREAKER_FAIL_COUNT` | `5` | 连续失败多少次后停用路由成员（0 关闭；渐进模式下限 5） |
| `MODEL_BREAKER_FAIL_COUNT` | `5` | 连续失败多少次后打开内存中的渠道×模型断路器（0 关闭） |
| `KEY_FAIL_THRESHOLD` | `5` | 每密钥连续失败多少次后排除该密钥 30 分钟（0 关闭） |
| `CHANNEL_AUTO_DISABLE_THRESHOLD` | `5` | 连续转发失败多少次后自动停用渠道（0 关闭） |
| `RECOVERY_PROBE_ENABLED` / `RECOVERY_PROBE_INTERVAL_SECONDS` | `true` / `600` | 对自动停用渠道进行恢复探测 |
| `ROUTING_LATENCY_AWARE` / `ROUTING_ERROR_AWARE` / `ROUTING_CONCURRENCY_AWARE` | `true` | 路由信号开关（延迟、错误历史、并发负载） |
| `ROUTING_CONCURRENCY_LIMIT` | `64` | 并发信号的每模型在途上限 |
| `STABLE_FIRST_ENABLED` / `STABLE_FIRST_DENOMINATOR` / `STABLE_FIRST_PROMOTE_REQUESTS` | `false` / `25` / `100` | 灰度发布路由：`stable_first` 渠道承接约 1/N 流量，积累足够成功请求后自动提升 |
| `STICKY_ENABLED` / `STICKY_TTL_MINUTES` | `false` / `30` | 会话粘性路由开关 + 绑定 TTL（可热切换） |
| `CHECKIN_ENABLED` | `false` | 启动定时凭证签到 |
| `CHECKIN_CRON` | `0 8 * * *` | 标准五段式签到调度 |
| `CHECKIN_TZ` | 空 | `CHECKIN_CRON` 的时区（容器默认 UTC；可设为 `Asia/Shanghai`） |
| `HEALTH_SWEEP_ENABLED` / `HEALTH_SWEEP_INTERVAL_SECONDS` | `false` / `300` | 主动健康巡检（延迟采样） |
| `ALERT_CONFIG_JSON` | 空 | 告警矩阵 JSON（bark / serverchan / telegram / SMTP + 冷却） |
| `ALERT_SWEEP_INTERVAL_SECONDS` / `ALERT_DAILY_SUMMARY_INTERVAL_SECONDS` | `0` / `0` | 告警评估与每日汇总频率（0 = 关闭） |
| `RELAY_MODEL_RATE_PER_MINUTE` / `RELAY_MODEL_RATE_BURST` | `0` / `0` | 可选按模型转发限流（0 关闭） |
| `PLUGINS_DIR` | `<DATA_DIR>/plugins` | 官方模块包目录 |
| `PLUGIN_CATALOG_URL` | 空 | 额外的插件市场注册表 URL（逗号分隔） |
| `WEBDAV_SYNC_ENABLED` | `false` | 启用只读 WebDAV 备份拉取 |
| `WEBDAV_URL` / `WEBDAV_USERNAME` / `WEBDAV_PASSWORD` | 空 | WebDAV 引导凭证 |
| `WEBDAV_BACKUP_PASSWORD` | 空 | 加密 AAH 信封的解密密码 |
| `WEBDAV_CRON` | `0 */6 * * *` | WebDAV 拉取调度 |
| `WEBDAV_MAX_BYTES` | `10485760` | WebDAV 最大下载大小 |

`METRICS_TOKEN` 仅在配置了 `TRUSTED_SCRAPER_CIDRS` 时可为空。所有超时与请求体/头限制见 `.env.example`。非法安全配置会导致启动失败。

## API 概览

### 健康检查

```
GET /healthz → 200 {"status":"ok"}
```

### 管理端（需 `Authorization: Bearer <ADMIN_TOKEN>`）

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET | /console/sites | 站点列表 |
| POST | /console/sites | 创建站点 |
| GET | /console/sites/{id} | 获取站点 |
| PUT | /console/sites/{id} | 更新站点 |
| DELETE | /console/sites/{id} | 删除站点 |
| GET | /console/site-type?url=… | 探测上游平台（AAH 链路） |
| POST | /console/connections | 一键创建：站点 + 凭证 + 渠道（带回滚） |
| GET | /console/sites/{siteId}/credentials | 站点凭证列表 |
| POST | /console/sites/{siteId}/credentials | 创建凭证（加密存储密钥） |
| DELETE | /console/credentials/{id} | 删除凭证 |
| GET | /console/channels | 渠道列表 |
| GET | /console/channels/overview | 渠道概览（健康/就绪状态） |
| GET | /console/search?q=… | 全局搜索 |
| POST | /console/channels | 创建渠道 |
| POST | /console/channels/{id}/duplicate | 复制渠道 |
| POST | /console/channels/{id}/ping | 连通性检测 |
| GET | /console/channels/{id} | 获取渠道 |
| PUT | /console/channels/{id} | 更新渠道 |
| DELETE | /console/channels/{id} | 删除渠道 |
| POST | /console/reset | 工厂重置（清空业务数据） |
| GET | /console/routes | 路由列表 |
| GET | /console/routes/overview | 路由概览（含成员） |
| GET | /console/routes/explain?model={model} | 解释候选渠道资格与优先级 |
| POST | /console/routes | 创建路由 |
| GET | /console/routes/{id} | 获取路由 |
| PUT | /console/routes/{id} | 更新路由 |
| DELETE | /console/routes/{id} | 删除路由 |
| GET | /console/routes/{routeId}/members | 路由成员列表 |
| POST | /console/routes/{routeId}/members | 创建路由成员 |
| PUT | /console/route-members/{id} | 更新路由成员 |
| POST | /console/route-members/{id}/clear-health | 清除成员失败/冷却状态 |
| DELETE | /console/route-members/{id} | 删除路由成员 |
| GET | /console/downstream-keys | 下游密钥列表 |
| POST | /console/downstream-keys | 创建下游密钥 |
| PUT | /console/downstream-keys/{id} | 更新下游密钥 |
| DELETE | /console/downstream-keys/{id} | 删除下游密钥 |
| GET | /console/usage/summary | 用量汇总（请求/tokens/费用） |
| GET | /console/usage?limit=… | 用量记录 |
| GET/PUT | /console/ratios | 模型成本系数（1.0 = 无加价） |
| GET/PUT/DELETE | /console/groups | 租户分组（额度 / 限流） |
| PATCH | /console/channels/tag/{tag} | 按标签批量操作渠道 |
| GET | /console/sticky | 会话粘性路由统计 |
| GET | /console/proxy-logs | 代理日志列表 |
| GET | /console/proxy-logs/latency-histogram | 延迟分布 |
| GET | /console/decision-snapshot | 路由决策审计轨迹 |
| GET/DELETE | /console/model-blocks | 模型未找到黑名单 |
| POST/GET/DELETE | /console/redemption-codes | 额度兑换码 |
| GET/PUT/DELETE | /console/model-metadata | 模型能力元数据注解 |
| GET | /console/health-history?channel_id=&hours= | 最近探测点 |
| GET | /console/health-history/summary?hours= | 每渠道可用性汇总 |
| GET/POST/PUT/DELETE | /console/alert-rules | 告警规则（指标 → Webhook） |
| GET/POST/PUT/DELETE | /console/prompt-guards | 敏感提示词防护规则 |
| GET/POST/PUT/DELETE | /console/error-rules | 错误透传/改写规则 |
| POST/GET | /console/db/gc | 执行 / 查看数据库维护 |
| GET/POST | /console/totp/status、/console/totp/setup、/console/totp/enable、/console/totp/disable | 管理端 TOTP 双因素 |
| POST | /console/discovery/channels/{id}/refresh | 刷新单渠道模型与自动路由 |
| POST | /console/discovery/refresh | 刷新全部已启用渠道（带分项结果） |
| GET | /console/discovery/models?channel_id={id} | 持久化模型发现快照 |
| POST | /console/discovery/channels/{id}/probe | 探测模型但不落库 |
| POST | /console/channels/{id}/account/probe | 探测上游账户 |
| POST | /console/channels/{id}/account/sync-keys | 从上游账户同步 sk- 密钥 |
| POST | /console/try/chat | 管理端聊天探测 |
| GET/POST | /console/plugins/* | 模块目录、安装、启用、停用 |
| GET/PUT | /console/webdav/* | 只读 WebDAV 同步状态与设置 |
| GET/PUT/POST | /console/runtime-settings | 运行时热更新（重试、限流、签到、审计） |
| PUT | /console/credentials/{id}/checkin | 启用/停用定时签到 |
| POST | /console/checkin/credentials/{id}/run | 手动执行单个凭证签到 |
| POST | /console/checkin/run | 执行全部已启用签到凭证 |
| GET | /console/checkin/logs | 查看并过滤脱敏签到日志 |
| POST | /console/exchange/export | 导出全部或所选渠道；含密钥需显式开启 |
| POST | /console/exchange/import | 原子导入 canonical、New API 或 AAH V2 渠道资产 |
| GET | /console/audit-events | 追加式脱敏审计事件列表 |
| POST | /console/audit-events/cleanup | 立即执行保留策略 |
| GET | /console/backups | 备份清单 |
| POST | /console/backups | 创建并校验在线 SQLite 备份 |

### 公共接口（需 `Authorization: Bearer <DownstreamKey>`）

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET | /v1/models | 路由可用模型 |
| POST | /v1/chat/completions | 聊天补全（支持 SSE） |
| POST | /v1/completions | 文本补全（OpenAI 兼容） |
| POST | /v1/embeddings | 向量嵌入 |
| POST | /v1/responses | OpenAI Responses API（透传） |
| POST | /v1/messages | Anthropic Messages API（原生客户端） |
| POST | /v1/messages/count_tokens | Anthropic token 计数 |
| POST | /v1/images/generations、/v1/images/edits | 图片生成（透传） |
| GET | /v1/dashboard/billing/credit_summary | 密钥额度/信用汇总 |
| POST | /v1/redemption/redeem | 兑换额度码 |

下游密钥的 `scopes` 会强制执行：`relay` 开放全部公共接口；否则按需使用 `models`、`chat`、`completions`、`embeddings`、`responses`、`messages`。路由先精确匹配模型名，再匹配最长的 `*` / `?` 通配模式。

连接类型 **Anthropic（Claude 官方）** 在链路上使用 Anthropic 认证头和 `/v1/messages`。OpenAI 聊天客户端仍调用 `/v1/chat/completions`；网关对非流式与 SSE 流量双向翻译请求与响应。

连接类型 **Google Gemini（官方）** 对接 `generativelanguage.googleapis.com`（`x-goog-api-key`）。OpenAI `/v1/chat/completions` 翻译为 `generateContent`（非流式与 SSE），`/v1/embeddings` 翻译为 `batchEmbedContents`。模型发现通过 `GET /v1beta/models` 列出 Gemini 模型。

官方模块（`exchange`、`checkin`、`operations`）首次启动自动安装。停用模块会隐藏其管理 API 组；签到调度同样要求 `checkin` 模块处于启用状态。

## 示例：创建渠道并调用 /v1

```bash
# 1. 创建下游密钥
curl -s -X POST http://127.0.0.1:4100/console/downstream-keys \
  -H "Authorization: Bearer my-admin-token" \
  -H "Content-Type: application/json" \
  -d '{"name":"my-app"}' | jq .
# → {"id":1,"name":"my-app","token":"mg-abc123...","enabled":true,...}

# 2. 创建站点
curl -s -X POST http://127.0.0.1:4100/console/sites \
  -H "Authorization: Bearer my-admin-token" \
  -H "Content-Type: application/json" \
  -d '{"name":"OpenAI","base_url":"https://api.openai.com","platform":"openai"}' | jq .

# 3. 创建凭证（密钥静态加密）
curl -s -X POST http://127.0.0.1:4100/console/sites/1/credentials \
  -H "Authorization: Bearer my-admin-token" \
  -H "Content-Type: application/json" \
  -d '{"kind":"api_key","secret":"sk-your-real-key"}' | jq .

# 4. 创建渠道
curl -s -X POST http://127.0.0.1:4100/console/channels \
  -H "Authorization: Bearer my-admin-token" \
  -H "Content-Type: application/json" \
  -d '{"name":"GPT-4","site_id":1,"credential_id":1,"base_url":"https://api.openai.com","models_csv":"gpt-4,gpt-4-turbo","status":"enabled"}' | jq .

# 5. 发现模型并创建精确自动路由
curl -s -X POST http://127.0.0.1:4100/console/discovery/channels/1/refresh \
  -H "Authorization: Bearer my-admin-token" \
  | jq .

# 6. 调用 v1/chat/completions
curl -s -X POST http://127.0.0.1:4100/v1/chat/completions \
  -H "Authorization: Bearer mg-abc123..." \
  -H "Content-Type: application/json" \
  -d '{"model":"gpt-4","messages":[{"role":"user","content":"Hello!"}]}'
```

## 架构

完整架构说明见 [docs/architecture.md](docs/architecture.md)。
部署、出站安全、指标、审计保留、备份与恢复流程见 [docs/operations.md](docs/operations.md)。

Web 管理后台覆盖日常资产、路由、发现、签到、审计、备份与交换操作。指标采集与离线恢复仍属于命令行/运维流程，而非浏览器操作。

路由先按数值更高的优先级求值，权重仅在所选优先级层内使用。若某层所有合格权重均为零，则均匀选择。可重试的传输失败与瞬时上游响应会切换到其他合格渠道；普通客户端错误响应不重试。

模型发现支持 OpenAI 兼容、New API、Anthropic（官方 `GET /v1/models`）与 Gemini（官方 `GET /v1beta/models`）平台。将站点 `platform` 或渠道 `type_hint` 设为 `openai-compatible`、`openai`、`new-api`、`anthropic` 或 `gemini`，然后手动触发刷新。

凭证签到支持 New API 与 One API 站点的 `session` 和 `access_token` 凭证。调度默认关闭；通过管理 API 启用单个凭证后设置 `CHECKIN_ENABLED=true`。New API 凭证可通过 `meta_json` 设置 `{"platform_user_id": 42}`，用于上游要求 `New-Api-User` 头的场景。

渠道交换使用严格版本化的 canonical 格式，并支持文档化的 New API 与 All API Hub V2 兼容输入。含密钥的导出包含明文 API 密钥，并返回 `Cache-Control: no-store`；除非确需迁移，请使用仅元数据导出。完整格式、默认值、幂等性与安全契约见 [docs/aah-exchange-format.md](docs/aah-exchange-format.md)。

## 安全边界

所有适配器、发现、签到、交换与转发请求共用同一出站策略。仅接受不含 userinfo 的 HTTP(S) URL。DNS 答案在连接时校验，重定向重新验证，跨源凭证被移除，默认拒绝回环/私网/链路本地/特殊地址。环境代理变量被有意忽略。

如需放行受信任的自托管上游，请按精确主机名放行，必要时使用尽可能小的 CIDR。主机例外不包含子域。

## 备份与恢复

通过 `POST /console/backups` 创建备份；调用方不能提供文件系统路径。仅在服务停止时恢复：

```bash
DATA_DIR=/data BACKUP_DIR=/data/backups \
  meta-gateway restore --from meta-gateway-YYYYMMDDTHHMMSSZ-xxxxxxxxxxxx.db
```

恢复后的服务必须使用原始 `MASTER_KEY`。备份包含加密凭证，绝不包含密钥本身。
