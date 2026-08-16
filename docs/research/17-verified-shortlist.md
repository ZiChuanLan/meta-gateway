# 候选功能逐一验证清单（17-verified-shortlist.md）

> 验证日期：2026-08-10。方法：**逐项亲手 grep + 读码**（非脚本批量），三项检查：①来源项目文件/代码真实存在；②meta-gateway `internal/` + `web/src/` 当前确实没有（docs/research 提及不算实现）；③接入可行（技术栈/依赖匹配）。
> 结论：✅ 确认 27 项 / ⚠️ 部分已有 5 项 / ❌ 排除 2 项（调研误报）+ 不适用 9 项。

## 一、✅ 确认：来源属实 + meta 当前没有 + 可接入（27 项）

| # | 功能 | 来源项目（证据文件已确认存在） | meta 现状证据 |
|---|---|---|---|
| 1 | 推理摘要意图层（reasoning_effort 跨协议映射+能力感知写回） | CLIProxyAPI `internal/thinking/summary.go` | `proxy.go:891` 仅注释提及；`proxylog.go` 只落库不转换 |
| 2 | Payload 规则引擎（body 改写 default/override/filter） | CLIProxyAPI `config_types.go` PayloadConfig；axonhub `channel_override.go`；metapi `payloadRules.ts` | `channels` 表只有 `header_override` 列（channel.go:282） |
| 3 | 模型元数据表（模态/上下文窗口/思考能力） | new-api `model/model_meta.go`；CLIProxyAPI `registry/` context_window | `discovered_models` 只有名字+latency；`internal/` 无 modalities/context_window |
| 4 | 缺失模型检测（渠道引用但未登记） | new-api `controller/missing_models.go` + `model/missing_models.go` | 无 missing-models 端点 |
| 5 | 分项倍率定价（cache/image/audio 独立单价） | new-api `model/pricing.go` CacheRatio | `model_ratios` 单一 ratio（033_usage_cost.sql）；cache 按 prompt 价 |
| 6 | 错误透传规则表（错误码+关键词→透传/改写/跳过监控） | sub2api `error_passthrough_rule.go` + migration 048 | 无 passthrough_rule（passthrough 是转发适配器非规则表） |
| 7 | 路由决策快照持久化 | metapi `decisionSnapshot`（schema.ts L154） | 无 decision_json/decision_snapshot |
| 8 | 渠道健康历史 + 可用率窗口聚合 | sub2api `channel_monitor_service.go` + ComputeAvailability | healthsweep 无历史表（仅内存状态+last_probe 列） |
| 9 | 模型 not_found/协议不支持 → 渠道×模型不可用标记 | sub2api `model_not_found_error.go` | classifyForChannel 无此分类分支 |
| 10 | 首字节/首输出超时保护 | sub2api `openai_first_output_timeout.go` | 有 nonStreamTimeout 整体超时，无首字节维度 |
| 11 | 兑换码体系（给下游 Key 加配额） | new-api `model/redemption.go` + `controller/redemption.go` | `site_profile.go:32` 仅 RedeemPath 展示（上游 UI 路径） |
| 12 | 余额历史曲线（每日快照） | all-api-hub `services/history/dailyBalanceHistory/` | alert/account 只探余额不落历史 |
| 13 | Key 自助查额度（OpenAI credit_summary 兼容端点） | new-api `controller/token.go` GetTokenStatus | 无 credit_summary |
| 14 | upstream_request_id 落库 + 筛选 | new-api `controller/log.go` | proxy_logs 无该列 |
| 15 | 401→刷新→原样重放闭环 | CLIProxyAPI `RefreshHomeSelectionAfterUnauthorized` | 无凭据刷新重放（4xx 仅 failover） |
| 16 | 指标阈值告警规则引擎 | sub2api `ops_alert_evaluator_service.go` | 只有事件→通知，无可配规则 |
| 17 | 逐 token/账号模型可用性探测 | metapi `modelAvailabilityProbeService.ts` | 无 token_model_availability |
| 18 | 敏感 prompt 保护（掩码/拒绝/渠道排除） | new-api `service/sensitive.go`；axonhub `prompt_protection_rule.go` | 无 |
| 19 | 系统代理出口（全局/每站点） | metapi `systemProxyUrl`（config.ts L78） | outbound 明确禁用代理 |
| 20 | TOTP 2FA | new-api `controller/twofa.go` + `model/twofa.go` | 无 totp |
| 21 | 定时模型同步 | axonhub `channel_model_sync.go` | discovery 仅手动 refresh + 被动恢复探测（discovery.go:366） |
| 22 | 渠道复制（克隆配置） | axonhub `channel_duplicate.go` | 无 |
| 23 | 全局搜索（跨站点/模型/Key） | metapi `apiSearch.ts` | 无 |
| 24 | 工厂重置 | metapi `factoryResetService.ts` | 无 |
| 25 | SSE keepalive + 首字节前重试 | CLIProxyAPI `keepalive-seconds`/`bootstrap-retries` | 无（proxy_test.go:867 是测试字符串；outbound KeepAlive 是 TCP 层） |
| 26 | /v1/messages/count_tokens 端点 | CLIProxyAPI `count_tokens` 路由 | 无 |
| 27 | 定时 GC/VACUUM 维护 | axonhub `gc.go` | 仅 audit retention 清理 |

