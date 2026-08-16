# new-api 功能复核（对照 meta-gateway 缺口，2026-08 二轮）

> 依据：`07-new-api-main.md` 旧调研（源码级）+ 本轮实读 new-api 源码（controller/model/service/middleware 目录）+ `08-gap-check.md` 已确认的 meta-gateway 现状（✅20/⚠️12/❌12）。
> 筛选原则：meta-gateway 为单实例、SQLite、无 Redis、无多租户、个人/小团队、React 控制台。依赖 Redis/多实例/支付/多租户计费的功能标 [不适合]；与 meta 已有能力重复的不再列为亮点。

## 一、用户侧功能

1. 完整多用户体系（注册/角色/状态/分组/额度/邀请返利）— [不适合]：meta 是"单管理员 + DownstreamKey"模型，引入用户体系等于改变定位。
2. 多登录渠道（GitHub/Discord/OIDC/微信/Telegram/LinuxDO OAuth + Turnstile）— [不适合]：面向公开注册运营，meta 无注册场景。
3. 2FA（TOTP + 备用码）与 Passkey — [可做]：管理面 Bearer token 无密码，2FA 作为可选加固项成本低；若将来加密码登录再升级。
4. 用户每日签到领额度 — [不适合]：与 meta 的"上游凭据 check-in"是两回事（这是终端用户拉活功能）。
5. 兑换码（批量生成/过期/一次性/面额）— [可做]（弱）：08 #26 已列 ❌；meta 场景弱化为"给下游 key 分发一次性加配额凭证"，单表 + 两个端点。
6. 充值（易支付/Stripe/Creem/Waffo）+ 支付合规确认 — [不适合]：个人网关无支付需求。
7. 订阅计划 + 订阅资金源 — [不适合]：依赖支付与多租户；底层"多资金源 + 计费偏好"抽象可借鉴但订阅本身不适用。
8. 排行榜（用户/模型/渠道用量周榜）— [可做]：08 #19 已点名 meta 缺模型排行榜；数据源已有，纯聚合查询。

## 二、令牌管理

9. 令牌能力全集（UnlimitedQuota 无限额度/令牌分组/跨组重试/AccessedTime）— [可做]：08 #14 已确认 meta 具备过期/模型白名单/IP 白名单 ✅；无限额度与 AccessedTime 是零成本小增量。
10. Key 自助查额度（OpenAI credit_summary 兼容端点 + GetTokenUsage）— [可做]：给下游 key 持有者自助查余额的轻量只读端点。
11. 令牌搜索/掩码展示 — [已具备]：meta 控制台已有。

## 三、运营功能

12. 用量日聚合看板（按日/按用户/按流量）— [可做]：08 #19 已点名；纯 SQL GROUP BY 实现，无需预聚合表。
13. 日志多维筛选（含 upstream_request_id）— [可做]（高优先小增量）：meta 已有 channel/model/status 筛选 ✅；补 upstream_request_id 落库 + 筛选对排障很有用。
14. 性能指标报表（按模型/分组 24h 聚合）— [可做]：meta 有 first_byte_ms/retryCount 等字段但无聚合端点。
15. 统一系统任务调度器（DB lease + 心跳）— [可做]（重构项）：单实例 DB lease 无意义；统一注册表是代码质量收益。
16. 系统信息页 — [可做]：版本/启动时间/运行时长页面，低成本运维友好。
17. Uptime Kuma 状态页集成 — [不适合]：meta 自带健康探测 + Prometheus + 告警，重复。
18. ClickHouse 日志导出 — [不适合]：与零依赖定位冲突。

## 四、API / 协议面

19. 端点广度分项：
    - **Rerank** — [可做]：Cohere/Jina rerank 轻量适配器，pivot 中间格式可复用。
    - **Realtime（WebSocket）** — [可做]（低优先）：WS 双向流实现成本高。
    - **视频生成（可灵/即梦）** — [不适合]：非个人网关核心；"异步任务三段式计费"模型将来做图像/视频渠道可借鉴。
    - **Midjourney/Suno** — [不适合]。
    - **Codex 订阅账号管理** — [不适合]：依赖订阅账号体系。
    - **40+ 厂商适配器广度** — [可做]：按需逐个加（阿里/字节/智谱/讯飞等），不必全量移植。
20. 思考转内容 + Reasoning Effort 值域映射 — [高价值]：命中 08 #2 TransformOptions ❌（meta 的 reasoning_effort 只进日志不进转换），改动集中在适配器层。
21. 图片/音频生成端点 — [可做]：若目标是"OpenAI 兼容全集"是主要缺口；个人网关按需。
22. 异步任务体系（提交→轮询→计费）— [可做]（条件性）：仅当引入图像/视频类异步渠道才需要。

## 五、计费 / 定价

23. 分项倍率定价（cache/image/audio 独立单价）— [高价值]：08 #13 已确认 meta 只有单一 ModelRatio；命中 meta 最缺的定价维度，成本是"价目表加列 + 计费按分项计算"。
24. 阶梯/表达式计费（billingexpr 表达式引擎 + 分档结算）— [可做]（低优先）：表达式引擎偏重；可简化（倍率表加 tier 字段）。
25. 缓存 token 独立计费（cache_read/creation/write）— [高价值]：meta 已提取 cache 分项却按 prompt 价计费，补独立倍率即闭环，与 #23 合并实施。
26. 预扣→结算→退款计费会话 — [高价值]（排期大）：07 已列为 meta 最缺体系之首；根治写放大与重复计费，但改动涉及 relay 主链路。
27. 批量更新器（额度写放大治理，内存聚合定时刷库）— [高价值]：直击 08 性能 P1-1（热路径每请求 ~5 次同步 SQLite 写）；内存 map + 定时刷库思路可直接移植。
28. 工具调用计费 — [可做]（低）。
29. 违规费用（CSAM → 违规费 + skip-retry）— [不适合]：公众运营合规机制；"错误归一化 + skip-retry"思路 meta 重试体系已具备。
30. 用户级/分组级倍率 — [不适合]（完整形态）：多分组计费是分销运营概念；只取"倍率组"简化则归入 #23。

