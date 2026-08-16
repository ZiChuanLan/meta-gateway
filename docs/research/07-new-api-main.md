# new-api-main 架构分析(源码级,2026-08)

> 证据说明:全部结论来自实际读码,行号 grep 验证过的为精确值,其余按读取顺序估算。

目标: `H:/WorkSpace/api/new-api-main`(Go + Gin + GORM)。本文全部结论来自实际读码。

## 第一部分:可借鉴机制(按价值排序)

### 1. BillingSession 统一计费会话(预扣→结算→退款生命周期)
文件: `service/billing_session.go:34-330`

- 单一会话对象封装"预扣费/结算/退款"全生命周期,`settled/refunded/fundingSettled` 状态位保证幂等;`FundingSource` 接口抽象资金源,有 `WalletFunding` 与 `SubscriptionFunding` 两个实现(`service/billing_session.go:34-58`)。
- 预扣:信任额度旁路(`shouldTrust`,额度充足时预扣 0)→ 令牌预扣 → 资金源预扣,任一步失败原子回滚(`billing_session.go:130-180`)。
- 结算:`Settle(actualQuota)` 计算 `delta = actual - preConsumed`,补扣/返还,先提交资金源再调令牌额度(`billing_session.go:60-90`)。
- 退款:`Refund` 幂等、`gopool.Go` 异步执行(`billing_session.go:93-130`)。
- 工厂 `NewBillingSession` 支持 `subscription_first / wallet_first / wallet_only / subscription_only` 四种偏好及自动回退(`billing_session.go:250-330`)。

```go
// service/billing.go:40-46 — 结算入口,delta 为正补扣、为负返还
func SettleBilling(ctx *gin.Context, relayInfo *relaycommon.RelayInfo, actualQuota int) error {
	if relayInfo.Billing != nil {
		preConsumed := relayInfo.Billing.GetPreConsumedQuota()
		delta := actualQuota - preConsumed
		...
		if err := relayInfo.Billing.Settle(actualQuota); err != nil { return err }
```

**meta-gateway 借鉴点**:把"扣费"从业务代码中抽成会话对象,天然支持幂等退款与多资金源扩展;`controller/relay.go:158-165` 中 `defer` 统一退款(请求失败即退)是与流水线解耦的关键写法。

### 2. 渠道内存缓存三级索引 + 定时全量同步(热路径零 DB)
文件: `model/channel_cache.go:26-126`、`model/channel_cache.go:108-205`

- 结构:`group2model2channels map[string]map[string][]int`(按 priority 排序)+ `channelsIDM map[int]*Channel` + `channelSyncLock RWMutex`。
- 启动时 `InitChannelCache()` 全量构建,`SyncChannelCache(frequency)` 定时(默认 60s)重建;渠道增删改走 `CacheUpdateChannel / CacheUpdateChannelStatus` 增量维护(`channel_cache.go:231-245`)。
- 选择算法 `GetRandomSatisfiedChannel`:精确模型 → 归一化模型(`ratio_setting.FormatMatchingModelName`)→ **retry 次数直接映射优先级层级**(`sortedUniquePriorities[retry]`)→ 同层级内按 weight 加权随机(weight 全 0 时平滑为等权)。

```go
// model/channel_cache.go:108-111 — 内存缓存关闭时才走 DB
func GetRandomSatisfiedChannel(group string, model string, retry int, requestPath string) (*Channel, error) {
	if !common.MemoryCacheEnabled {
		return GetChannel(group, model, retry, requestPath)
	}
	channelSyncLock.RLock()
	defer channelSyncLock.RUnlock()
```

```go
// model/channel_cache.go:171-184 — 优先级=重试层级,同层内加权随机
uniquePriorities := make(map[int]bool) ...
if retry >= len(uniquePriorities) { retry = len(uniquePriorities) - 1 }
targetPriority := int64(sortedUniquePriorities[retry])
...
for _, channel := range targetChannels {
	randomWeight -= channel.GetWeight()*smoothingFactor + smoothingAdjustment
	if randomWeight < 0 { return channel, nil }
}
```

**meta-gateway 借鉴点**:热路径不碰 DB;priority/weight 双维度负载均衡;"重试次数→优先级降级"的映射天然实现了"先优后劣、同层随机"。

### 3. Ability 能力矩阵表(渠道×模型×分组物化)
文件: `model/ability.go:17-25`、`model/ability.go:186-280`

