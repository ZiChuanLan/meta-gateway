# 竞品调研:sub2api / metapi / new-api / axonhub / all-api-hub

> 调研日期:2026-08-02(子代理实读代码,路径见各节;仅标注"README 宣称"的内容未逐行验证)
> 目的:为 meta-gateway 补齐能力缺口提供借鉴清单。**约束:不复制源码,只借鉴思路与设计。**

## 一、各项目定位速览

| 项目 | 技术栈 | 定位 | 核心卖点 |
| --- | --- | --- | --- |
| sub2api | Go + Gin + Ent + PostgreSQL + Redis + Vue3 | 订阅额度分发型聚合网关 | 账号级调度状态机、复合计费、支付/兑换/联盟全闭环 |
| metapi | Node.js + Fastify + Drizzle(SQLite/MySQL/PG)+ React | "中转站的中转站"元聚合层 | 智能路由引擎、13+ 平台适配器、多 key 健康管理 |
| new-api | Go + Gin + GORM + React | 经典分销网关(one-api 活跃分支) | Ability 二维索引路由、预扣费/退款、兑换码/订阅 |
| axonhub | Go + ent + GraphQL + React | 企业级 AI 网关 | 条件引擎路由、多策略负载均衡、分级定价、RBAC |
| all-api-hub | TypeScript + WXT 浏览器扩展 | 多站点资产管理客户端(非网关) | 跨站比价、自动签到、用量报表、客户端导出 |

## 二、meta-gateway 可借鉴清单(合并去重,按优先级)

### P0 —— 直接服务当前开发方向(非 OpenAI 适配器 + per-model 路由)

1. **条件驱动的模型路由注册表**(sub2api composite_model_route、axonhub ModelAssociation)
   - 机制:`public_model → 目标平台 + 上游模型`,支持 exact/prefix 匹配、endpoint 范围(messages/responses/chat_completions)、优先级、enabled 开关;axonhub 进一步支持 When 条件(stream、has_image、daily_time、request_header 等)与 6 种关联方式(渠道名/标签/正则/模型 ID)。
   - 借鉴:per-model 路由模式直接按此建模——route 表 + 解析器 + 请求体 model 改写 + 条件过滤。一处条件引擎(expr-lang 风格嵌套 AND/OR)供路由/定价/关联三处复用。
   - 佐证:sub2api `backend/ent/schema/composite_model_route.go`、`docs/COMPOSITE_GROUPS.md`;axonhub `internal/objects/condition.go`、`internal/objects/model.go`、`internal/objects/model.go`(ModelAssociationWhen)

2. **适配器级转换选项 TransformOptions**(axonhub)
   - 机制:per-channel 转换开关,如 ForceArrayInstructions、ForceArrayInputs、ReplaceDeveloperRoleWithSystem、ReasoningEffortMapping(自动把入站 effort 值映射为出站兼容值,xhigh→max 等,可 per-channel 覆盖)。
   - 借鉴:正是 Gemini/Anthropic 适配器的真实痛点(数组格式、role 名、reasoning_effort 值域差异)。adapter 层加 per-channel transform 配置,首期实现 reasoning_effort 映射与 role 替换。
   - 佐证:axonhub `internal/objects/channel.go`(TransformOptions)、`internal/server/biz/auto_reasoning_effort.go`(路径待核)

3. **失败分类 → 差异化重试 + 可配置重试条件**(metapi、axonhub)
   - 机制:metapi 把错误分为"model unsupported"(不可重试/需降级)与"protocol unsupported"(需格式转换),超时类单独列表;axonhub 支持自定义 RetryableStatusCodes + 错误文本正则/子串匹配。
   - 借鉴:重试判定函数先按错误分类;"模型不存在/格式不支持"直接标记该 channel 该模型不可用,避免白耗配额。当前写适配器时,上游模型类错误被误判重试是真实风险。
   - 佐证:metapi `src/server/services/tokenRouter.ts`(SITE_MODEL_FAILURE_PATTERNS / SITE_PROTOCOL_FAILURE_PATTERNS)、`proxyRetryPolicy.ts`;axonhub `internal/objects/channel.go`

