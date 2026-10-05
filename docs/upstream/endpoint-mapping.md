# 自定义端点映射

转发适配器假定上游是「`/v1` 根下的 OpenAI 形态」。有两类上游打破这个假设，而一套**渠道级**机制同时覆盖它们（`channels` 上的四个 `upstream_*` 列）。

| 上游的问题 | 用什么解决 |
| :--- | :--- |
| **路径根不对** —— 它不在 `/v1` 下服务 | `upstream_path_override` / `upstream_path_map` |
| **协议不对** —— 它的请求/响应体不是 OpenAI chat | `upstream_request_map` / `upstream_response_map` |

四列都在**渠道**上，所以同一个站点下可以有一个走默认 OpenAI 形态的渠道，和一个走自定义映射的渠道。

> [!TIP]
> 控制台的映射区只在「自己接的端点」上出现：渠道类型是 `custom`，**或**该行已经有映射。第二条不可省——供应商 profile 与 base URL 拆分写的就是这几列，藏起来会让操作员既看不到也清不掉它们。

## 一、路径覆盖

`upstream_path_override` 把 OpenAI 相对路径整个换掉。它有**三种写法**，因为操作员表达同一个意图有两种习惯，猜错就会静默打到 404：

| 你写的 | 结果 | 为什么 |
| :--- | :--- | :--- |
| `systemone` | `/v1/systemone` | 裸名字保留约定俗成的 `/v1` 槽位 |
| `models/{model}` | `/v1/models/{model}` | 占位符不算一段，所以这仍是裸名字 |
| `/api/v3/x` | `/api/v3/x` | 带前导斜杠 = 绝对路径，原样 |
| `api/v3/x` | `/api/v3/x` | 多段路径也只能是绝对路径，不可能是槽位猜测 |

绝对形式是给「服务在自己的 base 根之外」的供应商用的：base 是 `https://host`，而端点要打到 `/api/paas/v4/chat/completions`。

> [!WARNING]
> **覆盖路径时不再自动补 `/v1`。** 一旦渠道上有映射，URL 拼接换用 `JoinRawPath`——映射值原样拼在 Base URL 之后。需要 `/v1` 的供应商请在映射值里自己写（例如 `"/v1/models"`）。

## 二、路径映射

`upstream_path_map` 是 `{"<OpenAI 路径>": "<上游路径>"}` 的对象，用来只改部分端点、保留其余。

解析顺序是**确定性的**（不依赖 Go map 的遍历顺序）：

1. **精确匹配** 优先：`{"chat/completions": "systemone"}` 只改这一个端点。
2. 然后是**最长前缀**：键可以 `*` 结尾，`{"embeddings*": "v2/embed"}` 匹配所有以它开头的路径；多个前缀命中时**最长者胜**。
3. 都不命中 → 原路径不变。

映射值里可以用两个占位符：

| 占位符 | 替换成 |
| :--- | :--- |
| `{path}` | 命中前缀**之后**的剩余路径段（含通配符吃掉的部分） |
| `{model}` | 本次请求的模型名 |

```json
{
  "chat/completions": "systemone",
  "embeddings*": "v2/embed/{path}"
}
```

**`upstream_path_override` 优先于 `upstream_path_map`**：两者都配时覆盖生效，映射被忽略。

保存期校验：键不能为空、值不能为空、**值不能带 query 或 fragment**（`?` / `#`），且映射会被**按键排序重新编码**，所以未改动的映射往返后是同一个字符串（可 diff、控制台显示稳定）。

## 三、字段搬运：五种写法

`upstream_request_map` 与 `upstream_response_map` 是**数组**，数组顺序就是操作顺序。每个条目用下面五种写法之一。

### 1. 复制 / 搬运

```json
[
  {"from": "messages.0.content", "to": "state"},
  {"from": "choices.0.message.content", "to": "content", "move": true}
]
```

- **复制保留 JSON 类型**：源是数字，目标还是数字。
- `"move": true` = 先复制再删源（真正的改名）。`from` 与 `to` 相同时不会删（否则等于把刚写的东西删掉）。
- **源不存在 = 静默跳过**，不会往目标写 `null`。这条很关键：搬运 `tools` / `temperature` 这类可选字段就靠它——上游不会收到一个它不认的 `null`。
- `to` 省略时默认等于 `from`（原地改写）。

### 2. 模板

```json
[{"to": "questions", "template": "{\"{id}\":{\"q\":\"{messages.0.content}\"}}"}]
```

模板**产出的永远是字符串**——这正是把「上游期望文本」的值变确定的手法。

