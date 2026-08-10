# meta-gateway 性能审查(源码级,2026-08)

> 证据说明:本报告所有结论均来自实际源码阅读(非推测),每条问题标注 `文件:行号` 并附代码片段/依据。审查范围:internal/proxy、internal/relay、internal/outbound、internal/adapters(流式转换)、internal/store(SQLite 访问)、internal/ratelimit、internal/usage、internal/routing、internal/runtimeconfig、internal/observability,以及 httpapi/auth/config 等热路径相关文件。验证:`go build ./...` 通过(go 1.26.4)。报告生成日期:2026-08。

## 0. 总体结论

- **问题统计**:共 **23 个问题点**。P0 = 0,P1 = 6,P2 = 13,P3 = 4。
- **热点路径**:HTTP 入站 → auth/限流 → routing 选择 → relay → outbound → 上游 → 流式转发 → 记账(usage/proxy_logs)。
- **做得好的地方**(避免误报):
  - 出站连接池正确复用:`httpapi/router.go:90-93` / `cmd/server/main.go:98-101` 使用 `outbound.NewClient` 注入 relay,`MaxIdleConnsPerHost` 默认 64、`MaxIdleConns` 默认 512(outbound/policy.go:83-96),**不存在每请求新建 client**。`relay.New()` 的裸 Transport(relay.go:24-27,默认每主机 2 条空闲连接)仅测试使用。
  - SQLite 开启 WAL + busy_timeout + 4 连接池(store/store.go:47-56),主要表有索引(route/member/credential/proxy_logs/usage_records)。
  - Site/Credential/Group/DownstreamKey/ModelRatio 均有进程内缓存;ticker 均有 Stop;调度器生命周期管理正确。
- **核心瓶颈**:热路径上**每请求 ~5 次同步 SQLite 写往返 + ~6 次读**、**无整体出站超时**、**路由表无缓存**、**流式记账逐行全量 JSON 解析**、**限流器全局互斥**。

---

## 1. P1 级问题(6 个)

### P1-1 SQLite 热路径同步写风暴(每请求 ~5 次写往返)

- **位置**:`internal/proxy/proxy.go:676`、`:858`、`:895-914`、`:930-976`;`internal/httpapi/relay.go:297,451`;`internal/store/proxylog.go:32,48`;`internal/store/usage.go:152-190`
- **代码证据**:
  - `proxy.go:676`:`s.db.RouteMember.RecordSuccess(candidate.Member.ID, s.now())`(UPDATE route_members)
  - `proxy.go:858`:`s.db.Channel.RecordRelaySuccess(channelID)`(UPDATE channels)
  - `proxy.go:895-914` `recordAttempt` → `s.db.ProxyLog.Insert(...)`(INSERT proxy_logs,每次 attempt 一次)
  - `proxy.go:930-976` `RecordUsage` → `s.db.RecordRelayUsage(...)`:store/usage.go:163-186 事务内 INSERT usage_records + UPDATE downstream_keys 配额 + UPDATE key_groups + UPDATE proxy_logs 回填(`WHERE id = (SELECT id FROM proxy_logs WHERE request_id = ? ORDER BY id DESC LIMIT 1)`)
  - `relay.go:297/451`:`h.db.ProxyLog.UpdateMetaByRequestID(requestID, firstByteMs, clientFamily)`(同一行 proxy_logs 第 3 次写)
- **问题**:单个成功请求产生 5 次独立写往返(proxy_logs 一行被写 3 次),全部同步阻塞请求 goroutine。
- **影响场景**:SQLite WAL 单写者;`_busy_timeout=5000`(store.go:53)下写队列堆积。500+ RPS 时请求延迟被 DB 拖高,上游连接被占住,吞吐封顶在 SQLite 写速率。
- **严重度**:P1
- **修复建议**:① 把 UpdateMetaByRequestID 合并进 RecordRelayUsage 事务;② proxy_logs/usage_records 改内存队列 + 批量 INSERT(每 100ms/500 条);③ 渠道/成员健康计数(consecutive_failures)改内存原子计数 + 周期落盘。

### P1-2 出站无整体超时/响应体读超时

