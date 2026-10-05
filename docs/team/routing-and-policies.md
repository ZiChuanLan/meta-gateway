# 路由方案与策略

## 一、模式门控

所有团队端点都在 `ModeGate` 之后：

| 模式 | 行为 |
| :--- | :--- |
| 个人 | 团队端点不提供服务；`/me/*` 与 `/auth/*` 也不提供 |
| 团队 | 团队端点生效 |

切换回个人模式**保留团队数据**，但会**撤销团队会话并阻止团队 Key 生效**。

> `user_id > 0` 的 Key **不会**降级成个人无限制凭据。这是切换时最重要的一条安全性质。

| 接口 | 作用 |
| :--- | :--- |
| `GET /admin/mode` | 读当前模式 |
| `PATCH /admin/mode` | 切换模式 |
| `POST /admin/mode/owner` | 初始化 owner（`POST /admin/team/bootstrap` 是同一个实现的旧地址） |

模式的控制入口在**设置 → 运行参数 → 运行模式**。

## 二、成员的公共渠道授权

管理员决定哪些公共渠道对成员开放：

| 接口 | 作用 |
| :--- | :--- |
| `GET /admin/team/candidates` | 可授权的公共候选 |
| `GET/PUT /admin/team/settings` | 团队设置（含授权范围） |

授权以 **`TeamAccess` 快照**的形式随令牌带出，并在选型时生效：

```text
候选池
  → 过滤：AllowsModel(model) && AllowsMember(model, memberID)
  → 应用方案覆盖：按成员的 plan 覆盖该成员的 priority / weight
  → 正常选型
```

> [!IMPORTANT]
> **授权在最终候选、插件改选与故障转移上都生效。** 插件不能把一个未授权的渠道「改选」回来——否则授权就是装饰。

## 三、成员自己的路由方案

成员可以在自己的控制台里**按模型编排上游**：调整顺序、开关某个上游、改权重。

| 接口 | 作用 |
| :--- | :--- |
| `GET /me/routes` | 有哪些模型可以编排 |
| `GET /me/routes/{model}` | 该模型当前的上游顺序 |
| `PUT /me/routes/{model}` | 保存顺序 / 开关 / 权重 |
| `DELETE /me/routes/{model}` | 恢复默认 |
| `GET /me/candidates` | 可用的候选 |
| `GET/POST/PUT/DELETE /me/plans` | 具名的方案 |

### 两条边界

1. **用户方案不写回公共 `route_members`。** 它是账户级覆盖，不是对公共配置的修改。
2. **成员只能在自己的授权范围内编排。** 方案里出现未授权的渠道时，选型会把它过滤掉——保存成功不等于会被使用。

## 四、策略

| 接口 | 作用 |
| :--- | :--- |
| `GET /admin/team/policies` | 列出策略 |
| `POST /admin/team/policies` | 新建 |
| `PUT /admin/team/policies/{id}` | 修改 |
| `DELETE /admin/team/policies/{id}` | 删除 |

策略被邀请码与额度码引用（`policy_id`），决定成员加入后拿到的权限与限额模板。

## 五、团队设置

`GET/PUT /admin/team/settings` 返回团队设置，并附带两个状态字段：

| 字段 | 含义 |
| :--- | :--- |
| `has_owner` | 是否已经初始化 owner |
| `role` | 当前调用者的角色 |

`has_owner = false` 是「还没初始化」的信号——此时才允许 bootstrap。

## 相关

- [个人与团队模式](./modes)
- [成员与账户](./members)
- [团队码](./codes)
- [路由选型与故障转移](/routing/selection)
