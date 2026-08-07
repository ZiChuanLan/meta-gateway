# Meta Gateway 优化与借鉴清单

> 基于对 `meta-gateway` 全量代码审计，对比 new-api / axonhub / sub2api / metapi / all-api-hub 五个同类项目。
> 每条标注：【优化】= 自身病灶修复；【借鉴】= 从其他项目引入的模式；【功能】= 新增能力。
> 状态列：`未开始 / 进行中 / 已完成 / 暂缓`。

---

## 一、UI / 前端

### 设计系统（根源问题：6132 行手写 CSS、无标尺）

| # | 事项 | 类型 | 来源/依据 | 状态 |
| --- | --- | --- | --- | --- |
| UI-1 | 引入 **Tailwind 4 + shadcn/ui + CVA + cn()**，`components.json` 驱动生成 button/dialog/table/form，替代手写 CSS | 借鉴 | all-api-hub `components.json`、`src/styles/style.css`（同为 React 19 + react-query + lucide） | 进行中（依赖已装） |
| UI-2 | 建设计 token：间距 `--space-1..8`、字号 `--text-xs..xl`、z-index 标尺；消除 **457 处手写 px**、**20 种 font-size** | 优化 | `styles.css` 审计 | 未开始 |
| UI-3 | 暗色模式改为 **CSS 变量切换**（`@custom-variant dark`），而非逐组件 `dark:` 类 | 借鉴 | all-api-hub `style.css` | 未开始 |
| UI-4 | 登录页/过渡特效 CSS（约占 35%，~1100 行 + 38 个 keyframes）**独立文件懒加载**，不再拖慢主包 | 优化 | `styles.css:2378-3397`、`3429+` | 未开始 |
| UI-5 | 清除 **17 个 `!important`**（修特异性而非打补丁）；删除 3 次重复定义的 `.stat-card`、2 次 `.member-row` 等 | 优化 | `styles.css:1075/1648/2085/2182/5559`、`571/1740/4570` | 未开始 |
| UI-6 | 统一主题：应用壳是暖纸墨色系（radius 2px），登录页却局部覆盖成钴蓝品牌色——二选一，语义 token 不被局部覆盖 | 优化 | `styles.css:2-31` vs `2420-2428` | 未开始 |

### 组件与页面结构

| # | 事项 | 类型 | 来源/依据 | 状态 |
| --- | --- | --- | --- | --- |
| UI-7 | 拆 **`Channels.tsx`（2651 行、30+ hooks）** → `features/Channels/{components,hooks,utils}`；把"创建+验证"流程收敛为 `useCreateConnection` hook（消除渲染期写 ref 的 `runVerifyRef.current = ...`） | 借鉴 | all-api-hub feature-folder 模式；`Channels.tsx:225-1633/298` | 未开始 |
| UI-8 | 做**一个配置驱动的响应式 DataTable**：列配置 + 每列渲染 slot，桌面表格/移动卡片同源，内置骨架/空态/选择/行操作 | 借鉴 | sub2api `components/common/DataTable.vue`（移植 React） | 未开始 |
| UI-9 | **`useTableLoader` hook**：分页 + 300ms 防抖搜索 + AbortController 取消竞态 + 记住页大小，包在现有 react-query 外 | 借鉴 | sub2api `composables/useTableLoader.ts`、`usePersistedPageSize.ts` | 未开始 |
| UI-10 | 合并 4 份近乎相同的失效 key 常量（`INVALIDATE`/`ROUTING_INVALIDATE_KEYS`/`DISCOVERY_INVALIDATE_KEYS`/`STORE_INVALIDATE`）为单一 `QUERY_FAMILIES` | 优化 | `Channels.tsx:65-73`、`Models.tsx:47-54`、`OpsPanels.tsx:39-47`、`Store.tsx:21` | 未开始 |
| UI-11 | **Drawer 与 Dialog 二选一**（按实体-操作类别定规则）；当前 Channels 同时用 5 个 Drawer + 13 个 Dialog，交互模型不一致 | 优化 | `Channels.tsx` 全文 | 未开始 |
| UI-12 | 移动端显式组件：`MobileCard/MobileDrawer/MobileBatchBar/ResponsiveFormGrid` + 单一 `useIsMobile`（matchMedia）；行内 hover 显示操作、触屏常显 | 借鉴 | metapi `src/web/components/`；all-api-hub `AccountListItem`（`group-hover` + `isTouchDevice`） | 未开始 |
| UI-13 | i18n 拆分：2254 行内联字典 → 按 namespace 分文件懒加载；**加 key 联合类型**（现在 `t("anything")` 编译通过、运行时静默渲染原始 key）；引入 `i18next-cli extract/sync` + lint-staged 校验 | 借鉴 | all-api-hub i18next 管线；sub2api `i18n/locales/en/*.ts` | 未开始 |
| UI-14 | 消灭硬编码英文串绕过 i18n（登录页装饰文案、`"Custom…"`、按英文 message 比较错误）；错误改为机器 `code` 而非英文句子 | 优化 | `App.tsx:314-362/174`、`Channels.tsx:81` | 未开始 |
| UI-15 | 清掉内联样式与魔法数（`style={{marginTop:4}}`、px/rem 混用、Drawer 宽度内联计算）改为类/变体 | 优化 | `Channels.tsx:2127/2609/2636`、`Drawer.tsx:58` | 未开始 |
| UI-16 | 路由切换不再每次 `requestAnimationFrame` 重挂载动画（与 15s 轮询叠加导致页面脉冲）；按 route key 播一次或移除 | 优化 | `App.tsx:543-548`、`Models.tsx:94/104` | 未开始 |
| UI-17 | `DataTable` 支持列对齐/吸顶表头/排序；`StatusBadge` 不再按原始值拼 class（新后端状态会无样式）；非交互统计卡别用 `<button disabled>` | 优化 | `ui.tsx:474-498/423`、`StatGrid.tsx:61-73` | 未开始 |
| UI-18 | 空态文案给出"下一步操作指引"而非泛泛 "Nothing here yet." | 优化 | `i18n.tsx:36` | 未开始 |

