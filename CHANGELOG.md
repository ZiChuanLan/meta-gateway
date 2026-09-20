# Changelog

All notable changes to Meta Gateway are documented here. Versions follow
[SemVer](https://semver.org/); each entry lands together with its git tag and
Docker image (`zichuanlan/meta-gateway:<version>`).

## [Unreleased]

### Fixed

- **CI 的 race 步骤不再靠运气**：`go test -race ./...` 吃的是 10 分钟**每包**默认超时，而 `-race`
  会给纯 Go 版 SQLite 插桩、每个开新库的测试都要重放全部 101 个迁移（实测 0.14s → 3.4s，25×），
  于是 `internal/store` 单包约 500s、`internal/httpapi` 约 700s —— 600s 的上限就压在悬崖边上
  （09-17 那次 httpapi 504.9s 通过，之后 store 599.8s 只差 0.2s 就红）。现在 race 步骤显式给到
  每包 20 分钟、作业预算 40 分钟；步骤再红时先看是不是 `DATA RACE`，超时不再是"这套测试跑不完"
  的常态解释。
- **测试不再泄漏路由器的后台调度器**：`NewWithDependencies` 会起 alert / balance / health sweep、
  alert rules、daily summary、model catalog、DB GC、probe、update check、recovery loop，各自注册
  停止回调；生产在关机路径统一 `StopBackground`，测试却从不调用，于是每个测试建过的 router 都把
  自己的调度器留到进程退出（实测 44 个泄漏 router ≈ 458 个常驻 goroutine，且随测试数线性增长，
  还持续对着已关闭的库打点）。新增 `NewTestRouter(t, cfg, db, enc)` 作为测试建 router 的唯一入口，
  测试结束自动 `StopBackground`；另加一个不变量测试（router 停掉后不得剩 goroutine）与 TestMain
  兜底（有停止回调没被 drain 直接判失败），避免这条约定再次腐化。注：这属于资源与关机语义问题，
  **不是** race 超时的成因（A/B 实测每测试耗时比值中位数 1.056，即噪声）。
- **冷却状态不再要切页面才刷新**：渠道列表的「降级 / N 个路由成员正在冷却」与模型页成员行的
  「冷却中」都是派生状态，却都停在最后一次取数上，得切页或刷新才看到变化。两处根因不同：
  渠道页那两个查询（`channel-overviews`、`route-overviews`）自己没带刷新间隔，只蹭外壳的 30s
  tick —— 于是"切走再回来"（观察者重挂载会补取一次）永远比停在原地快；模型页成员行虽然每 15s
  轮询，但 `candidateState` 是在渲染时读 `Date.now()` 比对 `cooldown_until`，冷却到期那一刻
  **没有任何 state 变化**，React 没有理由重渲，徽章、倒计时和「清除冷却 / 恢复成员」按钮就一直
  挂到下一次轮询才翻。现在渠道页自带 15s 间隔；并新增 `lib/cooldownClock.ts`：取当前屏上所有
  `cooldown_until` 中最早的那个到期时间排一个一次性定时器，到点强制重渲一次，让下一帧读到新的
  时钟 —— 刻意做成边沿触发，而不是把整个成员列表每秒重渲一遍（空闲时零开销）。两个方向都钉了
  回归：定时器到点必翻、未到点不重渲、连续多个冷却会依次接上；渠道页那条直接断言"不碰页面，
  15s 后徽章自己从 Degraded 变 Healthy"（把间隔去掉即红）。

## [v3.1.1] — 2026-09-20

### Added

- **粘性会话面板可折叠**：模型页那块「粘性会话」常驻在模型目录上方，活跃绑定一多就把目录
  顶到首屏之外。现在标题即折叠开关，默认收起成一条窄栏（实测 212px → 56px），栏上仍带
  `活跃绑定 / 命中` 两个读数，所以收起后照样能一眼看到会话状态；展开才铺开完整统计行与
  绑定明细表。折叠选择按标签页记住（`sessionStorage`），从模型页跳走再回来不会自己弹开。
  两套外观都适配，经典皮肤下折叠栏会去掉表头分隔线，避免一条孤零零的横线挂在标题下面。

### Changed

- **时间区间控件并入它所解释的那块面板表头**：总览页原来在首屏之上独占一整行的区间控制条，
  现在落在「流量总览」面板表头，紧贴它所解释的窗口读数；日志页的读数条（当前条数 / 失败数 /
  失败率 + 导出 CSV / 刷新）移到内容区最上方，先看数再看图。
- 时间区间控件的圆角改走 `--radius-sm`：经典外观得到直角（与相邻的胶囊、返回按钮、普通按钮
  一致），现代外观仍是自己的圆角控件语言 —— 此前硬编码的 9/7/8px 让它在经典下成了全页唯一
  一个圆角控件。

### Fixed

- 总览「流量总览」标题与「粒度」胶囊之前**零间距、看着像焊在一起**：`.cockpit-chart-title`
  从来没有样式（样式表里一直写的是已废弃的 `.dashboard-chart-title`），没有 `display: flex`
  时三个子节点走 inline 流，而 JSX 会吃掉元素之间的空白。
- **经典外观下所有 `.panel-header` 的同行元素视觉中心错位**：基础表声明 `center`，经典把它
  改成了 `flex-start`，而顶边对齐只在同行元素等高时才等于视觉对齐 —— 表头把 21px 的标题和
  38px 的区间控件摆在一起，中心最多差 8.5px（总览图表 7px），标题看着浮起、控件看着下沉。
- 模型页粘性面板的统计行此前没有任何样式，五个读数挤成一行纯文本；现在按「数字在前、
  标签退后」的读数样式排版。

## [v3.1.0] — 2026-09-17

### Added

- **精确时间区间（总览 + 日志）**：两页共用一个区间控件——预设 `15 分钟 / 1 小时 / 6 小时 /
  24 小时 / 7 天 / 30 天 / 全部`，外加**自定义绝对起止**（`datetime-local`，秒级）。区间写进 URL
  （`range` / `from` / `to`），所以「最近 15 分钟里失败的那些」是一条可以发给别人的链接；
  刷新、前进后退、换标签页都还原。滚动预设每分钟自动跟随当前时间，也可以手动「对齐当前时间」。
  此前总览只有写死的 24h/48h 两个标签，日志页**完全没有时间维度**（只能靠"最近 N 条"倒推）。

- **延迟分布面板移到日志页最上方并默认展开**：原来它是页面最底部一个默认收起的
  `<details>`（只有一个「延迟分布 · 全站最近 1000 条请求」的标题条），排查慢请求时得先滚到底
 还要点开，是全页最不显眼的位置。

- **延迟分布支持时间区间**，不再只能用"全站最新 N 条"：区间生效时取样上限提到 20000 条，
  并新增 **p99**、**慢请求占比**（≥5s，超 5% 标黄）和**样本覆盖率**读数——
  区间内总条数超过取样上限时会明确写出"区间内 X 条 · 取样最新 Y 条"，
  不再让一个截断的样本看起来像全量。

- **`GET /admin/usage/series`**：按窗口在 SQL 内分桶聚合请求数 / 失败数 / tokens / 缓存 tokens /
  费用，桶宽自动吸附到 1m/5m/15m/30m/1h/3h/6h/12h/1d 并对齐到整点。总览图表改用这个接口，
  于是**宽区间不再失真**——此前图表是把列表接口最新 500 行在浏览器里分桶，7 天窗口只能看到
  最后 500 次请求的分布。

- **`GET /admin/usage/top-models`**：按 tokens 在 SQL 内给模型排名（带请求数、失败数、费用）。
  同样摆脱 500 行上限，并顺带给出此前没有的每模型失败数。

- **日志页「导出 CSV」**：把当前筛选命中的行（不只是当前分页）导出为带 UTF-8 BOM 的 CSV，
  含时间、模型、路由、渠道、状态、推理强度、tokens、缓存、耗时、首字节、费用、客户端族、
  上游请求 ID、request_id、错误详情。

- **总览图表标出失败占比**：每个柱子的底部按当桶失败数染色，鼠标悬停显示「其中失败 N」，
  图例相应增加一项。

### Changed

- `GET /admin/proxy-logs`、`GET /admin/usage`、`GET /admin/usage/summary` 新增可选的
  `since` / `until`（RFC3339，闭区间）；`GET /admin/proxy-logs/latency-histogram` 同样接受
  时间区间，取样上限从 10000 提到 100000。非法时间戳一律 `400`，不会静默放宽成全量查询。
- `GET /admin/usage/summary` 同时返回缓存 tokens 与状态分档（`ok_count` / `client_error_count` /
  `server_error_count` / `other_count`），总览的成功率与状态分布矩阵因此是聚合值，
  而不是"我碰巧加载到的那几百行"的统计。
- 总览的 24h/48h 标签、以及"24 小时"写死的卡片文案一并去掉：卡片改称「区间请求 / 区间成本 /
  区间消耗」，区间由上方控件决定；「较上一周期」的趋势徽标改为与**等长的前一个窗口**比较
  （此前是拿"全量-24h"当基线）。
- 总览图表点击柱子进入下钻：不再假定"小时→10 分钟"这种固定层级，而是按被点桶的绝对时间范围
  重新取一次 12 桶序列——任何粒度下钻都成立。

## [v3.0.4] — 2026-09-17

### Fixed

- **一次失败会让上一次生成的图彻底够不到**：工作台结果区只渲染「最新一次运行」，而失败的那次
  也会成为最新一次（只是没有图），面板于是变成一句错误提示。历史条又被
  `history.length > 1` 挡着 —— 只有**一次**成功记录时它根本不渲染，而那恰恰是最需要它的时刻：
  手里只有一张图，一次失败把它从界面上抹掉，没有任何入口能点回去。
  → 历史条改为「只要它存着结果区当前没显示的东西就渲染」（只有单条、且那张图已显示时才隐藏）。
  失败之后，那张图从历史条点一下就能回到结果区。
  注意历史仍是**内存态**：换标签页保留，刷新页面即清空（上限 6 张 / 48 MiB）。

## [v3.0.3] — 2026-09-17

### Fixed

- **工作台把上游的拒绝理由吞掉了**：图像试调失败时只回一句
  「上游返回 HTTP 429，没有解析出图片」，而上游其实已经把原因说清楚了。同一个 429 至少有两种
  完全不同的含义——grok2api 的 Web 通道是「Grok Web 媒体上游返回 429: 8: Too many requests.
  Wait a moment and try again.」（Cloudflare 侧限流），Console 通道是「上游账号额度等待恢复」
  （账号额度冷却）。前者等几分钟、后者等额度恢复，处置完全不同，界面却显示成同一句话，
  只能去翻网关日志才能分辨。

  `/admin/try/image` 在失败或没解析出图片时本来就带回了上游原始响应体（`body`），
  现在前端会从 `body.error.message` 取出上游原话并显示，取不到才退回原来的通用文案。
  文字试调台早就是这么做的，这次把那个函数提成共享模块 `web/src/lib/upstreamError.ts` 给两边共用，
  并补上图像路径一直缺失的测试（单元 + 界面各一条）。

## [v3.0.2] — 2026-09-17

### Fixed

- **聊天客户端里编辑"成功却没图"**：`/chat` 兼容转移把图片结果包回聊天格式时，图片被渲染成
  指向上游**自己域名**的 Markdown 链接（如 `![](https://<上游主机>/v1/media/images/img_xxx)`）。
  下游客户端只配置了网关地址，既没有到那个域名的路由也没有它的凭据，链接取不到就等于没图；
  有的客户端会把它当普通文本，表现为"回复里看不到图片"。现在网关在包装响应前
  **把远程图片抓下来内联成 data URI**，结果自包含，客户端无需访问上游域名。
  抓取复用与渠道流量同一个受策略约束的出站客户端（私网仍被拦、重定向仍复检），
  只发 `Accept: image/*`；**抓取失败、响应不是图片、或超过 12 MiB 都退回原链接**，
  不会把一次成功的编辑变成错误。仅影响已开启「聊天图片编辑兼容」的路由。

  代价要说清楚：内联会把响应体撑大（12 MiB 图片约合 16 MiB base64），
  并且多花一次到上游的抓取（上限 20 秒）。只走 `/v1/images/*` 的调用不受影响——
  那是纯透传，图片仍以链接原样返回。

## [v3.0.1] — 2026-09-17

### Fixed

- **多张参考图的编辑请求体用了单数键，导致该类请求必然失败**：网关自建编辑请求体时
  （`/chat` 兼容转移、工作台图像编辑、管理试调），单张参考图写成 `"image": {…}` 对象，
  多张却写成 `"image": [{…}, {…}]`——**数组挂在单数键下**。上游 grok2api 的编辑接口只认
  **复数键 `"images"`** 承载数组，单数键塞数组一律回
  `400 图片编辑 JSON 请求无效`。也就是说「一次带 2 张以上图片做编辑」此前是一次都跑不通的，
  而单张路径（也是唯一有测试覆盖的路径）一直正常，问题因此长期潜伏。现在多图改写信 `"images"`，
  并补上回归测试 `TestBuildJSONBodyForGrokMultipleReferences` 钉住该形状。
  直接调 `/v1/images/edits` 的下游不受影响——那是纯透传，请求体形状由客户端自己决定。

  另需注意的既有上游语义：多张参考图是**合成一张输出**，不是「按张各改一张」，
  响应里的 `data` 恒为 1 项；要改 N 张图仍需调用 N 次。

## [v3.0.0] — 2026-09-16

### Added

- **模型能力注册表**（迁移 `097_model_capabilities.sql`）：把「该用哪个端点、哪种编码调一个
  模型」从散落各处的内部判断，提升为一张可查看、可校正、可外部同步的表。每个模型记录端点、
  请求编码、输入输出模态、参考图上限、是否流式/异步与尺寸选项，并标注来源：`builtin`
  （模型名推断）/ `discovery`（探测自动标注）/ `catalog`（外部目录同步）/ `manual`（人工校正）。
  **人工校正后即冻结**，后续任何自动写入都不再覆盖；而 `builtin`/`discovery` 行会在规则改进后
  被自动重算——否则改进一条规则永远到不了已有行。
- **外部模型目录同步**（迁移 `099_model_catalog.sql`）：接入 LiteLLM 价目表与 models.dev 索引，
  一次补齐端点、模态、上下文窗口、厂商与单价。写库前强制走 **dry run**，逐字段列出 `— → 值`
  与数据来源，再决定是否应用；能力、元数据、价格三档可分别开关。默认每 24 小时自动同步
  （`MODEL_CATALOG_SYNC_INTERVAL_HOURS`，设 0 只保留手动）。单源失败不中断，全部源失败才报错。
- **模型工作台**（`/console/workbench`）：**图像**按是否带参考图自动在生成与编辑之间切换；
  **文字**是多轮流式对话试验台，复用与线上 `/v1` 完全相同的选路、计费与取消逻辑，无需下游
  令牌；**能力**用于浏览与校正注册表。
- **图片生成与编辑的原生入口**，以及 **`/chat` 兼容转移**（迁移 `098_route_image_edit_shim.sql`）：
  下游把图片发到 `/v1/chat/completions` 时，可按路由开关转移到 `/v1/images/edits`。转移**只改
  请求体、ContentType 与端点**，选路、分组、成员、计费与取消仍走原链路；opt-in，且对纯文本
  请求以及「图像本来就走 chat」的模型不生效。详见 [图片接口说明](docs/image-editing.md)。
- **两套完整界面包与正交配色**：`设置 → 外观` 可在**经典控制台**与**现代工作空间**之间切换，
  布局与过场随包变化；明暗与配色是彼此独立的维度（调整其一不会重置另外两个）。详见
  [界面主题包](docs/ui-themes.md)。
- **日志全文检索**：`proxy_logs` 增加全文索引，日志页可按内容检索，而不只是按模型与状态筛选。
- **CORS 白名单**：默认零配置即可放行，可用 `CORS_ALLOWED_ORIGINS` 收紧为白名单（支持
  `*.example.com`）。中间件挂在根链上、**早于下游鉴权**，无凭据的预检因此不会被 401 拦死。
- **首次引导**：Setup Wizard 与聚光式 GuidedTour，把「连上游 → 建路由 → 发令牌」的最短路径
  直接铺出来。

### Changed

- **控制台信息架构重做**：分组侧栏与统一页头；操作入口按「一个主要操作 + 行内高频 + 更多菜单」
  三层组织，更多菜单与右键菜单共用同一份动作定义；危险操作统一 pending 锁定、关闭限制与焦点
  恢复。详见 [控制台交互约定](docs/console-interactions.md)。
- **厂商归属以模型名推断为准**：外部目录里的 provider 块是**转售商**而非厂商（同一模型被数十个
  provider 重复列出），用它会把 `openai/gpt-oss-20b` 记成 `deepinfra`。现在名字能推断出厂商时
  不再被覆盖，只在无线索时退回目录。同时补齐 MiMo（小米）、混元、Mistral 各产品线、NVIDIA、
  MiniMax、LongCat 等家族的识别。

### Fixed

- **模态在跨源合并时被吞**：合并曾按「先到者独占」，粗粒度来源会覆盖更细的策展结果，出现同步
  反而把 `text,image,pdf` 收窄成 `text,image` 的情况；改为取并集。
- **目录同步结果不确定**：models.dev 是 `provider → models → id` 三层，同一模型被数十个 provider
  重复列出且模态不一致，map 遍历使「谁赢」随机、连续两次同步结果不同；改为排序遍历。
- **单条脏数据拖垮整个数据源**：LiteLLM 的 `sample_spec` 把数值字段写成散文（schema 示例），
  整份文档一次性解码会让该源数千条全部失败；改为逐条容错解码，并把跳过项汇入报告。
- **模型元数据里的转售商厂商无法纠正**：`model_metadata.vendor` 没有来源列、只在空值时回填，
  早期同步写错的转售商标识（如 `pioneer`）此后不会被任何同步纠正，只能显式改写。
- **转录端点误判**：`audio` 输入 + `text` 输出的多模态对话模型曾被判成语音识别端点，改为只有
  「输入含音频且不含文本」才算转录。

## [v2.7.4] — 2026-09-15

### Added

- **连接编辑支持手工填写用户 ID**：New-API 系的数字用户 ID 此前只能靠 AAH 导入带
  入，或由网关在首次签到时用 `/api/user/self` 反查。若上游把这个接口也挡在
  `New-Api-User` 请求头之后，反查必然失败，签到恒报「无法解析平台用户 ID」，而列表
  上的「缺用户 ID」徽标没有对应的输入位置——手动添加的连接因此进了死胡同。现在编辑
  连接的用户凭据区（用户 Access Token / 用户 Cookie 之后）多了「用户 ID」字段，随凭据
  写入 `meta_json.platform_user_id`。该字段缺失时编辑抽屉会自动展开高级选项，让徽标
  指向的地方可见。

### Changed

- **用户 ID 解析容错与归类**：`platform_user_id` 现在接受 JSON 数字或带引号的数字
  字符串（老版本 AAH 导出的形态），管理 API 统一规范化成裸数字写库；非法值（0、负数、
  非数字、非法 JSON）返回 400 而不是静默写零。解析用户 ID 失败时统一归类为
  `user_id_unavailable` 并指明去哪个字段填写；此前上游返回 404/403 会被误报成
  `upstream_status` / `upstream_unauthorized`，把排查方向引向上游而不是缺失的字段。

### Fixed

- **打开编辑抽屉后直接保存会删掉已存的用户凭据**：抽屉在站点凭据查询返回之前就已挂载，
  此时 Token/Cookie 输入框为空，而空值在保存逻辑里被解读为「清除该凭据」，一次保存即可
  删除整条 user credential（实测发出 `DELETE /admin/credentials/{id}`）。现在掩码只播种
  一次，且只提交真正发生变化的字段——未改动的抽屉不再发出任何凭据写入。

## [v2.7.3] — 2026-09-13

### Added

- **上游 API Key 自定义名称**：渠道凭证池的添加框此前只收 `sk-`，手工添加的密钥一律
  落名为 `manual`（列表里 `metapi` / `cc` 等名字全靠上游同步带回）。现在添加行多了
  一个可选的名称输入框，随密钥一起写入凭证 `meta_json.name`，故障转移池里的手工密钥
  从此可读。
- **引导页创建 Key 补全**：第三步创建成功后直接展示一次性明文 token（带复制按钮），
  并提供「再创建一个」；名称输入框不再在创建后被锁死，可连续建多个不同名字的 Key。

### Changed

- **计价模型简化，删除下游密钥级单价**：结算优先级从「路由成员价 → 模型元数据价 →
  密钥价」三层收敛为两层。模型在两层都没设价时按 0 计（免费），不再回落到密钥上的
  全局单价——两层「按模型计价」语义一致，密钥价是游离的全局兜底，最易引起误解。
  `downstream_keys` 的三个价格列保留在库但停止读写，可随时回滚。
- **成本展示一律读真实账单**：密钥页「成本」列改为按密钥聚合的 `usage_records.cost`
  真实入账合计（字段 `estimated_cost` → `cost`，列头「估算费用」→「累计费用」）；
  日志页「成本」列改为按 `request_id` 关联每笔请求的真实结算金额。修复了原先日志页
  在前端用密钥单价重算、与实际入账口径不一致的隐性偏差。
- **连接徽标「已同步模型」→「已路由模型」**：该徽标统计的是已采纳进路由的模型数，
  同步发现的模型数另有独立展示，原文案与语义不符。

### Fixed

- 清理死代码 `usage.EstimateCost`；计费与密钥相关测试改为双层语义并新增成本聚合
  （按密钥 / 按请求）回归用例。

## [v2.7.2] — 2026-09-13

### Fixed

- **Self-Update / Docker Pull**: Fix process panic caused by splitting image reference without explicit tag (e.g. `zichuanlan/meta-gateway`) or containing registry host port.
- **Proxy Stream Policy**: Fix missing `stream_policy` and `non_stream_timeout_seconds` columns in route store candidate queries, unblocking stream conversion pipeline.
- **Relay Stall Watchdog**: Fix potential infinite hang on non-stream clients under forced upstream streaming via `idleTimeoutBody` wrapper.
- **Live Trace Status**: Accurately mark upstream HTTP errors (e.g. 429 / 500) as failed with error context instead of false-positive success.
- **Negative Tool Index Panic**: Prevent slice bounds panic when processing negative tool call indices in stream chunks.
- **Member Prices Resolution**: Resolve member prices strictly by member ID primary key to avoid multi-group rate collision.
- **导入全有或全无**：`exchange.Parse` 以前只要备份里有一行不可用就整份拒绝——
  AAH 里 `authType:"cookie"` 的账号 `access_token` 本来就是空的，缺字段或指向同一
  站点的重复条目也很常见，结果 99% 合法的备份被报成「无法识别的备份格式」，无论
  走文件导入还是网盘同步。现在改为**逐条容错**：不可用的行连同原因进 `skipped`
  明细，其余照常导入；只有一行都导不进来时才报「没有可导入的凭据」，与「格式不
  认识」区分开。
- **自导出往返自坏**：交换信封的严格解码（`DisallowUnknownFields`）不认自己写出的
  `skipped` 字段。只要导出时有渠道因为没凭据而被跳过，这份导出就再也导不回来。
- **无密钥导出误报**：`include_secrets:false` 的导出（信封自己标了 `importable:false`）
  被报成「文档无效」，真实原因只是「这份导出不含凭据」。
- **导入错误话术**：导入失败以前复用 `validation_error` / `unsupported_format`，控制台
  据此渲染成「检查 Base URL、连接类型和凭据状态」，与真实原因无关。现在导入有专用
  分类（`exchange_document_invalid` / `_unsupported` / `_empty` / `_conflict`，
  以及 `backup_unlock_required` / `decrypt_failed`）。
- **备份预览认不出新版备份**：前端预览写死 `version === "2.0"`，而 AAH 当前写 `"4.0"`、
  还可能把各段嵌在 `data` 下。用户先被界面告知「认不出这份备份」，才去点导入。
- **AAH 选择性同步的备份**：只勾了偏好设置 / 标签、完全没带凭据段的备份，以前报
  「不支持的格式」，现在识别为「这份备份里没有可导入的凭据」，并提示去 AAH 勾上
  「账号」与「API 凭据」后重新备份。

### Added

- **加密备份可直接导入**：AAH 的加密封套（`all-api-hub-webdav-backup-encrypted`，
  PBKDF2-SHA256 250000 轮 + AES-256-GCM）以前只有网盘拉取认识，文件导入拿到的只是一份
  认不出的 JSON。现在导入面板会识别出加密备份并给出解锁密码输入框，服务端与网盘下载
  共用同一套解密实现，同一份备份在两条路径上都能打开。密码错误会明确提示「无法解密
  备份」，而不是含混的格式错误。
- **新手指引支持网盘导入**：引导页的 AAH 导入从「只能拖文件」扩展为**文件 / 网盘**两个
  子页。网盘侧填地址、账号与密码 → 测试连接 → 立即导入，走的就是控制台同一个 WebDAV
  同步接口；定时同步保持关闭，引导流程不会静默开启周期性导入。
- 导入结果会回传被跳过的条目（序号、名称、原因），导入面板逐条列出，避免「导入成功但
  少了几条」变成无声的数据丢失。

### Changed

- **Console Layout & Hygiene**: Group governance rules, isolate TOTP security & dangerous reset zones, add delete confirmation dialogs to alert and guard rules, align accessibility focus rings, and tokenize layout spacing.
- **Documentation & Identity**: Overhaul README.md with high-precision symmetrical 16:10 screenshots, real brandmark logo, cyber-kinetic banner, card-matrix features, and AI one-click prompt.


### Added

- Pricing gains its most precise layer: **per route member** (this channel
  serving this model — the same model is often priced differently per
  upstream). The member edit dialog carries the three unit prices; relay
  billing resolves from the most specific layer that has one: **成员价 →
  模型默认价（元数据）→ 密钥价**, with cache-read tokens billing at the
  member's cache price when set. Member prices survive full-member updates
  (toggles, bulk edits) because they live on the member row itself.

## [v2.7.0] — 2026-09-12

### Added

- Per-model self-set pricing. The model metadata editor (model row → 编辑元
  数据) gains three unit-price fields — 输入单价 / 输出单价 / 缓存读取单价,
  each per 1k tokens in whatever unit the operator uses consistently. Billing
  now prefers the model's own prices: prompt and cache-creation tokens at the
  prompt price, cache-read tokens at the cache price, all scaled by the
  model's billing ratio as before. When a model has no priced row (or both
  prices are zero) the downstream key's unit prices apply exactly as before —
  the per-key pricing stays as the fallback layer.

