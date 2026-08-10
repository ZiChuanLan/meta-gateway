# Metapi 功能再盘点 — meta-gateway 尚未具备的功能（11-metapi-recheck）

> 调研方法：实读 `H:/WorkSpace/api/metapi` 源码（src/server/services、src/server/routes/proxy、src/web/pages、docs/）+ 复用既有调研 `01-metapi.md`；对照基准为 meta-gateway 实际落地状态（README + `research/08-gap-check.md` 的 ✅/⚠️/❌ 逐项复核）。metapi 有而 meta-gateway 没有（或只有半成品）的功能如下，均标注证据文件。

## 一、[高价值] — 与 meta-gateway 核心定位直接相关，建议优先

### 1. 成本感知概率路由（四级成本信号链 + 40/30/30 加权分摊）
- **一句话**：路由选择时综合"实测成本→账号配置成本→目录参考价→默认兜底"四级成本信号，按成本 40%/余额 30%/使用率 30% 加权算分选通道。
- **证据**：`src/server/services/tokenRouter.ts` `resolveEffectiveUnitCost()`（L1574+）、`calculateWeightedSelection()`（L3538+，`valueScore = costWeight*(1/unitCost) + balanceWeight*balance + usageWeight*(1/recentUsage)`）；权重在 `config.ts` 可配。
- **为什么有价值**：meta-gateway 已有 priority+weight、latency/error 打分（routing.go）、FinanceOverview 余额/单价数据（`internal/account/service.go`），但 08 复核 #15 明确"路由决策不参考成本"，无 observed→configured→catalog→fallback 链。数据已齐，缺的是把 cost 接进 `routing.Select` 的权重公式——改动集中、收益直观（个人用户最在意"最便宜通道"）。

### 2. 路由决策快照持久化（可审计的"为什么走这个渠道"）
- **一句话**：每次路由决策的候选列表/选中结果/刷新时间持久化到 `token_routes.decision_snapshot`，并有定时刷新服务。
- **证据**：`db/schema.ts` L154 `decisionSnapshot`；`db/routeGroupingSchemaCompatibility.ts`（三库迁移）；`services/routeDecisionSnapshotStore.ts`、`routeDecisionRefreshService.ts`。
- **为什么有价值**：08 复核 #17 标注 meta-gateway 只有 live `/console/routes/explain`，决策快照不落库，事后无法审计。metapi 是直接把快照挂在路由表上（JSON 列 + 刷新时间戳），实现成本低，直接对应已识别的 #17 缺口。

### 3. 用量预聚合投影（定时聚合 + 重算 + 调度器）
- **一句话**：代理日志→按站点/账号/模型/时段投影聚合表，调度器定时跑、支持从指定 logId 重算。
- **证据**：`services/usageAggregationService.ts`（`runUsageAggregationProjectionPass`、`requestUsageAggregatesRecompute(fromLogId)`、`startUsageAggregationProjectorScheduler`，projector key `usage-aggregates-v1`）。
- **为什么有价值**：08 复核 #20 是 ❌（meta-gateway 图表直接 `SUM(usage_records)`，无聚合表）。metapi 的实现正好是"SQLite 友好"的——写路径累加 + 定时投影 + watermark 重算，可缓解 05/06 报告指出的 SQLite 热路径写压力（P1-1）与看板查询压力。

### 4. 系统代理出口（全局 + 每站点 + 单次请求）
- **一句话**：支持 `SYSTEM_PROXY_URL` 全局代理、站点级 `useSystemProxy` 开关、以及 OAuth 等场景的单次代理参数。
- **证据**：`config.ts` L78 `systemProxyUrl: env.SYSTEM_PROXY_URL`；`contracts/siteRoutePayloads.ts` `useSystemProxy`；`contracts/settingsRoutePayloads.ts` `systemProxyTestPayloadSchema`；`docs/oauth.md`"远程部署/国内服务器"章节。
- **为什么有价值**：08 复核 #29 明确 meta-gateway"环境代理变量禁用、无 proxyUrl 出口"（❌）。对国内个人/小团队用户，无法直连 OpenAI/Anthropic/Google 是真实痛点。注意安全敏感（需与 outbound SSRF/DNS 校验顺序兼容），建议列为独立任务而非顺手加。

