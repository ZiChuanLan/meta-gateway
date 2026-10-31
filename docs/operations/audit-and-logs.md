# 审计与日志

网关有两套记录，**用途不同，不要混**：

| | 审计事件 `audit_events` | 代理日志 `proxy_logs` |
| :--- | :--- | :--- |
| 记什么 | **谁改了配置**、认证失败、管理面限流 | **每一次转发尝试** |
| 粒度 | 管理动作 | 请求 × 尝试 |
| 写入量 | 低 | 高（全库最大的表之一） |
| 可删？ | **没有单条删除/编辑接口**（追加型） | 按保留期清理 |

## 一、审计事件

管理面变更、认证失败与管理面限流拒绝都会产生**脱敏的**追加型事件。

清理每天跑一次，受两个上限约束：

```dotenv
AUDIT_RETENTION_DAYS=90
AUDIT_RETENTION_ROWS=100000
```

任一设为 `0` 只关闭该维度。需要立即执行同一策略时：

```bash
curl -X POST -H "Authorization: Bearer <session_token>" \
  https://<host>/admin/audit-events/cleanup
```

| 接口 | 作用 |
| :--- | :--- |
| `GET /admin/audit-events` | 列表 |
| `POST /admin/audit-events/cleanup` | 按保留策略立即清理 |

> [!IMPORTANT]
> **没有编辑或删除单条事件的 API。** 审计是追加型记录——这是它有意义的前提。

审计表的时间格式是 **RFC3339Nano**（与其他历史表不同，写脚本时注意）。

## 二、零信任日志

`proxy_logs` **只存元数据**，不落任何凭据明文。

新增字段前问一句：这个字段会不会带上 `Authorization` / `Cookie` / `token` / `secret`？

- 会 → 不能进日志、不能进审计、不能进错误响应体。
- 上游凭据只以**指纹**（`key_fingerprint`，sha256 前缀）出现，密钥本体从不落库。
- `upstream_url` 只保留 scheme + host + path，**query / fragment / userinfo 全部剥离**——它们经常携带密钥（`?api_key=…`）。

## 三、日志行携带的链路

排障就是按这些字段钻取。日志行的展开区就是据此拼出的链路：

| 字段 | 含义 |
| :--- | :--- |
| `downstream_key_id` | 哪个客户令牌发起的 |
| `path` | 中继端点相对名。**客户端入口 = `/v1/` + path** |
| `route_id` / `route_pattern` | 命中了哪条路由 |
| `channel_id` | 最终用了哪个渠道 |
| `upstream_url` | **实际打到的地址** |
| `upstream_model` | 发给上游的模型名（可能与客户端请求的不同） |
| `upstream_key_id` / `upstream_key_name` | 服务本次尝试的上游凭据 |
| `key_fingerprint` | 同一把上游密钥的 sha256 前缀 |
| `status` / `latency_ms` / `first_byte_ms` / `tps` | 结果与性能 |
| `client_family` | 从 User-Agent 归一出客户端家族（`pi` / `claude-code` / `opencode` / `codex` / `gemini-cli` / `dsh` / `cline` / `roo-code` / `goose` / `librechat` …，认不出的记 `unknown`，**不再一律记 `browser`**）。签名取自客户端自己的源码或实测，不是猜的 |
| `client_ua` | **原始 User-Agent**（截断 200 字符、剥离控制字符）。家族表只能收录被想到的客户端，认出并点名一个未知客户端靠的是这个字符串 |

> 客户端家族是二分类的结果，原始 UA 是证据。日志展开区的「入口」那一跳优先显示原始 UA；两者都在，认错/认不出时才可追问。

### 一条请求，一行日志

**同一次请求只会出现在列表里一次**，无论中间轮换过几个渠道。每次上游尝试仍然各写一行原始记录（这是审计、健康统计与计费回执的依据），但除最后一次外都会被标记为「已合并」，列表只显示每条请求**最终的那一行**，并把之前的尝试作为**故障转移链路**附在这一行上：