### 危险操作 UX（admin 台安全）

| # | 事项 | 类型 | 来源/依据 | 状态 |
| --- | --- | --- | --- | --- |
| UI-19 | 危险操作三件套：**输入确认句 + 倒计时 + 重新认证**（如删除站点/重置）；按钮加 `tone: neutral/primary/danger/warning` 类型化 | 借鉴 | metapi `pages/Settings.tsx`（确认常量/倒计时/重认证） | 未开始 |
| UI-20 | 时间类输入按"值 + 单位"编辑（秒/分/时/天），存储统一为秒 | 借鉴 | metapi `Settings.tsx` 的 `*_UNIT_OPTIONS` 模式 | 未开始 |

---

## 二、后端

### Handler 与路由（根源问题：1463 行 admin.go 装 40 个 handler）

| # | 事项 | 类型 | 来源/依据 | 状态 |
| --- | --- | --- | --- | --- |
| BE-1 | `httpapi/admin.go` 按资源拆 `handler/admin/`，一个 struct 一个文件（sites/credentials/channels/routes/members/keys/usage/groups/logs），聚合进 `Handlers` 树 | 借鉴 | sub2api `internal/handler/handler.go`（~35 个小 struct） | 未开始 |
| BE-2 | 路由按面拆文件：`SetAdminRouter/SetRelayRouter/SetWebRouter` 各自独立，注册只声明策略 | 借鉴 | new-api `router/main.go`（35 行调度） | 未开始 |
| BE-3 | **新增 `POST /admin/connections` 编排端点**：一次完成 site（按规范化 URL 去重）→ credential（加密）→ channel，失败回滚已建部分；替代前端 3 连调无回滚 | 优化 | 前端 `Channels.tsx:348-391` 现状 | 进行中（测试已写） |
| BE-4 | 渠道选择/分发移入 middleware + `Ability(group, model, channel, priority, weight)` 反规范化表（带优先级重试阶梯），handler 不再内嵌选路 | 借鉴 | new-api `middleware/distributor.go`、`model/ability.go` | 未开始 |

### Proxy 管道（根源问题：1443 行 proxy.go 装 48 个方法）

| # | 事项 | 类型 | 来源/依据 | 状态 |
| --- | --- | --- | --- | --- |
| BE-5 | 按管道阶段拆 proxy：选择/重试/熔断/计费/密钥轮换/流式探活/系统提示注入各自成模块；`Retryable`（跨渠道）/`ChannelRetryable`（同渠道）声明式重试 | 借鉴 | axonhub `llm/pipeline/pipeline.go`、`middleware.go` | 未开始 |
| BE-6 | **Adaptor 接口 + 每 provider 子包 + `RelayInfo` 请求上下文对象**：适配器只接 `RelayInfo` 值对象，不摸 gin.Context/DB model | 借鉴 | new-api `relay/channel/adapter.go`、`relay/common/relay_info.go` | 未开始 |
| BE-7 | 计费/日志/配额/覆盖改为**管道装饰器**（10 个钩子点：OnInboundRequest/OnOutboundRawRequest/…），而非写死在 proxy 主体 | 借鉴 | axonhub `llm/pipeline/middleware.go` | 未开始 |
| BE-8 | 统一流抽象 `Stream[T]{Next/Current/Err/Close}`，原始 SSE 与统一事件共用迭代契约，中间件可泛型包装 | 借鉴 | axonhub `llm/streams/stream.go` | 未开始 |

