# 故障排查

## 第一现场：代理日志

排障从控制台的日志页开始，或者直接查接口：

```bash
# 最近 50 条
curl -s -H "Authorization: Bearer <session_token>" \
  "https://<host>/admin/proxy-logs?limit=50"

# 按维度钻取：某个客户令牌 / 只看失败 / 某个渠道
curl -s -H "Authorization: Bearer <session_token>" \
  "https://<host>/admin/proxy-logs?downstream_key_id=3&status=failed"
```

日志行本身就携带完整链路，展开区就是按这些字段拼出来的：

| 字段 | 含义 |
| --- | --- |
| `downstream_key_id` | 哪个客户令牌发起的 |
| `path` | 中继端点相对名（如 `chat/completions`、`messages`，或任意路径透传的自定义后缀）。**客户端入口 = `/v1/` + path** |
| `route_id` / `route_pattern` | 命中了哪条路由 |
| `channel_id` | 最终用了哪个渠道 |
| `upstream_url` | **实际打到的地址**（scheme + host + path，query 与 fragment 已剥离） |
| `upstream_model` | 发给上游的模型名（可能与客户端请求的不同） |
| `upstream_key_id` / `upstream_key_name` | 服务本次尝试的上游凭据 |
| `key_fingerprint` | 同一把上游密钥的 sha256 前缀（**密钥本体从不落库**） |
| `status` / `latency_ms` / `first_byte_ms` / `tps` | 结果与性能 |

> 排查「上游报错」类问题时先看 `upstream_url` 与上游返回的 body。本项目是透明网关，**多数「网关的 bug」其实是上游协议不匹配**（例如某上游的 `/v1/images/edits` 只收 JSON，multipart 直接 415）。

## 按症状排查

### 请求返回 502，且响应体是 HTML

网关自己不会返回 HTML 错误页。这几乎总是**前置反代或 CDN** 产生的：

- 更新容器时监听会中断约两秒，直连单地址的反代把这个空档暴露成 `502`；
- 反代后面还挂着 CDN 时，客户端可能收到 CDN 的 HTML 错误页，于是报「对 `<!DOCTYPE html>` 解析 JSON 失败」。

处理见[部署与反代 / 零停机更新](./deployment#零停机更新与那个-502)。

### 请求体是流式但客户端一次性收到全部内容

前置反代在缓冲。网关的 `WriteTimeout` 是关闭的，问题一定在中间层：nginx 要 `proxy_buffering off`，Caddy 要 `flush_interval -1`。

### 上游超时

分清是哪一道闸门：

| 现象 | 闸门 |
| --- | --- |
| 发出请求后很久才有响应头 | `OUTBOUND_HEADER_TIMEOUT_SECONDS`（默认 60s） |
| 拿到 `200` 后一直没有第一个分片 | 首字节超时（固定 30s） |
| 非流式慢请求被掐 | 渠道的 `non_stream_timeout_seconds` |

详见[流式与 SSE 语义](/clients/streaming)。

### 明明配了单价，成本却是 0

两层单价链是**整层替换**、**层内不逐字段回退**：

- 只填了 completion 而 prompt 留 0 的那一层，prompt 就按 0 计（等于免费），**不会**去找下一层的 prompt 价；
- 一层的扁平列全 0 且没配阶梯，这一层不算「已定价」，会继续下探；两层都不算则成本记 0。

详见[计费](/billing/)。

### 配置保存了，重开抽屉又变回旧值

编辑抽屉的回填数据来自渠道/路由的**列表投影**。如果某个列没被投影进去，保存时会以零值回写——表现就是「设置了、重开就没了」。

这类问题属于代码缺陷而不是配置错误。排查时确认：该列是否同时出现在三处投影里（渠道列表、转发选型、路由详情）。

### 配了端点映射，请求仍走旧路径

同一类问题的另一种形态：**转发选型用的投影漏了那几列**。列表里显示正常，实际转发读到的是零值。

判据：日志里的 `upstream_url` 是不是你配的那个路径。不是的话，看该列在转发选型投影里是否存在。

### 日志页查不到刚发生的请求

`proxy_logs` 的 FTS5 索引列是固定的。若新触发器引用了索引里不存在的列，**每一次**日志写入都会失败——日志静默丢失。启动时 `logfts.go` 会比对列并重建索引；如果日志整段缺失，先看启动日志有没有 FTS 相关的记录。

### 渠道被自动禁用了，但上游其实没挂

网络抖动不会直接把正常渠道禁用。链路是：偶尔超时 → 平滑冷却 + 后台静默探活 → 只有连续硬失败且探活全失败才触发渠道保护。

如果阈值太敏感，看 `CHANNEL_AUTO_DISABLE_THRESHOLD`、`COOLDOWN_SECONDS` 与恢复探测的设置。

### 上游返回 400，说字段不认识

有些上游对请求体校验很严（多一个未知字段就 400）。这种情况要用请求体字段映射的**白名单写法**：只保留该上游认识的顶层键，其余删掉。

### 反向代理/容器里访问外部服务 connection refused

容器里的 `HTTP_PROXY` 常指向宿主机，用它去访问外网会失败。网关的 OAuth 客户端与出网策略都**显式忽略环境变量代理**，要用代理请在运行设置里配全局代理，或用渠道级 `proxy_url`。

### 数据库启动即崩，日志说 duplicate column name

`schema_migrations` 表被删过，导致迁移重放。**永远不要删这张表。**

## 相关

- [架构总览](./architecture)
- [观测与指标](./observability)
- [数据保留策略](./retention)
