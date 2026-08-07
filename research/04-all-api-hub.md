# All API Hub 源码级调研（源码确认，2026-08）

> 证据等级：源码级（除少量标注 unknown）。来源：H:/WorkSpace/api/all-api-hub-main（WXT/TS 浏览器扩展）

## 1. 智能站点识别 — ✅ 属实（倍率非 URL 嗅探）

- `src/services/siteDetection/detectSiteType.ts` `getAccountSiteType()`：四级链 = ①域名精确匹配 → ②根页 `<title>` 正则（15 种站点，`-`→`[-_ ]?` 容错，词边界）→ ③Sub2API `/api/v1/auth/me` 端点形状 → ④New-API 系**认证报错文案 + compat 头反推白标站**（New-API-User / X-Api-User / Rix-Api-User）
- `autoDetectService.ts` `autoDetectSmart()`：标签页→background→直接 API 三级级联
- **措辞陷阱**：README 的"粘贴 URL 探测计费倍率"是夸大——URL 只判站点架构，`group_ratio` 是后续 API 拉的（`apiService/oneHub/transform.ts`）

## 2. 模型价格换算 + 排序闭环 — ✅ 属实

- `services/models/utils/modelPricing.ts`：`NEW_API_QUOTA_PER_USD=500_000`，`baseUSD = 1e6/500k = 2 USD/1M`；`inputUSD = model_ratio × 2 × groupMultiplier`；`outputUSD = inputUSD × completion_ratio`；有 `token_price_usd_per_million` 直填价则优先；perCall 按次计费 × groupMultiplier（DONE_HUB_TOKEN_TO_CALL_RATIO=0.002）
- `useFilteredModels.ts` 排序：可比较价格键（primary=input / secondary=output，realPrice 时用 CNY），**null 沉底**；`MODEL_CHEAPEST_FIRST` 先按模型名归组再组内升序；平局键序 `billingMode → effectiveGroup → model_name → source`；按 `model_name+billingMode` 分组标记 isLowestPrice
- 无价模型不参与比价、排序固定沉底

## 3. 多渠道批量验证 — ✅ 属实（顺序串行，非并发批量）

- `services/verification/aiApiVerification/suiteRunner.ts` `runApiVerificationSuite`：**顺序 await**（无并发）；models probe 抓全模型列表 → `pickSuggestedModelId`（前缀 gpt/o、claude、gemini 猜推荐模型）→ 4 个能力 probe（text-generation "Reply with exactly: OK" / tool-calling verify_tool 强制 toolChoice / structured-output zod literal / web-search）
- 三形态模型抓取：OpenAI `/v1/models`、Anthropic `after_id` 分页（200/页×20 页 2000 上限）、Google `pageToken` 分页 + strip `models/`
- token 兼容判定（`models/utils/tokenModelCompatibility.ts`）：`status===1` + group 匹配（enableGroups）+ allow-list（`models` 字段优先，其次 `model_limits_enabled&&model_limits`）
- CLI 仿真：claude/codex/gemini 固定 API 族，复用 tool-calling probe
- 安全：`redactSecrets` [REDACTED]、结构化 status 分离、input/output 永不含 apiKey；runId + AbortController 取消
- **可能夸大**：README 若指并发批量——源码是顺序串行

## 4. 深度用量分析 — ✅ 属实

- 增量摄入：New-API 无 log id → 游标用 `lastSeenCreatedAt` + 边界时间戳指纹去重；store 只存聚合桶无原始日志
- 多级聚合键：daily（本地时区）/ hourly（00-23）/ model / token / token×model；延迟四路：latencyDaily/ByModel/ByToken/ByTokenByModel
- 慢请求：`USAGE_HISTORY_SLOW_THRESHOLD_SECONDS=5`（use_time≥5s）；延迟直方图固定 10 桶 `[0.25,0.5,1,2,3,5,8,13,21,34,∞)`
- 热力图：模型×天（dailyByModel.totalTokens）、星期×小时（hourly，星期=UTC weekday）
- 聚合在导出时融合（`computeUsageHistoryExport` + addToAggregate/addToLatencyAggregate），图表 resolver 纯函数

## 5. WebDAV 加密备份信封 — ✅ 属实（AAH envelopes）

- `webdavBackupEncryption.ts`：`{type:"all-api-hub-webdav-backup-encrypted", v:1, kdf:"PBKDF2", cipher:"AES-GCM", iter:250_000, salt(16B), iv(12B), ct}` 全 base64；每次备份随机 salt+iv；`extractable:false` 密钥不落盘
- 加解密封装在 `webdavService.uploadBackup/downloadBackup` 内，autoSync 无感知；下载兼容明文旧备份
- 自动同步：browser.alarms 周期（clamp 1-1440 分钟）+ 1 分钟 best-effort 上传 + isSyncing 防重入；合并策略 merge/download_only/upload_only，最新者胜，含墓碑记录；withExtensionStorageWriteLock + 失败回滚
- 服务端适配：信封协议可直接复用（Node crypto.subtle 同协议）；MV3 alarm 可简化为 singleflight + 幂等 key

## 真伪判定

| 机制 | 判定 |
|---|---|
| URL 探测平台架构 | ✅ 属实（四级判定链） |
| URL 探测计费倍率 | ⚠️ 夸大（倍率是 API 拉的 group_ratio） |
| 模型有效价格对比排序 | ✅ 属实（公式+排序闭环完整） |
| 批量验证 | ✅ 属实（但顺序串行，非并发） |
| 深度用量分析 | ✅ 属实（游标摄入+多级聚合+10 桶直方图） |
| WebDAV 加密信封 | ✅ 属实（AES-256-GCM + PBKDF2 250k） |

## 待补读（可选）

- `usageHistory/sync.ts`（拉取/游标落盘细节）、`webdavSelectiveSync.ts`、`modelPricingCache.ts`
