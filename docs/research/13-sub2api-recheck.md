# Sub2API 功能再盘点 — meta-gateway 尚未具备的功能点（13-sub2api-recheck）

> 方法：重读 `H:/WorkSpace/api/sub2api/backend/internal/{service,handler,handler/admin,payment,securityaudit}` 文件面 + `frontend/src/views/{user,admin,auth}` 页面面 + `docs/COMPOSITE_GROUPS.md`；对照 `meta-gateway/README.md`、`research/08-gap-check.md`、`research/09-merged-summary.md`、`OPTIMIZE.md` 逐项比对。凡 meta-gateway 已落地（✅）或已在计划内的不再重复列出。
> 筛选基准：meta-gateway = 单实例、SQLite、无 Redis、无多租户、个人/小团队。依赖 PG/Redis/多实例/公开注册的项标 [不适合]；单实例 SQLite 可降级实现标 [可做]。
> 旧调研衔接：`research/02-sub2api.md` 已证"幂等防重复扣费"是 README 夸大——幂等仅挂在 API Key 创建（`handler/idempotency_helper.go`），扣费路径未接入。本报告沿用该结论，不把"扣费幂等"列为 sub2api 已落地功能。

## 一、结论速览（对 meta-gateway 最值得借鉴的 6 项）

1. **[高价值] 可配置错误透传规则表** —— 正补 meta-gateway 差距 #4（现只能改代码）。
2. **[高价值] 渠道主动健康监控 + 可用率历史聚合** —— 正补差距 #24（healthsweep 无历史落库）。
3. **[高价值] 指标阈值告警规则引擎** —— 现 meta-gateway 只有"事件→通知"，无可配指标规则（metric/operator/threshold/window/sustained）。
4. **[高价值] 模型 not_found/协议不支持 → 渠道×模型不可用标记** —— 正补差距 #3（现 4xx 仅不重试、不写状态）。
5. **[高价值] 兑换码/充值码体系** —— 正补差距 #26（checkin 已有、redeem 半截）。
6. **[高价值] 首输出/首字节超时保护** —— 正补自身 P1-2"出站无整体超时"。

## 二、亮点功能清单（按类）

### A. 用户侧功能（多为 SaaS 运营向）

| 功能点 | 一句话描述 | 借鉴价值评估 | 档位 |
|---|---|---|---|
| 用户注册/登录 + 邮箱验证 + 找回密码 | 完整用户体系 | meta-gateway 用 ADMIN_TOKEN + DownstreamKey 单管理员模型；引入用户体系 = 多租户，与定位冲突 | [不适合] |
| 第三方 OAuth 登录（DingTalk/LinuxDo/WeChat/OIDC/邮箱绑定） | 多来源身份登录 | 社区身份源对个人网关无收益；ADMIN_TOKEN 已够用 | [不适合] |
| TOTP 二次认证（`totp_service.go`，密钥加密落库） | 账号级 2FA | 单管理员场景给 Admin 登录加 2FA 是真实安全增量（现只有 Bearer token）；实现轻量，无需 Redis | [可做] |
| Passkey 无密码登录（WebAuthn） | 无密码认证 | 2FA 进阶项，依赖浏览器 WebAuthn 支持面，优先级低于 TOTP | [可做] |
| Turnstile 人机验证 | Cloudflare 防机器人 | meta-gateway 无公开注册入口 | [不适合] |
| 支付闭环（EasyPay/支付宝/微信/Stripe/Airwallex + 订单生命周期） | 内置支付 | 个人/小团队无对外售卖需求；支付合规是重负担 | [不适合] |
| 订阅支付计划 | 按时长套餐售卖 | 同支付，SaaS 专属 | [不适合] |
| 兑换码/充值码（`redeem_code.go`、`redeem_service.go`） | 批量生成兑换码充值额度，批量导出 | meta-gateway 差距 #26 恰缺 redeem 半截；可简化为"给 DownstreamKey 加配额"的管理员兑换码（单事务 + 唯一索引防并发），实现成本低 | [高价值] |
| 促销码/分销/联盟/公告/定向投放 | 商业运营向 | 依附支付/用户体系 | [不适合] |
| iframe 嵌入外部系统 | 后台内嵌第三方页面 | 纯前端，成本极低，个人价值一般 | [可做] |

### B. 计费 / 定价