引用规则是刻意宽松的，因为模板最主要的用途就是**嵌一段 JSON 字面量**：

| 写法 | 含义 |
| :--- | :--- |
| `{messages.0.content}` | 引用：替换为该节点的值 |
| `{"role":"user"}` | 字面文本——花括号体里有引号/冒号/逗号，所以**不是**路径，原样保留 |
| `\{` `\}` `\\` | 转义出字面的花括号 / 反斜杠 |
| `{a..b}` | **报错**：形状像路径但不是合法路径，所以拼错不会静默变成输出文本 |

判据是「花括号体是否**像路径**」：含空白、引号、单引号、逗号、冒号、花括号的一律当字面量。

- 被引用的节点是**数组或对象**时渲染成紧凑 JSON，所以结构能在插值中存活。
- 引用的路径**不存在**时：该条目跳过并记一条 warning，其余条目照常执行。

### 3. 字面量

```json
[{"to": "model", "value": {"str": "fixed-model"}}]
```

`value` 的形状与 payload 规则一致：

| 写法 | 值 |
| :--- | :--- |
| `{"str": "文本"}` | 字符串 |
| `{"num": 8}` | 数字 |
| `{"bool": true}` | 布尔 |
| `{"null": true}` | null |

### 4. 顶层白名单（`keep`）

```json
[{"keep": ["model", "state", "questions"]}]
```

把**不在名单里的顶层键全部删掉**。

这是唯一一种「删」的写法，它存在的理由很具体：有些上游对请求模型校验极严——**多一个未知字段就 400**（TypeSafe System One 实测连 `temperature` 都拒绝）。而 OpenAI 形态的请求体必然带 `messages`，通常还带 `temperature` / `stream`。

只靠「删除」表达不了这件事：**要删的集合取决于客户端**，只有操作员知道**要保留的集合**。

规则：

- 只支持**顶层键**。带 `.` 或 `[` 的条目会被校验直接拒绝——白名单是给「上游严格校验的信封」塑形用的，嵌套白名单是另一件危险得多的事。
- 至少要有 1 个键。
- 与 `from` / `to` / `value` / `template` / `move` **互斥**。
- **条目按数组顺序生效。** 典型写法是**先读再 keep**：`keep` 在它出现的位置执行，所以读取必须在源还在的时候发生。

```json
[
  {"from": "messages.0.content", "to": "state"},
  {"keep": ["model", "state"]}
]
```

顺序颠倒的话 `state` 会因为源已被删而拿不到值——而且**不会报错**，只是静默少了字段。

## 四、生效顺序

两个方向各跑一次，映射只看**该方向收到的那个 body**：

```text
请求方向：  客户端 body → 适配器 TransformRequest → 字段映射 → 上游
响应方向：  上游 body   → 适配器 TransformResponse → 字段映射 → 客户端
```

两个后果：

1. **响应映射的路径描述的是「客户端看到的文档」**，不是上游原始报文。这样别家后端的 `choices[0].message.content` 与 OpenAI 上游的语义一致——你不需要为每个供应商记两套路径。
2. **两个方向的映射互相独立**，不能拿另一个 hop 的产物当源。这是刻意的限制：它让转发路径保持无缓冲，也让「顺序搞错」不可能发生。

## 五、路径语言（与 payload 规则共用）

字段映射与 payload 规则的 `match.payload` / `actions[].path` 用的是**同一套**路径语法，实现在同一个文件里，所以两边对「路径是什么意思」的理解逐字节一致。

```text
messages.0.content        数组下标
a[0].b                    下标也写作方括号
messages.#.image_url      # = 任意数组元素
```

| 规则 | 行为 |
| :--- | :--- |
| `#` | 任意数组元素。**读取时返回第一个命中的元素**的值 |
| `[n]` | 数字下标。写入时下标超出长度会**自动扩容**，中间补空对象或空数组 |
| 写入不存在的键 | 自动创建容器：下一段是数字就建数组，否则建对象 |
| 空段 | 拒绝（`a..b`、`a.`、`a.[0]`） |
| 括号外的空格 | 拒绝 |
| 嵌套括号 | 拒绝（`a[b[c]]`） |
| 括号未闭合 | 拒绝 |

> **`[...]` 形式的「整包体路径」被明确拒绝**，校验会给出替代写法。想把整个 body 换掉的话，那是适配器层的事，不是这一列。历史设计里有过这个想法，从未实现。

## 六、保存期严格，运行期 fail-open

这是整个特性的核心设计，两边都要理解：

