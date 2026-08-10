# 竞品清单落地状态复核(差距检查)

> 复核日期:2026-08(差距复核员,实读 meta-gateway 源码)
> 方法:逐条对照 `docs/competitive-review.md` 的 P0/P1/P2 清单(36 项)+ grokbuild 增量(#A-#D)+ 第 2 轮新增建议(6.3 共 4 项),在 `internal/` 各包与 `web/` 中 grep + 读码验证。
> 结论统计:**✅ 20 项(主清单 13 + 增量 7)/ ⚠️ 12 项 / ❌ 12 项**。所有结论来自实际读码;文档自称不作为证据。
> 附带发现 1 处"注释宣称与代码不符"(healthsweep semaphore)。

---

## 一、P0 清单(6 项)

| # | 清单项 | 状态 | 证据文件:行号 | 缺口描述 | 落地建议 |
|---|---|---|---|---|---|
| 1 | 条件驱动的模型路由注册表 | ⚠️ | 已实现:`internal/store/route.go:294-296`(exact+最长 wildcard 匹配)、`internal/routing/routing.go:60-380`(priority/weight/enabled/cooldown/excluded)、`internal/proxy/proxy.go:483-489`(mapping_json 改写 model)、`internal/domain/models.go:202-260`(Route/MappingJSON) | 缺条件引擎:无 When 条件(stream/has_image/daily_time/request_header)、无 endpoint 范围(messages/responses/chat_completions)、无 6 种关联方式。全仓库 grep 无 condition/When 实现 | `routes` 表加 `condition_json`;`routing.Select` 增加请求上下文过滤器,条件求值为纯函数包(可复用 expr-lang 思路),首期实现 has_image/stream 两条件 |
| 2 | 适配器级转换选项 TransformOptions | ❌ | `internal/domain/models.go:112-160`(Channel 无 transform_options 字段);`internal/proxy/proxy.go:131-133`(reasoning_effort 仅可观测性记录,不映射);`internal/adapters/anthropic.go:109`、`internal/adapters/gemini.go:177`(role 映射为硬编码) | 无 per-channel 转换开关:reasoning_effort 值域映射(xhigh→max)不存在;ForceArrayInstructions/ReplaceDeveloperRole 等不可配;xhigh 全仓库 grep 只出现在日志字段注释 | Channel 加 `transform_options` JSON;`ForwardAdapter.TransformRequest` 增加 options 参数(默认空=现行为);首期实现 reasoning_effort 映射与 role 替换,`internal/adapters` 各适配器各加一个映射函数 |
| 3 | 失败分类 → 差异化重试 + 可配置重试条件 | ⚠️ | 已实现:`internal/proxy/proxy.go:1130-1170`(classifyForChannel)、`internal/domain/models.go:163-200`(RetryConfig+ParseRetryConfig 预编译 regex)、`internal/proxy/proxy.go:1210-1240`(isRetryableForChannel:默认 429+5xx+528/529/530,叠加渠道 statusCodes/patterns)、`internal/store/027_channels_retry_config.sql` | 缺"模型不存在/格式不支持 → 标记该 channel×model 不可用,避免白耗配额":当前 4xx 只是不重试(proxy.go:1222 注释),不写状态 | 在 `classifyForChannel` 增加 model_not_found/protocol_unsupported 分类分支,命中时调用 `breaker.RecordError`(或新增 model 级黑名单),复用现有熔断接线点 |
| 4 | 错误透传规则表(Error Passthrough Rule) | ❌ | 全仓库 grep 无 passthrough_rule/error_rule;错误体透传为硬编码:`internal/proxy/proxy.go:1172-1205`(upstreamErrorText 读回 body 后原样还原) | 无可配置规则表(错误码+关键词 any/all+平台范围 → 透传/改写状态码与错误体,skip_monitoring);非 OpenAI 适配器压错只能改代码 | 新增 `error_passthrough_rules` 表 + store CRUD;proxy 错误路径查表(状态码+关键词匹配)决定透传/改写;最低成本:表 + 一个纯函数 `MatchPassthroughRule(platform,status,body)`,接入 classifyForChannel 之后 |
| 5 | usage_log 三字段双写(requested/upstream/mapping_chain) | ❌ | `internal/proxy/proxy.go:915-925`(recordAttempt 只写 `Model=req.Model` 请求模型);`internal/store/proxylog.go:37-45`、迁移 `025/026`(schema 无 upstream_model/mapping_chain 列);`internal/store/usage.go:20-40`(usage_records 亦无) | mapping_json 改写后的真实上游模型名不落库;排障/路由归因只能看到别名 | `proxy_logs`+`usage_records` 各加 `upstream_model` 列;`recordAttempt` 在 adapter.TransformRequest 后记录改写值(rewriteModelName 处取 real 值);mapping_chain 可用 route_pattern+upstream_model 两列覆盖首期诉求 |
| 6 | 请求/响应改写操作(JSON-path body/header override) | ⚠️ | 已实现 header:`internal/domain/models.go:122-126`(Channel.HeaderOverride)、`internal/proxy/proxy.go:1420-1450`(mergeHeaderOverrides,禁改 hop-by-hop) | 缺 body 改写:无 set/set_if_absent/delete/rename/copy/array_append 等 JSON-path 操作(axonhub 9 种);自定义 endpoint 场景只能建新渠道 | `internal/adapters` 或 `internal/proxy` 加 `body_override` 操作链(纯函数数组操作,JSON path 用现成库如 tidwall/gjson+sjson),在 TransformRequest 前应用;复用 HeaderOverride 的配置形态 |

## 二、P1 清单(19 项)

| # | 清单项 | 状态 | 证据文件:行号 | 缺口描述 | 落地建议 |
|---|---|---|---|---|---|
| 7 | 分级冷却 + 断路器 | ✅ | 分级冷却:`internal/store/route.go:63-70`(SetProgressiveCooldown)、`395-490`(tieredBackoff 4 档 + 成功逐级衰减)、`internal/store/023_progressive_cooldown.sql`;断路器:`internal/proxy/circuit_breaker.go` 全文(AxonHub 移植:closed/half-open/open、probe 5min、指数退避封顶 8×、TTL 30m、单次成功复位)、`internal/runtimeconfig/runtimeconfig.go:351-360`(热加载) | — | — |
| 8 | 粘性会话(thread/session sticky) | ✅ | `internal/routing/sticky.go:47-53`(TTL 30min 内存 Map)、`internal/routing/session.go` 全文(显式 header 优先,否则首条 user 消息内容摘要)、`internal/routing/routing.go:196-240`(SelectSticky+escape)、`internal/proxy/proxy.go:610-618`(成功后 Bind)、`internal/store/021_sticky_session.sql`、`proxy_logs.session_key` | — | — |
| 9 | 多 key 轮换 + 失败 key 自动剔除 | ✅ | key 池:`internal/proxy/proxy.go:690-740`(resolveAPIKeyPool:绑定凭据优先+站点级全部 api_key)、`internal/store/credential.go:100`;同 channel key 级 failover:proxy.go:495-590;失败 key 剔除:`proxy.go:1320-1350`(recordKeyFailure:channel×key×status 三元组计数,阈值 5,禁用 30min)、`recordKeySuccess` 同 channel 解禁;全挂级联禁渠道:`proxy.go:1355-1390`(cascadeChannelIfAllKeysDisabled) | 缺 new-api 的 random/polling 选择模式(当前为确定性顺序 failover);阈值 5 硬编码,无 pool_mode 配置标志(与 #C 同源) | 可选:`resolveAPIKeyPool` 增加随机起点轮换;阈值提到 RetryConfig 或 runtime settings |
| 10 | 渠道自动测速 + 禁用/恢复闭环 | ✅ | 自动禁用:`internal/proxy/proxy.go:740-770`(recordChannelFailure+AutoDisable+通知)、`internal/store/channel.go:340-360`(AutoDisable 清 cooldown)、`018` 迁移;被动恢复:`internal/discovery/discovery.go:84-96`(SetRecoveryConfig)、`329-397`(恢复探测循环)、`store/channel.go:370-378`(RecoverAutoDisabled)、`discovery.go:200-203`(ChannelRecovered webhook);健康探测:`internal/healthsweep/service.go` 全文、`store/channel.go:460-470`(last_probe_at/ok/error) | — | — |
| 11 | 优先级 + 权重双维路由 | ✅ | `internal/routing/routing.go:60-120`(优先级 tier 取最高→tier 内加权随机)、`pickWeighted` 全零回退 uniform、`RouteMember.Priority/Weight`(`internal/domain/models.go:230-250`)、`internal/store/020_routing_mode.sql`(auto/latency/weighted/adaptive) | — | — |
| 12 | 渠道级 RPM/TPM/MaxConcurrent 限流 | ⚠️ | 429-park:`internal/store/channel.go:437-455`(RecordRateLimited)、`internal/store/route.go:334`(SQL 排除 rate_limited_until)、`internal/account/service.go:166-176`(429 探测停车,尊重 Retry-After);并发防护:`internal/proxy/proxy.go:240-260`(inflight 计数)、`routing.go:280-330`(concurrencyFactor)、`028` 迁移 | 缺每渠道 RPM/TPM token bucket 与软/硬队列(axonhub ChannelRateLimit);现有仅"429 后停车"与"并发上限"两个被动/软性机制 | `channels` 表加 `rate_limit_json`(rpm/tpm/max_concurrent);proxy 路由前查内存 token bucket(每渠道一份),超限跳过该渠道 |
| 13 | 分级定价引擎 + cache token 计费 | ⚠️ | cache 计费:`internal/usage/usage.go:20-60`(cache_read/creation 提取)、`internal/store/022_usage_cache_tokens.sql`、`internal/proxy/proxy.go:1000-1015`(billingCost:cache 按 prompt 价) | 缺 flat_fee/usage_tiered/usage_volume 定价模式:仅 `ModelRatio` 倍率(`internal/store/usage.go:150-220`)+ key 级 per-1k 单价;cache write 无独立单价 | `model_ratios` 扩展为价目表(字段:input/output/cache_read/cache_write+模式),usage 入库按模式计算;保留 ratio 兼容 |
| 14 | 令牌增强(过期/模型白名单/IP 白名单) | ✅ | `internal/domain/models.go:270-300`(ExpiresAt/ModelAllowlist/ModelDenylist/AllowedIPs)、`internal/auth/auth.go:255-266`(校验)、`auth.go:363-367`(过期拒绝)、`auth.go:372`(IP 校验)、迁移 `014/016`、`internal/store/034_key_groups.sql`(组配额+限速) | — | — |
| 15 | 成本信号四级 + 每百万单价成本明细 | ⚠️ | 上游价格表:`internal/account/service.go:485-600`(FinanceOverview:balance+quota_per_unit+每模型 quota/1M 价格)、`web/src/features/Models.tsx:1383-1416`(每渠道模型单价+可调用次数展示) | 无 observed→configured→catalog→fallback 四级成本链;路由决策不参考成本;无 billingDetails JSON 明细落库(UsageRecord.Cost 只是金额标量,`store/usage.go:110-135`) | routing 增加 CostProvider(取 FinanceOverview 缓存);`UsageRecord` 加 `cost_detail_json` 列存每百万单价明细 |
| 16 | 首字节延迟 + 客户端识别审计字段 | ✅ | `internal/store/025_proxy_log_observability.sql`(first_byte_ms/client_family)、`internal/proxy/proxy.go:1290-1340`(peekFirstChunk 打点 FirstByteMs)、`internal/httpapi/relay.go:564`(onUsage 传递)、`relay.go:573-650`(ClientFamily 分类)、`ProxyLog.Attempt`(retryCount)、`proxy_logs.session_key` | modelActual 缺(同 #5) | 并入 #5 的 upstream_model 列 |
| 17 | 路由决策快照(透明可审计) | ⚠️ | `internal/routing/routing.go:100-160`(Explain/ExplainWithSession 实时评估)、`internal/httpapi/admin.go`(/console/routes/explain) | 无持久化:决策快照不落库(全仓库 grep 无 decision_snapshot);proxy_logs 无候选列表/打分/命中策略列,无法事后回答"为什么走这个渠道" | `proxy_logs` 加 `decision_json` 列(候选 ID 列表+Score+SelectedPriority+Sticky/Gray 字段,复用 `routing.Explanation` 的 JSON),recordAttempt 时写入 |
| 18 | 逐 token 模型可用性探测 | ❌ | `internal/store/discovery.go:35-101`(discovered_models 是 per-channel 模型列表)、`internal/domain/models.go:190-200`(DiscoveredModel) | 无 token×model 可用性表(token_model_availability),无法回答"这个 key 能不能跑 gpt-4o";无 isManual 人工覆盖 | 新表 `token_model_availability(token_id, model, available, latency_ms, is_manual)`;复用 healthsweep/discovery 巡检,按 DownstreamKey 维度落表 |
| 19 | 请求日志后台筛选表格 | ✅ | `web/src/features/Logs.tsx:31-90`(channel/model/status/慢请求≥5s 筛选)、`internal/store/008_proxy_log_filters.sql`(channel_id/model/id 索引)、`internal/store/proxylog.go:196-215`(多条件查询) | 缺按小时聚合 QuotaData 看板/模型排行榜(Dashboard 仅总量 summary,`Dashboard.tsx:55-89`) | 可选:web 加 per-hour 聚合查询端点(直接 SQL GROUP BY,数据量小无需预聚合) |
| 20 | 用量预聚合投影 | ❌ | `internal/store/usage.go:120-145`(Summary 直查 SUM(usage_records)) | 无 site_day_usage/site_hour_usage/model_day_usage 预聚合表;无 watermark/lease/recompute;图表量大时拖垮 SQLite | `usage_records` 写路径(RecordRelayUsage 事务内)同步累加 `usage_daily/hourly` 聚合表,查询只走聚合表 |
| 21 | 告警通知系统 | ✅ | 5 通道:`internal/webhook/notifier.go:16-110`(webhook/bark/serverchan/telegram/smtp)、`SendAlert:96-140`(内容签名冷却);事件流:`Notify:340-380`;日摘要:`internal/alert/alert.go:150-280`(DailySummary);主动巡检:`alert.go:40-110`(Sweep:余额低+token 过期);告警触发点:proxy 请求失败/自动禁用(`proxy.go:755-790`)、account 余额/Token(`account/service.go:166-210`)、checkin 失败、healthsweep 状态转换(`healthsweep/service.go:260-280`) | — | — |
| 22 | 模型卡片元数据(ModelCard) | ❌ | 全仓库 grep 无 context_window/knowledge_cutoff/modalities 等;`discovered_models` 只有名字+latency | 无模型能力/成本/上下文/模态元数据,"模型发现"停留在名字列表,无法支撑 vision 请求只去有 vision 的渠道 | 新表 `model_cards(model, context_window, input_modalities, output_modalities, knowledge_cutoff)`;适配器 ListModels 时尽力回填,路由条件引擎(#1)消费 |
| 23 | 渠道 Tag 批量运维 | ✅ | `internal/domain/models.go:146-147`(Channel.Tags)、`internal/store/channel.go:475-520`(UpdateByTag:逗号锚定匹配,批量改 priority/weight/status/models/group/retry_config/system_prompt/header_override)、`internal/store/036_channel_tags.sql`、WebUI Channels.tsx 批量勾选 | — | — |
| 24 | 渠道健康监控升级(jitter+工作池+历史/日聚合) | ⚠️ | jitter:`internal/healthsweep/service.go:157-172`(rand.IntN jitter);每渠道独立探测 goroutine:`service.go:120-155`;状态转换告警:`service.go:260-280` | 缺历史表/每日聚合(仅内存 status map + channels.last_probe_*);**注释宣称 semaphore 但 cfg.Concurrency 只在 `service.go:94-95` 校验从未使用**(注释与代码不符);探测结果不落历史 | 新表 `channel_health_history(channel_id,state,latency_ms,error,checked_at)`+每日 rollup;删/改注释或真正接入 semaphore |
| 25 | 多窗口美元限速(5h/1d/7d) | ❌ | `internal/domain/models.go:270-290`(QuotaTotalTokens/QuotaUsedTokens 仅累计值) | 无时间窗成本上限:5h/1d/7d 窗口起点滚动计数、Redis 计数缓存+脏集批量回写均无 | `downstream_keys` 加 usage_5h/1d/7d+window_start 列;RecordRelayUsage 事务内按窗口累加(窗口起点在 auth 校验时惰性滚动) |
| 26 | 兑换码 + 每日签到 | ⚠️ | 签到 ✅:`internal/checkin/service.go:76-330`(RunCredential/RunAll/acquire 防重入)、`internal/checkin/scheduler.go:58-260`(cron+missed catch-up+CHECKIN_TZ)、迁移 `004/032`、WebUI Checkins.tsx | 兑换码 ❌:全仓库无 redeem 逻辑(`internal/adapters/site_profile.go:29-64` 的 RedeemPath 仅是展示用上游路径) | 新表 `redeem_codes(code, quota_tokens, expires_at, used_at, used_by_key_id)`;admin 端点 POST /console/redeem/redeem 给 DownstreamKey 加配额(单事务+唯一索引防并发) |
| 27 | 幂等记录 | ❌ | 全仓库 grep 无 idempotency(仅注释与测试名中出现) | 客户端重试场景无防重复计费/重复转发;usage 写路径无幂等键 | proxy.RecordUsage 前查 `request_id` 唯一索引(usage_records.request_id 加 UNIQUE,已存在该列)即可获得最小幂等 |
| 28 | leader lock + 定时维护任务 | ❌ | 全仓库无 lease/leader 实现(grep 仅 sync.Mutex);`internal/backup/service.go` 在线备份无实例锁 | 多实例部署时在线备份/用量清理会互相干扰 | 单实例部署可接受;若要支持多实例:DB 租约表(system_task_lock)+ 备份/清理任务加租约获取 |
| 29 | 每站点多 endpoint + 代理出口 | ❌ | `internal/domain/models.go:34-46`(Site 单 BaseURL);`internal/outbound/policy.go`(SSRF 策略);架构文档明确"环境代理变量禁用" | 无站点级 endpoint 列表(各自 cooldown/lastSelected)、无 proxyUrl/customHeaders/SOCKS 出口 | `sites` 加 `endpoints_json`;出站代理需在 outbound 层加显式代理配置(注意与 SSRF/DNS 校验顺序兼容,属安全敏感改动) |
| 30 | 智能站点识别 | ✅ | `internal/sitedetect/sitedetect.go` 全文(AAH 四级链完整移植:域名白名单→15 站点 title 正则→sub2api /api/v1/auth/me 端点形状→new-api 系认证错误文案+compat header 反推白标站) | — | — |
| 31 | 跨渠道比价面板 + 健康总览 | ⚠️ | 健康总览 ✅:`web/src/features/Dashboard.tsx:59-137`(健康/失败/用量概览);单渠道价格展示 ✅:`web/src/features/Models.tsx:1383-1416`(memberFinance:上游价格表换算 USD 单价+可调用次数) | 无跨渠道同模型比价排序/最划算组合标注;无按渠道/模型/日期聚合报表与热力图 | 纯前端增量:Models.tsx 对同模型多渠道按 priceUsd 排序并标记最低价(数据已在 FinanceOverview);报表页复用 usage 查询 |
| 32 | 代理调试快照模式 | ❌ | 全仓库无 debug trace 存储(grep debug_trace 无);`internal/httpapi/try.go` 仅单次手动 try | 无开关式录制请求/响应/每次尝试的调试快照,问题工单排障靠日志 | 新表 `proxy_debug_traces(request_id, attempt, req_body, resp_body, ts)`;runtime settings 加开关,命中时 proxy 写快照(配额限制条数) |
| 33 | 结构化操作审计(op/action + params) | ✅ | `internal/store/audit.go:14-40`(AuditEvent:action/resource_kind/resource_id/outcome/status_code/category)、`internal/store/006_p7_operations.sql`、WebUI OpsPanels | — | — |
| 34 | 缺失模型配置提示 | ❌ | `web/src/features/Channels.tsx:89-214`(只有 missing API key 标志) | 无"渠道声明模型 vs 模型元数据差集"暴露(diff 接口+展示);`Models.tsx` 无未配置提示 | admin 加 GET /console/models/missing(渠道 models_csv ∪ discovered_models − routes 覆盖);Models 页面展示 |
| 35 | WebDAV 备份目标 | ✅ | `internal/webdavsync/` 全套(service.go:94-374 同步/调度/状态、scheduler.go、decrypt.go AES-GCM、settings.go 脱敏)、`internal/store/010_webdav_settings.sql`、`/console/webdav/*` | — | — |
| 36 | 敏感 prompt 保护 / 上游额度轮询 | ⚠️ | 额度轮询 ✅:`internal/account/service.go:504-600`(FinanceOverview:余额+quota+每模型价格)、`ProbeAll:225-240`(token 探测)、余额低/Token 过期告警(`alert.go:40-110` Sweep)、429 park+Retry-After(`account/service.go:166-176`) | 敏感 prompt 保护 ❌:无"敏感内容不发给指定渠道"的渠道标记与请求内容匹配 | `channels` 加 `sensitive_only`/`no_sensitive` 标记;relay 请求体敏感词匹配(可选开关,默认关)后从候选集排除 |

## 三、grokbuild 增量条目(#A-#D)

| # | 条目 | 状态 | 证据 | 缺口 | 建议 |
|---|---|---|---|---|---|
| A | 健康度打分选路(时延感知+负载因子) | ✅ | `internal/routing/routing.go:280-330`(latency EWMA α=0.2 / error EMA α=0.5 / concurrencyFactor 打分)、`routing.go:460-540`(pickLatencyAware/pickErrorAware)、`internal/proxy/proxy.go:240-260`(observeLatency/observeError/decayError)、`routing_mode=adaptive`(`020` 迁移) | — | — |
| B | 上游配额探测器(耗尽绕行) | ✅ | `internal/account/service.go:485-600`(FinanceOverview 余额+quota+价格)、`ProbeAll`、余额低告警(`alert.go` Sweep)、429 park(`account/service.go:166-176`) | 缺"配额告急自动标记模型不可用/自动禁用渠道"联动(目前只告警) | account 服务加阈值判定,余额低于 N 时调 `Channel.AutoDisable` 或候选排除 |
| C | Pool 模式(API-key 同账号重试) | ✅ | `internal/proxy/proxy.go:495-590`(同 channel 多 key 顺序重试)、`recordKeyFailure/recordKeySuccess`(失败 key 剔除)、`cascadeChannelIfAllKeysDisabled`(全挂禁渠道) | 无 pool_mode 配置标志/`pool_mode_retry_count`/`pool_mode_retry_status_codes`(阈值与重试集合硬编码) | RetryConfig 加 pool 字段(重试次数/状态码),回退默认值 |
| D | 版本化价格快照 | ⚠️ | `internal/proxy/proxy.go:990-1015`(billingCost 记录时点算 Cost 落库,`033` 迁移,价格改动不影响历史单) | 无价格版本号:价目表更新不留版本,usage 记录不引用版本 | `model_ratios` 加 `version` 列(更新时自增),UsageRecord 加 `price_version` |

## 四、第 2 轮新增 P0 建议(6.3 节,4 项)

| 项 | 状态 | 证据 |
|---|---|---|
| 协议转换中间格式(pivot) | ✅ | `internal/adapters/intermediate.go`(SegmentConverter:ToOpenAI/FromOpenAI/PivotPath/WrapOpenAIStream)、`ComposeForwardAdapter`(proxy.go:455-475 组合,Anthropic 下游×非 Anthropic 上游自动组合)、`internal/adapters/anthropic_downstream.go`(Anthropic 段) |
| Retry-After 冷却 | ✅ | `internal/proxy/proxy.go:1382-1400`(retryAfterCooldown:整数秒/HTTP-date,只增不减)、429-park 同源(`account/service.go:166-176`) |
| 选择时计数防突发 | ✅ | `internal/proxy/proxy.go:240-260`(acquireChannel/releaseChannel 全 attempt 序列占槽)、`routing.go:280-330`(concurrencyFactor,超限 0.01 兜底) |
| 稳定灰度池 stable_first | ✅ | `internal/domain/models.go:150-160`(StableFirst/StableFirstRequests)、`internal/routing/routing.go:330-360`(pickWithGray 1/N 抽取,全灰/全稳兜底)、`internal/store/channel.go:379-410`(RecordGraySuccess 自动转正)、`internal/store/027_stable_first.sql`、runtime 热配(`runtimeconfig.go:363-366`) |

## 五、README/文档自称 vs 代码核对

| 宣称 | 判定 | 证据 |
|---|---|---|
| README Admin/Public 端点表(含 try/plugins/webdav/runtime-settings/exchange/checkin/audit/backup/v1/messages/v1/responses) | ✅ 全部属实 | `internal/httpapi/` 各 handler 与 README 一一对应;`relay.go:59`(/responses)、`try.go:28`、`plugins.go`、`webdav.go`、`runtime_settings.go`、`exchange.go`、`checkin.go`、`audit.go`、`backup.go` |
| 架构文档(ForwardAdapter 接口/中间格式/粘性/灰度/自动禁用/被动恢复/分级冷却/告警矩阵) | ✅ 与代码一致 | 见上表各证据 |
| **healthsweep 注释"semaphore bounds simultaneous probes"** | ❌ 注释与代码不符 | `internal/healthsweep/service.go:3` 声称 semaphore,但 `cfg.Concurrency` 仅在 `service.go:94-95` 校验、从未用于限制;实际并发=启用渠道数(每渠道一 goroutine + jitter 错峰) |

## 六、最关键的 5 个差距(按影响排序)

1. **#5 usage_log 三字段双写缺失**(❌)——model 别名改写后真实上游模型名不落库,路由归因与排障盲区;改动最小(两列+一处记录),建议最先做。
2. **#2 TransformOptions 缺失**(❌)——清单 P0 首期点名"reasoning_effort 映射与 role 替换",正是 Gemini/Anthropic 适配器真实痛点;当前 `reasoning_effort` 只进日志不进转换。
3. **#17 路由决策快照不持久化**(⚠️)——只有 live explain,无历史快照;per-model 路由上线后"为什么走这个渠道"无法审计。
4. **#4 错误透传规则表缺失**(❌)——非 OpenAI 适配器上线初期压错只能改代码重编译;规则表是清单里最明确的"可配置化"诉求。
5. **#18 逐 token 模型可用性探测缺失**(❌)——Discovery 只列模型,不能回答"这个 key 能不能跑 gpt-4o";与 #1 条件路由组合后价值最大。
