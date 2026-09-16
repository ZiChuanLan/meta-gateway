# 本地项目对照与本轮改进

日期：2026-09-13。对照的是 `H:/WorkSpace/api` 下的本地源码。本轮抽查图片接口、协议测试、页面状态和路由诊断。

2026-09-14 续作：共享交互、布局与诊断已继续实现，详细约定和验收见 [console-interactions.md](console-interactions.md)。

| 本轮管理界面参考 | 借鉴及 MG 适配 |
| --- | --- |
| New API `web/classic/src/components/table/channels/ChannelsActions.jsx` | 列表管理集中组织批量动作、筛选与紧凑显示。MG 保留原有列表结构，减少行内按钮，明确批量选择对象并改善手机统计栏。 |
| Sub2API `frontend/src/components/admin/account/AccountActionMenu.vue` | 操作集中到浮层，并依据对象能力展示。MG 复用既有动作定义，统一更多/右键入口，增加对象标题、键盘操作和边缘定位。 |
| Octopus `web/src/components/common/PageActions.tsx` | 搜索、视图选项和创建操作由共享组件组织。MG 收拢模型工具，日志筛选一次提交，保留现有 URL、React Query 和页面状态机制。 |
| AxonHub `frontend/src/components/confirm-dialog.tsx` | 危险操作与加载状态集中管理。MG 统一 pending 字段锁定、关闭限制和焦点恢复，继续使用现有 React 组件。 |

没有引入这些项目的账户体系、前端框架或协议改写链。借鉴的是可复用的交互方式，保留 MG 的渠道/成员/分组和透明转发架构。

| 项目 | 参考实现 | 对 Meta Gateway 的价值与处理 |
| --- | --- | --- |
| New API | `relay/channel/openai/image_edit_test.go`、`relay/helper/openai_image_request_test.go` | 图片编辑测试同时核对 multipart 文件、`stream` 和可选字段。MG 已补原图/mask/false/0 字段的保留、multipart 模型别名映射和流式用量测试。 |
| AxonHub | `llm/transformer/openai/image_edit_outbound_test.go`、`llm/transformer/openai/testdata/` | 按请求类型验证真正发出的 HTTP 协议。MG 的请求构造继续把 model 写在文件前；新增回归直接检查上游端点、编码、响应头、错误和账单。 |
| RikkaHub | [OpenAIProvider.kt](../../rikkahub/ai/src/main/java/me/rerere/ai/provider/providers/openai/OpenAIProvider.kt)、[ImgGenVM.kt](../../rikkahub/app/src/main/java/me/rerere/rikkahub/ui/pages/imggen/ImgGenVM.kt)、[离线回放说明](../../rikkahub/ai/src/test/resources/stream-traces/README.md) | 有无参考图决定生成或编辑，解析实际图片类型，保留生成状态，并离线回放协议。MG 已修正自动模式、JPEG/WebP 类型、切换标签后的状态保留，补上传校验和历史图片预览。 |
| Sub2API | `backend/internal/handler/chat_completions_image_model_test.go`、`backend/internal/handler/image_concurrency_limiter.go` | 提交前识别不支持的图片操作，图片请求有独立并发/排队能力。MG 本轮补工作台的操作校验；现有渠道并发门继续适用，独立图片队列列为后续候选。 |
| Octopus | `web/src/components/modules/group/MemberStatus.tsx`、`internal/relay/route.go` | 路由状态能直接解释当前成员、冷却和亲和；倒计时到期停止更新。MG 已修复冷却倒计时到期后仍持续运行的问题；共享时钟和路由状态展示可继续完善。 |

## 本轮已落地

- 收尾聊天图片编辑开关的中英文文案和 `Route.image_edit_shim` 类型。
- 图像工作台自动模式：无参考图生成，有参考图编辑；生成请求默认 JSON，支持 multipart 的编辑上传使用 multipart。
- 聊天编辑兼容入口复用正常转发和计费链路，保留路由分组、成员价格、客户端条件和取消上下文；支持已启用的通配符路由。
- 上游错误保留状态、正文和响应类型；无图片的成功响应作为网关错误返回；SSE 使用正确响应头。
- 用量来自上游 `usage`，支持 `input_tokens` / `output_tokens`；不把图片张数或 Base64 字节数计作 token。
- 图片写请求不因重试配置、凭据刷新或 `Idempotency-Key` 重发。
- 原生 multipart 编辑保留文件与可选字段，识别 `stream=true`，并能改写显式配置的模型别名。
- 工作台增加加载/失败状态、文件类型/数量/总大小检查、运行时表单锁定、标签切换后的结果保留、历史结果查看和受限的历史内存占用。
- 能力注册表可修改输入/输出类型与尺寸；保存后刷新工作台使用的能力缓存，操作失败有可见反馈。
- 工作台成功响应默认只返回一份图片内容；需要原始响应时可传 `include_raw_response: true`。

## 建议继续做的项目

| 优先级 | 改进 | MG 现状与建议 |
| --- | --- | --- |
| 高 | 按渠道记录图片协议差异 | 当前注册表以模型名为键，同一别名若混用不同上游协议，需要把协议规划移到渠道选择之后。先明确实际混用场景，再扩展为渠道覆盖。 |
| 高 | 跨协议的离线回放样例 | 已有确定性的协议回归；可借鉴 RikkaHub/AxonHub，积累脱敏的公开示例或人工测试样例，覆盖分段工具调用、思考签名、图片输出和流尾用量。 |
| 中 | 路由诊断时间线 | MG 已有 live trace、决策快照、冷却和会话粘性，可将这些信息串成一次请求的时间线，减少排查时跨页面查找。 |
| 中 | 图片独立并发与等待队列 | 现有 `ChannelGate` 限制渠道总并发；当长时间出图明显影响聊天时，可增加图片等待上限和超时。 |
| 中 | 冷却列表共享时钟 | 本轮已停止过期计时器；大量模型同时冷却时，可以进一步采用列表级共享时钟。 |

视频统一 API 保留为后续工作。本轮围绕已有 1–7 项和图片编辑完成收尾与优化。

## 部署记录

本地 `.workbuddy/memory/MEMORY.md` 和 `2026-09-13.md` 的「角色对调」段记录：生产应用在 RN `192.129.128.178`，阿里云 `43.108.52.153` 运行 demo；当时 `mg.zichuanlan.top` 经阿里云 Caddy 桥接到 RN，Cloudflare 源站切换列为待办。

本轮 SSH 只读核验进一步确认：阿里云 Caddy 仍保留该桥接，`mg.015201314.xyz` 指向阿里云本机；两台 `/healthz` 都返回 v2.7.3，commit 均为 `72179f3`。旧的升级待办已过时，长期 memory 已更新。Caddy 配置本身不能证明 Cloudflare 当前选择了哪个源站，该 DNS 项仍待单独核对。

## 图片收尾验证结果（2026-09-13）

- 前端：ESLint、TypeScript、18 个测试文件 / 97 个测试、Vite 生产构建通过。
- 后端：`go build ./...`、`go test ./...`、`go vet ./...`、gofmt 与 diff 空白检查通过。
- 生产构建产物已更新到 `internal/webui/dist`。
- 浏览器使用本地模拟 API 验证生成、历史结果切换；1440px 桌面和 390px 手机宽度均无横向溢出、无页面运行错误。
- 本机 `CGO_ENABLED=0`，本轮未运行 race 检测。图片协议回归使用模拟上游。
- 本轮代码改动保留在本地工作区，尚未发布或部署。