- **位置**:`internal/outbound/policy.go:130-151`;`internal/relay/relay.go:41-43`;`internal/proxy/proxy.go:555`
- **代码证据**:
  - policy.go:130-151:NewClient 仅设置 `TLSHandshakeTimeout`、`ResponseHeaderTimeout`、`IdleConnTimeout`、`MaxIdleConns*`;`http.Client` 未设 `Timeout`,Transport 无 `MaxConnsPerHost`。
  - relay.go:41-43:`ResponseHeaderTimeout: 60 * time.Second` 只覆盖响应头。
  - proxy.go:555:`raw, readErr := io.ReadAll(io.LimitReader(result.Body, 8<<20))` —— 响应体读无任何 deadline。
- **问题**:上游发完响应头后 body 挂起,非流式读无限阻塞(仅客户端断开 ctx 才取消);重试链 maxAttempts=2(默认)× 多 channel × 60s 头超时,单请求可占 goroutine 数分钟。
- **影响场景**:慢/坏上游(半开连接、限速器挂起)导致 goroutine 与出站连接无限累积,内存/FD 耗尽;流式场景中段挂起同样无心跳超时。
- **严重度**:P1
- **修复建议**:非流式读包一层带 idle-read deadline 的 reader;或按 attempt 用 `context.WithTimeout` 设整体上限;评估 `MaxConnsPerHost` 硬上限。

### P1-3 路由选择无缓存(每请求 2 条 SQL + 相关子查询)

- **位置**:`internal/store/route.go:296-370`(RoutingCandidates)、`:603-660`(findBestWildcardRoute)、`internal/routing/routing.go:262-320`(evaluate)
- **代码证据**:
  - route.go:296-310:先 `SELECT ... FROM routes WHERE model_pattern = ? AND enabled = 1`;未命中再 `findBestWildcardRoute`(route.go:603-630 加载**所有**含 `*`/`?` 的路由,Go 内 `matchModelPatternRunes` 递归匹配)。
  - route.go:330-370:成员查询对每行执行 `EXISTS (SELECT 1 FROM credentials pool_cred WHERE pool_cred.site_id = c.site_id ...)` 相关子查询 + 每行 `julianday('now')` 函数。
- **问题**:路由/成员/通道快照是"近静态"配置(仅 admin 写),却每请求全量重查。
- **影响场景**:高 RPS 下 routing 查询与 P1-1/P1-4 的写读叠加,4 连接池饱和;通配符模型下每请求 O(routes) 全表扫。
- **严重度**:P1
- **修复建议**:仿 CredentialStore/SiteStore 做进程内缓存(路由+成员+通道+凭据可用性),所有写路径 invalidate;wildcard 匹配结果按 model 前缀缓存。

### P1-4 DownstreamKey 认证缓存必然 miss(每请求 1 次 SQL 读)

- **位置**:`internal/store/downstream_key.go:214-222`(bumpCachedUsage)、`internal/proxy/proxy.go:976`、`internal/auth/auth.go:242-267`
- **代码证据**:
  - downstream_key.go:214-222:`func (s *DownstreamKeyStore) bumpCachedUsage(...) { s.invalidate(id) }` —— 记账后直接删缓存条目。
  - auth.go:242-267:每请求 `da.store.GetByHash(hash)`。
- **问题**:请求 N 记账 invalidate → 请求 N+1 认证必 miss → 回源 SQLite。稳态下单 key 高频流量下**每个请求**至少 1 次认证 SQL 读。
- **影响场景**:与 P1-1 叠加放大 SQLite 负载;高并发下缓存命中率≈0。
- **严重度**:P1
- **修复建议**:bumpCachedUsage 改为在缓存副本上原子更新 `QuotaUsedTokens`(原子字段或小锁),不 invalidate;仅管理端写才失效。

### P1-5 usage.Tee:流式逐行全量 JSON 解析 / 非流式全量缓冲

- **位置**:`internal/usage/usage.go:156-173`(Read)、`:236-257`(scanSSE)、`:259-283`(consumeSSELine)、`:175-195`(Close)
- **代码证据**:
  - usage.go:236-257:每个 data 行先 `t.line.Write(chunk)` + `bytes.ReplaceAll(line, []byte{'\r'}, nil)`(分配)→ `string(line)`(分配)。
  - usage.go:259-283:→ `ExtractFromSSELine` 再 `[]byte(data)`(分配)→ 整行 `json.Unmarshal`(usage.go:40-110)。
  - usage.go:156-173:每 chunk 一次 `t.mu.Lock`(单 reader 场景无必要)。
  - usage.go:175-195:非流式把**整个响应体**缓冲进 `t.buf`,Close 时 `ExtractFromJSONBody(t.buf.Bytes())` 全量二次解析。
