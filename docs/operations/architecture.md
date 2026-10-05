# 架构总览

Meta Gateway 是一个 OpenAI 兼容中继：按精确模型路由、优先级、权重与共享冷却状态选择上游渠道。

## 四条主链路

```text
下游请求
  → HTTP 适配层（鉴权、校验、响应提交）
  → 代理服务（尝试、重试策略、日志、凭据）
  → 路由选型器（资格过滤、优先级层、加权选择）
  → 存储仓储（SQLite）
  → 中继传输（绑定 context 的上游 HTTP）

管理面
  → 发现服务（资格、凭据、确定性摘要）
  → 平台适配器（有界的上游 /v1/models 请求）
  → 发现归并（快照、models_csv、路由、成员）

管理面 / 定时调度
  → 签到服务（资格、解密、进程内互斥）
  → 平台签到适配器（有界的上游签到请求）
  → 签到审计日志（脱敏结果、奖励、延迟、来源）

管理面（导入导出）
  → 交换解析器（严格 / 兼容形态校验与归一）
  → 交换服务（HMAC 身份、加密、历史匹配）
  → 交换仓储（一个 Site/Credential/Channel 事务）
  → 提交后再跑发现服务（有序、脱敏的结果）

入站 HTTP
  → 请求 ID、可信客户端身份、结构化访问日志
  → 端点鉴权与独立限流器
  → 有界的管理面 JSON 或流式安全的中继

出站 HTTP
  → 共享的 URL / DNS / IP / 重定向 / 凭据转发策略

运维
  → 存活与就绪、受保护的低基数指标
  → 追加型脱敏审计事件与保留期清理
  → 校验过的在线 SQLite 备份与离线可回滚恢复
```

## 包结构

| 包 | 职责 |
| --- | --- |
| `cmd/server` | 生产入口：配置 → 加密 → 存储 → 服务 → HTTP 装配 |
| `cmd/e2e-mock`、`cmd/e2e-runner` | 黑盒 E2E 的假上游与客户端 |
| **HTTP 层** | |
| `internal/httpapi` | 路由与装配根，以及全部 handler。管理面按资源拆分（`admin_sites/connections/credentials/channels/routes/keys/usage/rules/ops.go`，共享 `admin_validation.go`）；中继端点都在 `relay.go` |
| `internal/auth` | 管理员与会话令牌、下游令牌 bearer 鉴权、作用域、模型过滤、过期与 IP 校验 |
| `internal/ratelimit` | 进程内令牌桶：管理面、下游令牌、分组、模型 |
| **中继核心** | |
| `internal/proxy` | 转发引擎，按职责拆分：`proxy_forward.go`（候选循环 `ForwardWithMeta`）、`proxy_keypool.go`（API Key 池、按 Key 的熔断）、`proxy_classify.go`（可重试性与错误分类）、`proxy_health.go`（成员/渠道/Key 记账与用量写入）、`proxy_rewrite.go`（模型改名、推理降档、系统提示词、头部覆盖）、`proxy_streams.go`（首分片窥探、静默 SSE 检测）、`proxy_direct.go`（不经过路由的渠道冒烟测试），以及 `circuit_breaker.go`、`channel_gate.go`、`payload_rules.go`、`prompt_guard.go`、`jsonpath.go` + `upstream_map.go` + `upstream_map_validate.go`（渠道端点与字段映射） |
| `internal/routing` | 纯候选评估（优先级层、加权 / 时延 / 自适应、单渠道固定、会话粘性）与解释输出 |
| `internal/relay` | 薄的中继传输层（绑定 context） |
| `internal/adapters` | 无状态的平台集成：转发适配器 + N×M 转换注册表、模型清单、签到、账号适配器 |
| **上游账号生命周期** | |
| `internal/account` | 针对上游平台的账号探测、财务与密钥同步 |
| `internal/checkin` | 按凭据作用域的签到编排 + 带补跑的 cron 调度 |
| `internal/discovery` | 模型清单刷新、归并成路由与成员、被动恢复 |
| `internal/healthsweep` | 带抖动的周期渠道健康探测（operational / degraded / error） |
| **持久化** | |
| `internal/store` | SQLite：按文件名跟踪的迁移、按实体的仓储、热路径缓存（下游令牌、分组）、用量写入、GC |
| `internal/domain` | 共享实体结构与状态/分类常量 |
| **运维与集成** | |
| `internal/alerts` | 可配置的告警规则评估（60 秒一跳） |
| `internal/financesweep` | 主动余额/token 扫描 + 经通知器的每日摘要 |
| `internal/plugins` | 插件目录、市场、sidecar、模块启用门控、拦截钩子 |
| `internal/runtimeconfig` | 持久化的管理面覆盖，实时应用到运行中的服务 |
| `internal/webdavsync` | 加密的 WebDAV 备份拉取同步 + 调度 |
| `internal/exchange` | 带版本号的 AAH / New API 导入导出，含密文往返 |
| `internal/backup` | 在线 SQLite 备份 + 离线恢复 |
| `internal/maintenance` | cron 驱动的孤儿 GC / VACUUM + 每日余额扫描（快照与保留期裁剪） |
| **基础设施** | |
| `internal/config` | 环境变量解析与校验 |
| `internal/crypto` | 主密钥 AES-GCM、指纹 |
| `internal/outbound` | SSRF 策略 + 共享 HTTP 客户端 + 按渠道的代理钩子 |
| `internal/webhook` | 多通道通知器（webhook / bark / serverchan / telegram / SMTP） |
| `internal/usage` | 从 OpenAI / Anthropic 响应体解析 token 用量 |
| `internal/sitedetect` | AAH 风格的站点平台识别 |
| `internal/totp` | 管理面二次验证的 TOTP 生成与校验 |
| `internal/observability` | 就绪状态与 Prometheus 文本指标 |
| `internal/webui` | 内嵌的控制台静态资产（`//go:embed dist`，由 `web/` 构建） |

