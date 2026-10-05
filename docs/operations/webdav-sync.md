# WebDAV 同步

用 WebDAV 网盘做配置的异地副本。**两个方向完全独立**：各有自己的连接、开关、计划与结果。

| 方向 | 做什么 |
| :--- | :--- |
| **下载**（download） | 从网盘拉取 AAH / exchange 备份并**导入** |
| **上传**（upload） | 把网关自己的 exchange 备份**推到**网盘 |

> **两个方向绝不借用对方的凭据。** 没配全某个方向的 URL / 用户名 / 密码时，那个方向报「未配置」，而不是悄悄用另一个方向的凭据。

## 环境变量

| 变量 | 方向 | 默认 |
| :--- | :--- | :--- |
| `WEBDAV_SYNC_ENABLED` | 下载 | `false` |
| `WEBDAV_URL` / `WEBDAV_USERNAME` / `WEBDAV_PASSWORD` | 下载 | 空 |
| `WEBDAV_BACKUP_PASSWORD` | 下载 | 空 |
| `WEBDAV_CRON` | 下载 | `0 */6 * * *` |
| `WEBDAV_MAX_BYTES` | 下载 | — |
| `WEBDAV_UPLOAD_ENABLED` | 上传 | `false` |
| `WEBDAV_UPLOAD_URL` / `WEBDAV_UPLOAD_USERNAME` / `WEBDAV_UPLOAD_PASSWORD` | 上传 | 空 |
| `WEBDAV_UPLOAD_BACKUP_PASSWORD` | 上传 | 空 |

`WEBDAV_UPLOAD_*` 未设置时会回落到对应的共享变量——这是**上传方向独有**的回落，反向不成立。

## 管理接口

| 接口 | 作用 |
| :--- | :--- |
| `GET` / `PUT /admin/webdav/settings` | 读写两个方向的配置 |
| `GET /admin/webdav/status` | 最近一次同步结果 |
| `POST /admin/webdav/test` | 测试连接（不导入、不推送） |
| `POST /admin/webdav/sync` | 手动触发一次同步 |

结果状态有三种：`success`、`failed`、`skipped`。`skipped` 是「这一轮没有可同步的东西」，不是失败。

## 加密封套

备份用与 All-API-Hub 共用的加密封套，两边可以互相解：

| 项 | 值 |
| :--- | :--- |
| 密钥派生 | PBKDF2-SHA256 / 250000 轮 |
| 加密 | AES-256-GCM |
| 编码 | base64 的 salt / iv / ct |
| type | `all-api-hub-webdav-backup-encrypted` |

所以网关可以导入 AAH 的加密备份，AAH 也能读网关导出的加密备份。

## 相关

- [备份与恢复](./backup-restore)
- [数据保留策略](./retention)
- [参考 / 环境变量](/reference/env-vars)