- **问题**:万 chunk 长流 = 万次完整 JSON 解析 + 每行 3~4 次分配,而 99% 的行不含 usage;非流式 10MB 响应双份驻留内存 + 二次全量解析。
- **影响场景**:高 token 流式流量下 CPU/GC 压力显著;长文档非流式响应内存放大 2×。
- **严重度**:P1
- **修复建议**:解析前廉价预筛(`bytes.Contains(line, "usage")`);用 `bytes.Index` 手写行切分;Tee 改无锁(结束时一次快照);非流式仅解析 usage 前 64KB 或流式扫描。

### P1-6 限流器全局互斥 + 锁内全表清理

- **位置**:`internal/ratelimit/limiter.go:56-84`(Allow)、`:96-101`(cleanupIfDue);调用点 `internal/httpapi/ratelimit.go:16`、`internal/httpapi/relay.go:711-728`(checkModelRate)、`internal/httpapi/grouplimit.go:37-50`
- **代码证据**:
  - limiter.go:56-84:单把 `l.mu` 覆盖**所有 key** 的 Allow;临界区含 map 查插 + 浮点运算。
  - limiter.go:96-101:每小时 `cleanupBefore(now.Add(-l.maxIdle))` 在锁内遍历**全部桶**。
- **问题**:relay 路径每请求串行过 3 把限流锁(relayLimiter → modelLimiter → groupLimiter 外层 mu),任何时刻所有 key 的请求被同一把锁串行化;桶数 = key×model×client-family 时每小时 O(n) 清理造成尖峰停顿。
- **影响场景**:万级 RPS 时锁竞争显著;桶量大时每小时一次长停顿。
- **严重度**:P1
- **修复建议**:按 key hash 分片(16~64 shard);清理分批/摊还(每次 Allow 顺带删少量);或 tokens 用 atomic 编码。

---

## 2. P2 级问题(13 个)

### P2-7 SessionKeyFromBody 无条件整包解析

- **位置**:`internal/proxy/proxy.go:374-378`;`internal/routing/session.go:35-52`
- **代码证据**:proxy.go:374-378 在 sticky **未启用**(`STICKY_ENABLED` 默认 false,config.go)时也执行:`sessionKey = routing.SessionKeyFromBody(req.Body)`;session.go:35-52 全 body `json.Unmarshal`(解析所有 messages)+ sha256。
- **影响**:每请求额外一次全 body JSON 解析 + 哈希,纯浪费。
- **修复**:仅在 `s.sticky != nil` 时派生;或并入前导解析。严重度 P2。

### P2-8 请求体 3~5 次整包 JSON 往返

- **位置**:`internal/httpapi/relay.go:380-388`、`:462-480`(ensureStreamUsageOption);`internal/proxy/proxy.go:1416-1449`(injectSystemPrompt)、`:868-893`(rewriteModelName);adapters TransformRequest
- **代码证据**:同一 body 依次被整包解析:① 解析 model/stream(relay.go:380-388)→ ② `ensureStreamUsageOption` Unmarshal+Marshal(relay.go:462-480)→ ③ `injectSystemPrompt` Unmarshal+Marshal(proxy.go:1416-1449,配置了 system prompt 时)→ ④ `rewriteModelName` Unmarshal+Marshal(proxy.go:868-893,配置了 mapping 时)→ ⑤ 适配器转换。全部基于 `map[string]any`,小对象分配密集。
- **影响**:请求体越大分配越重;流式请求每请求 2~4 次整包往返。
- **修复**:单遍解析 + 基于 json.RawMessage 的定点改写;合并 ②③。严重度 P2。

### P2-9 每请求 6~8 次全局互斥

