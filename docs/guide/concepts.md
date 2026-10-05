# 核心概念

这一页是整个文档的地基。Meta Gateway 的功能都可以还原成下面这几个实体的组合，搞混它们的代价是配置看起来生效了但行为不对。

## 上游三层：站点 → 凭据 → 渠道

| 实体 | 回答的问题 | 关键字段 |
| --- | --- | --- |
| **站点** `sites` | 「这是哪个上游服务商」 | `base_url`、`platform` |
| **凭据** `credentials` | 「用什么身份访问它」 | `kind`（`api_key` / `session` 等）、加密存储的 `secret_enc` |
| **渠道** `channels` | 「哪一套具体配置可以承接请求」 | `base_url`、`type_hint`、`models_csv`、`priority`、`weight`、`status` |

关系是**级联**的：删站点会级联删掉它的凭据与渠道。一个站点可以有多个凭据（多账号轮换），一个凭据可以派生多个渠道（同一账号的不同协议形态）。

> 渠道是转发时**实际被选中**的对象。所有影响转发行为的列（端点覆盖、字段映射、超时、并发上限、代理）都挂在渠道上。

## 下游一层：令牌

| 实体 | 回答的问题 |
| --- | --- |
| **下游令牌** `downstream_keys` | 「哪个客户端凭据在调用」——`token_hash` 存哈希，明文只在创建时显示一次 |
| **令牌分组** `key_groups` | 「这一批令牌共用多少额度与限速」——有自己的 `quota_total_tokens` / `rate_per_minute` / `rate_burst` |

令牌携带 `scopes`（能调哪些端点类别）与 `route_group_name`（用哪一组路由成员）。

## 路由层：路由 → 成员 → 渠道

| 实体 | 回答的问题 |
| --- | --- |
| **路由** `routes` | 「对外这个模型名怎么被承接」——`model_pattern` 是模型名或通配符 |
| **路由成员** `route_members` | 「这条路由有哪些渠道可用、什么顺序、什么权重」 |

一条路由挂多个成员；每个成员指向一个渠道，并带自己的 `priority` / `weight` / `enabled` / 冷却状态。

**路由还承载了一堆行为开关**（都是 `routes` 表的列，不是全局设置）：

- `routing_mode`、`retry_times`、`channel_retry_times` —— 选型策略与重试预算
- `stable_first` 及 `stable_first_*` —— 先稳定后扩展的灰度策略
- `sticky_session` —— 会话粘性
- `max_concurrent` —— 该路由的并发上限
- `max_reasoning_effort` —— 该模型允许的最高推理档位
- `payload_rules` —— 请求体改写规则
- `header_override`、`system_prompt`、`proxy_url` —— 头部覆盖、系统提示词、专用代理
- `image_edit_shim` —— 把带参考图的聊天请求转成图像编辑请求
- `single_member_id` —— 固定成员（该路由只走这一个渠道）
- `mapping_json` —— 该路由的模型名映射

## 两个「分组」不是一回事

这是本项目最容易混淆的地方，两者的名字里都有「分组」，作用域完全不同：

| | 路由成员分组 | 令牌分组 |
| --- | --- | --- |
| 存储位置 | `route_members.group_name` | `downstream_keys.group_name` → `key_groups.name` |
| 粒度 | **每条路由内部**：同一条路由可以有多组成员集，每组自己的优先级顺序 | **全站**：一批令牌归到一个组 |
| 谁选择它 | 下游令牌的 `route_group_name`（空 = 用每条路由的 `default` 组） | 创建令牌时指定，默认 `default` |
| 影响什么 | **选哪些渠道**：例如「高可用生产组」与「低成本测试组」 | **额度与限速**：组级配额与组级速率，与令牌自身配额叠加 |
| 典型用途 | 同一模型对不同客户端开放不同的渠道集 | 按客户/部门切分总预算 |

同一个渠道可以出现在**多个成员分组的多个优先级上**——`(route_id, channel_id)` 在不同 `group_name` 下允许重复，同一分组内不允许（无映射的绑定由唯一索引保证）。

## 模型侧的实体

模型名不是一个字符串，而是四张表合起来的结果：

| 实体 | 内容 | 来源 |
| --- | --- | --- |
| **模型元数据** `model_metadata` | 上下文窗口、模态、厂商、单价 | 人工校正 / 外部目录同步 / 内置推断 |
| **模型能力** `model_capabilities` | 用哪个端点、哪种请求编码、参考图上限、是否异步 | 四档来源：`builtin` / `discovery` / `catalog` / `manual` |
| **模型目录** `model_catalog_sync` | 外部目录（LiteLLM、models.dev）的同步状态 | 定时同步 |
| **模型倍率** `model_ratios` | 控制台「计费倍率」 | 人工设置 |

覆盖优先级统一是 **人工校正 > 外部目录同步 > 内置推断**。人工改过的行标记为 `manual` 并冻结，后续自动写入不再覆盖它。

> 清空一个字段不等于「没有值」，而是「要求下次同步重填」。

## 一次请求的链路

下游 `POST /v1/chat/completions` 进来之后：

1. **鉴权** —— 用 `token_hash` 找到下游令牌，取出 `scopes`、配额、团队授权快照。
2. **限流与配额准入** —— 组级速率 → 模型级速率 → 令牌配额 + 组配额 + 团队账户额度。任一项不足直接拒绝，不走上游。
3. **选路** —— 按 `model_pattern` 找到路由，再按令牌的 `route_group_name` 选出成员集，按优先级/权重/感知维度挑一个渠道。
4. **协议适配** —— 下游协议（OpenAI / Anthropic）→ 中间格式 → 上游协议；渠道上的端点覆盖与字段映射在这一步生效。
5. **转发与重试** —— 每次尝试记录渠道、真实上游 URL、上游模型、上游凭据指纹；失败按重试预算换渠道。
6. **计费** —— 从响应解析用量，按单价链算成本，在**同一个事务**里累加令牌配额、组配额与团队账户额度。

链路的每一步都会落到 `proxy_logs`：`downstream_key_id`（客户令牌）、`route_id` / `route_pattern`、`channel_id`、`upstream_url`、`upstream_model`、`upstream_key_id`。排障就是按这些字段钻取。

## 相关页面

- [基础 URL 与端点规则](/upstream/base-url-rules)
- [路由与成员](/routing/)
- [计费与用量](/billing/)
- [团队与用户](/team/)
