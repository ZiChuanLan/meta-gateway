# AxonHub 功能再盘点 — meta-gateway 尚未具备的功能（12-axonhub-recheck）

> 调研方法：实读 `H:/WorkSpace/api/axonhub` 源码（`internal/server/...`）与官方文档（`docs/en/guides/*.md`）+ 复用既有调研 `03-axonhub.md`；对照基准为 meta-gateway 实际落地状态（README + `research/08-gap-check.md` 的 ✅/⚠️/❌ 逐项复核）。筛选口径：meta-gateway 为单实例、SQLite、无 Redis、无多租户、面向个人/小团队，故多用户/分布式/外部存储类功能标注 [不适合]。
> 旧调研已覆盖的熔断器/Key 自动禁用/可配置重试/流式指标/选择即计数/CTE 吞吐聚合，因 meta-gateway 大部分已移植（gap-check ✅），本报告不再重复。

## 一、[高价值] 直接补已确认缺口、低成本高收益

### 1. API Key Profiles（每 Key 模型映射 + 多 Profile 切换）
**描述**：每个 API Key 可配多个 Profile，Profile 内定义模型映射（exact/regex，首个匹配生效）、渠道限制（channel ID/tag）、模型白名单；激活 Profile 即时生效。
**证据**：`docs/en/guides/api-key-profiles.md`；`internal/server/biz/api_key.go`、`api_key_profile_template.go`；`orchestrator/candidates_condition.go`。
**对 meta-gateway 的价值**：meta-gateway 的 DownstreamKey 只有 scope（chat/embeddings…），无"模型改名"能力。这是解决 Claude Code / Codex 固定模型名（如 `claude-sonnet-4-5`）映射到便宜模型的最直接手段，个人自托管高频刚需。落地：`downstream_keys` 加 `profile_json` + 路由前改写一层，改动小。

### 2. Request Override：9 种 JSON-path Body 操作 + Go 模板 + 条件执行
**描述**：渠道级请求改写，body 支持 `set / set_if_absent / delete / rename / copy / array_append / array_prepend / array_insert / array_remove` 9 种操作；值支持 Go 模板（`.RequestModel / .Model / .ReasoningEffort / .Metadata / .RequestHeader / .PromptCacheKey`）；每条操作可带 `condition` 模板条件；`array_remove` 支持按数组元素字段匹配删除（如按 `function.name` 删工具）；模板渲染出 JSON 对象时自动结构化插入。
**证据**：`docs/en/guides/request-override.md`；`internal/server/biz/channel_override.go`、`channel_override_template.go`。
**对 meta-gateway 的价值**：meta-gateway 只有 header override（gap-check #6 ⚠️ 缺 body 改写）。这直接补上"自定义 endpoint 场景无法改 body"的缺口；`set_if_absent`（客户端可覆盖的默认值）与 `array_prepend`（往 system 注入 Claude Code 风格指令而不动用户内容）都是高价值操作。落地：纯函数操作链 + gjson/sjson，可完全照搬。

### 3. Model Associations：6 种关联规则 + Developer Rule 继承
**描述**：模型→渠道选择支持 6 种规则：具体渠道+具体模型、渠道+正则、全局正则、全局模型、tag 渠道+模型、tag 渠道+正则；按 priority 硬分组（低值优先，组内耗尽才进下一组）；同开发者的模型可配"Developer Rule"统一继承渠道规则（可对单模型关闭继承）。
**证据**：`docs/en/guides/model-management.md`；`internal/server/biz/model_association_matcher.go`、`model_settings_inheritance.go`；`orchestrator/candidates.go`。
**对 meta-gateway 的价值**：meta-gateway 路由是 exact + 最长 `*`/`?` 通配（gap-check #1 ⚠️ 缺条件引擎与 6 种关联方式）。正则与 tag 匹配是最值得补的两点——"所有 `gpt-4.*` 走 DeepSeek 渠道"这类规则用通配符表达不了。Developer Rule 继承对多模型管理是减负设计。
**级别**：[高价值]（建议只取正则 + tag 两个关联类型，全局/优先级机制 meta-gateway 已有）