- **位置**:`internal/proxy/circuit_breaker.go:132-166`(EffectiveWeight)、`internal/proxy/proxy.go:135-149/155-169`(ChannelLatency/ChannelErrorRate)、`:202-231`(inflight)、`:1018-1052`(keyErrMu)、`internal/observability/metrics.go:24-30`(ObserveHTTP)
- **代码证据**:请求路径依次获取:breaker.mu → latencyMu/errorMu(×N 候选)→ inflightMu×2 → latencyMu(成功记账)→ observability.mu → 限流锁(P1-6)。
- **影响**:高并发下多个全局短临界区相互放大,锁开销占比上升。
- **修复**:EMA/breaker 改 RWMutex 或分片;observability 用原子计数。严重度 P2。

### P2-10 路由评分每请求 4N 次全局锁

- **位置**:`internal/routing/routing.go:262-320`(evaluate→scoreFor 每候选)、`:468-511`(pickLatencyAware 每候选再查 latency/error/concurrency)
- **代码证据**:N 个候选 × (scoreFor 内 latency/error 2 锁 + pick 内 2~3 锁)= 每请求 ~4N 次全局 Mutex 获取(锁实现见 proxy.go:135-169)。
- **修复**:evaluate 时一次性快照 providers。严重度 P2。

### P2-11 peekFirstChunk O(n²) 扫描 + 首字节延迟

- **位置**:`internal/proxy/proxy.go:1301-1333`
- **代码证据**:每次 4KB Read 后 `bytes.Contains(buffered.Bytes(), []byte("\n\n"))` 从头重扫全缓冲;无空行分隔的流最多缓冲 256KB(`maxStreamFirstChunkBytes = 256 * 1024`)才放行首字节。
- **影响**:首字节延迟被推迟(最多 256KB 缓冲);二进制"流"(如 stream=true 的音频)可能长时间无 `\n\n`,O(n²) 扫描放大;`FirstByteMs` 计点失真。
- **修复**:增量窗口扫描;非 SSE Content-Type 跳过 peek。严重度 P2。

### P2-12 流转换器每 chunk map[string]any + json.Marshal

- **位置**:`internal/adapters/anthropic_stream.go:87`、`:270-289`;`internal/adapters/gemini_stream.go:88`、`:196-225`
- **代码证据**:`bufio.Reader.ReadString('\n')` 每行分配 string;每事件 `json.Unmarshal` 进 `map[string]any`,再构造新 map + `json.Marshal` 输出。万 token 流 = 万组小对象分配。
- **修复**:struct 化解析 + `json.Encoder`/预分配 buffer + sync.Pool;`ReadString` 换 `ReadSlice`。严重度 P2。

### P2-13 Tee 每 Read 加互斥(单 reader)

- **位置**:`internal/usage/usage.go:156-173`
- **代码证据**:单 goroutine 顺序消费,`t.mu.Lock` 仅保护 tokens 快照,却每 chunk 加锁。
- **修复**:结束时一次快照。严重度 P2。

### P2-14 失败路径 keyErrCounts 锁内 O(n) 扫描

- **位置**:`internal/proxy/proxy.go:1018-1046`
- **代码证据**:每次 key 失败在 `keyErrMu` 锁内遍历**全部** disabledKeys 删过期项(recordKeyFailure 内 for 循环)。
- **修复**:惰性删除(仅命中时检查 TTL)。严重度 P2。

### P2-15 proxy_logs 冗余索引放大插入成本

- **位置**:`internal/store/008_proxy_log_filters.sql:4`
- **代码证据**:`CREATE INDEX IF NOT EXISTS idx_proxy_logs_id ON proxy_logs(id)` —— id 为 INTEGER PRIMARY KEY 自带索引,此索引纯冗余;proxy_logs 共 7 个索引,每 INSERT 维护全部。
- **影响**:热路径最高频写表上的写放大。
- **修复**:删除冗余索引;评估低频索引取舍。严重度 P2。

### P2-16 StickyStore 无后台清理,条目滞留

- **位置**:`internal/routing/sticky.go:61-97`、`:128-133`
- **代码证据**:Lookup/Bind 只删除"被再次访问"的过期键;`pruneLocked` 仅在管理端 Stats/Snapshot 调用。内容摘要 session key 的一次性请求(不再回访)生成的条目永不清理。
- **影响**:sticky map 缓慢无界增长(内存泄漏式)。
- **修复**:后台定时 pruner。严重度 P2。

### P2-17 burst guard 槽位释放过早 + defer 循环内累积

