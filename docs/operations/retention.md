# 数据保留策略

每天一次维护会裁剪历史表。每个窗口是一个环境变量，**设 `0` 关闭该裁剪器**（不接受负数）。

## 历史表

| 环境变量 | 默认 | 表 |
| --- | --- | --- |
| `HEALTH_HISTORY_RETENTION_DAYS` | 90 | `channel_health_history` |
| `BALANCE_HISTORY_RETENTION_DAYS` | 90 | `balance_history` |
| `DECISION_SNAPSHOT_RETENTION_DAYS` | 7 | `decision_snapshots` |
| `MODEL_CHANGE_RETENTION_DAYS` | 90 | `model_changes`（已完成的） |
| `SITE_PROBE_RETENTION_DAYS` | 7 | `site_probe_runs` + `site_probe_samples` |

> [!WARNING]
> **站点探针的窗口要刻意给值。** 每一轮采集会为「每个被监控模型 × 每个站点」写一行，它是整个库里写入量最高的历史表。只有当真有东西在画这份历史时，才值得把它调大。

## 审计事件

管理面变更、认证失败与管理面限流拒绝都会产生**脱敏的**追加型事件。清理每天跑一次，同时受两个上限约束：

```dotenv
AUDIT_RETENTION_DAYS=90
AUDIT_RETENTION_ROWS=100000
```

任一设为 `0` 只关闭该维度。需要立即执行同一策略时用 `POST /console/audit-events/cleanup`。

**没有编辑或删除单条事件的 API** —— 审计是追加型记录。

## 站点探针采集频率

外部站点探针读的是站点自己公开的页面（Uptime Kuma 状态页、New-API 价格表），**不花上游 token**，所以频率问题等价于「那些页面变得有多快」：

| 环境变量 | 默认 | 说明 |
| --- | --- | --- |
| `SITE_PROBE_INTERVAL_SECONDS` | 900 | 最小 60 |
| `SITE_PROBE_JITTER_SECONDS` | 120 | 每轮随机加上，需 ≤ interval |

两者都可以在控制台热改（运维 → 站点探针）并**在下一轮生效**。900 秒的默认值对应「每约 15 分钟发一次心跳」的状态页；如果站点自己每分钟轮询上游，用 60–120 更合适。

## 上游站点消息读取频率

总览页的「上游站点消息」读的是站点自己公开的公告板（New-API 系的 `GET /api/status` → `data.announcements`），同样**不花上游 token**：

| 环境变量 | 默认 | 说明 |
| --- | --- | --- |
| `SITE_NEWS_INTERVAL_SECONDS` | 300 | 最小 60 |

它同样可以在控制台热改（运行设置里的「站点消息」段），并且**立即重排等待中的那一轮**，不用等当前间隔走完。与站点探针有一处不同：这里可以填 `0` —— 那是**关闭定时读取**（总览面板的「刷新」按钮仍能手动抓一轮），而不是「回落到默认值」；数据库里存 `NULL` 才是「没覆盖过」并落到这个环境变量。

## 数据库 GC

`POST /admin/db/gc` 按需执行孤儿清理 + `VACUUM`，同时会删掉「站点已被删除」的探针行。

## 相关

- [备份与恢复](./backup-restore)
- [配置全表](/reference/env-vars)
