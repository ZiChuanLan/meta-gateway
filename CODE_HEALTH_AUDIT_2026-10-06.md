# Meta Gateway：死代码、重复实现与代码结构专项审查

日期：2026-10-06  
性质：审查报告；未执行业务重构或删除。

## 一、结论

项目不是“到处都是废代码”。更准确的问题是：**功能迁移后有遗留入口；共享能力已经建立，但部分旧实现没有退出；关键流程过度集中；类型断言和不完整的测试掩盖了部分偏差。**

建议按风险排序，而不是按能删除多少行排序：

1. 先消除已经产生业务偏差的重复实现：成员汇总、金额格式化、成员搜索入口、别名解析。
2. 删除有完整引用证据的死代码及其专属样式。
3. 收拢共享流程：更新、表单提交、插件转发、转发后的计费与审计。
4. 最后拆分超大文件和整理 CSS，不把行为修复与全仓格式化混在一个提交里。

### 量化结果

| 检查 | 结果 |
| --- | --- |
| 前端生产 TS/TSX 文件引用图 | 196 个文件，192 个从 main.tsx 可达，4 个不可达 |
| 4 个孤立前端文件 | 合计约 625 物理行，未发现生产或测试导入 |
| Go deadcode，包含所有包与测试入口 | 20 个不可达函数/方法 |
| Staticcheck v0.8.1 | 28 条诊断；并不代表 28 个业务缺陷 |
| Go vet | 通过 |
| 全量 Go 测试 | 通过 |
| Go 格式检查 | internal / cmd / tools 未报告未格式化文件 |
| 前端测试 | 沿用上一轮同一前端源码的结果：72 个文件、432 项通过 |

### 方法和边界

- 用 TypeScript 编译器 API 解析静态 import、re-export 和字符串形式的动态 import，从真实入口建立文件引用图；不是仅搜索组件名称。
- 检查了 App 和团队页面的 lazy import，以及主题注册表，避免把动态加载页面误报为死文件。
- 使用 `golang.org/x/tools/cmd/deadcode` v0.49.0，执行 `-test ./...`。该结果是当前构建配置下的调用图结果，不承诺覆盖仓库外调用者。
- 用 Staticcheck 检查未使用字段、无效赋值、废弃 API 和可疑表达式。
- 进行跨文件规范化代码块匹配，再人工核对业务语义；未把相似 import、类型字段列表计成应重构的业务重复。
- 用 PostCSS 统计规则和相同上下文的重复选择器；**重复 CSS 选择器不自动等于错误或可以删除**。
- 本次未进行性能压测、真实 Docker 更新演练、浏览器 CSS 覆盖率采集或 race 测试。
- 审查期间检测到 `internal/selfupdate/selfupdate.go` 有本轮审查之外的改动，为最终容器补充了 GroupAdd；已保留，未覆盖。上一份报告的更新问题应按后续补丁重新验收，本报告不重复把该字段仍缺失当成现状。

## 二、可确认的死代码与过时接口

### D01 · P3 · 四个未接入应用的前端文件

| 文件 | 物理行 | 当前状态 | 建议 |
| --- | ---: | --- | --- |
| `web/src/components/CodeStreamCanvas.tsx` | 141 | 无生产/测试导入 | 删除旧背景效果实现，核查专属样式 |
| `web/src/components/GlobalSearch.tsx` | 202 | 无生产/测试导入；当前搜索由 CommandPalette 使用全局搜索 API | 删除旧搜索 UI，不删除仍在使用的搜索 API |
| `web/src/components/StatGrid.tsx` | 103 | 无生产/测试导入；页面已有 TelemetryStrip 等实现 | 删除旧统计组件，不连带删除其他地方使用的 useCountUp |
| `web/src/member/KeyDialog.tsx` | 179 | 无生产/测试导入；成员已复用 Keys.tsx 的编辑器 | 删除遗留成员编辑器，不误删其他同名 KeyDialog |

这些文件通常会被前端构建的摇树优化排除，**主要收益是降低误维护成本，不应承诺删除后页面一定显著变快**。相反，仍被导入的对应 CSS 不会因为组件未使用而自动全部消失。

删除验收：重新执行入口引用图、lint、类型检查、完整前端测试和构建；核对主题注册及样式引用。

### D02 · P3 · Go 不可达函数/方法清单

以下由 `deadcode -test ./...` 输出，并检查了定义和文本引用。测试入口也纳入了根集合。

| 位置 | 函数/方法 | 判断 |
| --- | --- | --- |
| `internal/adapters/intermediate.go:49` | OpenAISegment.Name | OpenAISegment 没有构造使用，默认路径直接返回 upstream |
| 同文件 `:51` | OpenAISegment.ToOpenAI | 同上 |
| 同文件 `:55` | OpenAISegment.FromOpenAI | 同上 |
| 同文件 `:59` | OpenAISegment.PivotPath | 同上 |
| 同文件 `:61` | OpenAISegment.WrapOpenAIStream | 同上 |
| `internal/adapters/site_profile.go:121` | CheckinSupported | 当前调用直接读取 profile |
| 同文件 `:127` | AccountSupported | 同上 |
| `internal/alerts/service.go:82` | Service.SetNotifier | 无可达调用 |
| `internal/domain/unify.go:26` | IsUnifyRemovalOp | 无可达调用；不要据此删除历史 op 回放支持 |
| `internal/httpapi/team_oauth_admin.go:243` | TeamHandler.identityCounts | 无调用，注释却声称用于成员列表 |
| `internal/httpapi/plugin_hooks_test.go:88` | hookPluginServer.requestInput | 测试辅助函数本身未使用 |
| 同文件 `:94` | hookPluginServer.responseInput | 同上 |
| `internal/maintenance/sweep.go:74` | NewBalanceSweeper | 已改用 WithRetention 构造器 |
| `internal/plugins/hooks.go:411` | Service.refreshHookEntries | 无调用的旧刷新包装 |
| `internal/proxy/upstream_map.go:310` | frameMarker | 无调用的旧括号路径构造器 |
| `internal/routing/session_chain_test.go:100` | ptrI64 | 未使用测试辅助函数 |
| `internal/runtimeconfig/runtimeconfig.go:472` | Controller.ResyncCheckin | 无调用，仍有旧说明引用 |
| `internal/selfupdate/docker.go:101` | Client.Ping | 无调用 |
| `internal/siteprobe/match.go:283` | Resolver.Patterns | 无调用的访问器 |
| `tools/docsgen/render.go:118` | uniqueSorted | 文档工具内也未使用 |

可按所属包分批删除。OpenAISegment 可以作为一个未使用类型整体清理，但 Anthropic/Responses segment 和 ComposeForwardAdapter 均在使用，不能连文件删除。

### D03 · P3 · 额外的未使用字段、类型和导出

- Staticcheck 确认 `internal/httpapi/team.go:97` 的 TeamHandler.models 未使用。
- Staticcheck 确认 `internal/alerts/service_test.go:24` 的 deliverable 类型未使用。
- TypeScript 符号引用检查未发现 `web/src/lib/format.ts:38` 的 currentCurrency 被使用。
- `web/src/team/types.ts` 中 Invite、OAuthOption 类型未发现生产或测试引用，可列入类型清理批次。

**应保留的反例**：`web/src/lib/consoleStore.ts` 的 resetConsoleStore 没有生产调用，但被测试调用，是有意的测试入口，不属于本轮应删除死代码。

### D04 · P3 · 更新 hook 保留了没有消费者的状态和接口

位置：`web/src/hooks/useOneClickUpdate.ts:44,64-72,114-124`。

唯一生产调用点 UpdateDialog 只取 watch、apply、failure。confirmTarget、setConfirmTarget、failedTarget、poll 均未被消费。

- confirmTarget 初值为 null，内部仅被清回 null。
- 完成/错误回调只有 poll 会设置；消费者从不调用 poll，因此该回调路径在当前应用中不生效。
- 这不是“功能已共享完成”，而是共享接口设计保留下来、旧页面却没有迁移。

建议先确定统一更新状态模型，再删掉无用 API；不要为保留这些接口再增加第三套状态。

### D05 · P3 · Page.kicker 是仍被广泛传入的无效参数

位置：`web/src/components/ui.tsx:94-95`，各页面 `<Page kicker={...}>`。

Page 接受 kicker，但不读取或渲染，调用端仍获取并传递对应翻译。它属于“可调用但没有效果”的死接口，普通未使用导出扫描发现不了。

建议删除 Page 的参数及其专属调用/翻译，或明确恢复设计；不要混淆 `EmptyHero.kicker`，后者确实渲染，并非死代码。

`ResultStrip.status` 则仍有多个真实调用，虽然注释标为 deprecated，也不能直接删除；应先完成调用迁移。