## [v2.6.0] — 2026-09-12

### Added

- The 一键更新 button now works out of the box — no socket mount needed in
  the gateway container. Compose ships an idle watchtower executor on the
  project network (ports never published; it holds the Docker socket so the
  gateway container does not have to): it runs NO periodic updates and only
  wakes when the console button triggers it — pull, recreate, verify, done.
  Removing the service (`docker compose stop watchtower`) returns to the
  command-line update path. The direct socket handoff remains as the
  alternative mode when the socket is mounted into the gateway itself.

## [v2.5.9] — 2026-09-12

### Changed

- Bulk selection on the 连接 and 模型 pages is now opt-in instead of a
  permanent checkbox column: right-click a row (or use the row menu) and pick
  **批量选择** — the checkbox column and the bulk action bar appear, and
  **完成** exits the mode and clears the selection.

### Changed (dashboard)

- The cockpit no longer shows two near-identical recent-request feeds: the
  最新活动 column merged into 最近代理日志, which now also carries per-request
  token usage; the 24 小时模型用量 ranking takes the full width.

## [v2.5.8] — 2026-09-12

### Added

- One-click container update. With the Docker socket mounted (opt-in in
  compose — the socket carries roughly host-root power, so it stays off by
  default), 检查更新 gains a 一键更新到 {version} button next to the found
  release: the gateway pulls the new image, hands off to a successor
  container that recreates the final one with the original name/ports/volume
  bindings, and restarts — a few seconds of downtime, automatic rollback to
  the current version if the handoff fails, data volumes untouched. The
  update endpoint is admin-token gated, validates the target against the
  cached latest release, lands in the audit log, and only ever touches its
  own container and image. Without the socket the panel keeps the
  copy-command fallback.