| 阶段 | 行为 |
| :--- | :--- |
| **保存**（管理 API） | **严格**。拒绝互斥组合、非法路径、坏模板、`keep` 里的嵌套键、`[...]` 路径、带 query 的映射值、含空白的覆盖值。同时把 `{}` / `[]` 规范成 `""`，让「无映射」只有一种表示 |
| **运行** | **fail-open**。畸形 JSON → 当成没有映射；body 不是 JSON → 原样透传；目标写不进去 → 记 warning 并**跳过该条目**，其余条目继续；引用缺失 → 跳过该条目 |

为什么保存期必须严格：**映射拼错的运行期表现是静默空操作**（源不存在被跳过、目标非法被记日志丢弃），对操作员来说和「上游拒绝了」无法区分。保存期是唯一能报出这个错误的地方。

为什么运行期必须 fail-open：**网关绝不能因为自己的改写层而丢掉一个请求**。直接改 SQLite 留下的坏行也不该让路由挂掉。

另外：**如果所有条目都没有真正写入**，返回的是原始字节（不重新编码）。所以一份只读取、或源全部不存在的映射，不会改变 body 的字节表示。

## 七、完整例子：TypeSafe System One

TypeSafe 提供 `GET /v1/models`（在裸主机上），但对话面是 `POST /v1/systemone`，收 `{state, model, questions}`、答 `{answers:{...}}`。**这个例子的每个细节都是对着真实 API 测出来的，不是推断的。**

```json
// upstream_path_override
"/v1/systemone"

// upstream_request_map
[
  {"from": "messages.0.content", "to": "state"},
  {"to": "questions", "template": "{\"q1\":{\"q\":\"{state}\"}}"},
  {"keep": ["model", "state", "questions"]}
]

// upstream_response_map
[
  {"from": "answers.q1.noul", "to": "choices.0.message.content", "move": true}
]
```

三个值得注意的点：

1. **`questions` 是按问题 id 索引的对象**，不是数组。
2. **带类型的答案是数字**（`answers.<id>.noul`），所以要一条 `template` 才能到达 OpenAI 期望的字符串 `content`——模板产出永远是字符串，这条规则在这里就是承重的。
3. **`keep` 把信封削到三个键**。TypeSafe 的请求模型是严格的，多一个键（连 `temperature` 都算）就答 `400 api_usage_error`。

> [!NOTE]
> **作用范围限制值得明说**：这套映射把**一个问题**映射到一次调用。System One 是拿一个 state 对一个问题打分，所以**多轮对话无法用这套映射表达**；客户端发来的开头 system 消息会变成那个 state。

因为 TypeSafe 是收录在案的供应商，这份映射**随供应商 profile 一起发布**（见[供应商 profile](/reference/provider-profiles)），操作员只需要在类型下拉里选它，不需要手写上面任何一行。

## 八、完整例子：根路径不是 `/v1` 的厂商

智谱的 base 是 `https://open.bigmodel.cn/api/paas/v4`。不带映射时，网关会拼出 `/api/paas/v4/v1/models`——**不存在**。

现在的做法是 `JoinOpenAIPath` 把「最后一段是版本形」的 base 识别为 API 根，所以这个例子**已经不需要手写映射**了。详见[基础 URL 与端点规则](./base-url-rules)。

什么时候仍然需要 `upstream_path_map`：**同一个渠道里，不同端点的根不一样**。例如模型清单在裸主机上、对话在 `/v1` 下：

```json
{
  "models": "/models",
  "chat/completions": "/v1/chat/completions"
}
```

## 九、排查：配了映射却没生效

按这个顺序看：

1. **日志里的 `upstream_url`** —— 这是实际打到的地址。如果它不是你配的路径，问题在路径列；如果路径对了但上游仍报错，问题在字段列。
2. **`upstream_path_override` 与 `upstream_path_map` 同时配了吗？** 覆盖优先，映射会被忽略。
3. **该列在「转发选型」的投影里存在吗？** 这是本项目历史上踩过两次的坑：列表页显示正常，但转发选型读到的投影漏了那几列，运行时读到零值，功能完全不生效。判据同样是 `upstream_url`。
4. **`keep` 的位置对吗？** 它在数组里的位置就是执行位置，放在 `from` 前面会把源删掉。
5. **保存时报错了吗？** 严格校验只发生在保存期；如果这份映射是直接写进数据库的，运行期只会静默跳过坏条目。

## 相关

- [基础 URL 与端点规则](./base-url-rules)
- [供应商 profile](/reference/provider-profiles)
- [连接类型全表](/reference/connection-types)