### 4. 定价引擎：3 种定价模式 + 价格版本号 + costItems 明细
**描述**：计费支持 `flat_fee`（每请求固定费）、`usage_per_unit`（每百万 token 单价）、`usage_tiered`（阶梯单价，含 tierBreakdown 明细）；token 类型拆 7 种（prompt/completion/cache read/cache write/5m 与 1h 变体/reasoning）；每次价格修改生成新版本，usage 日志记录 `cost_price_reference_id`；每请求 cost 明细 `costItems[]` 落库。
**证据**：`docs/en/guides/cost-tracking.md`；`internal/objects/price.go`、`cost.go`；`internal/server/biz/cost_calc.go`、`usage_log.go`、`channel_price.go`。
**对 meta-gateway 的价值**：meta-gateway 只有 ModelRatio 倍率 + key 级 per-1k 单价（gap-check #13 ⚠️、#15 ⚠️、#D ⚠️ 缺 flat/tiered、价格版本、明细）。flat_fee 与 tiered 是"多供应商中转成本核算"的实用补充，价格版本化保证历史账单可追溯（gap-check #D 点名）。落地：`model_ratios` 扩展为价目表 + `usage_records.cost_detail_json` + `price_version` 列。

### 5. Auto Reasoning Effort（模型名后缀自动映射 reasoning_effort）
**描述**：系统开关开启后，请求模型名带 `-max/-xhigh/-high/-medium/-low` 后缀时自动剥离后缀并映射为 `reasoning_effort`（Qwen 系 `-max` 特判不剥离）；默认仅识别 `max/xhigh/high/medium/low` 五个值。
**证据**：`internal/server/orchestrator/auto_reasoning_effort.go`（`splitAutoReasoningEffortModel` + `supportedAutoReasoningEfforts`，约 100 行）。
**对 meta-gateway 的价值**：gap-check #2 ❌ 点名"reasoning_effort 只进日志不映射"。这是该缺口的最小修复路径：一个纯函数 + 一处 middleware 接线。Claude Code 等客户端发 `-max` 模型名时自动转 `reasoning_effort: max`，无需改客户端。
**级别**：[高价值]（半天量级）

## 二、[可做] 有价值但需裁剪或投入中等

### 6. Prompt Protection Rules（正则掩码/拒绝敏感内容）
**描述**：正则规则按消息 role 作用域（system/developer/user/assistant/tool）匹配，动作 `mask`（替换为自定义串）或 `reject`（整请求拦截）；规则支持 enabled/disabled/archived 状态、批量操作、30s 缓存 + 异步重载、匹配预览。
**证据**：`docs/en/guides/prompt-protection-rules.md`；`internal/server/biz/prompt_protection_rule.go`、`prompt_matcher.go`、`prompt_protection_preview.go`。
**对 meta-gateway 的价值**：gap-check #36 ⚠️ 的"敏感 prompt 保护"缺口正对应此功能。个人自托管场景（防止误把 API key/密码发给上游、防 prompt 注入）有实用价值。落地：表 + 正则纯函数 + 转发前 middleware，量级小。

### 7. 渠道级 RPM/TPM/MaxConcurrent 硬限流 + 等待队列
**描述**：每渠道可配 RPM、TPM、MaxConcurrent、QueueSize、QueueTimeoutMs；实现为硬信号量 + FIFO 等待队列（有界队列满则立即拒绝 `ErrChannelQueueFull`，超时 `ErrChannelQueueTimeout`）；未配 MaxConcurrent 时回退到默认连接追踪器的并发信号。
**证据**：`internal/server/orchestrator/channel_limiter.go`（container/list FIFO + slot 转移）、`channel_limiter_manager.go`、`channel_limiter_metrics.go`；`internal/server/biz/channel_rate_limit.go`（校验规则：QueueSize>0 要求 MaxConcurrent>0）。
**对 meta-gateway 的价值**：gap-check #12 ⚠️ 明确"缺每渠道 RPM/TPM token bucket 与软/硬队列"。meta-gateway 现有 inflight 软计数 + 429-park 是弱版本。可简化移植：只取 MaxConcurrent 硬信号量 + QueueSize（RPM/TPM 桶可选）。

