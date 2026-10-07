# 升级与更新渠道

## V4

**V4 已是正式版**（`zichuanlan/meta-gateway:latest`，GitHub Release 标记 Latest）。升级前建议先备份：
从控制台点「更新」会**自动创建并校验一份数据库快照**（存到 `BACKUP_DIR`，如 `/data/backups`）。
Beta 渠道仍然存在，发布到 GitHub Pre-release 与 `zichuanlan/meta-gateway:beta`，不覆盖 `latest`。

### 登录方式的变化

- 管理员与成员**统一从 `/console` 登录**，旧 `/app` 地址保留为兼容跳转。
- 部署管理员初始用户名由 `ADMIN_USERNAME` 指定（默认 `admin`），密码仍是 `ADMIN_TOKEN`。
- 升级后可在「设置 → 管理员登录设置」验证口令和 TOTP 后保存新用户名：立即生效、重启保留、**优先于环境变量**，且不能与团队账号重名。
- 不知道旧版升级后的用户名时，可用登录页的升级说明入口，通过原管理口令和 TOTP 登录后再设置。**口令没有被重置。**
- 个人模式不要求团队账号；团队模式先创建 owner，再启用成员、权限和额度功能。

### 更新渠道

**渠道就是部署的镜像标签（`.env` 的 `IMAGE_TAG`），控制台只读展示，不提供切换。** 原因很直接：
控制台改变不了容器跑的是哪个标签，所以旧版那个「网页渠道选择」只能改「检查哪个渠道」，改不了
「实际装哪个渠道」——两者不一致时服务端会以 `watchtower_channel_mismatch` 拒绝安装。现在更新检查直接读
部署标签（`selfupdate.TrackingChannel`），**「检查到的」与「能装的」是同一个答案**。

```bash
# 换渠道：改 .env 再重建（唯一方式）
# .env：IMAGE_TAG=beta      # latest = 稳定版；beta = 预发布
cd /opt/meta-gateway
docker compose pull meta-gateway
docker compose up -d --no-build --no-deps --force-recreate meta-gateway
```

固定版本也可用（如 `IMAGE_TAG=4.0.0-beta.7`）：这时更新弹窗会说明「此部署固定了版本」，网页升级不会
改变它。部署文件里没有 `IMAGE_TAG` 时渠道未知，按稳定版检查，弹窗会提示去设置它。

> [!IMPORTANT]
> **网页只能往前装，不能降级。** `POST /admin/self-update/apply` 会先校验
> `updatecheck.IsNewer(target, 当前版本)`，不满足直接 `400 target must be a newer release than the running version`
> （实测）。所以「从 Beta 切回稳定」在稳定版还没超过你当前版本之前是**做不到**的——这是刻意的安全属性，
> 不是缺失的功能。

### 两条执行路径，以及为什么“挂了 socket 不一定就生效”

`selfupdate.Mode()` 的判定顺序是固定的：

```go
if WatchtowerReachable() { return ModeWatchtower }   // ← 先探测伴生服务
if s.socketAvailable()     { return ModeSocket }
return ModeNone
```

**Watchtower 优先。** 所以只取消注释 `/var/run/docker.sock` 挂载是**不够的**——只要那个伴生容器
在 compose 网络里可达，模式就仍然是 `watchtower`（实测：socket 已挂载但 `mode` 仍为 `watchtower`）。
要进 socket 模式，必须让 watchtower 不可达（停掉并从 compose 里移除）。

| | Watchtower 模式 | Socket 模式 |
| :--- | :--- | :--- |
| 前提 | 伴生容器可达 | socket 已挂载**且** watchtower 不可达 |
| 一键更新 | ✅ | ✅ |
| **安装指定版本 / 换渠道** | ❌ 只能更新已配置的标签 | ✅ 拉任意版本，并把新标签写进新容器 |
| 额外风险 | 无 | **socket ≈ 宿主机 root**，且插件/钩子同进程可达 |

### 首次从 v3 升级到 v4

**两条路都可以**，不需要先读文档再动手：

