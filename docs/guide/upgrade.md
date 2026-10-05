# 升级与更新渠道

## V4 Beta

V4 当前为预发布，**先备份数据库再试用**。Beta 发布到 GitHub Pre-release 和 `zichuanlan/meta-gateway:beta`，不覆盖稳定版 `latest`。

### 登录方式的变化

- 管理员与成员**统一从 `/console` 登录**，旧 `/app` 地址保留为兼容跳转。
- 部署管理员初始用户名由 `ADMIN_USERNAME` 指定（默认 `admin`），密码仍是 `ADMIN_TOKEN`。
- 升级后可在「设置 → 管理员登录设置」验证口令和 TOTP 后保存新用户名：立即生效、重启保留、**优先于环境变量**，且不能与团队账号重名。
- 不知道旧版升级后的用户名时，可用登录页的升级说明入口，通过原管理口令和 TOTP 登录后再设置。**口令没有被重置。**
- 个人模式不要求团队账号；团队模式先创建 owner，再启用成员、权限和额度功能。

### 更新渠道

更新弹窗可选稳定 / Beta 渠道，刷新对应版本后再确认安装。

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

### 首次切渠道

因为「stable 实例没有渠道功能、渠道功能只在 V4」这个先后关系，**第一次切到 Beta 必须手动一次**：

```bash
# .env：IMAGE_TAG=beta（它同时是 compose 重建时的落点）
docker compose pull meta-gateway
docker compose up -d --no-build --no-deps --force-recreate meta-gateway
```

之后就交给网页。`IMAGE_TAG=latest` 跟踪稳定版；固定版本可用 `4.0.0-beta.1` 这类标签。

> [!WARNING]
> **网页切换改的是运行中的容器，不会回写 `.env`。** 代码里没有任何回写 `.env` 的机制。
> 所以 `.env` 的 `IMAGE_TAG` 是「`docker compose up` 重建时的落点」——切完渠道顺手同步它，
> 否则下次 compose 重建会落回旧值。

### 已知限制

- 首次无 Socket 的跨渠道切换仍需一次部署配置变更（见上）。
- **稳定版实例上无法用网页切到 Beta**：渠道功能本身是 V4 引入的，v3 没有 `/admin/update-channel`。
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