## [v2.5.7] — 2026-09-12

### Added

- Bulk selection on the two remaining list pages. 连接 (channels): checkbox
  column with select-all-page, then 同步模型 / 启用 / 停用 over the selection
  in one pass. 模型 (routing): checkbox column with 启用选中 / 停用选中 /
  删除选中 (delete confirms first; member bindings go with the route).
- The upstream-change reminder now recognises work already done: a pending
  新增 whose model is already wired into the channel's routing is badged
  已接入, excluded from the summary count, covered by the one-click 忽略无影响
  bulk action, and resolved automatically by the nightly sweep.

## [v2.5.6] — 2026-09-12

### Fixed

- Renaming a model — alias, 统一名称, or any member mapping — no longer breaks
  relaying with `502 proxy: upstream credential unavailable`. The multi-key
  feature filters the key pool by which key actually lists the requested
  model, but it filtered on the public request name while a key's recorded
  set contains upstream names: a mapped name matched nothing and starved the
  pool even though the channel worked fine for listing. Key-pool selection
  now resolves against the effective upstream name (the member/route
  mapping's real model), and when no key's set claims the name at all
  (custom names, fresh renames before the next sync) the pool fails open with
  the model-blind selection — a wrong-group key just draws a missable 404
  upstream and failover moves on, instead of misreporting a naming problem as
  an auth failure.

## [v2.5.5] — 2026-09-12

### Added

- Live trace（日志 → 实时）overhaul. Running rows now tick every second
  client-side and streams stay visible for their whole life: a streaming
  response keeps its running state (with first-byte latency and a ~1/s byte
  progress) until the client has received everything, instead of flipping to
  success at response headers. New requests appear the moment they enter the
  gateway (the registry publishes on admission, not after routing).
- The live table carries real context: 客户端 (authenticated downstream key
  name), protocol + stream marker per channel, a failover chain
  (A✗ → B✗ → C with per-round failure reasons), TTFT, transferred bytes and
  final token counts.
- Operator interrupts now explain themselves: when a request interrupted from
  the console is followed within 15 seconds by a new request for the same
  model from the same client key, the new row is badged **疑似重试**（likely
  client retry）and links the interrupted request — the gateway cannot stop
  clients from auto-retrying, but the view finally says so. The interrupt
  toast says the same. Plus 全部中止（interrupt all in-flight）and a pause
  toggle that freezes the view for inspection and folds buffered frames back
  in on resume.
- Channel-level **非流式请求超时（秒）**（non-stream timeout, advanced edit
  field, `non_stream_timeout_seconds`, 0 = global 5-minute default): slow
  deep-reasoning upstreams can raise their own budget. Streaming requests are
  exempt; the global 2-minute stream idle guard is unchanged.
