# 五项目二次调研综合汇总（2026-08-10）

> 本次为第二轮调研（10-14 号文档，源码级），对照第一轮结论（08-gap-check：✅20/⚠️12/❌12 + 09 合并汇总）去重后输出。筛选标准：meta-gateway = Go 单二进制 + SQLite + React 控制台、单实例、无 Redis、个人/小团队。

## 一、跨项目共识的最值得借鉴项（按投入产出排序）

| # | 功能 | 来源项目 | 命中缺口 | 工作量 |
|---|---|---|---|---|
| 1 | **模型元数据表 + 缺失模型检测**（图标/厂商/名称规则/配额类型；渠道引用但未登记模型差集提示） | new-api #31/#32 | 08 #22 + #34（双 ❌） | 小（store 表 + 差集查询 + 前端） |
| 2 | **分项倍率定价**（cache_read/cache_write/image/audio 独立单价；meta 现在 cache 按 prompt 价算） | new-api #23/#25 | 08 #13 ⚠️ | 小（价目表加列 + 计费分项） |
| 3 | **Reasoning Effort 值域映射 + 模型名后缀自动映射**（`-max/-xhigh` 后缀剥离转 reasoning_effort；Claude Code 直接受益） | axonhub #5 + new-api #20 | 08 #2 ❌ | 半天~1 天 |
| 4 | **错误透传规则表**（错误码+关键词 → 透传/改写/跳过监控，热更新） | sub2api | 08 #4 ❌ | 中（表 + 匹配器 + 接线） |
| 5 | **路由决策快照持久化**（proxy_logs 记候选/打分/选中策略 JSON） | metapi #2 | 08 #17 ⚠️ | 小（一列 + 写入点） |
| 6 | **渠道主动健康监控历史 + 可用率窗口聚合**（探测结果落库 + 日聚合 + 视图） | sub2api | 08 #24 ⚠️ | 中 |
| 7 | **模型 not_found/协议不支持 → 渠道×模型不可用标记**（避免白耗配额） | sub2api | 08 #3 ⚠️ | 小（classify 分支 + 熔断接线） |
| 8 | **首输出/首字节超时保护**（流式首 chunk 超时即 failover） | sub2api | 性能 P1-2 | 小 |
| 9 | **兑换码体系**（给 DownstreamKey 加配额的一次性凭证） | new-api #5 + sub2api | 08 #26 ❌（半截） | 小（单表 + 两端点） |
| 10 | **签到奖励解析**（从返回文本正则提取奖励金额，落日志） | metapi #7 | 08 #26 增强 | 极小（纯函数） |
| 11 | **余额历史曲线**（FinanceOverview 每日快照落库 + 折线图） | all-api-hub | —（新） | 小（日快照表 + cron + UI） |
| 12 | **Key 自助查额度**（OpenAI credit_summary 兼容只读端点） | new-api #10 | —（新） | 小 |
| 13 | **upstream_request_id 落库 + 日志筛选**（排障"上游到底是谁"） | new-api #13 | —（新） | 小 |
| 14 | **API Key Profiles 精简版**（每 Key 模型映射，Claude Code 固定模型名→便宜模型） | axonhub #1 | —（新） | 中 |
| 15 | **Body 改写操作链**（set/set_if_absent/delete/array_prepend 等，渠道级） | axonhub #2 + metapi #11 | 08 #6 ⚠️（body 部分） | 中 |
| 16 | **用量预聚合 / 批量写入**（写路径聚合 + 定时投影；内存聚合定时刷库） | metapi #3 + sub2api worker pool + new-api #27 | 08 #20 ❌ + 性能 P1-1 | 中（性能改造） |
| 17 | **指标阈值告警规则引擎**（metric/operator/threshold/window/sustained，可配规则） | sub2api | —（新，事件告警之外） | 中 |
| 18 | **模型比价排序**（同模型跨渠道价格排序 + 最便宜标注） | all-api-hub #3 + metapi #13 | 08 #31 ⚠️ | 小（前端） |
| 19 | **系统代理出口**（全局/每站点代理，国内直连痛点） | metapi #4 | 08 #29 ❌ | 中（安全敏感） |
| 20 | **TOTP 2FA**（管理面可选加固） | new-api #3 + sub2api | —（新） | 小 |
| 21 | **敏感 prompt 保护**（渠道标记 + 内容匹配排除候选 / 出境防护） | new-api #37 + sub2api | 08 #36 ⚠️ | 中 |
| 22 | **逐 token/账号模型可用性探测**（"这个 key 能不能跑 gpt-4o"） | metapi #14 | 08 #18 ❌ | 中 |
| 23 | **定时模型同步 / 渠道复制 / 全局搜索 / 工厂重置** | axonhub #12/#13 + metapi #15/#16 | — | 各小 |

## 二、明确不适合（防重复调研）

- 订阅账号池 + OAuth 逆向（ChatGPT/Claude/Gemini/Grok/Antigravity）—— 与 API-key 渠道定位冲突
- 支付/订单/退款/促销/分销/订阅套餐 —— 个人网关无售卖场景
- 用户注册 + 多 OAuth 登录 + Turnstile —— 无公开注册
- RBAC/Projects/OIDC SSO —— 单管理员模型
- 多数据库（PG/MySQL）—— 破坏零依赖哲学
- Electron 桌面 / 更新中心（k3s）/ 视频生成 / OpenAI Realtime / Midjourney/Suno
- 代理池 + TLS 指纹 —— 与 SSRF 安全边界冲突
- ClickHouse 导出 / 对象存储 / Redis 依赖件 —— 单实例无需
- 浏览器专属（CF 挑战、Web 嗅探、远端站点管理、书签、会话捕获）—— 产品形态不符

## 三、建议执行顺序

**第一批（立即可做，各 ≤1 天）**：#3 Reasoning Effort 映射 → #1 模型元数据+缺失检测 → #10 签到奖励解析 → #13 upstream_request_id → #18 模型比价排序 → #12 Key 自助查额度 → #8 首输出超时

**第二批（1-2 天/项）**：#2 分项倍率定价 → #5 决策快照 → #7 not_found 标记 → #9 兑换码 → #11 余额历史曲线 → #20 TOTP

**第三批（按需，2-5 天/项）**：#4 错误透传规则表 → #6 渠道监控历史 → #14 Key 模型映射 → #15 Body 改写 → #16 批量写入/聚合 → #17 告警规则引擎 → #19 系统代理 → #21 敏感保护 → #22 逐 token 探测

## 四、与 08 清单的对应关系

本次 5 项目新调研把 08 的 12 个 ❌ 全部找到了现成参考实现（错误透传←sub2api、TransformOptions←axonhub/new-api、决策快照←metapi、模型元数据←new-api、缺失模型←new-api、兑换码←new-api/sub2api、逐 token 探测←metapi、调试快照←metapi、多 endpoint←metapi、预聚合←metapi/sub2api、条件路由←axonhub、body 改写←axonhub/metapi），无需再"从零设计"。