4. **错误透传规则表(Error Passthrough Rule)**(sub2api)
   - 机制:按错误码 + 关键词(any/all 匹配)+ 平台范围,决定透传/改写状态码与错误体,可 skip_monitoring。
   - 借鉴:把 meta-gateway 硬编码的错误分类升级为可配置规则表,非 OpenAI 适配器上线初期快速压错,无需改代码。
   - 佐证:sub2api `backend/ent/schema/error_passthrough_rule.go`

5. **usage_log 三字段双写(requested / upstream / mapping_chain)**(sub2api)
   - 机制:记账同时写 `requested_model`、`upstream_model`、`model_mapping_chain`,支撑路由归因与排障。
   - 借鉴:适配器转发前记录 requested,上游响应后补写 upstream,一行日志贯穿整条映射链。
   - 佐证:sub2api `backend/ent/schema/usage_log.go`

6. **请求/响应改写操作(JSON-path body/header override)**(axonhub、new-api)
   - 机制:axonhub 9 种操作 set/set_if_absent/delete/rename/copy/array_append/prepend/insert/remove;new-api 的 ParamOverride/HeaderOverride 在发送前 JSON merge。
   - 借鉴:许多上游兼容问题靠改写解决而不是新建 adapter;对 Gemini/Anthropic 适配器尤其有用(自定义 endpoint 需要的额外 header 无需新建渠道类型)。
   - 佐证:axonhub `internal/objects/channel.go`(OverrideOperation/OverrideMatch);new-api `relay/common`

### P1 —— 路由质量与可观测性

7. **分级冷却 + 断路器**(metapi)
   - 机制:冷却递增 [0→10min→1h→24h],连续失败 3 次触发断路器 [0→1min→5min→30min],成功恢复递减。
   - 借鉴:meta-gateway 现冷却固定(30s),易误伤慢渠道;channel 表加 consecutiveFailCount/cooldownLevel/cooldownUntil,成功清零。
   - 佐证:metapi `src/server/services/tokenRouter.ts` 常量区

8. **粘性会话(thread/session sticky)**(sub2api、metapi、axonhub)
   - 机制:sessionHash/thread → 优先命中上次账号(sessionHash→accountID 存 Redis,TTL 过期回落负载均衡)。
   - 借鉴:多轮对话续聊被路由到另一渠道会丢上下文;内存 Map(sessionId→channelId,TTL 30s)+ 每 channel 活跃请求计数。
   - 佐证:sub2api `backend/internal/service/gateway_scheduling.go`(SelectAccountWithLoadAwareness);metapi `proxyChannelCoordinator.ts`(stickySessionBindings);axonhub `orchestrator/load-balancing.md`

9. **多 key 轮换 + 失败 key 自动剔除**(new-api、axonhub)
   - 机制:单渠道多 API key(random/polling 模式),失败 key 记录(errorCode/reason)临时禁用,全挂才禁渠道。
   - 借鉴:一渠道一凭据下 key 级故障会整渠道失效;channel 表加 api_keys(JSON)+ disabled_api_keys,401 时自动标记。
   - 佐证:new-api `model/channel.go`(IsMultiKey/MultiKeyStatusList);axonhub `internal/objects/channel.go`(APIKeys/DisabledAPIKey)

10. **渠道自动测速 + 禁用/恢复闭环**(new-api)
    - 机制:定时/手动全渠道测试 → 按错误类型或超时阈值自动禁用 → passive-recovery 模式只测被禁渠道并自动启用;响应时间入库。
    - 借鉴:meta-gateway 有冷却但无主动探活;channel 加 test_time/response_time 字段,后台 goroutine 定时跑。第一步只做"自动禁用",恢复后续加。
    - 佐证:new-api `controller/channel-test.go`、`service/*`(ShouldDisableChannel)

11. **优先级 + 权重双维路由**(new-api)
    - 机制:同组同模型渠道按 priority 降序,第 N 次重试切到第 N 档优先级;同档内按 weight 加权随机(weight=0 给基准 100)。
    - 借鉴:meta-gateway 重试无"重试自动降优先级档"语义;channel 表加 priority+weight,重试循环按 (model, retry) 取集合加权选一个。
    - 佐证:new-api `model/channel_cache.go`(GetRandomSatisfiedChannel)

12. **渠道级 RPM/TPM/MaxConcurrent 限流**(axonhub)
    - 机制:负载均衡跳过超限渠道;并发有软/硬队列(QueueSize+QueueTimeoutMs)。
    - 借鉴:比固定冷却更贴近上游真实限制,防 429;内存 token bucket 每渠道一份,路由前检查。
    - 佐证:axonhub `internal/objects/channel.go`(ChannelRateLimit)

