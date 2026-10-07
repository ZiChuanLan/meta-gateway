# Product

<!-- impeccable:product-schema 1 -->

## Platform

web

## Users

自托管这台网关的运营者本人，加上少数朋友 / 小团队（几个成员账号）。没有面向陌生人的公开注册或分发：成员端只需「不拖后腿、能被朋友正常使用」，打磨预算主要花在运营者每天打开的页面上。

使用者都在浏览器里工作：配置上游连接与渠道账号、维护模型路由与价格、盯请求日志与用量、处理签到与余额。

## Product Purpose

一台自托管的 AI 网关 + 管理控制台。对下游暴露 OpenAI / Anthropic 兼容协议，对上游聚合多个渠道（站点）的账号与模型，做路由、故障转移、用量计费与审计。成功 = 运营者能在几分钟内看清「哪条链路在服务、谁在花钱、哪里坏了」，并且愿意把界面截图分享出去。

## Positioning

**透明 pass-through**：不改写请求语义、不注入提示词、不伪造响应。这一条决定了很多界面事实 —— 日志里能看到真实上游 URL、真实上游模型名与真实用量，控制台不虚构任何指标。

## Operating Context

- 部署：Docker 容器，本机或小服务器；数据卷里是 SQLite 库与加密后的渠道凭据。
- 日常动作：加渠道 → 拉模型 → 建路由 → 发密钥给朋友 → 看日志与用量。
- 排障第一现场是请求日志（含 upstream_url / 上游模型 / 用时 / 计费）。
- 会话与钱包：成员有自己的令牌、用量、额度；运营者有站点级的余额与成本。
- 界面有两种主题包（modern / classic），用户会按心情切换。

## Capabilities and Constraints

- 协议适配：openai / anthropic / gemini / responses 等，含自定义端点映射与字段搬运。
- 计费：两层单价（路由×渠道 / 模型），支持阶梯价与时段倍率；成本一律读真实账单。
- 插件：sidecar HTTP 插件可拦截 route / request / response。
- 约束：界面文案必须中英双语（有 parity 测试守着）；已有 88 个前端测试文件、507 条测试，改动不能放宽它们。
- 明确不做：面向陌生人的公开分发站、注册落地页、云商店式营销页。

## Brand Commitments

- 名字保留：**Meta Gateway**（含中文语境下的「网关」说法）。
- 保留 **modern / classic 两个主题包**，并保留它们各自的风格特征 —— 不是要推翻视觉世界，而是把它做得更精致、信息更足。
- 能复用管理端组件的地方就复用（成员端优先对齐管理端，只在真正需要差异处做差异）。

## Evidence on Hand

- `docs/` 文档站（VitePress）+ `docs/reference/*` 由代码生成。
- `web/src/styles/tokens.css`、`system.css`、`shell.css`、`workspaces.css` 是当前的视觉真相；`web/src/components/ui.tsx` 是共享组件库（Page / PageActions / Panel / Dialog / Field / Button / StatusBadge / DataTable / Tabs / InfoTip / TelemetryStrip）。
- 参考素材（用户提供）：ai.apizn.com 的极简黑白与协议拓扑表达；api.ilovecat520.me 的富信息模型卡（24h 用量、延迟、可用性条、含倍率价格、三层筛选 chips）。
- 不得虚构：不从参考站照抄品牌色、文案与用户数据。

## Product Principles

1. **透明优先** —— 界面只呈现真实存在的数字；拿不到就显示「不适用」，不编造。
2. **一眼看懂链路** —— 每个请求、每条路由、每个模型都要能回答「现在是谁在服务、花了多少、为什么」。
3. **复用而不另起一套** —— 成员端与运营端共用同一套组件语言，差异只出现在真正不同的地方。
4. **密集但有呼吸** —— 信息密度高，靠排版、留白与层次而不是靠堆装饰。
5. **两种主题同等完成度** —— 新组件必须同时在 modern 与 classic 下经得起看。
