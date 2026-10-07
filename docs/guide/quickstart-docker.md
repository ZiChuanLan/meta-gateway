# Docker Compose 部署

## 准备 compose 文件

```yaml
# docker-compose.yml
services:
  meta-gateway:
    image: zichuanlan/meta-gateway:${IMAGE_TAG:-latest}
    container_name: meta-gateway
    restart: unless-stopped
    ports:
      - "4100:4100"
    volumes:
      - ./data:/data
    environment:
      ADMIN_TOKEN: ${ADMIN_TOKEN:?ADMIN_TOKEN required}
      MASTER_KEY: ${MASTER_KEY:?MASTER_KEY 32-char required}
      METRICS_TOKEN: ${METRICS_TOKEN:-mg-metrics-secret}
```

`IMAGE_TAG` 默认 `latest`，也就是**当前正式版（v4.0.0）**；预发布用 `beta`。

## 生成密钥并启动

```bash
export ADMIN_TOKEN=$(openssl rand -hex 16)
export MASTER_KEY=$(openssl rand -hex 16)   # 32 字符加密主密钥
docker compose up -d
```

启动后访问 `http://localhost:4100/console/`，输入 `ADMIN_TOKEN` 进入控制台。

## 单行 docker run

```bash
docker run -d --name meta-gateway \
  -p 4100:4100 \
  -e ADMIN_TOKEN=your-secure-admin-token \
  -e MASTER_KEY=your-32-char-encryption-key-here!! \
  -v ./data:/data \
  --restart unless-stopped \
  zichuanlan/meta-gateway:latest
```

## 三个必填项

| 变量 | 作用 | 注意 |
| --- | --- | --- |
| `ADMIN_TOKEN` | 控制台管理员登录口令 | **不是** `METRICS_TOKEN`；两者不要复用 |
| `MASTER_KEY` | 凭证库 AES-GCM 主密钥 | **必须 ≥ 32 字符**；丢了就再也解不开已存的凭据与 cookie |
| `METRICS_TOKEN` | Prometheus `/metrics` 抓取凭据 | 可留空，但留空后 `/metrics` 需要 `TRUSTED_SCRAPER_CIDRS` 才可访问 |

> [!IMPORTANT]
> **`MASTER_KEY` 必须随数据库一起备份、一起迁移。** 换一个 `MASTER_KEY` 启动同一个库，所有 `secret_enc` / `cookie_enc` / `token_enc` 字段都无法解密。恢复备份时这条是第一位。

## 容器运行身份与数据卷

镜像以 UID/GID `10001` 运行，数据库与备份都在 `/data`。

- 新建的**命名卷**会自动继承正确属主，无需干预。
- 升级已有的 **bind mount 或旧卷**之前，先停服务，再把数据目录改成 UID/GID `10001` 可写，否则容器起不来。

## 健康检查

```bash
curl --fail http://127.0.0.1:4100/readyz
```

`/readyz` 在排空（draining）或 SQLite 不可达时返回 `503`。`/healthz` 只是进程存活检查。

## 下一步

- [源码构建运行](./quickstart-source)
- [升级与更新渠道](./upgrade) —— 正式版与预发布的区别，以及从 v3 升级与换渠道怎么操作
- [基础 URL 与端点规则](/upstream/base-url-rules) —— 开始接上游
- [配置全表](/reference/env-vars) —— 所有环境变量
