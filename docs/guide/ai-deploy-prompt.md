# AI 一键部署

如果你在用 Cursor / Claude Code / Windsurf / ChatGPT / DeepSeek 这类 AI 助手或运维 Agent，把下面的提示词整段发给它，它会自己检测宿主机环境、生成高强度密钥、写 compose 文件并拉起容器。

```markdown
请帮我在当前服务器/本机部署 Meta Gateway（高性能 AI 统一中继网关）。
项目仓库：https://github.com/ZiChuanLan/meta-gateway

部署要求：
1. 采用 Docker Compose 方式部署，使用官方镜像 `zichuanlan/meta-gateway:latest`；
2. 宿主机服务端口映射为 4100（即 `4100:4100`）；
3. 数据持久化挂载至当前目录下的 `./data` 目录（映射容器内 `/data`）；
4. 环境变量要求：
   - 自动生成一个高强度的 `ADMIN_TOKEN` 作为后台控制台登录密码；
   - 自动生成一个 32 位的强随机字符串作为 AES 密钥 `MASTER_KEY`（必须 ≥ 32 字符）；
   - 开启自动重启策略 `restart: unless-stopped`；
5. 生成完整的 `docker-compose.yml` 文件后，自动执行 `docker compose up -d` 命令拉起容器；
6. 检查容器运行状态，并在控制台清晰输出：
   - 控制台 WebUI 访问地址（`http://<IP或localhost>:4100/console`）；
   - API 中继调用地址（`http://<IP或localhost>:4100/v1`）；
   - 随机生成的 ADMIN_TOKEN 密码与 MASTER_KEY 明文记录。
```

## 部署完成后自己核对三件事

1. `curl --fail http://127.0.0.1:4100/readyz` 返回成功；
2. **`MASTER_KEY` 已单独记录下来**——它不在容器里任何可读位置，丢了就解不开已存凭据；
3. `./data` 目录属主可写（镜像以 UID/GID `10001` 运行，bind mount 需要手动 `chown`）。

## 相关

- [Docker Compose 部署](./quickstart-docker)
- [配置全表](/reference/env-vars)