### 8. Rate-Limit-Aware 负载均衡评分层 + 429 Retry-After 冷却分
**描述**：6 层评分中的第 6 层：按 1 分钟滑动窗口统计每渠道 RPM/TPM/并发用量，接近上限线性降分（`100*(1-max_usage_ratio)`），任一上限耗尽给 **-10000 分**垫底；渠道返回 429 + `Retry-After` 时进入冷却期并给 -10000 分直至过期。
**证据**：`docs/en/guides/load-balance.md`（策略表）；`internal/server/orchestrator/lb_strategy_rate_limit.go`、`channel_limiter_metrics.go`。
**对 meta-gateway 的价值**：meta-gateway 的 adaptive 路由有 latency/error/concurrency 三因子，缺显式限流感知。与 #7 搭配补全 gap-check #12。落地：复用 meta-gateway 已有 `routing.go` 打分框架加一个策略。

### 9. TransformOptions（每渠道转换开关）
**描述**：每渠道可配 `ForceArrayInstructions`（强制 instructions 为数组格式）、`ForceArrayInputs`（inputs 数组化）、`ReplaceDeveloperRoleWithSystem`（developer→system role 替换）。
**证据**：`internal/server/orchestrator/transform_options.go`；`internal/server/biz/channel_llm_gemini_test.go`。
**对 meta-gateway 的价值**：gap-check #2 ❌ 的 TransformOptions 缺口一部分。meta-gateway 适配器 role 映射是硬编码（`internal/adapters/anthropic.go:109` 等），加这三个开关可解 Anthropic/Gemini 兼容的实际痛点。

### 10. PassThroughBody（同格式原样透传）
**描述**：渠道级 `PassThroughBody` 开关（可回退全局设置）：当入站 API 格式与出站格式一致且 stream 对齐时，body 原样透传跳过转换管线。
**证据**：`internal/server/orchestrator/pass_through.go`（`isPassThroughEnabled`：格式一致 + `passThroughStreamAligned` 校验）。
**对 meta-gateway 的价值**：对"已兼容 OpenAI 的自建上游"省去无谓转换、保留私有字段。meta-gateway 有 pivot 中间格式，加一个"同格式短路"即可。
**级别**：[可做]（小功能）

### 11. Trace/Thread 三级追踪 + Claude Code/Codex 自动提取
**描述**：`AH-Trace-Id` / `AH-Thread-Id` 头建立 Thread→Trace→Request 三级结构；可从 Claude Code `metadata.user_id`、Codex `Session_id` 头自动提取 trace ID；`extra_trace_headers` 可复用 Sentry-Trace 等既有头；管理台有 Trace 查看页（请求体/响应体/耗时/token/渠道）。
**证据**：`docs/en/guides/tracing.md`；`internal/server/biz/trace.go`、`thread.go`；`internal/server/orchestrator/candidates_sticky.go`。
**对 meta-gateway 的价值**：meta-gateway 已有 sticky session（gap-check #8 ✅，trace-aware 路由的价值已覆盖）与 `proxy_logs.session_key`。真正缺的是"请求/响应体级 trace 存储 + 管理页查看 + GC 清理"。中投入（表 + 写入点 + 简单 UI），个人排障价值高但非刚需。
**级别**：[可做]（建议裁剪为：body 快照表 + 保留 sticky 复用现有点）

### 12. Channel 定时模型同步
**描述**：从上游定时拉取模型列表同步到本地（含调度配置）。
**证据**：`internal/server/biz/channel_model_sync.go`、`channel_model_sync_schedule.go`。
**对 meta-gateway 的价值**：meta-gateway discovery 是手动触发 refresh（`/console/discovery/channels/{id}/refresh`）。加 cron 自动刷新 = 小改动，避免模型列表过期。

### 13. Channel 复制 / 合并 / 批量操作
**描述**：渠道支持批量改状态/删除（`channel_bulk.go`）、一键复制（`channel_duplicate.go`）、渠道合并（`channel_merge.go`，多个渠道的 key/配置合并）。
**证据**：`internal/server/biz/channel_bulk.go`、`channel_duplicate.go`、`channel_merge.go`。
**对 meta-gateway 的价值**：meta-gateway 已有 tag 批量更新（gap-check #23 ✅）。复制渠道（克隆配置换 key）最实用、成本最低；合并是运维便利。
**级别**：[可做]（只取 duplicate + 批量勾选）

