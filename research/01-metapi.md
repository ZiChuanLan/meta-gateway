# Metapi 源码级调研（源码确认，2026-08）

> 证据等级：全部源码级（文件+函数+代码片段）。来源：H:/WorkSpace/api/metapi

## 1. 四级成本信号链 — ✅ 属实

- 文件：`src/server/services/tokenRouter.ts`，`resolveEffectiveUnitCost()` (L1574+)
- 回落顺序：`observed`（实测 totalCost/successCount）→ `configured`（账号 unitCost）→ `catalog`（定价缓存）→ `fallback`（config.routingFallbackUnitCost）
- `source` 字段进路由日志；fallback 时额外乘 `1/max(1,unitCost)` 惩罚
- 定价缓存：`modelPricingService.ts` `getCachedModelRoutingReferenceCost`，key=siteId:accountId，TTL 过期走兜底

## 2. 40/30/30 概率分摊 — ✅ 数值属实（可覆盖）

- `src/server/config.ts` L164-170：`costWeight:0.4 / balanceWeight:0.3 / usageWeight:0.3`（环境变量可覆盖）
- `tokenRouter.ts` `calculateWeightedSelection` (L3530+)：`valueScore = costWeight*(1/unitCost) + balanceWeight*balance + usageWeight*(1/recentUsage)`
- 注意：最终概率还叠加站点全局权重/运行时健康/历史健康/会话负载修正，非纯 40/30/30

## 3. 健康状态机 — ⚠️ README 说四档，实为五档（多 unknown）

- `accountHealthService.ts`：`RuntimeHealthState = 'healthy'|'unhealthy'|'degraded'|'unknown'|'disabled'`
- 站点级联禁用 ✅：`sitesStatusSideEffects.ts` L34 `UPDATE accounts SET status='disabled' WHERE siteId=?`（有测试）
- 凭证自动续签 ✅：`accountsLoginWorkflow.ts` `autoRelogin` 凭据（username+passwordCipher 加密存储）

## 4. 余额兜底 — ⚠️ 部分属实（有夸大）

- **属实**：`fetchTodayIncomeFromLogs` 用 `/api/log/self` 分页拉今日收入日志 `quota/500000` 换算（仅 new-api/anyrouter/one-api/veloera；veloera 用 1M）；`resolveProxyUsageWithSelfLogFallback` 用 self-log 恢复缺失的 usage（时间窗±90s/延迟差≤12s/模型名/token 值四重匹配，支持 done-hub/one-hub/new-api/anyrouter/sub2api）
- **不存在**："用请求日志直接重建 balance 字段"——balance 失败路径是置 unhealthy + 告警 + 重登录，保留旧值
- 换算系数写死 500_000（veloera 1_000_000），未从站点配置读取

## 5. 告警体系 — ✅ 属实

- `notifyService.ts` L17：`'webhook'|'bark'|'serverchan'|'telegram'|'smtp'`；webhook 自动识别企微/飞书
- 冷却 `notificationThrottle.ts`：键=level+title+message 完整拼接（同内容才合并），进程内 Map，合并时回注"已合并 N 条"，`pruneNotificationThrottleState` 清理
- 每日摘要 `dailySummaryService.ts`：cron 默认 23:58，含账号/签到/代理/todaySpend/todayReward/净值

## 6. 模型广场 — ⚠️ 未证实（三个独立服务存在，整合层未找到）

- `modelPricingService.ts`：按 site:account 缓存 10 分钟（失败 60s），拉 /api/pricing 或 /api/available_model+group_map，one-hub 形态归一化（model_ratio=1, completion_ratio=output/input）
- `modelAnalysisService.ts`：纯内存聚合 proxy_logs（7 天窗口 top10），产出 callRanking/successRate/avgLatencyMs/spend/tokens；排序为 calls 降序 + spend 分布
- `modelService.ts`：模型发现（apiToken→discoveredApiToken→accessToken→managed token 链）+ probeSiteModels 实测（latency 阈值判 unsupported）
- "广场"整合 UI 层未确认存在

## 真伪判定

| 机制 | 判定 |
|---|---|
| 四级成本链 | ✅ 完全属实 |
| 40/30/30 | ✅ 数值属实（叠加修正项） |
| 健康状态机四档 | ⚠️ 实为五档（多 unknown） |
| 余额日志兜底 | ⚠️ 部分属实（兜 todayIncome/usage，不重建 balance） |
| 告警 5 通道+冷却+日摘要 | ✅ 属实 |
| 模型广场 | ⚠️ 三个服务存在，整合层未找到 |
