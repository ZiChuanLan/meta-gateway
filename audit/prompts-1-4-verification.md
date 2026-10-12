# 四条任务单的实测记录（提示词 1–4）

分支 `experiment/availability`。全部在隔离验收栈（`docker compose -p mg-audit -f docker-compose.audit.yml`，
控制台 `http://127.0.0.1:4318/console`，mock 上游 `:4319`）里实测，截图见 `audit/evidence/`。
本地未推送任何提交。

| 任务 | 提交 |
| --- | --- |
| 1 创建连接「勾选即接入」 | `1eb7395` |
| 2 观测页两栏固定高度 + 3 采集反馈 | `25790c9` |
| 4 计费面板单层化 | `eff216e` |
| mock 验收数据 | `28cba7e` |
| 重新构建 `internal/webui/dist` | `5187f6c` |

## 1 创建连接时接入勾选的模型

- 实现：`internal/httpapi/admin_connections.go` 的 `adoptChannelModels`，在 `createConnection` 里于渠道建好后运行；
  manual 模式且 `models_csv` 非空才走，逐模型 find-or-create 路由（`Route.GetByModelAny` / `Create`）+ 建启用成员
  （`RouteMember.Create`，auto=1、manual_override=0，与前端 `adoptModel` 一致）。单模型失败进 `adopt_errors`，
  不影响整次创建；auto 模式完全不碰。
- 回归：`TestConnectionCreateAdoptsCheckedModels`（3 条路由 + 3 条成员 + 概览 `model_count=3`；同请求再跑一次
  仍是 3 条路由、新渠道各自绑定）、`TestConnectionCreateDoesNotAdoptInAutoSyncMode`。
- 浏览器：新建连接「接入验证」勾选 `audit-bulk-001..003` → 列表显示 模型 126 / **已选 3**；模型页出现这 3 个模型，
  成员指向「接入验证」。证据 `audit-t1-adopted-connection.png`。
- 反例对照：同页 `mock-upstream:4319`（修复前用同一对话框创建）仍是 已选 0。

## 2 观测页左右两栏固定高度

- `web/src/styles/availability.css`：`.observation-layout` 高度 `calc(100dvh - 475px)`（338 标题/工具栏/节奏行 +
  137 页脚与内边距，1440 宽实测），`min-height: 430px`；两栏 `min-height: 0` + 各自 `overflow-y: auto`；
  站点筛选与表头 `position: sticky`；`<=760px` 恢复单栏整页滚动。
- 实测（站点 Elysiver，**214** 条采集记录）：

| 视口 | 面板高 | 页面滚动 | 左栏 可见/内容 | 右栏 可见/内容 | 表头 |
| --- | --- | --- | --- | --- | --- |
| 1440×900 | 430 | 6px | 429 / 2003 | 420 / 14017 | 停在面板顶 +20px |
| 1440×1080 | 605 | 1px | 605 / 2003 | 595 / 14017 | 同上 |
| 1920×1080 | 605 | 1px | 605 / 2003 | 595 / 14017 | 同上 |
| 390×844（手机） | 内容高 | 20210（整页） | 横排 117，无内滚动 | 无内滚动 | `static` |

- 两套主题数值一致（`.theme-modern` 只在别处覆盖）。
- 证据：`audit-t2-final-1440x900.png`、`audit-t2-final-1440x1080.png`、`audit-t2-final-1920x1080.png`、
  `audit-t2-mobile-modern.png`、`audit-t2-desktop-modern.png`、`audit-t2-desktop-classic.png`。

## 3 采集/导入的反馈与「为什么是 0」

- 前端（`SiteObservationWorkspace.tsx`）：立即采集与导入按钮进行中显示「采集中… / 导入中…」；
  结果走 toast（`collectSite` / `collectSiteEmpty` / `collectSiteFailed` / `collectRound`）；
  失败横幅可重试（重复同一范围）；导入先弹说明对话框（只写已有站点、不创建、随后采集一轮），
  完成后一行给出 更新/跳过/未创建/采集成功/失败 计数。
- 0 条时按状态说出原因：采集关闭 / 上次失败（附上游原文）/ 从未采集 / 采集成功但清单为空 /
  上次采到 N 条但不在当前窗口 / 筛选无匹配（不再显示成「没有模型记录」）。
- 实测五种情况（站点「本地模拟」，来源指向本地 mock）：

