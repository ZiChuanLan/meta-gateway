# 部署与反代

## 部署

一个进程、一个 SQLite 数据库。复制 `.env.example` 到私有环境文件，把 `ADMIN_TOKEN`、`MASTER_KEY`、`METRICS_TOKEN` 换成各自独立的随机值，然后：

```bash
docker compose up -d --build
docker compose ps
curl --fail http://127.0.0.1:4100/readyz
```

Compose **没有兜底凭据**——缺变量会直接启动失败，这是刻意的。

镜像以 UID/GID `10001` 运行，数据库与备份存在 `/data`。新建的**命名卷**会继承正确属主；升级已有的 **bind mount 或旧卷**之前，先停服务并把数据目录改成 `10001` 可写。

## Web 控制台的构建顺序

控制台嵌在 Go 二进制里，由网关在 `/console/` 提供。无扩展名的嵌套路径（如 `/console/routing`）返回 SPA 外壳；**缺失的资源路径返回 `404`，绝不回退成 HTML**。HTML 用 `Cache-Control: no-cache`，带内容哈希的资源用一年期不可变缓存。

连接用 `ADMIN_TOKEN`，**不是** `METRICS_TOKEN`。令牌只放在 `Authorization: Bearer` 头里，默认只驻留内存（操作员可以主动选择标签页级 `sessionStorage`），不进 Cookie、`localStorage`、URL、配置或日志。收到 `401` 即失效当前 UI 会话。

凭据密文用密码输入框；下游令牌明文以 `MASTER_KEY` 加密存储，只能通过显式 reveal 调用重新查看（每次 reveal / 轮换都进审计）；带密文的导出不提供浏览器预览。

源码构建时**先生成前端产物，再编译 Go**：

```bash
cd web
npm ci
npm run build
cd ..
go build -trimpath -o bin/meta-gateway ./cmd/server
```

Vite 把生产文件写入 `internal/webui/dist`，`go:embed` 再把该目录打包进可执行文件。Dockerfile 与 CI 走同一条 Node → Go 顺序。

## 反代：必须关掉缓冲

网关保持 `WriteTimeout` 关闭并流式输出 SSE，所以前置反代**不能缓冲响应**：

```nginx
proxy_buffering off;
```

```text
flush_interval -1
```

## 零停机更新与那个 `502`

更新容器会停掉监听约两秒（旧容器被移除、新容器绑定端口）。直接指向单个 `host:port` 的反代会把这个空档暴露成 `502`；而反代后面还挂着 CDN 时，客户端可能收到 CDN 的 **HTML 错误页**而不是 JSON——这就是为什么有些客户端会报「对 `<!DOCTYPE html>` 解析 JSON 失败」。

nginx 无法对同一个单地址上游重试，所以让 nginx 指向一个小的重试中继：

```nginx
upstream meta_gateway_edge { server 127.0.0.1:4101; }
```

```text
:4101 {
	bind 127.0.0.1
	reverse_proxy 127.0.0.1:4100 {
		lb_try_duration 15s
		lb_try_interval 200ms
		flush_interval -1
	}
}
```

再补一个返回 JSON 体的 `location @gateway_unavailable`，让真正挂掉的网关也能用 JSON 应答。注意 CDN 可能用自己的响应替换源站的 `502`，所以「一定返回 JSON」是尽力而为，不是保证。

## 优雅关闭

Compose 发正常终止信号并给 20 秒。服务会先把就绪标记为不可用，停止后台工作，再在 `SERVER_SHUTDOWN_TIMEOUT_SECONDS` 内排空 HTTP 请求。

`WriteTimeout` 保持关闭以保住长连接 SSE——所以上游代理的流式超时要自己配。

## 相关

- [观测与指标](./observability)
- [备份与恢复](./backup-restore)
- [故障排查](./troubleshooting)
