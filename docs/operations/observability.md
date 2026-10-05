# 观测与指标

## 统一的时间窗口

总览与日志两个工作区共用**一个**时间范围控件：滚动预设（15m / 1h / 6h / 24h / 7d / 30d / 全部）+ 精确到秒的绝对起止。

选择存在 **URL 里**（`range`、`from`、`to`），所以带窗口的视图可复现、可分享，刷新后还在。

## 聚合一律在 SQL 里算

控制台**从不**把一页行拉到客户端再统计「一共多少」。每个聚合都是按选中窗口在 SQL 里算出来的：

| 接口 | 窗口聚合 |
| --- | --- |
| `GET /admin/usage/summary` | 请求数、token、缓存 token、成本、状态分布 |
| `GET /admin/usage/series` | 每个时间桶的请求 / 失败 / token / 成本 |
| `GET /admin/usage/top-models` | 按 token 的模型排行 |
| `GET /admin/proxy-logs` | 日志页本身 |
| `GET /admin/proxy-logs/latency-histogram` | 延迟分布 + p50 / p95 / p99 |

五个接口都接受 RFC3339 的**闭区间** `since` / `until`。**畸形的时间边界返回 `400` 而不是被忽略**——静默丢掉一个边界会把「最近 15 分钟」这种窄问题变成一次昂贵的全表扫描。

`series` 响应里的 `bucket_seconds` 按 epoch 对齐并吸附到可读单位（1m … 1d）；控制台用 `since + bucket_seconds` 在**查看者的时区**里格式化标签，所以服务端不需要知道时区。

`latency-histogram` 同时返回 `matched`（窗口内有多少行）与 `total`（实际采样了多少行）。当 `matched > total` 时控制台会明确说明，而不是把一个截断的样本当成整个窗口。

## 存活与就绪

| 端点 | 语义 |
| --- | --- |
| `/healthz` | 进程存活 |
| `/readyz` | 排空中或 SQLite 不可达时返回 `503` |
| `/metrics` | Prometheus 文本格式 |

```bash
curl -H "Authorization: Bearer $METRICS_TOKEN" \
  http://127.0.0.1:4100/metrics
```

只有显式配置的 `TRUSTED_SCRAPER_CIDRS` 可以免 token 抓取。**不要把 `ADMIN_TOKEN` 复用成 metrics token。**

## 实时追踪

控制台有一个实时追踪面板，观察进行中的请求。它只在管理面可见，用于回答「现在这一刻卡在哪个渠道」。

## 相关

- [数据保留策略](./retention)
- [故障排查](./troubleshooting)
- [配置全表](/reference/env-vars)
