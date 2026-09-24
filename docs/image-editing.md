# 图片生成与编辑

Meta Gateway 的原生图片入口是 `POST /v1/images/generations` 和 `POST /v1/images/edits`。图片编辑不是 `/edit`，也不要求图片模型支持 `/v1/chat/completions`。

| 模型/上游形态 | 生成 | 编辑 | 工作台默认编码 |
| --- | --- | --- | --- |
| GPT-Image | `/v1/images/generations` | `/v1/images/edits` | 生成 JSON；编辑 multipart |
| grok2api 图片编辑模型 | 以注册表能力为准；仅有编辑能力时需要参考图 | `/v1/images/edits` | JSON |
| 通过聊天协议提供图片能力的 Gemini 兼容上游 | `/v1/chat/completions` | `/v1/chat/completions` | JSON |

注册表里的人工设置优先于模型名推断。不同中转服务对同名模型的接口约定可能不同，按实际上游能力配置。

## 直接调用图片编辑

示例中的 `MG_BASE_URL` 是不含 `/v1` 的网关根地址，`MG_API_KEY` 是下游密钥。

GPT-Image 上传参考图片，保留需要使用的 mask、quality 等字段：

```bash
curl "$MG_BASE_URL/v1/images/edits" \
  -H "Authorization: Bearer $MG_API_KEY" \
  -F 'model=gpt-image-2' \
  -F 'prompt=把背景改为浅紫色，保留主体' \
  -F 'image=@reference.png'
```

多个参考文件使用上游支持的 `image[]`。`stream=true` 会按流式响应处理；请求中的文件、mask 和其他字段会继续转发。已配置的模型别名会替换 multipart 中的 model 字段。

grok2api 的编辑模型使用 JSON。以下 URL 由上游读取，也可以换成图片 data URI：

```bash
curl "$MG_BASE_URL/v1/images/edits" \
  -H "Authorization: Bearer $MG_API_KEY" \
  -H 'Content-Type: application/json' \
  -d '{"model":"grok-imagine-image-edit","prompt":"把背景改为浅紫色","image":{"url":"https://example.com/reference.png"}}'
```

直接调用时按上游要求提供请求编码，网关保留原始请求内容及上游响应。图片请求不会自动重发。

## 只有聊天入口的客户端

在「模型」的路由设置中开启「聊天图片编辑兼容」。默认关闭；只对带参考图且需要图片编辑接口的模型生效。

```json
{
  "model": "grok-imagine-image-edit",
  "stream": true,
  "messages": [{
    "role": "user",
    "content": [
      {"type": "text", "text": "把背景改为浅紫色"},
      {"type": "image_url", "image_url": {"url": "data:image/png;base64,..."}}
    ]
  }]
}
```

网关将此请求送到注册表指定的图片编辑接口，再把生成结果作为聊天图片返回。上游非流式出图完成后，才发送兼容的聊天 SSE；这不是逐帧出图。错误保留上游状态，缺少真实用量时不估算 token。

上游通常以**自己域名下的链接**返回图片，而下游客户端只配置了网关地址——它既没有到那个域名的路由，也没有它的凭据。为了让客户端真能显示结果，网关会在包装响应前把该链接**抓下来内联成 data URI**：

- 抓取走与渠道流量同一个受策略约束的出站客户端，私网目标仍被拦、重定向仍复检，只发 `Accept: image/*`。
- **抓取失败、响应不是图片（含 SVG）、或超过 12 MiB 时退回原链接**，不会把成功的编辑变成错误；此时客户端仍需自行可达该域名。
- 抓取超时 20 秒，且只在响应侧发生——不影响上游请求本身。
- 代价是响应体变大（12 MiB 图片约合 16 MiB base64）。若下游明确能访问上游域名、想省掉这份体积，直接调 `/v1/images/edits`：那是纯透传，图片按上游原样以链接返回。

纯文本聊天继续原样转发；已经通过聊天协议处理图片的模型继续使用聊天接口。通配符路由遵守正常的启用状态与匹配顺序。自定义别名需要在能力注册表中登记图像能力。

需要 multipart 的上游使用图片 data URI/上传文件；兼容入口在**请求**侧不会自行抓取远程 URL（响应侧的内联见上）。若客户端提供远程参考图而上游只接受文件，先下载并上传该图片。

## 控制台工作台

「工作台 → 图像」无需下游密钥，使用当前管理会话。自动模式根据参考图数量决定生成或编辑；只支持编辑的模型会提示添加参考图。

出图按张计费，所以「上游连接」可以**指定一条连接单独验证**（留空则走与线上 `/v1` 相同的选路规则）。选择器按**路由成员**列出，标签带该成员的原模型：统一别名会把多个上游模型名挂在**同一条渠道**上，只写渠道名的话每一行都长一样、选谁都等于没选。

参考图合计限制为 20 MiB，工作台 JSON 请求上限 40 MiB。原生图片生成 JSON 上限 20 MiB，原生编辑请求上限 30 MiB；聊天兼容入口沿用聊天请求的 10 MiB 上限。

成功的 `/admin/try/image` 响应返回 `plan`、`images`、上游状态、耗时及渠道。请求可带 `member_id`（精确到路由成员，优先于 `channel_id`）；响应里的 `upstream_model` 是**本次实际发给上游**的模型名，别名场景下只有它能说明这一行打到了哪个模型。需要诊断完整上游响应时，加 `include_raw_response: true`；错误响应继续提供原始响应供管理员排查。

测试使用本地模拟上游，覆盖编码、错误、SSE、用量、分组计费、别名、原图/mask 保留和禁止重发。具体第三方站点的协议差异需结合其实际配置核验。
