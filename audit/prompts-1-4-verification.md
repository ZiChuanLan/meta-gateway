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