## 六、模型管理

31. 模型元数据表（名称/描述/图标/标签/厂商/端点/名称规则/启用分组/配额类型）— [高价值]：08 #22 model_cards ❌；new-api 的 models 表比 08 设想的更贴合 meta 现状（支撑"模型管理页 + 名称规则匹配 + 缺失模型检测"）。注意：new-api 元数据不含 context_window/modalities，实施时按 meta 需求补充。
32. 缺失模型检测（渠道引用但元数据表没有的模型）— [高价值]：08 #34 ❌；改动极小（差集查询 + 前端提示），排障收益立竿见影。
33. 模型目录同步（从 basellm llm-metadata 拉官方模型/厂商）— [可做]：依赖外部上游，与 #31 配合才有意义，默认关闭。
34. 模型归属（owner_by 标记来源）— [可做]：轻量字段，并入 #31。
35. 模型限流（用户×模型维度）— [可做]（低优先）：可简化为 key×model 内存窗口计数（不引 Redis）。

## 七、监控告警

36. 通知通道（webhook HMAC 签名）— [已具备]：meta 5 通道 + 内容签名冷却 ✅。
37. 敏感词过滤（AC 自动机检测/替换/拦截）— [可做]（可选）：08 #36 "敏感 prompt 保护" ❌；new-api 有现成 AC 自动机实现，可平移为"渠道标记 no_sensitive + 内容匹配排除候选"。个人网关默认关闭。
38. 请求 ID 中间件 — [可做]（小）：与 #13 upstream_request_id 配合排障（需先确认 meta proxy_logs 是否已落 request_id）。
39. HTTP 统计中间件 — [可做]（低）：与 Prometheus 重复。

## 八、插件 / 扩展

40. 渠道亲和性缓存（令牌级长期亲和）— [可做]（可选）：与 meta sticky session（会话级）重叠，作为持久化升级低优先。
41. 多 key 轮询/随机选择 — [已具备]：meta key pool + 失败剔除 ✅（new-api 的 per-channel 互斥锁是反例不借鉴）。
42. 渠道 Tag 批量运维/自动禁用/被动恢复/健康巡检 — [已具备]：08 #10/#23 ✅。
43. 预填充分组（prefill_group）— [可做]：随 #31 元数据表实施的附属 UX。
44. 自定义渠道类型 — [已具备]：meta 自定义上游对等（body 改写缺口已在 08 清单内）。

## 九、meta 已具备（对照确认，无需再做）

渠道加权随机+重试、自动禁用/被动恢复、优先级+权重路由、分级冷却+断路器、粘性会话、多 key 轮换+失败剔除、渠道 Tag 批量运维、智能站点识别、结构化审计、WebDAV 备份、灰度池 stable_first、健康度打分选路、协议转换 pivot 中间格式、Retry-After 冷却、告警 5 通道+日摘要、key 级限流、模型白名单/IP 白名单/过期、上游模型发现与刷新。

## 十、最值得 meta-gateway 借鉴的前 5（按投入产出）

1. **#31+#32 模型元数据表 + 缺失模型检测**（[高价值]）——补齐 08 #22/#34 两个 ❌，改动集中在 store + 一个查询 + 前端。
2. **#23+#25 分项倍率定价（cache/image/audio 独立单价）**（[高价值]）——补独立倍率是纯计费配置扩展，命中 08 #13。
3. **#27 批量更新器**（[高价值]）——直击性能 P1-1（热路径同步 SQLite 写），内存聚合 + 定时刷库思路可直接移植。
4. **#20 Reasoning Effort 映射 + 思考转内容**（[高价值]）——命中 08 #2 TransformOptions ❌，改动集中在适配器层。
5. **#26 预扣/退款计费会话**（[高价值，排期大）——根治失败请求额度处理与写放大，建议在 #27 之后做。

次选：[可做] 中优先做 #13（upstream_request_id 落库+筛选）、#12（按日/按 key 聚合看板）、#10（key 自助查额度端点）、#38（request_id）。

## 十一、Gaps（未能完全验证项）

- meta 的 proxy_logs 是否已落 request_id / upstream_request_id（需父代理在 `internal/store/proxylog.go` 复核）。
- meta 是否已有 image/audio 转发端点（需确认 `internal/adapters/` 与 router 挂载）。
- new-api `prefill_group` 与 `usedata_flow` 的完整语义未逐行确认。
- new-api 的"模型限流"完整配置项细节未读全。

---

**证据清单（本轮实读）**：`H:/WorkSpace/api/new-api-main/model/user.go:31-70`、`model/token.go:20-42`、`model/redemption.go:15-30`、`model/pricing.go:20-45`、`model/model_meta.go:25-60`、`controller/token.go:82-110`、`controller/twofa.go:20-60`、`controller/redemption.go`、`controller/rankings.go`、`controller/usedata.go`、`controller/log.go:18-50`、`controller/perf_metrics.go`、`controller/model_sync.go:20-70`、`controller/model_meta.go:15-50`、`controller/missing_models.go`、`middleware/model-rate-limit.go:20-60`、`service/tiered_settle.go:15-80`、`service/sensitive.go:10-70`、`service/webhook.go:20-45`、`model/utils.go`（07 已证）。旧调研引用：`07-new-api-main.md`、`08-gap-check.md`。