- **行内**：渠道列下方有一条紧凑链路 `渠道A 429 → 渠道B 502 → 渠道C ✓`，一眼看出试过谁、死在哪；
- **展开**：「故障转移链路」面板按顺序列出每一跳的渠道（可点击跳转）、实际请求的模型、状态码、耗时，以及**该跳上游返回的原始错误文本**，最后一跳标「最终采用」；
- **CSV 导出**：失败链路会导出成 `渠道A 429 (upstream_429) → 渠道B 502 (upstream_5xx)` 一行。

几个口径随之对齐：

- `status` 是**客户端实际收到的结果**（最后一跳）；「仅失败」筛出的是最终失败的请求，而不是「试过一次但后来成功了」；
- 失败率与延迟分布按**请求**计数——一个重试后成功的请求不再同时贡献一条失败记录和一条成功记录；
- 全文检索**横跨整条链路**：搜中间某一跳报的错，也能找到这条请求（FTS 与无 FTS 的 LIKE 兜底都是如此）。

链路长度有上限（20 跳，超出时保留头尾并标注「已省略」），这是有意的：单条日志无上限地累积尝试明细，在其他网关上曾把单行撑到几百 MB。原始明细行仍按 `request_id` 全量保留，可用 `GET /admin/proxy-logs` 之外的方式复核。

### 客户端自报家门

客户端可以带 `X-Meta-Client: <家族名>` 主动声明身份，优先级高于 User-Agent（值必须在已知家族名单内，否则忽略）。控制台自己的工作台测试流量就是这样自报的（`X-Meta-Client: console`），所以能把它和“另一个拿着同一把 key 的浏览器”区分开；这是唯一一个日志能确定身份的调用方，其余仍属自报、可被伪造，只进日志、不参与任何路由或计费判断。

**AI 客户端的通用自报约定也被认**：User-Agent 里带第二个产品 `AI-Agent/<agent>`（如 `amplifier/1.0 AI-Agent/amp`，marker 对应 `AI_AGENT` 环境变量）时直接用该名字，优先级高于所有内置签名 —— 这是唯一一个跨工具约定好的"我是 AI agent"标记，`pi` / `codex` / `claude-code` / `opencode` / `gemini-cli` / `cursor-cli` / `copilot-cli` 等都在它的名字集合里；名字不在已知集合内则忽略（原始 UA 始终落在 `client_ua` 上）。`X-Meta-Client` 仍是最高优先级。
网关自身识别不了的客户端，日志会照原样记下 `client_ua`，从它出发再补一条规则即可。

### 按维度钻取

```bash
# 最近 50 条
curl -s -H "Authorization: Bearer <session_token>" \
  "https://<host>/admin/proxy-logs?limit=50"

# 某个客户令牌的失败请求
curl -s -H "Authorization: Bearer <session_token>" \
  "https://<host>/admin/proxy-logs?downstream_key_id=3&status=failed"
```

### 全文检索与 FTS5

日志支持全文检索。索引列：`model`、`error_brief`、`error_detail`、`path`、`request_id`、`upstream_request_id`、`upstream_url`、`client_ua`——所以「哪些请求来自 opencode」直接搜 `opencode` 即可。

**FTS5 索引的列是固定的**：启动时 `logfts.go` 会比对 `PRAGMA table_info(proxy_logs_fts)` 与期望列，缺列就 drop 表 + 触发器再重建。

> [!WARNING]
> 不做这一步的后果很严重：新触发器引用一个不存在的列，会让**每一次 `proxy_logs` 写入失败**——日志静默丢失，而 FTS5 是编译期选项，这个失败不能中断启动。
>
> 如果你发现日志**整段缺失**，先看启动日志有没有 FTS 相关的记录。

## 四、保留期

| 表 | 设置 | 默认 |
| :--- | :--- | :--- |
| `audit_events` | `AUDIT_RETENTION_DAYS` / `AUDIT_RETENTION_ROWS` | 90 天 / 100000 行 |
| `proxy_logs` | 见[数据保留策略](./retention) | — |

## 相关

- [观测与指标](./observability)
- [数据保留策略](./retention)
- [故障排查](./troubleshooting)