| 情况 | 来源 | 结果文本 |
| --- | --- | --- |
| 正常有数据 | `mock-upstream:4319` | toast「『本地模拟』采集到 126 个模型」；摘要「本次成功采集到 126 个模型」 |
| 上游返回空 | `…:4319/empty` | toast「采集成功，但站点没有返回模型记录」；原因行「上次采集成功，但站点公开的清单里没有模型记录。」 |
| 上游不可达 | `…:4399` | toast＋横幅「Get \"http://mock-upstream:4399/api/pricing\": … connection refused」 |
| 解析失败 | `…:4319/invalid` | toast＋横幅「parse pricing: model discovery failed: invalid_payload」 |
| 来源未开启 | 定时采集关闭且 0 条 | 原因行「这个站点的『定时采集』是关的，打开后才会采集。」 |

- 导入公开目录（对真实目录执行一次）：`已更新 12 个站点 · 跳过 0 个（已有自定义来源）· 目录中 33 个站点未创建
  · 随后采集成功 9 个、失败 3 个`。没有改动采集的判定逻辑（空表仍按既有行为算失败，只把原因说出来）。
- 证据：`audit-t3-has-data.png`、`audit-t3-empty-catalogue.png`、`audit-t3-unreachable.png`、
  `audit-t3-source-off.png`、`audit-t3-import-confirm.png`、`audit-t3-import-result.png`。

## 4 计费面板：一个价 + 一个改价入口 + 一行来源

- 默认视图（模型层与成员层相同）：「现在收费：$1.50 / 1M 输入 · $3.00 / 1M 输出」（未定价时显示「尚未定价」、
  免费规则显示「免费」），下一行才是规则用语与生效范围；`改价`（收起）与`这些价从哪来`（收起，一行摘要
  「站点标价 · 2.5 站点额度 /1M · 14 分钟前 · 另有 N 条」）。
- 展开来源仍是每行「什么价 / 什么时候抄的 / 设为收费价」，并给「这条就是当前收费价」标记（规则 `source=quote_copy`
  且 `quote_id` 命中）；复制仍带 `quote_id + expected_revision`。
- 改价路径实测：打开 `改价` → 选「自定义价格」→ 填 1.5 / 3 → 保存 → 读数变为
  「现在收费：$1.50 / 1M 输入 · $3.00 / 1M 输出」，版本 1。
- 顺带修掉成员层生效范围里的空引用：「只影响「」这一条连接」→「只影响「本地模拟 · 默认组」这一条连接」。
- 未解析的计费表达式、阶梯/时段编辑、计费语义均未改动。
- 证据：`audit-t4-model-default.png`、`audit-t4-model-expanded.png`、`audit-t4-model-saved.png`、
  `audit-t4-member-default.png`。

## 门禁

- Web：`lint`、`format:check`、`audit:entrypoints`（237 文件）、`typecheck`、`vitest run`（98 文件 / 596 测试）、
  `build` 全过（新增 8 个组件测试：观测页 6、计费面板 2）。
- Go：`gofmt -l .` 无输出、`go build ./...`、`go vet ./...`、`go test ./... -count=1`（40 包 ok，exit 0）。
- 验收栈：`docker compose -p mg-audit … up -d --build --wait` 健康。

## 说明

- 工作区里另有一份**别的会话**未提交的改动（`web/src/features/models/{RouteDirectory,ModelDirectoryTable}.tsx`，
  去掉模型目录行的可用性指标）。本轮没有触碰、没有提交；`5187f6c` 的 dist 是在把那两个文件暂存出去后构建的，
  因此提交里的产物对应已提交的源码。

---

# UI 打磨一轮（2026-10-13 00:0x，操作员截图反馈）

三张截图的两个诉求：**横条没填满，内容从边上漏出来**；**计费来源区没有层次**。提交 `cc846c0`（源码）+ `8dd491f`（dist）。

## 1 连接卡的动作条：12px 的漏缝（截图 2 的真因）

`web/src/themes/classic/compat.css` 里经典包把共享规则覆盖成了 `bottom: 0; padding-bottom: 4px`，
而共享规则（`web/src/styles.css` 的 `.ops-detail-card.is-compact > .detail-primary-bar`）本身是
`bottom: -12px; padding-bottom: 12px` —— 负偏移正是用来**抵消卡片自身 padding-bottom: 12px** 的。
被覆盖之后，动作条的填充比卡片内边界早结束 12px，滚动到折叠处的内容（截图中那行
「100.78 53/53 次探测成功」）就在按钮下方露出半行。

- 经典包两处（连接卡、模型卡）改为保留 `position: sticky; margin-bottom: 0; background`，
  不再覆盖 `bottom` / `padding-bottom`，交给共享规则；
- 共享规则加 `box-shadow: 0 -10px 14px -14px rgba(15,42,66,.35)`，让「内容从下面滚过去」变成可见的层次。
- 实测：`cardBottom=893 / barBottom=892`（原来相差 13px），条内点位上命中的元素就是动作条本身。

## 2 观测页的两条粘性带（截图 1）

