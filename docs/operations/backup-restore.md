# 备份与恢复

## 一、在线备份

```
POST /admin/backups
```

用 SQLite 的**在线备份 API**（不是复制文件），所以备份期间网关继续服务。

备份文件写到 `BACKUP_DIR`，命名格式固定：

```text
meta-gateway-YYYYMMDDTHHMMSSZ-<12 位十六进制>.db
```

文件名带时间戳与随机后缀，**排序即时间序**，也不会因为同一秒内的两次备份互相覆盖。

生成后会**校验快照**再以**原子重命名**发布——所以 `BACKUP_DIR` 里不会出现一个「看起来存在但其实是半截」的备份。

```bash
# 列出备份
curl -s -H "Authorization: Bearer <session_token>" https://<host>/admin/backups
```

## 二、保留期

默认保留 **30** 份，由 `BACKUP_RETENTION_COUNT` 控制。超出的最旧备份在每次创建后清理。

## 三、恢复是离线操作

**恢复没有 HTTP 接口**，只有命令行——这是刻意的：恢复会替换正在使用的数据库，让它成为一个 HTTP 请求能触发的事太危险。

```bash
# 停掉服务后执行
./bin/meta-gateway restore --from meta-gateway-20261005T120000Z-a1b2c3d4e5f6.db
```

恢复流程：

1. 校验指定的备份文件；
2. 把**当前**数据库移到一边（而不是删掉）作为回滚点；
3. 把备份放到位；
4. 失败时自动回滚到第 2 步移开的那份。

> [!IMPORTANT]
> **`MASTER_KEY` 必须与数据库一起保留。** 恢复一个库到另一台机器时，如果那台机器的 `MASTER_KEY` 不同，所有 `secret_enc` / `cookie_enc` / `token_enc` 字段都解不开——恢复出来的网关一个渠道都用不了。
>
> 校验手法：两台机器用各自环境打同一个 reveal 接口，返回串应**字节级一致**。

### 恢复后团队凭据会被失效

恢复会**主动失效被恢复库里的团队凭据**。原因是备份可能来自另一个实例或另一个时间点，让旧的成员会话/密钥在新上下文里继续有效是不安全的。

## 四、导出 / 导入（与备份的区别）

| | 备份 | 导出 / 导入 |
| :--- | :--- | :--- |
| 单位 | 整个数据库文件 | 结构化条目（站点 / 渠道 / 凭据） |
| 用途 | 灾备、整体搬迁 | 分享配置、跨实例迁移、AAH 兼容 |
| 接口 | `POST /admin/backups` | `POST /admin/exchange/{export,import,import-encrypted}` |

导出导入有自己的坑（strict decode、加密封套），见[导入与导出](/operations/deployment)与[WebDAV 同步](./webdav-sync)。

## 五、日常该怎么做

| 时机 | 动作 |
| :--- | :--- |
| **升级前** | 手动 `POST /admin/backups`，并确认 `MASTER_KEY` 已单独记录 |
| **改渠道/路由大批量配置前** | 同上 |
| **定期** | 用 WebDAV 同步把备份推到网盘（见[WebDAV 同步](./webdav-sync)） |
| **迁移机器** | 备份 + `MASTER_KEY` + 数据目录属主（UID/GID `10001`） |

## 相关

- [WebDAV 同步](./webdav-sync)
- [数据保留策略](./retention)
- [部署与反代](./deployment)