## 三、重复代码已产生的功能偏差

### R01 · P2 · 令牌页绕开统一金额格式化

位置：`web/src/features/Keys.tsx:218-221,802,808`；
`web/src/lib/format.ts:59-79`。

Keys.tsx 自定义 formatCost：非正数显示横线，其他值固定四位小数。共享 formatCost 则使用站点显示币种和汇率，并区分零值。

**结果**：设置显示货币后，仪表盘/日志按汇率展示，令牌费用仍显示原始数值且不带币种；零金额还与无数据混为一谈。

修复建议：金额全部从共享格式化入口输出，明确零、缺失值和原始 USD 配置输入的边界。添加“同一账单在仪表盘、日志、令牌页显示一致”的跨页面回归测试。

### R02 · P2 · 成员令牌页另造汇总，且用断言掩盖字段不匹配

位置：`web/src/member/MemberKeysSource.ts:57-67`；
`web/src/member/MemberDashboardSource.ts:15-17,27`；
`web/src/api/types.ts:243-259`；
`web/src/features/Keys.tsx:688-702`。

成员令牌页读取 `/me/requests?limit=500`，用返回行数和 tokens 求和表示用量；仪表盘已经使用真正的 `/me/usage/summary`。

问题：

1. 历史超过 500 条后，这不是总请求数和总 Token 用量，令牌页可能与概览不一致。
2. `.catch(() => [])` 把网络失败、权限/会话变化等也吞成“零用量”。
3. 返回的是 total_cost，而 UsageSummary 要求 cost，使用 `as never` 让类型检查无法揭示错误。
4. 当前令牌页使用的是请求数和 Token 字段；错写 total_cost 是潜在契约错误，不应声称它已直接导致当前费用列错误——费用列读的是 key.cost。

修复建议：复用账户聚合端点及明确的结果类型，不用列表抽样替代总量。添加 >500 条历史、断网、取消请求和字段完整性测试。

### R03 · P2 · 别名 JSON 至少存在五处解析实现

位置：

- `web/src/lib/alias.ts`：parseMemberMapping / memberRealName。
- `web/src/features/ChannelModels.tsx:114-123`：mappingReal。
- `web/src/features/channels/EditChannelDialog.tsx:358` 附近的内联解析。
- `web/src/features/Keys.tsx:230-246`：aliasReals 内部解析。
- `web/src/features/models/routingPolicy.tsx:43-52`：originModelOf。

共享实现检查 JSON 对象及 real 的字符串类型并 trim；部分副本直接断言 `{real?: string}` 并返回 real，没有运行时类型检查。

**风险**：畸形但可解析的 JSON（例如 real 为数字或带空格）在各页面得到不同结果。成员层/历史路由层回退规则也分散。

建议分两层统一：

1. 一个严格、失败开放的底层 mapping 解析器。
2. 明确表达 member → legacy route 的上层解析函数。

不要为“去重复”删除旧路由映射兼容；应测试新格式、旧格式、空值、错误类型和损坏 JSON。

### R04 · P2 · 身份边界规则没有覆盖当前目录，搜索组件绕开能力控制

位置：`web/eslint.config.js:25-43`；
`web/src/components/CommandPalette.tsx:51,77-81`；
`web/src/App.tsx` 的共享 CommandPalette 挂载。

- import 边界规则覆盖 `src/user/**`、`src/team/**`，当前成员代码主要在 `src/member/**`。
- 因此“成员模块禁止依赖管理员身份实现”的注释与执行规则不完全一致。
- CommandPalette 对所有身份挂载，输入文本便使用管理客户端请求 `/admin/search`，没有成员能力判断。

这说明统一控制台迁移后，部分旧安全/架构边界尚未同步。此处已确认的是成员会发起不应发起的管理面查询；**未据此认定服务端存在越权泄露**。

建议以数据源/能力明确控制搜索；成员只能搜索自己的可访问内容，或只做导航搜索。补充成员搜索不发 `/admin/*` 请求的测试，并更新目录边界规则。

### R05 · P2 · 更新逻辑迁移未结束

位置：`web/src/features/ops/RuntimeSettingsPanel.tsx`、`UpdateDialog.tsx`、`hooks/useOneClickUpdate.ts`。

旧运行设置独立轮询，新弹窗同时使用 hook 和自身轮询，存在三段观察逻辑。具体业务影响已在上一份审查报告 U06–U09 说明。

本轮代码层结论：这应当是“替换迁移”，不是“再增加一个通用 hook”。以唯一状态源替换旧分支后，再移除废弃接口和文案。

### R06 · P2 · 转发入口复制了计费、审计、追踪组装

位置：`internal/httpapi/relay.go:500-593`；
`internal/httpapi/relay_custom.go:111-186`。

可匹配到相同的模型校验、身份提取、追踪初始化、账单记录和日志元数据更新。人工核对发现两条入口的共同能力已经不同步：

- 常规路径有 withHookOrigin 和 setHookDecisionHeader。
- 自定义路径对应位置没有这两步。
- 是否支持流进度等区别可能是有意的，不能把整个函数简单合并。

建议提取“共同请求上下文”和“共同完成记账回调”，协议体解析及重试策略继续由端点控制。为常规/自定义入口建立同一份计费、授权、hook 审计契约测试。

**不可破坏的边界**：图像生成/编辑的非幂等请求不重试；成员授权需在最终候选和故障转移上继续生效。

### R07 · P2 · 插件两条反代路径重复，且同时使用废弃的 Director

位置：`internal/httpapi/plugins.go:436,505`。

proxySidecar 和 forwardAPIPrefix 分别实现目标校验、transport 选择、凭据处理、配置头注入、查询 token 清除、安全响应头等。

Staticcheck 同时提示 Go 1.26 已废弃 Director，建议使用 Rewrite。

建议共享安全转发构造器，并使用 Rewrite；调用端只提供不同的路径计算。必须覆盖管理令牌不外传、插件令牌正常传递、查询 token 清除、Cookie/Host/代理头和异常路径测试，不能直接全局替换字段名。

## 四、结构与维护成本

### A01 · P2 · 热路径单函数超过千行

`internal/proxy/proxy_forward.go` 共约 1,646 行；ForwardWithMeta 从 124 行开始，下一个函数在 1341 行，单函数跨度约 1,200 行。

该函数同时承担请求归一化、团队授权、粘性会话、候选选择、插件决策、并发门控、URL 构造、协议改写、错误分类、重试与状态回传。

这不是单纯格式不好看：不同 return/defer 和重试分支共用可变状态，新增约束容易只覆盖部分路径。

建议逐步形成：

1. 请求与授权准备。
2. 候选选择及 attempt 生命周期。
3. 上游请求构造。
4. 单次执行与结果分类。
5. 重试决策及最终结果组装。

先增加特征测试，再抽取纯函数；保持取消、资源释放、成员授权和非幂等规则。不要一开始重写整个引擎。

### A02 · P2 · 页面容器与业务状态耦合过重

物理行统计包括空行和注释：

| 文件 | 行数 | 建议拆分边界 |
| --- | ---: | --- |
| `web/src/features/Models.tsx` | 2,546 | 模型目录、选中项、路由成员、分组、弹窗编排 |
| `web/src/features/Channels.tsx` | 2,082 | 查询筛选、批量操作、渠道编辑、凭据操作、详情布局 |
| `web/src/features/ops/RuntimeSettingsPanel.tsx` | 1,677 | 各设置域、草稿/保存模型；移出软件更新 |
| `web/src/features/Keys.tsx` | 1,393 | typed 数据源、编辑器、统计、列表操作 |
| `web/src/features/Exchange.tsx` | 1,328 | 文件预览、导入结果、WebDAV 连接与调度 |
| `web/src/features/Logs.tsx` | 1,197 | 筛选、统计、单次尝试详情、导出 |

拆文件本身不解决问题。应把状态所有权和 API 契约一起明确，避免拆成几十个仍互相传递整包状态的小组件。

后端 `internal/plugins/service.go` 约 1,698 行、`internal/account/service.go` 约 1,526 行，也适合按安装/运行/注册与凭据同步/余额/定价分域拆分。

### A03 · P2 · 类型模型重复，映射层依赖 as never

位置：`web/src/features/Keys.tsx:289-290`；
`web/src/member/MemberKeysSource.ts:55,67,84,90`；
`web/src/features/Logs.tsx:1141`；
`web/src/features/keys/KeysSource.ts`。

令牌形态同时存在于 UserKey、DownstreamKey、KeyFormValues、KeysSource 参数和 api(client) 参数中。字段依靠多次手工搬运，随后用 as never 跳过不兼容。

R02 的 total_cost/cost 错误，以及上一轮发现的 group_name 漏传，都是这类结构问题的实际后果。