- `Ability{Group, Model, ChannelId}` 三元复合主键,附 `Enabled/Priority/Weight/Tag`,把"渠道有哪些模型、属于哪些组"物化成独立表,避免每次 JOIN channels。
- `UpdateAbilities` 在事务内 delete+重建(`ability.go:222-280`);`FixAbility` 全量 TRUNCATE 重建(`ability.go:303-360`);`UpdateAbilityStatusByTag` 支持按 tag 批量启停。
- DB 回退路径 `GetChannel`(`ability.go:108-170`)用 `MAX(priority)` 子查询取最高优先级层,再按 weight 加权随机。

```go
// model/ability.go:17-25 — 能力矩阵行
type Ability struct {
	Group     string  `json:"group" gorm:"type:varchar(64);primaryKey;autoIncrement:false"`
	Model     string  `json:"model" gorm:"type:varchar(255);primaryKey;autoIncrement:false"`
	ChannelId int     `json:"channel_id" gorm:"primaryKey;autoIncrement:false;index"`
	Enabled   bool    `json:"enabled"`
	Priority  *int64  `json:"priority" gorm:"bigint;default:0;index"`
	Weight    uint    `json:"weight" gorm:"default:0;index"`
	Tag       *string `json:"tag" gorm:"index"`
}
```

**meta-gateway 借鉴点**:模型列表维护 = ability 表去重查询(`GetEnabledModels`,`ability.go:33-45`),渠道测试/告警/上游模型同步都围绕该表;Channel 表只存配置(`model/channel.go:20-57` 含 Key/Models/Group/ModelMapping/StatusCodeMapping/Priority/AutoBan/ChannelInfo JSON 等字段)。

### 4. 重试管线:错误码元数据驱动 + 渠道自动禁用(异步)
文件: `controller/relay.go:186-250`(重试主循环)、`controller/relay.go:300-330`(shouldRetry)、`controller/relay.go:330-390`(processChannelError)、`service/channel.go:18-65`

- 完整链路:鉴权(TokenAuth 中间件)→ 预扣费(`service.PreConsumeBilling`)→ 渠道选择(`getChannel`/`CacheGetRandomSatisfiedChannel`)→ 转发(adaptor)→ 失败则 `shouldRetry` 判定 → 重试(重新选渠道)→ 成功 `SettleBilling` 结算;失败 `Billing.Refund` 退款。
- `shouldRetry`:errorCode 带 `channel:` 前缀 → 强制重试;`skipRetry` 选项 → 短路;按 status code 配置(`operation_setting.ShouldRetryByStatusCode`)。
- `processChannelError`:`ShouldDisableChannel`(自动禁用开关 + 状态码配置 + 关键字匹配,`service/channel.go:50-65`)命中后 `gopool.Go(DisableChannel)` 异步禁用并通知管理员;错误日志同样异步落库。

```go
// controller/relay.go:186-199 — 重试主循环,每轮重新选渠道
for ; retryParam.GetRetry() <= common.RetryTimes; retryParam.IncreaseRetry() {
	relayInfo.RetryIndex = retryParam.GetRetry()
	channel, channelErr := getChannel(c, relayInfo, retryParam)
	if channelErr != nil { newAPIError = channelErr; break }
	addUsedChannel(c, channel.Id)
	... // 重放 body,调用 relayHandler
	if newAPIError == nil { relayInfo.LastError = nil; return }
	processChannelError(c, *types.NewChannelError(...), newAPIError)
	if !shouldRetry(c, newAPIError, common.RetryTimes-retryParam.GetRetry()) { break }
}
```

**meta-gateway 借鉴点**:错误对象携带"是否重试/是否记日志/状态码"元数据,流水线各环节(重试、禁用、日志)统一消费同一错误;渠道禁用异步化避免阻塞请求线程。

### 5. 流式响应计费:SSE usage 提取 + 文本估算兜底
文件: `relay/channel/openai/relay-openai.go:100-170`(OaiStreamHandler)、`relay/helper/stream_scanner.go:55-260`(SSE 扫描器)

- `StreamScannerHandler`:scanner goroutine 逐行解析 `data:` 前缀,data handler goroutine 同步转发,ping goroutine 保活;`writeMutex` 保护并发写;`StreamStatus` 记录结束原因(DONE/EOF/timeout/client-gone/panic)。
- 转发的同时用 `responseTextBuilder` 累计所有 delta 文本 + tool calls;末尾 chunk 提取 usage(`ValidUsage` 校验),缺失时 `ResponseText2Usage` 用累计文本估算(工具调用按 `toolCount*7` 近似),并在流尾补发 usage chunk(`HandleFinalResponse`,`relay/channel/openai/helper.go:146-190`)。
- 结算在 `TextHelper` 完成: `service.PostTextConsumeQuota` / `PostAudioConsumeQuota` → `SettleBilling`(`relay/compatible_handler.go:25-190`)。