| 功能点 | 一句话描述 | 借鉴价值评估 | 档位 |
|---|---|---|---|
| Token 级精确计费 + 倍率（含 cache_read/write 细分、force_cache、长上下文） | 按 usage + 模型倍率计费 | meta-gateway 已有 usage 提取 + cache 计费 + ratio 倍率（#13 ⚠️），缺口仅在定价模式（flat/volume tiered） | [可做]（仅定价模式增量） |
| 图片按尺寸/输出数计费 | 图像生成计费 | 若后续加 `/v1/images` 端点则配套需要；当前无图片端点 | [可做]（随图片端点） |
| 利润预览/成本核算（官方参考价对比） | 按渠道定价 vs LiteLLM 参考价预览利润率 | meta-gateway 已有 FinanceOverview 每模型单价展示（#15 ⚠️），缺"利润率/最划算渠道"标注；纯前端增量 | [可做] |
| 余额变动流水 + 低余额邮件提醒 | 余额历史 | meta-gateway 已有余额低告警（alert Sweep ✅）；流水页对个人意义小 | [不适合] |
| 多窗口美元限速 | 按模型/平台多窗口额度 | meta-gateway 差距 #25（5h/1d/7d 窗口）仍缺；sub2api 是平台订阅额度窗口而非美元窗口，实现思路可参考但需自研窗口滚动 | [可做]（自研为主） |

### C. 令牌（API Key）管理

| 功能点 | 一句话描述 | 借鉴价值评估 | 档位 |
|---|---|---|---|
| Key 级 RPM/TPM/并发/额度 + 最近使用时间 | 每 key 独立限速/配额 | meta-gateway 已有 key 组配额+限速（034 ✅）+ 过期/白名单（#14 ✅）；缺口仅"last_used/用量查询页"，属前端小增量 | [可做] |
| API Key 认证缓存 + 失效 outbox（`api_key_auth_cache.go`、`auth_cache_invalidation_outbox.go`） | 认证缓存 + 变更 outbox 批量失效 | meta-gateway P1-4 恰是"记账后 invalidate 导致缓存命中率≈0"；sub2api 的"版本号 + outbox 增量失效"模式可直接借鉴修性能 | [高价值]（性能修复向） |
| 幂等框架（`idempotency.go`，30s 抢占 + 回放） | 幂等键防重复 | **旧调研结论：扣费路径未挂载，仅 API Key 创建在用**。作为"防重复计费"参考架构有价值，但 sub2api 自身也未落地到扣费 | [可做]（参考架构，落地需自证） |

### D. 模型管理 / 路由

| 功能点 | 一句话描述 | 借鉴价值评估 | 档位 |
|---|---|---|---|
| 复合组路由注册表（`COMPOSITE_GROUPS.md`：public_model→平台+upstream_model，exact/prefix + endpoint 维度 + 优先级，内置 claude-*/gpt-*/gemini-*/grok-* 前缀检测，未知模型 fail-closed） | 组内复合路由 | meta-gateway 的 route 只有 exact+wildcard，无 endpoint 维度、无内置平台检测；"endpoint 维度 + 内置前缀检测"是轻量高价值增量，与现有 mapping_json 互补 | [可做] |
| 模型广场/渠道广场 | 用户可见橱窗 | 对外售卖运营向 | [不适合] |
| 模型 not_found / 模型瞬态分类（`model_not_found_error.go`、`openai_account_model_transient.go`） | 区分"模型不存在/不支持"与"瞬时故障"，避免白耗配额 | meta-gateway 差距 #3 正是此缺口（4xx 不写状态）；sub2api 有现成分类语义可移植，改动集中在 classifyForChannel + breaker 接线 | [高价值] |
| 模型别名/映射 + 计费用真实上游模型 | 改写后按真实模型计费/日志 | meta-gateway 差距 #5（usage_log 三字段双写）同源；sub2api 证明"改写后记真实模型"是成熟做法 | [高价值]（补 upstream_model 落库） |

### E. 运营功能（Ops）

| 功能点 | 一句话描述 | 借鉴价值评估 | 档位 |
|---|---|---|---|
| 指标阈值告警规则引擎（`ops_alert_evaluator_service.go`：metric_type/operator/threshold/window/sustained/cooldown，firing/resolved/manual_resolved 状态机） | 可配告警规则按指标窗口评估 | meta-gateway 有 5 通道通知与事件告警，但无"用户可配指标规则"；评估器本身不依赖 Redis（锁可去掉），单实例可做内存版 | [高价值] |
| Ops 实时流量/趋势/直方图/健康分 | 实时请求面板 | meta-gateway 已有健康度打分（✅）与 Dashboard；实时流量/趋势页是纯展示增量，数据已在 proxy_logs | [可做] |
| 定时报告 | 邮件推送运营报告 | 与现有日摘要告警重叠；可并入告警通道 | [可做]（低优先） |
| 系统日志查看器（WebSocket 推送） | 后台实时查看系统日志 | 个人网关排障实用，纯增量 | [可做] |
| 请求拒绝/滥用拦截（无效认证滥用限流） | 按规则拒绝可疑流量 | 无效认证限流对公网暴露网关有真实价值，实现轻量（计数+冷却） | [可做] |
| 用量记录 worker pool（`usage_record_worker_pool.go`） | 用量异步批量写 | 直击 meta-gateway P1-1（热路径每请求多次同步 SQLite 写）；性能改造借鉴价值高 | [高价值]（性能向） |
| 在线升级 + 回滚（`update_service.go`：GitHub Releases 检查、域名白名单、500MB 上限、保留 3 回滚版、tar.gz 校验） | 后台一键升级 | 单二进制网关在线升级对个人用户实用；安全要点已现成可抄 | [可做] |
| 定时测试（`scheduled_test_service.go`） | 按计划跑测试请求验证渠道 | 与渠道监控互补；meta-gateway 有手动 try + healthsweep，定时化是增量 | [可做] |