建议：

- 页面使用符合自身需求的 ViewModel，不让成员结果伪装成完整管理员实体。
- 公共字段与管理专用字段用清晰的组合类型表达。
- 为转换函数声明具体返回类型，移除 as never 后让编译器暴露缺字段。
- 建立创建/编辑的字段往返测试，而不是只测请求成功。

### A04 · P2 · SQL 投影重复，并有可定位的 N+1 查询

位置：`internal/store/channel.go` 的 scanChannel；
`internal/store/route.go` 的 ListOverviews、RoutingCandidates、listCandidatesByRoute、ListRouteOverviews。

- 相同 channel 数据在多个 SELECT 与 Scan 中手工排列，字段增加时维护点分散。
- ListRouteOverviews 先列出所有 routes，然后逐条调用 listCandidatesByRoute，查询数量随路由数量线性增长。

建议共享明确、有序的投影/扫描契约，保留已有投影往返测试；总览考虑一次批量读取成员后按 route_id 分组。

这不意味着现在已经测得性能瓶颈；应对不同路由规模记录查询次数和耗时，再决定批处理实现。不要为了去重复引入隐藏列顺序的反射魔法。

### A05 · P3 · 表单提交与弹层存在并行体系

位置：`web/src/components/ui.tsx`、`web/src/components/Drawer.tsx`、`web/src/team/ui.tsx`；
`web/src/hooks/useAdminMutation.ts`、`web/src/team/useTeamMutation.ts`；
`web/src/features/ops/{AlertRulesPanel,ErrorRulesPanel,PromptGuardPanel}.tsx`。

- 共享 Dialog/Drawer 有统一焦点栈，TeamModal 使用原生 dialog 的另一套生命周期。
- 三种规则编辑器复制了 enabled、error、取消/保存按钮布局。
- 有的页面使用共享 mutation，有的自行维护 busy/error/saved。

它们不应被机械合并：管理员和成员请求的身份边界应保留；原生 dialog 与 portal 也有不同浏览器行为。

建议共享纯交互契约（提交状态、错误展示、关闭保护、草稿提醒），请求执行器继续区分权限。先收拢规则编辑器的页脚，不创建包办所有表单的巨型组件。

### A06 · P3 · 局部源码像压缩产物，缺少统一格式约束

代表文件：`web/src/features/OperatorProfilePanel.tsx`、`UpdateChannelPanel.tsx`、`features/models/PriceFields.tsx`、`themes/registry.ts`。

一些声明、回调和 JSX 挤在同一行，其他文件使用常规多行排版。当前 ESLint 不负责统一这些格式。

建议选择一种 formatter 并纳入检查；首次格式化与行为修改分开提交。不要把国际化长字符串与压缩的业务逻辑混为同一种问题。

## 五、CSS：历史快照与增量补丁叠加

### C01 · P2/P3 · 样式规模大，重复选择器多，但不能整包删

| 样式文件 | 物理行 | 大小 | 规则数 | 相同上下文重复选择器组 |
| --- | ---: | ---: | ---: | ---: |
| `web/src/styles.css` | 11,237 | 244,946 bytes | 1,817 | 94 |
| `web/src/themes/classic/theme.css` | 13,890 | 379,919 bytes | 2,201 | 151 |
| `web/src/themes/classic/compat.css` | 256 | 18,174 bytes | 80 | 3 |
| `web/src/styles/workspaces.css` | 951 | 52,954 bytes | 410 | 4 |

前两份源码合计约 610 KiB，**这是源码体积，不是压缩后的网络传输体积**。main.tsx 同时导入它们及现代主题、兼容样式、成员样式。

确定的维护问题：

- 经典主题是历史 UI 快照，再通过 compat.css 覆盖共享框架；不能仅凭“旧”认定整份不可用。
- 两份大样式中各有 15 条涉及已孤立组件的选择器规则（global-search / code-stream-canvas / telemetry-pod 相关），优先核查和清理这些明确关联的残留。
- 同一元素的最终样式要跨基础样式、主题、兼容层查找，覆盖来源难追踪。
- 例如 `.content`、`.routing-signal.is-on`、经典主题 `.panel` 等重复定义，应合并时核验层叠顺序。
- 共享 body portal 弹层与主题作用域有关，不能只保留 `.theme-*` 内部规则，否则可能损坏弹层。

建议：

1. 首先删除孤立组件专属规则；不要先跑全量 CSS 清洗。
2. 为共享组件、页面布局、主题差异建立清晰归属。
3. 对等价的选择器合并；媒体条件、keyframes 和 intentional override 单独处理。
4. 采集双主题、明暗、手机、嵌套弹层的视觉基线后再逐页迁移。

不要直接使用未配置白名单的 PurgeCSS：项目存在动态 className、运行时状态类与插件界面。

## 六、静态诊断与测试质量

### T01 · Staticcheck 的 28 条诊断分类

| 类别 | 数量 | 位置/解释 |
| --- | ---: | --- |
| U1000 未使用 | 9 | identityCounts、refreshHookEntries、frameMarker、uniqueSorted、TeamHandler.models、deliverable、三个测试辅助函数 |
| SA1019 废弃 API | 3 | responses.go 的 math/rand.Read；plugins.go 两处 Director |
| SA4006 未使用赋值 | 2 | payload_rules_test.go:79,160 的 body = post() |
| SA4000 可疑同值运算 | 1 | pricing_tiers_test.go:110 的 24 % 24 |
| SA2001 空临界区 | 1 | siteprobe/external.go:189 |
| S1002 布尔比较可简化 | 10 | runtimeconfig.go 的 `flag == false` |
| ST1013 HTTP 状态常量 | 2 | discovery/multi_key_test.go |

**需要人工判定的例子**：

- `24 % 24` 当前结果是 0，测试是用 FromHour=ToHour=0 表示全天；更适合写显式 0 并解释契约，不应误报成已知计费事故。
- `math/rand.Read` 在 Responses 转换中生成响应 ID，不是用于密码或访问令牌；应迁移但不能仅据此声称认证漏洞。
- 空临界区是 SyncExternalIfDue 失败后的一对 Lock/Unlock，中间仅有“保留时间戳”注释，没有共享状态操作。可去掉该锁对，保留节流语义。

### T02 · P2 · 测试有“执行了但没验证结果”的迹象

位置：`internal/httpapi/payload_rules_test.go:79,160`。

修改规则后执行 post()，返回 body 被赋值却不使用；测试检查上游收到的改写结果，但后续响应是否成功不一定被断言覆盖。

建议按每个阶段校验 HTTP 状态、响应结构、收到的请求和计费/副作用。不要只把无效赋值改成 `_ = post()` 来消除告警，先判断是否漏了业务断言。

上一份报告中的更新回滚测试同样属于此类：看到“调用了 start”不等于“旧版本成功恢复”。

### T03 · P2 · 当前 CI 没有死代码与深层静态检查门禁

位置：`.github/workflows/ci.yml`、`web/eslint.config.js`、`web/tsconfig.app.json`。

当前有 lint、类型检查、测试、gofmt、vet、race 和构建，但没有：

- Staticcheck；
- 跨文件未使用导出/孤立文件检测；
- 失效的架构 import 边界检查；
- CSS 重复/孤立规则审查；
- 文件复杂度和类型断言增长的约束。

ESLint 的 no-unused-vars 不会自动发现“导出但整个项目没人导入”的文件；TypeScript strict 也无法保护 `as never` 刻意绕过的契约。

建议固定版本工具，先建立基线并阻止新增问题，再逐步清零；不要用 `@latest` 作为 CI 的永久配置，也不要第一天把所有风格建议升级为阻断发布的错误。

## 七、不应该因“旧/重复”而删除的内容

1. 已发布 SQLite 迁移及历史列：迁移编号和旧数据是升级契约。
2. 旧密文读取、旧交换格式、历史统一名称 op 回放：这些保护存量数据。
3. `/app`、`/maintain` 等重定向：属于兼容 URL，需先明确退役政策。
4. 双主题：经典主题有真实注册和使用，不是因为与现代主题相似就能删除。
5. docs/reference 和 internal/webui/dist：生成产物，不能手工“精简”。
6. 成员/管理员独立请求身份：可以共享展示模型，但不能为去重复而下放管理接口。
7. 明确的测试入口和 fixture：生产未使用不等于无用。
8. 各协议适配器的相似请求处理：只有契约相同的部分才适合提取，图像/流式/非幂等差异应保留。

## 八、建议执行清单

### 批次 1：修正已有语义偏差

- R01 金额格式统一。
- R02 成员汇总改用聚合接口，移除错误强转。
- R03 别名解析归一。
- R04 成员搜索能力控制与目录约束。
- 延续上一轮更新安全、额度校验、字段漏传修复。