```go
// relay/channel/openai/relay-openai.go:105-112 — 流式计费双通道
if !containStreamUsage {
	usage = service.ResponseText2Usage(c, responseTextBuilder.String(), info.UpstreamModelName, info.GetEstimatePromptTokens())
	usage.CompletionTokens += toolCount * 7
}
applyUsagePostProcessing(info, usage, common.StringToByteSlice(lastStreamData))
HandleFinalResponse(c, info, lastStreamData, responseId, createAt, model, systemFingerprint, usage, containStreamUsage)
```

**meta-gateway 借鉴点**:上游不给 usage 也能按文本兜底计费;SSE 结束原因监控(客户端断开/超时/上游中断)是流式网关的运维关键。

### 6. 中间件管线组织:TokenAuth → 限流 → Distribute(选渠道)
文件: `router/relay-router.go`(路由挂载)、`middleware/auth.go:310-400`(TokenAuth)、`middleware/model-rate-limit.go:65-120`、`middleware/distributor.go:49-150`、`common/gin.go:56-150`(BodyStorage)

- `TokenAuth`:兼容 Bearer / `x-api-key`(Anthropic)/ query `key`(Gemini)/ WS `Sec-WebSocket-Protocol` 多协议取 key → `ValidateUserToken` → IP 白名单 → 用户缓存 → 分组可用性 → `SetupContextForToken`(token 模型限制写入 context)。
- `ModelRequestRateLimit`:总请求数(Redis 令牌桶)+ 成功请求数(窗口),支持分组级覆盖配置。
- `Distribute`:预选渠道(通道亲和性 `service.GetPreferredChannelByAffinity`)→ `CacheGetRandomSatisfiedChannel` → `SetupContextForSelectedChannel` 把渠道全部配置(Key/BaseURL/ModelMapping/ParamOverride/多 key 轮询索引)写入 context;`common/gin.go` 的 BodyStorage 让请求体可多次重读(中间件解析一次、relay 转发再读一次)。
- 鉴权/限流/选渠道全部前置到中间件,`controller/relay.go` 只消费 context,职责清晰。

**meta-gateway 借鉴点**:选渠道放中间件而非 controller;"多协议取 key"的兼容层写法;可重读 body 机制。

### 7. 系统任务调度器(DB lease 跨实例去重)
文件: `service/system_task.go:123-337`

- `SystemTaskHandler` 接口注册制(`system_task.go:34-56`),渠道测试、日志清理、上游模型更新等都注册为系统任务;`EnqueueSystemTask` 入队(`:201`),`runSystemTaskScheduler` 周期扫描(`:263`),`runWithLeaseHeartbeat` 心跳续租(`:308`),DB 锁保证多 master/多实例下同一任务只执行一次。
- `controller/channel-test.go:790` `TestAllChannels` 手动触发也走同一任务队列(已有任务运行则 409 拒绝)。
- 健康检查与告警(`service.NotifyRootUser`)均挂在此调度器上。

**meta-gateway 借鉴点**:定时任务(渠道健康检查/额度巡检/日志清理)用 DB lease 而非本地 cron,天然多实例安全。

### 8. 渠道测试复用真实 relay 管线
文件: `controller/channel-test.go:85-400`(testChannel)、`controller/channel-test.go:640-760`(自动巡检)

- `httptest.NewRecorder + gin.CreateTestContext` 构造真实请求,复用 模型映射→参数覆盖→adaptor 转换→DoRequest→DoResponse→计费日志 全链路,测试结果与生产行为一致。
- 自动巡检模式:失败计数、响应时间阈值(`ChannelDisableThreshold`)超限即禁用;`ChannelTestModePassiveRecovery` 模式只测被自动禁用的渠道,成功即恢复启用(`selectChannelsForAutomaticTest`)。

### 9. 批量更新器(额度写放大治理)
文件: `model/utils.go:33-113`

- 6 类额度变更在内存 map 聚合(每类一把锁),定时(默认 60s)统一刷库;用户维度合并 `used_quota/request_count` 为单条 SQL,避免热点行高频更新。

```go
// model/utils.go:41-49 — 内存聚合
func addNewRecord(type_ int, id int, value int) {
	batchUpdateLocks[type_].Lock(); defer batchUpdateLocks[type_].Unlock()
	if _, ok := batchUpdateStores[type_][id]; !ok { batchUpdateStores[type_][id] = value
	} else { batchUpdateStores[type_][id] += value }
}
```