13. **分级定价引擎 + cache token 计费**(axonhub、sub2api、new-api)
    - 机制:4 种模式 flat_fee / usage_per_unit / usage_tiered(分段)/ usage_volume(档位统一价),cache read/write 单价、时间窗价格覆盖;sub2api 价格与 LiteLLM 一致,支持 priority/flex 层级与长上下文倍率。
    - 借鉴:meta-gateway"简单估算成本"无法支撑"选最便宜渠道"与精确配额;SQLite 存 per-model 价目表(字段:input/output/cacheRead/cacheWrite + 模式),用量入库时按模式计算。
    - 佐证:axonhub `internal/objects/price.go`、`internal/server/biz/cost_calc.go`;sub2api `backend/internal/service/billing_service.go`;new-api `model/pricing.go`

14. **令牌增强:过期时间 + 模型白名单 + IP 白名单**(new-api)
    - 机制:ExpiredTime(-1 永不过期)、ModelLimitsEnabled/ModelLimits、AllowIps。
    - 借鉴:meta-gateway 已有配额字段,加三个字段 + 请求时校验即纯增量。
    - 佐证:new-api `model/token.go`(ValidateUserToken)

15. **成本信号四级 + 每百万单价成本明细**(metapi)
    - 机制:实测成本 → 账号配置 → 目录参考价 → 兜底;billingDetails 拆 input/output/cacheRead 每百万成本,随日志落库。
    - 借鉴:本地 model_pricing 目录表(per-million 单价,支持上游拉取 + 手动覆盖),估算函数产出明细 JSON 存审计日志。
    - 佐证:metapi `src/server/services/modelPricingService.ts`、`proxyBilling.ts`

16. **首字节延迟 + 客户端识别审计字段**(metapi)
    - 机制:proxy_logs 含 firstByteLatencyMs、clientFamily/clientAppId、modelRequested/modelActual、retryCount。
    - 借鉴:排查"谁慢/哪个客户端异常"最有用;SSE 场景在首 chunk 打点。
    - 佐证:metapi `src/server/db/schema.ts`(proxy_logs)

17. **路由决策快照(透明可审计)**(metapi)
    - 机制:每次路由选择把 {候选列表, 打分, 命中策略} JSON 存 decision_snapshot。
    - 借鉴:per-model 路由开发中,用户需要知道"为什么走这个渠道"。
    - 佐证:metapi `src/server/db/schema.ts`(decision_snapshot)、`services/tokenRouter.ts`

18. **逐 token 模型可用性探测**(metapi)
    - 机制:model_availability / token_model_availability 按 token 记录 available/latencyMs,isManual 人工覆盖,后台定时探测。
    - 借鉴:meta-gateway 的 Discovery 只列模型,无法回答"这个 key 能不能跑 gpt-4o";复用 Discovery 巡检落表 (token_id, model, available, latency_ms)。
    - 佐证:metapi `src/server/services/runtimeModelProbe.ts`、`modelAvailabilityProbeService.ts`

19. **请求日志后台筛选表格**(new-api)
    - 机制:Log 表含 model_name/token_name/channel/group/ip/request_id/upstream_request_id/use_time,多维筛选;按小时聚合 QuotaData 供看板;模型排行榜。
    - 借鉴:meta-gateway 有审计日志 + Prometheus,补一个带筛选的 Web 页面即达成"请求日志可视化",无需 ClickHouse。
    - 佐证:new-api `model/log.go`、`model/usedata.go`、web usage-logs 筛选工具栏

20. **用量预聚合投影**(metapi)
    - 机制:site_day_usage / site_hour_usage / model_day_usage 预聚合表 + watermark/lease/recompute checkpoint,避免大表扫描。
    - 借鉴:Web Admin 图表查询量大会拖垮 SQLite;日志落库后异步累加聚合表,查询只走聚合表。
    - 佐证:metapi `src/server/services/usageAggregationService.ts`