`httpapi` 是装配根，导入所有其他包；**没有任何包反向导入它**。各服务自己拥有后台循环，并向 `httpapi` 的生命周期注册表登记停止回调。

> [!IMPORTANT]
> 测试里建 router **必须**用 `NewTestRouter`，它会在测试结束时停止后台任务。直接用 `New` 会让调度器活到进程退出（实测 44 个泄漏 router ≈ 458 个常驻 goroutine）。`internal/httpapi/background_test.go` 有一个不变量测试加 TestMain 兜底守着这条约定。

## 前端控制台（`web/src`）

| 路径 | 职责 |
| --- | --- |
| `App.tsx` | 登录流程 + 已认证外壳（侧栏、全局搜索、路由） |
| `api/client.ts`、`api/types.ts` | 类型化的管理面 API 客户端，镜像 `internal/httpapi` |
| `i18n/` | `en.ts` + `zh.ts`，由 parity 测试保证键一一对应 |
| `features/` | 一页一个目录；`ops/`、`channels/`、`models/` 把弹层、徽标与纯函数从页面组件里拆出来 |
| `components/` | 设计系统（`ui.tsx`、`Drawer`、`ActionMenu`、`SecretRevealDialog`、图表、选择器） |
| `hooks/`、`lib/` | `useAdminMutation`（失效与 pending 状态）、分页、纯格式化 |
| `styles/` | 样式按层拆分：令牌、共享控件、导航、工作区、登录、动效 |

## 路由选型

路由优先匹配**精确** `model_pattern`；没有精确路由时，最长的启用通配符模式胜出（`*` 匹配任意串，`?` 匹配单个字符）。

`RouteMember` 是优先级与权重的**运行时真相源**；渠道自身的优先级与权重是兼容性/默认元数据。

选型步骤：

