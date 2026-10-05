# 源码构建运行

## 前置环境

| 依赖 | 版本 |
| --- | --- |
| Go | 1.26+ |
| Node.js | 24+ |

## 构建

```bash
git clone https://github.com/ZiChuanLan/meta-gateway.git && cd meta-gateway

# 1. 先构建前端静态资产
cd web && npm ci && npm run build && cd ..

# 2. 再构建二进制
go build -o bin/meta-gateway ./cmd/server

# 3. 启动
ADMIN_TOKEN=my-admin-token \
MASTER_KEY=0123456789abcdef0123456789abcdef \
./bin/meta-gateway
```

## 构建顺序不能颠倒

`web/` 的构建产物写入 `internal/webui/dist`，由 `go:embed` 编译进二进制。**Go 二进制里没有独立的 index.html**，静态资产由 Go 服务端直接提供。

所以：

- **顺序必须是 Node → Go。** 先 `go build` 再 `npm run build`，二进制里嵌的是旧界面。Dockerfile 与 CI 走的是同一条顺序。
- 改了 `web/src` 却没重新 `npm run build`，Go 侧跑的还是旧 UI —— 这是本项目最高频的「改了没生效」原因。

```bash
# 完整的交付前检查（少跑一项就可能被 CI 挡下）
cd web && npm run lint && npm run typecheck && npm test -- --run && npm run build && cd ..
gofmt -l . && go vet ./... && go build ./... && go test ./...
```

> [!TIP]
> `npm run lint` 不要省。CI 的 Verify 步骤是 `lint && typecheck && test && build`，一条 eslint error（例如测试文件里没被用到的导入）就能让 CI 全红，而 `tsc` 和 `vitest` 都不会报未使用的导入。

## 前端开发服务器

```bash
cd web
npm ci
npm run dev      # 127.0.0.1:4173，base 为 /console/
```

开发服务器需要网关后端在 `:4100` 上跑着才有数据。Vite 的 `base` 是 `/console/`，直接访问 `http://127.0.0.1:4173/` 会 404。

## 运行时的最小环境变量

```bash
ADMIN_TOKEN=test \
MASTER_KEY=test-key-32-chars-long!!!!!!! \
METRICS_TOKEN=test \
./bin/meta-gateway
```

## 相关

- [贡献指南](https://github.com/ZiChuanLan/meta-gateway/blob/master/CONTRIBUTING.md)
- [配置全表](/reference/env-vars)
- [架构总览](/operations/architecture)
