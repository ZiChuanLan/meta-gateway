# 提案：前端巨页按状态所有权拆分（A02 的深水区）

> 状态：**待你确认后实施**。本文件只描述方案，不动任何代码。
> 范围：`web/src/features/Models.tsx`（2,492 行，单个 `ModelCatalog` 组件 ≈2,360 行）与
> `web/src/features/Channels.tsx`（2,040 行，单个 `Channels` 组件 ≈1,975 行）。
> 目标（来自 CODE_HEALTH_AUDIT_2026-10-06 A02）：按**状态所有权**拆成多个组件，让「谁拥有这份状态、
> 谁负责重新取数」在文件结构上可见，而不是靠通读一个两千行的函数。

## 0. 为什么这两页不能靠「搬代码」解决

两页都是**一个巨型组件**：全部 `useState` / `useQuery` / 全部 handler / 全部 JSX 都在一个函数里，
子块之间通过闭包互相读写（例如渠道列表的选中行决定了右侧详情抽屉的查询键，而抽屉里的保存又要回写列表的
失效键）。所以拆分的单位不是「文件」，而是**状态簇**：

```
状态 + 它的取数(query) + 它的写入(mutation) + 只依赖它的 UI  =  一个组件
```

跨簇的只有两类东西：**选中项（哪些 id 被选中）**与**失效信号（保存后要刷新谁）**。
这两样用 props 显式传递，绝不再用闭包。

## 1. Models.tsx 拆分提案

现状：`ModelCatalog` 一个函数，内含 4 组彼此独立的状态：

| 状态簇 | 现有状态 | 现在的消费者 |
| :--- | :--- | :--- |
| A. 路由目录 | 路由总览查询、搜索/过滤/分页/分组 tab、批量选择 | 左侧列表 + 顶部工具条 |
| B. 路由详情 | 选中路由、成员列表查询、成员排序/分组编辑、价格与映射编辑 | 右侧详情面板 |
| C. 模型目录与工具 | 模型元数据、能力注册表入口、探测入口、站点探针入口、缺失模型提示 | 「模型工具」弹层与提示条 |
| D. 页面外壳 | tab 持久化（`readTabState`）、批量模式、页面动作（`PageActions`） | 页头 |

提案（4 个文件 + 1 个共享 hooks 文件）：

1. `features/models/ModelsWorkspace.tsx`（新，≈250 行）：只做布局与协调——拥有**选中路由 id**、
   **批量选择集合**、tab 持久化；把 A/B/C 渲染进来；通过 callback 把「保存成功」转成 react-query 的
   `invalidateQueries`。
2. `features/models/RouteDirectory.tsx`（新，≈600 行）：A 簇。自己持有搜索/过滤/分页/分组状态与
   路由总览查询；对外只暴露 `selectedRouteId` + `onSelect`。
3. `features/models/RouteDetailPanel.tsx`（新，≈700 行）：B 簇。自己持有成员查询、排序草稿、价格草稿；
   对外暴露 `routeId` + `onMutated`（用于让 A 簇刷新计数）。现有的 `MemberDialog`、`RouteDialog`、
   `PriceFields` 已经独立，直接复用。
4. `features/models/ModelTools.tsx`（新，≈200 行）：C 簇的入口与提示条，内部挂现有的
   `CapabilityRegistry` / `ProbeDialog` / `SiteProbeDialog`。
5. `features/models/useModelsSelection.ts`（新，≈80 行）：选中/批量选择的纯逻辑（可单测），
   两页概念相同，`Channels` 也复用。

## 2. Channels.tsx 拆分提案

现状：`Channels` 一个函数，含 5 组状态：

| 状态簇 | 现有状态 | 备注 |
| :--- | :--- | :--- |
| A. 连接目录 | 频道总览查询、健康过滤、分组 tab、搜索、批量选择 | 左侧 |
| B. 连接详情 | 选中频道、凭据（用户/relay）、账号与额度查询、编辑入口 | 右侧 |
| C. 新建/导入 | 添加抽屉、粘贴端点拆分、校验失败重试 | `EditChannelDialog` 已有独立文件 |
| D. 模型抽屉 | 频道模型列表（`ChannelModels` 已有独立页） | 只保留入口 |
| E. 页面外壳 | tab 持久化、批量动作（刷新/检测/删除）、阶段提示 `stageMessage` | 页头 |

提案：

1. `features/channels/ChannelsWorkspace.tsx`（新，≈250 行）：布局 + 选中 id + 批量动作的结果汇总；
   复用 `useModelsSelection`（改名 `useListSelection`，放 `web/src/lib/`）。
2. `features/channels/ChannelDirectory.tsx`（新，≈650 行）：A 簇（含健康过滤与分组 tab）。
3. `features/channels/ChannelDetailPanel.tsx`（新，≈450 行）：B 簇（凭据、账号、额度、操作菜单）。
   现有的 `connectionActions`（约 260 行）随之落在这里，或进一步拆成 `channelActions.tsx`。
4. `features/channels/ChannelCreateDrawer.tsx`（新，≈300 行）：C 簇。端点拆分/校验重试逻辑随之搬走，
   并**新增单测**（目前这段逻辑只被 E2E 覆盖）。

## 3. 共同约定（拆分后必须成立）

- **一个查询只有一个所有者**：`invalidateQueries` 只能出现在拥有该 query 的组件里；跨组件用
  `onMutated` 回调，不直接调用别人的 key。
- **URL 是选中状态的真相**：`?route=` / `?id=` 继续由 workspace 读写（现在是这行为，保持不变）。
- **行为必须零变化**：所有现有 `*.test.tsx` 不改断言（必要时只改导入路径与查询方式）。
  拆分**不**顺手改文案、不改排序、不改默认 tab。
- **每个新组件不超过 ~700 行**；超过说明状态簇没切干净。
- 拆完跑：`npm run lint && npx tsc -b && npx vitest run && npm run build`，
  并用运行中的控制台对 Models / Channels 两页做**现代 + 经典 + 手机**三个视角的目视核对
  （与本次 CSS 清理相同的截图流程）。

## 4. 顺序与验收

1. 先拆 `Channels.tsx`（有 `EditChannelDialog` 现成边界，风险最低）；
2. 再拆 `Models.tsx`（B 簇与 A 簇耦合最深，放第二步）；
3. 每步独立提交，提交信息写清「struct: split X into state owners」；
4. 每步完成后附：文件行数对比、测试结果、三视角截图。

## 5. 需要你拍板的点

- 是否接受上面的**文件划分与命名**（`*Workspace` / `*Directory` / `*DetailPanel` / `ModelTools`）；
- 是否接受把 `Models` 的 tab 持久化与批量选择一起下沉到 `Workspace`（当前它们在 `ModelCatalog` 内部）；
- 是否需要保留 `ModelCatalog` / `Channels` 这两个**导出名**（外部只从 `App.tsx` 路由引用，可安全改名；
  若你希望 diff 更小，我可以保留同名薄壳再逐步迁移）。