1. 加载启用的精确路由，以及全部成员/渠道/凭据事实。
2. 没有精确路由时，加载最佳通配符路由及其成员。
3. 把成员收窄到下游令牌绑定的分组：令牌可带 `route_group_name`；当该路由定义了那个分组时按 `route_members.group_name` 过滤，没定义时回落到 `default` 组（没有绑定的令牌用 `default`）。同一个渠道可以属于同一条路由的多个分组——成员身份按 `(route_id, channel_id, group_name)` 唯一。
4. 排除禁用的成员/渠道、不可用的凭据、冷却中的成员，以及本次请求已经尝试过的渠道。
5. 选择**有可用成员的、数值最高的优先级层**。
6. 在该层内按正权重选择。
7. 该层所有权重都为 0 时，均匀选择。

`GET /console/routes/explain?model=<model>` 用同一个评估器，返回稳定的原因码且**不改变状态**。

## 重试与流式

代理服务会重试传输失败，以及上游状态码 408、429、500、502、503、504，并完整计入失败统计（成员冷却 + 渠道连续失败计数）。

**上游 4xx 也会换渠道**——渠道能力是异构的，一个上游拒绝的请求另一个可能接受（例如某网关支持而另一个不支持的 `reasoning_effort` 取值）。但 4xx **只冷却该成员**，绝不进入渠道连续失败计数或自动禁用，因为请求本身可能有问题。

本地适配器/配置错误立即返回：故障转移帮不上忙。

一个渠道在一次请求里只被尝试一次；重试次数受 `RETRY_TIMES` 与候选耗尽双重限制。`CROSS_CHANNEL_FAILOVER_ENABLED=false` 会让请求在第一个选中的渠道之后停止，但**不改变保存的重试上限**；同渠道的 API Key 轮换仍然启用。

- 可重试的失败会累加成员失败计数并施加固定冷却；成功会清空成员失败状态。
- 每次上游尝试写一行 `ProxyLog`：请求 ID、渠道、尝试序号、延迟、真实上游状态（可得时）与脱敏的错误分类。
- 下游取消会传播到上游请求。中继用**响应头超时**而不是整体请求超时，所以已建立的 SSE 流可以一直开着，直到被取消或上游关闭。
- **响应已提交后不可能重试。**

## 数据库

SQLite 以 WAL 模式运行。内嵌迁移有序、在事务里执行，并记录在 `schema_migrations` 中；每个文件只执行一次。

- 精确模型名与路由/渠道成员身份有唯一约束，路由查询有索引。
- 用新二进制启动既有库之前先备份。如果历史数据里有重复的精确路由或重复的路由/渠道成员，**迁移会以唯一性错误停下**，而不是静默挑一条删掉。
- 凭据加密落盘，下游令牌存哈希；**没有任何 API 或 `ProxyLog` 字段返回原始凭据材料**。
- 就绪检查做一次有界的数据库探测。
- 在线备份用 SQLite 的备份 API，校验快照后以原子重命名发布。恢复**刻意做成离线 CLI 操作**，并保留被替换的数据库以便回滚。

### 时间格式因表而异

写种子脚本或修数脚本时混用会静默变成零值时间：

| 格式 | 表 |
| --- | --- |
| `YYYY-MM-DD HH:MM:SS` | `proxy_logs`、`usage_records`、`model_health` |
| RFC3339Nano | `balance_history`、`channel_health_history`、`discovered_models`、`audit_events` |

## 模型发现

渠道的适配器由 `type_hint` 解析，回落时取所属站点的 `platform`。OpenAI 兼容与 New API 注册共用 OpenAI 的 `GET /v1/models` 协议，但保留各自不同的来源名。

响应在处理前被限制大小、严格解码、归一并排序——**在任何数据库事务开始之前**。

一次成功的渠道刷新会原子地：替换 `discovered_models`、更新规范的 `channels.models_csv`、创建缺失的精确路由、归并自动路由成员。

- 缺失的模型只禁用**自动且未被人工覆盖**的成员。
- 已有路由与人工路由决策始终归操作员所有。
- 传输、状态码、体积或载荷错误**不改变任何状态**。

## 建路由时顺带补齐的两件事

创建一个路由，就是某个模型名开始可调用的时刻，也是网关能顺手回答「这个模型怎么调」与「哪些渠道到得了它」的时刻——不需要操作员再走第二步：