- Manual-sync channels now treat the checklist as the routing state:
  **unchecking a model removes its binding outright** (and a route the removal
  empties), re-checking adopts it fresh — the list no longer accumulates
  parked rows. Alias mappings are deliberate configuration and still park,
  and auto-sync channels keep the park semantic (reconcile respects parked
  members there; a deleted one would be re-adopted on the next sync).
  清理已停用（N）remains for legacy parked bindings and auto channels: one
  click deletes every parked binding on the channel and removes routes the
  cleanup empties.
- Channel-level **流式策略**（stream policy, advanced edit field,
  `stream_policy`). 跟随客户端 by default; 强制流式 serves non-streaming
  clients from an upstream stream the gateway aggregates into one completion
  (delta content, tool-call arguments and usage merge by frame); 强制非流式
  serves streaming clients from a non-streaming upstream answer replayed as a
  single-chunk SSE stream. The override applies to OpenAI-shaped chat
  exchanges — native Anthropic passthrough and the Responses API are exempt —
  and the non-stream budget follows the upstream's actual mode: an aggregated
  stream is not capped at five minutes, a synthesized answer is.

### Fixed

- Saving the edit-connection dialog no longer stomps the model sync mode
  changed in the model-management drawer. The drawer persists the toggle with
  an immediate single-field PATCH, but the still-open dialog used to rewrite
  it with its stale seeded value on save; the field is now sent only when the
  operator actually moved the picker inside the dialog.