### F. 监控告警（渠道健康）

| 功能点 | 一句话描述 | 借鉴价值评估 | 档位 |
|---|---|---|---|
| 渠道主动健康监控（`channel_monitor_service.go` + checker + runner + SSRF 防护 + 模板配置） | 定时真实请求探测渠道×模型，历史落库，窗口可用率（`ComputeAvailability`） | meta-gateway 差距 #24（healthsweep 无历史表/日聚合）正缺此；"探测→历史→可用率聚合→视图"闭环可整体移植到 SQLite | [高价值] |
| 渠道状态用户视图 + 健康分聚合 | 用户侧看渠道可用率 | 属监控展示层，随上项一起做 | [可做] |

### G. 协议转换 / API 面

| 功能点 | 一句话描述 | 借鉴价值评估 | 档位 |
|---|---|---|---|
| 错误透传规则表（`error_passthrough_service.go` + runtime + handler） | 可配置"错误码+关键词→透传/改写/跳过监控"规则，热更新 | **meta-gateway 差距 #4 的直接答案**；sub2api 是现成参考实现（表+匹配器+运行时开关） | [高价值] |
| Web 搜索模拟（`gateway_websearch_emulation.go`） | 上游不支持 web search 时模拟 web_search 工具 | 对个人网关（Claude/Gemini 系模型）有实用价值；依赖第三方搜索 API 配置，可做成可选插件 | [可做] |
| OpenAI Responses WebSocket ingress（`openai_ws_forwarder.go`） | Codex CLI 风格 WS 入口，WS↔HTTP 桥接 | Codex CLI 是个人网关高频客户端；meta-gateway 有 `/v1/responses` HTTP 但无 WS 端点。工作量较大，可只做"WS→HTTP bridge"最小版 | [可做]（中大型） |
| OpenAI Live / Realtime + attestation | ChatGPT 实时语音透传 | 依赖订阅账号 + attestation 体系，与 key 渠道定位冲突 | [不适合] |
| 异步图片任务 + 队列 + 存储 | `/v1/images/*/async` | 依赖队列/存储/CDN，单实例过重 | [不适合] |
| 图片/视频生成端点 | 图片/视频生成 | 图片同步透传可做（轻量），视频依赖 Grok 媒体体系 | [可做]（仅图片透传） |
| 首输出超时（`openai_first_output_timeout.go`） | 流式首 chunk 超时即 failover | 正补 meta-gateway P1-2"出站无整体超时"；轻量、直接可抄 | [高价值] |
| 思维链协议过滤（`thinking_protocol.go`） | 过滤/转换 reasoning 字段 | meta-gateway 的 reasoning_effort 只进日志不进转换（差距 #2）；有现成过滤实现可参考 | [可做] |

### H. 上游订阅管理 / 账号池（整体 [不适合]，仅列单项参考）

| 功能点 | 一句话描述 | 借鉴价值评估 | 档位 |
|---|---|---|---|
| 订阅账号池 OAuth 授权/刷新（ChatGPT/Claude/Gemini/Grok/Antigravity） | 订阅账号 token 刷新池 | meta-gateway 只接 API-key 渠道，不做订阅逆向；整体不适用 | [不适合] |
| 配额探测 + 调度联动（耗尽移出调度；429/403 分类） | 探测配额联动路由 | **唯一可借鉴点**：对 new-api/one-api 渠道做"余额探测→自动降级/标记不可用"联动（meta-gateway 差距 B 只告警不联动）；探测协议不同需自研 | [可做]（仅联动逻辑） |
| 账号过期自动暂停 | 到期账号暂停调度 | 对 key 渠道可用"key 失效剔除"覆盖（已有 ✅） | [不适合] |
| 池模式 failover + 会话粘滞（session_id header） | 同账号重试→切号 | meta-gateway 已有同渠道多 key 重试 + 会话粘滞（✅）；缺"按 header 显式粘滞"可补 | [可做]（session_id header 粘滞） |
| 代理池 + TLS 指纹 | 出口代理池 | 防封运营向，与 SSRF 安全边界冲突 | [不适合] |
| 调度快照/outbox/timing wheel | 调度候选快照 | 账号池专属；"路由候选进程内缓存"思路可缓解 P1-3，但属性能改造 | [不适合]（仅思路参考） |

