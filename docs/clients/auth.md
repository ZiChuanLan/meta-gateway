# 鉴权与下游令牌

## 请求怎么带凭据

下游令牌放在标准位置，取决于客户端协议：

```http
Authorization: Bearer <下游令牌>        # OpenAI 风格
x-api-key: <下游令牌>                   # Anthropic 风格客户端
```

网关按 `token_hash` 查表定位令牌——**数据库里不存明文**，明文只在创建令牌时显示一次。

## 作用域（scopes）

每个令牌带一组 `scopes`，决定它能调用哪些端点类别。缺少对应 scope 时返回 `403 insufficient scope`，请求不会到达上游。

| scope | 覆盖的端点 |
| --- | --- |
| `relay` | 基础中继能力（令牌的默认 scope） |
| `models` | `GET /v1/models` |
| `chat` | `/v1/chat/completions` |
| `completions` | `/v1/completions` |
| `embeddings` | `/v1/embeddings` |
| `responses` | `/v1/responses` |
| `messages` | `/v1/messages`、`/v1/messages/count_tokens` |
| `images` | `/v1/images/generations`、`/edits`、`/variations` |
| `audio` | `/v1/audio/speech`、`/transcriptions`、`/translations` |
| `moderations` | `/v1/moderations` |

> [!TIP]
> 给只做对话的客户端只开 `chat` + `models`。少开一个 scope，就少一类被误用或被滥用的端点。

## 请求体体积上限

按端点区分，超限在读取阶段就被拒绝：

| 端点 | 上限 |
| --- | --- |
| `/v1/images/generations` | 20 MB |
| `/v1/images/edits`、`/v1/images/variations` | 30 MB |
| `/v1/audio/speech` | 10 MB |
| `/v1/audio/transcriptions`、`/v1/audio/translations` | 30 MB |

## 准入：三层限额依次判定

一次请求要同时通过三层，任何一层不足都在**到达上游之前**被拒绝：

1. **组级速率** —— 令牌所属 `key_groups` 的 `rate_per_minute` / `rate_burst`
2. **模型级速率** —— 按模型维度的限流
3. **配额** —— 令牌自身配额 + 组配额 + 团队账户额度

> **配额与计价正交。** `quota_total_tokens` / `quota_used_tokens` 按 **token 数**扣减，与单价无关；单价只影响 `usage_records.cost`。把单价设成 0 不会让配额免扣。

## 令牌还决定「用哪些渠道」

令牌的 `route_group_name` 选择该令牌使用**哪一组路由成员**：

- 留空 = 使用每条路由的 `default` 成员分组；
- 填具体名称 = 使用该分组（例如只走「低成本测试组」）。

这是把同一模型对不同客户端开放不同渠道集的手段。详见[核心概念 / 两个「分组」不是一回事](/guide/concepts#两个分组不是一回事)。

## 团队模式下的额外一层

开启团队模式后，令牌还携带团队授权快照：哪些模型、哪些公共候选可用。团队授权在**最终候选、插件改选与故障转移**上都生效——用户方案不会写回公共 `route_members`。

详见[团队与用户](/team/)。

## 相关

- [流式与 SSE 语义](./streaming)
- [计费与用量](/billing/)
- [限流与安全](/operations/)
