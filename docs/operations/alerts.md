# 告警

告警分两层：**运维事件通知**（渠道被禁用、恢复、连续失败）与**可配置的规则告警**（指标超过阈值）。

## 一、规则告警

规则在固定节拍上评估（**60 秒一跳**），命中后经通知矩阵投递。

一条规则由五部分组成：

| 部分 | 含义 |
| :--- | :--- |
| `metric` | 观察哪个指标 |
| `operator` | 比较方式（大于 / 小于 / 等于…） |
| `threshold` | 阈值 |
| `window` | 在多大的时间窗上计算 |
| `sustained` | 需要**连续满足**多久才真正告警 |

`sustained` 是防抖的关键：单次抖动不该把人叫起来。

### 可用指标

指标全部**从网关已经在维护的表里算出来**，不额外采集：

| 指标 | 含义 |
| :--- | :--- |
| `channel_availability` | 时间窗内**最低**的单渠道可用率（0~1） |
| `request_fail_rate` | 时间窗内失败请求 / 总请求（0~1） |
| `channel_error` | 任一渠道最近一次探测失败时为 1 |
| `error_rate` | `request_fail_rate` 的别名 |
| 模型变更相关 | 来自上游差异台账：**有受影响路由的待处理移除**是正在或即将破坏路由的那些；候选清单的日常变动保持静默，除非有规则明确要求 |

> 「有受影响路由的待处理移除」与「候选清单变动」的区别是刻意的：后者每天都会发生，为它告警等于让人忽略告警。

### 接口

| 接口 | 作用 |
| :--- | :--- |
| `GET /admin/alert-rules` | 列出规则 |
| `POST /admin/alert-rules` | 新建 |
| `PUT /admin/alert-rules/{id}` | 修改 |
| `DELETE /admin/alert-rules/{id}` | 删除 |

## 二、通知矩阵

投递通道由 `ALERT_CONFIG_JSON` 配置（JSON 编码），支持：

| 通道 | 说明 |
| :--- | :--- |
| **webhook** | 通用 HTTP 回调，`WEBHOOK_URL`（空 = 关闭） |
| **bark** | iOS 推送 |
| **serverchan** | Server 酱 |
| **telegram** | Telegram Bot |
| **smtp** | 邮件 |

三种节流/合并机制，避免刷屏：

| 机制 | 作用 |
| :--- | :--- |
| **内容签名冷却** | 相同内容的事件在冷却窗口内只发一次 |
| `WEBHOOK_THROTTLE_SECONDS`（默认 300） | 重复事件在窗口内合并 |
| 每日摘要 | 由 `ALERT_DAILY_SUMMARY_INTERVAL_SECONDS` 控制（0 = 关闭） |

> **通知是尽力而为，永不阻塞请求路径。** 投递失败只记日志——告警通道挂掉不该影响转发。

## 三、运维事件

除了规则告警，这些事件本身也会触发通知：

- 渠道达到连续失败阈值被**自动禁用**；
- 渠道**恢复**；
- 连续失败计数跨过阈值。

渠道被自动禁用时，日志里会带上原因与当时的连续失败次数。

## 四、相关设置

| 设置 | 默认 | 含义 |
| :--- | :--- | :--- |
| `ALERT_CONFIG_JSON` | `""` | 通知矩阵配置 |
| `WEBHOOK_URL` | `""` | 运维通知端点（空 = 关闭） |
| `WEBHOOK_THROTTLE_SECONDS` | 300 | 重复事件合并窗口 |
| `ALERT_SWEEP_INTERVAL_SECONDS` | 0 | 主动健康清扫间隔（0 = 关闭） |
| `ALERT_DAILY_SUMMARY_INTERVAL_SECONDS` | 0 | 每日摘要间隔（0 = 关闭） |

## 五、配置建议

1. **先只开一个通道**（通常是 webhook 或 telegram），确认能收到再铺开。
2. **给 `channel_error` 设 `sustained`**（例如连续 10 分钟）——它是唯一会因为你上游短暂抖动而响的指标。
3. **`request_fail_rate` 的窗口不要太短**，否则一次重试潮就会触发。
4. **每日摘要适合代替高频告警**：把「渠道被禁用」这类事件留给即时通知，把趋势类观察交给摘要。

## 相关

- [观测与指标](./observability)
- [数据保留策略](./retention)
- [故障排查](./troubleshooting)
- [参考 / 环境变量](/reference/env-vars)
