# Jev 自动选路插件

把客户端的 `model: "auto-jev"` 交给 TypeSafe 的 [System One](https://docs.typesafe.ai/concepts/system-one) 模型 **Jev**，由它在**网关当前真正能路由**的模型里选一个来回答。

两种路由形状：

- **场景路由（推荐）**：先判这条请求属于哪类工作（code / simple / complex…），再在该场景**自己的候选**里选。写代码的请求只在写代码的模型之间比较，而不是把全部模型拉到一个问题里。
- **平铺路由**：只配一份候选清单，一次判断直接选模型。

客户端不需要知道任何具体模型名，网关不需要写路由规则，选路依据（哪个模型擅长什么）由你在插件配置里用自然语言描述。

## 它长什么样

```
客户端 → POST /v1/chat/completions  {"model":"auto-jev", …}
网关   → 命中本插件的 route 钩子（match_models: ["auto-jev"]）
插件   → 判断①：这条请求属于哪个场景？（choice，选项是你配的场景）
Jev    → {"choice":"code","confidence":0.91}
插件   → 判断②：在 code 场景的候选里选一个（该场景只剩一个模型时跳过这一步）
Jev    → {"choice":"claude-sonnet-4","confidence":0.87}
插件   → {"handled":true,"model":"claude-sonnet-4","reason":"code → claude-sonnet-4; …"}
网关   → 按 claude-sonnet-4 正常选路、计费、日志、故障转移
客户端 → 响应头 X-Meta-Hook-Decision: claude-sonnet-4 (0.87) via jev-router
```

## 快速开始

```bash
cd tools/plugins/jev-router
go build -o jev-router .
./jev-router -addr :9110
```

然后在控制台「插件」页 → 注册插件 → `http://127.0.0.1:9110`，填 `TypeSafe API Key`。

`auto-jev` 会自动出现在 `/v1/models` 里（因为它声明在钩子的 `match_models` 中），客户端可以直接使用：

```bash
curl $GATEWAY/v1/chat/completions -H "Authorization: Bearer $KEY" \
  -d '{"model":"auto-jev","messages":[{"role":"user","content":"帮我重构这个函数"}]}'
```

插件自带一个状态页（注册后可内嵌在控制台里），显示判断次数、降级次数与最近 40 条决策及其依据。

## 配置项

**在控制台里配**：插件页（「Jev 自动选路」）顶部就是配置卡片；商店页每个插件行的「配置」按钮打开的是同一份表单。

场景用卡片编辑，**模型从网关实际可路由的列表里选**——不会出现一个没有路由的模型名。配置存成 JSON 下发给插件（`X-Plugin-Config`），也可以用 API 手写：

| 键 | 默认 | 说明 |
| --- | --- | --- |
| `api_key` | — | TypeSafe API Key（`Authorization: Bearer`）。**必填**，缺失时插件拒绝本次钩子而不是猜一个模型。 |
| `base_url` | `https://api.typesafe.ai` | API 根地址，自建代理时改这里。 |
| `model` | `jev-latest` | 用哪个 System One 模型做判断。 |
| `scenarios` | 空 | **场景路由（推荐）**。JSON：`[{"name":"code","hint":"写代码","models":["a"]}]`；手写也支持行格式 `code (写代码): a`。见下节。 |
| `scenario_instructions` | `Which kind of work is this request asking for?` | 判断场景的指令。 |
| `candidates` | 三条示例 | **未配场景时**生效。每行 `模型名: 它擅长什么`。描述会成为 Jev 的 choice criteria。留空 = 用网关下发的全部可路由模型（仅凭名字判断）。 |
| `default_model` | 空 | 兜底模型。留空 = 判断不确定时拒绝本次钩子，让网关按原逻辑报错。 |
| `min_confidence` | `0.5` | 低于该值改用兜底模型。`0` = 永远相信 Jev 的选择。 |
| `jev_timeout_ms` | `1200` | 单次判断的超时。manifest 声明的钩子超时是 2000ms——必须更大，因为网关的计时还包含到本插件的往返。 |
| `instructions` | `Which model should answer this request?` | 判断指令。 |
| `summary_runes` | `2000` | 发给 Jev 的上下文截断长度。只发最后一条用户消息：判断是关于最新请求的，把整段对话发过去只是多付 token。 |

## 场景路由怎么工作

在插件页的配置卡片里直接编：每个场景一行（名字 + 说明 + 模型 chips），模型从网关可路由的列表里选。存下去的形状是：

```json
[{"name":"code","hint":"写代码、调试与重构","models":["claude-sonnet-4","gpt-5"]},
 {"name":"simple","hint":"闲聊、简单问答","models":["deepseek-chat"]}]
```

用 API 手写时也接受行格式（插件两种都读）：

```
code (写代码、调试与重构): claude-sonnet-4, gpt-5
simple (闲聊、简单问答): deepseek-chat
```

- 括号里是给 Jev 看的说明，像 `code` 这种自明的名字可以省掉；说明本身可以带冒号，解析时先取括号内容。
- **两级决策**：先选场景，再在该场景的候选里选。第二问比第一问窄得多（「这三个写代码的模型里选谁」而不是「这十二个里选谁」），判断更准，事后也更容易看出为什么选错。
- **单模型场景只花一次判断**：某场景只剩一个可路由模型时，插件直接返回它，不再问第二遍。
- **两级都在做交集**：场景里网关路由不了的模型会被剔除；整个场景都不可路由时它不参与判断 —— 否则 Jev 可能选中一个必然失败的选项。
- 状态页的「场景」列记下每次判断的结果，置信度与备选分布都在依据里。

延迟因此是 70–500ms（单模型场景）或两倍（多模型场景）。如果对交互式客户端太贵，把场景配得更「单模型」一些（每个场景留一个首选），或者不用场景、改用平铺候选。

## 降级是有意的，不是兜底

四条路径都会退回到一个**明确**的结果，而不是随机挑一个模型：

| 情况 | 行为 |
| --- | --- |
| 候选集为空（配置的模型网关都路由不了） | 用兜底模型；兜底也不可路由则拒绝钩子 |
| Jev 429 / 超时 / 5xx / 返回畸形 | 用兜底模型并记录 `jev_error` |
| Jev 选了网关路由不了的模型 | 用兜底模型（候选集本已与 `available_models` 取过交集，这一层防的是陈旧清单） |
| 置信度低于 `min_confidence` | 用兜底模型，并把 Jev 原本的选择与置信度写进依据 |

**候选集永远与网关下发的 `available_models` 取交集。** 选出一个没有渠道的模型，比选错模型更糟——前者是必失败，后者只是不最优。

## 成本与延迟（官方数字）

| 指标 | 值 | 来源 |
| --- | --- | --- |
| 端到端延迟 | **70–500ms** | [typesafe.ai 官方公告](https://typesafe.ai/blog/introducing-system-one-models-and-jev) |
| 输入 | **$0.042 / MTok** | 同上 |
| 输出 | **免费** | 同上 |
| choice 选项上限 | 255 | [官方 API 参考](https://docs.typesafe.ai/api) |
| 置信度 | 0–1，choice/score 有、noul 没有 | [官方 Confidence 文档](https://docs.typesafe.ai/confidence) |

**成本可以忽略**：一次判断约 800–2000 token 的 state，`1500 × $0.042/MTok ≈ $0.000063`，即 1 美元约合 **1.6 万次**路由决策——相对被路由的那次模型调用（通常 $0.001–0.01）是 0.6%–6%。

**延迟才是约束**：70–500ms 会直接加在首字节上。所以本插件只在 `auto` 上生效——普通模型名既不匹配钩子，也不产生任何调用与延迟。如果这对你的交互式客户端仍然太贵，把 `summary_runes` 调小（state 越小越快），或让客户端只在长任务上用 `auto`。

## 为什么是插件

判断"哪个模型该回答"依赖三件只有你知道的事：你的模型清单、它们各自的强项、以及你愿意为一次请求付多少成本。这些东西变化的速度远快于网关本身，也可能需要私有逻辑。插件形态让它可以独立迭代，网关只负责在正确的时机把它叫起来。

## 发布到插件市场

插件是**本地服务**：它跑在网关旁边（同一台机器、同一个 Docker 宿主，或任意能连到的地址），网关通过 HTTP 调用它。要让别人也能一键安装，需要三件事，前两件已经做好：

### 1. 交叉编译 + 打包

```bash
cd tools/plugins/jev-router
./build-release.ps1 -Version 1.0.0
```

产出 `dist/`：

```
jev-router_1.0.0_linux_amd64.zip
jev-router_1.0.0_linux_arm64.zip
jev-router_1.0.0_windows_amd64.zip
jev-router_1.0.0_darwin_arm64.zip
checksums.txt
```

命名是网关的 `github-release` 安装器要求的格式（`{id}_{version}_{goos}_{goarch}.zip` + `checksums.txt`），它会按自己的平台挑一个下载。每个 zip 里是二进制 + `plugin.json`（`plugin.json` 由二进制自己 `-dump-manifest` 输出，所以不会和运行时不一致）。

### 2. 读网关分配的地址 ⚠️

托管安装时网关会**自己拉起插件进程**：它预留一个空闲端口，通过环境变量告诉插件，然后健康检查**那个**地址。所以插件必须读它 —— 写死端口会永远装不上。

```bash
META_GATEWAY_PLUGIN_ADDR=127.0.0.1:41234   # 监听这里
META_GATEWAY_PLUGIN_KEY=<生成的共享密钥>     # 校验 X-Plugin-Key
```

本插件已经支持：`-addr` 默认取 `$META_GATEWAY_PLUGIN_ADDR`，`-key` 默认取 `$META_GATEWAY_PLUGIN_KEY`，两者都有时自动要求 `X-Plugin-Key`。

### 3. 发布 + 登记

1. 上传 `dist/*` 到 GitHub Release（tag 形如 `v1.0.0`）；
2. 在 registry 仓库（默认 `ZiChuanLan/meta-gateway-plugins` 的 `registry.json`）里加一条 —— 照 `registry-entry.json` 改掉仓库名：

```json
{
  "id": "jev-router",
  "repository": "<你的 GitHub 用户名>/meta-gateway-plugins",
  "permissions": ["relay:intercept"],
  "install": {
    "type": "github-release",
    "artifacts": [
      { "goos": "linux", "goarch": "amd64" },
      { "goos": "linux", "goarch": "arm64" }
    ]
  }
}
```

之后别人在控制台「商店 → 插件市场」里点安装即可：下载 → 校验 sha256 → 解压 → 读 manifest → 在自己的平台上拉起进程。

> 自建 registry 也行：`PLUGIN_MARKET_URLS` 环境变量可以追加任意 registry 地址。
