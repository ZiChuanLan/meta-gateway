# 维护与清理

## 一、数据库 GC

按 cron 计划执行两件事：**孤儿行清扫**与 **SQLite `VACUUM`**。

| 行为 | 说明 |
| :--- | :--- |
| 计划热加载 | 改表达式立即生效；设为**空字符串即关闭** |
| 首次启用 | 从「关闭」变为「启用」时**额外跑一次延迟任务**，不必等下一个 cron 边界 |
| 手动触发 | `POST /admin/db/gc` |
| 查看上次结果 | `GET /admin/db/gc` |

GC 同时会回收**站点已删除后残留的探针行**（`site_probe_runs` / `site_probe_samples`）。

`VACUUM` 会**重写整个数据库文件**，期间需要额外的磁盘空间（约等于库大小）并持有写锁。库很大时安排在低峰期。

## 二、保留期裁剪

每天一次维护会裁剪历史表。每个窗口一个环境变量，**设 `0` 关闭**：

| 变量 | 默认 | 表 |
| :--- | :--- | :--- |
| `HEALTH_HISTORY_RETENTION_DAYS` | 90 | `channel_health_history` |
| `BALANCE_HISTORY_RETENTION_DAYS` | 90 | `balance_history` |
| `DECISION_SNAPSHOT_RETENTION_DAYS` | 7 | `decision_snapshots` |
| `MODEL_CHANGE_RETENTION_DAYS` | 90 | 已完成的模型变更记录 |
| `SITE_PROBE_RETENTION_DAYS` | 7 | `site_probe_runs` + `site_probe_samples` |
| `AUDIT_RETENTION_DAYS` / `AUDIT_RETENTION_ROWS` | 90 / 100000 | `audit_events` |

> **站点探针的窗口要刻意给值。** 每轮采集为「每个被监控模型 × 每个站点」写一行，它是全库写入量最高的历史表。只有当真有东西在画这份历史时才值得调大。

详见[数据保留策略](./retention)。

## 三、余额与成本扫描

财务扫描会主动检查上游余额/token，并生成**每日摘要**经通知器发出。

## 四、工厂重置

```
POST /admin/reset
```

**只清业务数据，保留配置。** 这个边界是刻意的：

| 被清掉（业务） | 保留（配置） |
| :--- | :--- |
| 凭据、渠道、路由、成员 | 站点 |
| 下游令牌、令牌分组 | 运行设置 |
| 代理日志、用量记录、余额历史 | TOTP 状态 |
| 发现的模型、签到日志 | WebDAV / 插件状态 |
| 模型倍率、模型元数据、模型屏蔽 | **备份历史** |
| 兑换码、决策快照 | |

派生状态也一并清掉——例如目录同步看板描述的是上面那些表里的行，重置后还显示「已填 12 个价格」就是一句谎话。

> [!WARNING]
> **重置不可撤销。** 先 `POST /admin/backups`。

## 五、自更新

网关可以检查并安装新版本。两条渠道：

| 渠道 | 行为 |
| :--- | :--- |
| 稳定 | 跟踪正式 Release |
| Beta | 跟踪预发布（**不覆盖 `latest`**） |

相关设置：`UPDATE_CHECK_ENABLED`（默认 true）、`GET/PUT /admin/update-channel`、`GET /admin/update-check` + `POST /admin/update-check/refresh`、`GET/POST /admin/self-update`。

> [!IMPORTANT]
> **Watchtower 是 HTTP API 模式，不会自己轮询。** 两台生产机的 watchtower 配置里既没有 `--interval` 也没有 `--schedule`，所以推完镜像**不会自动上线**，必须显式触发 `/v1/update`。详见[部署与反代](./deployment)。

## 六、日常维护清单

| 频率 | 动作 |
| :--- | :--- |
| 每周 | 看一次告警与失败率趋势；确认 GC 有在跑（`GET /admin/db/gc`） |
| 每月 | 确认备份可恢复（至少演练一次 `restore`）；看库体积是否异常增长 |
| 升级前 | 备份 + 记录 `MASTER_KEY` + 看 CHANGELOG 的破坏性变更 |
| 长期 | 检查保留期设置是否与实际写入量匹配（尤其站点探针） |

## 相关

- [数据保留策略](./retention)
- [备份与恢复](./backup-restore)
- [部署与反代](./deployment)
- [参考 / 环境变量](/reference/env-vars)
