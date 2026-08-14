# Sidecar Plugin Protocol (v1)

meta-gateway 的扩展商店基于 **sidecar 插件协议**：插件是一个**独立的 HTTP 服务**（任何语言），meta-gateway 负责发现、安装、内嵌与反向代理。这与 Go plugin（.so 动态库，受编译工具链强耦合限制）不同——sidecar 只要求 HTTP，生态与语言无关。

## 目录

- [插件侧契约](#插件侧契约)
- [注册流程](#注册流程)
- [内嵌与代理](#内嵌与代理)
- [鉴权模型](#鉴权模型)
- [生命周期](#生命周期)
- [参考实现](#参考实现)
- [商店 API](#商店-api)

## 插件侧契约

插件服务需要提供三个端点（路径可用 manifest 自定义）：

| 端点 | 说明 |
| --- | --- |
| `GET /plugin.json` | 插件清单（见下） |
| `GET /healthz` | 健康检查；安装时 meta-gateway 会探测，非 2xx 拒绝注册 |
| `GET /`（及任意路径） | 页面与插件 API，由 meta-gateway 内嵌/反代 |

### /plugin.json 清单

```json
{
  "id": "demo-plugin",
  "version": "1.0.0",
  "name": "Demo Plugin",
  "description": "参考实现",
  "capabilities": ["admin_page"],
  "page_path": "/",
  "health_path": "/healthz"
}
```

| 字段 | 必填 | 约束 |
| --- | --- | --- |
| `id` | 是 | 小写字母/数字/`-`/`_`，≤64 字符 |
| `version` | 是 | 任意字符串 |
| `name` | 是 | 商店展示名 |
| `description` | 否 | 商店描述 |
| `page_path` | 否 | 内嵌页面路径，默认 `/` |
| `health_path` | 否 | 健康检查路径，默认 `/healthz` |
| `api_prefix` | 否 | 根路径 API 前缀（如 `/v0/management`）。声明后 meta-gateway 在根路径挂载转发：`{prefix}/*` → `{url}{prefix}/*`，用于前端固定调用绝对 API 路径的插件（如 CLIProxyAPI 的 CPAMC 调用 `/v0/management/*`），无需在插件内手动配置连接地址 |

> **Authorization 透传**：插件内嵌页面的加载走 `?t=` 管理员令牌校验（iframe 无法带请求头）；插件自身的 API 请求若携带 `Authorization: Bearer <插件密钥>`，该头会**透传**到插件（由插件自行校验密钥），而管理员令牌会被剥离且 `?t=` 不会转发——插件密钥是插件 API 自己的安全边界。

## 注册流程

1. 管理员在 **Store** 页填入插件 URL（如 `http://127.0.0.1:9100`）和可选 API Key
2. meta-gateway 请求 `GET {url}/plugin.json`，校验清单（id 合法、version/name 非空）
3. meta-gateway 请求 `GET {url}/{health_path}`（携带 `X-Plugin-Key`），非 2xx 拒绝注册
4. 注册成功 → 插件写入 `plugins` 表（`source=sidecar`，清单持久化）→ 自动安装并启用
5. 商店列表出现该插件，「打开」按钮进入内嵌页面

注册 API：

```
POST /admin/plugins/register
{"url": "http://127.0.0.1:9100", "api_key": "optional-secret"}
```

### 无清单服务（手动注册）

服务没有 `/plugin.json` 时（例如 **CLIProxyAPI 自带的 CPAMC 管理页**），
注册请求可携带 `id`/`name`/`page_path` 作为手动清单，健康检查照常执行：

```
POST /admin/plugins/register
{
  "url": "http://192.129.128.178:8317",
  "id": "cpa-console",
  "name": "CPA 管理",
  "page_path": "/management.html"
}
```

### 接入 CLIProxyAPI（CPAMC）实操

1. 商店注册上述插件（URL 填 CPA 管理端口，page_path 填 `/management.html`）
2. 商店卡片「打开」→ meta-gateway 内嵌 CPAMC 登录页
3. 登录页勾选「自定义连接地址」，填 meta-gateway 的反代地址：
   `http://<网关地址>/admin/plugins/cpa-console/proxy`
4. 输入 CPA 的 management key 登录（CPAMC 会记住连接信息）
5. 之后即可在 meta-gateway 内直接管理 CPA 账号池/配额/日志

## 内嵌与代理

- **页面内嵌**：`/console/plugins/{id}` 页面通过 iframe 加载
  `/admin/plugins/{id}/proxy/?t={admin-token}`。iframe 无法携带请求头，token 通过 query 传递；meta-gateway 反代前校验 `?t=` 与 `Authorization` 等价。
- **API 反代**：`/admin/plugins/{id}/proxy/{path}` 转发到 `{url}/{path}`（无 path 时转发到 `page_path`），query 参数保留。
- 插件响应原样返回（HTML/JSON/流式均可）。

## 鉴权模型

| 方向 | 机制 |
| --- | --- |
| 管理员 → meta-gateway | 现有 `Authorization: Bearer <ADMIN_TOKEN>`，或 iframe 场景的 `?t=` |
| meta-gateway → 插件 | 每次反代请求注入 `X-Plugin-Key: <注册时配置的 API Key>`（未配置则不注入） |
| 插件 → 插件自己 | 插件应校验 `X-Plugin-Key`（参考实现：不匹配返回 401） |

注意：插件页面可见管理员 token（`?t=` 出现在 iframe src 中）。插件是管理员主动安装的信任组件；如不信任某插件，请勿注册。

## 生命周期

- **启用/停用**：Store 卡片按钮，与内置 addon 一致
- **卸载**：删除 DB 记录与插件目录；插件进程不受影响（独立服务）
- **重启恢复**：`source=sidecar` 的插件清单持久化在 `plugins.meta_json`，重启后自动恢复目录条目与启用状态（无需重新注册）
- **健康状态**：注册时强制健康检查；运行期不轮询（请求失败时反代返回 502/503）

## 参考实现

`tools/plugins/demo-plugin/` 是一个完整的 Go 参考实现（单文件，无外部依赖）：

```bash
cd tools/plugins/demo-plugin
go build -o demo-plugin .
./demo-plugin -addr :9100        # 要求 X-Plugin-Key: demo-secret
# 或
./demo-plugin -addr :9100 -no-key  # 跳过 Key 校验
```

在 Store 注册：`http://127.0.0.1:9100`，API Key 填 `demo-secret`。

任何语言皆可实现：只需一个静态 HTTP 服务 + `/plugin.json` + `/healthz` + 页面。

## 商店 API

| 端点 | 说明 |
| --- | --- |
| `GET /admin/plugins/catalog` | 目录（内置 + 远程 + sidecar） |
| `GET /admin/plugins/status` | 安装状态（前端模块门控） |
| `POST /admin/plugins/register` | 注册 sidecar 插件 |
| `GET /admin/plugins/market` | 插件市场列表（拉取所有 registry 源） |
| `POST /admin/plugins/market/{id}/install` | 从市场安装插件（= 注册 + 健康检查 + 启用） |
| `POST /admin/plugins/{id}/activate` | 安装并启用 |
| `POST /admin/plugins/{id}/enable` / `disable` | 启停 |
| `DELETE /admin/plugins/{id}` | 卸载 |
| `GET/POST /admin/plugins/{id}/proxy/*` | 反代插件页面与 API |

## 插件市场（registry 协议）

插件市场是一个**远程 registry.json 文档**，列出一批可安装的 sidecar 插件。默认内置官方源（`lan/meta-gateway-plugins` 仓库的 `registry.json`），可通过 `PLUGIN_MARKET_URLS`（逗号分隔）追加自定义源：

```
PLUGIN_MARKET_URLS=https://example.com/my-plugins/registry.json,https://example.com/more/registry.json
```

### registry.json 格式

```json
{
  "schema_version": 1,
  "plugins": [
    {
      "id": "cpa-console",
      "name": "CPA 管理",
      "description": "OAuth 账号池管理面板",
      "author": "meta-gateway",
      "version": "1.0.0",
      "logo": "https://example.com/logo.png",
      "homepage": "https://example.com",
      "license": "MIT",
      "tags": ["oauth", "管理"],
      "url": "http://127.0.0.1:8317",
      "page_path": "management.html",
      "health_path": "healthz",
      "api_prefix": "/v0/management",
      "channel_path": "/v1"
    }
  ]
}
```

| 字段 | 必填 | 说明 |
| --- | --- | --- |
| `schema_version` | 是 | 目前仅 `1` |
| `id` | 是 | 小写字母/数字/`-`/`_`/`.`，≤64 字符 |
| `name` | 是 | 展示名 |
| `url` | 是 | sidecar 服务地址（http/https，禁止敏感 query 参数） |
| `description` / `author` / `version` / `logo` / `homepage` / `license` / `tags` | 否 | 商店卡片元数据 |
| `page_path` / `health_path` / `api_prefix` | 否 | 无 `/plugin.json` 的服务用这些字段注册（见注册流程） |
| `channel_path` | 否 | 插件对外暴露的 **OpenAI 兼容 API 路径前缀**（如 `/v1`）。声明后插件卡片出现「创建渠道」——渠道 `base_url` = `{url}{channel_path}`，插件以普通上游身份参与路由/冷却/日志 |

安装 = 注册：`POST /admin/plugins/market/{id}/install` 拉取该条目（15 分钟缓存）→ 拉 `/plugin.json`（无则用条目字段）→ 健康检查 → 安装启用。

**建仓指南**：
1. 建一个公开 GitHub 仓库（如 `meta-gateway-plugins`），在 `main` 分支放 `registry.json`
2. 把默认源 URL 改成你的仓库（`internal/plugins/market.go` 的 `DefaultMarketURL`），或通过 `PLUGIN_MARKET_URLS` 追加
3. 每个插件条目指向你的 sidecar 服务地址；社区插件可直接引用
4. 仓库内附 `registry.json` 模板：`tools/market-registry/registry.json`
