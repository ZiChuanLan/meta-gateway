# 站点签到

网关每天替代浏览器点一次签到按钮。控制台入口：**签到**页。

## 两种签到不是一回事

| | 谁 | 路径 | 配置在哪 |
| :--- | :--- | :--- | :--- |
| New-API 家族 | 该站自己的 `/api/user/checkin` | 固定（个别分支可用 `checkin_path` 覆盖）| 连接编辑对话框（`auth_mode=cookie`）|
| **外部签到** | 任何别的形状 | 由操作员填 | 签到页 →「添加站点」|

外部签到**不是** New-API 账号 adapter，也不进账号同步：它只做“带着 Cookie 打一次这个端点”。

## 外部签到发出的请求

以站点根地址为基准，拼上「签到路径」，然后：

| 头部 | 值 |
| :--- | :--- |
| `Cookie` | 你粘贴的那一行（原样发送）|
| `Origin` / `Referer` | 由站点地址推导（`https://host` 与 `https://host/`）|
| `User-Agent` | 桌面浏览器 UA（裸 Go UA 会被 WAF 直接拒）|
| `Content-Type` | `application/json`（仅 POST）|
| 额外请求头 | 你填的 JSON 对象；`Host` / `Cookie` / 逐跳头**不可覆盖** |
| 请求体 | 你填的 JSON（可空）；`{{today}}` 在执行时展开 |

`{{today}}` 按 `CHECKIN_TZ` 的**签到日**展开为 `YYYY-MM-DD`。战役式签到（今天要签哪一天）必须用它：
写死日期的配置第二天就在补签昨天。

## 成功怎么判定

- **2xx + JSON `success != false`** → 成功（`data.reward` 会记进日志）。
- **2xx + 非 JSON**（纯文本 `ok` 之类）→ 成功：小站的签到端点常常什么都不返回。
- **2xx + HTML 页面** → **失败**，并明确说是 WAF/挑战页。边缘盾（阿里云 ESA 的 `acw_sc__v2`、Cloudflare 的五秒盾）在缺少盾 cookie 时回 **200 + 一段 JS 页**，把这种响应当成成功会让人看到“绿了但没签到”。
- **400/401 且消息是「今日已签到」之类** → 成功（幂等）。
- 其余非 2xx → 失败，附带站点返回的短消息。

## 五秒盾 / WAF 怎么办

盾只在**缺少盾 cookie** 时才挑战，所以：

1. 从浏览器复制 **整行 Cookie**，把站点自己的会话 cookie 和盾 cookie 一起带上
   （例如 `session=…; acw_tc=…; cdn_sec_tc=…; acw_sc__v2=…`）。只填 `session=` 往往在几小时后开始吃 200+HTML。
2. 盾 cookie 有寿命，过期后必须重新复制一次 —— 网关**不执行 JavaScript**，也不会自动过盾。
3. 需要完全无人值守过盾的站点，正确做法是**插件**（插件可以自带浏览器），不是往网关本体里塞一个浏览器。

## 常见失败对照

| 现象 | 含义 | 怎么办 |
| :--- | :--- | :--- |
| `HTTP 405 method not allowed` | 这个路径存在，但不接受这个方法 | 填**站点自己签到按钮**打的那个动作路径，例如 `/api/user/checkin-campaigns/5/claim` 而不是战役列表 `/api/user/checkin-campaigns` |
| `HTTP 401 / 403` | Cookie 过期、被 IP 拦截，或缺 `new-api-user` 这类请求头 | 重新复制 Cookie；补上站点要求的头 |
| `HTTP 200` 但报「上游返回的是 HTML 页面」 | 挑战页/登录页，签到没发生 | 见上一节：把盾 cookie 一起填上 |
| 参数缺失（400） | 这个端点是**动作**，要带参数 | 在「请求体」里按站点前端的写法填，例如 `{"date":"{{today}}"}` |

## 两个真实例子

**anyrouter.top**（New-API 站，但签到端点是 `/api/user/sign_in`）：

| 字段 | 值 |
| :--- | :--- |
| 站点地址 | `https://anyrouter.top` |
| 签到路径 | `/api/user/sign_in` |
| 方法 | `POST` |
| 额外请求头 | `{"new-api-user":"<你的用户 id>"}` |
| 请求体 | 留空（这家就是空 body）|
| Cookie | `session=…`（浏览器里那整行；被盾挑战时把 `acw_*` 一起带上）|

**relayfor.xyz**（战役式签到，claim 是一个带日期的动作）：

| 字段 | 值 |
| :--- | :--- |
| 站点地址 | `https://relayfor.xyz` |
| 签到路径 | `/api/user/checkin-campaigns/<战役 id>/claim` |
| 方法 | `POST` |
| 请求体 | `{"date":"{{today}}"}` |
| Cookie | `auth_token=…` |

> 战役 id 写死在路径里：站点换了战役就得改一次路径 —— 网关只能照你给的路径打。

## 相关

- [数据保留策略](/operations/retention)
- [站点探针](/upstream/site-probe)
- [故障排查](/operations/troubleshooting)
