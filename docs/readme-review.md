# README 与展示素材对标

日期：2026-09-18。对标对象：[vastsa/pi-desktop](https://github.com/vastsa/pi-desktop) 的 README
（它是 32★ 的 MG 里少见的"把 README 当落地页做"的同类项目，且已经积累了 Trendshift / Product Hunt 外部榜单）。

本轮先产出一套 MG 自己的主视觉与功能大图，再逐条对照 README 手法。

---

## 一、pi-desktop 的手法拆解

| 手法 | 具体做法 | MG 现状 |
| --- | --- | --- |
| Hero 结构 | `<div align="center">` 包裹：logo(108px) → `# 标题` → `### 标语` → **加粗价值主张** → 能力标签行（`Local-first · Model-agnostic · …`）→ 徽章行 → `**[Download]** · [Docs] · [Screenshots] · [简体中文]` 导航行 → 主视觉大图 `width="94%"` → "无账号/无锁定" 小字 → 第三方榜单徽章 | 有居中块、徽章、锚点导航行；**主视觉是 1200×340 的纯 SVG，没有产品画面** |
| 明暗自适应 | Star History 用 `<picture>` + `prefers-color-scheme`；主视觉是**内容不同的明暗两张** | 无 |
| 特性区 | `<table>` + `valign="top"` 双列卡片 | 已有 3 列特性矩阵 ✓ |
| 截图区 | `<table>` 2×2，每格 `<img>` + 居中 `<sub>` 说明，末尾 "Explore all screenshots →" | 已有 5×2 十图矩阵 ✓（更全） |
| 折叠 | 兼容性 / 未签名说明 / 本地开发 / 用量统计都收进 `<details>` | 已有 5 处 ✓ |
| 收尾 | Star History 图 → 贡献指南 → 许可 → 页脚 CTA | **缺 Star History、贡献、许可三块** |
| 双语 | 英文主 README + `README.zh-CN.md` | **只有中文** |
| 信任状 | Trendshift / Product Hunt 榜单 | 无（国内项目可换成 Docker Pulls 大字 / Star History） |

结论：MG 的 README **骨架已经不比它差**（特性矩阵、截图矩阵、折叠、Mermaid 架构图、FAQ、AI 部署提示词都在）。
真正落后的是三处：**主视觉没有产品画面、没有明暗自适应、没有收尾块**；再加上仓库 About 元数据是空的。

---

## 二、本次产出的素材

`docs/marketing/`（四个成品，可直接放进 README 或任意落地页）：

| 文件 | 尺寸 | 用途 |
| --- | --- | --- |
| `hero-dark.png` | 1600×900 | 深色主视觉（总览控制台 + 品牌主张） |
| `hero-light.png` | 1600×900 | 浅色主视觉（模型路由 + 品牌主张） |
| `feature-aggregate.png` | 1600×1000 | 功能图 01：多通道聚合与故障自愈 |
| `feature-protocol.png` | 1600×1000 | 功能图 02：一个接口，三种协议 |

它们**不是纯 AI 生图**。文字与界面若交给图像模型渲染必然糊成一片，所以做法是：

1. `_art/bg-*.png` — 用图像模型生成**只有氛围、不含文字**的底图（品牌深蓝极光 / 浅色网格 / 光纤束流）；
2. `_compose/*.html` — 真实中文排版 + `docs/screenshots/*.png` 里**真实控制台界面** + 真实代码片段，叠在底图上；
3. `_compose/render.sh` — 无头浏览器按精确视口渲染为 PNG（一条命令可重出）。

所以图里每一个字都是真字、每一处界面都是真界面，后续改文案只需改 HTML 重跑。

> 复现：`bash docs/marketing/_compose/render.sh docs/marketing/_compose/hero-dark.html 1600 900 "H:/…/docs/marketing/hero-dark.png"`

---

## 三、可直接粘贴的 Hero 片段（替换现有 `docs/banner.svg` 那一行）

```html
<picture>
  <source media="(prefers-color-scheme: dark)" srcset="docs/marketing/hero-dark.png">
  <source media="(prefers-color-scheme: light)" srcset="docs/marketing/hero-light.png">
  <img src="docs/marketing/hero-light.png" alt="Meta Gateway — 多通道 AI 中继网关" width="100%">
</picture>
```

GitHub 会按访问者的系统偏好自动选图，与 pi-desktop 的做法一致。

---

## 四、待办清单（按性价比排序）

### A 级 · 十分钟见效

1. **补仓库 About**：`homepage` 目前是 `None`、`topics` 是**空数组**。README 里推的在线演示
   `https://mg.015201314.xyz` 没写进仓库主页，GitHub 搜索与话题页也完全吃不到流量。
   → 设 homepage，topics 补 `ai-gateway` `llm` `openai` `anthropic` `gemini` `api-gateway`
   `self-hosted` `golang`。
2. **补 CI 徽章**：`.github/workflows/ci.yml` 一直在跑，但 README 没有它的状态徽章
   （pi-desktop 有）。一行即可。
3. **Hero 换成带产品画面的图**：见上面的 `<picture>` 片段。

### B 级 · 内容层

4. **英文 README**：现在只有中文，海外流量全部流失。建议 `README.md` 保中文、
   新增 `README.en.md`，两边顶部互链（pi-desktop 就是这么做的）。
5. **截图补齐并重生成**：`docs/screenshots/` 缺 **logs 页**——而日志/延迟分布是 v3.1.0 的主打；
   也缺设置页（外观切换的入口）。且现有 10 张拍于 09-13/09-16，早于 v3.1.0 的界面改动。
6. **补一张"两套外观"对比图**：README 文字里讲了经典/现代，却没有任何视觉证据。
   一张并排图最划算（`设置 → 外观` 一键切换是本项目少见的差异化）。
7. **补收尾三块**：Star History（`<picture>` 明暗两版）、`## 贡献`、`## 许可`（MIT）。

### C 级 · 工程化

8. **截图脚本化 + 瘦身**：10 张 1600×1000 PNG 共 2.1MB，其中 `login.png` 单张 **1.19MB**。
   → 写一条 `scripts/screenshots.sh`（agent-browser 单链登录 + 逐页截图，与本次 render 同套路），
   并统一转 webp（pi-desktop 主视觉就是 `.webp`）。
9. **演示数据去测试味**：截图里的 `中转A / 中转B / 中转C`、`http://127.0.0.1:4567`
   是对外素材里最"像测试环境"的两处，换成接近真实站点的命名会顺眼很多。
10. **本次底图入库前压一下**：`_art/bg-*.png` 每张约 1.6MB，转 JPEG 后入库即可，
    成品四张 PNG 视需要再决定是否转 webp。

---

## 五、控制台本身（看截图时发现的实证问题）

| 位置 | 现象 | 建议 |
| --- | --- | --- |
| 工作台 →「图像」 | 初始是空表单 +「还没有结果」，作为对外截图显得未完成 | 空态给一句示例提示，或预置一个示例结果 |
| 总览 → 24 小时流量 | 演示数据下几乎空白，只有右侧一根尖峰 | demo 环境灌一点历史数据，曲线才有说服力 |
| 连接 / 模型列表 | `中转A·B·C` + `127.0.0.1:4567` | 见 C-9 |