### 批次 2：低风险清理

- 删除四个孤立前端文件。
- 分包移除确认无用的 Go 函数、字段和测试辅助代码。
- 清理 Page.kicker、旧 ResyncCheckin 注释、未消费的 hook API。
- 清理孤立组件专属样式。
- 删除时重新运行引用检查，避免过程中其他改动已接入这些接口。

### 批次 3：完成共享能力迁移

- 一个更新任务状态模型。
- 明确 typed ViewModel 和请求映射函数。
- 插件反代共享安全构造器。
- 转发入口共享计费/审计上下文。
- 收拢规则表单页脚及结果状态处理。

### 批次 4：结构拆分与样式治理

- 先对 ForwardWithMeta 建立行为特征测试，再按 attempt 生命周期逐步拆。
- 渠道、模型、运行设置页面按状态所有权拆分。
- 优化路由总览 N+1，使用查询数与耗时验证收益。
- CSS 基于视觉回归按组件/页面迁移；独立格式化提交。

### 后续清理的交付标准

- 死代码：引用证据 + 删除后编译/测试，不以 grep 无命中单独决策。
- 去重复：共用行为有契约测试，有意差异保留。
- 类型整改：移除强转后仍通过 strict，不新增 any/never 逃生口。
- 结构拆分：行为无变化，原测试保持，关键边界新增覆盖。
- 前端改动：完整 lint、类型检查、Vitest、构建并确认嵌入产物更新。
- 后端改动：格式检查、vet、全量 build/test；涉及并发的改动执行 race。
- 文档覆盖事实变化：运行 docsgen 并核对参考文档差异。
- 不把纯清理、功能修复、全仓格式化与发布部署混在一个提交中。

## 九、第一批整改进度与费用口径修订

2026-10-06，用户确认开始整改。随后进一步明确：个人与团队都应按实际承接的上游计费，不能因为对外模型名相同就固定成一口价。

### 本批实现范围

- 令牌额度严格校验：负数、非有限数、非整数 Token 限额不再转换为不限额。
- 租户分组编辑显式提交，包括清空，并增加保存后重开的回归测试。
- 成员令牌页改用 `/me/usage/summary`，不再用最近 500 条记录拼总量；失败不会伪装为零用量。
- 令牌请求类型在编辑器、数据源和客户端复用；创建结果只要求真实返回的 id/token，更新结果不再伪装成完整实体。
- 成员命令搜索仅搜索可见导航，不请求或渲染管理面搜索结果。
- 别名读取共用类型检查与归一化，保留历史路由映射回退规则。
- 运行设置首次加载错误可展示与重试。
- 删除四个已确认孤立的前端文件；其余死代码和 CSS 清理仍待后续批次。
- 镜像拉取检查 HTTP 200 流内错误和畸形响应，失败不再被当成拉取完成。
- Compose 向网关传入与 Watchtower 执行器一致的认证令牌。

### 费用整改不是简单合并 formatCost

此前 R01 的建议需要加上前提：**同一费用来源与计价单位才共用换算；上游报价、上游实际扣费和团队销售费用不能混为一谈。**

现状核对：

- Keys 的 cost 来自 `SUM(usage_records.cost)`，由网关当前路由成员/模型价格规则计算。
- 个人、团队目前共用该规则；尚未建立独立的“上游成本 + 团队售价”两套账。
- 上游公开报价不是实际扣费；缺少真实上游账单时只能标为报价或估算，不能包装成实际成本。

因此本批**未修改计费优先级、历史账单、价格输入或金额格式化**。
以上为第一批的历史记录。后续已确认并实现的口径是：保留实际成员价优先，不引入固定的团队模型售价；币种显示只换算已记录金额。新别名未定价时回退实际原模型的元数据价格，详见更新后的 AGENTS.md。

### 未完成范围

更新回滚/任务状态机与部署配置完整保留、独立成本与售价账本、其余审查项、超大函数拆分及 CSS 视觉治理尚未全部实现。不得将“第一批验证通过”表述为两份审查报告已全部整改完成。

### 第一批验证结果

- 前端 lint、typecheck 通过；77 个测试文件、457 项测试通过。
- 前端生产构建通过，`internal/webui/dist` 已重建。
- Go 格式检查、vet、全量 build/test 通过。
- docsgen 执行后，docs/reference 无差异。
- Compose 配置校验通过，使用非默认测试令牌核对网关和 Watchtower 两端认证值一致。
- 本批未运行 race 或生产更新演练；现有会话测试 act 警告、构建的大 chunk/静态与动态导入重叠警告仍待后续治理。

## 十、后续整改记录（持续验证，不代表全项目零缺陷）

### 已落地的功能修复

1. **重命名改为服务端事务**：新增 `/admin/channels/{id}/model-alias`。移动原成员而非删建，保留成员 ID、分组、权重、优先级、价格、冷却和停用状态；重复提交不新增成员；同一渠道的不同原模型可以共用对外名。存在同组重复且含义不明的旧绑定时返回冲突，不盲目删掉有不同价格或授权引用的成员。
2. **同步不重建已改名的原路由**：避免自动发现后出现两份承接成员、绕开原有冷却。
3. **配置与健康状态分离**：成员表单只发送实际变更；后端配置更新不接受旧 `fail_count/cooldown_until/last_error` 覆盖新健康状态。修改价格不再意外冻结继承的优先级和权重。
4. **评分与实际转发名一致**：成员映射、旧路由映射、通配符匹配的具体模型共用 `ResolveUpstreamModel`；错误率、延迟评分使用同一模型身份。自适应评分公式已归并到 `internal/routing/scoring.go`。
5. **费用不因别名丢失**：成员价仍优先；对外模型未定价时查实际原模型元数据；预览和账单复用模型层解析；所有层仍整层替换，不逐字段拼价。历史账单不重算，USD 输入不随显示币种变化。
6. **Responses 转换**：保留标准 `function_call.arguments`、无显式 type 的消息、图像 URL 和 strict=false；嵌套 `reasoning.effort` 可受已声明的上游能力约束；不能无损翻译的有状态/内置工具请求明确拒绝翻译。原生 Responses 仍优先原样透传。
7. **失败响应生命周期**：Responses 无法退回聊天接口时，不提前关闭仍要返回的原始错误体。新增实际 HTTP JSON/SSE 测试，验证内容、用量及只记一笔费用。
8. **更新恢复**：保留旧容器直至替换容器健康；创建、启动、健康检查或超时失败时恢复原容器。最终配置从原容器完整配置构造，并保留挂载、权限组、资源约束等；不安全的自动删除部署明确要求手动更新。容器回退不等于数据库任意向下迁移。
9. **更新任务可恢复观察**：数据目录保存不含凭据/日志的任务标识与时限；浏览器重开恢复观察，不重复提交。前后端统一有界等待，成功还需版本及就绪状态确认；旧失败记录重开后仍可见。
10. **一个更新界面流程**：移除运行设置内独立安装/轮询实现及无效 auto-update profile 命令；检查结果共用查询缓存，明确区分关闭、失败、不可比较、已是最新和高于当前通道。
11. **操作安全**：“启用选中”不再停用未选项；清空探测选择不再触发全量调用；批量渠道操作限并发、保留逐项错误及失败选择；探测结果在进行中及完成时刷新。
12. **权限与登录**：受限管理员复用已有路由，不再嵌套 MemoryRouter；只显示允许管理的团队页面，显示币种走 `/me`。成员搜索不请求管理面数据，部署管理员账户跳转契约保留。
13. **真实状态与语义**：停用渠道不再显示“就绪”；缺上游报价不再被写成“无价格”；实际选中信息缺失不再用第一个候选伪造；费用输入明确是 USD。首次登录引导等待管理员提示结束，避免两个弹层争抢焦点。
14. **插件代理**：两条入口复用安全构造器，改用 Rewrite；管理员令牌不外传，查询 token 清除，客户端 Connection 头不能删除注入的插件鉴权头。

### 已完成的代码治理

- 清理原审查的四个孤立前端文件、20 项不可达 Go 函数及额外未使用字段/类型；删除 Page 的无效 kicker 参数和调用点。
- 清理 30 条孤立组件相关样式选择器，未删迁移、旧密文、旧格式回放或仍在使用的经典主题。
- 令牌请求契约收敛，成员来源不再用 `as never` 伪装创建/更新/汇总结果。
- 上游模型名解析、金额格式、批量执行、协议选择、评分公式和插件代理等重复逻辑已归并。
- 路由总览从逐路由查成员改为批量读取，保留投影往返测试和每个路由的覆盖设置。
- 团队页面的 context 与布局模块分离，布局和受限管理员页面按需加载。
- CI 新增固定版本 Staticcheck、deadcode 和前端入口可达性检查。入口检查位于可被 Git 跟踪的 `web/tools/`，避免被仓库的 scripts 忽略规则漏掉。

