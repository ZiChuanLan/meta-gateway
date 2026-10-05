---
layout: home

hero:
  name: Meta Gateway
  text: 自托管 AI 网关
  tagline: 对下游暴露 OpenAI / Anthropic 兼容协议，对上游聚合多站点渠道与模型，做路由、故障转移、用量计费与审计。
  actions:
    - theme: brand
      text: 快速开始
      link: /guide/quickstart-docker
    - theme: alt
      text: 核心概念
      link: /guide/concepts

features:
  - title: 透明 pass-through
    details: 不改写请求语义、不注入提示词、不伪造响应。图像等非幂等端点按字节原样转发，且不做故障转移重试。
  - title: 协议原生互译
    details: OpenAI / Anthropic / Gemini / Responses 四族适配器，经中间格式 pivot 转换；非 OpenAI 供应商的字段映射随供应商 profile 走。
  - title: 路由与故障转移
    details: 路由 × 成员 × 分组三层模型，支持固定成员、权重、会话粘性、跨渠道重试、渐进式冷却与熔断恢复。
  - title: 两层单价链
    details: 路由成员单价优先于模型元数据单价，命中即停止下探；计费倍率独立生效，配额与计价正交。
  - title: 可审计到链路
    details: 每次尝试记录渠道、真实上游 URL、上游模型与上游凭据指纹；零信任日志，凭据明文永不落库。
  - title: 模块化插件
    details: sidecar HTTP 协议与可下载安装包两种形态，声明式权限与配置，拦截钩子可参与路由改选。
---

<GatewayFlow />

## 文档结构

| 章节 | 内容 |
| --- | --- |
| [入门](/guide/) | 定位、核心概念、部署与升级 |
| [接入](/clients/) | 下游协议、鉴权、流式语义、图像接口 |
| [上游](/upstream/) | 站点、渠道、基础 URL 规则、协议映射、站点探针 |
| [路由](/routing/) | 路由与成员、选型算法、故障转移、模型归一 |
| [计费](/billing/) | 单价优先级、配额、用量账单 |
| [团队](/team/) | 个人／团队模式、成员、团队码、第三方登录 |
| [控制台](/console/) | 信息架构、主题包、交互约定 |
| [运维](/operations/) | 架构、配置、数据与迁移、备份、告警、排查 |
| [插件](/plugins/) | sidecar 协议、安装、配置与钩子 |
| [参考](/reference/) | 由代码生成的配置、接口、错误码与数据结构全表 |

## 这个站点怎么维护

- 内容源就在仓库的 `docs/` 目录，与代码同一个 PR 评审，不存在第二份副本。
- `docs/reference/` 下的表格由 `tools/docsgen` 从代码生成；代码改了而文档没重新生成，CI 会失败。
- 正文里的内链死链会让构建失败，指向不存在页面的链接进不了主干。