## 二、[可做] — 单机个人场景有明确价值，工作量可控

### 5. `/v1/files` + `/v1/images` 接口面
- **一句话**：标准文件接口（POST/GET/DELETE `/v1/files`、`/v1/files/:id/content`）+ 图片生成/编辑（`/v1/images/generations`、`edits`，variations 显式返回 not-supported）。
- **证据**：`routes/proxy-core/surfaces/filesSurface.ts`（5 个路由）；`routes/proxy/images.ts`；配套 `services/proxyFileStore.ts`、`proxyFileRetentionService.ts`。
- **为什么有价值**：meta-gateway 目前只覆盖 models/chat/completions/embeddings/responses/messages；下游 Cursor/Open WebUI 等工具的多模态/文件上传场景会直接缺接口。实现是透传 + 本地临时存储 + 保留期清理，不算重。

### 6. 余额/收入追踪体系（定时刷新 + 今日收入 + 日志兜底估算）
- **一句话**：定时批量刷新余额；从上游 `/api/log/self` 分页拉今日收入日志换算收入；余额/用量接口不可用时用代理日志做时间窗+模型+token 四重匹配兜底估算；余额查询失败触发自动重登。
- **证据**：`services/balanceService.ts`（autoRelogin 测试存在）、`todayIncomeRewardService.ts`（`getTodayIncomeDelta`/`updateTodayIncomeSnapshot`/`estimateRewardWithTodayIncomeFallback`）、`proxyUsageFallbackService.ts`（self-log 恢复 usage，±90s/延迟差≤12s/模型/token 四重匹配）；01 调研确认换算系数写死 500_000。
- **为什么有价值**：meta-gateway 有 FinanceOverview 余额探测（✅），但没有"收入趋势"、"余额兜底估算"、"余额查询失败自动重登"。对中转站用户"今天赚了多少/还剩多少"是核心诉求；兜底估算逻辑可直接借鉴（注意系数改为配置）。

### 7. 签到奖励解析与追踪
- **一句话**：签到成功后从返回文本智能解析奖励金额（正则提取数字/金额），配合今日收入快照做奖励估算兜底。
- **证据**：`services/checkinRewardParser.ts`（`parseCheckinRewardAmount`，数字/带金额文案正则）；`todayIncomeRewardService.ts` 的 `estimateRewardWithTodayIncomeFallback`。
- **为什么有价值**：meta-gateway 签到调度/防重入已落地（08 #26 ✅），但签到日志只记成败，不解析奖励。加一个纯函数解析器 + 日志列即可，成本极低，运营价值直观（每天签到领了多少）。

### 8. 账号级健康状态机（五态 + 站点级联禁用）
- **一句话**：账号运行时健康分 `healthy/unhealthy/degraded/unknown/disabled` 五态；禁用站点自动级联禁用其下全部账号。
- **证据**：`services/accountHealthService.ts` L9 `RuntimeHealthState`；01 调研确认 `sitesStatusSideEffects.ts` L34 `UPDATE accounts SET status='disabled' WHERE siteId=?`。
- **为什么有价值**：meta-gateway 有渠道级 AutoDisable/healthsweep（✅）和"全 key 挂级联禁渠道"，但没有"账号"这一层（一个站点多账号）的健康状态展示与级联。若 meta-gateway 引入"每站点多账号"概念，此状态机是现成参考；若保持单凭据模型则价值降低——标注为**视账号模型演进而定**。

### 9. 代理调试快照（开关式录制请求/响应/每次尝试）
- **一句话**：可开关录制代理请求的会话、候选通道、每次尝试的请求/响应，带保留期自动清理，用于排障。
- **证据**：`services/proxyDebugTraceStore.ts`（`startProxyDebugTraceSession`/`insertProxyDebugAttempt`/`updateProxyDebugTraceCandidates`/`deleteExpiredProxyDebugTraces`）、`proxyDebugTraceRuntime.ts`。
- **为什么有价值**：08 复核 #32 正是 ❌（meta-gateway 只有单次手动 try，无 debug trace 存储）。metapi 的"会话+候选+尝试"结构与 meta-gateway 的 retry/attempt 模型天然对应，可直接映射到 `proxy_logs.attempt` 体系。