### 验收边界和剩余结构性工作

- 已在隔离本地数据实例检查经典/现代主题及 390px 窄屏：菜单、额度编辑、禁用保存、模型页、更新关闭/不可安装状态；过程中发现的引导、健康标签、报价提示问题已补修。没有访问或更改生产数据。
- 真实供应商的全部私有端点、真实 Docker 故障切换及数据库降级恢复并未作线上演练；测试桩通过不能替代这些部署验收。
- `ForwardWithMeta` 和大型页面仍有进一步分层空间。本轮已抽出协议规划、评分和事务边界，但没有为减少行数而整体重写核心引擎。
- 非孤立 CSS 覆盖有一部分属于双主题契约，尚未逐条合并；不得把所有重复选择器都当作死代码。
- 发布过程中浮动镜像标签与并行发布的最终一致性仍需专项演练和发布流程治理。本轮没有擅自推送镜像、标签或更改线上部署。
- 最终质量门结果以实际执行输出为准，不能将本记录当作“所有审查项无条件结案”。

## 十一、更新链路专项核查（2026-10-06 晚，接上一节继续）

第十节记录的是代码层修复已经落地。本节回答“更新到底还出了什么问题”，证据全部来自**外部可核对的事实**（线上 `/healthz`、GitHub Releases API、Docker Hub tags API、GitHub Actions runs API），不是本地推测。

### 11.1 事实

| 事实 | 来源 |
| --- | --- |
| 生产网关运行 `v3.8.6`（commit `a1419ab8`） | `GET https://mg.zichuanlan.top/healthz` |
| 最新公开 Release 是 `v4.0.0-beta.5`（预发布） | `GET /repos/ZiChuanLan/meta-gateway/releases?per_page=10` |
| `v4.0.0-beta.6` **没有** GitHub Release | `GET /repos/.../releases/tags/v4.0.0-beta.6` → **404** |
| 但 `v4.0.0-beta.6` 的 git tag 已推送（`a135ef8`，= `origin/master`） | `git ls-remote --tags origin` |
| 但镜像标签 `4.0.0-beta.6` 与浮动标签 `beta` 都已推到 Docker Hub（2026-10-05 18:20，digest `sha256:030532fa…`） | `GET /v2/repositories/zichuanlan/meta-gateway/tags` |
| 该 tag 的 Release 工作流**全绿**，其中 “Create GitHub release” 这一步 `conclusion=success` | `GET /actions/runs/37353631167/jobs` |
| `latest` 浮动标签仍指向 `3.8.6`（2026-10-02） | 同上 tags API |

### 11.2 由此产生的两个真实缺陷

**R01 · 更新检查看不见最新构建（发布链路）。** 更新检查读的是 GitHub Release，不是镜像标签。`v4.0.0-beta.6` 有 tag、有镜像、工作流绿色，却没有 Release —— 于是 Beta 渠道的“最新版”停在 `v4.0.0-beta.5`，而执行器的 `beta` 标签已经是 `beta.6`。两个事实源不一致，且绿色流水线把“什么都没发布出去”这件事完全藏住。

**R02 · 控制台承诺的版本 ≠ 执行器安装的版本（watchtower 模式）。** Watchtower 只更新部署里那一个浮动标签，控制台无法把自己的渠道偏好写进部署文件。所以操作员点“安装 v4.0.0-beta.5”、执行器实际拉起 `beta.6` 时，前端一直在等 `version == watch.target`，等满 16 分钟预算后报“未在等待时间内确认更新完成”——**而更新其实成功了**；同时这 16 分钟里更新锁一直被占着。这正是 beta.3→beta.5 三个提交一直在追的那个“转圈”的形状，只是之前都当成 socket 模式的 GroupAdd 问题。

### 11.3 本批改动

- `selfupdate.Status` 新增 `from` / `tracking_tag` / `tracking_channel`；`journal` 持久化 `from`（起始构建），使**另一个浏览器**也能确认一次 tracked-tag 交接成功。新增 `TrackingChannel()` 把标签映射成渠道（`latest`→stable、`beta`→beta、固定版本→无渠道）。
- 申请安装时的跨渠道拒绝信息现在点名两侧（目标是什么构建、部署跟踪的 `IMAGE_TAG` 是什么），不再只给一句“去改 IMAGE_TAG”。
- 前端：`web/src/lib/updateState.ts` 提出共享的 `compareVersions` / `updateLanded` / `updateWentBackwards`。**tracked-tag 路径的成功判据改为“版本变了且比原来新”**（socket 路径仍要求精确匹配，因为那条路径确实自己拉取了指定版本）；标签指回旧构建时立即报错，不再空等预算。
- 前端：watchtower 模式下如果跟踪标签交付不了当前渠道，安装按钮禁用并说明要改的部署值；按钮文案改为“安装 {tag} 上的最新构建”，不再承诺一个执行器未必安装的版本号。
- `release.yml`：新增 “Verify the release is published” 步骤，Release 不是公开状态就让工作流失败——绿色但没发布的情况从此可读。
- **修复被审计报告点名的格式门禁**：`gofmt -l .` 原本有 8 个文件不干净（`internal/httpapi/admin_routes.go`、`pricing_validate.go`、`internal/proxy/proxy_health.go`、`proxy_rewrite.go`、`internal/store/member_configuration.go` 及三个新测试文件），CI 的 `test -z "$(gofmt -l .)"` 会直接红。已全部格式化。

### 11.4 新增/更新的测试

- `internal/selfupdate/watchtower_test.go`（新）：`TrackingChannel` 的四种标签映射、跨渠道拒绝信息同时点名目标构建与 `IMAGE_TAG`、`Status` 携带 `from` / `tracking_tag` / `tracking_channel`。
- `internal/selfupdate/journal_test.go`：日志往返新增 `from` 断言（另一个浏览器确认 handed-off 任务的唯一参照）。
- `web/src/lib/updateState.test.ts`：新增 `compareVersions` 排序、`updateLanded`（tracked 路径接受更构建、socket 路径只接受精确版本、缺 `from` 时回退到精确匹配）、`updateWentBackwards`。
- `web/src/features/UpdateDialog.test.tsx`：跨渠道安装被禁用并说明要改的部署值；按钮在 tracked 模式下改说「安装 {tag} 上的最新构建」。
- `internal/httpapi/self_update_test.go`（新）：`/admin/self-update` 与 `/admin/update-channel` 的响应真的带上 console 决策所需的 tag / channel 字段。

### 11.5 本批验证结果

- `gofmt -l .` 干净；`go vet ./...`、`staticcheck@v0.8.1 ./...`、`deadcode -test ./...` 均无输出。
- `go test ./...` 全绿（含 `internal/httpapi` 57s、`internal/selfupdate`）。
- 前端 `npm run lint`、`npm run audit:entrypoints`、`tsc -b`、`vitest run`（82 文件 / 473 项）通过；`npm run build` 成功并已重建 `internal/webui/dist`（3.08s）。
- `go run ./tools/docsgen` 幂等，`docs/reference` 仅 admin-api.md 按新增的 `/admin/channels/{id}/model-alias` 路由更新（需与代码同提交）。
- `docker compose config --quiet` 通过；`release.yml` / `ci.yml` / `docker-compose.yml` 均可被 YAML 解析。
- **未运行 `-race`**：本机没有 C 编译器（gcc/clang 均无），`CGO_ENABLED=1 go test -race` 无法执行；race 门禁只能由 CI 覆盖。

### 11.6 仍需人工动作 / 未覆盖

- **`v4.0.0-beta.6` 的 Release 需要站长在 GitHub 上确认**：API 与公开列表都查不到，而工作流那一步报成功，最可能是创建成了 **draft（草稿）**（草稿对未登录 API 不可见），也可能是创建后被删除。无论哪种，都需要发布或重建该 Release；在那之前 Beta 渠道永远停在 beta.5。
- 本批**没有**动 channel 语义本身：`beta` 浮动标签仍会随正式版前进（文档写明「Beta 渠道接收预发布及后续正式版」），因此“给旧稳定分支打补丁会把浮动标签指回去”这条设计风险仍在（见上一份报告「更新的部署/发布设计风险」），需要单独演练发布流程来治理。
- 未在生产部署上实测一次真实的一键更新（本轮不改线上数据、不触发真实更新）。

## 十二、变更集独立重审（2026-10-06 深夜）

应要求对**未提交的整份改动**（109 个改动文件 + 新增模块）做一次独立复审，重点是细节与功能性问题。方法：逐个读源码（不做脚本批量验证），对每条"已修复"的说法回到调用点核对，并确认门禁可跑绿。

### 12.1 本轮新发现并已修

