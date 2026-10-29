# 验证 OpenAI 兼容上游

兼容性应按「网关版本 + 上游端点 + 模型 + 请求场景」验证。模型列表能读出来，不代表流式、工具调用或所有模型都可用。

## 最小配置

先在测试实例中添加连接，再创建或采纳模型路由。下面都是占位值，不是可调用的服务或密钥：

| 配置项 | 示例 | 说明 |
| --- | --- | --- |
| 连接类型 | `openai-compatible` | 使用现有 OpenAI 兼容适配器 |
| 上游基础 URL | `https://llm.example.com/v1` | 填 API 根；完整拼接规则见[基础 URL 与端点规则](./base-url-rules) |
| 上游 API Key | `<UPSTREAM_API_KEY>` | 在连接的凭据字段中填写，不放进 URL |
| 模型 | `<UPSTREAM_MODEL_ID>` | 从模型发现结果选择，或按上游文档手动配置 |
| 下游令牌 | `<DOWNSTREAM_TOKEN>` | 在网关中单独创建，并允许访问测试路由及 `chat`、`models` scope |

为便于定位，第一次验证只启用一个测试渠道，不设置模型别名、字段映射或强制流式策略。模型发现刷新的是上游候选；使用手动同步模式时，还要将候选采纳为路由并启用成员。下游 `GET /v1/models` 查看的是网关向该令牌开放的模型，不能替代上游模型发现验证。

客户端使用网关地址，例如 `http://127.0.0.1:4100/v1`，以及**下游令牌**；不要把上游密钥当作下游令牌。

以下 Bash 示例需替换占位值；不要开启 shell 跟踪，也不要把含真实令牌的命令或日志贴到 Issue/PR：

```bash
export GATEWAY_BASE_URL='http://127.0.0.1:4100/v1'
export DOWNSTREAM_TOKEN='<DOWNSTREAM_TOKEN>'

curl --fail-with-body --silent --show-error "$GATEWAY_BASE_URL/models" \
  -H "Authorization: Bearer $DOWNSTREAM_TOKEN"

curl --fail-with-body --silent --show-error --no-buffer \
  "$GATEWAY_BASE_URL/chat/completions" \
  -H "Authorization: Bearer $DOWNSTREAM_TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"model":"<UPSTREAM_MODEL_ID>","messages":[{"role":"user","content":"Reply briefly with hello."}],"stream":true}'
```

## 验证清单

| 场景 | 检查内容 |
| --- | --- |
| 模型发现 | 在连接中刷新候选，确认目标模型可见；区分 HTTP 错误与 `invalid_payload`，不要把发现成功当成推理成功 |
| 普通对话 | 使用 `stream: false`，核对非空回复、结束原因及上游实际返回的用量 |
| 流式文本 | 使用 `stream: true`，核对 SSE 增量、结束事件；需要用量时再测试上游是否接受 `stream_options.include_usage` |
| 流式工具调用 | 定义一个无副作用的测试函数，检查调用 ID、函数名、分片拼接后的 JSON 参数；工具调用结束不能被误判为空回复 |
| 工具结果续接 | 保留 assistant 的 `tool_calls`，以同一 `tool_call_id` 添加 tool 消息再请求；网关不会替客户端执行工具 |
| 渠道切换 | 在两个受控测试上游之间模拟首个渠道的 401、429、503；检查备用渠道使用自己的凭据、收到完整原请求，且返回正确的流式工具事件 |
| 错误透传 | 关闭跨渠道故障转移后重试上述模拟错误，检查状态码与错误体；开启故障转移时首个错误可能被备用渠道的成功响应取代 |

不要通过耗尽真实额度或制造大量请求来触发 429。上游错误可以用本地模拟服务验证。可信内网服务需按[出网策略](./index#出网策略)配置最窄范围的例外。

工具参数也受上游模型能力限制。例如某些思考模式不接受强制指定函数的 `tool_choice`。先验证默认工具选择，再单独测试这些可选参数；记录上游的错误信息，不把它误判成网关需要新增供应商适配。

下游无效令牌产生的 401 与上游无效凭据产生的 401 是不同链路。测试时分别记录，避免用下游鉴权成功或失败推断上游是否兼容。工具调用增量已经交付后，不应为了测试切换而重放同一次调用；流式提交与中断规则见[流式与 SSE 语义](/clients/streaming)。

## 无密钥回归测试

仓库内的 HTTP fixture 测试覆盖请求语义、流式工具分片和末尾用量、工具结果续接，以及错误透传/切换时的渠道凭据隔离：

```bash
go test ./internal/proxy -run '^TestOpenAICompatibleHTTP' -count=1 -v
go test ./internal/adapters -count=1
```

这些测试只访问 `httptest` 本地服务，使用合成消息和占位凭据，不访问真实供应商。

提交实测结果时记录源码提交号、上游 API 根、模型、测试日期、通过/失败项目和未覆盖范围。真实 API Key、Authorization/Cookie 头及包含凭据的输出不得进入报告。某一端点通过测试不代表项目为其背书，也不代表其他模型或未来版本已验证。