- A request whose handler exited through an unhandled path no longer lingers
  as a phantom running row in the live view; the registry settles it on
  release.

## [v2.5.4] — 2026-09-12

### Added

- Upstream model-change maintenance gains confidence signals and automation.
  Each pending removal now tracks how many consecutive complete syncs have
  lacked the model and is badged **已确认缺失**（Confirmed missing）once that
  reaches three, with the summary showing the confirmed count; snapshots taken
  while only some of a channel's API keys answered flag the row **部分 Key
  未响应**（possible false positive）and do not advance the counter; a model
  re-disappearing within 30 days increments a **反复上下线 ×N** churn counter
  instead of stacking history rows; and when the relay's upstream itself
  reported the channel × model as not found at request time (the existing
  model-block blacklist), the row shows **运行时观测到不可用** with the
  timestamp. Pending removals also display how many days they have gone
  unhandled.
- Handling is faster on both ends: pending additions offer a **去接入**
  deep link straight into the channel's model page with the model pre-filtered
  in search, and a **忽略无影响（N）** bulk action ignores every pending
  removal with no impacted route member in one click.
- Alert rules gain two gauges, `model_change_removed` and
  `model_change_affected_routes`, so upstream churn can notify through the
  existing webhook/bark/serverchan/telegram/smtp rules — recommended rule:
  `model_change_affected_routes > 0`.