| # | 问题 | 证据与影响 | 处置 |
| :-- | :-- | :-- | :-- |
| F1 | `rewriteModelName` 解析 `{"real":…}` 时**不 trim**，而 `domain.MemberRealModel`（健康键、`proxy_logs.upstream_model`、模型不存在黑名单、价格回退都用它）trim | `mapping_json` 由 `PUT /admin/routes/{id}/members` 原样落库，因此 `{"real":" gpt-4o "}` 可入库：请求体发**带空格**的模型名（上游多半 404），而日志/黑名单/计费记的是去掉空格的名字——同一请求两条事实 | `internal/proxy/proxy_rewrite.go` 改用共享提取器；新增 `internal/proxy/alias_rewrite_test.go`（JSON / multipart / 畸形 / 与 domain 提取器一致） |
| F2 | Go 侧仍有**三份** `{"real":…}` 解析器：`proxy` 内联、`httpapi.mappingRealName`、`store.mappingRealName`；前端 R03 已收敛，后端没有 | 两份纯副本行为相同，第三份就是 F1 的偏差来源；"两个解析器迟早会不一致"正是仓库自己被咬过多次的教训 | 两份 `mappingRealName` 改为委托 `domain.MemberRealModel`（语义等价，已由现有 unify 测试覆盖） |
| F3 | 日志决策面板在**本次尝试没有记录选中渠道**时什么也不渲染 | 与 I09 的验收口径「缺失时显示'选择记录不可用'」不符：操作员无法区分"没记录"和"面板没加载" | 新增 `logsPage.decisionUnavailable`（zh/en）并在 `selected` 为空时显示 |
| F4 | ESLint 身份边界规则只覆盖 `src/user/**` 与 `src/team/**`，而成员代码现在主要在 `src/member/**` | 报告 R04 的验收项「更新目录边界规则」未落地；规则与注释不一致 | 规则加入 `src/member/**`；`npm run lint` 通过即证明成员模块确实不依赖管理端 client/session/App/styles |

### 12.2 逐项复核为**成立**（记录依据，避免下次重复怀疑）

- **别名事务** `store/model_alias.go`：成员按 id 搬迁（分组、价格、健康、manual_override 全留），同组重复绑定先报冲突，源路由 pin 住移动成员时拒绝，整路由搬迁时复用路由 id 与路由级覆盖，空掉的路由才删除；`discovery.Reconcile` 跳过已是别名目标的模型（只限本渠道）。测试覆盖重复调用、两个原模型共用一个对外名、resync 不重建原名。
- **成员写入**：`PatchConfiguration` 复刻了 `ApplyManualIntent` 的语义（启用→清 `auto_disabled`；停用→清健康字段），且配置表单再也写不到健康列；`updateRouteMember` 只写 body 里出现且非 `null` 的键。前端 `MemberDialog` 发的是与打开时快照的**差集**，`Keys` 编辑则显式发送 `group_name ?? ""`（清空可生效）。
- **计费**：`BillingLayer` 先对外模型名、再实际转发名，**整层替换**、`Priced()` 认阶梯；`priceLayer` 成员层优先；`billingCost` 对 NaN/Inf/负数记 0 并落日志；控制台预览在嵌套查库前先关游标（`rows.Close()`，避免同连接重入）。
- **投影与批量读取**：`routeSelectColumns`/`downstreamKeySelect` 都是单一投影；`ListRouteOverviews` 改为一次批量读并在路由内保持 `priority DESC, weight DESC, id` 顺序，无成员路由仍是 `[]`；`channel_projection_test.go` 同时覆盖批量路径与转发热路径。
- **转发与协议**：`planProtocol` 与原内联逻辑逐条等价（anthropic/responses 分支、native 回退、composed 注入）；Responses 404/405 的原响应体现在只在**确实要换翻译重试**时才关闭，且做了 nil 保护；`downgradeReasoningEffort` 支持嵌套 `reasoning.effort`；`ResolveUpstreamModel` 的优先级（成员 > 路由 > 请求名）与转发路径一致。
- **插件反代**：两条入口共用一个 `Rewrite` 构造器；`Rewrite` 在 hop-by-hop 剥离**之后**执行，客户端 `Connection:` 再也不能抹掉注入的插件凭据；`RawPath` 清空、`SetXForwarded` 补齐。
- **Responses 转换**：有状态/内置工具请求明确拒绝（`previous_response_id`/`conversation`/`background`、非 function 工具），`arguments` 不再丢失，无 `type` 的消息项当消息处理，`input_image` 翻译成 chat 的 `image_url`（无图片时仍发普通字符串），`refusal` 文本不再丢，`strict` 透传，ID 生成改 `crypto/rand`。
- **前端细节**：`parseQuotaInput`（空=不限、非法=阻断提交、token 必须整数、`badInput` 也拦）；`formatCost` 统一到共享实现（0 与"无数据"分开）；探测改为显式"全部/指定"，清空不再等于全量且 0 对时禁用开始；运行设置**错误分支在加载分支之前**（I03 成立）、后台刷新不再覆盖未保存输入（I04 的覆盖半边成立）；成员命令面板不再请求 `/admin/search`。
- **门禁**：`gofmt -l` 干净；`go vet` / `staticcheck@v0.8.1` / `deadcode -test` 无输出；`go test ./...` 全绿；前端 lint / entrypoints / `tsc -b` / `vitest`（82 文件 473 项）/ `build` 通过且 `internal/webui/dist` 已重建；`docsgen` 幂等；`docker compose config --quiet` 通过。入口可达性检查是**真门禁**（只从 `main.tsx` 出发、TS 编译器解析、不可达即退出 1），其计数 197 = 原 196 − 删 4 + 新增 5 个模块，与报告数字对得上。

### 12.3 仍然存在的缺口（本轮未改，需要决策或属渐进项）

1. **I04 的另一半**：运行设置没有"远端已改"提示。未保存的本地草稿不会被后台刷新覆盖（已修），但保存时仍是整对象 PUT，最后写入者胜——另一会话的改动会被静默覆盖。报告的验收要求是"提示远端变化，提供重载或保留选项"。
2. **脏状态提醒**：离开运行设置页没有未保存提醒（共享弹层只有 `busy` 契约，没有 `dirty` 契约）。
3. **A06 没有选定 formatter**：局部文件仍是压缩风格（gofmt/eslint 不管 JSX 排版），报告建议的"选一种 formatter 并纳入检查"未落地；首次全量格式化应与行为改动分开提交。
4. **A01/A02 结构拆分**未做（`ForwardWithMeta` 仍约 1,200 行；Models/Channels/RuntimeSettings 等页面未按状态所有权拆分）——报告本身标为渐进。
5. **C01 非孤立 CSS 覆盖**未合并（属双主题契约，需要视觉基线）。
6. **发布语义**：`beta` 浮动标签仍随每次发布前进（文档写明 Beta 渠道接收后续正式版），"旧稳定分支补丁把浮动标签指回去"的风险仍在，需要发布流程演练治理。
7. 细节小项：`payload_rules_test.go` 第三处断言与前一处的 `choices` 检查重复；`RouteMemberStore.UpdateConfiguration` 在生产代码里已无调用者（仅测试使用，属保留的测试入口）。

## 十三、第二轮：按审计清单继续修复（2026-10-06 深夜—次日）

上一节列出的缺口，本轮按「能机械做的先做、需要重写或需要你决策的明确列出」的原则推进。

### 13.1 已修复

**I04 剩余半边 —— 远端变更提示。** `RuntimeSettingsPanel` 现在记住草稿是从哪个服务端快照播种的（`baseline`），后台 refetch 时：
- 若草稿干净 → 照旧跟随服务端；
- 若草稿有本地修改且服务端快照变了 → 显示提示条，「保留我的修改」/「重载服务端设置」由操作员决定，不再静默覆盖。

同时补上「有未保存的更改」标记。测试用真实组件驱动：改字段 → 令服务端快照变化 → 断言提示出现且本地值仍在（40）→ 重载后变成服务端值（9）；另一条断言「保留」后本地值仍在、未保存标记仍在。

**未保存离开提醒（dirty 契约）。** 新增 `web/src/lib/unsavedChanges.ts`：`beforeunload`（浏览器自己的措辞）+ 调用方包裹站内跳转。设置页把它用在三处：tab 切换、运行设置面板内部的「软件更新」链接（面板自己 preventDefault）；离开 runtime tab 会清掉待保存标记，避免面板卸载后仍提示。测试覆盖「有未保存才拦」「确认框被调用与返回值生效」「干净状态完全不弹」。