- **能力补齐。** 内置分类器**同步**跑（`AutoTag` 是本地、幂等、且会跳过人工或目录已拥有的行）。外部目录只针对这一个模型在**后台**查询：一个容量 32 的队列由 `runModelBootstraps` 消费，失败的同步只记日志——周期扫描是兜底。**保存永远不能等下载**，队列满时推迟给扫描而不是阻塞。**通配符模式会被跳过**：`gpt-*` 是匹配器而不是模型 id，没有目录会收录它。
- **一键挂载。** `POST /admin/routes/{id}/auto-match` 把「确实提供该模式」的启用渠道挂进指定的成员分组，用的是与创建路由同一套交集逻辑，所以过期的控制台选择不可能凭空造出一个成员。在交集内，禁用 / 未知 / 已不再提供该模型的 id 报为 `skipped`；**已在目标分组里的渠道既不算新增也不算跳过**——重复执行就是纯空操作。handler 侧 `channel_ids` 为空表示「全部当前匹配」，而 store 侧空列表表示空操作；控制台因此**从不在 0 选中时发请求**，因为线上语义恰好相反。

## 签到与调度

签到是**按凭据作用域**的能力，与模型发现和路由相互独立。

- New API / One API 的 session 或 access token 凭据走 `POST /api/user/checkin`；New API 还可以从凭据元数据里取正的 `platform_user_id`，作为 `New-Api-User` 头发出去。
- 数值用户 id 的解析顺序：先看凭据 `meta_json`（`{"platform_user_id":1544}`，由 AAH 导入写入或在连接编辑器里手填），为空时再问 `/api/user/self`。管理 API 接受数字或带引号的字符串，并归一成裸 JSON 数字。两个来源都拿不到时，本次运行记为 `user_id_unavailable`（**不是** `upstream_status`），把操作员指向要填的字段而不是上游的探测回复。
- 通用外部签到站点（平台 `external-checkin`）是 cookie 认证的、不属于 New-API 家族：适配器向可配置的 `checkin_path`（默认 `/api/checkin/spin`，存在凭据 `meta_json`）发 POST（或 GET），带上由站点 URL 推导的 Origin/Referer，HTTP 2xx 即成功，除非 JSON 体里说 `success:false`（以及常见的「今天已签」标记）。它们与 New-API 凭据走**完全相同的**调度器、资格门、审计日志与告警。
- 手动单目标执行只忽略「按凭据的调度开关」，其他资格规则照旧。批量执行按 ID 顺序选出 `checkin_enabled` 的凭据，并为每一次选中的尝试持久化一行脱敏审计——包括不支持、已禁用、失败与并发跳过。**网络操作永远不在数据库事务里跑。**
- 可选的进程内调度器用一条严格的五段 cron 表达式，与管理面 HTTP 共用同一个服务实例——共享实例避免了同一凭据的两个进程内并发运行。表达式按 `CHECKIN_TZ` 时区（IANA 名）解释；未设置时用进程本地时区，而默认容器镜像里是 UTC（没有 `TZ`、没有 tzdata），所以 UTC+8 的操作员应设置 `CHECKIN_TZ` 以避免 8 小时偏移。时区数据库内嵌在二进制里，任何容器里命名时区都能解析。
- 调度器会**补跑**错过的每日触发：启动或重新启用调度时，如果今天的触发时间已过且今天没有已记录的调度运行（从 `checkin_logs` 推断），立即跑一次。**没有历史的全新安装不会意外运行。** 批量会容忍单凭据的内部失败（瞬时数据库错误）并把它们当成合成的失败项；只有取消会中止剩余凭据。
- 已有凭据迁移后签到是**禁用**状态，且 `CHECKIN_ENABLED` 默认为 false，所以升级不会静默引入外部请求。

> 硬规则：**一个 Credential = 一次签到任务。** 同一凭据同时持有 Access Token 与 Cookie 时也只调度一次，绝不拆成两个任务造成双签到。

## 渠道交换（导入导出）

带版本号的交换边界覆盖三类文档：规范的 Meta Gateway 文档、New API 渠道列表、精简的 All API Hub V2 凭据档案。

