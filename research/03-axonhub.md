# AxonHub 机制提取报告（移植级别，2026-08）

> 证据等级：全部源码级（六个机制均直接打开源码确认）。来源：H:/WorkSpace/api/axonhub

## 1. 模型级三态熔断器（closed/half-open/open）

文件：`internal/server/biz/model_circuit_breaker.go`

- 策略默认：HalfOpenThreshold=3, OpenThreshold=5, FailureStatsTTL=30m, ProbeInterval=5m, HalfOpenWeight=0.3
- RecordError：TTL 检查防僵尸计数（30m 无新失败归零）→ 连续失败 ≥5 进 open（设 NextProbeAt），≥3 进 half-open
- **只有 wasProbe=true 的失败推进指数退避**（`nextInterval = ProbeInterval × 2^probeAttempts`，封顶 8 倍）——被熔断拒绝的普通请求不算，否则恢复被无限推迟
- RecordSuccess：单次成功全复位（closed、计数 0、NextProbeAt 清空）
- GetEffectiveWeight 懒放行：open 且过 NextProbeAt 且无探测进行中 → 放行唯一探测（权重 0.3），否则 0；TTL 懒自愈（double-check 复位，无后台 goroutine）
- 探测互斥：`probingInProgress` 原子位 CAS 抢占
- 接线（orchestrator/model_circuit_breaker.go）：请求前 TryBeginProbe（失败返回 errSkipCandidateByCircuitBreaker）→ 流式首个 completion token 到达 RecordSuccess → 错误路径先捕获 wasProbe 再 RecordError
- 打分（lb_strategy_model_aware_circuit_breaker.go）：`Score = GetEffectiveWeight × 200`，>0 时 `+= rand×0.5` 打平随机化；无 modelID 中性分 100

移植接口（可直接照搬）：
```go
type CircuitBreaker interface {
    RecordError(ctx, channelID int, modelID string, wasProbe bool)
    RecordSuccess(ctx, channelID int, modelID string)
    GetEffectiveWeight(ctx, channelID int, modelID string, baseWeight float64) float64
    TryBeginProbe(ctx, channelID int, modelID string) bool
    EndProbe(channelID int, modelID string)
    GetStats(ctx, channelID int, modelID string) *CircuitBreakerStats
}
```

## 2. 按 API Key 粒度自动禁用

文件：`internal/server/biz/channel_auto_disable.go` + `channel_apikey.go`

- 三元组计数：`map[channelID]map[apiKey]map[statusCode]int`，失败自增、**成功即删**（非 TTL）
- 达阈值（策略 Statuses[].Times）→ DisableAPIKey：key 不在 credentials 忽略、已禁用幂等；enabled keys 归零 → **级联禁用 channel**
- 一致性链：本地同步 `enabledChannelsCache.Load(force=true)` → `asyncReloadChannels()` 跨实例广播（live.NewForceRefreshEvent）→ webhook（context.WithoutCancel + goroutine）
- DB 幂等：UPDATE 带 `status=ENABLED` WHERE 条件
- 策略配置：`RetryPolicy.AutoDisableChannel{Enabled, Statuses[]{Status, Times}}` JSON 持久化系统表

## 3. 可配置重试条件

文件：`internal/server/orchestrator/retry.go`（全文件 70 行）+ `objects/channel.go` L202-220

- 判定顺序：默认集（429 + 5xx，`llm/httpclient/utils.go` IsHTTPStatusCodeRetryable）→ 渠道自定义 statusCodes → 渠道自定义 pattern（子串大小写敏感 / 正则 RE2）
- 正则编译失败在写入期拒绝（NormalizeRetryableStatusCodes/Patterns：克隆→校验 400-599→sort→去重；pattern TrimSpace+Compile 校验+去重）
- `ExtractStatusCodeFromError`：errors.As 链兼容 httpclient.Error 与 llm.ResponseError

```go
type RetryableErrorPattern struct { Pattern string; Regex bool }
```
判定函数与配置结构可直接复制，换掉 errors.As 分支即可。**P0 移植**（纯函数零依赖，半天落地）。

## 4. 流式质量指标（TTFT/TPS EWMA + ring buffer）

文件：`internal/server/biz/channel_metrics.go` + `pkg/ringbuffer/ringbuffer.go`

- 常量：`latencyEWMAAlpha=0.3`、`defaultPerformanceWindowSize=600s`、`MinLatencyMs=10`（前端共享契约 MINIMUM_LATENCY_MS_FOR_CACHE_HITS）
- 双 EWMA：流式 = StreamingFirstTokenLatencyEWMA + StreamingTokensPerSecondEWMA；非流式 = NonStreamingLatencyEWMA；首样本直接赋值
- TPS：`tokens/(effectiveLatency/1000)`；流式 effectiveLatency = latency − TTFT，**ClampLatency(10ms) 防缓存命中 TPS 无穷大**
- ring buffer：数组 + timestamp→下标 map（Get O(1)），容量满触发 cleanupExpiredSlots O(k) 批量扣减（Range 有序提前终止）
- 冷启动：单条 GROUP BY channel_id 查近 6h 只回填 RequestCount/LastFailureAt，EWMA 运行时积累
- 建议：泛型 ringbuffer + AggregatedMetrics + 魔数原样保留（10/0.3/600）

## 5. 选择时立刻计数 + perfCh 异步落盘

- `IncrementChannelSelection`（load_balancer.go L192 调用）：选中瞬间 RequestCount++（同步短锁）→ 突发并发下后续选择立即避开
- `perfCh chan *PerformanceRecord` 容量 1024 非阻塞投递；单一消费者 goroutine 串行化 RecordPerformance（无锁一致性）
- 账本约定：选择时 ++ / 结束时经秒槽入账 / 取消时对称回滚 --（**必须整体移植，否则滑动窗口漂移**）

## 6. 单条 CTE 批量吞吐聚合

文件：`internal/server/gql/qb/throughput.go` + `throughput_daily.go`

- ROW_NUMBER CTE：`ROW_NUMBER() OVER (PARTITION BY request_id ORDER BY created_at DESC)` + `WHERE rn=1` 去重（重试只取最新）；老库 MAX(id) 相关子查询等价
- 吞吐公式：`SUM(tokens)×1000 / SUM(effective_latency)`；流式 effective = latency − TTFT（钳 0）；token 含 completion+reasoning+audio
- 批量：所有 channel 一条 CTE（`channel_id IN (...)` 参数化，PG 从 $3 起）
- 时间戳对齐：`now.Truncate(interval)` 槽边界；应用层生成期望序列 + map 回填缺失补 0
- 置信度：<100 请求→low，ratio≥1.5 且 ≥500→high

## 移植优先级

| 优先级 | 机制 | 理由 |
|---|---|---|
| P0 | 3. 可配置重试条件 | 纯函数零依赖，直接改善重试正确性 |
| P0 | 4. 流式质量指标 | 自包含，是 1/5 的数据底座 |
| P1 | 1. 三态熔断器 | 价值最高，需接 3 个接线点 |
| P1 | 5. 选择即计数 | 依赖 4，账本约定需整体移植 |
| P2 | 6. CTE 吞吐聚合 | SQL 可复制，依赖事实表 |
| P2 | 2. Key 粒度禁用 | 依赖缓存+广播+webhook 基建 |

## 待读

- `channel_cache.go`（enabledChannelsCache 强制刷新去抖）
- `webhook_notifier*.go`（分发/重试/超时）
- `channel_metrics_test.go`/`model_circuit_breaker_test.go`（现成验收用例）