- Two upkeep knobs (daily sweep, zero disables): `MODEL_CHANGE_RETENTION_DAYS`
  (default 90) prunes finished model-change entries;
  `MODEL_CHANGE_AUTO_IGNORE_DAYS` (default 0 = off) auto-ignores harmless
  pending removals — candidate-list churn with no route impact — after N days.
- The channel model manager's status filter（全部 / 已启用 / 已禁用）now carries
  a live count badge on every option（total / enabled / disabled）, and the
  control is styled as a toolbar-height segmented switch aligned with the
  search and custom-model inputs.

### Fixed

- The console update check no longer claims "已是最新版本" (up to date) on dev
  builds. The comparison only parses dotted-numeric versions, so a binary
  built without the version ldflags could never see an update even when a
  newer release existed on GitHub; the panel now reports the build as a dev
  build and shows the latest release it found instead.

### Changed

- `docker compose build` forwards the VERSION and COMMIT build args
  (`VERSION=v2.5.4 docker compose build`), so locally built images can carry
  the real release tag in the console title and 当前版本 row instead of the
  buildinfo default "dev".

## [v2.5.3] — 2026-09-11

### Added

- The channel model manager (edit connection → 管理, or the `/models/channel/:id`
  page) can now narrow the candidate list by adoption state: **全部 / 已启用 /
  已禁用**（All / Enabled / Disabled）. The filter composes with search and bulk
  mode, updates the vendor-group counts, and shows an empty-state line when
  nothing matches. Previously the panel only offered search and bulk select, so
  parked models could not be isolated from adopted ones.

## [v2.5.2] — 2026-09-11

### Fixed

- Opening the console's live view could freeze every model at once. The live
  tab (Logs → live, SSE `/admin/relay/live`) subscribed through
  `livetrace.Registry.Subscribe`, which replayed the retained snapshot onto a
  subscriber channel sized for the live queue (32) while retention keeps up to
  50 finished requests. Past 32 served requests the replay blocked on the 33rd
  send **with the registry mutex held**; because every relay request calls
  `Attempt`/`Begin`/`Finish` — all of which take that mutex — the first
  live-view connection stalled all traffic. Requests were still accepted and
  model lists still loaded, but no model could answer, streaming or not, and
  the process never recovered on its own: only a restart cleared it. The
  subscriber queue is now sized for the snapshot plus the same live headroom
  the publisher tolerates, so replay can never block.
- The SSE handler treats a closed subscriber channel as terminal. After an
  overflow drop it read from the closed channel forever, replaying zero-value
  frames to the browser in a tight loop instead of returning so the client
  could reconnect and re-receive the snapshot.

### Changed

- Container images build the web and Go stages on the build platform and
  cross-compile per target architecture instead of running the whole toolchain
  under QEMU emulation. Multi-architecture releases no longer inherit QEMU's
  flakiness — `go mod download` died under emulation on 2026-09-10, which left
  v2.5.1 with a tag but no published image and no GitHub release — and release
  builds are substantially faster.
- GitHub releases are published with that version's CHANGELOG section as the
  release body plus the compare link, instead of the generated
  "Full Changelog" line on its own.

## [v2.5.1] — 2026-09-10

### Added

- Model discovery merges every enabled API key's list instead of stopping
  at the first usable key: a New API host with one key per group now syncs
  all groups' models (sorted, de-duplicated) instead of only whichever key
  answered first. Keys that fail keep their previously recorded set, so a
  transiently broken key never blanks its models.
- Per-key visibility is snapshotted (`credential_models`) on every
  successful refresh, and the relay's key pool filters each key by its
  discovered set when no manual `models_csv` allowlist exists: a request
  for a codex-group model only uses keys that actually list that model,
  shared models still rotate across the pool; manual allowlists keep
  precedence over learned sets.
- Upstream key creation is offered again even when the site already has
  keys (one key per group is the common setup), and the key drawer shows
  how many models each key synced.