**A05 —— 规则编辑器页脚收拢。** 三个规则编辑器（告警规则/错误透传规则/提示词护栏）的「取消/保存 + busy + 错误」页脚原本是逐字节相同的三份，现收拢到 `web/src/features/ops/RuleEditorFooter.tsx`（页脚以外仍各管自己的请求与字段）。新增测试断言提交一次、busy 期间两个按钮都禁用（避免一次慢写变成两条规则）、错误消息显示。

**R06 —— 两个转发入口共享记账。** 审计指出的「常规入口与自定义路径各持一份记账代码」已消除：
- 新增 `internal/httpapi/relay_accounting.go`：`attemptAccountant`（用量落库 = 按成员 id 解析单价、代理日志补时延与客户端族、live trace 收尾）与 `finalizeFailedLiveTrace`（无响应/出错才收尾；成功刻意留到 body 拷完，流式才有字节进度）。
- `relay.go` 与 `relay_custom.go` 的 40 行重复块各自变成一次调用。

新增契约测试 `TestBothRelayEntriesBookUsageTheSameWay`：同一条路由、同一成员价 3/1k，分别打**已登记端点**与**自定义路径**，断言 `usage_records` 恰好两行、路径分别为 `chat/completions` 与 `systemone`、各计 3 元——即「自定义路径不是绕过计费的旁门」。这条测试也是 R06 的验收证据（此前自定义路径只断言了路径/日志/回显，没有任何计费断言）。

**A01 —— 转发主函数拆分（本轮完成 1,160 → 754 行，-35%）。** 抽出四个内聚阶段到各自文件，函数体不再包含它们的实现：
- `internal/proxy/forward_plan.go`：`prepareAttempt`（原 228 行，适配器/协议规划/流策略/别名/系统提示/推理档位/载荷规则/端点解析/插件请求钩子）与 `normalizeAndAuthorize`（请求默认值 + 团队端点闸门；「公开渠道授予的是推理，不是任意使用其凭据」）。
- `internal/proxy/forward_convert.go`：`convertSuccessBody`（原 129 行，流策略折叠/展开、N×M 翻译对的 Response/Stream 模式、适配器 WrapStream、渠道响应映射、空成功判定）。
- `internal/proxy/forward_guards.go`：`globalPromptGuards` / `channelPromptGuards`（两处 switch 合一，顺带去掉一份重复的拒绝逻辑）。
- `internal/proxy/decision_snapshot.go`：`insertDecisionSnapshot`（决策快照 + 插件决策同帧落库）。

**未拆的部分与原因**：剩下的约 750 行是「尝试 → 密钥池 → 重试 → 失败转移」主循环。它持有 `defer releaseAttempt()/releaseGate()` 与 `restartKeyPool:` 标签，方法边界一旦切开就会改变 defer 生命周期与跳转目标——那是重写而非搬运，收益是维护性、风险却在最关键的转发路径上，因此本轮不做，留待有足够验证预算（含 `-race`）时单独进行。

**A02 —— 前端大文件（本轮做机械可做的部分）。** `RuntimeSettingsPanel.tsx` 的 5 个纯展示控件（`SettingLabel` / `ValidatedNumberInput` / `numberValidationError` / `RuntimeSettingsColumns` / `CollapsibleGroup`）移到 `web/src/features/ops/runtimeControls.tsx`：1,588 → 1,446 行，页面只留状态与请求，控件可被其它面板复用。

**细节小项。** `payload_rules_test.go` 里重复的 `choices` 断言去掉一条；`RouteMemberStore.UpdateConfiguration` 补上「HTTP 层刻意不用它」的说明（PATCH 只写客户端真的发过的键，全量写入会把未提交的字段打回零值）。

### 13.2 仍未做 / 需要你决策（不假装做完）

1. **A02 的深水区**：`Models.tsx`(2492)、`Channels.tsx`(2040)、`Keys.tsx`(1343) 都是**单个巨型组件**（`ModelCatalog` 2,360 行、`Channels` 1,975 行），按「状态所有权」拆分意味着把状态与其依赖 UI 一起下沉、穿透 props——属于 UI 重构，不是搬代码。这类改动你要求先看书面逐区块提案，我建议先给提案再动手。
2. **C01 CSS 非孤立覆盖合并**：需要视觉验收（双主题），改完必须由你在运行中的控制台确认；我可以先做「同值覆盖」这一确定的子集并给出前后截图。
3. **A06 未选 formatter**：本仓 JSX 排版仍是压缩风格。引入 Prettier 全量格式化会产生 ~200 文件的大 diff，属独立提交（与行为改动分开）。需要你决定是否引入、以及用哪个工具。
4. **发布链路**：`v4.0.0-beta.6` 的 GitHub Release 仍需你在网页确认（API/公开列表都查不到，工作流那步报成功，最可能是 draft）；`beta` 浮动标签随正式版前进的设计风险需一次发布演练。
5. **`-race` 门禁**：本机无 C 编译器，只能由 CI 覆盖。

## 十四、第三轮：C01（CSS）、A06（formatter）、A02（提案）与提交（2026-10-06 深夜）

### 14.1 C01 —— 先核验审计的前提，再动手

审计写「两份大样式中各有 15 条涉及已孤立组件的选择器规则（global-search / code-stream-canvas / telemetry-pod 相关）」。**实际核查结果与这句不符**，因此按事实重做：

- `global-search` / `code-stream-canvas` / `telemetry-pod` 在 `web/src` 全库（含 CSS）**零匹配**——它们的组件早已删除，规则也已被清理（那 15 条在 HEAD 上确实存在，处理它们的改动已在工作区里，属 C01 第 1 步的完成态）。
- 我用脚本把 CSS 里出现、而 TS/TSX 里从未出现的类名全部列了出来（303 个），但**这份清单不能当死代码直接删**：`driver.js` 会在运行时注入 `driver-popover*` 的 DOM；`badge-*` / `is-*` / `tone-*` / `theme-*` 等大量类名是模板字符串拼出来的。这印证了审计自己的提醒——“不能仅凭旧就认定整份不可用”。
- 审计提到的 `.routing-signal.is-on`（styles.css 3 处、theme.css 3 处）等重复定义**不是等价重复**：`border-color` 一处 32%、一处 30%，后者胜出——属“静默覆盖”，需要视觉判断，不能脚本合并。

因此本轮只做**可证明为中性**的那一类：同一文件、同一 at-rule 上下文、同一选择器文本、同一属性（含 `!important` 标志）下，**除最后一条以外的声明永不参与层叠**（同为普通声明、同一选择器 = 同特异性，最后一条必胜），删除它们不可能改变渲染。

- 结果：**452 条死声明**被删除（styles.css 156、经典主题 294、compat.css 2），三个文件共 -585 行；连带把 `.global-search*` / `.telemetry-pod*` 的规则按“组内只删死成员”的方式补齐（`.telemetry-pod, .stat-card` → `.stat-card`，**不能整条规则删**，否则会带走仍在用的 `.stat-card`——我的第一版正是这么干的，在自查中被 diff 抓住后重做）。
- 刻意不动：**keyframes**（同名 `@keyframes` 不叠加，后者整体替换前者，删里面的声明等于删动画的一帧——本仓 `page-enter` / `dialog-in` 各有三份定义，属独立问题，已单列）、简写/长写对（`margin` 与 `margin-top` 不算同属性）、仅部分重叠的选择器组（`.a, .b` 与 `.a`）、以及 `system.css`（其分页重复块被注释明确记录为“为第二个前端保留的分层”，我不越权改）。

**验证用的是测量而不是眼睛**：同一台机器上构建前后两个二进制，在运行中的控制台里对 12 种「页面 × 主题 × 视口」组合（总览/连接/模型/令牌/日志 × modern/classic + 两个手机视口）逐元素比较 Chrome 暴露的**全部层叠属性**（排除会被文字长度影响的布局值 width/height/margin/padding，并关闭动画以保证确定性）：**结果完全一致**。唯一出现过的差异是 `margin-inline-start` / `transform-origin` 这类**由文本宽度决定的 used value**，且只出现在内容确实变了的那一个元素上（总览页的时间戳文本）。

> 自我纠错记录（值得留档）：第一版合并把“值与前面相同”的**后**一条删掉，看似中性，实则错了——中间若有一条同选择器、同属性、**不同值**的规则，被删的那条正是恢复原值的胜者（经典主题的 `.brand-mark`、表格容器就是这种形状）。发现后用 `git checkout HEAD --` 回滚、从 diff 复原被误删的规则、改用“删前面、留最后”的正确方向重做，全部 gates 与上述测量重新通过。第一版产生的那三份文件版本已备份在 `%TEMP%\merged-*.css.bak` 供比对。

### 14.2 A06 —— 引入 Prettier（按你的选择单独提交）

