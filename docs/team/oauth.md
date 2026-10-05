# 第三方登录

支持 GitHub 与 Linux.do。配置路径：**团队管理 → 第三方登录**（`GET/PUT /admin/team/oauth`）。

## 一、流程

```text
浏览器 → GET /auth/oauth/{provider}/start   → 跳转到 provider
provider → GET /auth/oauth/{provider}/callback → 网关校验 → 建立会话
```

state + PKCE 存在**签名 cookie** 里（HMAC 用 `MASTER_KEY` 派生），**不依赖内存也不依赖会话**；回调要求 state 匹配。

自动注册只会创建或复用**自己**的账户；`team_identities` 靠唯一索引 `(provider, subject)` 防并发双建，撞索引时**回退去读已存在的那条**，而不是报错给用户。

## 二、端点会分裂成两个视角（最容易踩的坑）

| 端点 | 谁访问它 |
| :--- | :--- |
| `authorize_url` | **浏览器** |
| `token_url` / `userinfo_url` | **网关** |

自建测试环境时，这一条会决定成败：把 authorize 指向浏览器可达的地址（如 `127.0.0.1:4501`），把 token / userinfo 指向网关可达的地址（如容器里的 `host.docker.internal:4501`）。

**全都写 `host.docker.internal` → 浏览器报 502**（实测踩过）。

## 三、忽略容器环境代理

OAuth 的 HTTP 客户端**显式不走 `HTTP_PROXY`**，只跟随运行设置里的全局代理。

原因：容器里的 `HTTP_PROXY=127.0.0.1:7897` 指向宿主机，用它去访问 GitHub 会 `connection refused`（日志里表现为 `proxyconnect tcp`）。

这与出网策略的 SSRF 契约是同一条：**环境变量代理被忽略**。

## 四、密钥约定

| 字段 | 留空时 |
| :--- | :--- |
| `client_secret` | **保留已存的值**（与其他凭据字段一致） |
| `client_id` | **移除该 provider**（密钥一并清除） |

> `client_id` 留空是控制台**唯一的删除路径**。想改 client_id 而保留 secret 时，两个字段都要填。

## 五、限流

两个端点（start 与 callback）**共用登录限流**：burst 5 / 15 rpm / IP。

所以一个测试用例里连续 start + callback **不要超过 5 次**，否则会撞 `429`——拆成独立测试环境。

## 六、为什么这两个端点不要求 CSRF

它们是**顶层导航/重定向入口**，浏览器不会带自定义 header。而且它们没有「把已有账户绑到别人会话」的路径（自动注册只创建/复用自己），所以只校验 Origin + 限流。

> 不要为了「更安全」给它们加回 CSRF cookie 检查——那只会把登录页逼回 JS 中转。

## 七、解绑身份

| 接口 | 作用 |
| :--- | :--- |
| `GET /admin/team/users/{id}/identities` | 列出该成员绑定的第三方身份 |
| `DELETE /admin/team/users/{id}/identities/{identityID}` | 解绑 |

## 相关

- [成员与账户](./members)
- [个人与团队模式](./modes)
- [安全 / 第三方登录](/operations/security#七、第三方登录)
