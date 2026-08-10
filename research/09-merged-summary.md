# meta-gateway 全景审查合并汇总(2026-08)

> 汇总依据:05 代码质量(61 项)、06 性能(23 项)、07 new-api-main 分析、08 差距复核(36 项)、docs/competitive-review.md、OPTIMIZE.md。全部结论源码级,抽样复核属实。

## 一、项目定位区别

| 项目 | 技术栈 | 定位 | 与 meta-gateway 的核心区别 |
| --- | --- | --- | --- |
| meta-gateway | Go + SQLite + 原生 React WebUI | 单二进制轻量聚合网关 | 零外部依赖(PG/Redis),协议转换走"中间格式 pivot"适配器链 |
| new-api | Go + Gin + GORM + MySQL | 经典分销网关 | 重型依赖;计费/渠道/调度体系最完整,是 meta-gateway 的主要对标 |
| sub2api | Go + Ent + PG + Redis | 订阅额度分发网关 | 账号调度状态机、支付/兑换闭环,数据层最规范(Repository 接口) |
| metapi | Node + Fastify + Drizzle | 中转站的中转站(元聚合) | 智能路由、多 key 健康管理、分级冷却 |
| axonhub | Go + ent + GraphQL | 企业级 AI 网关 | 条件引擎、管道中间件、RBAC,架构分层最干净 |
| all-api-hub | TS + WXT 扩展 | 多站点资产管理客户端 | 非网关;站点识别/比价/签到,前端 feature-folder 模式 |

## 二、meta-gateway 自身问题(P0=0,无编译级问题;build/vet 全绿)

### 死代码(21 项,P1 代表)
- `proxy.go:1111 classify`、`proxy.go:786 resolveAPIKey`、`proxy.go:352 Service.Forward`、`relay.go:142 DecodeJSONRequestBody`、`site_profile.go:35 siteProfileEntry`、`webdavsync/client.go Client.Probe` — 全仓库 0 生产调用,直接删
- **功能死代码**:`adapters/openai.go:34 Error.RetryAfter` 生产从不赋值 → "429 按上游 Retry-After 延长暂停"永远走默认 60s 分支,功能静默失效
- `httpapi/relay.go:521 writeUpstreamResult` 的 `clientFamily` 死参数

### 垃圾代码(21 项,P1 代表)
- `backup/service.go` Create() 7 个失败分支全返回 `errors.New("backup failed")`,底层根因全丢,运维无法定位
- `gofmt -l` 24 个未格式化文件(CI 有 gofmt 门禁,说明最近合入绕过了)
- 魔数堆积:`5`/`30*time.Minute`/`1200*time.Millisecond`(两处)/`quotaPerUnit=500000`/`8<<20`/`10*1024*1024`

### 重复代码(19 项,P1 代表)
- `firstNonEmpty` ×3(account/anthropic/config,签名还不一致)、`platformUserID`/`persistPlatformUserID`/`zero` 跨 account+checkin 双份
- `httpapi/relay.go` forwardPassthrough 与 forwardModelRequest 后半段大段复制粘贴
- `store/route.go` listCandidatesByRoute 与 RoutingCandidates 约 60 行 SQL+Scan 整块复制
- 4 份近同的 `ErrorKind+Error` 错误类型、3 份 content 扁平化、4 份 base URL 校验拼接、3 个加权抽签函数

### 性能(23 项,P1=6)
| # | 问题 | 影响 |
| --- | --- | --- |
| P1-1 | 热路径每请求 ~5 次同步 SQLite 写往返(proxy_logs 一行写 3 次) | 500+ RPS 时吞吐封顶在 SQLite 写速率 |
| P1-2 | 出站无整体超时、响应体读无 deadline | 坏上游挂起时 goroutine/连接无限累积 |
| P1-3 | 路由选择无缓存,每请求 2 条 SQL + 通配符全表扫 | 高 RPS 下 4 连接池饱和 |
| P1-4 | DownstreamKey 记账后 invalidate 缓存 → 认证每请求必 miss 回源 | 缓存命中率≈0 |
| P1-5 | usage.Tee 流式逐行全量 JSON 解析(万 chunk=万次解析,99% 无 usage);非流式整包缓冲二次解析 | 高 token 流 CPU/GC 压力,10MB 响应内存 2× |
| P1-6 | 限流器单全局互斥 + 锁内每小时全表清理 | 所有 key 的请求被同一把锁串行化,桶大时尖峰停顿 |