- 现状测量：285 个 TS/TSX 文件中 **139 个含超过 100 字符的行**（最长 646 字符），风格分裂为「有分号 224 : 无分号 61」。
- 配置取**多数派**以缩小 diff：`printWidth: 100`（与已规范文件的换行宽度一致）、`semi: true`、`singleQuote: false`、`trailingComma: all`、`endOfLine: lf`。
- `.prettierignore` **排除 CSS**：`src/styles*`、`src/themes/**/*.css` 等是手工调过的（部分刻意压成一行），格式化 2.6 万行只会把真实改动埋进空白噪声；构建产物与 lockfile 同样排除。
- 新增 `npm run format` / `npm run format:check`，并把 `format:check` 加入 CI 的 “Verify Web Admin” 步骤，风格不能再漂回去。
- 全量格式化：提交 `b804f54` 共 **308 个文件，+22,870 / −20,982 行**（含重建的 `internal/webui/dist`、新增的配置与 CI 门禁；纯源码部分是 259 个 TS/TSX 文件），无行为改动。
- 验证：`format:check` 干净、`lint` / `tsc -b` / `vitest`（84 文件 480 项）/ `build` 全绿；并用与 C01 相同的方法比较“格式化前 vs 格式化后”两个构建：**数据未变的页面全部一致**；出现差异的页面（总览的实时数字、连接页刚从健康清扫更新过的徽标）在**未格式化的旧构建上以完全相同的方式**偏离其早先基线（`channels-classic` 元素数 317→323 在两侧都发生），因此差异来自数据漂移而非渲染变化。
- 说明：`MemberLogsView.test.tsx` 需要 Prettier 跑两遍才稳定（首遍产物非幂等），已在提交前跑第二遍并复查 `format:check` 通过。

### 14.3 A02 —— 巨页拆分提案（按你的选择，先给方案）

`PAGE_SPLIT_PROPOSAL_2026-10-06.md`：把 `Models.tsx`(2,492) 与 `Channels.tsx`(2,040) 按**状态所有权**各拆成 4 个组件 + 1 个共享选中逻辑模块，明确了「一个查询只有一个所有者」、URL 仍是选中状态真相、行为零变化、每组件 ≤700 行等约束，并列出需要你拍板的三点（文件划分与命名、tab 持久化与批量选择是否一起下沉、是否保留 `ModelCatalog`/`Channels` 导出名）。**未动任何代码，等确认。**

### 14.4 提交

本轮把工作区整理成三个提交（此前都是未提交状态，Prettier 必须是独立提交，所以先落前两个）：

| 提交 | 内容 |
| --- | --- |
| `ecb49fc` | `fix: remediate the 2026-10-06 audit findings across gateway and console` —— 审计修复主体（含上一节的全部改动、`internal/webui/dist` 重建、`docs/reference` 更新） |
| `d650a2f` | `style: drop the CSS declarations that can never win the cascade` —— 452 条死声明 + 孤立组件规则 |
| `b804f54` | `style(web): adopt Prettier and format the console source` —— 配置、scripts、CI 门禁与全量格式化 |

仍未提交（有意）：`PROJECT_AUDIT_2026-10-06.md`、`CODE_HEALTH_AUDIT_2026-10-06.md`、`PAGE_SPLIT_PROPOSAL_2026-10-06.md` 三份工作文档（仓库没有审计文档目录约定，是否纳入由你决定）；`.playwright-mcp/visual/{before,after}` 是本次 CSS 验收截图（该目录已被 gitignore）。

### 14.5 本节仍需你决定/知晓

1. **A02 巨页拆分**：提案已就绪，等你确认后实施（预计两页各一次独立提交）。
2. **同名 `@keyframes` 三份定义**（`page-enter`、`dialog-in`）：后一份整体替换前一份，前两份是死代码；删除它们**理论上**中性（浏览器都是“后者胜”），但改的是动画，我没有把它混进本轮提交，建议单独确认后再动。
3. **CSS 近似覆盖**（如 `.routing-signal.is-on` 的 32% vs 30%）：属需要视觉决策的静默覆盖，已列出未改。
4. **303 个“CSS 有、TSX 无”的类名**：其中相当一部分是 `driver.js` 运行时注入或模板拼接，需要逐项人工判断，不适合脚本清理。
5. **`-race`**：本机无 C 编译器，仍未运行，只能由 CI 覆盖。

## 十五、动画优化（2026-10-06 收尾）

### 15.1 已做：删除永不运行的 `@keyframes`（提交 `4a21a26`）

两类，都是**可证明**不会运行的定义：

1. **名字无人引用**：58 个名字在整个前端（CSS + TS/TSX + index.html）没有任何 `animation` / `animation-name` 引用。前提先验证过：全仓 TS/TSX **没有**任何关键帧名引用（动效一律用 CSS 类驱动，canvas 动画走 JS），也没有用 `var()` 拼动画名的写法（否则脚本会漏判）。
2. **同名被后者整体替换**：`page-enter`(3 份)、`strip-in`(2 份)、`dialog-in`(3 份，且无人引用)、`classic-page-enter`(3 份)、`classic-strip-in`(2 份)、`classic-dialog-in`(3 份)。同一名字的 `@keyframes` **不叠加**，最后一份整体替换前面的，所以只有最后一份可能生效。

结果：**68 个块、614 行**（`styles.css` −344、经典主题 −270），剩余 116 个 `@keyframes`。顺带解决了唯一一个「动画 `width`（触发布局）却从未运行」的 `tag-typewriter`。

**三重验证**（不是只跑脚本）：
- 独立扫描：对每个待删名字，检查是否存在任何 **非 `@keyframes <name>` 行**的引用（含注释、内联样式、HTML）——命中的只有 `page-enter`/`strip-in`/`classic-*` 这 5 个“被替换但仍在用”的名字，正好对应“只删重复定义、保留胜者”的分支；
- 反例抽查：`stat-rise` 在 HEAD 上**只出现于它自己的定义里**，`.stat-card:nth-child(n)` 的 `animation-delay` 是被删动画留下的残留（未动，见下）；
- 运行期对照：用 `document.getAnimations()` 采集**改动前后**两个构建在 10 种「页面 × 主题」组合下真正在跑的动画集合——**58 个被完全删除的名字从未出现在任何一次运行集合里**；5 个“保留胜者”的动画（如 `classic-page-enter`、`classic-dialog-in`）改动后照旧运行。集合差异只出现在**瞬时状态**上（对话框刚打开/关闭的过渡、焦点 outline 过渡），`missing` 里没有任何被删动画名。

### 15.2 核查后**不改**的项（避免无效改动）

- **`prefers-reduced-motion` 已经完整**：`styles.css` 末尾有一个全局兜底块（`*` + `!important`、`animation-duration: 0.01ms`），经典主题也有一份 scoped 版本。我第一次 grep 时被 `-First 20` 截断而误判「modern 没有覆盖」，读到源码后更正；实测在 `prefers-reduced-motion: reduce` 下 `/console/models` 运行中的动画数 = **0**。原计划的“补兜底”因此取消。
- **残留 `animation-delay`**（如 `.stat-card:nth-child(n)`，共 62 处“所在选择器自身不含 animation 声明”的动画属性声明）：**不删**。这些选择器所在的**元素**可能由另一条规则（基类）赋予动画，例如 `.login-tile.is-back { animation-delay: … }` 的动画来自 `.login-tile`；这种跨规则匹配无法用脚本判定，删错会破坏入场错峰。只把 `.stat-card` 一类确认为残留的记在这里，不做批量清理。
- **隐藏页面暂停**：JS 的 canvas 动画（`DashboardAura`、`KatanaCanvas`）已经监听 `visibilitychange` 并停掉 rAF；CSS 动画未额外暂停——隐藏标签页本身不会被合成器出帧，收益有限，故不加全局 `animation-play-state: paused`（它会引入“回到前台后补播”的新行为）。
- **常驻 `box-shadow` 动画**（真正剩下的每帧重绘成本）：`badge-live` / `led-breathe` / `led-pulse` / `pulse-health` / `pulse-warn` 及其经典主题孪生，动画的是 `box-shadow`（非合成器属性，只能重绘），而且是 `infinite`。**改成别的属性就会改变光晕观感**（`opacity`/`filter` 的观感不同），属需要你看一眼的设计决策，因此未动。它的成本随页面上的徽标数量线性增长（本次单渠道实例实测仅 1–2 个在跑；渠道多时连接页会成比例上升）。若要优化，可把光晕挪到伪元素上做 `opacity`+`scale`——但那要重新对一遍视觉。
- **引用了但未定义的动画**（`plane-code-scroll-x` / `plane-code-scroll-y`，位于 `.impact-code-*`）：那些类名已随组件删除，整条规则是死代码，动画本就无从运行，属 C01 的“孤立组件规则”范畴，未在本轮处理。