| 方式 | 做了什么 | 什么时候用 |
| :--- | :--- | :--- |
| 控制台点「更新」 | 执行器拉取 `IMAGE_TAG` 指向的镜像、重建容器、跑迁移；**动手前自动备份数据库** | 默认选择，日常升级也是它 |
| `docker compose pull && up -d` | 同上，**并且把 compose 文件里的环境变量一起带进新容器** | 想让 `.env` 里新增/修改的变量生效时（随时补做一次即可） |

```bash
cd /opt/meta-gateway
docker compose pull meta-gateway
docker compose up -d --no-build --no-deps --force-recreate meta-gateway
```

两条路的差别只有一个：**watchtower 不读 compose 文件**（官方 issue #233：它只用容器元数据里的环境变量），
所以点按钮时新容器**沿用旧容器的环境变量**，而 `docker compose up` 会把 compose 里声明的变量重新应用一遍。
这不会阻止升级：v4 对每一个新增变量都有代码默认值（已用 v3 部署的实际容器环境实测启动与转发正常）。

> [!NOTE]
> **升级后建议补做一次 `docker compose up -d`**：v3 时代的容器环境里没有 `ADMIN_TOKEN_LOGIN`、
> `SELFUPDATE_TRACK_TAG`，而 `.env` 里有些变量（如 `HEALTH_SWEEP_*`、`SQLITE_MAX_OPEN_CONNS`）因为
> 旧版 compose 没传而**从未生效**。补做一次重建就会全部对齐，不需要停服务、也不需要手动备份——
> 升级前的那次快照已经在 `/data/backups` 里。

大版本会跑数据库迁移（实测 v3.8.6 → v4：108 → 125），**迁移是单向的**，所以无论走哪条路，升级前都会
先落一份经过校验的数据库快照（备份失败或没配 `BACKUP_DIR` 就不开始）。需要回退时用它恢复：

```bash
# 列出快照名（控制台「设置 → 备份」也可以看）
docker exec meta-gateway-meta-gateway-1 ls -1 /data/backups
# 回退（先停容器，再恢复，再起来）
docker compose stop meta-gateway
docker compose run --rm meta-gateway restore --from <快照名>
docker compose up -d --no-build meta-gateway
```

### 定时自动更新（可选，默认关闭）

watchtower 以 HTTP API 模式运行时**默认不轮询**（官方文档：*“By default, enabling this mode prevents
periodic polls”*），所以推完镜像不会自己上线。要无人值守自动升级，在 `.env` 里显式打开：

```bash
WATCHTOWER_HTTP_API_PERIODIC_POLLS=true
WATCHTOWER_POLL_INTERVAL=86400    # 秒；默认 24 小时
```

> [!WARNING]
> 自动轮询只能跟随**当前标签**（换渠道仍要改 `.env`），而且**大版本会在无人值守时落地**——包括数据库迁移。
> 开启前请确认有可用备份与回滚方案。

### 已知限制

- 换渠道（稳定 ↔ Beta）与固定版本变更**必须改 `.env` 并重建容器**：这是部署文件的事，控制台按设计不提供切换。
- 从 v3 升级的第一次仍需手动执行上面的 compose 命令（v3 没有渠道功能，容器环境也不会自己更新）。
- 未实现更新助手、任意历史版本降级、价格版本锁定。
- 测试框架的开发依赖仍有已记录的 Vitest/mocker 公告，勿对不可信网络开放开发测试服务。
- **Beta 不等于完整安全审计完成。**

## 升级前的检查清单

1. 备份数据库（控制台 `POST /console/backups`，或直接复制 `/data`）。
2. **确认 `MASTER_KEY` 与数据库一起保存**——新实例没有它会解不开所有凭据。
3. 检查 `docker-compose.yml` 是否与当前版本同步（`IMAGE_TAG` 声明是后加的）。
4. 升级后 `curl --fail http://127.0.0.1:4100/readyz` 并确认版本号。

## 相关

- [备份与恢复](/operations/)
- [自更新机制](/operations/)
