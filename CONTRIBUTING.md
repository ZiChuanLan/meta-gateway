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

```bash
# 后端测试
go test ./internal/...

# 前端测试
cd web && npm test

# 格式化
gofmt -w .
cd web && npm run typecheck
```

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
- 文档已同步更新（如适用）

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