## [v2.5.0] — 2026-09-10

### Added

- Upstream model change maintenance: the Models page surfaces a change
  summary (added / possibly removed / impacted routes) and an expandable,
  filterable history (channel, change type, handling status, model name).
  Snapshots are compared per channel; the first successful sync establishes
  a baseline, a failed sync never counts as removal, and a model missing
  from one channel is reported as possibly removed — not as retired
  everywhere.
- The sync no longer silently deletes automatic members or routes when a
  model disappears: bindings stay in place (IDs, overrides, health state
  intact) so an operator can inspect and remap them.
- Replacement workflow: select changes → pick a target model from the
  current inventory (same-sync additions are shown as candidates, never
  auto-guessed as "newer versions") → choose affected members → preview on
  the server → confirm. Supports same-channel bulk replacement and an
  explicit cross-channel choice; only the upstream mapping changes, public
  model names, route names and other settings are preserved.
- Preview tokens are state fingerprints: applying with a stale selection
  (snapshot refresh, changed mapping, moved credential) is rejected with
  409, so a preview you looked at applies exactly what it showed. Bulk
  application rolls back atomically on failure.
- Ignoring a change only dismisses the reminder; reappearing models are
  tracked again, and outdated pending entries are resolved.

## [v2.4.0] — 2026-09-10

### Added

- Responses API translation: a client speaking OpenAI `/v1/responses` is now
  served by ANY channel — native passthrough when the upstream has the
  endpoint, an automatic one-shot chat/completions pivot on 404/405 for
  OpenAI-compatible channels without it, and the translation matrix routes
  Anthropic/Gemini channels through the chat pivot (`responses → anthropic / gemini` pairs).
  Streams reshape into the Responses SSE event contract (`response.created`,
  `output_text.delta`, `response.completed` …) and usage metering understands
  `response.usage`.
- Live request trace (admin API): `GET /admin/relay/live` streams in-memory
  request states over SSE (running → target channel/round → success/failed/
  canceled) and `POST /admin/relay/live/{request_id}/interrupt` cancels an
  in-flight upstream attempt.
- Console live-trace tab: the Logs page gains a "Live Trace" tab wired to
  `/admin/relay/live` — real-time rows (model, target channel, round, status,
  duration) with in-flight interrupt buttons, connection state, backoff
  reconnect, and a bounded window of settled requests.

## [v2.3.4] — 2026-09-10

### Fixed

- WebDAV scheduled sync no longer overwrites a key you saved or rotated in
  the console: incremental imports now treat a credential with a cleared
  `import_fingerprint` (the marker left by a manual secret edit) as locally
  owned and skip the backup value, while import-managed credentials keep
  their token-rotation semantics and empty creds are still backfilled

### Changed

- External check-in sends a browser `User-Agent` by default so
  Cloudflare-fronted sites stop rejecting the bare Go client UA with 403;
  per-credential custom headers still override it

## [v2.3.3] — 2026-09-06

### Fixed

- The logs page status column no longer shows a misaligned green dot: the
  colored status light and the status badge now share one vertically
  centered row, and the badge's redundant built-in dot is hidden
- The connection type picker no longer freezes when typing Chinese: the
  search box stayed mounted only while more than four options matched, so
  two letters unmounted the input mid-composition and stranded the IME.
  Whether the box appears is now decided once when the panel opens

### Maintenance

- CI is green again: restore gofmt struct-tag alignment (failing since
  v2.3.1) and remove a data race in the probe service tests that
  `go test -race` flagged

## [v2.3.2] — 2026-09-06

### Added

- Routing member rows: the channel name is clickable and jumps to that
  channel's model management page (`/models/channel/:id`), pre-filtered on
  the member's origin model via a `?model=` deep link (the route pattern
  when the member has no origin)

### Fixed

- The unify dialog's "strip owner prefix" badge no longer shows a hardcoded
  `deepseek-ai/` example: it names the prefix the group actually loses
  (`meta/`, `deepseek-ai/`, one badge per distinct prefix), and the rule
  checkbox label marks deepseek-ai/ as an example instead

## [v2.3.1] — 2026-09-06

### Added

- The runtime schedule fields (定时模型同步 / 探测计划) use the same
  preset picker as check-in instead of a raw cron input: off / hourly /
  every 3-12 hours / daily at a picked time / custom cron, with the empty
  (disabled) state spelled out instead of a blank text box

### Changed

- The channel edit drawer moved 用户 Access Token / 用户 Cookie out of the
  main form into the advanced section, and only shows them for site
  families that can actually use them (New-API-family account surfaces;
  cookie-only for generic external check-in). Plain OpenAI-compatible
  relays, official provider APIs, and unsupported families no longer show
  the fields at all — unless a credential is already stored, so it stays
  clearable

## [v2.3.0] — 2026-09-06

### Added

- A guided picker for the per-channel model sync mode (auto vs on-demand) in
  the channel edit drawer, the add-channel dialog, and a quick auto/manual
  switch on the channel models page: mode cards with trade-offs, a preview
  of what the next sync will do, live "N models · M adopted" counters, the
  inherit-system-default marker, and a collapsible "how do the modes
  differ?" explainer
- `POST /admin/connections` accepts an optional `model_sync_mode`
  (`auto`/`manual`; empty inherits the system default)
- The channel models page telemetry now pairs model total with adopted,
  enabled, and aliased counts, and manual-mode channels with pending
  candidates show a "N not adopted yet" hint instead of a bare 0
- The add-route dialog can auto-match channels serving the model: it lists
  every enabled channel whose models.csv or discovery snapshot matches the
  pattern (`GET /admin/discovery/model-channels` previews the match) with
  per-channel checkboxes, all selected by default, and only the checked
  ones are attached as members (`auto_match_channel_ids` on
  `POST /admin/routes`). A route that already carries the pattern is
  reused — the checked channels attach to it — instead of failing with
  "already exists"
- The unify assistant can now re-unify restored originals: a group whose
  canonical route exists but whose original name is exposed again (restored
  from history) stays listed with an "N exposed originals" badge, and
  applying hides the duplicates once more
- The channel overview and list report the discovered candidate count
  (`discovered_model_count`) next to the adopted model count, so
  manual-mode channels read as "N of M adopted" instead of a bare 0

### Fixed

