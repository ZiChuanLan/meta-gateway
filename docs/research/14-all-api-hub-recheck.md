# All API Hub 复检报告（meta-gateway 功能差距分析，2026）

> 依据：`H:/WorkSpace/api/all-api-hub-main`（WXT/TS 浏览器扩展）源码 + `H:/WorkSpace/api/meta-gateway` README/`internal/` 源码。凡未读源码处已标注"待确认"。
> meta-gateway 已有能力基线（避免重复）：sitedetect 四级判定链（已移植自 AAH）、check-in 调度、WebDAV AAH 信封解密拉取、Exchange 导入（canonical/New-API/AAH V2）、模型发现+自动路由、SyncKeys、usage 计量、alert + webhook notifier、审计、在线备份、metrics、重试/熔断/粘性路由。

## 一、[高价值] — 值得做进 meta-gateway

### 1. 余额历史曲线（BalanceHistory / dailyBalanceHistory）
- 描述：按天快照各账户余额，形成历史曲线；支持保留天数、按账户/标签筛选、折线图与汇总表。
- 证据：`src/features/BalanceHistory/BalanceHistory.tsx`；`src/services/history/dailyBalanceHistory/`。
- 对 meta-gateway：**数据源已存在**——`internal/alert` 的 FinanceOverview 已在周期探余额，只差"落库+画图"。SQLite 加一张日快照表 + 每日 cron + Web UI 图表即可，无外部依赖。
- 差距点：meta-gateway 只做余额低告警，不存历史，无趋势视图。

### 2. 深度用量分析（usageHistory：多级聚合/延迟直方图/慢请求/热力图）
- 描述：按 日/时/模型/token/token×模型 聚合 tokens；延迟直方图（10 固定桶）；≥5s 慢请求分析；模型×天与星期×小时热力图；增量游标摄入。
- 证据：`src/services/history/usageHistory/`；机制已在 `04-all-api-hub.md` 第 4 节源码确认。
- 对 meta-gateway：[高价值] 但为**最大工程量**。meta-gateway 已有 proxy-logs 与 usage 计量，但无聚合桶、无直方图、无热力图。建议分期：先做 按模型×天 token 聚合 + 慢请求列表（直接查 proxy_logs 即可），再做延迟直方图与热力图。
- 备注：AAH 因上游无 log id 才用"时间戳游标+指纹去重"；meta-gateway 自带 proxy_logs 有 id，摄入模型可简化，不必照搬游标。

### 3. 模型价格对比与排序（modelPricing + ModelList 价格列）
- 描述：按 New-API 公式 `inputUSD = model_ratio × 2 × groupMultiplier`、`outputUSD = inputUSD × completion_ratio`、perCall 按次计费换算，跨账户比价并"最便宜优先"排序，无价模型沉底。
- 证据：`src/services/models/utils/modelPricing.ts`、`useFilteredModels.ts`（04 号文档第 2 节已确认）。
- 对 meta-gateway：[可做]偏[高价值]。gateway 有发现到的模型快照，缺的是"每个渠道/上游的 group_ratio/completion_ratio 拉取 + 价格列 + 按价排序"。依赖上游 `group_ratio` 接口（New-API 系已有），可在发现刷新时一并拉取落库。

## 二、[可做] — 有价值但优先级低

### 4. 上游 Token 自动创建/修复（TokenProvisioning）
- 描述：在 New-API 系上游直接 `POST /api/token` 创建令牌（含分组选择、一次性密钥、创建后密钥不可再读的兜底重取）；key 失效时自动"修复"（重建）。
- 证据：`src/services/apiAdapters/contracts/tokenProvisioning.ts`（workflows: BackgroundAutoProvision/Repair；Sub2API/AIHubMix 特殊分支）。
- 对 meta-gateway：已有 SyncKeys 只能**列出/反掩码/导入已有 key**，不能创建新 token。补一个 `CreateAPIKey` adapter 即可覆盖"key 被上游删了自动重建"场景，与 KEY_FAIL_THRESHOLD 熔断互补。注意安全：需上游 admin 权限，建议仅手动触发。