### 10. 适配器工厂 + RelayInfo 大上下文(架构骨架)
文件: `relay/channel/adapter.go:14-38`、`relay/relay_adaptor.go:60-110`、`relay/common/relay_info.go:88-180`

- `Adaptor` 接口统一 `Init/GetRequestURL/SetupRequestHeader/ConvertOpenAIRequest/DoRequest/DoResponse/GetModelList/GetChannelName`,30+ 上游各一包实现,`GetAdaptor(apiType)` 工厂注册(`relay_adaptor.go`);另有 `TaskAdaptor`(异步任务:提交/轮询/计费三段式 `EstimateBilling/AdjustBillingOnSubmit/AdjustBillingOnComplete`)。
- `RelayInfo` 单对象贯穿全链路(用户/令牌/渠道/价格/计费/流状态),`InitChannelMeta` 从 context 组装渠道元数据(`relay_info.go:190-240`)。

**对比 meta-gateway 能力差距(按价值排序,new-api 明显更强项)**:① 预扣费+退款计费会话与订阅资金源;② 渠道热路径内存索引与加权随机;③ 能力矩阵与模型列表维护;④ 自动禁用+被动恢复健康检查;⑤ 错误元数据驱动重试;⑥ 流式 usage 兜底计费;⑦ 批量写聚合;⑧ 多协议(OpenAI/Claude/Gemini)互转适配层。meta-gateway 是否具备需对照其 `docs/architecture.md` 与 `internal/` 逐项确认。

## 第二部分:new-api 自身缺点(9 条)

1. **新旧计费双路径并存**: `service/quota.go` 的 `PostConsumeQuota`(约 326 行)与 `service/billing_session.go` 的 `BillingSession.Settle` 功能重叠;`PostWssConsumeQuota/PostAudioConsumeQuota`(quota.go:120-300)仍走旧路径,逻辑分叉、易修漏。
2. **字符串匹配错误分类**: `service/billing_session.go:196-199` 用 `strings.Contains(errMsg, "no active subscription")` 判断订阅错误,代码内 TODO 自认应改哨兵错误(`errors.Is`)。
3. **限流器三份重复实现**: `middleware/rate-limit.go:34-70`(redisRateLimiter)与 `:168-215`(userRedisRateLimiter)几乎逐行相同,仅 key 构造不同;`middleware/model-rate-limit.go:30-63` `checkRedisRateLimit` 又重复一遍(时间窗口 List 实现)。
4. **注释遗留死代码**: `model/channel_cache.go:87` `//channelsIDM = newChannelId2channel`(被注释的赋值);`controller/channel-test.go` `TestChannel` 中整段注释掉的 `//defer func() { if channel.ChannelInfo.IsMultiKey { go func() { _ = channel.SaveChannelInfo() }() } }()`;`middleware/distributor.go` 中注释掉的渠道一致性错误分支(`//if channel != nil {` 块)。
5. **每渠道全局互斥锁**: `model/channel.go:610-616` `GetChannelPollingLock` 用 `sync.Map` 存 per-channel `sync.Mutex`,`GetNextEnabledKey` 多 key 轮询时**同一渠道的所有请求串行**;`model/ability.go:302-303` `FixAbility` 全局 `fixLock` 串行化整个修复任务。
6. **缓存全量重建无增量**: `model/channel_cache.go:26-49` `InitChannelCache` 每次 `DB.Find(全部 channels)` + `DB.Find(全部 abilities)` 全表加载,渠道量大时同步开销高;多实例间仅靠定时轮询(无 Redis 发布订阅),一致性有秒级延迟。
7. **热路径同步扣 token 额度**: `service/quota.go:300-320` `PreConsumeTokenQuota` 每次 `GetTokenByKey`(DB 查询)+ `DecreaseTokenQuota`(同步写),未走批量更新器(`model/utils.go` 的 batchUpdate 只覆盖部分类型),高 QPS 下 `tokens/users` 表写放大与行锁竞争。
8. **巨型函数与 if-else 链**: `controller/channel-test.go` `testChannel` 单函数 400+ 行(测试构造/路径探测/多端点分发/计费日志混在一起);`middleware/distributor.go` `getModelRequest` 按 URL 路径逐段 if 解析模型(约 230-380 行),新增端点易遗漏。
9. **Adaptor 接口过胖**: `relay/channel/adapter.go:14-38` 单一接口 15 个方法,`relay/channel/submodel/adaptor.go` 等 20+ 适配器被迫为每个不支持端点写 `errors.New("... not supported")` 样板(仅 submodel 一个文件就有 7 处),缺接口隔离。