- The channel edit drawer no longer forgets the model sync mode:
  `ListOverviews` (the endpoint the form seeds from) omitted the
  `model_sync_mode` column — along with `max_reasoning_effort`,
  `payload_rules`, `max_concurrent`, and `proxy_url` — so the empty
  read-back normalized to "manual" and a saved auto-sync channel reopened
  as on-demand; the columns are now selected, scanned, and normalized, with
  a regression test covering the projection
- The channel list model column no longer shows a stark bold 0 for channels
  without models: synced-but-nothing-adopted renders a muted 0 with a
  tooltip pointing at the models page or auto sync, never-synced renders a
  muted dash (mirroring the latency column), and adopted counts stay bold
- Unify apply no longer leaves a silent dead alias: a pre-existing disabled
  route with the canonical name is re-enabled, recorded as its own
  undoable op
- Unify undo refuses to delete a created route that still carries members
  from another batch or added by hand, instead of cascading them away
- Unify history counts only the still-hidden originals per batch and keeps
  restored entries visible (greyed out) so a restore leaves a trace
- Jumping from the models page to a channel's model settings drawer no
  longer needs closing it twice: the deep-link effect is one-shot per
  navigation (a close committed before the router's param transition used
  to re-fire it with the stale `?channel=` URL) and closing strips the
  resurrected param
- Info tips in checkbox labels stay inline after the label instead of
  wrapping onto their own line

## [v2.2.0] — 2026-08-31

### Added

- Upstream error details on failed log rows: the real upstream error body or
  transport error string (UTF-8-safe, 600 bytes) is captured into
  `proxy_logs.error_detail` and rendered in the log expansion next to the
  routing decision panel, so a failure no longer needs guesswork to diagnose
- Consecutive transport failures (connection refused, TLS, timeouts) now
  count toward channel health: the first failure of a streak stays
  cooldown-free (pure jitter is still free) while a repeat inside the same
  streak earns the full cooldown, the channel consecutive-failure counter
  (auto-disable) accumulates, and the next success clears the streak

### Fixed

- An outbound header/TLS timeout no longer kills the request as
  "cancelled": with the client still waiting it is reclassified as a
  retryable transport failure, so the failover walk reaches the other
  channels instead of ending after the first slow upstream
- The retried mark on log rows now sits on the row that TRIGGERED the retry
  (a later attempt of the same request exists) instead of on the retried
  attempt itself — a 200 row no longer reads as "retried"
- When every member of a route is cooling, the selector now tries the
  least-bad cooling member (highest priority, earliest expiry) instead of
  failing the request outright: a sole-member route no longer
  self-inflicts an outage for the whole cooldown window, and a successful
  fallback attempt doubles as the natural recovery path. Disabled,
  absent-credential, and already-attempted members stay out of the
  fallback; a fully disabled fleet still fails fast
- Fault-protection settings hint now describes the transport-failure
  streak semantics instead of the old "jitter is never penalized" wording

## [v2.1.2] — 2026-08-31

### Fixed

- Proxy log audit: every failover attempt row now shows the routing
  decision behind THAT attempt — snapshots carry an attempt number, the
  panel names the channel actually picked (`selected_channel_id`,
  highlighted) instead of repeating the last attempt's decision and
  the highest-priority candidate on every row
- Silent upstream failures no longer reach clients as empty replies:
  a 2xx chat completion with no choices / an empty message / a 2xx
  error object, and 200 streams that end or stall after only
  role/usage frames, are now retryable failures that fail over.
  The empty-reply failure is variant-scoped, so the channel's other
  names stay in the fallback walk; `content_filter` and tool-call
  responses still pass through untouched
- Error labels no longer disguise client cancellations as network
  errors: "cancelled (client gone/timeout, no retry)" and
  "empty reply" are their own classes now
- Routing decision panel renders cooldown reasons in amber and marks
  the picked channel

## [v2.1.1] — 2026-08-31

### Admin console

- Model list gains a status filter (enabled / disabled / all, default
  enabled) next to the channel filter, so shadow models left behind
  by name unification stay out of sight until wanted
- Toolbar keeps search + family/channel/status filters on one row at
  desktop widths (selects size to content, search absorbs the rest)

## [v2.1.0] — 2026-08-31

### Admin console

- Settings page reorganized into semantic groups (routing / health /
  governance / ops / maintenance tools) with headers matching the
  section nav; cards flow into balanced masonry columns
- Field relocations: model sync (discovery cron + default sync mode)
  now sits in the health group next to probing; the global outbound
  proxy moved to the renamed "Service & network" card, which also
  shows the build version
- Update check: the toggle lives in "Service & network" with a
  check-now button (`POST /admin/update-check/refresh`) and result
  readout

### Fixed

- Data race in the probe test fake under concurrent workers (caught by
  the CI race detector)

## [v2.0.2] — 2026-08-31

First tagged release.

### Relay core

- OpenAI-compatible relay (`/v1/*`) across multiple upstream channels with
  model routing, retry rounds, and cross-channel failover
- Same-key re-sends, key-pool rotation, stable-first grayscale pools,
  sticky sessions, latency/error-aware routing, and an in-flight
  concurrency guard
- Fault protection: consecutive-failure cooldown, channel auto-disable,
  and passive recovery probes

### Models

- Scheduled model discovery with per-channel auto/manual adoption modes
- Unify assistant for managing adopted model names across groups
- Scheduled model probing with optional auto-disable on repeated failures

### Operations

- Alert matrix (webhook / Bark / ServerChan / Telegram / SMTP) with
  proactive health sweeps and daily digests
- Scheduled check-ins, balance exchange, audit logs with retention,
  database maintenance (orphan GC + VACUUM), and TOTP/redemption tooling
- Prometheus metrics, health/ready endpoints, and structured request logs

### Admin console

- React console: overview telemetry, command palette, zh-CN/English UI,
  route animations, and dark mode
- Custom login/console backdrop with opt-in localStorage persistence
- Sidecar plugin host with an in-console market

### Security

- Encrypted upstream keys (MASTER_KEY), admin bearer auth, rate limits on
  relay and admin surfaces, and outbound SSRF guardrails
  (OUTBOUND_ALLOW_CIDRS)

### Backup & sync

- Online backups with retention, plus native WebDAV two-way sync and
  AAH 4.0 import — independent connections, schedules, and results per
  direction

### Deployment

- Multi-stage Dockerfile (non-root, amd64/arm64), docker compose files,
  CI covering lint/tests/e2e, and tagged releases with version-injected
  builds plus an opt-out update check