### 10. 每站点多 endpoint（自动选择 + 失败记录）
- **一句话**：站点可配多个 API 端点，按失败/成功记录自动选择可用目标（`selectSiteApiEndpointTarget`/`recordSiteApiEndpointFailure/Success`），并有失败分类。
- **证据**：`services/siteApiEndpointService.ts`（含 `SiteApiEndpointRequestError`、`classifySiteApiEndpointFailure`）。
- **为什么有价值**：08 复核 #29 ❌（meta-gateway `Site` 单 BaseURL）。对自建站多入口/镜像站场景（如 one-api 多域名）有价值；实现是"端点列表 + 冷却/成功计数"，不重。

### 11. Payload 规则引擎（按模型改写请求体参数）
- **一句话**：按模型匹配（minimatch）对请求体做 default/override/filter 四类规则：设置默认参数、覆盖参数、过滤移除参数，支持嵌套 path 与数组下标。
- **证据**：`services/payloadRules.ts`（`applyPayloadRules`、`PayloadRulesConfig` 含 `default/defaultRaw/override/overrideRaw/filter`，`setPath`/`hasPath` 支持 `a.0.b` 路径）。
- **为什么有价值**：08 复核 #6 ⚠️（meta-gateway 只有 header override，无 body 改写）。metapi 的实现（纯函数 + 按模型规则表）正是 08 建议的"body_override 操作链"形态，且规避了 axonhub 9 种 JSON-path 操作的重型设计。

### 12. 下游 Key 级路由策略（限制可见站点/凭证/权重）
- **一句话**：每个下游 Key 可配 `supportedModels`、`allowedRouteIds`、`siteWeightMultipliers`、`excludedSiteIds`、`excludedCredentialRefs`，空时可选 denyAll。
- **证据**：`services/downstreamPolicyTypes.ts`（`DownstreamRoutingPolicy` 完整字段）。
- **为什么有价值**：meta-gateway 下游 Key 只有端点 scope（models/chat/...），无法做到"这个 Key 只能走某些站点/不能走某凭证/给某站加权"。对个人网关（给家人/朋友发受限 Key）是实用隔离能力；字段直接对应 meta-gateway 的 route/site/credential 模型。

### 13. 模型广场的轻量版：品牌分类 + 跨渠道比价排序
- **一句话**：模型目录按品牌分类（OpenAI/Anthropic/Google/DeepSeek...），同模型跨渠道展示定价与实测延迟/成功率并可排序。
- **证据**：`services/brandMatcher.ts`、`modelPricingService.ts`（site:account 定价缓存 10min）、`modelAnalysisService.ts`（7 天窗口 top10：调用/成功率/延迟/花费）、`upstreamModelDescriptionService.ts`；web 页 `Models.tsx`（marketplace 文案测试存在）。
- **为什么有价值**：08 复核 #31 ⚠️（meta-gateway 单渠道价格展示已有，跨渠道比价排序无）、#22 ❌（模型元数据无）。建议只做"轻量版"：复用 FinanceOverview + discovered_models 做同模型跨渠道价格排序与品牌标签，不做 metapi 的全量"广场"UI。

### 14. 模型可用性逐账号/逐 token 探测
- **一句话**：对每个账号/token 实测指定模型可用性与延迟，供路由排除不可用组合。
- **证据**：`services/modelAvailabilityProbeService.ts`、`runtimeModelProbe.ts`、`modelService.probeSiteModels`（延迟阈值判 unsupported，01 调研确认）。
- **为什么有价值**：08 复核 #18 ❌（"这个 key 能不能跑 gpt-4o"不可回答）。meta-gateway 已有 `/console/discovery/channels/{id}/probe` 单发探测，缺的是"持久化 + 路由时消费"；metapi 的探测+落库+排除链路是现成设计参考。

### 15. 全局搜索（站点/账号/模型/令牌）
- **一句话**：后台全局搜索框跨站点、账号、模型、令牌检索。
- **证据**：`src/web/apiSearch.ts`、`src/web/pages/` 相关入口。
- **为什么有价值**：纯增量小功能；meta-gateway 站点/渠道/路由多了以后缺一个统一搜索入口。SQLite LIKE 即可，成本低。

