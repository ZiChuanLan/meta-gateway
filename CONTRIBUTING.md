# 贡献指南

感谢你对 Meta Gateway 的关注！以下是参与贡献的基本流程。

## 开发环境

```bash
# 前置条件
# Go 1.26+ / Node.js 24+ / Git

git clone https://github.com/ZiChuanLan/meta-gateway.git
cd meta-gateway

# 安装前端依赖并构建
cd web && npm ci && npm run build && cd ..

# 构建后端
go build -o bin/meta-gateway ./cmd/server

# 启动开发环境
ADMIN_TOKEN=test MASTER_KEY=test-key-32-chars-long!!!!!!! METRICS_TOKEN=test ./bin/meta-gateway
```

## 提交规范

- Commit message 使用英文
- 格式：`<type>(<scope>): <description>`
- 类型：`feat` / `fix` / `docs` / `chore` / `refactor` / `test` / `ci`
- 示例：`feat(checkin): add external site cookie support`

## 测试

提交前请把下面这一整套跑一遍（CI 会逐条卡住，少跑一项就可能被挡下）：

```bash
# 后端
gofmt -l .              # 应无输出
go vet ./...
go build ./...
go test ./...           # CI 另跑一遍 go test -race -timeout 20m ./...

# 文档与代码一致性（改了代码里被参考层覆盖的事实时必跑）
go run ./tools/docsgen
git diff --exit-code docs/reference

# 前端
cd web && npm run lint && npm run typecheck && npm test -- --run && npm run build && cd ..

# 文档站
cd docs && npm ci && npm run build && cd ..
```

几个容易踩的点：

- **`npm run lint` 不要省。** 一条 eslint error（例如测试文件里没被用到的导入）就能让 CI 全红，而 `tsc` 与 `vitest` 都不会报未使用的导入。
- **构建顺序必须是前端 → 后端。** `web/` 的产物写入 `internal/webui/dist` 并由 `go:embed` 编进二进制；先 `go build` 再 `npm run build`，二进制里嵌的是旧界面。产物更新后要一起提交。
- **本地跑 `-race` 需要 C 编译器**（`CGO_ENABLED=1` 加可用的 `gcc`），否则会以 `cgo: C compiler "gcc" not found` 退出——那看着像代码错，其实是环境问题。
- **`docs/reference/*.md` 是生成物，不要手改。** 环境变量、运行设置、管理面与公开端点、错误分类、数据表、连接类型、供应商 profile 全部在内；改了其中任何一项就要重跑生成器并提交结果。
- **新增一页文档要同时改 `docs/.vitepress/docTree.ts`**，nav 与 sidebar 都从它派生。内链死链会让文档站构建失败。

## Pull Request

1. Fork 本仓库
2. 创建特性分支 (`git checkout -b feat/my-feature`)
3. 提交更改 (`git commit -m 'feat(scope): add something'`)
4. 推送到分支 (`git push origin feat/my-feature`)
5. 创建 Pull Request

请确保：
- 测试全部通过
- 代码通过 `gofmt` 和 `tsc` 检查
- 新功能附带测试用例
- 文档已同步更新（如适用）；改了 `docs/reference` 覆盖的事实时跑过 `go run ./tools/docsgen`

## 报告 Issue

请使用 GitHub Issue 模板，包含：
- 问题描述
- 复现步骤
- 期望行为
- 实际行为
- 运行环境（OS、Docker 版本、Go 版本等）

## 代码风格

### Go
- 遵循 `gofmt` 默认格式
- 使用 `go vet` 检查常见问题
- 避免不必要的依赖

### TypeScript/React
- 使用 `tsc --strict` 模式
- 组件使用函数式写法 + Hooks
- 样式使用 CSS 变量（主题色）

## 许可证

提交贡献即表示你同意将代码以 MIT 许可证开源。
