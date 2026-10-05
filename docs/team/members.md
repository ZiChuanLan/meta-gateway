# 成员与账户

## 一、三种角色

| 角色 | 能力 |
| :--- | :--- |
| `owner` | 全部管理能力，包括把别人设为 `admin` |
| `admin` | 能管团队（成员、码、策略），但**不能**把别人提为 `admin`，也不能动非 `member` 的账号 |
| `member` | 只能用自己账户下的资源：自己的 Key、`/me/*`、自己的路由方案 |

两条硬约束：

- **`owner` 不能被停用。**
- **只有 `owner` 能授予 `admin`。**

## 二、管理接口

| 接口 | 作用 |
| :--- | :--- |
| `GET /admin/team/users` | 成员列表 |
| `POST /admin/team/users` | 新建成员 |
| `POST /admin/team/users/bulk` | 批量创建（同一份模板） |
| `POST /admin/team/users/import` | 从文件导入 |
| `PATCH /admin/team/users/{id}` | 改角色 / 状态 / 额度 / 备注 |
| `GET /admin/team/users/{id}/keys` | 该成员的 Key |
| `GET /admin/team/users/{id}/events` | 该成员的账户事件 |
| `POST /admin/team/users/{id}/revoke-sessions` | **踢掉该成员的所有会话** |
| `POST /admin/team/users/{id}/recovery` | 生成恢复码 |
| `GET /admin/team/users/{id}/identities` | 绑定的第三方身份 |
| `DELETE /admin/team/users/{id}/identities/{identityID}` | 解绑 |

## 三、成员自己的 API（`/me/*`）

成员控制台**只**调用 `/me/*`——服务端按账号角色隔离权限，管理接口的权限绝不下放给成员。

| 分组 | 接口 |
| :--- | :--- |
| 账户 | `GET /me/`、`POST /me/logout`、`POST /me/password` |
| 会话 | `GET /me/sessions`、`DELETE /me/sessions/{id}` |
| Key | `GET/POST /me/keys`、`PATCH /me/keys/{id}`、`POST /me/keys/{id}/reveal`、`POST /me/keys/{id}/rotate`、`DELETE /me/keys/{id}` |
| 模型 | `GET /me/models`、`GET /me/model-catalog`、`GET /me/model-pricing` |
| 用量 | `GET /me/usage/{summary,series,top-models}`、`GET /me/requests`、`GET /me/requests/latency-histogram` |
| 路由方案 | `GET /me/routes`、`GET/PUT/DELETE /me/routes/{model}`、`GET /me/candidates`、`GET/POST/PUT/DELETE /me/plans` |
| 其他 | `GET/PUT /me/preferences`、`GET /me/display-settings`、`POST /me/redeem` |

> **额度码由已登录成员兑换**（`POST /me/redeem`），不是在转发时由 Key 兑换——因为它填充的池子属于**账户**，不属于某把 Key。

## 四、登录入口

| 接口 | 作用 |
| :--- | :--- |
| `GET /auth/options` | 这个实例开启了哪些登录方式 |
| `POST /auth/login` | 用户名 + 密码 |
| `POST /auth/accept` | 用邀请码激活 |
| `POST /auth/recover` | 用恢复码找回 |
| `GET /auth/oauth/{provider}/start` | 第三方登录跳转 |

全部是**公开**端点（浏览器顶层导航），所以它们**不要求 CSRF 头**——只校验 Origin 并限流。

> 不要为了「更安全」给它们加回 CSRF cookie 检查，那只会把登录页逼回 JS 中转。这些端点没有「把已有账户绑到别人会话」的路径：自动注册只会创建/复用**自己**的账户。

## 五、账户事件与审计

成员相关的管理动作都会写审计事件（`resource_kind = team`），**操作者是成员时** `actor_kind` 记为 `user`，否则记为 `admin`。所以「谁把谁的额度改了」是可查的。

## 六、并发与安全

| 机制 | 说明 |
| :--- | :--- |
| `(provider, subject)` 唯一索引 | 防止并发双建第三方身份 |
| `(code_id, user_id)` 唯一索引 | 防止同一账号重复兑同一个额度码 |
| CSRF 头 | 成员写操作要求 `X-Meta-CSRF`（HMAC 校验） |
| 会话代际隔离 | 跨标签页账号变化会保守退出旧界面 |
| 踢会话 | `revoke-sessions` 立即失效该成员全部会话 |

## 相关

- [个人与团队模式](./modes)
- [团队码](./codes)
- [第三方登录](./oauth)
- [路由方案与策略](./routing-and-policies)
