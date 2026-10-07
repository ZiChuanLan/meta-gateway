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

### 三条执行路径，以及为什么默认是 compose 侧车

`selfupdate.Mode()` 的判定顺序是固定的：

```go
if ComposeUpdaterAvailable() { return ModeCompose }    // ← 侧车心跳新鲜
if WatchtowerReachable()     { return ModeWatchtower }  // ← 伴生服务可达
if s.socketAvailable()       { return ModeSocket }
return ModeNone
```

| | **Compose 侧车**（默认） | Watchtower | Socket |
| :--- | :--- | :--- | :--- |
| 前提 | `compose-updater` 服务在跑（心跳新鲜） | 伴生容器可达 | socket 已挂载**且**前两者不可用 |
| 一键更新 | ✅ | ✅ | ✅ |
| **环境变量同步** | ✅ **重新读 `.env` 与 compose 文件** | ❌ 按旧容器的 inspect 数据重建 | ❌ 同左 |
| 失败可见 | ✅ 结果与日志尾部写进状态卷 | ❌ 只说“开始了” | 部分 |
| 谁持有 socket | 侧车容器（网关看不到） | 伴生容器 | **网关自己** |
| 换渠道 / 装指定版本 | ❌ 跟随部署的 `IMAGE_TAG` | ❌ 同左 | ✅ 可换标签 |

**为什么不再用 watchtower 做默认：** 它按**旧容器的 inspect 数据**重建容器，所以 `environment:` 与
`.env` 的变更**永远进不了新容器**。这不是配置问题，是它的设计边界——维护者在被问到时说得直接：
*"Watchtower works with env vars present in container metadata (like docker inspect _containerId_),
so it looks like it can't use docker-compose variables"*（containrrr/watchtower#233），后续结论是
*"outside of the scope of watchtower"*。于是每次改环境变量都得手敲一次 `docker compose up -d`。

`compose-updater` 侧车持有 socket **与工程目录**，点更新时在宿主机上跑的就是那两条命令：

```bash
docker compose pull meta-gateway
docker compose up -d --no-build --no-deps meta-gateway
```

所以镜像与环境变量一起生效，失败时还会把退出码与日志尾部写进状态卷，控制台直接展示。

> [!NOTE]
> **`docker-compose.yml` 本身的改动仍需要一次人工重建。** `.env` 是配置（自动同步），compose 文件是代码：
> 当一个版本新增了服务或新增了变量声明（如 v4.0.0 补齐的 32 个变量），要 `git pull` 后跑一次
> `docker compose up -d`。侧车刻意**不会**替你改部署文件——静默改写别人的部署配置不是“更新镜像”的含义。

| | Compose 侧车模式 | Watchtower 模式 | Socket 模式 |
| :--- | :--- | :--- | :--- |
| 前提 | 侧车心跳新鲜 | 伴生容器可达 | socket 已挂载**且**前两者不可用 |
| 一键更新 | ✅ | ✅ | ✅ |
| **安装指定版本 / 换渠道** | ❌ 只能更新部署标签 | ❌ 同左 | ✅ 拉任意版本，并把新标签写进新容器 |
| 额外风险 | 侧车持有 socket（≈ 宿主机 root） | 同左 | **网关进程自己持有 socket**，且插件/钩子同进程可达 |

### 首次从 v3 升级到 v4

**老部署（v3 时代的 compose 文件）需要一次 `docker compose up -d`**，之后就都交给控制台。原因很具体：
侧车是**新增的服务**，而旧 compose 文件里没有它，也没有共享的状态卷；同时 v4.0.0 补齐的 32 个环境变量
也是写在**新** compose 里的。一次重建把这两件事一起解决：

```bash
cd /opt/meta-gateway
git pull --ff-only
docker compose pull meta-gateway
docker compose up -d --no-build --force-recreate meta-gateway
curl -s http://127.0.0.1:4100/healthz          # version 应变成 v4.0.0
```

也可以**先点控制台「更新」**（watchtower 路径，会先自动备份数据库），升级完再补上面那条
`docker compose up -d` 把侧车装上——两种顺序都会得到同一个结果。

> [!NOTE]
> **控制台会自己提醒这一步。** 网关能看出自己的环境变量是否来自当前部署文件（看容器里有没有
> `SELFUPDATE_TRACK_TAG` 这个由 compose 声明的标记，或者侧车在不在）：两者都没有就说明升级只换了镜像、
> 文件没被重新读，于是登录后会弹出提示并给出这条命令，执行完提示自己消失。所以不读文档也不会漏。
>
> 如果你用 `docker run` 部署（没有 compose 文件），这个提示不适用——你的环境变量就是启动参数本身，
> 没有“待生效”的改动。按[单行 docker run](./quickstart-docker#单行-docker-run)的示例带上 `SELFUPDATE_TRACK_TAG`
> 就不会看到这个提示（它同时告诉控制台你在跟哪个渠道）。

升级后的日常更新就是控制台点一下：**镜像与环境变量一起更新**，不需要再碰宿主机。

> [!NOTE]
> **升级会自动备份。** 点「更新」时网关会在启动交接**之前**创建并校验一份数据库快照（存在
> `BACKUP_DIR`，如 `/data/backups`），备份失败或没配 `BACKUP_DIR` 就不开始。所以不要停服、
> 也不要手动拷数据卷——除非你想在升级前把快照取到宿主机上另存。

大版本会跑数据库迁移（实测 v3.8.6 → v4：108 → 125），**迁移是单向的**。需要回退时用升级前那份快照：

```bash
# 列出快照名（控制台「设置 → 备份」也可以看）
docker exec meta-gateway-meta-gateway-1 ls -1 /data/backups
# 回退（先停容器，再恢复，再起来）
docker compose stop meta-gateway
docker compose run --rm meta-gateway restore --from <快照名>
docker compose up -d --no-build meta-gateway
```

### 自动更新（没有内置定时器）

侧车**只响应点击**，不做任何定时轮询（稳定性优先：无人值守的升级会带着数据库迁移一起落地）。
要无人值守就自己加一个 cron/systemd timer，内容与侧车一样：

```bash
cd /opt/meta-gateway && docker compose pull meta-gateway \
  && docker compose up -d --no-build --no-deps meta-gateway
```

> [!WARNING]
> 定时更新只能跟随**当前标签**（换渠道仍要改 `.env`），而且**大版本会在无人值守时落地**——包括数据库迁移。
> 这条路径也**不会**自动备份（备份是控制台点按钮那一步做的），请自行安排。

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