21. **告警通知系统**(metapi)
    - 机制:Webhook/Bark/Server酱/Telegram/SMTP 五渠道 + alertRules(余额不足/签到失败/代理失败/Token 过期/每日摘要)+ 300s 节流;events 表统一事件流。
    - 借鉴:渠道挂掉/余额不足不能只靠人工看页面;先做 Webhook + 告警规则表 + 事件去重。
    - 佐证:metapi `src/server/services/notifyService.ts`、`alertRules.ts`(部分为 README 宣称 + 模块存在性)

22. **模型卡片元数据(ModelCard)**(axonhub)
    - 机制:模型的能力/成本/上下文限制/知识截止/模态(输入输出 text/image/video)元数据。
    - 借鉴:让"模型发现"从名字列表升级为可路由决策的能力地图(vision 请求只去有 vision 的渠道)。
    - 佐证:axonhub `internal/objects/model.go`

23. **渠道 Tag 批量运维**(new-api)
    - 机制:Channel.Tag 字段,Enable/Disable/EditChannelByTag 一键启停/改映射整个标签组。
    - 借鉴:channels 表加 tag,Web 页批量勾选、按 tag 搜索。
    - 佐证:new-api `model/channel.go`

24. **渠道健康监控升级(jitter + 工作池 + 历史/日聚合)**(sub2api)
    - 机制:monitor 独立 goroutine + ticker(interval±jitter 随机抖动防同步)+ pond 工作池 + 历史表/每日聚合 + SSRF 防护 + 自定义请求模板快照。
    - 借鉴:meta-gateway check-in 是固定定时任务,加抖动、并发池、history/daily_rollup 表。
    - 佐证:sub2api `backend/ent/schema/channel_monitor.go`、`channel_monitor_runner.go`

25. **多窗口美元限速(5h/1d/7d)**(sub2api)
    - 机制:API key 按窗口起点滚动计数 usage_5h/1d/7d,Redis 计数缓存 + 脏集批量回写。
    - 借鉴:在现有 token 配额之外加时间窗成本上限。
    - 佐证:sub2api `backend/ent/schema/api_key.go`

### P2 —— 锦上添花 / 视场景

26. **兑换码 + 每日签到**(new-api、sub2api)
    - new-api redemption.go 用 FOR UPDATE 事务防并发抢兑;checkin 每日随机额度 + 唯一索引。sub2api redeem_code 支持过期时间、绑定 group。
    - 借鉴:meta-gateway 是"按 token 配额"模型,兑换码直接给 token 加配额即可落地,**不需要用户体系**;签到实现极简。
27. **幂等记录**(sub2api)
    - 客户端重试场景防重复计费/重复转发。
28. **leader lock + 定时维护任务**(sub2api)
    - 为在线备份/用量清理提供单实例保证(现备份若多实例部署会互相干扰)。
29. **每站点多 endpoint + 代理出口**(metapi)
    - site_api_endpoints(各自 cooldown/lastSelected/lastFailed)、proxyUrl/customHeaders/SOCKS。
    - 上游站点常有备用域名;国内部署需要代理出口。
30. **智能站点识别**(all-api-hub)
    - 添加渠道时自动探测上游平台类型/兼容格式/计费比例并回填,减少人工配置错误。
31. **跨渠道比价面板 + 健康总览**(all-api-hub 思路,gateway 天然有数据)
    - 展示各渠道同模型折合单价、标出最划算组合;按渠道/模型/日期聚合的用量报表与热力图。
32. **代理调试快照模式**(metapi)
    - proxy_debug_traces/attempts 开关打开时录制请求/响应/每次尝试,问题工单必备。
33. **结构化操作审计(op/action + params)**(new-api)
    - 审计日志用稳定 action 标识 + 结构化参数而非自然语言,前端 i18n 渲染。
34. **缺失模型配置提示**(new-api)
    - 渠道声明模型与模型元数据差集暴露给管理员,补 diff 接口 + Web 展示。
35. **WebDAV 备份目标**(metapi)
    - meta-gateway 已有在线备份,补远程目标(注意:meta-gateway 的 Store 已有 WebDAV 同步能力,需评估复用)。
36. **敏感 prompt 保护 / 上游额度轮询**(axonhub)
    - 敏感内容不发给指定渠道;provider quota 用独立凭据轮询。

## 三、明确不借鉴(定位不符)

