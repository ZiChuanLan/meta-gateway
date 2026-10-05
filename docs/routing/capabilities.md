# 模型能力注册表

网关要知道的不只是「模型叫什么」，还有**怎么调它**：用哪个端点、哪种请求编码、能不能带参考图、是不是异步。

这就是能力注册表存在的原因——`/v1/images/edits` 与 `/v1/chat/completions` 的分工、参考图上限、是否需要 multipart，都不是从模型名能可靠推断出来的。

## 一、能力种类

| `kind` | 含义 |
| :--- | :--- |
| `chat` | 对话 |
| `image_gen` | 图像生成 |
| `image_edit` | 图像编辑 |
| `video` | 视频 |
| `embedding` | 向量 |
| `audio_tts` | 语音合成 |
| `audio_stt` | 语音识别 |
| `rerank` | 重排 |
| `moderation` | 内容审核 |

无法识别时归一到 **`chat`**——最保守的读法（一个未知模型最可能是对话模型）。

## 二、四档来源与覆盖优先级

```
manual  >  catalog  >  discovery  >  builtin
```

| 来源 | 谁写它 |
| :--- | :--- |
| `builtin` | 按模型名的内置启发式推断 |
| `discovery` | 探测时自动标注 |
| `manual` | **人工校正** |
| `catalog` | 外部目录（LiteLLM / models.dev）同步 |

两条规则决定了这套优先级怎么落地：

1. **`manual` 行是操作员覆盖，永不被自动写入覆盖**——发现流程的自动标注与目录同步都不碰它。
2. **`catalog` 行归同步服务所有**，所以它可以自由刷新。判据就是「不是操作员覆盖」：`builtin`、`discovery`、以及更早一次目录同步留下的行都可以被替换。

> **机器推断的行会在规则改进后被重算。** 否则改进一条规则永远到不了已经存在的行——那些行会一直带着旧的错误推断。

## 三、管理接口

| 接口 | 作用 |
| :--- | :--- |
| `GET /admin/model-capabilities` | 列出 |
| `PUT /admin/model-capabilities/{name}` | 写入 / 覆盖（记为 `manual`） |
| `DELETE /admin/model-capabilities/{name}` | 删除 |
| `POST /admin/model-capabilities/auto-tag` | 按内置规则批量补全 |
| `POST /admin/model-capabilities/resolve` | 解析某个模型最终会走哪条能力 |
| `GET /admin/model-capabilities/catalog` | 目录同步状态 |
| `POST /admin/model-capabilities/catalog/preview` | 预览同步会改什么 |
| `POST /admin/model-capabilities/catalog/sync` | 执行同步 |

**`resolve` 是排查「为什么这个模型走了那个端点」的入口**——它返回最终生效的能力，而不是某一行。

## 四、建路由时的自动补全

创建路由时网关会顺手补能力，两条路径：

| 路径 | 时机 | 边界 |
| :--- | :--- | :--- |
| 内置分类器 `AutoTag` | **同步** | 本地、幂等、跳过 `manual` 与 `catalog` 已拥有的行 |
| 外部目录同步 | **后台**，只针对这一个模型 | 队列满 → 静默丢弃并 defer 到下一次计划扫描；worker 里失败只记日志 |

> **两半都不许让保存变慢或变失败。** 队列满时是丢弃，不是阻塞——写这条路径时不要把任何网络调用挪进 HTTP handler。

**通配符会被跳过**（含 `*` / `?`）：`gpt-*` 是匹配器，不是可调用模型名。

## 五、它在控制台哪里

**模型页 → 模型工具 → 模型能力注册表**（弹窗）。

它**不是**工作台的 tab——工作台只保留「图像 / 文字」两个跑测 tab。它描述的是模型清单里的协议数据，所以入口在模型页。

> 移动这类入口时，顺手 grep 一遍提到旧位置的文案（连提示语也算入口）。

## 六、排查

| 现象 | 原因 |
| :--- | :--- |
| 配了能力但没生效 | 该行是不是 `manual`？是的话自动流程不会碰它，但**运行时读的就是它**——检查字段是否填错 |
| 自动标注改不动某一行 | 那一行是 `manual`，或由目录同步拥有 |
| 目录同步看起来没生效 | `POST .../catalog/preview` 先看它打算改什么；只有「不是操作员覆盖」的行会被改 |
| 未知模型的行为奇怪 | 它的 `kind` 可能被归一成了 `chat` |

## 相关

- [一键挂载与模型归一](./auto-match)
- [图像生成与编辑](/clients/image-editing)
- [参考 / 管理 API](/reference/admin-api)