- **位置**:`internal/proxy/proxy.go:424`
- **代码证据**:`s.acquireChannel(...)` 后 `defer s.releaseChannel(...)` 位于 for attempt 循环体内:① 流式响应在 ForwardWithMeta 返回(响应头到达)即释放槽位,长流占用不被 burst guard 计入;② 重试多次时 defer 累积(有界)。
- **修复**:显式释放并绑定流生命周期(由 httpapi 在 copy 结束后释放)。严重度 P2。

### P2-18 observability 全局互斥 + 每请求分配

- **位置**:`internal/observability/metrics.go:24-30`
- **代码证据**:`r.mu.Lock` 覆盖全部请求计数/时长累加;`labels()` 每请求 `strings.Join` 分配。
- **修复**:分片或 per-route 原子槽。严重度 P2。

### P2-19 流式转发链内存放大(非流式 8MB 上限 + preserve 10MB)

- **位置**:`internal/proxy/proxy.go:555`(io.ReadAll 8MB)、`:1281-1299`(preserve 上限 10MB)
- **代码证据**:非流式响应先被 proxy 全量读入(8MB 上限),preserve 在失败重试路径再缓冲至 10MB;叠加 P1-5 Tee 缓冲,大响应多份驻留。
- **修复**:降低上限、错误路径只保留前 64KB。严重度 P2。

---

## 3. P3 级问题(4 个)

### P3-20 认证每请求分配

- **位置**:`internal/auth/auth.go:242-267`、`:318`
- **证据**:每请求 `NormalizeScopes`(map+slice)、`ParseModelFilter`(map+slice)、`hashToken`(sha256+hex)。
- **修复**:缓存键时预计算 scopes/filter。严重度 P3。

### P3-21 keyFingerprint 每 key 每 attempt sha256+hex

- **位置**:`internal/proxy/proxy.go:1008-1016`(keyDisabled/recordKeyFailure/recordKeySuccess 各算一次,单请求最多 3 次)
- **修复**:每 key×channel 缓存指纹。严重度 P3。

### P3-22 checkModelRate 每请求 bytes.Buffer + fnv

- **位置**:`internal/httpapi/relay.go:711-728`
- **修复**:直接对字节增量写 fnv。严重度 P3。

### P3-23 路由候选查询每行 julianday('now') 函数

- **位置**:`internal/store/route.go:349`(`AND (c.rate_limited_until IS NULL OR julianday(c.rate_limited_until) <= julianday('now'))`)
- **修复**:参数化传入 now,避免每行函数求值。严重度 P3。

---

## 4. Top 10 清单(按严重度)

1. **[P1-1]** SQLite 热路径同步写风暴(每请求 ~5 次写往返)
2. **[P1-2]** 出站无整体超时/响应体读超时(goroutine 与连接可无限累积)
3. **[P1-3]** 路由选择无缓存(每请求 2 SQL + EXISTS 子查询 + wildcard 全表扫)
4. **[P1-4]** DownstreamKey 认证缓存因记账 invalidate 必然 miss
5. **[P1-5]** usage.Tee 流式逐行全量 JSON 解析 / 非流式全量缓冲
6. **[P1-6]** 限流器全局互斥 + 锁内每小时全表清理
7. **[P2-7]** SessionKeyFromBody 无条件整包解析(sticky 未启用也执行)
8. **[P2-8]** 请求体 3~5 次整包 JSON 往返
9. **[P2-9]** 每请求 6~8 次全局互斥(breaker/EMA/inflight/observability/限流)
10. **[P2-11]** peekFirstChunk O(n²) 扫描 + 首字节延迟/二进制流 256KB 缓冲

---

## 5. 验证记录

- `go build ./...` 通过(go 1.26.4),无编译错误。
- 已核实连接池/Transport 复用正确(见"总体结论"),未将"每请求新建 client"列为问题。
- 重试/退避逻辑核实:`store/route.go:290-330` RecordFailure 指数退避正确(2^n,上限 24h);`proxy.go:1384-1398` retryAfterCooldown 尊重上游 Retry-After,不缩短基础冷却。
- ticker/goroutine 生命周期核实:discovery.go:366-367、healthsweep/service.go:145-146、alert.go:93/212、main.go runAuditCleanup 均正确 Stop;未发现 ticker 泄漏。