- **用户体系 / 钱包 / 支付 / 订阅**(new-api、sub2api):整个用户域,除非产品定位转向分销网关。metapi 作为同类"元聚合层"也没有这些,印证轻量定位可行。
- **异步任务型渠道**(new-api relay_task / sub2api batch_image):仅接视频/MJ 类上游才需要,现阶段不做。
- **ClickHouse 日志存储**:日志量到千万级再考虑,先以 SQLite 索引 + 定时清理兜底。
- **多实例系统任务队列 / 实例面板**:单实例部署下无必要。
- **RBAC / 多租户**(axonhub):meta-gateway 无用户体系,跳过。

## 四、grokbuild 调研交叉验证与增量合并(2026-08-02)

grokbuild subagents 产出 12 条精选清单,与本报告(4 个 Explore 代理)重叠度约 50%。已对其中我方缺失/弱化的条目逐条回源码验证,结果如下(均真实存在,附验证路径):

| # | grokbuild 条目 | 验证结果 | 验证路径 | 处理 |
| --- | --- | --- | --- | --- |
| 1 | 粘性会话路由 | ✅ 与清单 #8 重合 | — | 已收录(★) |
| 2 | EWMA 健康度 + 时延感知选路 | ✅ 成立(注意:源码无 "EWMA" 字样,实际为 TTFT 权重 + 负载因子 + 延迟感知策略) | sub2api `backend/internal/service/domain_constants.go:487`(SettingKeyOpenAIAdvancedSchedulerWeightTTFT)、`gateway_scheduling.go:100`(SelectAccountWithLoadAwareness)、`account.go:37`(LoadFactor);axonhub `internal/server/orchestrator/load-balancing.md`(错误/延迟/限流感知策略) | **新增 → 下方 #A** |
| 3 | 渠道自动禁用 + 状态码/关键词规则 + 通知 | ✅ 与清单 #10/#24 重合 | — | 已收录(★) |
| 4 | 上游配额探测器 | ✅ 成立 | axonhub `internal/server/biz/provider_quota_service.go`(后台配额轮询)、`conf/conf.go:294-295`(check_interval=5m、warning 间隔=4×)、`frontend/src/components/quota-badges.tsx`(Claude/Codex/Copilot/NanoGPT 配额徽章)、`biz/channel_auto_disable.go` | **新增 → 下方 #B** |
| 5 | 预扣→结算→退款计费会话 | ✅ 与清单 P1 配额增强重合 | new-api `controller/relay.go`(PreConsumeBilling/Refund) | 已收录(★) |
| 6 | 多通道通知 | ✅ 与清单 #21 重合 | — | 已收录(★) |
| 7 | Pool 模式(API-key 渠道 401/403/429 同账号重试) | ✅ 成立 | sub2api `backend/internal/service/account.go:1026`(IsPoolMode)、`account_pool_retry_status_codes_test.go`(默认 [429,401,403],可配)、`gateway_forward.go:698`(RetryableOnSameAccount) | **新增 → 下方 #C** |
| 8 | 配额时间窗(定点时区重置、5h 会话窗) | ✅ 与清单 #25 重合,补充定点重置细节 | sub2api `backend/ent/schema/api_key.go`(usage_5h/1d/7d + window_start) | 已收录(★) |
| 9 | WebDAV 双向同步 + 墓碑 | ✅ 成立(墓碑=删除标记参与 merge,防旧备份复活已删项) | all-api-hub `docs/docs/en/webdav-sync.md:45`("deletion markers … participate in the merge")、CHANGELOG #849 | **合并 → 清单 #35** |
| 10 | 路由决策快照 | ✅ 与清单 #17 重合 | — | 已收录(★) |
| 11 | DB 租约任务框架 | ✅ 与清单 #28 重合 | new-api `main.go:145`(DB-lease dedup)、`model.SystemTask` + `model.SystemTaskLock` | 已收录(★) |
| 12 | 版本化价格快照 | ⚠️ 机制存在(ChannelModelPrice 实体),"版本化"细节未实读 | axonhub `internal/ent/channelmodelprice.go` | **新增 → 下方 #D** |

### 增量条目(合并进主清单)

**#A — 健康度打分选路(时延感知 + 负载因子)[P1]**
sub2api 调度器按 TTFT 权重(advanced scheduler weight)与 load_factor 对候选账号打分,axonhub 有错误感知/延迟感知/限流感知三种负载均衡策略可组合。落地:在 priority/weight/cooldown 之上加"错误率 + 首字节延迟"的移动平均打分(注意:两项目源码均无字面 EWMA,实现时自选指数/滑动窗口),429 响应尊重 Retry-After 头。