- 站点筛选行：白底 + 底部发丝线 + 向下阴影 + `padding: 8px 0 10px`（原来只有 4px 底衬、无边界）；
- 表格表头：`--surface-subtle` 底色 + 发丝线 + 向下阴影，成为真正的表头带。
- 两套主题都验证（经典 #eef3f7 表头 / 现代 #f6f7f9 表头）。

## 3 计费来源区（截图 3）

原来是一条平坦列表：来源名、时间、数值同字号同字重，值之间只有一根分隔线，按钮孤零零跟在后面。

现在每条来源是一张卡：
- 头部：来源名（13.5px/600）+ 单位胶囊；当前生效的那条挂 `is-current` 胶囊（主色）；
- 出处降为脚注：`site_public · 站点分组 default · 2026/10/12 23:27:38 抄到的 · 站点显示 CNY`；
- 数值区：标签 11px 灰、数值 15px/600 tabular，`repeat(auto-fit, minmax(104px,1fr))` 自适应列；
- 计价表达式不可解析时用 `--surface-subtle` 提示带 + 原文 code；
- 动作独立成行（`设为收费价`，右对齐，上方发丝线；没有 target 时不渲染这一行）。
- 顺带把孤零零的「模型倍率」按钮挪到「现在收费」那一行的右端（`margin-left: auto`），
  不再单独占一行；打开倍率弹窗的功能在浏览器里复测通过。

## 证据

`audit/evidence/ui-before-*.png` / `ui-after-*.png`：观测页筛选带、表头、连接动作条、计费来源区（经典 + 现代）。
门禁：web 六项（lint / format:check / audit:entrypoints / tsc / 596 测试 / build）全过。

---

# 第二轮：全部覆盖 + 模型卡密度（2026-10-13 01:2x，操作员第二次反馈）

操作员否掉了「让内容滚到条下面这件事有可见的层次」这个做法（阴影），要求"全部都覆盖住"，
并指出模型详情页信息密度过高。提交 `74a25fb`（源码）+ `e92c00a`（dist）。

## 1 覆盖：不重叠，而不是把重叠画得好看

- **左栏筛选行**：原来是 sticky 筛选行 + 下方列表一起滚（列表行必然在筛选行下沿被切一半）。
  现在 `.observation-sites` 是 `flex column + overflow: hidden`，筛选行 `flex: 0 0 auto` 静态占位，
  `.observation-site-list` 自己 `overflow-y: auto` —— 列表视口从筛选行下面开始，**永远不会有行跑到条下面**。
  实测 `list.scrollTop=500 / scrollHeight=1938`，筛选行不再是 sticky。
- **全部阴影删除**：`.observation-site-filters`、`.observation-model.is-header`、
  `.ops-detail-card.is-compact > .detail-primary-bar` 三处 `box-shadow` 去掉（那是"可见层次"的来源）。
  条是"不透明底色 + 发丝线"，不靠投影解释内容从下面经过。
- 连接卡动作条：填充仍覆盖到卡片边界（gap = 1px），无阴影（实测 `barShadow: none`、`barBg = cardBg`）。
- 移动端（390px）：站点栏仍是横向 chips 滚动条，无横向溢出（`docScrollWidth 380 ≤ 390`）。

## 2 模型详情页：五排 chrome 变三排

同一张卡：**524px → 403px**（1 个成员）、**同一 524px 高度下可见成员 1 个 → 4 个**。

| 原来 | 现在 |
| --- | --- |
| head（模型名 + 优先成员）56+14 | 同，但右侧多了可用性 chip；`分组 default` 进 meta 行 |
| 动作条 80px（含"收费设置"上下各 12px 外边距） | 动作条 56px |
| **`分组 default` + `—` 独占一行 50px** | 删除，chip 进 head |
| 路由成员标题 24 + 上 25/下 14 | 24 + 上 16/下 10 |
| `.models-advanced` 上 8 + 内 12 + 按钮行 38 + 下 10 | 0 + 0（标题即分区标记） |
| 分组 tabs 单独一行 36 + 下 10 | 与按钮合为一条工具栏（`.member-group-toolbar`） |
| `一键挂载提供此模型的渠道`（210px，撑到第三行） | `一键挂载`（+ 原文进 tooltip） |
| 策略折叠 43 + 上下各 15 | 39 + 上下各 12 |

功能一个没删：可用性 chip、分组 tab、添加通道、一键挂载、批量选择、路由模式、收费设置、试调、
策略折叠都还在卡上，只是位置更省。经典与现代两套主题都实测过。

## 证据

`audit/evidence/ui-after-model-card-1member.png`、`ui-after-model-card-4members.png`、
`ui-after-obs-rail-split.png`、`ui-after-obs-mobile.png`、`ui-after-conn-card-bottom.png`、
`ui-after-model-modern.png`。门禁：web 六项全过（596 测试）。
