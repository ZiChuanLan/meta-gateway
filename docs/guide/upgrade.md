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

更新弹窗可选稳定 / Beta 渠道，刷新对应版本后再确认安装。Socket 更新安装确认的官方精确版本，**不自动降级**。

> [!WARNING]
> **Watchtower 只更新当前镜像标签，不能通过网页替换部署标签。** 首次切 Beta，要在部署的 `.env` 里设置 `IMAGE_TAG=beta`，然后手动执行：

```bash
# 首次切渠道
docker compose pull meta-gateway
docker compose up -d --no-build --no-deps --force-recreate meta-gateway
```

之后同渠道内支持一键更新。`IMAGE_TAG=latest` 跟踪稳定版。Compose 会同时注入跟踪标签声明，旧部署需要更新 Compose 文件。**切回稳定前先确认数据库兼容性，不可直接降级。**

- 稳定 Release 也会推进 beta 镜像标签，使测试渠道能升级到后续正式版。
- 固定版本可使用 `4.0.0-beta.1` 这类标签。

### 已知限制

- 首次无 Socket 的跨渠道切换仍需一次部署配置变更。
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