**#B — 上游配额探测器(耗尽绕行)[P1]**
axonhub provider_quota 用独立凭据轮询上游配额仪表盘(Claude/Codex/GitHub Copilot/NanoGPT 等),配额告急时 UI 徽章提示、channel_auto_disable 联动禁用。meta-gateway 的 discovery/checkin 是"拉模型/签到",可加"配额轮询 + 耗尽自动标记"。

**#C — Pool 模式(API-key 同账号重试)[P1]**
sub2api 的 API-key 渠道可在凭据标记 `pool_mode: true`,配合 `pool_mode_retry_count`(默认 3,上限 10)与 `pool_mode_retry_status_codes`(默认 [429,401,403])实现"上游账号内重试",与跨渠道 failover 正交。落地:meta-gateway 渠道凭据加 pool_mode 配置,重试循环区分"同渠道重试"与"切渠道"。

**#D — 版本化价格快照[P2]**
计费时引用不可变价格版本(axonhub ChannelModelPrice),审计友好;落地为价目表更新时生成版本号,usage 记录引用版本。

## 六、深度复审(第 2 轮)关键增量 — 2026-08-02

第 2 轮 4 个代理无轮次限制,精读源码(约 150 个文件),完整报告落盘于 `.trellis/tasks/08-02-gemini-adapters/research/`(sub2api / metapi / newapi / axonhub-allapihub 各一份,共 1371 行)。以下为与 meta-gateway 直接相关的增量:

### 6.1 重要勘误(影响原清单判断)

1. **new-api 适配器不是注册表** — `GetAdaptor(apiType)` 是编译期 switch(`relay/channel/adapter.go:15` 接口 15 方法,`relay_adaptor.go:54` 分发)。meta-gateway 的 ForwardAdapter + ResolveForward(map 注册 + openai 默认回退)反而是**更优设计**,方向正确,无需回头。可借鉴的只是 `DoResponse` 返回 usage 打通结算的形态。
2. **sub2api 的 Anthropic 流式 usage 已完整提取** — `parseSSEUsagePassthrough`(message_start/message_delta/5m-1h 明细)。meta-gateway 任务 R2b "Anthropic usage 计量存疑"的核查结论应修正为"提取逻辑存在,需对照 meta-gateway 自己的写库路径"。
3. **metapi 分级冷却实为 round_robin 专用**;weighted 策略用 fibonacci backoff;alertRules.ts 实为错误分类工具而非告警规则表(名不符实)。
4. **axonhub 默认负载均衡链是 4 策略非 composite**;load-balancing.md 文档路径已过时;Bedrock 凭据支持无直接证据[INFERENCE]。
5. **all-api-hub 无 "tombstone" 字样**,实际机制为 `deletedEntryRecords`(newest-wins 合并)。

### 6.2 可直接抄的算法细节(meta-gateway routing/usage 升级素材)

