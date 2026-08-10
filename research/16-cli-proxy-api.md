# CLIProxyAPI 调研报告 → meta-gateway 借鉴清单（16-cli-proxy-api.md）

> 调研对象：`H:/WorkSpace/api/CLIProxyAPI`（v7，Go 单二进制，本地 CLI 代理，为 Claude Code/Codex/Gemini CLI/Grok/Kimi 提供 OpenAI/Gemini/Claude/Codex 兼容端点，以订阅 OAuth 账号池为核心）
> 对照基准：`meta-gateway`（Go+SQLite+React 单机网关，API-key 渠道，无订阅 OAuth），已具备项见 `research/08-gap-check.md` 与 `15-consolidated-recheck.md`
> 筛选原则：订阅 OAuth/账号池/逆向类标 [不适合]；只取协议转换、客户端兼容、通用机制、运维模式中 meta-gateway 没有或更优的做法。

## 一、项目定位与架构概览

CLIProxyAPI 是一个本地运行的协议转换代理：对外同时暴露 OpenAI Chat Completions / OpenAI Responses / Claude Messages / Gemini + Interactions / Codex(含 WebSocket、alpha/search、Live) 等端点，对内把请求翻译成目标上游协议（Claude/Gemini/Codex/OpenAI 兼容/Kimi/xAI/Antigravity）并转发。架构核心是 **`sdk/translator` 的 N×M 协议翻译注册表**（格式矩阵：openai / openai-response / claude / gemini / codex / antigravity / interactions），每个 (from,to) 单元格注册 RequestTransform + ResponseTransform{Stream, NonStream, TokenCount}，并外挂插件钩子（Normalize/Translate 前后置）。其上有一层**协议无关的语义意图层**（`internal/thinking`：把"是否要推理摘要"从任意源协议提取、按目标模型能力写回任意目标协议）。凭据选择由 `internal/auth`（Home 模式）+ `internal/runtime/executor`（每个 provider 一个 executor，负责请求改写/重试/冷却/签名）完成。管理面是独立的 Management API（`/v0/management/*`）+ 可自动更新的捆绑控制面板。

## 二、亮点功能清单（按价值排序）

### A. 协议转换与客户端兼容（meta-gateway 正在做的领域，重点）

**1. 协议无关的"推理摘要"意图层** — [高价值]
- 描述：把"是否展示/生成推理摘要"作为协议无关意图：从 OpenAI Chat 的 `reasoning_effort`、Responses 的 `reasoning.summary/generate_summary`、Claude 的 `thinking.display`、Gemini/Antigravity 的 `includeThoughts`、Interactions 的 `thinking_summaries` 中**任一种**提取意图（ExtractSummaryConfig），再按目标协议与**目标模型能力**写回（ApplySummaryConfigForModel：如目标为 Claude 且请求了摘要但未开 thinking，则按模型注册表自动激活 adaptive/enabled thinking 并设 budget；Responses 目标则映射 auto/concise/detailed；Interactions 只接受 auto/none 时做降级）。还处理 OpenRouter 的 `reasoning.exclude`、DeepSeek/Kimi 的 `reasoning_content` 方言差异。
- 证据：`internal/thinking/summary.go`（ExtractSummaryConfig/ApplySummaryConfigForModel/enableClaudeThinkingForSummary/normalizedSummaryDetail 全文）
- 对 meta-gateway 价值：**这是 pivot 中间格式最值得抄的样板**——meta-gateway 的中间格式目前主要是字段搬运，而这里展示了"语义意图 + 模型能力"如何在翻译时跨协议保真且避免无效参数（比如给不支持 thinking 的模型硬塞 thinking 块）。可直接对应 08 #2（TransformOptions / reasoning_effort 映射）与 15 清单 #3（reasoning effort 值域映射），且粒度更细：**意图提取独立于格式、能力感知写回**。