## 二、⚠️ 部分已有（增强项，不算新功能）

| 项 | 现状 | 缺口 |
|---|---|---|
| 签到奖励解析 | **meta 已有**：`checkin/service.go:52` Reward + `adapters/checkin.go:131-171` 解析 JSON reward + checkin_logs.reward 列（004_checkin.sql） | 无（metapi 的文本正则解析可作补充但非必需） |
| images/audio/moderations 端点 | **meta 已有**：`relay.go:183-207` forwardPassthrough（images/generations、edits、variations、audio/speech、transcriptions、translations、moderations） | 仅 /v1/files 无 |
| 模型跨渠道比价排序 | **meta 有单价展示**：Models.tsx memberFinance（priceUsd+calls） | 无按价排序/最便宜标注（sortMembers 只按 priority/weight） |
| 渠道硬限流队列 | **meta 有软限**：inflight 计数 + 429 park + concurrencyFactor | 无 MaxConcurrent 硬信号量 + FIFO 队列 |
| 会话亲和链 | **meta 有 sticky**：X-Meta-Session-Id + 首条 user 摘要 + 绑定后成功转发 | 身份提取链较短（无 prompt_cache_key/conversation id/内容哈希兜底），绑定优先级策略可增强 |
| N×M 翻译注册表 | **meta 有 pivot 中间格式**（SegmentConverter + ComposeForwardAdapter） | 无 (from,to) 矩阵注册表组织、无 TokenCount 三态分离——架构组织方式可借鉴 |

## 三、❌ 调研误报已排除（2 项）

- ~~签到奖励解析~~ → meta 已有（见上）
- ~~images 端点~~ → meta 已有（relay.go:183-207）

## 四、不适用（9 项，与定位冲突，确认后排除）

Claude Code 请求伪装（cloaking）、thinking 签名重放、Redis RESP 用量输出、动态库插件系统、Go SDK 嵌入、订阅 OAuth 账号池、多进程凭据并发契约、TLS 指纹/代理池、多数据库支持。

## 五、建议优先级（结合 15 清单）

**第一批（≤1 天）**：#26 count_tokens → #13 credit_summary → #12 余额历史 → #14 upstream_request_id → #4 缺失模型检测 → 比价排序（部分项）
**第二批（1-2 天）**：#1 推理意图层 → #5 分项倍率 → #7 决策快照 → #9 not_found 标记 → #11 兑换码 → #20 TOTP
**第三批（按需）**：#2 body 改写 → #6 错误透传 → #8 健康历史 → #15 401 重试 → #16 告警规则 → #17 逐 token 探测 → #18 敏感保护 → #19 系统代理 → #21 定时同步 → #22 渠道复制 → #23 全局搜索 → #24 工厂重置 → #25 keepalive → #27 GC/VACUUM