### 14. Provider Quota URL 探测（可配置上游余额接口）
**描述**：按可配置 URL 探测上游余额/配额，含退避（`provider_quota_backoff.go`）与缓存（`provider_quota_cache.go`）；`biz/provider_quota.go` 为统一入口。
**证据**：`internal/server/biz/provider_quota.go`、`provider_quota_backoff.go`、`provider_quota_cache.go`、`provider_quota_url.go`。
**对 meta-gateway 的价值**：meta-gateway 已有 account probe + FinanceOverview（gap-check #B ✅，余额/配额/价格探测）。差异点是"URL 可配置"与"退避/缓存"细节，价值有限；若 meta-gateway 想支持更多上游余额 API 形态可参考。
**级别**：[可做]（低优先，与现有探测重叠）

### 15. 逐分钟配额窗口
**描述**：配额校验支持分钟级窗口（`quota_minute.go`），配合 `candidates_quota.go` 在候选选择阶段做 key 配额过滤。
**证据**：`internal/server/orchestrator/quota_minute.go`、`candidates_quota.go`、`quota.go`。
**对 meta-gateway 的价值**：meta-gateway 有 per-token 累计配额（gap-check #25 ⚠️ 缺时间窗美元上限）。分钟窗口是对"突发用量控制"的补充，但个人场景低频。
**级别**：[可做]（低优先）

### 16. Image Generation API 转发
**描述**：除 OpenAI/Anthropic/Gemini 对话外，提供 image generation 兼容接口（多供应商）。
**证据**：`README.md` API Types 表（Image Generation ✅）；`docs/en/api-reference/image-generation.md`。
**对 meta-gateway 的价值**：meta-gateway 公共面只有 chat/completions/embeddings/responses/messages。图片生成对个人工具链（配合中转）有实用价值，转发层改动不大。
**级别**：[可做]（Rerank 同属此类但更低频，可一并评估）

### 17. 首次运行引导向导 + 系统默认设置
**描述**：首次启动进入 setup wizard 创建管理员/初始化；`system_default.go` 提供系统级默认设置兜底。
**证据**：`internal/server/biz/system_onboarding.go`、`system_default.go`；README"First run: Follow the setup wizard"。
**对 meta-gateway 的价值**：纯体验项。meta-gateway 是 .env 配置 + Admin token，引导向导可降低小白门槛，但个人自托管用户多为开发者，价值低。
**级别**：[可做]（低优先）

### 18. 定时 GC + SQLite VACUUM
**描述**：定时清理过期 trace/请求数据，含 SQLite vacuum 维护。
**证据**：`internal/server/gc/gc.go`、`gc_internal.go`、`vacuum_test.go`；`internal/server/scheduler/scheduler.go`、`task.go`（通用定时任务框架）。
**对 meta-gateway 的价值**：meta-gateway 有 audit retention 清理；若采纳 #11 的 body 级 trace 存储，就必须配 GC。scheduler 框架对单实例可用 cron 简化替代。
**级别**：[可做]（作为 #11 的配套）

### 19. Profile/Override 模板复用
**描述**：API Key Profile 模板（`api_key_profile_template.go`）与渠道 Override 模板（`channel_override_template.go`），把常用配置存为模板批量套用。
**证据**：`internal/server/biz/api_key_profile_template.go`、`channel_override_template.go`。
**对 meta-gateway 的价值**：配合 #1/#2 落地后的减负功能。个人场景多 Key 同配置时有用，但非必需。
**级别**：[可做]（低优先，随 #1/#2 附带）

## 三、[不适合] 与 meta-gateway 定位冲突