### 数据层与错误契约

| # | 事项 | 类型 | 来源/依据 | 状态 |
| --- | --- | --- | --- | --- |
| BE-9 | **消费侧定义 Repository 接口**：`service` 包声明 `AccountRepository` 等，`store` 包实现并在构造函数返回接口 → 可替换/可 mock | 借鉴 | sub2api `service/account_service.go:50` + `repository/account_repo.go:76` | 未开始 |
| BE-10 | 统一错误契约 `ApplicationError{Code, Reason, Message, Metadata}`，store→service→handler 一路透传；前端按 `code` 匹配而非英文 message | 借鉴 | sub2api `internal/pkg/errors/errors.go` | 未开始 |
| BE-11 | 渠道健康/能力枚举（`channelHealth`/`capabilityFlags`）由**后端返回**，删除前端按 8 个字段重推导的逻辑（注释里写 "Match backend pickUserCredential"，极易静默发散） | 优化 | `Channels.tsx:152-223/737-763` | 未开始 |
| BE-12 | DTO 映射与密钥脱敏集中在 handler 边界的 mapper，不散落各 handler（已有 `safeCred` 局部 struct 的雏形，推广成统一模式） | 借鉴 | sub2api `internal/handler/dto/mappers.go`、`credentials_redact.go` | 未开始 |

### 装配（可选、改动大）

| # | 事项 | 类型 | 来源/依据 | 状态 |
| --- | --- | --- | --- | --- |
| BE-13 | 用 **fx 或 Wire** 做 DI：每包 `Module`、命名中间件类型、聚合 `Handlers struct`，消灭手工串联 | 借鉴 | axonhub `internal/server/api/fx_module.go`；sub2api `cmd/server/wire_gen.go` | 暂缓 |

---

## 三、功能（可新增能力）

| # | 事项 | 类型 | 来源/依据 | 状态 |
| --- | --- | --- | --- | --- |
| FN-1 | **设置/导航命令面板**（cmdk）：每个设置项有唯一 target-id，DOM id + 搜索索引 + URL anchor 深链三处共用 | 借鉴 | all-api-hub `OptionsSearchDialog.tsx` | 未开始 |
| FN-2 | 仪表盘统计卡由纯函数 `buildStatusCards()` 生成（带 severity + 点击深链到对应页），逻辑可测试 | 借鉴 | all-api-hub `features/OptionsOverview/statusCards.ts` | 未开始 |
| FN-3 | 多租户分组配额/限流的 UI 呈现与后端 `groups` 打通（后端已有 `PUT/DELETE /admin/groups`，前端暂无可视化） | 优化 | `admin.go:99-101` | 未开始 |
| FN-4 | 模型可用性探测（冷却/熔断状态）在前端可视，配"输入确认句"后手动重置 | 借鉴 | metapi 模型探测 + 确认 UX | 未开始 |
| FN-5 | 架构守护测试：`*.architecture.test.ts` 强制分层规则（如 handler 不得直接 import store），防回潮 | 借鉴 | metapi 的 `*.architecture.test.ts` | 未开始 |

---

## 四、执行顺序建议

| 阶段 | 内容 | 对应条目 | 量级 |
| --- | --- | --- | --- |
| **P0 止血** | 连接编排端点 + 测试 | BE-3（联动 UI-7 的 hook） | 小 |
| **P1 设计系统** | Tailwind+shadcn 落地、token、登录页 CSS 隔离、清 `!important` | UI-1~6 | 中 |
| **P2 前端结构** | 拆 Channels、统一 DataTable + useTableLoader、合并失效常量、i18n 类型化 | UI-7~17 | 中大 |
| **P3 后端 handler 拆分** | admin.go 按资源拆 + 路由按面拆 | BE-1~2 | 中（机械） |
| **P4 proxy 管道化** | Adaptor/RelayInfo + 阶段拆分 + 装饰器 | BE-5~8 | 大 |
| **P5 数据层** | Repository 接口 + 统一错误契约 + 后端返回健康枚举 | BE-9~12 | 中大 |
| **P6 功能增量** | 命令面板、统计卡构建器、分组配额 UI、架构守护测试 | FN-1~5 | 按需 |

---

## 五、已确认暂不做的

- **不引入重型组件库**（AntD/MUI）：项目偏好自研 + lucide，shadcn/ui 的"生成即拥有"模式更契合。
- **暂不上 fx/Wire**（BE-13）：先手工传参把层级理顺，装配容器是后续可选优化。
- **不重写 store 为 ORM**：现有手写 SQL + 缓存（如 `SiteStore`）性能可控，先用接口抽象（BE-9）获得可测性，ORM 化收益不成正比。

---

### 进度记录

- 2026-08-07：初版清单建立；P0（BE-3）测试先行、P1（UI-1）依赖安装完成。
