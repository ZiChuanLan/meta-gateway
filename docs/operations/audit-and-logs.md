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
| `client_family` | 从 User-Agent 归一出客户端家族 |

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

日志支持全文检索。**FTS5 索引的列是固定的**：启动时 `logfts.go` 会比对 `PRAGMA table_info(proxy_logs_fts)` 与期望列，缺列就 drop 表 + 触发器再重建。

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