**2. N×M 翻译器注册表 + 插件钩子 + 无翻译回退** — [高价值]
- 描述：翻译按 (from,to) 格式对注册，响应翻译按**目标格式**索引（`r.responses[to][from]`），每个单元格含 Stream/NonStream/TokenCount 三种转换；翻译前后有插件钩子（NormalizeRequest/TranslateRequest/NormalizeResponseBefore/After），无原生翻译时回退"仅改写 model 字段"（防止客户端前缀如 `copilot/gpt-5-mini` 泄漏到上游）。
- 证据：`sdk/translator/registry.go`（TranslateRequest/TranslateStream/TranslateNonStream/TranslateTokenCount 全文）、`sdk/translator/formats.go`（7 种格式常量）、`internal/translator/` 目录按 claude/codex/gemini/openai × claude/gemini/interactions/openai 组织为矩阵
- 对 meta-gateway 价值：meta-gateway 的 pivot 适配器现在是"下游段/上游段组合"（08 已确认 ✅ 中间格式），CLIProxyAPI 的注册表结构提供了**可测试的矩阵组织方式、流/非流/token-count 三态转换、钩子扩展点、以及"无转换也要改写模型名"的回退约定**。低风险增量。

**3. Payload 规则引擎（default/override/filter + 多条件匹配）** — [高价值]
- 描述：全局 YAML 规则按模型名通配（`gpt-*`、`gemini-*-pro`）+ **协议约束**（from-protocol/target protocol）+ **请求头通配** + **payload 条件**（match/not-match/exist/not-exist，gjson 路径）来决定对请求 JSON 做 default（缺失才设）/ override（强制覆盖）/ filter（删除路径）操作，且有 *-raw 变体（值按原始 JSON 片段写入）。本质是**可配置的请求改写操作链**。
- 证据：`internal/config/config_types.go`（PayloadConfig/PayloadRule/PayloadFilterRule/PayloadModelRule 全文）、`config.example.yaml`（payload 配置段）
- 对 meta-gateway 价值：**同时命中 08 的两个 ❌**——#6 body 改写操作链（meta-gateway 只有 HeaderOverride，无 body 改写）和 #1 条件引擎（When 条件：stream/has_image/header 等）。CLIProxyAPI 的条件面更宽（协议 + 头 + payload 存在性/相等性），实现思路可直接搬。

**4. 每模型能力元数据：ThinkingSupport + InputModalities/OutputModalities + MaxContextLength + alias(Fork/ForceMapping/DisplayName)** — [高价值]
- 描述：每个模型条目携带：thinking 能力（levels 或 min/max budget）、输入/输出模态声明（喂给 Codex 客户端模型列表）、上下文窗口覆盖（MaxContextLength 广告给 Codex 客户端）、别名映射（Fork=额外挂一个别名、ForceMapping=把上游响应里的 model 字段改回别名、DisplayName=目录展示名）。全局 `registry.ModelInfo` 支撑模型列表（Claude/OpenAI/Gemini/Grok 四种格式）与摘要激活决策。
- 证据：`internal/config/config_types.go`（ClaudeModel/CodexModel/GeminiModel/OpenAICompatibilityModel 字段）、`internal/server_routes.go`（formatHomeClaudeModel 的 max_input_tokens/max_output_tokens、grokModelsFromRegistryInfos 的 ReasoningLevels）、`internal/thinking/summary.go`（enableClaudeThinkingForSummary 读 registry.LookupModelInfo）
- 对 meta-gateway 价值：对应 08 #22（模型卡片 ❌）——meta-gateway 的 discovered_models 只有名字+延迟，缺模态/上下文/思考能力元数据。这里给出了字段级参考，且"ForceMapping 把响应 model 字段改回别名"正是 meta-gateway mapping_json 改写后没做的**响应侧回写**（08 #5 的 upstream_model 双写问题的镜像）。

**5. 统一 /v1/models 按客户端嗅探返回不同协议格式** — [可做]
- 描述：同一个 `/v1/models` 路径：有 `Anthropic-Version` 头或 `claude-cli` UA → Claude 格式；Grok shell UA → Grok 格式；`client_version` 查询参数 → Codex 客户端格式；否则 OpenAI 格式；Home 模式下再叠加按账号池聚合的模型列表。
- 证据：`internal/api/server_routes.go`（unifiedModelsHandler/isAnthropicModelsRequest/handleHomeModels/handleGrokModels/handleHomeCodexClientModels）
- 对 meta-gateway 价值：meta-gateway 已有 client_family 识别（08 #16 ✅），但"**单端点按客户端自动切换响应 schema**"的编排方式（而非按路径硬分）值得参考，尤其多客户端共用模型列表时。