### 16. 工厂重置
- **一句话**：管理端一键清空全部数据回到初始状态（含确认流程）。
- **证据**：`services/factoryResetService.ts`、web `settings.factory-reset-modal` 测试。
- **为什么有价值**：个人网关换手/重配场景实用；meta-gateway 目前需手删 data 目录。低成本小功能。

### 17. Sub2API 托管认证与定时刷新（含 singleflight 防并发）
- **一句话**：对订阅制平台 Sub2API 提供托管登录态管理、定时刷新 token、singleflight 合并并发刷新。
- **证据**：`services/sub2apiManagedAuth.ts`、`sub2apiRefreshScheduler.ts`、`sub2apiRefreshSingleflight.ts`；平台适配器 `platforms/sub2api.ts`。
- **为什么有价值**：仅当用户使用 Sub2API 类订阅站时有价值；meta-gateway 目前无此适配器。作为"平台适配器扩展"的一部分做，单列价值有限。标注为**跟随适配器扩展**。

### 18. 站点公告轮询
- **一句话**：轮询上游站点公告（如 LinuxDo 类）并在后台展示。
- **证据**：`services/siteAnnouncementPollingService.ts`、`siteAnnouncementService.ts`、web `SiteAnnouncements.tsx`。
- **为什么有价值**：边缘功能；对中转站用户"站方维护通知"有一定用，但对 meta-gateway 属锦上添花。低优先。

## 三、[不适合] — 与 meta-gateway 单实例/SQLite/轻量定位冲突或过重

### 19. OAuth 连接（Codex / Claude / Gemini CLI / Antigravity）
- **一句话**：浏览器授权直连官方 provider 账号，自动建站、loopback 回调、手动回填 callback、重绑。
- **证据**：`docs/oauth.md`（完整流程 + 7 个管理 API）；`services/oauth/`；平台适配器 `platforms/codex.ts`/`claude.ts`/`geminiCli.ts`/`antigravity.ts`。
- **为什么不适合**：依赖各 provider 逆向 OAuth 端点 + 本机 loopback 回调 + 远程 SSH 隧道/手动回填体验，且 provider 端随时可能变更。对单机网关是持续性维护负担；且 meta-gateway 的 outbound 安全策略（禁私网/禁代理）与 OAuth 回调链冲突风险高。**若未来要做 Claude Code/Codex 官方登录，可作为独立大项评估，不建议并入常规路线。**

### 20. 多数据库支持（MySQL/PostgreSQL）
- **一句话**：metapi 数据层 Drizzle 同时支持 SQLite/MySQL/PostgreSQL。
- **证据**：`drizzle/`、`db/generated/mysql.bootstrap.sql`、`postgres.bootstrap.sql`、README 技术栈表。
- **为什么不适合**：meta-gateway 明确定位"嵌入式 SQLite 零外部依赖"；引入 PG/MySQL 破坏单二进制部署哲学，且 08 复核 #28 连多实例 lease 都未做，多库支持无意义。

### 21. 桌面安装包（Electron）
- **一句话**：metapi 提供 Win/macOS 桌面安装包（electron-builder.yml、tsconfig.desktop.json）。
- **为什么不适合**：meta-gateway 是 Go 单二进制 + 内嵌 WebUI，`server.exe` 双击即用已覆盖"桌面体验"；引入 Electron 与零依赖哲学冲突。若确有桌面诉求，打包 WebUI 到系统托盘即可，不需要 Electron。

### 22. 更新中心（k3s 更新/部署运维设施）
- **一句话**：面向托管部署（k3s）的更新中心：版本检查、部署守卫、轮询、任务状态（约 10 个 updateCenter* 服务）。
- **证据**：`services/updateCenter*`（`updateCenterVersionService`/`updateCenterDeployGuardService`/`updateCenterPollingService` 等）、`docs/k3s-update-center.md`。
- **为什么不适合**：面向"运营一批 k3s 节点"的重型运维设施，与单实例个人网关定位完全不符。

### 23. 视频任务存储（视频生成模型）
- **一句话**：为视频生成类模型提供任务存储与状态管理（`proxyVideoTaskStore.ts`）。
- **为什么不适合**：依赖上游视频生成接口形态，场景小众；meta-gateway 连 images 都未做，视频更应后置。若未来做，也应等 files/images 落地后再评估。

