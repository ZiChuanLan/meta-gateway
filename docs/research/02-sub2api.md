# Sub2API 源码级调研（源码确认，2026-08）

> 证据等级：源码级。来源：H:/WorkSpace/api/sub2api/backend

## 1. 过期服务回收 — ✅ 真实（语义=暂停非删除）

- `service/account_expiry_service.go`（87 行）：1 分钟 ticker + 启动先跑一次；`AutoPauseExpiredAccounts` → `UPDATE accounts SET schedulable=FALSE WHERE auto_pause_on_expired=TRUE AND expires_at<=now`
- 通过 scheduler outbox（`SchedulerOutboxEventAccountBulkChanged`）增量刷新调度桶
- 无 leader lock（对比 SubscriptionExpiryService 有 SetLeaderLock）；不吊销凭据、不删除账号

## 2. 幂等防重复扣费 — ❌ 伪命题（README 夸大）

- `handler/idempotency_helper.go` `executeUserIdempotentJSON` **唯一生产调用点是 API Key 创建**（`handler/api_key_handler.go:177`）；**扣费/usage billing 路径无任何幂等挂载**
- `service/idempotency.go` `IdempotencyCoordinator.Execute`：CreateProcessing 抢占（30s 锁）→ fingerprint 比对（method/route/actor/payload SHA-256，不一致 409）→ 成功回放 / in_progress / 5s 退避 Retry-After
- `DefaultIdempotencyConfig().ObserveOnly = true`（默认只观察不强制）

## 3. 供应商配额 — ⚠️ 命名误导多处

- `grok_quota_service.go` ✅ 真实：ProbeBilling（weekly+monthly 并发，502/503/504 重试 2 次）+ ProbeUsage（stream 探测 xai 限流头）+ singleflight + 429 持久化；ResetQuota=NotImplemented
- `grok_quota_fetcher.go` ⚠️ **命名误导**：不实现 QuotaFetcher 接口，是只读 extra 快照的被动解释器，不发网络请求
- `antigravity_quota_fetcher.go` ✅ 真实实现 QuotaFetcher：403 分类（validation/violation/forbidden）+ 验证 URL 提取
- `account_repo_upstream_billing_probe*.go` **不存在**：真实实现是 `service/upstream_billing_probe.go` + `repository/account_repo.go`（UpdateExtra JSONB 原子合并）+ admin handler；退避调度主体未读

## 4. failover 池模式 — ✅ 真实（生产调用点未验证）

- `handler/failover_loop.go`：同账号重试默认 3 次 500ms（401/403/429 触发）；耗尽→TempUnscheduleRetryableError→切号；SwitchCount 上限；Antigravity 换号线性递增延时；profit-veto 活锁保护（maxProfitVetoAttempts=10）
- `service/account.go` `IsPoolMode()`：API Key 账号 + credentials.pool_mode；`pool_mode_retry_count` 默认 3 上限 10；retryable status 可配置（空数组=关闭）
- 会话粘滞：切换账号时 input_tokens 转 cache_read 计费补偿（ForceCacheBilling）
- **unknown**：`NewFailoverState/HandleFailoverError` 生产调用点未在前 40 命中（可能在 openai_gateway_handler.go 分页之后）

## 真伪判定

| 项 | 判定 |
|---|---|
| account_expiry_service.go | ✅ 真实（暂停调度位） |
| 幂等挂扣费事务 | ❌ 伪（唯一调用=API Key 创建） |
| account_repo_upstream_billing_probe*.go | ❌ 不存在（真实实现另两处） |
| grok_quota_fetcher.go | ⚠️ 命名误导（被动解释器） |
| antigravity_quota_fetcher.go | ✅ 真实 QuotaFetcher |
| failover 池模式 | ✅ 真实（调用点未验证） |

## 待补读（最小集）

1. `service/upstream_billing_probe.go` 全文（退避调度主体）
2. `openai_gateway_handler.go`/`gateway_handler.go` failover 循环生产调用点
3. `gateway_usage_billing.go`+`billing_service.go` 扣款事务（幂等未接入终证）