- 解析、URL/列表归一、范围检查与重复检测**都在数据库变更之前**完成。
- 用途隔离的 HMAC 标识「归一后的 URL + API Key」，既不存明文，也不依赖随机密文。
- 每个导入的身份拥有**专属**的 Credential / Channel。既有的共享 CRUD 凭据保持有效，但历史采纳要求「一个无歧义的渠道 + 常量时间的密钥相等」。
- Site、Credential、Channel 的写入走一个专属仓储事务。
- 发现只在提交后运行，并以脱敏的逐渠道结果继续；人工路由保护仍在既有归并服务内部。

## 运行时安全与运维

- 私网、环回、link-local、元数据及其他特殊上游地址**默认拒绝**。DNS 在连接时校验，重定向会重新检查。精确主机名与 CIDR 例外用于显式信任的自建上游。
- **环境变量代理被禁用**，因为代理侧 DNS 会绕过上述保证。
- 转发来的客户端地址只从已配置的代理网段接受。
- 中继限流按已认证的下游令牌隔离；管理面用另一个全局限流器。
- 指标用固定基数的标签，并使用与管理面**不同**的凭据。
- 日志、错误、指标与审计**排除**原始 URL、头部、请求体、密钥、密文，以及数据库或加密细节。
- `/healthz` 报告存活。`/readyz` 还要求生命周期处于就绪状态且 SQLite 连接可用。服务端 `WriteTimeout` 保持为 0，让已建立的 SSE 流存活到取消或上游关闭。关闭时先把就绪标记为 false 再排空。

## 插件拦截钩子

sidecar 插件可以在 manifest 里声明 `hooks` 并参与转发路径。`internal/proxy/hooks.go` 拥有契约（`Interceptor` 接缝），`internal/plugins/hooks.go` 拥有宿主侧（声明、模型匹配、HTTP 调用、熔断）。

**`proxy` 从不导入插件机制**——与 `LiveTraceObserver` 同一种形状。

| 钩子点 | 运行时机 | 可以改什么 |
| --- | --- | --- |
| `route` | 每请求一次，在选型之前 | 被路由的模型名，或拒绝该请求 |
| `request` | 每次渠道尝试一次，在上游请求体与端点定稿之后 | 上游请求体与头部 |
| `response` | 成功的非流式应答，在客户端看到之前 | 应答体、状态码与头部 |

**流式应答刻意不开放**：SSE 体是逐块拷贝的，没有完整文档可以重写，而缓冲一份会把流式存在的意义——延迟——直接毁掉。

四条性质在每条路径上都成立，且每一条都是承重的：

1. **未匹配的模型永远到不了插件。** `match_models` 是内存内的 glob 匹配，用路由模式的语义（`store.MatchModelPattern`），所以普通流量零成本。钩子也可以改声明 `models_path`——一个插件端点，它报出的名字会替换声明的匹配器，仍然只在内存里。
2. **fail-open。** 超时、传输错误、非 200、畸形 JSON、panic —— 全部意味着「没有意见」，请求原样继续。熔断打开（连续五次失败）后停 30 秒，然后放一个探测过去。
3. **不自拦截。** 网关在钩子调用上打 `X-Meta-Hook-Origin` 与 `X-Meta-Hook-Depth`；插件把这两个头透传到嵌套的网关调用上时，它自己的钩子就不会作用于那个请求。深度 ≥3 时跳过所有钩子。
4. **显式权限。** 声明了 `hooks` 的 manifest 必须同时声明 `relay:intercept`，否则注册被拒。拦截会把匹配到的提示词与应答暴露给插件进程。

`route` 决策会同时改写路由键与请求体里的模型名，并记录在决策快照（`hook_decisions`）、`X-Meta-Hook-Decision` 响应头，以及（对虚拟模型名而言）`/v1/models` 目录里。

### 插件模型发现（`models_path`）

钩子可以声明 `models_path`：一个插件相对的 GET 端点，应答 `{"models":[...]}`（OpenAI 形状的 `{"data":[{"id":...}]}` 也接受）。