## 三、其他项目优点 → 可借鉴(对照 08 复核:✅20 / ⚠️12 / ❌12)

### 已落地 ✅(不用再做)
分级冷却+断路器、粘性会话、多 key 轮换+失败剔除、渠道 Tag 批量运维、智能站点识别、结构化审计、WebDAV 备份、灰度池 stable_first、健康度打分选路、中间格式协议转换、Retry-After 冷却

### 最值得补的 5 个差距 ❌
1. **usage_log 三字段双写**(sub2api 模式):mapping 改写后的真实上游模型名不落库 → 排障盲区;改动最小(两列+一处记录),建议最先做
2. **TransformOptions**(axonhub):reasoning_effort 值域映射、role 替换不可配,正是 Gemini/Anthropic 适配器痛点,当前只进日志不进转换
3. **错误透传规则表**(sub2api):非 OpenAI 适配器压错只能改代码重编译
4. **路由决策快照持久化**:只有 live explain,无历史,"为什么走这个渠道"无法审计
5. **逐 token 模型可用性探测**:Discovery 只列模型,不能回答"这个 key 能不能跑 gpt-4o"

### new-api 独有强项(meta-gateway 最缺的体系)
1. BillingSession 预扣/结算/退款生命周期 + FundingSource 接口(钱包/订阅双资金源)
2. 渠道三级内存索引(group→model→channels),热路径零 DB,retry 次数直接映射优先级层级
3. Ability 能力矩阵表物化渠道×模型×分组,模型列表维护零 JOIN
4. 流式计费双通道:SSE usage 提取优先 + 累计文本估算兜底,流尾补发 usage chunk
5. 批量更新器:6 类额度变更内存聚合定时刷库(meta-gateway 的 P1-1 写风暴正缺这个)
6. 系统任务 DB lease 跨实例去重 + 心跳续租
7. 渠道测试复用真实 relay 管线 + 自动禁用阈值 + 被动恢复
8. 中间件管线 TokenAuth→限流→Distribute 选渠道,BodyStorage 可重读

### new-api 自身 9 条缺点(反例,避免踩同样的坑)
新旧计费双路径并存、字符串匹配判错误、限流器三份重复实现、注释遗留死代码、per-channel 全局互斥锁(同渠道请求全串行)、缓存全量重建无增量、热路径同步扣 token 额度、巨型函数(testChannel 400+ 行)、Adaptor 接口过胖(15 方法,适配器被迫写 7 处 not supported 样板)

## 四、建议执行顺序(与 OPTIMIZE.md 的 P0-P6 阶段衔接)

1. **止血(一天内)**:删 6 个死函数;`RetryAfter` 在 429 分支赋值或删字段;backup 改 `%w` 包装;`gofmt -l` 24 个文件格式化
2. **去重**:firstNonEmpty/platformUserID/persistPlatformUserID/zero 收敛到 `internal/xutil`;relay.go 两 handler 抽公共函数;store/route.go 重复 SQL 合并
3. **性能(对齐 P1-1/2/3/4/6)**:proxy_logs 内存队列批量写(借 new-api 批量更新器思路);DownstreamKey 记账改原子更新不 invalidate;出站加整体超时;路由表进程内缓存
4. **功能差距(对齐 08 复核 top5)**:usage_log 三字段 → TransformOptions → 错误透传规则表 → 决策快照 → 逐 token 探测
5. **体系(可选大项)**:BillingSession 计费会话、Ability 矩阵、DB lease 系统任务

## 五、文档状态提醒

- `OPTIMIZE.md` 曾处于未提交删除状态(疑似误删),已 `git checkout` 恢复;建议确认是否要保留
- `docs/competitive-review.md`(08-02)与 08-gap-check(08-08)状态一致:36 项中 20 项已落地,可考虑把 competitive-review 中已完成项标记归档,避免新旧两份清单并存