**6. Codex 专用客户端兼容：multi-agent v2 消息归一化、/backend-api/codex 路由别名、alpha/search 直通、prompt_cache_key 清理** — [可做]
- 描述：`codex.optimize-multi-agent-v2` 把 Codex 特有的 `agent_message`（加密内容）归一化为标准 user 消息再发给非 Codex 上游；提供 `chatgpt_base_url` 兼容的 `/backend-api/codex/responses` 路由别名；alpha/search 端点直通上游且**剥离 prompt_cache_key/prompt_cache_retention 字段**（sanitizeCodexAlphaSearchBody）；Codex 模型请求强制官方 User-Agent/Originator 头（可关闭）。
- 证据：`internal/api/server_routes.go`（codexDirect 路由组、sanitizeCodexAlphaSearchBody）、`config.example.yaml`（codex 配置段）、`internal/runtime/executor/codex_executor_*.go`
- 对 meta-gateway 价值：如果 meta-gateway 要服务 Codex CLI 客户端，`agent_message → user 消息`归一化与"搜索端点剥离缓存字段"是两个可直接抄的协议改写点；路由别名是低成本的兼容性补丁。

**7. /v1/messages/count_tokens 的 token 计数翻译** — [可做]
- 描述：Claude 客户端调 count_tokens 时，翻译器按 (from,to) 注册 TokenCount 转换，把上游 token 计数翻译回 Claude 响应格式。
- 证据：`sdk/translator/registry.go`（TranslateTokenCount）、`internal/api/server_routes.go`（`/v1/messages/count_tokens` 路由）
- 对 meta-gateway 价值：低成本端点；补齐 Claude 兼容面的最小项。

**8. Claude Code 请求伪装（cloaking）+ 头基线** — [不适合]
- 描述：把非 Claude Code 客户端伪装成官方 Claude Code CLI（替换 system prompt 为官方 billing/identity 块、按实测版本基线注入 User-Agent/Package-Version/Runtime/OS/Arch 头、缓存 user_id、敏感词零宽字符混淆）。`ClaudeHeaderDefaults` 记录"实测 Claude Code 软件基线"。
- 原因：本质是**针对 Anthropic 订阅后端的逆向伪装**，meta-gateway 是 API-key 渠道，无此场景；与 15 清单"代理池+TLS 指纹"同类，违反安全边界原则。

**9. 签名验证 / reasoning replay / 签名缓存（Claude/Kimi/Codex/Antigravity/xAI 各一套）** — [不适合]
- 描述：Claude thinking 块的 protobuf 签名校验与重放、Kimi/Codex/xAI 的 reasoning replay、Antigravity 签名缓存开关。
- 原因：签名机制绑定订阅逆向。**但"is-compat"概念可借鉴**：每个模型一个标志位控制 thinking 块如何透传/改写，这正是 meta-gateway TransformOptions（08 #2 ❌）想要的 per-model 开关形态——只取形态，不取签名实现。

**10. 会话亲和：显式头优先 + 派生身份链** — [可做]
- 描述：通用 session affinity：显式会话头（Claude Code/Codex/OpenCode/pi）→ prompt_cache_key → Responses conversation id → 旧式 body id → 执行/派生身份 → 消息内容哈希兜底；TTL 默认 1h；**已绑定凭据压过优先级**（即使更高优先级凭据恢复也保持绑定），仅冷绑定/无会话请求按优先级；绑定凭据不可用时自动 failover。
- 证据：`internal/config/config_types.go`（RoutingConfig.SessionAffinity/SessionAffinityTTL 注释）、`config.example.yaml`（routing 段）
- 对 meta-gateway 价值：meta-gateway 已有粘性会话（08 #8 ✅，TTL 30min + 首条 user 消息摘要），但 CLIProxyAPI 的**身份提取优先级链**（显式头 > prompt_cache_key > conversation id > 内容哈希）和"绑定压过优先级"策略是更成熟的增量，值得吸收进现有 sticky 实现。

### B. 多账号池的通用机制（只记录可借鉴的通用工程，不移植 OAuth）

**11. 401→刷新→重试闭环** — [可做]
- 描述：Codex 请求遇 401 时：ReportHomeUnauthorized → 触发凭据刷新（RefreshHomeSelectionAfterUnauthorized）→ 用刷新后的凭据**原样重放请求**（重新执行 performRequest）；Home 选择有 attempt 生命周期（End("request_failed") 等归因标签）。
- 证据：`internal/api/server_routes.go`（codexAlphaSearch 处理器中 401 分支完整代码）
- 对 meta-gateway 价值：对需要 token 交换/过期的 API-key 渠道（如部分中转站短时效 key）同样适用：**401 → 刷新 → 单次重放 + 归因标签**，比"直接报错"体验好很多，实现小。