网关会在这些时机询问它——注册、启用、保存配置、托管插件进程启动、网关启动，以及一个 30 秒的慢定时器上。用的是钩子调用同样的凭据，**包括 `X-Plugin-Config`**。

报出的名字会替换该路由钩子声明的 `match_models`，并且就是 `/v1/models` 对外宣传的内容。所以插件可以按自己的配置改名虚拟模型，而网关只需要发现答案。替换只发生在一处——`rebuildHookEntriesLocked`，即热路径、目录与控制台共同读取的唯一位置。

失败静默降级：端点不可达或答案不可用（非 JSON、为空、含通配符名、条目数超过 `maxDiscoveredModels`）时保留上次已知列表；首次发现则用声明的 `match_models`。声明了 `models_path` 的钩子可以完全省略 `match_models`——报出的列表就是匹配列表，省略**从不**意味着「匹配空」。发现可以让改名变慢，**永远不会**扩大或清空钩子拦截的范围。

> 插件**代码**不属于本仓库。官方注册表与一方插件源码在 [`ZiChuanLan/meta-gateway-plugins`](https://github.com/ZiChuanLan/meta-gateway-plugins)（`demo-plugin` 是协议参考，`jev-router` 是一个可用的 `route` 钩子）。第三方插件在自己的仓库里，通过一条注册表条目（或用 `PLUGIN_MARKET_URLS` 指向自建注册表）接入，所以没有任何插件源码需要合并到这里。留在这里的是协议本身：manifest 形状（`internal/plugins/service.go`）、宿主侧（`internal/plugins/hooks.go`）与契约（`internal/proxy/hooks.go`）。

## 转发适配器（平台互译）

中继路径对客户端说 OpenAI 协议。原生就说 OpenAI 的渠道（`openai-compatible`、`new-api`、`one-api` 及各中继品牌）由默认的 passthrough 适配器**原样转发**。原生平台通过按平台注册的转发适配器翻译：

| 平台 | 适配器 | 转换 |
| --- | --- | --- |
| OpenAI 兼容 | `OpenAIPassthroughAdapter`（默认） | 无——原样透传 |
| Anthropic | `AnthropicForwardAdapter` | OpenAI chat ⇄ Messages API（`x-api-key`、`anthropic-version`）；Anthropic SSE → OpenAI chunks |
| Gemini | `GeminiForwardAdapter` | OpenAI chat ⇄ `generateContent` / `streamGenerateContent`（`x-goog-api-key`）；embeddings ⇄ `batchEmbedContents` |

`ForwardAdapter` 接口（`internal/adapters/forward.go`）覆盖：渠道匹配（`IsFor`）、上游 URL 构造、请求转换（`TransformRequest`）、响应转换（`TransformResponse`）、SSE 流包装（`WrapStream`）、上游认证头（`AuthHeaders`）、供应商用量提取（`ExtractUsage`）。

`proxy` 通过 `Registry.ResolveForward(typeHint, platform)` 按渠道解析适配器，找不到时回落到 passthrough。**用量统计跑在转换后的 OpenAI 形态响应体上**（非流式）或最后一个 SSE 分片上（流式），所以原生渠道也能报出真实 token 用量。

**加一个上游平台 = 一个适配器实现 + 一行注册。**

## 渠道端点与字段映射

转发适配器假定上游是 `/v1` 根下的 OpenAI 形态。有两类上游打破这个假设，而一套渠道级机制同时覆盖两者（`internal/proxy/upstream_map.go`，`channels.upstream_*` 四列）：

| 问题 | 机制 |
| --- | --- |
| **路径根不对** | `JoinOpenAIPath` 会插入 `/v1`，于是智谱的 base 变成了不存在的 `/api/paas/v4/v1/models`。`upstream_path_override` / `upstream_path_map` 替换路径；映射存在时 URL 改用 `adapters.JoinRawPath` 构造，base 保留自己的根。 |
| **协议不对** | TypeSafe 的 `POST /v1/systemone` 收 `{state, model, questions}`、答 `{answers:{…}}`，不是 OpenAI chat。`upstream_request_map` / `upstream_response_map` 用 payload_rules 的路径语言在两个形状之间搬运字段。 |

**两个方向上的顺序都很重要：**

- 请求：适配器 `TransformRequest` → 字段映射 → 上游。
- 响应：适配器 `TransformResponse` → 字段映射 → 客户端。

所以映射路径描述的是**客户端看到的文档**。

没有映射的渠道走的还是原来那条路（引擎在 `Empty()` 上直接退出），所以这个特性是纯增量的。每个映射都是 fail-open：畸形或不匹配的映射会原样转发原始字节并记一行日志。管理 API 在保存时校验语法（`proxy.ValidateUpstreamMap`），因为运行时的拼写失败模式是**静默空操作**——对操作员来说和「上游拒绝了」无法区分。

## 供应商 profile

非 OpenAI 供应商的映射是**供应商的属性**，所以随供应商一起发布（`internal/proxy/provider_profile.go`），而不是一个需要操作员点击、再手工验证的控制台预设按钮。

保存一个类型能解析到 profile 的渠道时，会填上那四个映射列——**且只在操作员留空的地方填**。已经带请求或响应映射的渠道被视为手工构建，完全不动。因此控制台的映射字段扮演的是**覆盖与检视**界面。

控制台的供应商列表携带每个预设的文档端点；`SplitEndpointBaseURL` 在保存时把粘进来的端点剥回「根 + 覆盖」，所以 `/models` 与对话端点深度不同的供应商（TypeSafe：`GET /v1/models` 在裸主机上，`POST /v1/systemone` 在 `/v1` 下）两个方向都可达。

**TypeSafe 是那个被完整验证过的例子**，每个细节都是对着真实 API 测出来的，不是推断的：

- `questions` 是按问题 id 索引的**对象**，不是数组；
- 一个带类型的答案回来时是数字（`answers.<id>.noul`），所以需要一条 `template` 才能到达 OpenAI 的字符串 `content`；
- 它的请求模型是严格的——多一个键（连 `temperature` 都算）就答 `400 api_usage_error`。OpenAI 形态的请求体必然带 `messages`，所以请求映射用 `keep` 形式（顶层 body 白名单）把信封削到 `{model, state, questions}`。

`keep` 是通用原语，不是 TypeSafe 的特例：任何严格校验自己请求模型的上游都需要它，而只靠「删除」表达不了这件事——**要删的集合取决于客户端**。条目按数组顺序生效，所以 profile 里是先读 `messages.0.content` 再 keep：读取必须在节点还在的时候发生。

> **作用范围限制值得明说**：这个映射把**一个问题**映射到一次调用。System One 是拿一个 state 对一个问题打分，所以多轮对话无法通过这套映射表达；而客户端发来的开头 system 消息会变成那个 state。

## 自定义路径与单请求端点

三项补充让「一个字段搞定」成为可能——就像 new-api 的 Custom 渠道与 sub2api 的 passthrough 那样：

1. **`POST /v1/<未登记>` 透传**（`internal/httpapi/relay_custom.go`）。`/v1` 表面是一份固定清单，所以说自己协议的上游过去没有入口；现在未登记的路径会被原样转发到 `<base>/<同路径>`，请求体与响应都不动。**已登记端点优先**，因为兜底路由最后注册。路径走闭集白名单（`adapters.IsSafeURLPathSuffix`：每段 `[A-Za-z0-9_-]` 加 `.`，最多 8 段，每段 ≤128 字节，拒绝纯点段）——理由与 sub2api 的 `upstream_path_guard.go` 一样：一个受客户端控制的字符串被拼进上游 URL，就不能允许它改变那个 URL 的结构。任何 query string 都被丢弃，永不转发。

2. **Base URL 拆分**（`adapters.SplitEndpointBaseURL`）。粘了整个端点（`https://api.typesafe.ai/v1/systemone`）的操作员，保存时会得到「根 + `upstream_path_override`」，这样 `<base>/v1/<path>` 不会把版本段叠两次。拆分**刻意很窄**（`carriesEndpoint`）：只有「版本段不在最后」（`/v1/systemone`）或「结尾是网关自己中继的表面名」（`/chat/completions`，Perplexity 就是这样配的——它文档给的 base 没有 `/v1`，而 `/v1/chat/completions` 是 404）才拆。其余一律保持完整，因为 base 里的一个路径段不一定是端点：`/api/paas/v4` 已经是 API 根，而 `/ok` 或 `/prefix` 是上游在其下服务 `/v1/...` 的**挂载前缀**。拆掉挂载前缀会让该渠道每个请求都丢掉 `/v1/<path>`——这条回归在 v3.4.1 发布过，由 Compose E2E 抓出，所以现在有 `TestMountPrefixBaseURLKeepsTheV1Root` 在本地复现这个契约，而不是留一个约 20 分钟的反馈环当唯一防线。

   `JoinOpenAIPath` 解析同样的三种 base 形状，并与拆分共用 `isAPIRootPath`。`JoinAnthropicPath` 额外把「首段是版本」的覆盖视为已是绝对路径（`/v1/messages`）——因为拆分产出的正是这个形状，而之前的回落会再追加一个 `/v1`。

3. **单请求端点**（`upstream_path` / `upstream_url`，来自请求体或 payload 规则头）。在 payload 规则之后、发送之前解析，所以一个模型可以被改指到另一个端点，而不必为每个端点建一个渠道。`adapters.EndpointOverrideURL` 要求 URL 形式**必须留在渠道的 host 上**：请求带着渠道的 API Key，调用方选定的 host 会变成一个凭据外泄原语。

每次调用都记录 `proxy_logs.upstream_url`，由 `adapters.SafeURL` 渲染成 scheme + host + path，剥掉 query / fragment / userinfo——query string 经常携带密钥。实际服务的 URL 也会通过 `X-Meta-Upstream-URL` 回传给客户端。

> [!WARNING]
> `proxy_logs` 上的 FTS5 索引列是**固定**的。`logfts.go` 会比对 `PRAGMA table_info(proxy_logs_fts)` 与期望列，缺列就 drop + 重建一次：一个引用了索引里不存在列的触发器会让**每一次日志 INSERT 失败**，而 FTS5 是编译期选项，这个失败不能中断启动。

## 中间格式转换链（pivot）

客户端协议（下游线协议）与上游平台通过一个 **pivot** 相连：内部的 OpenAI chat/completions 格式。

每个下游协议实现一个 `SegmentConverter`（`internal/adapters/intermediate.go`），含四部分：请求转 pivot（`ToOpenAI`）、pivot 转响应（`FromOpenAI`）、路径映射（`PivotPath`）、OpenAI SSE 转协议流的包装（`WrapOpenAIStream`）。

```text
客户端协议 --ToOpenAI--> OpenAI pivot --TransformRequest--> 上游格式
上游格式 --TransformResponse--> OpenAI pivot --FromOpenAI--> 客户端协议
```

`ComposeForwardAdapter` 把一个下游 segment 与一个上游 `ForwardAdapter` 配起来；上游适配器保留自己的 URL 构造、认证头与流重塑，所以**不需要 N×M 转换矩阵**：

| 客户端协议 | 上游平台 | 适配器 |
| --- | --- | --- |
| OpenAI | 任意 | 上游适配器不变 |
| Anthropic（`/v1/messages`） | Anthropic 原生 | 原样透传（`messages` 路径） |
| Anthropic（`/v1/messages`） | OpenAI / Gemini | `ComposeForwardAdapter{AnthropicDownstreamSegment, upstream}` |

当 `DownstreamProtocol=anthropic` 遇到非 Anthropic 渠道时，`proxy` 会自动组合。组合适配器上的一个 `OnOpenAI` 钩子在 pivot 步骤与上游转换之间运行（用于被翻译的请求上的系统提示词注入）。

**加一个新的客户端协议 = 一个 `SegmentConverter`；加一个新的上游平台仍然只是一个 `ForwardAdapter`。**