### I. 安全

| 功能点 | 一句话描述 | 借鉴价值评估 | 档位 |
|---|---|---|---|
| 内容审核（关键词/风控/脱敏/转发审核） | 请求内容审核 | 面向公开服务合规；个人网关可做"敏感词→拒绝或改发渠道"轻量版（差距 #36 敏感 prompt 保护同源） | [可做]（轻量版） |
| Prompt 审计与出境防护（`prompt_guard.go`、`prompt_outbound_security.go`） | 敏感内容阻止出境 | 与内容审核合并评估；Qwen3Guard 属外部模型依赖 | [可做]（仅出境防护） |
| URL 白名单/响应头过滤/CSP | 上游 URL 白名单等 | meta-gateway 已有出站 SSRF 策略（✅）；响应头过滤/CSP 是增量，成本低 | [可做]（响应头/CSP） |
| 客户端 IP 信任链 | CDN 客户端 IP 头 | meta-gateway 已有 TRUSTED_PROXY_CIDRS；模式可参考但非缺口 | [不适合] |

### J. 部署 / 运维

| 功能点 | 一句话描述 | 借鉴价值评估 | 档位 |
|---|---|---|---|
| 设置向导 / Simple Mode | 首次启动向导 | meta-gateway 为 env 配置单二进制，无向导需求 | [不适合] |
| 自动生成密钥的部署脚本 | 部署时自动生成安全凭证 | meta-gateway 已要求 env 手动填密钥；生成器是便利性小工具 | [可做]（低优先） |
| 在线升级（见 E 节） | — | — | [可做] |

## 三、明确不适用清单（一句话理由，防重复调研）

- 订阅账号池/OAuth 逆向（ChatGPT/Claude/Gemini/Grok/Antigravity 订阅）—— 与 meta-gateway API-key 渠道定位冲突。
- 支付/订单/退款/促销码/分销/公告/订阅套餐 —— 个人网关无售卖场景。
- 用户注册 + 第三方 OAuth 登录 + Turnstile —— 无公开注册入口。
- 模型广场/渠道广场 —— 对外展示运营向。
- 异步图片队列 + 存储 + 视频生成 —— 依赖队列/存储/CDN，单实例过重。
- OpenAI Live/Realtime + attestation —— 依赖订阅账号与 attestation 体系。
- 代理池 + TLS 指纹 —— 防封运营向，与出站安全边界冲突。
- 数据管理 gRPC 代理 —— sub2api 已自行标注 deprecated。
- 依赖 Redis 的分布式件（leader lock、timing wheel、billing cache、调度快照）—— meta-gateway 单实例无需。

## 四、证据与缺口

**证据（源码路径）**
- sub2api 后端：`H:/WorkSpace/api/sub2api/backend/internal/service/`（error_passthrough_service.go、channel_monitor_service.go、ops_alert_evaluator_service.go、update_service.go、redeem_service.go、gateway_websearch_emulation.go、openai_live.go、openai_ws_forwarder.go、openai_first_output_timeout.go、api_key_auth_cache.go、usage_record_worker_pool.go 等）
- sub2api 前端：`H:/WorkSpace/api/sub2api/frontend/src/views/{user,admin,auth}/`
- sub2api 文档：`docs/COMPOSITE_GROUPS.md`
- meta-gateway 基线：`README.md`、`research/08-gap-check.md`、`research/09-merged-summary.md`、`OPTIMIZE.md`
- 旧调研衔接：`research/02-sub2api.md`

**缺口（未完全验证）**
1. `ops_alert_evaluator_service.go` 的规则评估主体只读了模型定义与入口，metric_type 支持的具体指标集合未逐一核对。
2. `update_service.go` 的"一键升级"是否含二进制自替换与 systemd 重启未读到执行段。
3. sub2api 是否存在"5h/1d/7d 美元窗口限速"未在源码中直接命中（为平台/模型维度），该项以"需自研"表述。
4. `channel_monitor` 的探测是否真实发请求未读实现段（仅见接口与 SSRF 防护），但测试文件存在，倾向真实探测。
5. meta-gateway 前端是否已有"错误透传/渠道监控"半成品：`web/src/features` 仅有 Channels/Models/Logs/Dashboard/Checkins/Exchange/Keys/Maintain/OpsPanels/Store/TryPanel，判定为全缺。