**12. 有界凭据自动刷新工作池 + 冷却调度（transient-error cooldown / save-cooldown-status / per-credential disable-cooling）** — [可做]
- 描述：OAuth token 自动刷新走 16 worker 的池（`auth-auto-refresh-workers`）；失败冷却分两类（transient 408/500/502/503/504 可配时长，quota/授权类更长）；冷却状态可落盘（.cds 文件）或仅内存；每个凭据可单独 disable-cooling。
- 证据：`internal/config/config.go`（AuthAutoRefreshWorkers/TransientErrorCooldownSeconds/SaveCooldownStatus/DisableCooling）、`config.example.yaml`
- 对 meta-gateway 价值：meta-gateway 已有渠道级分级冷却+熔断（08 #7 ✅），但**"每凭据可单独关闭冷却"与"transient 冷却时长可配（-1 关闭）"**是缺的开关粒度；有界刷新池模式对上游 token 刷新类渠道通用。

**13. 凭据权重池：priority + weight + prefix 命名空间 + excluded-models** — [可做]
- 描述：每个凭据可配 Priority（高者优先）、Weight（默认 1，上限 1,000,000，非正数=排除，支持 weighted-round-robin 与 fill-first 策略）、Prefix（`teamA/claude-sonnet-4` 形式命名空间，未加前缀的请求只走无前缀凭据，配合 `force-model-prefix`）、ExcludedModels。权重解析/校验抽成共享包（int/string/float/json.Number 全类型兼容）。
- 证据：`internal/config/config_types.go`（各 Key 的 Priority/Weight/Prefix/ExcludedModels 字段）、`config.example.yaml`（routing.strategy 与 force-model-prefix）、`internal/credentialweight/weight.go`
- 对 meta-gateway 价值：meta-gateway 已有 priority+weight 双维路由（08 #11 ✅），但 **prefix 命名空间（多租户/多上游共用模型名消歧）+ 每 key excluded-models** 是增量；weight 校验包的健壮性（JSON Number/字符串/浮点全兼容）可直接抄。

**14. 凭据并发生命周期（heartbeat / reclaim-grace / cancel-bound）+ in-flight 观测快照** — [不适合]
- 描述：Home 模式下凭据有严格并发契约：heartbeat 超时、cancel-bound、reclaim-grace、释放退避；in-flight 观测快照（snapshot-interval/stale-after/分片大小上限）供管理面板展示实时占用。
- 原因：**多进程共享同一批 OAuth 凭据的分布式并发协调**（CPA 多实例场景），meta-gateway 单实例单进程无此问题。

### C. 运维/监控/管理

**15. 流式增强：SSE keepalive + bootstrap-retries（首字节前重试）+ 非流式 keepalive** — [可做]
- 描述：流式响应可配 SSE keep-alive 间隔（防客户端/中间层断流）；**bootstrap-retries：首字节发出前失败可重试**（换凭据重来）；非流式响应按 N 秒发空行防 idle 超时。
- 证据：`config.example.yaml`（streaming.keepalive-seconds / bootstrap-retries / nonstream-keepalive-interval 段）
- 对 meta-gateway 价值：直接对应 15 清单 #8（首输出/首字节超时保护）——"**首字节前重试**"语义比单纯超时报错更完整，meta-gateway 做首字节保护时可一并设计重试。