1. **sub2api 双枢轴协议转换链** — 以 "Anthropic Messages + Responses" 为中间格式,把 N×M 协议矩阵变 N+M 转换链(`apicompat` 包)。meta-gateway 现有 OpenAI⇄Gemini、OpenAI⇄Anthropic 两条独立链;引入中间格式后新协议只写一段转换。另含:强制流式缓冲回退、429 分层冷却(PST 午夜/tier)、流式 scanner goroutine + 16 缓冲通道 + 双定时器(keepalive/间隔超时)+ 客户端断开后 drain for usage。
2. **OpenAI Advanced Scheduler 评分公式**(sub2api):`score = Σ W×factor`,10 个可配权重(priority/load/queue/error_rate/ttft/reset/quota_headroom/upstream_cost)+ Top-K 最小堆 + xorshift64 确定性加权抽样 + **EWMA 运行时统计(alpha=0.2)** + 粘性逃生。
3. **axonhub LB 评分公式**:ErrorAware `200-30n·decay-40·decay`(5min 线性衰减)、WeightRR `150·exp(-count/150)` 权重归一化 + 不活跃衰减、Latency 流式 `0.7·FTTL+0.3·TPS`、RateLimit 并发双区间[30,100]单调、耗尽统一 -10000 主导分、**选择时即计数防突发集中**。
4. **metapi 分层加权路由**:valueScores = cost×1/cost + balance×bal + usage×1/usage → 归一化 → (weight+10)×(0.5+0.5×norm) → 乘站点权重×运行时健康×历史健康×会话负载 → 轮盘赌;stable_first 主/观察池灰度(24:1)。
5. **usage 解析器(sub2api/metapi)**:40+ 字段 BFS 候选收集 + 评分选优 + SSE 解析 + self-log 兜底匹配窗口——与 Gemini/Anthropic usage 提取需求(R2/R2b)同构,直接可作实现参考。
6. **tiered_expr 计费表达式**(new-api `pkg/billingexpr`):expr-lang + SHA-256 编译缓存 + AST 内省 usedVars + `|||` 请求规则。
7. **兑换五道防线**(sub2api):每小时 20 次限流 → Redis 锁(10s TTL)→ DB 事务 → `WHERE status='unused'` 乐观锁 → 事务内权益发放;余额充值 = 生成兑换码 + RedeemService 复用幂等。
8. **支付 LoadBalancer**(sub2api):含 PENDING 的日用量/限额过滤/round-robin|least-amount;15 态订单状态机;fulfillment lease 版本乐观锁。
9. **条件路由引擎**(axonhub):JSON AST → expr-lang 编译(sync.Map 缓存);字段含 prompt_tokens(CJK/1.5+其他/4 启发式)、request_format、has_image/video/document/audio、request_header.X、daily_time;两阶段:静态关联缓存(5min TTL+4 失效键)+ 请求级 When 过滤。
10. **模型合并优先级**(axonhub):Model 级 associations 优先,渠道模型是 FallbackToChannelsOnModelNotFound 回退层。

### 6.3 第 2 轮新增 P0 建议(并入第 2 节)

- **协议转换中间格式**(sub2api 双枢轴):适配器层重构方向,建议写进 design.md 的后续演进。
- **Retry-After 冷却**(axonhub):429 响应尊重 Retry-After 头,比固定冷却精确。
- **选择时计数防突发**(axonhub):选路同时占计数,防多请求同时选中同一渠道。
- **稳定灰度池 stable_first**(metapi):主/观察池 24:1 灰度,路由新渠道先小额引流。

### 6.4 报告索引

- `.trellis/tasks/08-02-gemini-adapters/research/sub2api-deep-review.md`(431 行:流式/支付/兑换/前端/failover/调度算法)
- `.trellis/tasks/08-02-gemini-adapters/research/metapi-deep-review.md`(275 行:路由算法/usage 解析器/OAuth/通知)
- `.trellis/tasks/08-02-gemini-adapters/research/newapi-deep-review.md`(407 行:适配器 switch/计费/tiered_expr/Gemini 切片细节)
- `.trellis/tasks/08-02-gemini-adapters/research/axonhub-allapihub-deep-review.md`(258 行:LB 公式/条件路由/模型合并/all-api-hub features)

## 五、证据索引(可深读)

- sub2api:`backend/ent/schema/`(account/group/composite_model_route/api_key/usage_log/subscription_plan/channel_monitor/error_passthrough_rule/redeem_code/user_platform_quota)、`backend/internal/service/gateway_scheduling.go`、`channel_monitor_runner.go`、`billing_service.go`、`backend/internal/handler/failover_loop.go`、`docs/COMPOSITE_GROUPS.md`
- metapi:`src/server/db/schema.ts`、`src/server/services/tokenRouter.ts`(常量区)、`routeRoutingStrategy.ts`、`proxyChannelCoordinator.ts`、`proxyChannelRetry.ts`、`modelPricingService.ts`、`proxyDebugTraceStore.ts`、`notifyService.ts`、`services/platforms/*`
- new-api:`model/ability.go`、`model/channel_cache.go`、`model/channel.go`、`model/token.go`、`model/pricing.go`、`model/redemption.go`、`model/log.go`、`controller/relay.go`、`controller/channel-test.go`、`model/missing_models.go`
- axonhub:`internal/objects/condition.go`、`model.go`、`price.go`、`channel.go`、`cost.go`、`internal/server/orchestrator/load-balancing.md`、`internal/objects/channel.go`(OverrideOperation/TransformOptions)
- all-api-hub:`src/features/`(ModelList/UsageAnalytics/AutoCheckin/ImportExport 等,目录级证据)