### 24. 外部监控面板内嵌（Monitors）
- **一句话**：后台内嵌外部站点可用性监控页面（LinuxDo 等 iframe）。
- **证据**：`src/web/pages/Monitors.tsx`（"监控内嵌"、"在 metapi 内查看外部站点监控页面"）。
- **为什么不适合**：这是 metapi 面向其社区站点生态的定制功能，对 meta-gateway 无通用价值。

## 四、已排除项（metapi 有、但 meta-gateway 已具备或等价，无需再做）

| metapi 功能 | meta-gateway 现状 | 依据 |
| --- | --- | --- |
| 告警 5 通道 + 冷却 + 每日摘要 | ✅ 已落地（webhook/bark/serverchan/telegram/smtp + 日摘要 + 冷却） | 08 #21 |
| 定时签到 + 并发锁防重入 | ✅ 已落地（cron + acquire 防重入 + catch-up） | 08 #26 |
| 失败通道冷却/自动重试/通道协调 | ✅ 分级冷却 + 断路器 + 重试 | 08 #7/#10 |
| 粘性会话/灰度池/健康度打分选路 | ✅ 已落地 | 08 #8/#A/stable_first |
| 路由决策实时解释 | ✅ `/console/routes/explain`（缺的只是持久化，见 #2） | 08 #17 |
| 协议转换（OpenAI⇄Claude SSE） | ✅ pivot 中间格式 | 08 第 2 轮 P0 |
| 智能站点识别 | ✅ sitedetect 四级链 | 08 #30 |
| 凭证加密存储/多 key 轮换 | ✅ crypto + key pool | 08 #9 |
| 模型发现（跨平台 list models） | ✅ discovery（openai/new-api/anthropic/gemini） | README |
| WebDAV 备份 / 在线 SQLite 备份 / 数据导入导出 | ✅ 已落地 | 08 #35 |
| 结构化审计 / 日志筛选表格 | ✅ 已落地 | 08 #33/#19 |
| 渠道 Tag 批量运维 | ✅ 已落地 | 08 #23 |
| 失败原因分类 | ✅ classifyForChannel | 08 #3 |
| 每账号多 Token 生命周期 | ≈ credentials + sync-keys（粒度略粗） | README |

## 五、结论与建议顺序

1. **立即可做（改动小、收益大）**：#1 成本路由（接 FinanceOverview 数据）、#2 决策快照持久化（路由表加 JSON 列）、#7 签到奖励解析（纯函数 + 日志列）。
2. **与已识别的 08 缺口直接重合**：#3 用量预聚合（=08 #20）、#9 调试快照（=08 #32）、#10 多 endpoint（=08 #29）、#11 body 改写（=08 #6 的 body 部分）、#14 逐 token 探测（=08 #18）——说明 metapi 是这些缺口的现成设计参考。
3. **独立新功能**：#5 files/images 接口、#6 余额收入追踪、#8 账号健康状态机（视账号模型演进）、#12 下游 Key 路由策略。
4. **明确不做**：OAuth 全家桶、多数据库、Electron 桌面、更新中心、视频任务、外部监控内嵌。

---

**证据路径汇总**：metapi 源码 `H:/WorkSpace/api/metapi/src/server/services/{tokenRouter,usageAggregationService,proxyDebugTraceStore,siteApiEndpointService,payloadRules,downstreamPolicyTypes,accountHealthService,checkinRewardParser,todayIncomeRewardService,balanceService,proxyUsageFallbackService,modelPricingService,modelAnalysisService,brandMatcher,modelAvailabilityProbeService,factoryResetService,sub2api*}.ts`、`src/server/routes/proxy-core/surfaces/filesSurface.ts`、`src/server/routes/proxy/images.ts`、`src/web/apiSearch.ts`、`docs/oauth.md`；meta-gateway 现状依据 `research/08-gap-check.md`（逐项源码复核）与 README。

**Gaps（未验证项）**：未逐一核实 metapi 的 tokenRouter 概率分摊在真实流量下的效果（仅源码确认）；`usageAggregationService` 的聚合表 schema 未展开细读；meta-gateway 侧我未重读 proxy/usage 代码，其现状描述全部引用 08 复核结论（该文件标注为源码级核实）。
