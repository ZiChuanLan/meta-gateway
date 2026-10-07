# 接入

Meta Gateway 对下游暴露 OpenAI 与 Anthropic 兼容协议。多数客户端只需要改两处：**API Base URL** 指向网关，**API Key** 填管理后台颁发的下游令牌。

| 客户端类别 | Base URL | 传输协议 | 鉴权填法 |
| --- | --- | --- | --- |
| Cursor / VS Code / Cherry Studio | `http://<网关>:4100/v1` | OpenAI 兼容 | 下游令牌 |
| Claude Code 等原生客户端 | `http://<网关>:4100/v1` | Anthropic Messages | 下游令牌 |
| Open WebUI / LibreChat | `http://<网关>:4100/v1` | OpenAI 兼容 | 下游令牌 |

## 最小可用请求

```bash
curl http://localhost:4100/v1/chat/completions \
  -H "Authorization: Bearer <你的下游令牌>" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "deepseek-v4-flash",
    "messages": [{"role": "user", "content": "Hello!"}]
  }'
```

```python
from openai import OpenAI

client = OpenAI(base_url="http://localhost:4100/v1", api_key="<你的下游令牌>")

response = client.chat.completions.create(
    model="deepseek-v4-flash",
    messages=[{"role": "user", "content": "Hello!"}],
)
print(response.choices[0].message.content)
```

## 本节内容

| 页面 | 内容 |
| --- | --- |
| [鉴权与下游令牌](./auth) | 令牌、作用域（scopes）、配额与限流如何影响接入 |
| [流式与 SSE 语义](./streaming) | 保活帧、超时、断流处理与取消 |
| [图像生成与编辑](./image-editing) | 生成 / 编辑协议、体积上限、非幂等不重试 |
| 下游端点全表 | 见[参考 / 公开接口](/reference/)（由代码生成） |
| 错误语义与错误码 | 见[参考 / 错误码](/reference/)（由代码生成） |

## 三条容易踩的边界

1. **未登记的 `/v1` 路径会被原样透传**，但只接受路径闭集白名单（每段仅 `[A-Za-z0-9_-]` 与 `.`，最多 8 段）。query 不参与上游 URL 构造，所以不会被透传。
2. **生成类端点默认不做故障转移重试。** 图像、音频、视频、音乐与 `responses` 都属此类——上游可能已经计费，重放等于第二次扣费。带 `Idempotency-Key` 时音频 / 视频 / `responses` 会重试，**图像始终不会**。完整矩阵见[流式与 SSE 语义](./streaming#重试与幂等)。
3. **`/v1/responses` 在只有 chat/completions 的上游上会自动转换。** 网关先试原生 `/v1/responses`；上游回 `404`/`405` 时，把同一个请求改写成 `chat/completions` 重放一次（带 `stream` 时再把回答转回 Responses 事件流），计费与用量仍然按真实上游读数记。

### Responses 客户端的两个例外

Codex、Cursor 这类客户端默认走 Responses API。接到只有 chat/completions 的渠道上时：

| 请求内容 | 行为 |
| --- | --- |
| 普通对话 + function 工具（含 Codex 的 `exec_command` 等） | **可用**：正常转换 |
| 服务端工具（`web_search`、`file_search`、`mcp`、`namespace` 等） | **转换时丢弃**，并在响应头 `X-Meta-Dropped-Tools` 里列出（例：`web_search,namespace:multi_agent_v1`）；日志也记一行。模型只是不会用那个工具，对话照常 |
| `previous_response_id` / `conversation` / `background: true` | **拒绝（501）**：这些状态存在上游，网关无法重建，只有原生 Responses 上游能服务 |
| `tool_choice` 强制指定一个被丢弃的工具 | **拒绝（501）**：不能向上游要一个它没有的工具 |

> [!TIP]
> 看到 `X-Meta-Dropped-Tools` 就说明这个渠道的上游不支持 Responses 的服务端工具。要让这些工具真正生效，需要把模型路由到有**原生 `/v1/responses`** 的上游；否则客户端只能拿到不带搜索的对话。
