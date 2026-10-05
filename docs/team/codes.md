# 团队码

**一张表三种用途**（`team_invites`），所以只有一组管理接口。

| `kind` | 用途 |
| :--- | :--- |
| `invite` | 邀请码，用来加入团队（`max_uses > 1` 时多人可用同一个码） |
| `credit` | 额度码，兑换 token 或金额额度 |
| `recovery` | 恢复码，用来找回账户 |

## 一、码的格式与匹配

短码形如：

```text
XXXX-XXXX-XXXX-XXXX
```

16 位、80 bit。

**哈希存的是「去掉分组符、大写化后的原文」**，所以下面这些写法都能命中同一个码：

- `ABCD-EFGH-IJKL-MNOP`
- `abcdefghijklmnop`
- `ABCD EFGH IJKL MNOP`

旧的长 token 存的是**原样哈希**，继续兼容。

> [!IMPORTANT]
> 宽松匹配**只对「看起来像短码」的值生效**（16 位且字符集属于短码字母表）。否则会把 base64url 的长 token 错误地归一化，导致长 token 再也兑不上。

## 二、两条并发保护

| 约束 | 位置 | 防的是 |
| :--- | :--- | :--- |
| `(code_id, user_id)` 唯一索引 | `team_code_redemptions` | 同一账号重复兑同一个额度码 |
| `used_count < max_uses` | `team_invites` | 码的总使用次数超限 |

两者在**同一个事务**里先检查再入账——所以并发兑换不可能双入账。

## 三、额度码带什么

额度码可以携带两类额度，与账户上的两套限额对应：

| 字段 | 单位 |
| :--- | :--- |
| `quota_tokens` | token 数 |
| `quota_cost` | 金额 |

**两者同时生效，谁先耗尽谁拒绝请求**，不是二选一。

## 四、管理接口

| 接口 | 作用 |
| :--- | :--- |
| `GET /admin/team/codes` | 列出码 |
| `POST /admin/team/codes` | 生成码（指定 kind / 次数 / 额度 / 策略） |
| `DELETE /admin/team/codes/{id}` | 吊销 |
| `GET/POST/DELETE /admin/team/invitations...` | 邀请码的单次使用「面孔」，同一张表的旧接口 |

> `POST /admin/team/invitations` 是**兼容入口**，实现与 codes 相同。新代码应该用 codes。

## 五、一个实现细节：`policy_id` 是 NOT NULL

`credit` 码与策略**语义上无关**，但 `policy_id` 是 `NOT NULL` 且有外键，所以插入时填一个**占位列**（站点第一条策略）。

**不要因为「这列对 credit 无意义」就传 0**——外键会直接拒绝。

## 六、给成员兑换

额度码由**已登录成员**在 `/me` 兑换（`POST /me/redeem`），不是在转发时由 Key 兑换：它填充的池子属于**账户**。

## 相关

- [成员与账户](./members)
- [计费 / 配额](/billing/)
- [参考 / 数据表](/reference/database-schema)