### 5. 兑换码兑换（redemption）
- 描述：把兑换码经上游站点 API 兑换成账户余额，展示到账金额。
- 证据：`src/services/redemption/redeemService.ts`。
- 对 meta-gateway：gateway 本身不发行兑换码，价值有限；但 meta-gateway 已管理上游账户，复用 adapter 层加 `redeem` 能力可让"充值时不必打开浏览器"。标 [可做]（低优先）。

### 6. 客户端配置一键导出（integrations: Cherry Studio / CC Switch / CLI Proxy / Claude Code Router / Kilo Code）
- 描述：一键生成/写入各类客户端配置片段。
- 证据：`src/services/integrations/`。
- 对 meta-gateway：浏览器扩展是"写本地文件"，gateway 做不到也不该做；但 Web UI 可提供"Connect"页：给每个下游 key 展示现成的 Base URL + Key + 各客户端贴入步骤。轻量、实用。

### 7. 站点公告拉取（siteAnnouncements）
- 描述：拉取上游站点公告，支持未读过滤与标记已读。
- 证据：`src/services/apiAdapters/contracts/siteAnnouncements.ts`。
- 对 meta-gateway：轻量 widget（Dashboard 显示上游站点公告），复用 outbound 安全策略即可。价值一般。

### 8. 标签体系（tags）
- 描述：给账户/模型打标签并筛选。
- 证据：`src/services/tags/tagStorage.ts`。
- 对 meta-gateway：渠道/凭据加 tags 列 + Web UI 筛选，成本低。价值一般。

### 9. 引导式添加站点（accountSiteOnboarding）
- 描述：粘贴 URL → 自动检测 → 逐步引导补全凭据。
- 证据：`src/services/accountSiteOnboarding/`。
- 对 meta-gateway：sitedetect 已移植，缺的是 Web UI 里的"添加站点向导"。轻量 UI 改进。

## 三、[不适合] — 浏览器专属 / 产品形态不符 / 外部依赖

10. Cloudflare challenge 助手（浏览器自动过 CF 验证）
11. Web 嗅探/快速捕获（浏览器专属）
12. 远端站点渠道管理（ManagedSiteChannels/ModelSync/Verification）——"以游客身份管理别人的 New-API 站点"，与 meta-gateway "导入后自管"形态不符（仅目录级证据）
13. LDOH 站点目录检索（社区生态专属）
14. 快照分享（ShareSnapshots）——涉隐私，无社交场景
15. 站点书签（浏览器书签概念）
16. 浏览器会话认证（依赖浏览器 cookie）
17. 产品遥测与版本公告（gateway 无商店分发渠道）
18. 扩展权限管理/弹窗提示/MeshGradientLab（浏览器 UI/装饰）
19. 多平台签到扩展（anyrouter/veloera/wong 均 New-API 系，meta-gateway 现有 checkin adapter 大概率已兼容，待实测确认）

## 四、已覆盖项（防止重复建设）

sitedetect 四级判定链（`internal/sitedetect/`）、WebDAV 加密信封拉取（`internal/webdavsync/`）、Exchange 导入 AAH V2（`internal/exchange/`）、签到调度与日志（`internal/checkin/`）、余额低/令牌过期/签到失败告警 + webhook（`internal/alert/` + `internal/webhook/`）、SyncKeys 上游 key 同步（`internal/account/service.go:255`）、usage 计量（`internal/usage/`）、模型发现与自动路由（`internal/discovery/`）。

## 五、Gaps（未核实项，供父任务跟进）

- `src/services/checkin/autoCheckin/externalCheckInService.ts` 具体机制未读。
- `ManagedSiteChannels/ModelSync/Verification` 三个 feature 仅目录级证据。
- `keyManagement.ts`、`tokenProvisioningModel.ts` 细节未读。
- AAH 签到 providers 与 meta-gateway New-API checkin adapter 的实际接口兼容性未实测。
- BalanceHistory 与 meta-gateway `internal/account` FinanceOverview 的数据结构对齐未核对。

**主要结论**：真正值得 meta-gateway 借鉴的只有三块——余额历史曲线（数据源已具备，改动最小）、深度用量分析（工程量大，可分期）、模型价格对比列（需补 group_ratio 拉取）；其余多为浏览器专属或产品形态不符。
