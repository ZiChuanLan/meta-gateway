# 基础 URL 与端点规则

这一页解释**网关到底把请求打到了哪个地址**。它是本项目历史上踩坑最多的一处，所以规则写得比较细。

## 一条规则决定 `/v1` 加在哪

不带任何映射时，上游 URL 由 `JoinOpenAIPath(base, path)` 构造，只有三个分支：

| 渠道 base_url 的路径部分 | 结果 | 例子 |
| --- | --- | --- |
| 空 | 补 `/v1` | `https://api.x.com` → `https://api.x.com/v1/chat/completions` |
| **是 API 根**（最后一段是版本形） | 原样拼在其后 | `https://api.zhipu.com/api/paas/v4` → `/api/paas/v4/chat/completions` |
| 其他（挂载前缀） | 前缀后补 `/v1` | `https://host/prefix` → `/prefix/v1/chat/completions` |

**版本形**指最后一段形如 `v1`、`v2`、`v4`、`v1beta`、`v1alpha`（`v` + 数字开头）。所以 `/api/paas/v4`、`/api/v3`、`/v2`、`/v1beta`、`/openai/v1`、`/compatible-mode/v1` 都被识别为 API 根。

> [!WARNING]
> 这里有两个被证伪过的「更简单」的规则，别再引入：
>
> 1. **只找 `/v1` 后缀** —— 会让每个根路径不叫 `/v1` 的厂商全挂掉。智谱的预设请求变成了不存在的 `/api/paas/v4/v1/chat/completions`。类型下拉里每个 cn 厂商预设都因此是坏的。
> 2. **把任意路径都当 API 根** —— 会让挂载前缀型渠道挂掉：上游实际服务 `/ok/v1/chat/completions` 的渠道被发到了 `/ok/chat/completions`。
>
> 两者的取舍点就是**「版本形」**：它是 API 根与挂载前缀的唯一分界。裸的多一段名字（`/ok`、`/fail`、`/prefix`）无法区分，所以一律按挂载前缀处理。

## 把完整端点粘进 base_url 也能用

从 new-api 的 Custom 渠道过来的操作员习惯把**整个上游端点**写进 base 字段：

```
https://api.typesafe.ai/v1/systemone
```

new-api 接受它是因为它把 base 与请求路径原样拼接；而网关构造的是 `<base>/v1/<path>`，同样的输入会变成 `/v1/systemone/v1/chat/completions`。

所以保存时会自动拆成「干净根 + 显式端点覆盖」：

| base_url 的路径 | 是否拆分 | 理由 |
| --- | --- | --- |
| `/v1/systemone` | **拆** | 版本段不在最后 → 这是一个完整端点 |
| `/api/v2/core/generate` | **拆** | 同上 |
| `/chat/completions`（Perplexity 文档给的 base） | **拆** | 结尾是网关自己的中继表面名 |
| `/api/paas/v4`、`/v1beta`、`/openai/v1` | 不拆 | 最后一段本身就是版本 → 已经是 API 根 |
| `/ok`、`/fail`、`/api/invoke` | 不拆 | 没有版本段 → 是挂载前缀，仍要走 `/v1` 拼接 |

**拆错的代价是真实的**：把 `/ok` 拆成端点覆盖，会让该渠道每一个请求都丢掉 `/v1/chat/completions`。这条回归是 Compose E2E 抓出来的。

拆出来的覆盖值总是**绝对形式**（带前导 `/`），转发时直接接在裸主机之后。

## 任意路径透传

`POST /v1/<未登记路径>` 会原样转发到渠道的 `<base>/<同路径>`，不改 body 也不改响应。客户端直接打 `/v1/systemone` 就行，渠道只需要填一个 base_url。

路径走**闭集白名单**（默认拒绝，而不是穷举坏字符）：

- 每段只允许 `A-Za-z0-9` 与 `_`、`-`、`.`；
- 最多 8 段，每段最多 128 字节；
- 拒绝纯点段（`..`、`.`）。

因为 `%` 不在白名单里，任何百分号转义也被拒绝——`/v1/%2E%2E%2Fsystemone` 直接 404，永远到不了上游。

**query 不参与上游 URL 构造**，所以不会被透传。

## 单模型改道

在 payload 规则里对某个模型设置 `upstream_path` 或 `upstream_url`，就能把该模型打到另一个端点，不用为每个端点建一个渠道。

| 写法 | 行为 |
| --- | --- |
| `upstream_url` | 完整 URL，**必须与渠道同 host** |
| `upstream_path` | 替换 base 的路径部分（同 host、同 scheme） |

同 host 限制不是洁癖：请求会带上渠道的 API Key，允许换 host 等于让下游用自建服务器把凭据偷走。

两者都给时 `upstream_url` 优先。空覆盖返回 base 原值；非法值直接报错，**不会**退化成「转发到别处」。

## 映射存在时换用另一套拼接

一旦渠道上有路径映射，URL 拼接改用 `JoinRawPath`：**映射路径原样拼在 Base URL 之后，不再自动补 `/v1`**。

需要 `/v1` 的供应商请在映射值里自己写（例如 `"/v1/models"`）。这正是根路径不是 `/v1` 的供应商需要的行为。

`JoinRawPath` 会避免重复的首段：base 是 `/v1`、映射值是 `v1/models` 时，结果是 `/v1/models` 而不是 `/v1/v1/models`。

## 怎么看实际打到了哪

- **渠道编辑抽屉**里，基础 URL 下方实时显示「将请求到：…」，拼错一眼可见。
- **日志行**在渠道名下方显示真实上游 URL（`proxy_logs.upstream_url`，只保留 scheme + host + path，query / fragment / userinfo 全部剥离）。
- **成功响应**带 `X-Meta-Upstream-URL` 头，值同上。

剥离 query 与 userinfo 是刻意的：它们经常携带凭据（`?api_key=…`），不能进日志、不能进审计事件、不能回传给客户端。

## 相关

- [站点探针](./site-probe)
- [自定义端点映射](/upstream/) —— 请求/响应字段搬运（P2）
- [参考 / 连接类型](/reference/) —— 各厂商预设的 base URL