**16. Management API 独立化 + 控制面板自动更新** — [可做/低]
- 描述：管理面是独立文档化的 HTTP API（/v0/management/*），绑定独立端口与 secret-key（支持 bcrypt），控制面板资源可从 GitHub releases 自动更新（可关）。
- 证据：`config.example.yaml`（remote-management 段）、`internal/config/config.go`（RemoteManagement）
- 对 meta-gateway 价值：meta-gateway 已有 /console/* 管理 API 与 React 前端，**控制面板自动更新**是唯一增量（个人自托管收益低）。

**17. 请求日志体系（CPA trace id / 流式日志 / 体源标记）** — [可做/低]
- 描述：每请求打 CPA Trace ID（绑定凭据索引），请求体记录区分来源（原始/翻译后），流式响应日志，日志文件轮转+总量上限。
- 证据：`internal/logging/request_logger.go`、`requestid.go`、`cpa_trace.go`、`config.example.yaml`（commercial-mode/logs-max-total-size-mb）
- 对 meta-gateway 价值：meta-gateway 已有请求日志+筛选（08 #19 ✅）；**翻译前后双份请求体留痕**（原始 vs 改写后）是排障翻译问题的关键，值得补一个"改写前后 body"记录开关。

**18. Redis RESP 协议输出（用量队列）** — [不适合]
- 描述：同一端口可嗅探 Redis RESP 协议，把内存用量队列按 Redis 协议喂给外部消费者（当前默认关闭）。
- 原因：破坏 meta-gateway 零依赖（无 Redis）哲学；用量查询 SQLite 已覆盖。

**19. 动态库插件系统 + 插件商店** — [不适合/可做]
- 描述：C ABI + JSON 方法协议的动态库插件（Go c-shared 构建），插件商店 registry，插件级 CLI flag 与 Management API 路由注入。
- 原因：meta-gateway 已有 HTTP 插件端点（✅）；动态库加载是重机制，单机个人网关风险大于收益。

**20. Go SDK（可嵌入代理）** — [不适合]
- 描述：整个代理作为可复用 Go SDK（executors/translators/access/watcher/pluginabi），第三方项目可内嵌（生态里 vibeproxy、Claude Dialects 等都在用）。
- 原因：产品形态不同（meta-gateway 是自托管服务非嵌入式库）。

**21. TLS 服务端、pprof、healthz、WebSocket 认证开关（ws-auth 可配）** — [可做/低]
- 描述：内置 TLS、可选 pprof 调试端口、/healthz、WS 端点认证可开关。
- 对 meta-gateway 价值：低；ws-auth 开关对"本地可信客户端直连 WS"场景有一点参考价值。

## 三、最值得借鉴的前 5 清单

1. **协议无关推理摘要意图层**（`internal/thinking/summary.go`）[高价值]
   把"reasoning 摘要/thinking 显示"做成跨协议语义意图：任意源协议提取 → 按目标模型能力写回任意目标协议。这是 meta-gateway pivot 中间格式从"字段搬运"升级为"语义保真"的最短路径，直接补 08 #2 / 15 #3。

2. **N×M 翻译器注册表 + 三态转换 + 插件钩子**（`sdk/translator/registry.go`）[高价值]
   按 (from,to) 矩阵组织翻译、响应按目标格式索引、流/非流/token-count 分离、无翻译时仍改写 model 字段防前缀泄漏——meta-gateway pivot 适配器可直接采纳的架构骨架。

3. **Payload 规则引擎**（`internal/config/config_types.go` PayloadConfig）[高价值]
   模型通配 + 协议 + 头 + payload 条件 → default/override/filter 改写链。一次性覆盖 meta-gateway 两个 ❌（body 改写 #6、条件引擎 #1），且实现可照搬（gjson/sjson 已是 meta-gateway 熟悉的技术栈）。

4. **每模型能力元数据 + 响应侧 ForceMapping**（`config_types.go` 各 Model 结构）[高价值]
   ThinkingSupport / InputModalities / OutputModalities / MaxContextLength / alias Fork+ForceMapping+DisplayName。补 08 #22 模型卡片 ❌，同时解决"响应 model 字段改回别名"（08 #5 的响应侧镜像）。

5. **401→刷新→重试闭环 + 有界刷新池**（`internal/api/server_routes.go` codexAlphaSearch 401 分支）[可做]
   对需要 token 交换/短时效 key 的 API-key 渠道通用：401 → 刷新凭据 → 原样重放一次，带归因标签。实现小、收益直接；配合 #12 的"每凭据 disable-cooling"开关粒度更佳。

## 四、Gaps（未深挖/存疑，留给 parent 决定）

- `internal/auth/`（Home 选择器、CredentialPolicy）、`internal/home/`（管理面板客户端）、`internal/signature/`（签名实现细节）未逐文件读，以上结论基于调用点证据；若 meta-gateway 真要抄"401→刷新→重试"，建议再读 `internal/auth` 的 SelectAuthWithCredentialPolicy 与刷新协调代码。
- `internal/translator/{claude,codex,gemini,openai}/` 各单元格的**具体字段映射表**（如 claude→openai 时 tool_use 如何转 function call）未逐格读，报告只验证了注册表机制本身；meta-gateway 若要对齐某条具体映射（如 agent_message 归一化），需再取对应 translator 文件。
- `docs/sdk-advanced.md`（executors & translators）未读，SDK 层的高级扩展点可能还有遗漏。
- count_tokens、/backend-api/codex 别名等端点已确认存在（routes 证据），但未验证 meta-gateway 是否已有等价实现，需 parent 侧确认。