| 功能 | 证据 | 原因 |
|---|---|---|
| 企业 RBAC + Projects（角色/作用域/项目数据隔离） | `docs/en/guides/permissions.md`；`biz/role.go`、`project.go`、`permission_validator.go` | 多用户/多项目模型；meta-gateway 单管理员 token + key scope 已够个人/小团队 |
| OIDC SSO（JIT 建号/PKCE/角色映射/SSO-only） | `docs/en/guides/oidc.md`；`biz/oidc.go`、`oidc_pkce.go` | 依赖多用户体系与 IdP；个人自托管用不上 |
| GraphQL 管理 API | `internal/server/gql/`（gqlgen） | meta-gateway REST 管理面已完整，引入 GraphQL 层无收益 |
| 多数据库（TiDB/PostgreSQL/MySQL）+ 自动迁移 | README 数据库表；`internal/server/db/` | meta-gateway 明确 SQLite 单机定位；PG 支持违反轻量原则 |
| 多实例广播 / 跨实例缓存刷新 | `internal/server/biz/channel_apikey.go`、`channel_cache.go` | 单实例无需广播；gap-check #28 已判定 leader lock 可接受不做 |
| Trace 大 payload 外置对象存储（S3/GCS） | `internal/server/biz/objectstore.go`、`objectstore_s3.go` | 依赖外部存储服务；个人单机磁盘即可 |
| 图片/视频生成媒体文件存储 | `internal/server/video_storage/`、`biz/video.go` | 依赖外部存储 + 媒体管线，个人中转场景基本用不到 |
| K8s/Helm/Render 部署矩阵、多平台安装脚本 | README 部署章节；`deploy/` | 与功能无关的部署面，meta-gateway 已有 Docker/单二进制 |
| 渠道自动模型价格同步（auto model price） | `biz/channel_auto_model_price_test.go`（仅测试文件可见） | 依赖上游价格数据源，且该功能在本仓库证据不全（仅见测试），不做依据 |

## 四、建议优先级汇总（给父代理的落地建议）

1. **第一批（P0，各 ≤ 2 天）**：#5 Auto Reasoning Effort → #2 Request Override body 操作 → #1 API Key Profiles 精简版（仅模型映射）。
2. **第二批（P1）**：#4 定价模式 flat/tiered + 价格版本 → #3 正则/tag 关联 → #6 Prompt Protection → #9 TransformOptions。
3. **第三批（P2，按需）**：#7/#8 渠道硬限流 + 限流感知评分 → #11 trace 存储精简版（含 #18 GC）→ #12 定时模型同步 → #13 渠道复制 → #16 图片生成转发。
4. **明确不做**：第三部分全部。

## 证据清单

- `H:/WorkSpace/api/axonhub/docs/en/guides/api-key-profiles.md`、`model-management.md`、`request-override.md`、`cost-tracking.md`、`load-balance.md`、`tracing.md`、`prompt-protection-rules.md`、`permissions.md`、`oidc.md`
- `H:/WorkSpace/api/axonhub/internal/server/orchestrator/auto_reasoning_effort.go`、`transform_options.go`、`channel_limiter.go`、`live_streaming.go`、`pass_through.go`、`lb_strategy_rate_limit.go`、`quota_minute.go`、`candidates_quota.go`
- `H:/WorkSpace/api/axonhub/internal/server/biz/channel_rate_limit.go`、`channel_override.go`、`prompt_protection_rule.go`、`provider_quota*.go`、`trace.go`、`thread.go`、`channel_model_sync.go`、`channel_bulk.go`、`channel_duplicate.go`、`channel_merge.go`、`api_key_profile_template.go`、`channel_override_template.go`、`system_onboarding.go`
- `H:/WorkSpace/api/axonhub/internal/server/gc/gc.go`、`scheduler/scheduler.go`、`video_storage/worker.go`
- `H:/WorkSpace/api/axonhub/internal/objects/price_schedule_test.go`（时段定价结构：Timezone + Overrides + DailyTime/Weekdays + Priority）
- meta-gateway 对照基准：`H:/WorkSpace/api/meta-gateway/README.md`、`H:/WorkSpace/api/meta-gateway/research/08-gap-check.md`

## 未验证项（Gaps）

- `channel_endpoint.go`（模型→endpoint 映射）与 `model_mapper.go` 的具体语义未读源码，未列入报告，避免臆断。
- `channel_auto_model_price` 仅见测试文件、未见主实现，已标注不采纳。
- axonhub 的 `live_streaming.go`/`stream_preview.go` 属管理台实时流预览，未确认其对外部依赖（WebSocket/SSE）的程度，报告按"可做（小）"保守处理。
- 各功能移植到 meta-gateway 的精确工作量为估算（基于 gap-check 已有接线点），需 parent 侧 Plan 再核实。
