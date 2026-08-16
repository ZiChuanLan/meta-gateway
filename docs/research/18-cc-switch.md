# CC Switch 调研报告 → meta-gateway 借鉴清单（18-cc-switch.md）

> 调研对象：`H:/WorkSpace/api/cc-switch`（Tauri 2 桌面应用，Rust 后端 + React 前端）——Claude Code / Claude Desktop / Codex / Gemini CLI / Grok Build / OpenCode / OpenClaw / Hermes Agent 的统一配置管理器（多供应商配置切换、MCP/Prompts/Skills 管理、代理与故障转移）。
> 与 meta-gateway 的关系：cc-switch 是**客户端侧配置工具**（改 agent 的 settings/env），meta-gateway 是**服务端网关**——两者不直接竞争，但 cc-switch 的"UA 伪装"机制对 meta-gateway 有直接借鉴价值。

## 一、项目定位

- Tauri 桌面应用（Windows/macOS/Linux），管理 8 种 Coding Agent 的供应商配置（ANTHROPIC_BASE_URL/AUTH_TOKEN/env/headers 等），一键切换。
- 自带本地路由/代理接管能力（请求经本地代理转发，替换 UA、headers 等）。
- session-manager.md：会话管理（多会话并行）。

## 二、核心发现：CC 伪装 = 渠道级自定义 User-Agent（实测白名单规则）

### 1. 预设清单（`src/config/userAgentPresets.ts`）

```ts
export const USER_AGENT_PRESETS: readonly string[] = [
  "claude-cli/2.1.161 (external, cli)",  // 官方 Claude Code CLI 完整格式（最稳）
  "claude-cli/2.1.161",
  "claude-code/1.0.0",
  "claude-code/0.1.0",
  "Kilo-Code/1.0",
];
```

### 2. 关键实测结论（来源注释，PR #3671 对 Kimi Coding Plan UA 白名单的 curl 实测）

- 白名单规则：`claude-cli/*`、`claude-code/*`、`Kilo-Code/*` **可通过**；`codex-cli`、`kimi-cli` **被 403**
- **白名单只校验 UA 名称前缀、不看版本号** → 静态值即可，不会因 Claude Code 升级而失效
- 官方完整格式 `claude-cli/2.1.161 (external, cli)` 最贴近真实客户端，最稳过严格校验

### 3. 实现（`src/components/providers/forms/CustomUserAgentField.tsx`）

- 输入框 + 预设下拉（DropdownMenu 套用 USER_AGENT_PRESETS）
- 合法性校验 `isValidUserAgentHeader`（不能含控制字符如换行，否则运行时静默忽略）
- 生效方式：本地路由/代理接管后，**替换转发到供应商 API 请求中的 User-Agent**

### 4. 用途场景

非白名单 Coding Agent（Codex/Gemini/Hermes/OpenClaw 等）想接入受 UA 限制的上游（如 Kimi Coding Plan、部分中转站）——把转发请求伪装成已在白名单内的客户端。**是否使用由用户显式选择。**

## 三、对 meta-gateway 的借鉴价值

### [高价值·半天量级] 渠道级"UA 伪装预设"

- meta-gateway 已有渠道级 `header_override`（可设任意头），**技术上已能做** UA 伪装——但需要手填完整 UA 字符串，且普通用户不知道填什么。
- 借鉴 cc-switch：在渠道编辑表单加一个 **User-Agent 预设下拉**（5 个预设值照抄）+ 自定义输入框，选择后自动写入 header_override 的 `User-Agent` 项（或独立字段）。
- **与 CLIProxyAPI 完整 cloaking 的本质区别**：cc-switch 证明"静态 UA 前缀"即可过白名单（白名单不看版本号）——**不需要 CLIProxyAPI 那种持续抓包维护的 ClaudeHeaderDefaults 基线**。之前我标"cloaking 不适合"的理由（基线会过期）对"UA 预设"不成立。
- 额外收益：meta-gateway 的 `client_family` 识别会把 UA 为 `claude-cli/*` 的请求识别为 claude-code，日志更清晰。

### [可做] 其他可借鉴的小点

| 功能 | cc-switch 实现 | 借鉴价值 |
|---|---|---|
| 多供应商配置模板 | ProviderForm + presets（openclawProviderPresets 等） | meta-gateway 渠道"从模板创建"可借鉴（填 base_url/头/模型前缀） |
| 代理故障转移 | 本地代理 + 故障转移 | meta-gateway 已有完整 failover，无需 |
| MCP/Prompts/Skills 管理 | UnifiedMcpPanel 等 | 客户端侧功能，meta-gateway 不适用 |
| 会话管理 | session-manager.md | meta-gateway 有 sticky session（服务端），不适用 |

## 四、结论

1. **"CC 伪装"的真相**：不是逆向签名/风控对抗，而是 **User-Agent 预设**（Kimi Coding Plan 等上游按 UA 前缀白名单放行）。
2. **meta-gateway 直接可做**：渠道表单加 UA 预设下拉（半天），一行 header_override 写入——解决"Claude Code 客户端想走 UA 受限上游"的实际场景。
3. **修正之前评估**：CLIProxyAPI 的完整 cloaking（system prompt 替换/零宽混淆/版本基线）仍不建议照搬（对抗性、基线维护）；但 cc-switch 的"静态 UA 预设"方案**没有维护负担**，是真正可落地的形态。

## 五、证据清单

- `src/config/userAgentPresets.ts`（预设 + 白名单实测注释）
- `src/components/providers/forms/CustomUserAgentField.tsx`（字段实现）
- `src/lib/userAgent.ts`（isValidUserAgentHeader 校验）
- `src/lib/requestOverrides.ts`、`LocalProxyRequestOverridesField.tsx`（headers 覆盖）
- README/README_ZH.md（项目定位、8 种 agent 支持）
